package expect_test

import (
	"testing"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/expect"
	"github.com/Dnakitare/kweli/internal/model"
)

func findingByID(findings []model.Finding, id string) (model.Finding, bool) {
	for _, f := range findings {
		if f.ID == id {
			return f, true
		}
	}
	return model.Finding{}, false
}

func TestCheck_MissingResourceEntirely(t *testing.T) {
	// A CapabilityStatement that claims nothing at all — every US Core
	// required resource type should come back as a whole-resource miss.
	cs := &capstmt.CapabilityStatement{Rest: []capstmt.Rest{{Mode: "server"}}}
	findings := expect.Check(cs)

	f, ok := findingByID(findings, "Patient/expect")
	if !ok {
		t.Fatal("expected a Patient/expect finding for a CapabilityStatement claiming nothing")
	}
	if f.Status != model.StatusMissing || f.Kind != model.KindMissing {
		t.Errorf("Patient/expect = %+v, want Status=missing Kind=missing", f)
	}
	if f.Resource != "Patient" {
		t.Errorf("Resource = %q, want Patient", f.Resource)
	}

	// Every required resource type should be represented, one way or another.
	if len(findings) < len(expect.USCoreRequiredParams) {
		t.Errorf("got %d findings for %d required resource types — every unclaimed resource should produce at least one finding",
			len(findings), len(expect.USCoreRequiredParams))
	}
}

func TestCheck_ClaimedResourceMissingSomeParams(t *testing.T) {
	cs := &capstmt.CapabilityStatement{
		Rest: []capstmt.Rest{{
			Mode: "server",
			Resource: []capstmt.ResourceEntry{
				{
					Type:        "Patient",
					Interaction: []capstmt.InteractionRef{{Code: "search-type"}},
					SearchParam: []capstmt.SearchParam{
						{Name: "birthdate", Type: "date"},
						{Name: "name", Type: "string"},
						// missing: _id, gender, identifier
					},
				},
			},
		}},
	}
	findings := expect.Check(cs)

	if _, ok := findingByID(findings, "Patient/expect"); ok {
		t.Error("Patient/expect (whole-resource miss) should not fire when Patient IS claimed")
	}
	for _, param := range []string{"_id", "gender", "identifier"} {
		id := "Patient/expect/" + param
		f, ok := findingByID(findings, id)
		if !ok {
			t.Errorf("expected a finding for missing param %q", param)
			continue
		}
		if f.Status != model.StatusMissing {
			t.Errorf("%s: Status = %s, want missing", id, f.Status)
		}
	}
	for _, param := range []string{"birthdate", "name"} {
		if _, ok := findingByID(findings, "Patient/expect/"+param); ok {
			t.Errorf("%q is claimed — should not produce a missing finding", param)
		}
	}
}

func TestCheck_FullyCompliantResourceProducesNoFindings(t *testing.T) {
	cs := &capstmt.CapabilityStatement{
		Rest: []capstmt.Rest{{
			Mode: "server",
			Resource: []capstmt.ResourceEntry{
				{
					Type:        "AllergyIntolerance",
					Interaction: []capstmt.InteractionRef{{Code: "search-type"}},
					SearchParam: []capstmt.SearchParam{{Name: "patient", Type: "reference"}},
				},
			},
		}},
	}
	findings := expect.Check(cs)
	for _, f := range findings {
		if f.Resource == "AllergyIntolerance" {
			t.Errorf("AllergyIntolerance fully satisfies its required params, got an unexpected finding: %+v", f)
		}
	}
}

func TestCheck_NilCapabilityStatement(t *testing.T) {
	if got := expect.Check(nil); got != nil {
		t.Errorf("Check(nil) = %v, want nil", got)
	}
}

func TestCheck_DeterministicOrder(t *testing.T) {
	cs := &capstmt.CapabilityStatement{Rest: []capstmt.Rest{{Mode: "server"}}}
	first := expect.Check(cs)
	second := expect.Check(cs)
	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("order not stable at index %d: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
}

// TestUSCoreRequiredParams_SanityCheck guards against the table silently
// losing entries or gaining garbage — not a re-verification against the
// live IG (see table.go's doc comment for provenance), just a floor.
func TestUSCoreRequiredParams_SanityCheck(t *testing.T) {
	if len(expect.USCoreRequiredParams) < 15 {
		t.Errorf("only %d resource types in the table — suspiciously small for US Core 6.1", len(expect.USCoreRequiredParams))
	}
	for rtype, params := range expect.USCoreRequiredParams {
		if rtype == "" {
			t.Error("empty resource type key in USCoreRequiredParams")
		}
		if len(params) == 0 {
			t.Errorf("%s has zero required params — should have been left out of the table entirely", rtype)
		}
		for _, p := range params {
			if p == "" {
				t.Errorf("%s has an empty param name", rtype)
			}
		}
	}
	// Spot-check a few known-important entries so a future accidental
	// edit to the table gets caught immediately, not just via coverage.
	mustContain := map[string]string{
		"Patient":     "birthdate",
		"Observation": "category",
		"Condition":   "patient",
	}
	for rtype, want := range mustContain {
		params, ok := expect.USCoreRequiredParams[rtype]
		if !ok {
			t.Errorf("%s missing from the table entirely", rtype)
			continue
		}
		found := false
		for _, p := range params {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s required params %v missing expected %q", rtype, params, want)
		}
	}
}
