package client_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dnakitare/kweli/internal/client"
)

func TestGet_SuccessAndAuth(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: srv.URL, Token: "secret-token", RPS: 1000})
	resp, err := cl.Get(context.Background(), "Patient?_count=50")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "ok") {
		t.Errorf("unexpected body: %s", resp.Body)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if gotAccept != "application/fhir+json" {
		t.Errorf("Accept header = %q, want application/fhir+json", gotAccept)
	}
	if cl.RequestCount() != 1 {
		t.Errorf("RequestCount() = %d, want 1", cl.RequestCount())
	}
}

func TestWithStrictHandling(t *testing.T) {
	var gotPrefer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrefer = r.Header.Get("Prefer")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	if _, err := cl.Get(context.Background(), "Patient", client.WithStrictHandling()); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPrefer != "handling=strict" {
		t.Errorf("Prefer header = %q, want handling=strict", gotPrefer)
	}
}

func TestRetry_SucceedsAfter429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	resp, err := cl.Get(context.Background(), "Patient")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200 after retry", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Errorf("server saw %d calls, want 2 (one 429 + one success)", calls.Load())
	}
}

func TestRetry_ExhaustedReturnsErrThrottled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := cl.Get(ctx, "Patient")
	if err != client.ErrThrottled {
		t.Errorf("err = %v, want client.ErrThrottled", err)
	}
}

func TestVerbose_RedactsSensitiveParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000, Verbose: true, Out: &buf})
	if _, err := cl.Get(context.Background(), "Patient?birthdate=1990-01-01&address-city=Nairobi&status=final"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "1990-01-01") || strings.Contains(out, "Nairobi") {
		t.Errorf("verbose log leaked a sensitive value: %s", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("verbose log missing REDACTED marker: %s", out)
	}
	if !strings.Contains(out, "status=final") {
		t.Errorf("verbose log should leave non-sensitive params alone: %s", out)
	}
	if !strings.Contains(out, "ms") {
		t.Errorf("verbose log missing latency: %s", out)
	}
}

func TestGetURL_UsesAbsoluteLink(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cl := client.New(client.Config{BaseURL: "http://example.invalid/base", RPS: 1000})
	if _, err := cl.GetURL(context.Background(), srv.URL+"/Patient?_page=2"); err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	if gotPath != "/Patient" {
		t.Errorf("server saw path %q, want /Patient (GetURL must not prepend BaseURL)", gotPath)
	}
}
