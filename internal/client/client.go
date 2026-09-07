// Package client implements kweli's HTTP layer for talking to a FHIR server:
// authentication, rate limiting, retry logic for throttling, and request/response
// redaction for verbose logging.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Config configures a Client.
type Config struct {
	BaseURL     string        // e.g. "https://hapi.fhir.org/baseR4" (no trailing slash required — normalize it)
	Token       string        // bearer token; empty means no Authorization header
	Timeout     time.Duration // per-request timeout; 0 means use a sane default (30s)
	RPS         float64       // requests per second cap; 0 or negative means use a default (8)
	Concurrency int           // advisory only, not enforced by Client itself (the caller uses errgroup); may be ignored
	Verbose     bool          // if true, log one line per request (see Verbose logging below)
	Out         io.Writer     // where verbose lines go; nil means os.Stderr
}

// Client is an HTTP client for FHIR servers with rate limiting, retry, and redaction.
type Client struct {
	baseURL    string
	token      string
	timeout    time.Duration
	limiter    *rate.Limiter
	verbose    bool
	out        io.Writer
	httpClient *http.Client
	requestCnt atomic.Int64
}

// Response is a fully-read HTTP response.
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

// Option customizes a single request.
type Option func(*http.Request)

// WithStrictHandling sets the "Prefer: handling=strict" header.
func WithStrictHandling() Option {
	return func(r *http.Request) {
		r.Header.Set("Prefer", "handling=strict")
	}
}

// ErrThrottled is returned by Get/GetURL/Do when a request still gets
// 429/503 after exhausting retries.
var ErrThrottled = errors.New("throttled after retries")

// New constructs a Client from cfg.
func New(cfg Config) *Client {
	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	rps := cfg.RPS
	if rps <= 0 {
		rps = 8
	}

	out := cfg.Out
	if out == nil {
		out = os.Stderr
	}

	return &Client{
		baseURL:    baseURL,
		token:      cfg.Token,
		timeout:    timeout,
		limiter:    rate.NewLimiter(rate.Limit(rps), 1),
		verbose:    cfg.Verbose,
		out:        out,
		httpClient: &http.Client{},
	}
}

// Get issues GET {BaseURL}/{path} where path may include a query string.
// Applies auth, rate limiting, and retry. Returns the Response even for
// non-2xx HTTP statuses — only network/transport errors or context cancellation
// produce a non-nil error.
func (c *Client) Get(ctx context.Context, path string, opts ...Option) (*Response, error) {
	fullURL := c.baseURL + "/" + strings.TrimPrefix(path, "/")
	return c.do(ctx, "GET", fullURL, nil, opts)
}

// GetURL issues a GET to an absolute URL already returned by the server.
// Same auth/rate-limit/retry/error semantics as Get.
func (c *Client) GetURL(ctx context.Context, absoluteURL string, opts ...Option) (*Response, error) {
	return c.do(ctx, "GET", absoluteURL, nil, opts)
}

// Do issues an arbitrary method to {BaseURL}/{path} with an optional body.
// Same semantics as Get.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, opts ...Option) (*Response, error) {
	fullURL := c.baseURL + "/" + strings.TrimPrefix(path, "/")
	return c.do(ctx, method, fullURL, body, opts)
}

// RequestCount returns how many requests have been sent so far (thread-safe).
func (c *Client) RequestCount() int {
	return int(c.requestCnt.Load())
}

// do is the core request handler that applies rate limiting, retry, logging.
func (c *Client) do(ctx context.Context, method, fullURL string, body []byte, opts []Option) (*Response, error) {
	// Apply exponential backoff with retries for 429/503.
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	maxRetries := 3 // so up to 4 total attempts (initial + 3 retries)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Rate limiting.
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		// Per-request timeout context.
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		start := time.Now()
		resp, respErr := c.singleRequest(reqCtx, method, fullURL, body, opts)
		latency := time.Since(start)
		cancel()

		// Count every attempt (including retries).
		c.requestCnt.Add(1)

		// Log the attempt if verbose.
		if c.verbose {
			c.logRequest(method, fullURL, resp, latency)
		}

		// If it's not a retryable status, return immediately.
		if resp != nil && resp.StatusCode != 429 && resp.StatusCode != 503 {
			return resp, respErr
		}

		// If it's 429/503, try to get Retry-After header for guidance.
		if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
			if attempt < maxRetries {
				backoff := c.parseRetryAfter(resp.Header, backoffs[attempt])
				// Sleep using context-aware sleep.
				select {
				case <-time.After(backoff):
					// Continue to next attempt.
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				continue
			}
			// Exhausted retries; return ErrThrottled instead of the response.
			return nil, ErrThrottled
		}

		// If there's another error (network, etc.), return it.
		if respErr != nil {
			return resp, respErr
		}

		// Otherwise this shouldn't happen (resp is non-nil but status is not 429/503).
		return resp, nil
	}

	return nil, ErrThrottled
}

// singleRequest performs one HTTP request without retry/backoff logic.
func (c *Client) singleRequest(ctx context.Context, method, fullURL string, body []byte, opts []Option) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return nil, err
	}

	// Add standard headers.
	req.Header.Set("Accept", "application/fhir+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// Set body if provided.
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}

	// Apply options (e.g., WithStrictHandling).
	for _, opt := range opts {
		opt(req)
	}

	// Execute the request.
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	// Read the entire response body.
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	return &Response{
		StatusCode: httpResp.StatusCode,
		Body:       respBody,
		Header:     httpResp.Header,
	}, nil
}

// parseRetryAfter parses the Retry-After header and falls back to exponential backoff.
func (c *Client) parseRetryAfter(header http.Header, defaultBackoff time.Duration) time.Duration {
	retryAfter := header.Get("Retry-After")
	if retryAfter == "" {
		return defaultBackoff
	}

	// Try parsing as integer seconds.
	if seconds, err := strconv.ParseInt(retryAfter, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// If we can't parse it, fall back to exponential backoff.
	return defaultBackoff
}

// logRequest logs a single request in the form: METHOD URL STATUS LATENCYms
// The URL is printed with redacted query parameters. Never logs response
// bodies or the Authorization header (brief §5.5).
func (c *Client) logRequest(method, fullURL string, resp *Response, latency time.Duration) {
	redactedURL := c.redactURL(fullURL)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	fmt.Fprintf(c.out, "%s %s %d %dms\n", method, redactedURL, status, latency.Milliseconds())
}

// redactURL redacts sensitive query parameters from the URL for logging.
func (c *Client) redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	q := u.Query()
	sensitiveParams := map[string]bool{
		"identifier": true,
		"name":       true,
		"birthdate":  true,
		"telecom":    true,
		"email":      true,
		"phone":      true,
	}

	// Check for parameters starting with "address".
	changed := false
	for key := range q {
		if sensitiveParams[key] || strings.HasPrefix(key, "address") {
			q.Set(key, "REDACTED")
			changed = true
		}
	}

	if !changed {
		return rawURL
	}

	u.RawQuery = q.Encode()
	return u.String()
}
