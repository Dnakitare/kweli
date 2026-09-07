package sample_test

import (
	"testing"

	"github.com/Dnakitare/kweli/internal/sample"
)

func obs(system, code string) map[string]any {
	return map[string]any{
		"resourceType": "Observation",
		"id":           "obs-1",
		"code": map[string]any{
			"coding": []any{
				map[string]any{"system": system, "code": code},
			},
		},
	}
}

func TestExtractAndFormat_Token(t *testing.T) {
	res := obs("http://loinc.org", "8480-6")
	raw, ok := sample.Extract("Observation", "code", res)
	if !ok {
		t.Fatal("Extract failed")
	}
	got, ok := sample.FormatSearchValue("token", raw)
	if !ok || got != "http://loinc.org|8480-6" {
		t.Errorf("FormatSearchValue = %q, %v, want http://loinc.org|8480-6, true", got, ok)
	}
}

func TestExtract_MissingPathEntry(t *testing.T) {
	_, ok := sample.Extract("Observation", "no-such-param", obs("sys", "code"))
	if ok {
		t.Error("Extract should fail for a param with no table entry")
	}
}

func TestExtract_EmptyValueTreatedAsAbsent(t *testing.T) {
	res := map[string]any{"resourceType": "Patient", "id": "p1", "birthDate": ""}
	_, ok := sample.Extract("Patient", "birthdate", res)
	if ok {
		t.Error("Extract should treat an empty string value as absent")
	}
}

func TestMatches_Token_BareCodeFallback(t *testing.T) {
	res := obs("http://loinc.org", "8480-6")
	if !sample.Matches("Observation", "code", "token", "8480-6", res) {
		t.Error("bare code (no system) should match on code alone")
	}
	if !sample.Matches("Observation", "code", "token", "http://loinc.org|8480-6", res) {
		t.Error("system|code should match exactly")
	}
	if sample.Matches("Observation", "code", "token", "http://loinc.org|9999-9", res) {
		t.Error("wrong code should not match")
	}
}

func TestMatches_DatePrefix(t *testing.T) {
	res := map[string]any{"resourceType": "Patient", "id": "p1", "birthDate": "1990-05-15"}
	if !sample.Matches("Patient", "birthdate", "date", "1990-05-15", res) {
		t.Error("exact date should match")
	}
}

func TestMatches_StringCaseInsensitivePrefix(t *testing.T) {
	res := map[string]any{"resourceType": "Patient", "id": "p1", "name": []any{map[string]any{"family": "Smith"}}}
	if !sample.Matches("Patient", "name", "string", "smith", res) {
		t.Error("case-insensitive exact match should succeed")
	}
	if sample.Matches("Patient", "name", "string", "jones", res) {
		t.Error("non-matching name should not match")
	}
}

func TestMatches_ReferenceByID(t *testing.T) {
	res := map[string]any{"resourceType": "Condition", "id": "c1", "subject": map[string]any{"reference": "Patient/p1"}}
	if !sample.Matches("Condition", "subject", "reference", "Patient/p1", res) {
		t.Error("full reference should match")
	}
	if !sample.Matches("Condition", "subject", "reference", "p1", res) {
		t.Error("bare id should match by id comparison")
	}
}

func TestNonsenseValue_TypeAppropriate(t *testing.T) {
	cases := map[string]string{
		"token":     "urn:kweli|zz",
		"date":      "1800-01-01",
		"reference": "Patient/kweli-nonexistent-",
		"string":    "kweliZZZ",
		"number":    "-999999",
	}
	for typ, wantPrefix := range cases {
		got := sample.NonsenseValue(typ, "Patient", "abc123")
		if len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
			t.Errorf("NonsenseValue(%q) = %q, want prefix %q", typ, got, wantPrefix)
		}
	}
}

func TestSameIDs(t *testing.T) {
	a := sample.Set{Entries: []sample.Entry{{Resource: map[string]any{"id": "1"}}, {Resource: map[string]any{"id": "2"}}}}
	b := sample.Set{Entries: []sample.Entry{{Resource: map[string]any{"id": "2"}}, {Resource: map[string]any{"id": "1"}}}}
	c := sample.Set{Entries: []sample.Entry{{Resource: map[string]any{"id": "1"}}}}
	if !sample.SameIDs(a, b) {
		t.Error("same ids in different order should be equal")
	}
	if sample.SameIDs(a, c) {
		t.Error("different-sized sets should not be equal")
	}
}

func TestParseBundle(t *testing.T) {
	body := []byte(`{
		"resourceType": "Bundle",
		"type": "searchset",
		"total": 2,
		"link": [{"relation": "next", "url": "http://example/next"}],
		"entry": [
			{"fullUrl": "urn:1", "resource": {"resourceType":"Patient","id":"1"}, "search": {"mode":"match"}},
			{"fullUrl": "urn:2", "resource": {"resourceType":"Patient","id":"2"}, "search": {"mode":"include"}}
		]
	}`)
	s, err := sample.ParseBundle("Patient", body)
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if s.Total != 2 {
		t.Errorf("Total = %d, want 2", s.Total)
	}
	if s.NextLink != "http://example/next" {
		t.Errorf("NextLink = %q", s.NextLink)
	}
	if len(s.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(s.Entries))
	}
	if len(s.MatchEntries()) != 1 {
		t.Errorf("MatchEntries() should exclude the include entry, got %d", len(s.MatchEntries()))
	}
}

func TestParseBundle_RejectsNonBundle(t *testing.T) {
	_, err := sample.ParseBundle("Patient", []byte(`{"resourceType":"Patient","id":"1"}`))
	if err == nil {
		t.Fatal("expected an error for a non-Bundle response")
	}
}
