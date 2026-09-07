package smart

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func b64(i *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(i.Bytes())
}

// writeRSAJWK generates a fresh RSA key and writes it as a private JWK
// file, returning the path and the key (so tests can verify signatures
// against the matching public key).
func writeRSAJWK(t *testing.T, dir, alg string) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty": "RSA",
		"kid": "test-rsa-key",
		"alg": alg,
		"n":   b64(key.N),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		"d":   b64(key.D),
		"p":   b64(key.Primes[0]),
		"q":   b64(key.Primes[1]),
	}

	path := filepath.Join(dir, "rsa.jwk.json")
	writeJSON(t, path, jwk)
	return path, key
}

func writeECJWK(t *testing.T, dir string) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty": "EC",
		"kid": "test-ec-key",
		"alg": "ES384",
		"crv": "P-384",
		"x":   b64(key.X),
		"y":   b64(key.Y),
		"d":   b64(key.D),
	}
	path := filepath.Join(dir, "ec.jwk.json")
	writeJSON(t, path, jwk)
	return path, key
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadKey_RSA(t *testing.T) {
	dir := t.TempDir()
	path, want := writeRSAJWK(t, dir, "RS384")

	sk, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if sk.Alg != "RS384" {
		t.Errorf("Alg = %q, want RS384", sk.Alg)
	}
	if sk.Kid != "test-rsa-key" {
		t.Errorf("Kid = %q, want test-rsa-key", sk.Kid)
	}
	got, ok := sk.Key.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("Key type = %T, want *rsa.PrivateKey", sk.Key)
	}
	if got.N.Cmp(want.N) != 0 || got.D.Cmp(want.D) != 0 {
		t.Error("loaded RSA key doesn't match the source key material")
	}
}

func TestLoadKey_RSA_DefaultsAlgToRS384(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty": "RSA", "kid": "k1",
		"n": b64(key.N), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		"d": b64(key.D), "p": b64(key.Primes[0]), "q": b64(key.Primes[1]),
	}
	path := filepath.Join(dir, "k.json")
	writeJSON(t, path, jwk)

	sk, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if sk.Alg != "RS384" {
		t.Errorf("Alg = %q, want default RS384 when JWK omits alg", sk.Alg)
	}
}

func TestLoadKey_RSA_RejectsRS256(t *testing.T) {
	dir := t.TempDir()
	path, _ := writeRSAJWK(t, dir, "RS256")
	if _, err := LoadKey(path); err == nil {
		t.Error("expected an error for alg RS256 — SMART Backend Services requires RS384")
	}
}

func TestLoadKey_EC(t *testing.T) {
	dir := t.TempDir()
	path, want := writeECJWK(t, dir)

	sk, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if sk.Alg != "ES384" {
		t.Errorf("Alg = %q, want ES384", sk.Alg)
	}
	got, ok := sk.Key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("Key type = %T, want *ecdsa.PrivateKey", sk.Key)
	}
	if got.D.Cmp(want.D) != 0 {
		t.Error("loaded EC key doesn't match the source key material")
	}
}

func TestLoadKey_EC_MismatchedXYRejected(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty": "EC", "kid": "k1", "alg": "ES384", "crv": "P-384",
		"x": b64(other.X), "y": b64(other.Y), // x/y from a DIFFERENT key than d
		"d": b64(key.D),
	}
	path := filepath.Join(dir, "bad.json")
	writeJSON(t, path, jwk)

	if _, err := LoadKey(path); err == nil {
		t.Error("expected an error for a JWK whose x/y don't correspond to d")
	}
}

func TestLoadKey_NoPrivateKeyErrors(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// Public-only JWK (no "d").
	jwk := map[string]any{
		"kty": "RSA", "kid": "k1",
		"n": b64(key.N), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
	path := filepath.Join(dir, "pub.json")
	writeJSON(t, path, jwk)

	if _, err := LoadKey(path); err == nil {
		t.Error("expected an error loading a public-only JWK")
	}
}

func TestLoadKey_JWKSetPicksThePrivateKey(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	eB64 := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	set := map[string]any{
		"keys": []map[string]any{
			{"kty": "RSA", "kid": "pub-only", "n": b64(key.N), "e": eB64}, // no d
			{"kty": "RSA", "kid": "priv", "alg": "RS384", "n": b64(key.N), "e": eB64,
				"d": b64(key.D), "p": b64(key.Primes[0]), "q": b64(key.Primes[1])},
		},
	}
	path := filepath.Join(dir, "set.json")
	writeJSON(t, path, set)

	sk, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if sk.Kid != "priv" {
		t.Errorf("Kid = %q, want the entry that actually has a \"d\"", sk.Kid)
	}
}

func TestLoadKey_MissingFile(t *testing.T) {
	if _, err := LoadKey("/nonexistent/path.json"); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestLoadKey_NotJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(path); err == nil {
		t.Error("expected an error for a non-JSON file")
	}
}
