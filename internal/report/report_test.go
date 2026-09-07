package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Dnakitare/kweli/internal/model"
)

func TestText(t *testing.T) {
	r := buildTestReport()

	var buf bytes.Buffer
	err := Text(&buf, r, false)
	if err != nil {
		t.Fatalf("Text() error: %v", err)
	}

	output := buf.String()

	// Check for header
	if !strings.Contains(output, "kweli  https://fhir.example.com") {
		t.Errorf("Text output missing server URL in header")
	}
	if !strings.Contains(output, "FHIR 4.0.1") {
		t.Errorf("Text output missing FHIR version")
	}
	if !strings.Contains(output, "software: Example Server") {
		t.Errorf("Text output missing software")
	}

	// Check for resources table
	if !strings.Contains(output, "Resource") || !strings.Contains(output, "Claimed") {
		t.Errorf("Text output missing resources table header")
	}
	if !strings.Contains(output, "Patient") {
		t.Errorf("Text output missing Patient resource")
	}

	// Check for LIES section
	if !strings.Contains(output, "LIES") {
		t.Errorf("Text output missing LIES section")
	}
	if !strings.Contains(output, "Patient?birthdate=error") {
		t.Errorf("Text output missing lied finding claim")
	}

	// Check for UNTESTED section
	if !strings.Contains(output, "UNTESTED") {
		t.Errorf("Text output missing UNTESTED section")
	}
	if !strings.Contains(output, "Observation?code=missing") {
		t.Errorf("Text output missing untested finding claim")
	}

	// Check for summary line
	if !strings.Contains(output, "Claims") || !strings.Contains(output, "Verified") {
		t.Errorf("Text output missing summary line")
	}
	if !strings.Contains(output, "requests") {
		t.Errorf("Text output missing request count")
	}

	// Test with color
	var bufColor bytes.Buffer
	err = Text(&bufColor, r, true)
	if err != nil {
		t.Fatalf("Text() with color error: %v", err)
	}
	colorOutput := bufColor.String()
	if !strings.Contains(colorOutput, "\x1b[") {
		t.Errorf("Text output with color=true should contain ANSI codes")
	}
}

func TestJSON(t *testing.T) {
	r := buildTestReport()

	var buf bytes.Buffer
	err := JSON(&buf, r)
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	output := buf.String()

	// Verify it ends with newline
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("JSON output should end with newline")
	}

	// Round-trip: unmarshal and verify key fields
	var unmarshalled model.Report
	err = json.Unmarshal(bytes.TrimSuffix([]byte(output), []byte("\n")), &unmarshalled)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON output: %v", err)
	}

	if unmarshalled.Server != r.Server {
		t.Errorf("Round-trip failed: Server mismatch. Expected %q, got %q", r.Server, unmarshalled.Server)
	}
	if unmarshalled.FHIRVersion != r.FHIRVersion {
		t.Errorf("Round-trip failed: FHIRVersion mismatch. Expected %q, got %q", r.FHIRVersion, unmarshalled.FHIRVersion)
	}
	if len(unmarshalled.Findings) != len(r.Findings) {
		t.Errorf("Round-trip failed: Findings length mismatch. Expected %d, got %d", len(r.Findings), len(unmarshalled.Findings))
	}
	if len(unmarshalled.Resources) != len(r.Resources) {
		t.Errorf("Round-trip failed: Resources length mismatch. Expected %d, got %d", len(r.Resources), len(unmarshalled.Resources))
	}
}

func TestMarkdown(t *testing.T) {
	r := buildTestReport()

	var buf bytes.Buffer
	err := Markdown(&buf, r)
	if err != nil {
		t.Fatalf("Markdown() error: %v", err)
	}

	output := buf.String()

	// Check for main heading
	if !strings.Contains(output, "## kweli report") {
		t.Errorf("Markdown output missing ## kweli report heading")
	}

	// Check for server info
	if !strings.Contains(output, "https://fhir.example.com") {
		t.Errorf("Markdown output missing server URL")
	}
	if !strings.Contains(output, "FHIR 4.0.1") {
		t.Errorf("Markdown output missing FHIR version")
	}

	// Check for resources section
	if !strings.Contains(output, "### Resources") {
		t.Errorf("Markdown output missing ### Resources heading")
	}
	if !strings.Contains(output, "| Patient |") {
		t.Errorf("Markdown output missing Patient in resources table")
	}

	// Check for lies section
	if !strings.Contains(output, "### Lies") {
		t.Errorf("Markdown output missing ### Lies heading")
	}
	if !strings.Contains(output, "Patient?birthdate=error") {
		t.Errorf("Markdown output missing lied finding claim")
	}

	// Check for untested section
	if !strings.Contains(output, "### Untested") {
		t.Errorf("Markdown output missing ### Untested heading")
	}
	if !strings.Contains(output, "Observation?code=missing") {
		t.Errorf("Markdown output missing untested finding claim")
	}

	// Check for summary line
	if !strings.Contains(output, "Claims") {
		t.Errorf("Markdown output missing summary line with Claims")
	}
}

func TestTextPagingAndIncludes(t *testing.T) {
	// Test paging findings
	r := &model.Report{
		Server:      "https://example.com",
		FHIRVersion: "4.0.1",
		Resources: []model.ResourceSummary{
			{Resource: "Patient", Claimed: 5, Verified: 5, Rejected: 0, Ignored: 0, Untested: 0},
		},
		Findings: []model.Finding{
			{
				ID:       "patient-paging-1",
				Resource: "Patient",
				Claim:    "Patient?_count=50",
				Kind:     model.KindPaging,
				Status:   model.StatusVerified,
				Detail:   "Paging works",
			},
			{
				ID:       "observation-include-1",
				Resource: "Observation",
				Claim:    "Observation?_include=Observation:subject",
				Kind:     model.KindInclude,
				Status:   model.StatusVerified,
				Detail:   "Include works",
			},
			{
				ID:       "observation-revinclude-1",
				Resource: "Observation",
				Claim:    "Patient?_revinclude=Observation:subject",
				Kind:     model.KindRevInclude,
				Status:   model.StatusRejected,
				Detail:   "Not supported",
			},
		},
		Summary: model.Summary{Claims: 3, Verified: 2, Lies: 1, Untested: 0},
		Timing:  model.Timing{Duration: 5 * time.Second, DurationMS: 5000, Requests: 10},
	}

	var buf bytes.Buffer
	err := Text(&buf, r, false)
	if err != nil {
		t.Fatalf("Text() error: %v", err)
	}

	output := buf.String()

	// Paging: OK
	if !strings.Contains(output, "Paging: OK (Patient)") {
		t.Errorf("Text output should show Paging: OK")
	}

	// Includes: 1/2 OK
	if !strings.Contains(output, "Includes: 1/2 OK") {
		t.Errorf("Text output should show Includes: 1/2 OK")
	}
}

func TestTextOperations(t *testing.T) {
	r := &model.Report{
		Server:      "https://example.com",
		FHIRVersion: "4.0.1",
		Resources: []model.ResourceSummary{
			{Resource: "Patient", Claimed: 3, Verified: 3, Rejected: 0, Ignored: 0, Untested: 0},
		},
		Findings: []model.Finding{
			{
				ID:       "operation-1",
				Resource: "Patient",
				Claim:    "Patient/$everything",
				Kind:     model.KindOperation,
				Status:   model.StatusUntested,
				Detail:   "Operations not implemented in Phase 1",
			},
		},
		Summary: model.Summary{Claims: 1, Verified: 0, Lies: 0, Untested: 1},
		Timing:  model.Timing{Duration: 2 * time.Second, DurationMS: 2000, Requests: 5},
	}

	var buf bytes.Buffer
	err := Text(&buf, r, false)
	if err != nil {
		t.Fatalf("Text() error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "Operations: skipped") {
		t.Errorf("Text output should show Operations: skipped")
	}
}

func TestTextWarnings(t *testing.T) {
	r := &model.Report{
		Server:      "https://example.com",
		FHIRVersion: "4.0.1",
		Resources: []model.ResourceSummary{
			{Resource: "Patient", Claimed: 1, Verified: 1, Rejected: 0, Ignored: 0, Untested: 0},
		},
		Findings: []model.Finding{
			{
				ID:       "test-1",
				Resource: "Patient",
				Claim:    "Patient/1",
				Kind:     model.KindRead,
				Status:   model.StatusVerified,
				Detail:   "OK",
			},
		},
		Warnings: []string{"Warning 1", "Warning 2"},
		Summary:  model.Summary{Claims: 1, Verified: 1, Lies: 0, Untested: 0},
		Timing:   model.Timing{Duration: 1 * time.Second, DurationMS: 1000, Requests: 2},
	}

	var buf bytes.Buffer
	err := Text(&buf, r, false)
	if err != nil {
		t.Fatalf("Text() error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "WARNINGS (2)") {
		t.Errorf("Text output should show WARNINGS (2)")
	}
	if !strings.Contains(output, "Warning 1") {
		t.Errorf("Text output should show Warning 1")
	}
}

// buildTestReport creates a synthetic report for testing.
func TestTextOutputFormat(t *testing.T) {
	// Test that output matches the expected format from the brief
	r := &model.Report{
		Server:      "https://fhir.example.org/r4",
		FHIRVersion: "4.0.1",
		Software:    "Foo Server 3.2",
		Resources: []model.ResourceSummary{
			{Resource: "Patient", Claimed: 14, Verified: 11, Rejected: 1, Ignored: 2, Untested: 0},
			{Resource: "Observation", Claimed: 18, Verified: 12, Rejected: 3, Ignored: 2, Untested: 1},
			{Resource: "Condition", Claimed: 10, Verified: 10, Rejected: 0, Ignored: 0, Untested: 0},
		},
		Findings: []model.Finding{
			{ID: "obs-1", Resource: "Observation", Claim: "Observation?code=...", Kind: model.KindSearch, Status: model.StatusIgnored, Detail: "result set identical to unfiltered query"},
			{ID: "obs-2", Resource: "Observation", Claim: "Observation?value-quantity=..", Kind: model.KindSearch, Status: model.StatusRejected, Detail: "400 \"unknown search parameter\""},
			{ID: "pat-1", Resource: "Patient", Claim: "Patient?_include=Patient:general-practitioner", Kind: model.KindInclude, Status: model.StatusRejected, Detail: "400 (claimed in rest.resource.searchInclude)"},
			{ID: "obs-3", Resource: "Observation", Claim: "Observation?component-code", Kind: model.KindSearch, Status: model.StatusUntested, Detail: "no sample value found in first 50 resources"},
			{ID: "paging", Resource: "Patient", Claim: "Patient?_count=50", Kind: model.KindPaging, Status: model.StatusVerified, Detail: "Paging works"},
		},
		Summary: model.Summary{Claims: 214, Verified: 180, Lies: 22, Untested: 12},
		Timing:  model.Timing{Duration: 41 * time.Second, DurationMS: 41000, Requests: 187},
	}

	var buf bytes.Buffer
	err := Text(&buf, r, false)
	if err != nil {
		t.Fatalf("Text() error: %v", err)
	}

	output := buf.String()
	t.Logf("=== TEXT OUTPUT ===\n%s\n=== END ===", output)

	// Verify key elements are in correct order
	headerIdx := strings.Index(output, "kweli  https://fhir.example.org/r4")
	resourceTableIdx := strings.Index(output, "Resource      Claimed  Verified")
	pagingIdx := strings.Index(output, "Paging: OK (Patient)")
	liesIdx := strings.Index(output, "LIES (3)")
	untestedIdx := strings.Index(output, "UNTESTED (1)")
	summaryIdx := strings.Index(output, "Claims 214")

	if headerIdx == -1 {
		t.Errorf("Header not found")
	}
	if resourceTableIdx == -1 {
		t.Errorf("Resource table not found")
	}
	if pagingIdx == -1 {
		t.Errorf("Paging section not found")
	}
	if liesIdx == -1 {
		t.Errorf("LIES section not found")
	}
	if untestedIdx == -1 {
		t.Errorf("UNTESTED section not found")
	}
	if summaryIdx == -1 {
		t.Errorf("Summary line not found")
	}

	// Verify order
	if headerIdx > resourceTableIdx {
		t.Errorf("Header should come before resource table")
	}
	if resourceTableIdx > liesIdx {
		t.Errorf("Resource table should come before LIES section")
	}
	if liesIdx > summaryIdx {
		t.Errorf("LIES section should come before summary line")
	}
}

func buildTestReport() *model.Report {
	return &model.Report{
		Server:      "https://fhir.example.com",
		FHIRVersion: "4.0.1",
		Software:    "Example Server",
		Resources: []model.ResourceSummary{
			{
				Resource: "Patient",
				Claimed:  10,
				Verified: 8,
				Rejected: 1,
				Ignored:  1,
				Untested: 0,
			},
			{
				Resource: "Observation",
				Claimed:  15,
				Verified: 12,
				Rejected: 2,
				Ignored:  0,
				Untested: 1,
			},
		},
		Findings: []model.Finding{
			// Verified finding
			{
				ID:       "patient-read-1",
				Resource: "Patient",
				Claim:    "Patient/1",
				Kind:     model.KindRead,
				Status:   model.StatusVerified,
				Detail:   "Resource retrieved successfully",
			},
			// Rejected/Lie finding
			{
				ID:       "patient-search-birthdate",
				Resource: "Patient",
				Claim:    "Patient?birthdate=error",
				Kind:     model.KindSearch,
				Status:   model.StatusRejected,
				Detail:   "400 Unknown search parameter",
			},
			// Ignored/Lie finding
			{
				ID:       "observation-search-code",
				Resource: "Observation",
				Claim:    "Observation?code=system|code",
				Kind:     model.KindSearch,
				Status:   model.StatusIgnored,
				Detail:   "Result set identical to unfiltered query",
			},
			// Untested finding
			{
				ID:       "observation-search-component",
				Resource: "Observation",
				Claim:    "Observation?code=missing",
				Kind:     model.KindSearch,
				Status:   model.StatusUntested,
				Detail:   "No sample value found in first 50 resources",
			},
			// Inconclusive finding
			{
				ID:       "observation-vread",
				Resource: "Observation",
				Claim:    "Observation/1/_history/1",
				Kind:     model.KindVRead,
				Status:   model.StatusInconclusive,
				Detail:   "Server returned 200 but could not verify version semantics",
			},
			// Paging finding
			{
				ID:       "patient-paging",
				Resource: "Patient",
				Claim:    "Patient?_count=50",
				Kind:     model.KindPaging,
				Status:   model.StatusVerified,
				Detail:   "Paging works correctly",
			},
			// Include finding
			{
				ID:       "observation-include",
				Resource: "Observation",
				Claim:    "Observation?_include=Observation:subject",
				Kind:     model.KindInclude,
				Status:   model.StatusVerified,
				Detail:   "Include parameter works",
			},
			// Operation finding (Phase 1 always skipped)
			{
				ID:       "patient-operation",
				Resource: "Patient",
				Claim:    "Patient/$everything",
				Kind:     model.KindOperation,
				Status:   model.StatusUntested,
				Detail:   "Operations not implemented in Phase 1",
			},
		},
		Summary: model.Summary{
			Claims:   8,
			Verified: 3,
			Lies:     2,
			Untested: 3,
		},
		Timing: model.Timing{
			Duration:   41 * time.Second,
			DurationMS: 41000,
			Requests:   187,
		},
	}
}

// TestText_PagingWordingDistinguishesTimeoutFromBrokenServer is a
// regression test for a wording bug found running kweli against a real
// server (hapi.fhir.org): a paging hop that failed because the run's own
// --budget expired mid-request was labeled "broken", overstating what
// actually happened. A genuinely broken server (404/loop) should still
// say "broken"; a request failure/timeout should say "incomplete".
func TestText_PagingWordingDistinguishesTimeoutFromBrokenServer(t *testing.T) {
	r := &model.Report{
		Server: "https://example.org", FHIRVersion: "4.0.1",
		Findings: []model.Finding{
			{ID: "A/paging", Resource: "A", Kind: model.KindPaging, Status: model.StatusRejected, Detail: "hop 1: 404 fetching next link"},
			{ID: "B/paging", Resource: "B", Kind: model.KindPaging, Status: model.StatusUntested, Detail: "hop 1: request failed: context deadline exceeded"},
		},
	}
	var buf bytes.Buffer
	if err := Text(&buf, r, false); err != nil {
		t.Fatalf("Text() error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "broken (A: hop 1: 404") {
		t.Errorf("a genuinely broken (404) paging hop should say \"broken\": %s", out)
	}
	if !strings.Contains(out, "incomplete (B: hop 1: request failed: context deadline exceeded") {
		t.Errorf("a timed-out paging hop should say \"incomplete\", not \"broken\": %s", out)
	}
	if strings.Contains(out, "broken (B:") {
		t.Errorf("a timed-out paging hop must not be labeled \"broken\": %s", out)
	}
}

func missingReport() *model.Report {
	return &model.Report{
		Server: "https://example.org", FHIRVersion: "4.0.1",
		Findings: []model.Finding{
			{ID: "Coverage/expect", Resource: "Coverage", Claim: "Coverage", Kind: model.KindMissing, Status: model.StatusMissing,
				Detail: "US Core 6.1 requires this resource type; the CapabilityStatement doesn't claim it at all"},
			{ID: "Patient/expect/gender", Resource: "Patient", Claim: "Patient?gender=...", Kind: model.KindMissing, Status: model.StatusMissing,
				Detail: "US Core 6.1 requires Patient search by \"gender\"; not present in the CapabilityStatement's searchParam list"},
			{ID: "Patient/search/birthdate", Resource: "Patient", Claim: "Patient?birthdate=1990-01-01", Kind: model.KindSearch, Status: model.StatusVerified},
		},
		Summary: model.Summary{Claims: 1, Verified: 1, Missing: 2},
	}
}

func TestText_MissingSection(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, missingReport(), false); err != nil {
		t.Fatalf("Text() error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "MISSING (2)") {
		t.Errorf("missing header not found: %s", out)
	}
	if !strings.Contains(out, "Coverage") || !strings.Contains(out, "Patient?gender=...") {
		t.Errorf("missing findings not rendered: %s", out)
	}
	if !strings.Contains(out, "Missing 2") {
		t.Errorf("summary line missing the Missing count: %s", out)
	}
}

func TestText_NoMissingSectionWhenZero(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, buildTestReport(), false); err != nil {
		t.Fatalf("Text() error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "MISSING") {
		t.Errorf("MISSING section should be entirely absent when Summary.Missing is 0: %s", out)
	}
	if strings.Contains(out, "· Missing") {
		t.Errorf("summary line should not mention Missing when there are none: %s", out)
	}
}

func TestMarkdown_MissingSection(t *testing.T) {
	var buf bytes.Buffer
	if err := Markdown(&buf, missingReport()); err != nil {
		t.Fatalf("Markdown() error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "### Missing") {
		t.Errorf("missing heading not found: %s", out)
	}
	if !strings.Contains(out, "Coverage") {
		t.Errorf("missing finding not rendered: %s", out)
	}
}

func TestJSON_MissingRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, missingReport()); err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	var got model.Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if got.Summary.Missing != 2 {
		t.Errorf("Summary.Missing = %d, want 2", got.Summary.Missing)
	}
	var sawMissingKind bool
	for _, f := range got.Findings {
		if f.Kind == model.KindMissing {
			sawMissingKind = true
		}
	}
	if !sawMissingKind {
		t.Error("no finding round-tripped with Kind == missing")
	}
}
