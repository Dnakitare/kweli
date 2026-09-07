package smart

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// assertionLifetime is how long the signed client assertion is valid for.
// SMART Backend Services servers commonly reject assertions with a longer
// lifetime; 5 minutes is the conventional safe value (also what HL7's
// reference implementations use) and is far shorter than a single kweli
// run's --budget, so there's no reason to push it.
const assertionLifetime = 5 * time.Minute

// BuildAssertion builds and signs a private_key_jwt client assertion for
// the SMART Backend Services client_credentials flow: iss/sub identify
// the registered client, aud is the token endpoint being asked to trust
// this assertion, and jti/exp keep it single-use and short-lived.
func BuildAssertion(key *SigningKey, clientID, tokenURL string) (string, error) {
	if key == nil {
		return "", fmt.Errorf("no signing key")
	}
	jti, err := randomJTI()
	if err != nil {
		return "", fmt.Errorf("generating jti: %w", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": clientID,
		"sub": clientID,
		"aud": tokenURL,
		"jti": jti,
		"iat": now.Unix(),
		"exp": now.Add(assertionLifetime).Unix(),
	}

	method := jwt.GetSigningMethod(key.Alg)
	if method == nil {
		return "", fmt.Errorf("unsupported signing algorithm %q", key.Alg)
	}
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = key.Kid

	signed, err := token.SignedString(key.Key)
	if err != nil {
		return "", fmt.Errorf("signing client assertion: %w", err)
	}
	return signed, nil
}

func randomJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
