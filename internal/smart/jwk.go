// Package smart implements SMART Backend Services authentication (brief
// Phase 2): loading a private JWK, signing a private_key_jwt client
// assertion (RS384/ES384), discovering the token endpoint, exchanging the
// assertion for an access token, and caching it across a run.
//
// No JWK-parsing library dependency: the brief's allowed-dependency list
// doesn't include one, and the JWK subset SMART Backend Services actually
// needs (RSA or EC private keys) is small enough to parse directly against
// RFC 7517/7518 with the stdlib.
package smart

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

// SigningKey is the key material and metadata needed to sign a
// private_key_jwt client assertion.
type SigningKey struct {
	// Key is *rsa.PrivateKey or *ecdsa.PrivateKey — the concrete type
	// golang-jwt's RS384/ES384 signing methods expect.
	Key any
	// Alg is "RS384" or "ES384", per SMART Backend Services' required
	// algorithm list (it explicitly excludes RS256/ES256 and HMAC).
	Alg string
	// Kid is the JWK's "kid", required in the assertion's JOSE header so
	// the server can pick the right key out of a registered JWK Set.
	Kid string
}

// rawJWK is the subset of RFC 7517/7518 fields kweli reads. The same "d"
// field name is reused by both RSA and EC private keys (different
// semantics, same JSON key) — that's the JWK spec, not a kweli shortcut.
type rawJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`

	// RSA
	N string `json:"n"`
	E string `json:"e"`
	D string `json:"d"`
	P string `json:"p"`
	Q string `json:"q"`

	// EC
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type rawJWKSet struct {
	Keys []rawJWK `json:"keys"`
}

// LoadKey reads a private JWK from path — either a bare JWK object or a
// JWK Set ({"keys":[...]})  — and returns the first key that has private
// key material (a "d" member). Most JWK generators for SMART testing hand
// you a set with both a public and private view, or a set with one
// private key; kweli wants the one it can actually sign with.
func LoadKey(path string) (*SigningKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading JWK file %s: %w", path, err)
	}

	candidates, err := parseCandidates(data)
	if err != nil {
		return nil, fmt.Errorf("parsing JWK file %s: %w", path, err)
	}

	for _, k := range candidates {
		if k.D == "" {
			continue // public-only entry; skip
		}
		return signingKeyFromJWK(k)
	}
	return nil, fmt.Errorf("JWK file %s has no private key (no member has a \"d\" value) — kweli needs the private JWK, not just the public one registered with the server", path)
}

func parseCandidates(data []byte) ([]rawJWK, error) {
	var set rawJWKSet
	if err := json.Unmarshal(data, &set); err == nil && len(set.Keys) > 0 {
		return set.Keys, nil
	}
	var single rawJWK
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	if single.Kty == "" {
		return nil, fmt.Errorf("no \"kty\" field found — not a JWK or JWK Set")
	}
	return []rawJWK{single}, nil
}

func signingKeyFromJWK(k rawJWK) (*SigningKey, error) {
	if k.Kid == "" {
		return nil, fmt.Errorf("JWK has no \"kid\" — SMART Backend Services requires one so the server can select this key out of your registered JWK Set")
	}

	switch k.Kty {
	case "RSA":
		key, err := rsaPrivateKeyFromJWK(k)
		if err != nil {
			return nil, err
		}
		alg := k.Alg
		if alg == "" {
			alg = "RS384" // SMART Backend Services' RSA default
		}
		if alg != "RS384" && alg != "RS256" && alg != "RS512" {
			return nil, fmt.Errorf("JWK alg %q is not an RSA JWS algorithm kweli signs with", alg)
		}
		if alg != "RS384" {
			// SMART Backend Services requires RS384 or ES384 specifically
			// (not RS256) — warn by erroring rather than silently signing
			// with an algorithm the server may reject.
			return nil, fmt.Errorf("JWK alg %q is not RS384 — SMART Backend Services requires RS384 for RSA keys; regenerate the JWK with \"alg\":\"RS384\"", alg)
		}
		return &SigningKey{Key: key, Alg: "RS384", Kid: k.Kid}, nil

	case "EC":
		key, err := ecPrivateKeyFromJWK(k)
		if err != nil {
			return nil, err
		}
		alg := k.Alg
		if alg == "" {
			alg = "ES384"
		}
		if alg != "ES384" {
			return nil, fmt.Errorf("JWK alg %q is not ES384 — SMART Backend Services requires ES384 for EC keys; regenerate the JWK with \"alg\":\"ES384\" and \"crv\":\"P-384\"", alg)
		}
		if k.Crv != "P-384" {
			return nil, fmt.Errorf("JWK crv %q is not P-384 — ES384 requires a P-384 key", k.Crv)
		}
		return &SigningKey{Key: key, Alg: "ES384", Kid: k.Kid}, nil

	default:
		return nil, fmt.Errorf("JWK kty %q is not RSA or EC — SMART Backend Services only accepts those", k.Kty)
	}
}

func b64ToInt(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func rsaPrivateKeyFromJWK(k rawJWK) (*rsa.PrivateKey, error) {
	n, err := b64ToInt(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA \"n\": %w", err)
	}
	e, err := b64ToInt(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA \"e\": %w", err)
	}
	d, err := b64ToInt(k.D)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA \"d\": %w", err)
	}

	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: n, E: int(e.Int64())},
		D:         d,
	}
	if k.P != "" && k.Q != "" {
		p, err := b64ToInt(k.P)
		if err != nil {
			return nil, fmt.Errorf("decoding RSA \"p\": %w", err)
		}
		q, err := b64ToInt(k.Q)
		if err != nil {
			return nil, fmt.Errorf("decoding RSA \"q\": %w", err)
		}
		key.Primes = []*big.Int{p, q}
	} else {
		return nil, fmt.Errorf("RSA JWK is missing \"p\"/\"q\" — kweli needs the full private key, not just d/n/e")
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("RSA key from JWK failed validation: %w", err)
	}
	key.Precompute()
	return key, nil
}

func ecPrivateKeyFromJWK(k rawJWK) (*ecdsa.PrivateKey, error) {
	var curve elliptic.Curve
	var ecdhCurve ecdh.Curve
	switch k.Crv {
	case "P-256":
		curve, ecdhCurve = elliptic.P256(), ecdh.P256()
	case "P-384":
		curve, ecdhCurve = elliptic.P384(), ecdh.P384()
	case "P-521":
		curve, ecdhCurve = elliptic.P521(), ecdh.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve %q", k.Crv)
	}

	x, err := b64ToInt(k.X)
	if err != nil {
		return nil, fmt.Errorf("decoding EC \"x\": %w", err)
	}
	y, err := b64ToInt(k.Y)
	if err != nil {
		return nil, fmt.Errorf("decoding EC \"y\": %w", err)
	}
	d, err := b64ToInt(k.D)
	if err != nil {
		return nil, fmt.Errorf("decoding EC \"d\": %w", err)
	}

	// Validate d against x/y via crypto/ecdh rather than the deprecated
	// elliptic.Curve.IsOnCurve: derive the public point from the private
	// scalar and confirm it matches what the JWK claims. This catches a
	// mismatched/corrupted JWK (wrong x/y for this d) rather than
	// silently producing a key whose signatures no verifier will accept.
	size := (curve.Params().BitSize + 7) / 8
	dBytes := make([]byte, size)
	d.FillBytes(dBytes)
	ecdhPriv, err := ecdhCurve.NewPrivateKey(dBytes)
	if err != nil {
		return nil, fmt.Errorf("EC private scalar \"d\" from JWK is invalid for %s: %w", k.Crv, err)
	}
	wantPub := ecdhPriv.PublicKey().Bytes() // uncompressed point: 0x04 || X || Y
	gotPub := make([]byte, 1+2*size)
	gotPub[0] = 0x04
	x.FillBytes(gotPub[1 : 1+size])
	y.FillBytes(gotPub[1+size:])
	if !bytes.Equal(wantPub, gotPub) {
		return nil, fmt.Errorf("EC key from JWK is inconsistent: the public point derived from \"d\" doesn't match \"x\"/\"y\"")
	}

	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         d,
	}, nil
}
