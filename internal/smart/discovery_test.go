package smart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
)

func csWithExtensionToken(url string) *capstmt.CapabilityStatement {
	return &capstmt.CapabilityStatement{
		Rest: []capstmt.Rest{{
			Mode: "server",
			Security: capstmt.Security{
				Extension: []capstmt.Extension{{
					URL: smartOAuthURIsExtension,
					Extension: []capstmt.Extension{
						{URL: "token", ValueURI: url},
					},
				}},
			},
		}},
	}
}

func TestDiscoverTokenURL_ExplicitFlagWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not have contacted the server when --token-url is set; got %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})

	got, err := DiscoverTokenURL(context.Background(), cl, nil, srv.URL, "https://explicit.example.org/token")
	if err != nil {
		t.Fatalf("DiscoverTokenURL: %v", err)
	}
	if got != "https://explicit.example.org/token" {
		t.Errorf("got %q, want the explicit --token-url", got)
	}
}

func TestDiscoverTokenURL_WellKnownPreferredOverExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/smart-configuration" {
			w.Write([]byte(`{"token_endpoint":"https://wellknown.example.org/token"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	cs := csWithExtensionToken("https://extension.example.org/token")

	got, err := DiscoverTokenURL(context.Background(), cl, cs, srv.URL, "")
	if err != nil {
		t.Fatalf("DiscoverTokenURL: %v", err)
	}
	if got != "https://wellknown.example.org/token" {
		t.Errorf("got %q, want .well-known to win over the CapabilityStatement extension", got)
	}
}

func TestDiscoverTokenURL_FallsBackToExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // no .well-known support
	}))
	defer srv.Close()
	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})
	cs := csWithExtensionToken("https://extension.example.org/token")

	got, err := DiscoverTokenURL(context.Background(), cl, cs, srv.URL, "")
	if err != nil {
		t.Fatalf("DiscoverTokenURL: %v", err)
	}
	if got != "https://extension.example.org/token" {
		t.Errorf("got %q, want the extension fallback", got)
	}
}

func TestDiscoverTokenURL_NothingFoundErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cl := client.New(client.Config{BaseURL: srv.URL, RPS: 1000})

	_, err := DiscoverTokenURL(context.Background(), cl, nil, srv.URL, "")
	if err == nil {
		t.Error("expected an error when no discovery method finds anything")
	}
}
