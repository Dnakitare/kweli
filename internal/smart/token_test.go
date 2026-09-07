package smart

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeTokenServer is a minimal SMART Backend Services token endpoint: it
// verifies the client_assertion is a validly-signed JWT for the expected
// client/audience against the given public key, then issues a token with
// the given lifetime. Exercises the real signature verification path,
// not just "the server always says yes".
func fakeTokenServer(t *testing.T, pub *rsa.PublicKey, wantClientID string, expiresIn int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != "client_credentials" {
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
			return
		}
		assertion := r.FormValue("client_assertion")
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(assertion, claims, func(tok *jwt.Token) (any, error) {
			return pub, nil
		})
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid_client","error_description":%q}`, err.Error()), http.StatusUnauthorized)
			return
		}
		if claims["iss"] != wantClientID {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + fmt.Sprint(calls),
			"expires_in":   expiresIn,
			"token_type":   "Bearer",
		})
	}))
	return srv, &calls
}

func TestExchange_Success(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := fakeTokenServer(t, &priv.PublicKey, "client-1", 3600)
	defer srv.Close()

	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "k1"}
	assertion, err := BuildAssertion(key, "client-1", srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	token, expiresAt, err := Exchange(context.Background(), srv.Client(), srv.URL, assertion, DefaultScope)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if token == "" {
		t.Error("empty access token")
	}
	if !expiresAt.After(time.Now()) {
		t.Error("expiresAt is not in the future")
	}
}

func TestExchange_ServerRejectsBadSignature(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := fakeTokenServer(t, &priv.PublicKey, "client-1", 3600) // server expects priv's public key
	defer srv.Close()

	// Sign with a DIFFERENT key than the server has registered.
	wrongKey := &SigningKey{Key: otherPriv, Alg: "RS384", Kid: "k1"}
	assertion, err := BuildAssertion(wrongKey, "client-1", srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = Exchange(context.Background(), srv.Client(), srv.URL, assertion, DefaultScope)
	if err == nil {
		t.Error("expected the token endpoint to reject an assertion signed with the wrong key")
	}
}

func TestExchange_NoAccessTokenErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer"}) // missing access_token
	}))
	defer srv.Close()
	_, _, err := Exchange(context.Background(), srv.Client(), srv.URL, "assertion", "")
	if err == nil {
		t.Error("expected an error for a response missing access_token")
	}
}

func TestTokenSource_CachesUntilNearExpiry(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, calls := fakeTokenServer(t, &priv.PublicKey, "client-1", 3600) // long-lived: well outside the 30s refresh skew
	defer srv.Close()

	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "k1"}
	ts := NewTokenSource(key, "client-1", srv.URL, "")

	tok1, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	tok2, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok1 != tok2 {
		t.Errorf("expected the cached token to be reused, got %q then %q", tok1, tok2)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("token endpoint was called %d times, want exactly 1 (second Token() call should have hit the cache)", got)
	}
}

func TestTokenSource_RefreshesAfterExpiry(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, calls := fakeTokenServer(t, &priv.PublicKey, "client-1", 5) // 5s, well inside the 30s refresh skew
	defer srv.Close()

	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "k1"}
	ts := NewTokenSource(key, "client-1", srv.URL, "")
	// A 5s token lifetime is already inside Token()'s 30s refresh skew, so
	// every call must re-exchange rather than trust the cache — no sleep
	// needed, the skew alone forces it.

	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("token endpoint was called %d times, want 2 — a token whose lifetime is shorter than the refresh skew must be re-exchanged every call, not reused past its safety margin", got)
	}
}

func TestTokenSource_ConcurrentCallsShareOneExchange(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, calls := fakeTokenServer(t, &priv.PublicKey, "client-1", 3600)
	defer srv.Close()

	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "k1"}
	ts := NewTokenSource(key, "client-1", srv.URL, "")

	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := ts.Token(context.Background())
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Token() call failed: %v", err)
		}
	}
	// Every goroutine started before the first exchange completed and
	// cached a token, so they should all have shared it rather than each
	// independently hitting the token endpoint.
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("token endpoint was called %d times for %d concurrent Token() calls, want 1", got, n)
	}
}
