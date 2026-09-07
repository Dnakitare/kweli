// Package sample fetches sample resources from a search response and
// implements the value-extraction side of the two-query test (brief §5.3):
// finding a real value for a search param via the curated Paths table
// (paths.go), formatting it as a query value, and comparing it back against
// entries in a result set — all without a FHIRPath engine.
package sample

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Entry is one Bundle.entry from a search response.
type Entry struct {
	FullURL    string
	Resource   map[string]any
	SearchMode string // entry.search.mode: "match", "include", ""
}

// Set is the sample resources fetched for one resource type via the
// unfiltered search probe (brief §5.2a).
type Set struct {
	Resource string
	Total    int // Bundle.total if present, else -1. Never use for
	// correctness comparisons (brief §8) — compare entry ids instead.
	Entries  []Entry
	NextLink string // link[rel=next].url, if any
}

// MatchEntries returns only the entries with search.mode == "match" (or no
// mode recorded, which is the common case for plain unfiltered searches).
// Included/rev-included entries should not be used as sample data for
// search-param extraction.
func (s Set) MatchEntries() []Entry {
	var out []Entry
	for _, e := range s.Entries {
		if e.SearchMode == "" || e.SearchMode == "match" {
			out = append(out, e)
		}
	}
	return out
}

// IDs returns the set of resource ids (falling back to fullUrl) present in
// the set, for the "same entries" comparison in §5.3 step 3.
func (s Set) IDs() map[string]bool {
	out := make(map[string]bool, len(s.Entries))
	for _, e := range s.Entries {
		out[entryKey(e)] = true
	}
	return out
}

func entryKey(e Entry) string {
	if id, ok := e.Resource["id"].(string); ok && id != "" {
		return id
	}
	return e.FullURL
}

// SameIDs reports whether two sets contain exactly the same resources,
// compared by id/fullUrl (never by Bundle.total — see brief §8).
func SameIDs(a, b Set) bool {
	ai, bi := a.IDs(), b.IDs()
	if len(ai) != len(bi) {
		return false
	}
	for k := range ai {
		if !bi[k] {
			return false
		}
	}
	return true
}

// ParseResource decodes a single FHIR resource response body (e.g. from a
// read or vread) into a generic map, per the brief's "treat resources as
// map[string]any" rule (§4).
func ParseResource(body []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decoding resource: %w", err)
	}
	return m, nil
}

// ParseBundle decodes a FHIR searchset Bundle response body into a Set.
func ParseBundle(resourceType string, body []byte) (Set, error) {
	var raw struct {
		ResourceType string `json:"resourceType"`
		Total        *int   `json:"total"`
		Link         []struct {
			Relation string `json:"relation"`
			URL      string `json:"url"`
		} `json:"link"`
		Entry []struct {
			FullURL  string         `json:"fullUrl"`
			Resource map[string]any `json:"resource"`
			Search   struct {
				Mode string `json:"mode"`
			} `json:"search"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Set{}, fmt.Errorf("decoding Bundle: %w", err)
	}
	if raw.ResourceType != "Bundle" {
		return Set{}, fmt.Errorf("expected resourceType Bundle, got %q", raw.ResourceType)
	}
	s := Set{Resource: resourceType, Total: -1}
	if raw.Total != nil {
		s.Total = *raw.Total
	}
	for _, l := range raw.Link {
		if l.Relation == "next" {
			s.NextLink = l.URL
		}
	}
	for _, e := range raw.Entry {
		s.Entries = append(s.Entries, Entry{
			FullURL:    e.FullURL,
			Resource:   e.Resource,
			SearchMode: e.Search.Mode,
		})
	}
	return s, nil
}

// dig walks a dotted path (paths.go syntax: digit segments index arrays,
// others index maps) through a decoded JSON value.
func dig(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		if idx, err := strconv.Atoi(seg); err == nil {
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	if cur == nil {
		return nil, false
	}
	if s, ok := cur.(string); ok && s == "" {
		return nil, false
	}
	return cur, true
}

// Extract looks up the curated path for (resourceType, param) and digs it
// out of resource. ok is false if there's no table entry, or the path is
// absent/empty in this resource.
func Extract(resourceType, param string, resource map[string]any) (value any, ok bool) {
	path, found := Paths[resourceType+"/"+param]
	if !found {
		return nil, false
	}
	return dig(resource, path)
}

// coding renders a {system, code} object (or a bare code string) as
// FHIR's "system|code" token search syntax. Falls back to a bare code when
// there's no system, matching how most servers accept token searches.
func coding(v any) (system, code string, ok bool) {
	switch t := v.(type) {
	case string:
		return "", t, true
	case map[string]any:
		c, _ := t["code"].(string)
		if c == "" {
			return "", "", false
		}
		sys, _ := t["system"].(string)
		return sys, c, true
	default:
		return "", "", false
	}
}

func numberString(v any) (string, bool) {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case string:
		return t, true
	default:
		return "", false
	}
}

// FormatSearchValue renders an extracted raw value as the query value to
// use for the positive query (§5.3 step 2), per FHIR search param type.
func FormatSearchValue(paramType string, value any) (string, bool) {
	switch paramType {
	case "token":
		sys, code, ok := coding(value)
		if !ok {
			return "", false
		}
		if sys == "" {
			return code, true
		}
		return sys + "|" + code, true
	case "date", "dateTime", "instant":
		s, ok := value.(string)
		if !ok || s == "" {
			return "", false
		}
		if len(s) > 10 {
			s = s[:10] // day precision, per brief §5.3 step 2
		}
		return s, true
	case "reference":
		s, ok := value.(string)
		if !ok || s == "" {
			return "", false
		}
		return s, true
	case "string":
		s, ok := value.(string)
		if !ok || s == "" {
			return "", false
		}
		return s, true
	case "number", "quantity":
		return numberString(value)
	default:
		// Unknown/"special"/uri types: try string as a best effort; callers
		// should generally skip these per §5.2c.
		if s, ok := value.(string); ok && s != "" {
			return s, true
		}
		return "", false
	}
}

func refID(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Matches reports whether resource's value at (resourceType, param) matches
// expected, using the type-aware comparison rules from §5.3 step 2:
// token = system+code match (or bare code match when expected has none),
// date = day-precision prefix match, reference = id match, string =
// case-insensitive prefix match, number/quantity = exact string match.
func Matches(resourceType, param, paramType, expected string, resource map[string]any) bool {
	raw, ok := Extract(resourceType, param, resource)
	if !ok {
		return false
	}
	got, ok := FormatSearchValue(paramType, raw)
	if !ok {
		return false
	}
	switch paramType {
	case "token":
		if !strings.Contains(expected, "|") {
			// expected has no system: match on bare code
			gotCode := got
			if i := strings.LastIndex(got, "|"); i >= 0 {
				gotCode = got[i+1:]
			}
			return gotCode == expected
		}
		return got == expected
	case "date", "dateTime", "instant":
		return strings.HasPrefix(got, expected) || strings.HasPrefix(expected, got)
	case "reference":
		return refID(got) == refID(expected)
	case "string":
		return strings.HasPrefix(strings.ToLower(got), strings.ToLower(expected))
	case "number", "quantity":
		return got == expected
	default:
		return got == expected
	}
}

// NonsenseValue returns a type-appropriate value guaranteed not to match
// real data (§5.3 step 3), given a FHIR search param type, the resource
// type being searched (used for reference-typed params, per brief's
// "T/kweli-nonexistent" form), and a random suffix supplied by the caller
// (so repeated calls don't collide and results stay reproducible under a
// fixed seed).
func NonsenseValue(paramType, resourceType, randomSuffix string) string {
	switch paramType {
	case "token":
		return "urn:kweli|zz" + randomSuffix
	case "date", "dateTime", "instant":
		return "1800-01-01"
	case "reference":
		return resourceType + "/kweli-nonexistent-" + randomSuffix
	case "string":
		return "kweliZZZ" + randomSuffix
	case "number", "quantity":
		return "-999999"
	default:
		return "kweli-nonexistent-" + randomSuffix
	}
}
