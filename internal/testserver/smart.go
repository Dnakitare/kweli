package testserver

// A self-contained SMART Backend Services fixture: a FHIR server that
// requires a client_credentials + private_key_jwt access token on every
// resource request. Deterministic and dependency-free, so kweli's Phase 2
// auth flow (internal/smart) has an always-available CI counterpart to
// the real, live validation done by hand against SMART's public
// bulk-data server (brief Phase 2: "Test against SMART's public
// bulk-data server in auth mode").

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type smartServer struct {
	pub      *rsa.PublicKey
	clientID string
	tokenTTL time.Duration
	mu       sync.Mutex
	tokens   map[string]time.Time // issued access token -> expiry
	calls    int                  // /token hits, valid or not
}

// SMARTStats exposes counters from a NewSMARTProtected fixture — tests
// use TokenCalls to confirm kweli's TokenSource actually caches (i.e.
// doesn't re-exchange for every FHIR request in a run).
type SMARTStats struct{ s *smartServer }

// TokenCalls returns how many times /token has been hit so far.
func (st SMARTStats) TokenCalls() int {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	return st.s.calls
}

// NewSMARTProtected returns an httptest.Server requiring a valid SMART
// Backend Services access token on every FHIR request:
//   - GET /metadata (public) — declares the oauth-uris security
//     extension pointing at this server's own /token endpoint.
//   - GET /.well-known/smart-configuration (public) — same token_endpoint,
//     via the modern discovery mechanism.
//   - POST /token — verifies a private_key_jwt client assertion (RS384,
//     matching iss/sub/aud/kid, signed by pub) for clientID, and issues a
//     short-lived opaque bearer token.
//   - GET /Patient — 401 without a valid, unexpired token from /token;
//     200 with a small Bundle otherwise.
//
// tokenTTL controls how long an issued access token lives — pass
// something well above kweli's TokenSource refresh skew (30s) to test
// caching, or well below it to force a refresh on every call.
func NewSMARTProtected(pub *rsa.PublicKey, clientID string, tokenTTL time.Duration) (*httptest.Server, SMARTStats) {
	s := &smartServer{
		pub:      pub,
		clientID: clientID,
		tokenTTL: tokenTTL,
		tokens:   map[string]time.Time{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata", s.handleMetadata)
	mux.HandleFunc("/.well-known/smart-configuration", s.handleWellKnown)
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/Patient", s.handlePatient)
	return httptest.NewServer(mux), SMARTStats{s}
}

func (s *smartServer) selfURL(r *http.Request) string {
	return "http://" + r.Host
}

func (s *smartServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.selfURL(r)
	cs := map[string]any{
		"resourceType": "CapabilityStatement",
		"fhirVersion":  "4.0.1",
		"software":     map[string]any{"name": "kweli-smart-testserver", "version": "1.0.0"},
		"rest": []any{
			map[string]any{
				"mode": "server",
				"security": map[string]any{
					"extension": []any{
						map[string]any{
							"url": smartOAuthURIsExtensionForFixture,
							"extension": []any{
								map[string]any{"url": "token", "valueUri": base + "/token"},
							},
						},
					},
				},
				"resource": []any{
					map[string]any{
						"type":        "Patient",
						"interaction": []any{map[string]any{"code": "search-type"}},
					},
				},
			},
		},
	}
	writeJSONSmart(w, http.StatusOK, cs)
}

// smartOAuthURIsExtensionForFixture mirrors internal/smart's own constant
// (kept as a separate literal here, not an import, so this fixture stays
// import-cycle-free and self-contained — internal/smart is what's under
// test, it shouldn't also be a dependency of the thing testing it).
const smartOAuthURIsExtensionForFixture = "http://fhir-registry.smarthealthit.org/StructureDefinition/oauth-uris"

func (s *smartServer) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	writeJSONSmart(w, http.StatusOK, map[string]any{
		"token_endpoint": s.selfURL(r) + "/token",
	})
}

func (s *smartServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.FormValue("grant_type") != "client_credentials" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if r.FormValue("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	assertion := r.FormValue("client_assertion")
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(assertion, claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", tok.Header["alg"])
		}
		return s.pub, nil
	})
	if err != nil || !tok.Valid {
		s.mu.Lock()
		s.calls++
		s.mu.Unlock()
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	iss, _ := claims["iss"].(string)
	sub, _ := claims["sub"].(string)
	aud, _ := claims["aud"].(string)
	if iss != s.clientID || sub != s.clientID {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if aud != s.selfURL(r)+"/token" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}

	s.mu.Lock()
	s.calls++
	accessToken := "kweli-test-access-" + randomHex()
	s.tokens[accessToken] = time.Now().Add(s.tokenTTL)
	s.mu.Unlock()

	writeJSONSmart(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(s.tokenTTL.Seconds()),
		"scope":        r.FormValue("scope"),
	})
}

func (s *smartServer) handlePatient(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == auth || token == "" { // no "Bearer " prefix found, or empty
		writeOutcomeSmart(w, http.StatusUnauthorized, "login", "missing bearer token")
		return
	}
	s.mu.Lock()
	expiry, ok := s.tokens[token]
	s.mu.Unlock()
	if !ok || time.Now().After(expiry) {
		writeOutcomeSmart(w, http.StatusUnauthorized, "login", "invalid or expired token")
		return
	}
	writeJSONSmart(w, http.StatusOK, map[string]any{
		"resourceType": "Bundle",
		"type":         "searchset",
		"total":        1,
		"entry": []any{
			map[string]any{
				"fullUrl":  "urn:patient-1",
				"resource": map[string]any{"resourceType": "Patient", "id": "patient-1"},
				"search":   map[string]any{"mode": "match"},
			},
		},
	})
}

func randomHex() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSONSmart(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	writeJSONSmart(w, status, map[string]any{"error": code})
}

func writeOutcomeSmart(w http.ResponseWriter, status int, code, diagnostics string) {
	writeJSONSmart(w, status, map[string]any{
		"resourceType": "OperationOutcome",
		"issue":        []any{map[string]any{"severity": "error", "code": code, "diagnostics": diagnostics}},
	})
}
