// Package capstmt parses the subset of a FHIR CapabilityStatement that
// kweli probes. Deliberately not a generated FHIR model: unknown fields drop
// on the floor via encoding/json, and this same struct shape parses R4, R4B,
// and R5 CapabilityStatements.
package capstmt

import (
	"encoding/json"
	"fmt"
	"io"
)

// CapabilityStatement is the minimal shape kweli reads from /metadata.
type CapabilityStatement struct {
	ResourceType string `json:"resourceType"`
	FHIRVersion  string `json:"fhirVersion"`
	Software     struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"software"`
	Rest []Rest `json:"rest"`
}

type Rest struct {
	Mode        string           `json:"mode"`
	Security    Security         `json:"security"`
	Resource    []ResourceEntry  `json:"resource"`
	Interaction []InteractionRef `json:"interaction"`
	Operation   []OperationRef   `json:"operation"`
}

// Security carries just enough of rest.security to locate SMART's OAuth
// endpoints (Phase 2). The "extension" shape here is FHIR's generic
// Extension array; kweli only looks at the two SMART OAuth URIs.
type Security struct {
	Service   []CodeableRef `json:"service"`
	Extension []Extension   `json:"extension"`
}

type Extension struct {
	URL       string      `json:"url"`
	ValueURI  string      `json:"valueUri"`
	Extension []Extension `json:"extension"`
}

type CodeableRef struct {
	Coding []struct {
		System string `json:"system"`
		Code   string `json:"code"`
	} `json:"coding"`
	Text string `json:"text"`
}

type ResourceEntry struct {
	Type             string           `json:"type"`
	Interaction      []InteractionRef `json:"interaction"`
	SearchParam      []SearchParam    `json:"searchParam"`
	SearchInclude    []string         `json:"searchInclude"`
	SearchRevInclude []string         `json:"searchRevInclude"`
	Operation        []OperationRef   `json:"operation"`
}

type InteractionRef struct {
	Code string `json:"code"`
}

type SearchParam struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Definition string `json:"definition"`
}

type OperationRef struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// HasInteraction reports whether the resource entry claims the given
// interaction code (e.g. "read", "vread", "search-type", "history-instance").
func (r ResourceEntry) HasInteraction(code string) bool {
	for _, i := range r.Interaction {
		if i.Code == code {
			return true
		}
	}
	return false
}

// HasSystemInteraction reports whether the top-level rest entry claims a
// given system-wide interaction code (e.g. "history-system").
func (r Rest) HasSystemInteraction(code string) bool {
	for _, i := range r.Interaction {
		if i.Code == code {
			return true
		}
	}
	return false
}

// FindResource returns the resource entry for a given type, if claimed.
func (c *CapabilityStatement) FindResource(resourceType string) (ResourceEntry, bool) {
	for _, rest := range c.Rest {
		if rest.Mode != "" && rest.Mode != "server" {
			continue
		}
		for _, r := range rest.Resource {
			if r.Type == resourceType {
				return r, true
			}
		}
	}
	return ResourceEntry{}, false
}

// ResourceTypes returns every resource type claimed under a "server" mode
// rest entry, in the order the CapabilityStatement lists them.
func (c *CapabilityStatement) ResourceTypes() []string {
	var out []string
	for _, rest := range c.Rest {
		if rest.Mode != "" && rest.Mode != "server" {
			continue
		}
		for _, r := range rest.Resource {
			out = append(out, r.Type)
		}
	}
	return out
}

// ServerRest returns the first "server" mode rest entry, if any. Most
// CapabilityStatements have exactly one.
func (c *CapabilityStatement) ServerRest() (Rest, bool) {
	for _, rest := range c.Rest {
		if rest.Mode == "" || rest.Mode == "server" {
			return rest, true
		}
	}
	return Rest{}, false
}

// Parse decodes a CapabilityStatement from r. It fails fast (per brief
// §5.1) when the body isn't JSON or isn't actually a CapabilityStatement.
func Parse(r io.Reader) (*CapabilityStatement, error) {
	var cs CapabilityStatement
	dec := json.NewDecoder(r)
	if err := dec.Decode(&cs); err != nil {
		return nil, fmt.Errorf("decoding CapabilityStatement: %w", err)
	}
	if cs.ResourceType != "CapabilityStatement" {
		return nil, fmt.Errorf("expected resourceType CapabilityStatement, got %q", cs.ResourceType)
	}
	return &cs, nil
}
