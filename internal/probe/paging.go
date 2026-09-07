package probe

import (
	"context"
	"fmt"

	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

// testPaging implements brief §5.2e: follow the baseline's next link up to
// 3 hops, verifying each hop is 200, returns a Bundle, and that no fullUrl
// repeats across hops (including the baseline page itself).
func testPaging(ctx context.Context, cl *client.Client, resourceType string, baseline sample.Set) model.Finding {
	id := resourceType + "/paging"
	f := model.Finding{ID: id, Resource: resourceType, Kind: model.KindPaging, Category: "paging", Claim: resourceType + " paging"}

	seen := baseline.IDs()
	link := baseline.NextLink
	hops := 0
	for hops < 3 && link != "" {
		resp, err := cl.GetURL(ctx, link)
		hops++
		if err != nil {
			f.Status = model.StatusUntested
			f.Detail = fmt.Sprintf("hop %d: request failed: %v", hops, err)
			return f
		}
		f.Request, f.StatusCode = "GET "+link, resp.StatusCode
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			f.Status = model.StatusRejected
			f.Detail = fmt.Sprintf("hop %d: %d fetching next link", hops, resp.StatusCode)
			return f
		}
		s, err := sample.ParseBundle(resourceType, resp.Body)
		if err != nil {
			f.Status = model.StatusUntested
			f.Detail = fmt.Sprintf("hop %d: 200 but body did not parse as a Bundle: %v", hops, err)
			return f
		}
		for k := range s.IDs() {
			if seen[k] {
				f.Status = model.StatusIgnored
				f.Detail = fmt.Sprintf("hop %d repeats an entry from an earlier page (%s)", hops, k)
				return f
			}
			seen[k] = true
		}
		if len(s.Entries) == 0 && s.NextLink != "" {
			f.Status = model.StatusIgnored
			f.Detail = fmt.Sprintf("hop %d returned 0 entries but still advertised a next link", hops)
			return f
		}
		link = s.NextLink
	}

	f.Status = model.StatusVerified
	f.Detail = fmt.Sprintf("%d page(s) followed cleanly, no repeated entries", hops+1)
	return f
}

// testCount implements brief §5.2f: GET T?_count=2, expect at most 2
// entries. Servers that ignore _count tend to ignore other params too.
func testCount(ctx context.Context, cl *client.Client, resourceType string) model.Finding {
	id := resourceType + "/count"
	path := resourceType + "?_count=2"
	f := model.Finding{ID: id, Resource: resourceType, Claim: path, Kind: model.KindCount, Request: "GET " + path}

	resp, err := cl.Get(ctx, path)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		return f
	}
	f.StatusCode = resp.StatusCode
	if resp.StatusCode >= 400 {
		f.Status = model.StatusRejected
		f.Detail = fmt.Sprintf("%d requesting %s", resp.StatusCode, path)
		return f
	}
	s, err := sample.ParseBundle(resourceType, resp.Body)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("200 but body did not parse as a Bundle: %v", err)
		return f
	}
	n := len(s.MatchEntries())
	if n > 2 {
		f.Status = model.StatusIgnored
		f.Detail = fmt.Sprintf("_count=2 requested, got %d entries", n)
		return f
	}
	f.Status = model.StatusVerified
	f.Detail = fmt.Sprintf("_count=2 honoured (%d entries)", n)
	return f
}
