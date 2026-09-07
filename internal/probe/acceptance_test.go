package probe_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/probe"
	"github.com/Dnakitare/kweli/internal/testserver"
)

// fetchCapStmt gets and parses /metadata from a running test server.
func fetchCapStmt(t *testing.T, cl *client.Client) *capstmt.CapabilityStatement {
	t.Helper()
	resp, err := cl.Get(context.Background(), "metadata")
	if err != nil {
		t.Fatalf("fetching /metadata: %v", err)
	}
	cs, err := capstmt.Parse(bytes.NewReader(resp.Body))
	if err != nil {
		t.Fatalf("parsing CapabilityStatement: %v", err)
	}
	return cs
}

func runProbe(t *testing.T, baseURL string) *model.Report {
	t.Helper()
	return runProbeWithOptions(t, baseURL, probe.Options{Concurrency: 4, Seed: 42})
}

func runProbeWithOptions(t *testing.T, baseURL string, opts probe.Options) *model.Report {
	t.Helper()
	cl := client.New(client.Config{BaseURL: baseURL, RPS: 1000, Timeout: 5 * time.Second})
	cs := fetchCapStmt(t, cl)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if opts.Concurrency == 0 {
		opts.Concurrency = 4
	}
	report, err := probe.Run(ctx, cs, cl, opts)
	if err != nil {
		t.Fatalf("probe.Run: %v", err)
	}
	return report
}

func findingByID(t *testing.T, r *model.Report, id string) model.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding with ID %q in report (have %d findings)", id, len(r.Findings))
	return model.Finding{}
}

// TestAcceptance_LyingServer is brief §7's acceptance test: Phase 1 is done
// when kweli reports every planted defect and nothing else.
func TestAcceptance_LyingServer(t *testing.T) {
	srv := testserver.New()
	defer srv.Close()

	report := runProbe(t, srv.URL)

	cases := []struct {
		name   string
		id     string
		status model.Status
	}{
		{"1: Observation?code silently ignored", "Observation/search/code", model.StatusIgnored},
		{"2: Patient?birthdate rejected", "Patient/search/birthdate", model.StatusRejected},
		{"3: Patient?name partially honoured", "Patient/search/name", model.StatusIgnoredPartial},
		{"4: Condition include rejected", "Condition/include/Condition:subject", model.StatusRejected},
		{"5: Encounter paging 404s", "Encounter/paging", model.StatusRejected},
		{"6: Procedure paging repeats", "Procedure/paging", model.StatusIgnored},
		{"7: Observation _count ignored", "Observation/count", model.StatusIgnored},
		{"9: Patient _lastUpdated ignored", "Patient/search/_lastUpdated", model.StatusIgnored},
		{"11: Immunization date strict-only", "Immunization/search/date", model.StatusRejectedStrict},
		{"12: DiagnosticReport based-on classified", "DiagnosticReport/search/based-on", model.StatusVerified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := findingByID(t, report, c.id)
			if f.Status != c.status {
				t.Errorf("finding %s: got status %q, want %q (detail: %s)", c.id, f.Status, c.status, f.Detail)
			}
		})
	}

	// 8: MedicationRequest/read claimed, returns 500 -> rejected.
	t.Run("8: MedicationRequest read 500", func(t *testing.T) {
		f := findingByID(t, report, "MedicationRequest/read")
		if f.Status != model.StatusRejected {
			t.Errorf("got status %q, want %q (detail: %s)", f.Status, model.StatusRejected, f.Detail)
		}
	})

	// 10: AllergyIntolerance claimed with search, but returns 0 entries ->
	// every downstream claim for it must be untested, never a lie.
	t.Run("10: AllergyIntolerance empty search is untested, not a lie", func(t *testing.T) {
		f := findingByID(t, report, "AllergyIntolerance/search/clinical-status")
		if f.Status != model.StatusUntested {
			t.Errorf("got status %q, want %q (detail: %s)", f.Status, model.StatusUntested, f.Detail)
		}
	})

	// "and nothing else": every other finding must not be a lie.
	planted := map[string]bool{
		"Observation/search/code":             true,
		"Patient/search/birthdate":            true,
		"Patient/search/name":                 true,
		"Condition/include/Condition:subject": true,
		"Encounter/paging":                    true,
		"Procedure/paging":                    true,
		"Observation/count":                   true,
		"MedicationRequest/read":              true,
		"Patient/search/_lastUpdated":         true,
		"Immunization/search/date":            true,
	}
	for _, f := range report.Findings {
		if planted[f.ID] {
			continue
		}
		if f.Status.IsLie() {
			t.Errorf("unplanted lie: %s is %s (%s) — Phase 1 must report every defect and nothing else", f.ID, f.Status, f.Detail)
		}
	}
}

// TestAcceptance_TruthfulServer is brief §7's "truthful" control: same
// data and claims, every defect fixed, zero lies expected.
func TestAcceptance_TruthfulServer(t *testing.T) {
	srv := testserver.NewTruthful()
	defer srv.Close()

	report := runProbe(t, srv.URL)

	for _, f := range report.Findings {
		if f.Status.IsLie() {
			t.Errorf("truthful control reported a lie: %s is %s (%s)", f.ID, f.Status, f.Detail)
		}
	}
	if report.Summary.Lies != 0 {
		t.Errorf("report.Summary.Lies = %d, want 0", report.Summary.Lies)
	}
}

// TestAcceptance_ExpectUSCore is Phase 3's acceptance test: the fixture's
// CapabilityStatement only claims 9 resource types (out of US Core's
// required set) and leaves several search params off the ones it does
// claim, so --expect us-core should surface both flavors of gap — a
// resource type never claimed at all, and a required param missing from
// a resource type that IS claimed.
func TestAcceptance_ExpectUSCore(t *testing.T) {
	srv := testserver.New()
	defer srv.Close()

	report := runProbeWithOptions(t, srv.URL, probe.Options{Concurrency: 4, Seed: 42, Expect: "us-core"})

	// Whole resource types the fixture never claims at all.
	for _, rtype := range []string{"Coverage", "Device", "DocumentReference", "CarePlan", "CareTeam", "Location", "Organization", "Practitioner"} {
		f := findingByID(t, report, rtype+"/expect")
		if f.Status != model.StatusMissing {
			t.Errorf("%s/expect: got status %q, want missing", rtype, f.Status)
		}
	}

	// Resources the fixture DOES claim, but with required params absent.
	missingParamCases := []struct{ id string }{
		{"Patient/expect/_id"},
		{"Patient/expect/gender"},
		{"Patient/expect/identifier"},
		{"Observation/expect/category"},
		{"Observation/expect/date"},
		{"Observation/expect/patient"},
		{"Condition/expect/category"},
		{"Condition/expect/patient"},
		{"MedicationRequest/expect/intent"},
		{"MedicationRequest/expect/patient"},
		{"MedicationRequest/expect/status"},
	}
	for _, c := range missingParamCases {
		f := findingByID(t, report, c.id)
		if f.Status != model.StatusMissing {
			t.Errorf("%s: got status %q, want missing", c.id, f.Status)
		}
	}

	// Params the fixture DOES claim must not be reported missing.
	for _, id := range []string{"Patient/expect/birthdate", "Patient/expect/name", "Observation/expect/code"} {
		for _, f := range report.Findings {
			if f.ID == id {
				t.Errorf("%s should not exist — this param is claimed by the fixture", id)
			}
		}
	}

	if report.Summary.Missing == 0 {
		t.Error("Summary.Missing should be non-zero under --expect us-core against this fixture")
	}
	if report.Summary.Missing != len(filterMissing(report.Findings)) {
		t.Errorf("Summary.Missing = %d, want it to match the actual count of missing findings (%d)", report.Summary.Missing, len(filterMissing(report.Findings)))
	}
}

// TestAcceptance_ExpectOffProducesNoMissingFindings confirms --expect is
// truly opt-in: without it, no "missing" findings appear even though the
// fixture is (by design) US-Core-incomplete.
func TestAcceptance_ExpectOffProducesNoMissingFindings(t *testing.T) {
	srv := testserver.New()
	defer srv.Close()

	report := runProbe(t, srv.URL) // no Expect set
	if got := len(filterMissing(report.Findings)); got != 0 {
		t.Errorf("got %d missing findings without --expect, want 0", got)
	}
	if report.Summary.Missing != 0 {
		t.Errorf("Summary.Missing = %d without --expect, want 0", report.Summary.Missing)
	}
}

func filterMissing(findings []model.Finding) []model.Finding {
	var out []model.Finding
	for _, f := range findings {
		if f.Status == model.StatusMissing {
			out = append(out, f)
		}
	}
	return out
}
