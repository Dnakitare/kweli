package smart

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestBuildAssertion_ValidRS384(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "kid-1"}

	assertion, err := BuildAssertion(key, "my-client", "https://example.org/token")
	if err != nil {
		t.Fatalf("BuildAssertion: %v", err)
	}

	parsed, err := jwt.Parse(assertion, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			t.Fatalf("unexpected signing method %T", tok.Method)
		}
		if tok.Header["kid"] != "kid-1" {
			t.Errorf("kid header = %v, want kid-1", tok.Header["kid"])
		}
		return &priv.PublicKey, nil
	})
	if err != nil {
		t.Fatalf("assertion did not verify against its own public key: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims are not MapClaims")
	}
	for _, k := range []string{"iss", "sub", "aud", "jti", "exp", "iat"} {
		if _, ok := claims[k]; !ok {
			t.Errorf("claims missing %q: %v", k, claims)
		}
	}
	if claims["iss"] != "my-client" || claims["sub"] != "my-client" {
		t.Errorf("iss/sub = %v/%v, want my-client/my-client", claims["iss"], claims["sub"])
	}
	if claims["aud"] != "https://example.org/token" {
		t.Errorf("aud = %v, want the token URL", claims["aud"])
	}
}

func TestBuildAssertion_ExpiryIsShortLived(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "kid-1"}

	before := time.Now()
	assertion, err := BuildAssertion(key, "c", "https://example.org/token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(assertion, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	expF, _ := claims["exp"].(float64)
	exp := time.Unix(int64(expF), 0)
	if exp.After(before.Add(10 * time.Minute)) {
		t.Errorf("exp = %v, expected a short-lived assertion (a handful of minutes out), not something far in the future", exp)
	}
	if exp.Before(before) {
		t.Errorf("exp = %v is already in the past", exp)
	}
}

func TestBuildAssertion_JTIIsUniquePerCall(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "kid-1"}

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		assertion, err := BuildAssertion(key, "c", "https://example.org/token")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _, err := jwt.NewParser().ParseUnverified(assertion, jwt.MapClaims{})
		if err != nil {
			t.Fatal(err)
		}
		jti, _ := parsed.Claims.(jwt.MapClaims)["jti"].(string)
		if jti == "" {
			t.Fatal("jti is empty")
		}
		if seen[jti] {
			t.Fatalf("jti %q reused across calls — must be unique per assertion", jti)
		}
		seen[jti] = true
	}
}

func TestBuildAssertion_WrongKeyFailsVerification(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := &SigningKey{Key: priv, Alg: "RS384", Kid: "kid-1"}

	assertion, err := BuildAssertion(key, "c", "https://example.org/token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = jwt.Parse(assertion, func(tok *jwt.Token) (any, error) {
		return &other.PublicKey, nil // wrong public key
	})
	if err == nil {
		t.Error("assertion verified against the wrong public key — signature check is broken")
	}
}
