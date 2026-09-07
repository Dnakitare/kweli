package probe

// Regression tests for bugs found in a deep-dive review before the first
// public push. Each test names the bug it locks in so a future change
// that reintroduces it fails loudly here instead of only showing up as a
// subtle wrong finding against a real server.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *client.Client {
	t.Helper()
	cl, _ := newTestClientWithURL(t, handler)
	return cl
}

func newTestClientWithURL(t *testing.T, handler http.HandlerFunc) (*client.Client, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return client.New(client.Config{BaseURL: srv.URL, RPS: 1000}), srv.URL
}

func bundleBody(t *testing.T, ids ...string) []byte {
	t.Helper()
	var entries []map[string]any
	for _, id := range ids {
		entries = append(entries, map[string]any{
			"fullUrl":  "urn:" + id,
			"resource": map[string]any{"resourceType": "X", "id": id},
			"search":   map[string]any{"mode": "match"},
		})
	}
	body, err := json.Marshal(map[string]any{"resourceType": "Bundle", "type": "searchset", "entry": entries})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// --- vread failure must be its own, actually-failing finding ---

func TestProbeRead_VReadFailureIsReportedAsALie(t *testing.T) {
	patient := map[string]any{
		"resourceType": "Patient",
		"id":           "p1",
		"meta":         map[string]any{"versionId": "2"},
	}
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Patient/p1":
			json.NewEncoder(w).Encode(patient)
		case "/Patient/p1/_history/2":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:p1", Resource: patient, SearchMode: "match"}}}

	findings := probeRead(context.Background(), cl, "Patient", baseline, true)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (read + vread): %+v", len(findings), findings)
	}
	read, vread := findings[0], findings[1]
	if read.Status != model.StatusVerified {
		t.Errorf("read status = %s, want verified", read.Status)
	}
	if vread.ID != "Patient/vread" || vread.Kind != model.KindVRead {
		t.Errorf("vread finding = %+v, want ID Patient/vread, Kind vread", vread)
	}
	if vread.Status != model.StatusRejected {
		t.Errorf("vread status = %s, want rejected — a failing vread must not be silently folded into a passing read", vread.Status)
	}
	if !vread.Status.IsLie() {
		t.Error("a broken vread must count as a lie")
	}
}

func TestProbeRead_VReadSuccessIsItsOwnVerifiedFinding(t *testing.T) {
	patient := map[string]any{
		"resourceType": "Patient",
		"id":           "p1",
		"meta":         map[string]any{"versionId": "2"},
	}
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(patient)
	})
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:p1", Resource: patient, SearchMode: "match"}}}

	findings := probeRead(context.Background(), cl, "Patient", baseline, true)
	if len(findings) != 2 || findings[1].Status != model.StatusVerified || findings[1].Kind != model.KindVRead {
		t.Fatalf("findings = %+v, want [read verified, vread verified]", findings)
	}
}

// --- paging Category must not force --fail-on paging on a clean pass ---

func TestTestPaging_VerifiedHasNoFailOnCategory(t *testing.T) {
	page2 := bundleBody(t, "a", "b")
	cl, baseURL := newTestClientWithURL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(page2)
	})
	baseline := sample.Set{
		Entries:  []sample.Entry{{FullURL: "urn:x", Resource: map[string]any{"id": "x"}}},
		NextLink: baseURL + "/Encounter?_page=2",
	}
	f := testPaging(context.Background(), cl, "Encounter", baseline)
	if f.Status != model.StatusVerified {
		t.Fatalf("Status = %s, want verified (setup bug?)", f.Status)
	}
	if got := f.FailOnCategory(); got != "" {
		t.Errorf("FailOnCategory() = %q on a VERIFIED paging finding, want \"\" — --fail-on paging would incorrectly fire on working paging", got)
	}
}

func TestTestPaging_BrokenHasPagingFailOnCategory(t *testing.T) {
	cl, baseURL := newTestClientWithURL(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	baseline := sample.Set{
		Entries:  []sample.Entry{{FullURL: "urn:x", Resource: map[string]any{"id": "x"}}},
		NextLink: baseURL + "/Encounter?_page=2",
	}
	f := testPaging(context.Background(), cl, "Encounter", baseline)
	if f.Status != model.StatusRejected {
		t.Fatalf("Status = %s, want rejected (setup bug?)", f.Status)
	}
	if got := f.FailOnCategory(); got != "paging" {
		t.Errorf("FailOnCategory() = %q on a broken paging finding, want \"paging\"", got)
	}
}

// --- rejected (strict) must be counted somewhere, not vanish from the table ---

// --- a real value AND a nonsense value both returning 0 must not be
// reported as "verified" (that combo is just as consistent with a filter
// that's broken for every input) ---

func obsWithCode() map[string]any {
	return map[string]any{
		"resourceType": "Observation",
		"id":           "obs-1",
		"code": map[string]any{
			"coding": []any{map[string]any{"system": "http://loinc.org", "code": "8480-6"}},
		},
	}
}

func TestTestSearchParam_BothQueriesEmpty_IsInconclusiveNotVerified(t *testing.T) {
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bundleBody(t)) // always empty, regardless of the query
	})
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:obs-1", Resource: obsWithCode(), SearchMode: "match"}}}

	f := testSearchParam(context.Background(), cl, "Observation", capstmt.SearchParam{Name: "code", Type: "token"}, baseline, "000001")
	if f.Status != model.StatusInconclusive {
		t.Errorf("Status = %s, want inconclusive — a real extracted value returning 0 results is not proof the filter works", f.Status)
	}
}

func TestTestSearchParam_NoTableEntry_BothEmpty_IsVerifiedWeak(t *testing.T) {
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bundleBody(t)) // always empty
	})
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:dr-1", Resource: map[string]any{"resourceType": "DiagnosticReport", "id": "dr-1"}, SearchMode: "match"}}}

	// "based-on" has no internal/sample/paths.go entry for DiagnosticReport
	// (deliberately, per brief §7 defect 12) — the positive query never runs.
	f := testSearchParam(context.Background(), cl, "DiagnosticReport", capstmt.SearchParam{Name: "based-on", Type: "reference"}, baseline, "000002")
	if f.Status != model.StatusVerified {
		t.Errorf("Status = %s, want verified (weak) — with no table entry, a nonsense value returning 0 results is the only evidence there is", f.Status)
	}
}

// --- a strict-handling retry that returns a different, non-empty,
// non-baseline set must not be mislabeled "identical to unfiltered" ---

func TestTestNonsense_StrictRetryDifferentSet_IsInconclusive(t *testing.T) {
	baselineIDs := bundleBody(t, "a", "b", "c")
	strictDifferent := bundleBody(t, "x", "y")
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Prefer") == "handling=strict" {
			w.Write(strictDifferent)
			return
		}
		w.Write(baselineIDs) // default handling: looks fully ignored
	})
	baseline, err := sample.ParseBundle("Observation", baselineIDs)
	if err != nil {
		t.Fatal(err)
	}

	f := testNonsense(context.Background(), cl, "Observation", capstmt.SearchParam{Name: "code", Type: "token"}, baseline, "000003",
		model.Finding{ID: "Observation/search/code", Resource: "Observation", Kind: model.KindSearch}, false)

	if f.Status != model.StatusInconclusive {
		t.Errorf("Status = %s, want inconclusive", f.Status)
	}
	if f.Status == model.StatusIgnored {
		t.Error("must not fall back to StatusIgnored for a strict result that differs from the baseline")
	}
}

// --- _revinclude must not look up its param against the wrong resource
// type (it structurally can never match), and must not silently mark
// every _revinclude claim untested without even trying the server ---

func TestTestInclude_RevIncludeActuallyQueriesTheServer(t *testing.T) {
	called := false
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		entries := []map[string]any{{
			"fullUrl":  "urn:obs-1",
			"resource": map[string]any{"resourceType": "Observation", "id": "obs-1"},
			"search":   map[string]any{"mode": "include"},
		}}
		body, _ := json.Marshal(map[string]any{"resourceType": "Bundle", "type": "searchset", "entry": entries})
		w.Write(body)
	})
	// "patient" is an Observation-level param, not a Patient-level one —
	// the old code looked it up as sample.Extract("Patient", "patient", ...)
	// which can never exist in the path table, so this claim was always
	// untested before the server was ever asked.
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:p1", Resource: map[string]any{"resourceType": "Patient", "id": "p1"}, SearchMode: "match"}}}

	f := testInclude(context.Background(), cl, "Patient", "Observation:patient", true, baseline)
	if !called {
		t.Fatal("_revinclude claim never reached the server — the populated-reference precheck must not apply to revinclude")
	}
	if f.Status != model.StatusVerified {
		t.Errorf("Status = %s, want verified (an include-mode entry came back)", f.Status)
	}
}

func TestTestInclude_EscapesSpecInQuery(t *testing.T) {
	var gotRawQuery string
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.Write(bundleBody(t))
	})
	baseline := sample.Set{Entries: []sample.Entry{{FullURL: "urn:c1", Resource: map[string]any{
		"resourceType": "Condition", "id": "c1", "subject": map[string]any{"reference": "Patient/p1"},
	}, SearchMode: "match"}}}

	testInclude(context.Background(), cl, "Condition", "Condition:subject", false, baseline)
	values, err := url.ParseQuery(gotRawQuery)
	if err != nil {
		t.Fatalf("server received an unparseable query %q: %v", gotRawQuery, err)
	}
	if got := values.Get("_include"); got != "Condition:subject" {
		t.Errorf("_include = %q, want \"Condition:subject\" round-tripped through escaping", got)
	}
}

// --- a resource-scoped operation claim (Patient's $everything, say) must
// produce a finding, not vanish silently ---

func TestProbeResource_ResourceScopedOperationProducesAFinding(t *testing.T) {
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bundleBody(t, "p1"))
	})
	entry := capstmt.ResourceEntry{
		Type:        "Patient",
		Interaction: []capstmt.InteractionRef{{Code: "search-type"}},
		Operation:   []capstmt.OperationRef{{Name: "everything"}},
	}
	out := probeResource(context.Background(), cl, "Patient", entry, func() string { return "000004" }, false)

	var found bool
	for _, f := range out.findings {
		if f.ID == "Patient/operation/everything" {
			found = true
			if f.Kind != model.KindOperation {
				t.Errorf("Kind = %s, want operation", f.Kind)
			}
		}
	}
	if !found {
		t.Error("resource-scoped operation claim produced no finding at all")
	}
}

// --- an unfiltered baseline search that 500s must produce a finding, not
// disappear with no record of what happened ---

func TestProbeResource_BaselineSearchServerErrorProducesAFinding(t *testing.T) {
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	entry := capstmt.ResourceEntry{Type: "Observation", Interaction: []capstmt.InteractionRef{{Code: "search-type"}}}
	out := probeResource(context.Background(), cl, "Observation", entry, func() string { return "000005" }, false)

	if len(out.findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one (the failed baseline search)", out.findings)
	}
	f := out.findings[0]
	if !f.Status.IsLie() {
		t.Errorf("Status = %s, want a lie status — a resource that claims search-type but 500s on the most basic search is not \"nothing to report\"", f.Status)
	}
}

func TestStatusRejectedStrict_CountsAsUntestedCategory(t *testing.T) {
	if !model.StatusRejectedStrict.IsUntestedCategory() {
		t.Error("StatusRejectedStrict.IsUntestedCategory() = false, want true (it must land in Claimed's Untested bucket and the report's UNTESTED section, or the numbers won't add up)")
	}
	if model.StatusRejectedStrict.IsLie() {
		t.Error("StatusRejectedStrict.IsLie() = true, want false (the brief is explicit: this is the server being truthful about a gap)")
	}
	if got := model.StatusRejectedStrict.FailOnCategory(); got != "untested" {
		t.Errorf("FailOnCategory() = %q, want \"untested\"", got)
	}
}
