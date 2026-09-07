package smart

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
)

// smartOAuthURIsExtension is the SMART App Launch StructureDefinition a
// CapabilityStatement's rest.security.extension uses to point at the
// authorize/token/introspect/register endpoints — the legacy discovery
// mechanism the brief's §5.1.2 names explicitly.
const smartOAuthURIsExtension = "http://fhir-registry.smarthealthit.org/StructureDefinition/oauth-uris"

// DiscoverTokenURL determines the OAuth token endpoint for SMART Backend
// Services, per brief §5.1.2. Precedence: an explicit --token-url always
// wins (the operator knows their server); otherwise kweli prefers
// {baseURL}/.well-known/smart-configuration — the SMART App Launch
// standard's primary discovery mechanism today — and falls back to the
// CapabilityStatement's oauth-uris security extension, which is older but
// still what some deployed servers only publish.
//
// cl must be an unauthenticated client (discovery happens before kweli
// has a token — both of these endpoints are meant to be publicly
// readable). baseURL is used only to make a failure message legible.
func DiscoverTokenURL(ctx context.Context, cl *client.Client, cs *capstmt.CapabilityStatement, baseURL, explicitTokenURL string) (string, error) {
	if explicitTokenURL != "" {
		return explicitTokenURL, nil
	}

	if url, err := wellKnownTokenURL(ctx, cl); err == nil && url != "" {
		return url, nil
	}

	if url := extensionTokenURL(cs); url != "" {
		return url, nil
	}

	return "", fmt.Errorf("could not discover a token endpoint: no --token-url given, %s/.well-known/smart-configuration didn't yield one, and the CapabilityStatement has no oauth-uris security extension", baseURL)
}

func wellKnownTokenURL(ctx context.Context, cl *client.Client) (string, error) {
	resp, err := cl.Get(ctx, ".well-known/smart-configuration")
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf(".well-known/smart-configuration returned %d", resp.StatusCode)
	}
	var doc struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return "", fmt.Errorf("parsing .well-known/smart-configuration: %w", err)
	}
	if doc.TokenEndpoint == "" {
		return "", fmt.Errorf(".well-known/smart-configuration has no token_endpoint")
	}
	return doc.TokenEndpoint, nil
}

func extensionTokenURL(cs *capstmt.CapabilityStatement) string {
	if cs == nil {
		return ""
	}
	for _, rest := range cs.Rest {
		for _, ext := range rest.Security.Extension {
			if ext.URL != smartOAuthURIsExtension {
				continue
			}
			for _, sub := range ext.Extension {
				if sub.URL == "token" && sub.ValueURI != "" {
					return sub.ValueURI
				}
			}
		}
	}
	return ""
}
