package capstmt_test

import (
	"strings"
	"testing"

	"github.com/Dnakitare/kweli/internal/capstmt"
)

const sampleCapStmt = `{
  "resourceType": "CapabilityStatement",
  "fhirVersion": "4.0.1",
  "software": {"name": "Foo Server", "version": "3.2"},
  "rest": [{
    "mode": "server",
    "resource": [
      {
        "type": "Patient",
        "interaction": [{"code": "read"}, {"code": "search-type"}],
        "searchParam": [{"name": "birthdate", "type": "date", "definition": "http://hl7.org/fhir/SearchParameter/Patient-birthdate"}],
        "searchInclude": ["Patient:general-practitioner"]
      },
      {
        "type": "Observation",
        "interaction": [{"code": "search-type"}],
        "searchParam": [{"name": "code", "type": "token"}]
      }
    ]
  }]
}`

func TestParse(t *testing.T) {
	cs, err := capstmt.Parse(strings.NewReader(sampleCapStmt))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cs.FHIRVersion != "4.0.1" {
		t.Errorf("FHIRVersion = %q, want 4.0.1", cs.FHIRVersion)
	}
	if cs.Software.Name != "Foo Server" || cs.Software.Version != "3.2" {
		t.Errorf("Software = %+v, want Foo Server 3.2", cs.Software)
	}

	types := cs.ResourceTypes()
	if len(types) != 2 || types[0] != "Patient" || types[1] != "Observation" {
		t.Errorf("ResourceTypes() = %v, want [Patient Observation]", types)
	}

	patient, ok := cs.FindResource("Patient")
	if !ok {
		t.Fatal("FindResource(Patient) not found")
	}
	if !patient.HasInteraction("read") || !patient.HasInteraction("search-type") {
		t.Errorf("Patient interactions = %+v, want read and search-type", patient.Interaction)
	}
	if patient.HasInteraction("delete") {
		t.Errorf("Patient should not claim delete")
	}
	if len(patient.SearchInclude) != 1 || patient.SearchInclude[0] != "Patient:general-practitioner" {
		t.Errorf("SearchInclude = %v", patient.SearchInclude)
	}

	if _, ok := cs.FindResource("Condition"); ok {
		t.Error("FindResource(Condition) should not be found — not claimed")
	}
}

func TestParse_RejectsWrongResourceType(t *testing.T) {
	_, err := capstmt.Parse(strings.NewReader(`{"resourceType": "Bundle"}`))
	if err == nil {
		t.Fatal("expected an error for resourceType != CapabilityStatement")
	}
}

func TestParse_RejectsNonJSON(t *testing.T) {
	_, err := capstmt.Parse(strings.NewReader("not json"))
	if err == nil {
		t.Fatal("expected an error for non-JSON body")
	}
}

func TestParse_UnknownFieldsDropOnTheFloor(t *testing.T) {
	// R5-ish extra fields kweli doesn't read should never break parsing.
	body := `{"resourceType":"CapabilityStatement","fhirVersion":"5.0.0","status":"active","date":"2024-01-01","rest":[{"mode":"server","resource":[{"type":"Patient","interaction":[{"code":"read"}]}]}]}`
	cs, err := capstmt.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cs.FHIRVersion != "5.0.0" {
		t.Errorf("FHIRVersion = %q, want 5.0.0", cs.FHIRVersion)
	}
}
