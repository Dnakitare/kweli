package smart_test

// End-to-end acceptance test for brief Phase 2: discover the token
// endpoint, build and sign a private_key_jwt client assertion, exchange
// it for an access token, and use that token to make an authenticated
// FHIR request — against internal/testserver's self-contained SMART
// fixture (the deterministic, always-available CI counterpart to the
// real, hand-run validation against SMART's public bulk-data server).

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/smart"
	"github.com/Dnakitare/kweli/internal/testserver"
)

func writeTestJWK(t *testing.T, priv *rsa.PrivateKey, kid string) string {
	t.Helper()
	jwk := map[string]any{
		"kty": "RSA",
		"kid": kid,
		"alg": "RS384",
		"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
		"d":   base64.RawURLEncoding.EncodeToString(priv.D.Bytes()),
		"p":   base64.RawURLEncoding.EncodeToString(priv.Primes[0].Bytes()),
		"q":   base64.RawURLEncoding.EncodeToString(priv.Primes[1].Bytes()),
	}
	b, err := json.Marshal(jwk)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.jwk.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAcceptance_SMARTBackendServicesEndToEnd(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "kweli-test-client"
	srv, stats := testserver.NewSMARTProtected(&priv.PublicKey, clientID, time.Hour) // long-lived: this test is about caching
	defer srv.Close()

	jwkPath := writeTestJWK(t, priv, "test-kid")
	key, err := smart.LoadKey(jwkPath)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}

	ctx := context.Background()

	// Step 1: fetch /metadata unauthenticated, like kweli's real bootstrap.
	bootstrapClient := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	resp, err := bootstrapClient.Get(ctx, "metadata")
	if err != nil {
		t.Fatalf("fetching metadata: %v", err)
	}
	cs, err := capstmt.Parse(bytes.NewReader(resp.Body))
	if err != nil {
		t.Fatalf("parsing CapabilityStatement: %v", err)
	}

	// Step 2: discover the token endpoint (this fixture supports both
	// .well-known and the CapabilityStatement extension — either path
	// should resolve to the same URL).
	tokenURL, err := smart.DiscoverTokenURL(ctx, bootstrapClient, cs, srv.URL, "")
	if err != nil {
		t.Fatalf("DiscoverTokenURL: %v", err)
	}
	if tokenURL != srv.URL+"/token" {
		t.Fatalf("tokenURL = %q, want %s/token", tokenURL, srv.URL)
	}

	// Step 3: build a TokenSource and an authenticated client, exactly
	// how cmd/kweli wires it.
	ts := smart.NewTokenSource(key, clientID, tokenURL, "")
	authedClient := client.New(client.Config{
		BaseURL:     srv.URL,
		TokenSource: ts.Token,
		RPS:         1000,
	})

	// Step 4: an authenticated FHIR request should succeed.
	patResp, err := authedClient.Get(ctx, "Patient")
	if err != nil {
		t.Fatalf("Get Patient: %v", err)
	}
	if patResp.StatusCode != 200 {
		t.Fatalf("Patient search status = %d, want 200 (body: %s)", patResp.StatusCode, patResp.Body)
	}
	if !bytes.Contains(patResp.Body, []byte("patient-1")) {
		t.Errorf("response body missing expected entry: %s", patResp.Body)
	}

	// Step 5: a second authenticated request within the token's lifetime
	// must reuse the cached token, not re-exchange.
	if _, err := authedClient.Get(ctx, "Patient"); err != nil {
		t.Fatalf("second Get Patient: %v", err)
	}
	if got := stats.TokenCalls(); got != 1 {
		t.Errorf("token endpoint called %d times for 2 FHIR requests within one token's lifetime, want 1 (TokenSource should have cached)", got)
	}
}

func TestAcceptance_UnauthenticatedRequestIsRejected(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := testserver.NewSMARTProtected(&priv.PublicKey, "kweli-test-client", time.Hour)
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000}) // no token at all
	resp, err := cl.Get(context.Background(), "Patient")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401 for an unauthenticated request to a SMART-protected server", resp.StatusCode)
	}
}

func TestAcceptance_WrongKeyIsRejectedByTheServer(t *testing.T) {
	registered, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	unregistered, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "kweli-test-client"
	srv, _ := testserver.NewSMARTProtected(&registered.PublicKey, clientID, time.Hour)
	defer srv.Close()

	jwkPath := writeTestJWK(t, unregistered, "test-kid") // signed with the WRONG key
	key, err := smart.LoadKey(jwkPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := smart.NewTokenSource(key, clientID, srv.URL+"/token", "")

	_, err = ts.Token(context.Background())
	if err == nil {
		t.Error("expected token exchange to fail when signed with a key the server doesn't recognize")
	}
}

func TestAcceptance_TokenRefreshesAfterExpiryAcrossRealRequests(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "kweli-test-client"
	srv, stats := testserver.NewSMARTProtected(&priv.PublicKey, clientID, 2*time.Second) // shorter than the 30s refresh skew: forces a refresh every call
	defer srv.Close()

	jwkPath := writeTestJWK(t, priv, "test-kid")
	key, err := smart.LoadKey(jwkPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := smart.NewTokenSource(key, clientID, srv.URL+"/token", "")
	cl := client.New(client.Config{BaseURL: srv.URL, TokenSource: ts.Token, RPS: 1000})

	ctx := context.Background()
	if _, err := cl.Get(ctx, "Patient"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// The fixture's tokens live 2s; TokenSource refreshes 30s before
	// expiry, so a real-world clock always forces a refresh here — no
	// sleep needed, same reasoning as the unit-level refresh test.
	if _, err := cl.Get(ctx, "Patient"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := stats.TokenCalls(); got != 2 {
		t.Errorf("token endpoint called %d times, want 2 (a short-lived token must be refreshed, not reused past its safety margin)", got)
	}
}
