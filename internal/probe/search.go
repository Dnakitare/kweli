package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

// hasWarningOutcome reports whether a Bundle response body carries an
// OperationOutcome entry (or R5 Bundle.issue) with severity=warning, per
// brief §5.3 step 4 — servers that do this for unknown params are
// classified as ignored, and the warning text is surfaced.
func hasWarningOutcome(body []byte) (string, bool) {
	var raw struct {
		Issue []struct {
			Severity    string `json:"severity"`
			Diagnostics string `json:"diagnostics"`
			Details     struct {
				Text string `json:"text"`
			} `json:"details"`
		} `json:"issue"`
		Entry []struct {
			Resource struct {
				ResourceType string `json:"resourceType"`
				Issue        []struct {
					Severity    string `json:"severity"`
					Diagnostics string `json:"diagnostics"`
					Details     struct {
						Text string `json:"text"`
					} `json:"details"`
				} `json:"issue"`
			} `json:"resource"`
		} `json:"entry"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return "", false
	}
	for _, i := range raw.Issue {
		if i.Severity == "warning" {
			return warningText(i.Diagnostics, i.Details.Text), true
		}
	}
	for _, e := range raw.Entry {
		if e.Resource.ResourceType != "OperationOutcome" {
			continue
		}
		for _, i := range e.Resource.Issue {
			if i.Severity == "warning" {
				return warningText(i.Diagnostics, i.Details.Text), true
			}
		}
	}
	return "", false
}

func warningText(diagnostics, text string) string {
	if diagnostics != "" {
		return diagnostics
	}
	return text
}

// testSearchParam runs the two-query test from brief §5.3 for one claimed
// search param and returns the resulting Finding.
func testSearchParam(ctx context.Context, cl *client.Client, resourceType string, p capstmt.SearchParam, baseline sample.Set, randomSuffix string) model.Finding {
	id := resourceType + "/search/" + p.Name
	f := model.Finding{ID: id, Resource: resourceType, Kind: model.KindSearch}

	// Step 2: positive query, if the curated table has a real value.
	var positiveValue string
	havePositive := false
	for _, e := range baseline.MatchEntries() {
		raw, ok := sample.Extract(resourceType, p.Name, e.Resource)
		if !ok {
			continue
		}
		v, ok := sample.FormatSearchValue(p.Type, raw)
		if !ok {
			continue
		}
		positiveValue, havePositive = v, true
		break
	}

	if havePositive {
		f.Claim = fmt.Sprintf("%s?%s=%s", resourceType, p.Name, positiveValue)
		path := fmt.Sprintf("%s?%s=%s&_count=50", resourceType, url.QueryEscape(p.Name), url.QueryEscape(positiveValue))
		resp, err := cl.Get(ctx, path)
		if err != nil {
			f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
			return f
		}
		f.Request, f.StatusCode = "GET "+path, resp.StatusCode
		if resp.StatusCode >= 400 {
			f.Status = model.StatusRejected
			f.Detail = fmt.Sprintf("%d searching %s=%s", resp.StatusCode, p.Name, positiveValue)
			return f
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			s, err := sample.ParseBundle(resourceType, resp.Body)
			if err == nil {
				entries := s.MatchEntries()
				switch {
				case len(entries) == 0:
					// Zero results for a value we pulled straight out of a
					// sample resource almost always means the server
					// evaluated the param and (correctly or not) excluded
					// everything else; fall through to the nonsense query
					// to characterize it further.
				case sample.SameIDs(baseline, s):
					// Identical to the unfiltered baseline: the param had no
					// effect at all rather than a loose/wrong effect. Don't
					// call this "ignored (partial)" — defer to the nonsense
					// query (and its strict-handling retry) to confirm a
					// full ignore vs. a strict-only gap.
				default:
					allMatch := true
					for _, e := range entries {
						if !sample.Matches(resourceType, p.Name, p.Type, positiveValue, e.Resource) {
							allMatch = false
							break
						}
					}
					if allMatch {
						f.Status = model.StatusVerified
						f.Detail = "every returned entry matched the searched value"
						return f
					}
					f.Status = model.StatusIgnoredPartial
					f.Detail = "server filtered loosely: returned a different result set than the unfiltered baseline, but not every entry in it matches the searched value"
					return f
				}
			}
		}
	}

	// Step 3: nonsense query. Always run when step 2 didn't already
	// resolve the claim (rejected / ignored-partial return early above).
	// havePositive tells testNonsense whether a real-value query already
	// ran and failed to resolve anything (zero results, or identical to
	// the unfiltered baseline) — as opposed to never running at all
	// because there was no table entry — since those two situations
	// deserve different verdicts when the nonsense query also comes back
	// empty (see the positiveRan branch below).
	return testNonsense(ctx, cl, resourceType, p, baseline, randomSuffix, f, havePositive)
}

func testNonsense(ctx context.Context, cl *client.Client, resourceType string, p capstmt.SearchParam, baseline sample.Set, randomSuffix string, f model.Finding, positiveRan bool) model.Finding {
	nonsense := sample.NonsenseValue(p.Type, resourceType, randomSuffix)
	if f.Claim == "" {
		f.Claim = fmt.Sprintf("%s?%s=%s", resourceType, p.Name, nonsense)
	}
	path := fmt.Sprintf("%s?%s=%s&_count=50", resourceType, url.QueryEscape(p.Name), url.QueryEscape(nonsense))

	resp, status, err := doSearch(ctx, cl, path)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		return f
	}
	f.Request, f.StatusCode = "GET "+path, status

	if status >= 400 {
		f.Status = model.StatusRejected
		f.Detail = fmt.Sprintf("%d searching %s=%s", status, p.Name, nonsense)
		return f
	}

	if warn, ok := hasWarningOutcome(resp); ok {
		f.Status = model.StatusIgnored
		f.Detail = "server returned a warning OperationOutcome for an unrecognized param instead of rejecting or filtering: " + warn
		return f
	}

	s, err := sample.ParseBundle(resourceType, resp)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("200 but body did not parse as a Bundle: %v", err)
		return f
	}
	entries := s.MatchEntries()

	if len(entries) == 0 {
		if !positiveRan {
			// No curated table entry for this param at all: the nonsense
			// query is the only evidence we have, and it shows the param
			// has *some* effect. Call it verified, but weakly.
			f.Status = model.StatusVerified
			f.Detail = "no curated sample value for this param; nonsense value returned 0 results (verified, weak)"
			return f
		}
		// A positive query DID run (on a value pulled straight out of a
		// real matching resource) and still didn't resolve the claim —
		// either it also returned 0 (a real value should almost never
		// return nothing for a working filter) or it matched the
		// unfiltered baseline exactly (no visible effect). Either way,
		// "nonsense also returns 0" doesn't confirm the param works; it's
		// just as consistent with a filter that's broken for every input.
		f.Status = model.StatusInconclusive
		f.Detail = "a real extracted value didn't produce a clean verified/ignored result, and the nonsense value returned 0 results too — not enough to confirm the param actually works"
		return f
	}

	if sample.SameIDs(baseline, s) {
		// Same result set as the unfiltered baseline under default
		// handling: could be a genuine silent ignore, or a server that's
		// only lenient by default. Disambiguate with strict handling
		// (brief: "honour Prefer: handling=strict on the nonsense query").
		strictResp, strictStatus, err := doSearch(ctx, cl, path, client.WithStrictHandling())
		if err == nil {
			if strictStatus >= 400 {
				f.Status = model.StatusRejectedStrict
				f.Detail = fmt.Sprintf("ignored under default handling, but %d under Prefer: handling=strict — server is truthfully reporting the gap when asked strictly", strictStatus)
				f.StatusCode = strictStatus
				return f
			}
			if strictSet, perr := sample.ParseBundle(resourceType, strictResp); perr == nil {
				strictEntries := strictSet.MatchEntries()
				switch {
				case len(strictEntries) == 0:
					// Strict handling revealed the param actually filters
					// (the nonsense value now returns nothing) even though
					// default handling silently let it through.
					f.Status = model.StatusVerified
					f.StatusCode = strictStatus
					f.Detail = "ignored under default handling, but a nonsense value returned 0 results under Prefer: handling=strict — the param does have an effect once strict handling is requested"
					return f
				case sample.SameIDs(baseline, strictSet):
					f.Status = model.StatusIgnored
					f.Detail = "result set identical to unfiltered query, even under Prefer: handling=strict"
					return f
				default:
					// A different, non-empty, non-baseline-identical set
					// under strict handling doesn't cleanly confirm either
					// verdict — don't mislabel it as "identical".
					f.Status = model.StatusInconclusive
					f.StatusCode = strictStatus
					f.Detail = "ignored under default handling; Prefer: handling=strict returned a different, non-empty result that doesn't cleanly confirm the param works or is ignored"
					return f
				}
			}
		}
		f.Status = model.StatusIgnored
		f.Detail = "result set identical to unfiltered query"
		return f
	}

	f.Status = model.StatusInconclusive
	f.Detail = "nonsense value returned a different, non-empty result set (neither clearly filtered nor clearly ignored)"
	return f
}

// doSearch is a small helper so both the plain and strict-handling
// requests share status/body extraction.
func doSearch(ctx context.Context, cl *client.Client, path string, opts ...client.Option) ([]byte, int, error) {
	resp, err := cl.Get(ctx, path, opts...)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.StatusCode, nil
}
