package probe

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/sample"
)

// probeRead implements brief §5.2b: GET T/{id} for the first sample id,
// verify 200 and that the returned id matches; vread the same resource's
// current version if claimed and the sample has a meta.versionId.
func probeRead(ctx context.Context, cl *client.Client, resourceType string, baseline sample.Set, tryVRead bool) model.Finding {
	entries := baseline.MatchEntries()
	var id string
	for _, e := range entries {
		if v, ok := e.Resource["id"].(string); ok && v != "" {
			id = v
			break
		}
	}
	if id == "" {
		return model.Finding{
			ID: resourceType + "/read", Resource: resourceType, Claim: resourceType + "/{id}",
			Kind: model.KindRead, Status: model.StatusUntested, Detail: "no sample resource had an id",
		}
	}

	path := resourceType + "/" + url.PathEscape(id)
	resp, err := cl.Get(ctx, path)
	f := model.Finding{ID: resourceType + "/read", Resource: resourceType, Claim: path, Kind: model.KindRead, Request: "GET " + path}
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		return f
	}
	f.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		f.Status = model.StatusRejected
		f.Detail = fmt.Sprintf("%d reading %s", resp.StatusCode, path)
		return f
	}
	got, err := sample.ParseResource(resp.Body)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("200 but body did not parse: %v", err)
		return f
	}
	gotID, _ := got["id"].(string)
	if gotID != id {
		f.Status = model.StatusIgnored
		f.Detail = fmt.Sprintf("returned id %q does not match requested %q", gotID, id)
		return f
	}
	f.Status = model.StatusVerified

	if !tryVRead {
		return f
	}
	// vread is reported as part of the same claim's detail rather than a
	// separate top-level finding, since it only makes sense in the
	// presence of a working read; a server that fails vread but passes
	// read is rare enough that Phase 1 folds it into one line.
	meta, _ := got["meta"].(map[string]any)
	vid, _ := meta["versionId"].(string)
	if vid == "" {
		return f
	}
	vpath := fmt.Sprintf("%s/_history/%s", path, url.PathEscape(vid))
	vresp, err := cl.Get(ctx, vpath)
	if err != nil || vresp.StatusCode < 200 || vresp.StatusCode >= 300 {
		f.Detail = fmt.Sprintf("read OK; vread %s failed", vpath)
		return f
	}
	f.Detail = "read and vread both OK"
	return f
}
