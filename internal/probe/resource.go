package probe

import (
	"context"
	"fmt"
	"strings"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

// skippableParam reports whether a search param should be skipped per
// brief §5.2c: "special" typed params, and underscore params other than
// _id, _lastUpdated, _tag, _profile, _security (those are commonly lied
// about, so they're tested like any other param).
func skippableParam(p capstmt.SearchParam) bool {
	if p.Type == "special" {
		return true
	}
	if !strings.HasPrefix(p.Name, "_") {
		return false
	}
	switch p.Name {
	case "_id", "_lastUpdated", "_tag", "_profile", "_security":
		return false
	default:
		return true
	}
}

// probeResource runs the full §5.2 pipeline for one resource type,
// sequentially (probes within a resource are not parallel; only different
// resources run concurrently — see run.go).
func probeResource(ctx context.Context, cl *client.Client, resourceType string, entry capstmt.ResourceEntry, nextSuffix func() string, probeOperations bool) resourceOutcome {
	out := resourceOutcome{summary: model.ResourceSummary{Resource: resourceType}}

	add := func(f model.Finding) {
		out.findings = append(out.findings, f)
		out.summary.Claimed++
		switch {
		case f.Status == model.StatusRejected:
			out.summary.Rejected++
		case f.Status == model.StatusIgnored || f.Status == model.StatusIgnoredPartial:
			out.summary.Ignored++
		case f.Status.IsUntestedCategory():
			out.summary.Untested++
		case f.Status == model.StatusVerified:
			out.summary.Verified++
		}
	}

	if ctx.Err() != nil {
		add(model.Finding{
			ID: resourceType + "/budget", Resource: resourceType, Claim: resourceType,
			Kind: model.KindSearch, Status: model.StatusUntested, Detail: "run budget exceeded before this resource was probed",
		})
		return out
	}

	canSearch := entry.HasInteraction("search-type")

	var baseline sample.Set
	haveBaseline := false
	if canSearch {
		resp, err := cl.Get(ctx, resourceType+"?_count=50")
		if err != nil {
			add(model.Finding{
				ID: resourceType + "/search", Resource: resourceType, Claim: resourceType + "?_count=50",
				Kind: model.KindSearch, Status: model.StatusUntested, Detail: fmt.Sprintf("unfiltered search failed: %v", err),
			})
		} else if resp.StatusCode == 403 {
			out.warnings = append(out.warnings, fmt.Sprintf("%s: unfiltered search returned 403 (token may be scoped narrower than the CapabilityStatement claims)", resourceType))
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if s, err := sample.ParseBundle(resourceType, resp.Body); err == nil {
				baseline = s
				haveBaseline = true
			} else {
				add(model.Finding{
					ID: resourceType + "/search", Resource: resourceType, Claim: resourceType + "?_count=50",
					Kind: model.KindSearch, Status: model.StatusUntested, StatusCode: resp.StatusCode,
					Detail: fmt.Sprintf("200 but body did not parse as a Bundle: %v", err),
				})
			}
		} else {
			// Any other status (500, 404, 400, ...) on the most basic
			// possible request — a resource that claims search-type but
			// can't even do an unfiltered search is a lie in its own
			// right, not a silent "nothing to report".
			add(model.Finding{
				ID: resourceType + "/search", Resource: resourceType, Claim: resourceType + "?_count=50",
				Kind: model.KindSearch, Status: model.StatusRejected, StatusCode: resp.StatusCode,
				Detail: fmt.Sprintf("%d on the unfiltered search", resp.StatusCode),
			})
		}
	}

	emptyBaseline := !haveBaseline || len(baseline.MatchEntries()) == 0

	// b. read / vread
	if entry.HasInteraction("read") {
		if emptyBaseline {
			// Per brief §5.2a: skip read entirely when there's no sample id.
		} else {
			for _, f := range probeRead(ctx, cl, resourceType, baseline, entry.HasInteraction("vread")) {
				add(f)
			}
		}
	}

	// c. search params
	if canSearch {
		for _, p := range entry.SearchParam {
			if skippableParam(p) {
				continue
			}
			id := resourceType + "/search/" + p.Name
			if !haveBaseline {
				add(model.Finding{ID: id, Resource: resourceType, Claim: resourceType + "?" + p.Name + "=...",
					Kind: model.KindSearch, Status: model.StatusUntested, Detail: "no unfiltered sample set to test against"})
				continue
			}
			if emptyBaseline {
				add(model.Finding{ID: id, Resource: resourceType, Claim: resourceType + "?" + p.Name + "=...",
					Kind: model.KindSearch, Status: model.StatusUntested, Detail: "unfiltered search returned 0 entries; can't prove a param filters when there's nothing to filter"})
				continue
			}
			add(testSearchParam(ctx, cl, resourceType, p, baseline, nextSuffix()))
		}
	}

	// d. includes / revincludes
	if haveBaseline && !emptyBaseline {
		for _, inc := range entry.SearchInclude {
			add(testInclude(ctx, cl, resourceType, inc, false, baseline))
		}
		for _, inc := range entry.SearchRevInclude {
			add(testInclude(ctx, cl, resourceType, inc, true, baseline))
		}
	}

	// e. paging
	if haveBaseline && baseline.NextLink != "" {
		add(testPaging(ctx, cl, resourceType, baseline))
	}

	// f. _count honoured
	if canSearch && haveBaseline && !emptyBaseline {
		add(testCount(ctx, cl, resourceType))
	}

	// Resource-scoped operations (e.g. Patient's $everything), same
	// tracked-debt treatment as the system-level ones in system.go — see
	// probeOperations for why these aren't actually exercised in Phase 1.
	for _, op := range entry.Operation {
		add(model.Finding{
			ID: resourceType + "/operation/" + op.Name, Resource: resourceType, Claim: resourceType + "/{id}/$" + op.Name,
			Kind: model.KindOperation, Status: model.StatusUntested, Detail: operationDetail(probeOperations),
		})
	}

	return out
}
