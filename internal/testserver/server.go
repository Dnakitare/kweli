// Package testserver is the acceptance-test fixture from brief §7: an
// httptest.Server serving a synthetic R4 CapabilityStatement and synthetic
// resources, with twelve deliberately planted lies. NewTruthful serves the
// same shape of data with every one of those lies fixed, as the "zero
// lies" control.
//
// Data is generated deterministically (see data.go) — no randomness, so
// tests never flake.
package testserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/Dnakitare/kweli/internal/sample"
)

type server struct {
	truthful bool
	data     map[string][]map[string]any
}

// New returns a server implementing every defect from brief §7:
//  1. Observation?code — ignored
//  2. Patient?birthdate — rejected (400)
//  3. Patient?name — ignored (partial)
//  4. Condition?_include=Condition:subject — rejected
//  5. Encounter paging — next link 404s on hop 2
//  6. Procedure paging — hop 3 repeats hop 2's entries
//  7. Observation?_count — ignored (always 20)
//  8. MedicationRequest/read — 500
//  9. Patient?_lastUpdated — ignored
//  10. AllergyIntolerance — search returns 0 entries (untested, not a lie)
//  11. Immunization?date — honoured only under Prefer: handling=strict
//  12. DiagnosticReport?based-on — not in kweli's path table; must still
//     classify via the nonsense-only query
func New() *httptest.Server {
	return newServer(false)
}

// NewTruthful serves the same resource shapes with every defect above
// fixed — the "zero lies" control from brief §7.
func NewTruthful() *httptest.Server {
	return newServer(true)
}

func newServer(truthful bool) *httptest.Server {
	s := &server{
		truthful: truthful,
		data: map[string][]map[string]any{
			"Patient":            makePatients(15),
			"Observation":        makeObservations(30),
			"Condition":          makeConditions(10),
			"Encounter":          makeEncounters(25),
			"Procedure":          makeProcedures(25),
			"MedicationRequest":  makeMedicationRequests(12),
			"Immunization":       makeImmunizations(10),
			"DiagnosticReport":   makeDiagnosticReports(8),
			"AllergyIntolerance": nil, // defect 10: genuinely empty, both variants
		},
	}
	if truthful {
		s.data["AllergyIntolerance"] = makeAllergyIntolerances(6)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata", s.handleMetadata)
	mux.HandleFunc("/", s.handleResource)
	return httptest.NewServer(mux)
}

func (s *server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, capabilityStatementJSON())
}

func (s *server) handleResource(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeOutcome(w, http.StatusNotFound, "not-found", "no resource path")
		return
	}
	resourceType := parts[0]
	if _, known := s.data[resourceType]; !known {
		writeOutcome(w, http.StatusNotFound, "not-found", "unknown resource type "+resourceType)
		return
	}

	switch len(parts) {
	case 1:
		s.handleSearch(w, r, resourceType)
	case 2:
		s.handleRead(w, r, resourceType, parts[1])
	default:
		writeOutcome(w, http.StatusNotFound, "not-found", "unsupported path")
	}
}

func (s *server) handleRead(w http.ResponseWriter, r *http.Request, resourceType, id string) {
	if resourceType == "MedicationRequest" {
		// Defect 8, both variants except NewTruthful.
		if !s.truthful {
			writeOutcome(w, http.StatusInternalServerError, "exception", "internal error reading "+id)
			return
		}
	}
	for _, res := range s.data[resourceType] {
		if res["id"] == id {
			writeJSON(w, http.StatusOK, res)
			return
		}
	}
	writeOutcome(w, http.StatusNotFound, "not-found", resourceType+"/"+id+" not found")
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request, resourceType string) {
	q := r.URL.Query()
	all := s.data[resourceType]

	switch resourceType {
	case "Observation":
		s.searchObservation(w, r, all)
	case "Patient":
		s.searchPatient(w, q, all)
	case "Condition":
		s.searchCondition(w, q, all)
	case "Encounter":
		s.searchPaged(w, r, resourceType, all, encounterBroken(s.truthful))
	case "Procedure":
		s.searchPaged(w, r, resourceType, all, procedureBroken(s.truthful))
	case "Immunization":
		s.searchImmunization(w, r, q, all)
	case "DiagnosticReport":
		s.searchDiagnosticReport(w, q, all)
	case "AllergyIntolerance":
		s.searchAllergyIntolerance(w, q, all)
	default:
		// MedicationRequest: plain, correctly-behaved search with no
		// claimed defect — honour _count, no other filters.
		entries, _ := paginate(all, effectiveCount(q, len(all)))
		writeBundle(w, entries, "")
	}
}

// --- Observation: defects 1 (code ignored) and 7 (_count ignored) ---

func (s *server) searchObservation(w http.ResponseWriter, r *http.Request, all []map[string]any) {
	if s.truthful {
		q := r.URL.Query()
		var filtered []map[string]any
		if code := q.Get("code"); code != "" {
			for _, res := range all {
				if sample.Matches("Observation", "code", "token", code, res) {
					filtered = append(filtered, res)
				}
			}
		} else {
			filtered = all
		}
		entries, _ := paginate(filtered, effectiveCount(r.URL.Query(), len(filtered)))
		writeBundle(w, entries, "")
		return
	}
	// Defects 1 & 7: every query param, including _count, is ignored —
	// always the same fixed first 20, regardless of what's asked for.
	n := 20
	if n > len(all) {
		n = len(all)
	}
	writeBundle(w, all[:n], "")
}

// --- Patient: defects 2 (birthdate rejected), 3 (name partial), 9 (_lastUpdated ignored) ---

func (s *server) searchPatient(w http.ResponseWriter, q url.Values, all []map[string]any) {
	vals := q
	if v := vals.Get("birthdate"); v != "" {
		if !s.truthful {
			writeOutcome(w, http.StatusBadRequest, "not-supported", "unknown search parameter \"birthdate\"")
			return
		}
		var filtered []map[string]any
		for _, res := range all {
			if sample.Matches("Patient", "birthdate", "date", v, res) {
				filtered = append(filtered, res)
			}
		}
		entries, _ := paginate(filtered, effectiveCount(vals, len(filtered)))
		writeBundle(w, entries, "")
		return
	}
	if v := vals.Get("name"); v != "" {
		var filtered []map[string]any
		if s.truthful {
			for _, res := range all {
				if sample.Matches("Patient", "name", "string", v, res) {
					filtered = append(filtered, res)
				}
			}
		} else {
			// Defect 3: loose first-letter-only match instead of a real
			// prefix match — returns entries that don't actually match.
			want := strings.ToUpper(v[:1])
			for _, res := range all {
				fam, _ := sample.Extract("Patient", "name", res)
				famStr, _ := fam.(string)
				if strings.HasPrefix(strings.ToUpper(famStr), want) {
					filtered = append(filtered, res)
				}
			}
		}
		entries, _ := paginate(filtered, effectiveCount(vals, len(filtered)))
		writeBundle(w, entries, "")
		return
	}
	if v := vals.Get("_lastUpdated"); v != "" {
		if !s.truthful {
			// Defect 9: ignored entirely.
			entries, _ := paginate(all, effectiveCount(vals, len(all)))
			writeBundle(w, entries, "")
			return
		}
		var filtered []map[string]any
		for _, res := range all {
			if sample.Matches("Patient", "_lastUpdated", "date", v, res) {
				filtered = append(filtered, res)
			}
		}
		entries, _ := paginate(filtered, effectiveCount(vals, len(filtered)))
		writeBundle(w, entries, "")
		return
	}
	entries, _ := paginate(all, effectiveCount(vals, len(all)))
	writeBundle(w, entries, "")
}

// --- Condition: defect 4 (_include rejected) ---

func (s *server) searchCondition(w http.ResponseWriter, q url.Values, all []map[string]any) {
	vals := q
	if inc := vals.Get("_include"); inc == "Condition:subject" {
		if !s.truthful {
			writeOutcome(w, http.StatusBadRequest, "not-supported", "_include=Condition:subject is not supported")
			return
		}
		entries, _ := paginate(all, effectiveCount(vals, len(all)))
		var included []map[string]any
		seen := map[string]bool{}
		for _, res := range entries {
			ref, _ := sample.Extract("Condition", "subject", res)
			refStr, _ := ref.(string)
			id := strings.TrimPrefix(refStr, "Patient/")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			// A minimal included Patient stub is enough to prove the
			// server actually resolved the reference.
			included = append(included, map[string]any{"resourceType": "Patient", "id": id, "_kweliIncluded": true})
		}
		writeBundleMixed(w, entries, included)
		return
	}
	entries, _ := paginate(all, effectiveCount(vals, len(all)))
	writeBundle(w, entries, "")
}

// --- Encounter / Procedure paging: defects 5 and 6 ---

type pagingDefect int

const (
	pagingNone pagingDefect = iota
	pagingHop2NotFound
	pagingHop3Repeats
)

func encounterBroken(truthful bool) pagingDefect {
	if truthful {
		return pagingNone
	}
	return pagingHop2NotFound
}

func procedureBroken(truthful bool) pagingDefect {
	if truthful {
		return pagingNone
	}
	return pagingHop3Repeats
}

const pageSize = 10

// searchPaged implements the shared paging behaviour for Encounter and
// Procedure: an internal page size of 10 regardless of a larger requested
// _count (a tolerated shrink, per brief §8), navigated via an internal
// "_page" cursor query param embedded in each Bundle's next link.
func (s *server) searchPaged(w http.ResponseWriter, r *http.Request, resourceType string, all []map[string]any, defect pagingDefect) {
	vals := r.URL.Query()
	page := 1
	if p := vals.Get("_page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			page = n
		}
	}

	if defect == pagingHop2NotFound && page == 2 {
		writeOutcome(w, http.StatusNotFound, "not-found", "page 2 not found")
		return
	}

	count := pageSize
	if c := vals.Get("_count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n < pageSize {
			count = n
		}
	}
	// Small explicit _count requests (the §5.2f probe uses _count=2) are
	// honoured exactly; larger/absent requests are capped at pageSize.

	var entries []map[string]any
	var haveNext bool
	var nextPage int
	switch {
	case defect == pagingHop3Repeats && page == 3:
		// Defect 6: hop 3 repeats hop 2's entries instead of advancing.
		entries = sliceWindow(all, 1, pageSize) // same window as page 2
	case page == 1 && count < pageSize:
		// A small explicit _count on page 1 (the §5.2f probe): honour it
		// exactly, no next link — this is a distinct request from the
		// unfiltered §5.2a baseline that establishes real pagination.
		entries = sliceWindow(all, 0, count)
	default:
		start := (page - 1) * pageSize
		entries = sliceWindow(all, start/pageSize, count)
		if start+len(entries) < len(all) && page < 3 {
			haveNext, nextPage = true, page+1
		}
	}
	if defect == pagingHop3Repeats && page == 2 {
		haveNext, nextPage = true, 3
	}

	var next string
	if haveNext {
		next = fmt.Sprintf("%s://%s/%s?_page=%d&_count=%d", scheme(r), r.Host, resourceType, nextPage, pageSize)
	}

	writeBundle(w, entries, next)
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func sliceWindow(all []map[string]any, windowIndex, count int) []map[string]any {
	start := windowIndex * pageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + count
	if end > len(all) {
		end = len(all)
	}
	if end > start+pageSize {
		end = start + pageSize
	}
	return all[start:end]
}

// --- Immunization: defect 11 (date honoured only under strict handling) ---

func (s *server) searchImmunization(w http.ResponseWriter, r *http.Request, q url.Values, all []map[string]any) {
	vals := q
	v := vals.Get("date")
	if v == "" {
		entries, _ := paginate(all, effectiveCount(vals, len(all)))
		writeBundle(w, entries, "")
		return
	}
	if s.truthful {
		var filtered []map[string]any
		for _, res := range all {
			if sample.Matches("Immunization", "date", "date", v, res) {
				filtered = append(filtered, res)
			}
		}
		entries, _ := paginate(filtered, effectiveCount(vals, len(filtered)))
		writeBundle(w, entries, "")
		return
	}
	if strings.Contains(r.Header.Get("Prefer"), "handling=strict") {
		writeOutcome(w, http.StatusBadRequest, "not-supported", "unknown search parameter \"date\" (strict handling requested)")
		return
	}
	// Ignored under default (lenient) handling.
	entries, _ := paginate(all, effectiveCount(vals, len(all)))
	writeBundle(w, entries, "")
}

// --- DiagnosticReport: defect 12 (based-on, not in kweli's path table) ---

func (s *server) searchDiagnosticReport(w http.ResponseWriter, q url.Values, all []map[string]any) {
	vals := q
	v := vals.Get("based-on")
	if v == "" {
		entries, _ := paginate(all, effectiveCount(vals, len(all)))
		writeBundle(w, entries, "")
		return
	}
	// based-on is genuinely, correctly filtered in BOTH variants — the
	// point of this fixture item is that kweli's curated path table has
	// no entry for it, so it must fall through to the nonsense-only
	// query and still reach a definitive verdict (brief §5.3 step 3,
	// §7.12), not that the server lies about it.
	var filtered []map[string]any
	for _, res := range all {
		basedOn, _ := res["basedOn"].([]any)
		if len(basedOn) == 0 {
			continue
		}
		first, _ := basedOn[0].(map[string]any)
		ref, _ := first["reference"].(string)
		if ref == v {
			filtered = append(filtered, res)
		}
	}
	entries, _ := paginate(filtered, effectiveCount(vals, len(filtered)))
	writeBundle(w, entries, "")
}

// --- AllergyIntolerance: no planted defect (its own defect, #10, is an
// empty dataset — see New()); clinical-status filters correctly in both
// variants whenever there's data to filter. ---

func (s *server) searchAllergyIntolerance(w http.ResponseWriter, q url.Values, all []map[string]any) {
	v := q.Get("clinical-status")
	if v == "" {
		entries, _ := paginate(all, effectiveCount(q, len(all)))
		writeBundle(w, entries, "")
		return
	}
	var filtered []map[string]any
	for _, res := range all {
		if sample.Matches("AllergyIntolerance", "clinical-status", "token", v, res) {
			filtered = append(filtered, res)
		}
	}
	entries, _ := paginate(filtered, effectiveCount(q, len(filtered)))
	writeBundle(w, entries, "")
}

// --- shared helpers ---

func effectiveCount(q url.Values, total int) int {
	if c := q.Get("_count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n >= 0 {
			if n > 50 {
				n = 50
			}
			return n
		}
	}
	if total > 50 {
		return 50
	}
	return total
}

func paginate(all []map[string]any, count int) ([]map[string]any, bool) {
	if count > len(all) {
		count = len(all)
	}
	return all[:count], count < len(all)
}

// writeBundle writes a plain searchset Bundle of match entries, with an
// optional next link.
func writeBundle(w http.ResponseWriter, entries []map[string]any, next string) {
	writeBundleFull(w, entries, nil, next)
}

// writeBundleMixed writes a searchset Bundle whose "match" entries are
// matchEntries and whose "include" entries (if any) are includeEntries,
// with no next link (only Condition's include defect test uses this).
func writeBundleMixed(w http.ResponseWriter, matchEntries, includeEntries []map[string]any) {
	writeBundleFull(w, matchEntries, includeEntries, "")
}

func writeBundleFull(w http.ResponseWriter, matchEntries, includeEntries []map[string]any, next string) {
	var entries []any
	for _, res := range matchEntries {
		entries = append(entries, map[string]any{
			"fullUrl":  "urn:kweli:" + fmt.Sprint(res["resourceType"]) + "/" + fmt.Sprint(res["id"]),
			"resource": res,
			"search":   map[string]any{"mode": "match"},
		})
	}
	for _, res := range includeEntries {
		entries = append(entries, map[string]any{
			"fullUrl":  "urn:kweli:" + fmt.Sprint(res["resourceType"]) + "/" + fmt.Sprint(res["id"]),
			"resource": res,
			"search":   map[string]any{"mode": "include"},
		})
	}
	bundle := map[string]any{
		"resourceType": "Bundle",
		"type":         "searchset",
		"total":        len(matchEntries),
		"entry":        entries,
	}
	if next != "" {
		bundle["link"] = []any{map[string]any{"relation": "next", "url": next}}
	}
	writeJSON(w, http.StatusOK, bundle)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/fhir+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOutcome(w http.ResponseWriter, status int, code, diagnostics string) {
	writeJSON(w, status, map[string]any{
		"resourceType": "OperationOutcome",
		"issue": []any{
			map[string]any{"severity": "error", "code": code, "diagnostics": diagnostics},
		},
	})
}
