package probe

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

// includeParam extracts the search param name out of a searchInclude/
// searchRevInclude value like "Patient:general-practitioner" or
// "Patient:general-practitioner:Organization" (the optional third segment
// is a target-type hint kweli doesn't need).
func includeParam(spec string) string {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// testInclude implements brief §5.2d: GET T?_count=5&_include=T:param (or
// &_revinclude=), and verify at least one entry has search.mode=="include"
// when the sample set actually has a populated reference at that param.
func testInclude(ctx context.Context, cl *client.Client, resourceType, spec string, rev bool, baseline sample.Set) model.Finding {
	kind := model.KindInclude
	verb := "_include"
	if rev {
		kind = model.KindRevInclude
		verb = "_revinclude"
	}
	id := fmt.Sprintf("%s/%s/%s", resourceType, string(kind), spec)
	claim := fmt.Sprintf("%s?%s=%s", resourceType, verb, spec)
	f := model.Finding{ID: id, Resource: resourceType, Claim: claim, Kind: kind}

	// For a forward _include, spec's param (e.g. "subject" in
	// "Condition:subject") belongs to resourceType itself, so we can check
	// whether the sample set actually has a populated reference there
	// before spending a request on it — an empty reference means we can't
	// prove the server does anything (§5.2d).
	//
	// For _revinclude, spec's source type is the *other* resource (e.g.
	// "Observation:patient" declared on Patient means "Observations
	// referencing this patient via their own 'patient' param") — the
	// param belongs to a resource type kweli isn't sampling here, so this
	// precheck doesn't apply. Just run the query and, if nothing comes
	// back, report untested rather than guessing it's a lie: we have no
	// way to tell "no referencing resources exist" from "server ignores
	// revinclude" without cross-resource-type sampling Phase 1 doesn't do.
	if !rev {
		param := includeParam(spec)
		populated := false
		if param != "" {
			for _, e := range baseline.MatchEntries() {
				if _, ok := sample.Extract(resourceType, param, e.Resource); ok {
					populated = true
					break
				}
			}
		}
		if !populated {
			f.Status = model.StatusUntested
			f.Detail = "no sample resource had a populated reference at this param"
			return f
		}
	}

	path := fmt.Sprintf("%s?_count=5&%s=%s", resourceType, verb, url.QueryEscape(spec))
	resp, err := cl.Get(ctx, path)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		return f
	}
	f.Request, f.StatusCode = "GET "+path, resp.StatusCode
	if resp.StatusCode >= 400 {
		f.Status = model.StatusRejected
		f.Detail = fmt.Sprintf("%d requesting %s", resp.StatusCode, claim)
		return f
	}
	s, err := sample.ParseBundle(resourceType, resp.Body)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("200 but body did not parse as a Bundle: %v", err)
		return f
	}
	for _, e := range s.Entries {
		if e.SearchMode == "include" {
			f.Status = model.StatusVerified
			f.Detail = "at least one included entry present"
			return f
		}
	}
	if rev {
		f.Status = model.StatusUntested
		f.Detail = "no included entry came back; can't tell whether that's because no resource actually references this sample or because the server ignores _revinclude"
		return f
	}
	f.Status = model.StatusIgnored
	f.Detail = "no entry with search.mode=include, despite a populated reference in the sample set"
	return f
}
