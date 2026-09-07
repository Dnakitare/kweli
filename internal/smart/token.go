package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultScope is what kweli asks for when --scope isn't given: read-only,
// matching the "no writes, ever" rule the rest of the tool holds to
// (brief §2 goals).
const DefaultScope = "system/*.read"

// defaultTokenLifetime is used when a token response omits expires_in
// (technically optional in RFC 6749, but every SMART server in practice
// sends it) — short enough that kweli will just re-authenticate rather
// than risk using a token past its real, unknown expiry.
const defaultTokenLifetime = 60 * time.Second

// exchangeHTTPTimeout bounds a single token-endpoint call. This is a
// separate, un-rate-limited http.Client, not kweli's internal/client:
// the token endpoint is typically a different host from the FHIR base
// URL, doesn't speak FHIR, and is called rarely (once per TokenSource
// per token lifetime), so it doesn't need the FHIR client's retry/rate
// machinery.
const exchangeHTTPTimeout = 30 * time.Second

// Exchange posts a client_credentials grant with a private_key_jwt client
// assertion to tokenURL and returns the resulting access token and when
// it expires.
func Exchange(ctx context.Context, httpClient *http.Client, tokenURL, assertion, scope string) (accessToken string, expiresAt time.Time, err error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	form.Set("client_assertion", assertion)
	if scope != "" {
		form.Set("scope", scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Token error responses (RFC 6749 §5.2) are diagnostic, not
		// secret — surfacing them is how an operator finds out their
		// client isn't registered, their JWK doesn't match, etc. The
		// assertion and any client secret never appear in this body.
		return "", time.Time{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, truncateForError(body))
	}

	var doc struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   *int64 `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", time.Time{}, fmt.Errorf("parsing token response: %w", err)
	}
	if doc.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("token response has no access_token")
	}

	lifetime := defaultTokenLifetime
	if doc.ExpiresIn != nil && *doc.ExpiresIn > 0 {
		lifetime = time.Duration(*doc.ExpiresIn) * time.Second
	}
	return doc.AccessToken, time.Now().Add(lifetime), nil
}

func truncateForError(body []byte) string {
	const max = 500
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// TokenSource obtains and caches SMART Backend Services access tokens,
// building a fresh signed assertion and re-exchanging it only when the
// cached token is missing or close to expiry. Safe for concurrent use —
// kweli probes multiple resource types in parallel (internal/probe/run.go)
// and they all share one TokenSource.
type TokenSource struct {
	key        *SigningKey
	clientID   string
	tokenURL   string
	scope      string
	httpClient *http.Client

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// NewTokenSource builds a TokenSource. scope defaults to DefaultScope
// when empty.
func NewTokenSource(key *SigningKey, clientID, tokenURL, scope string) *TokenSource {
	if scope == "" {
		scope = DefaultScope
	}
	return &TokenSource{
		key:        key,
		clientID:   clientID,
		tokenURL:   tokenURL,
		scope:      scope,
		httpClient: &http.Client{Timeout: exchangeHTTPTimeout},
	}
}

// refreshSkew re-authenticates a bit before the token actually expires,
// so a request that starts right at the edge doesn't get a token that
// expires mid-flight.
const refreshSkew = 30 * time.Second

// Token returns a valid bearer token, refreshing it if none is cached or
// the cached one is within refreshSkew of expiring. Its signature matches
// client.Config.TokenSource exactly, so a *TokenSource's Token method
// plugs straight in.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cached != "" && time.Now().Before(t.expiresAt.Add(-refreshSkew)) {
		return t.cached, nil
	}

	assertion, err := BuildAssertion(t.key, t.clientID, t.tokenURL)
	if err != nil {
		return "", fmt.Errorf("building client assertion: %w", err)
	}
	token, expiresAt, err := Exchange(ctx, t.httpClient, t.tokenURL, assertion, t.scope)
	if err != nil {
		return "", fmt.Errorf("exchanging client assertion for a token: %w", err)
	}

	t.cached, t.expiresAt = token, expiresAt
	return token, nil
}
