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
func probeResource(ctx context.Context, cl *client.Client, resourceType string, entry capstmt.ResourceEntry, nextSuffix func() string) resourceOutcome {
	out := resourceOutcome{summary: model.ResourceSummary{Resource: resourceType}}

	add := func(f model.Finding) {
		out.findings = append(out.findings, f)
		out.summary.Claimed++
		switch {
		case f.Status.IsLie():
			switch f.Status {
			case model.StatusRejected, model.StatusRejectedStrict:
				out.summary.Rejected++
			default:
				out.summary.Ignored++
			}
		case f.Status == model.StatusUntested || f.Status == model.StatusInconclusive:
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
			}
		}
	}

	emptyBaseline := !haveBaseline || len(baseline.MatchEntries()) == 0

	// b. read / vread
	if entry.HasInteraction("read") {
		if emptyBaseline {
			// Per brief §5.2a: skip read entirely when there's no sample id.
		} else {
			add(probeRead(ctx, cl, resourceType, baseline, entry.HasInteraction("vread")))
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

	return out
}
