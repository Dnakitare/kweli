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
// current version if claimed and the sample has a meta.versionId. Returns
// one finding for "read", plus a second "vread" finding when tryVRead is
// set and the sample had a versionId to test against — vread gets its own
// finding (rather than being folded into read's Detail) so a server that
// claims vread but fails it is actually counted as a lie, not silently
// left StatusVerified because the plain read succeeded.
func probeRead(ctx context.Context, cl *client.Client, resourceType string, baseline sample.Set, tryVRead bool) []model.Finding {
	entries := baseline.MatchEntries()
	var id string
	for _, e := range entries {
		if v, ok := e.Resource["id"].(string); ok && v != "" {
			id = v
			break
		}
	}
	if id == "" {
		return []model.Finding{{
			ID: resourceType + "/read", Resource: resourceType, Claim: resourceType + "/{id}",
			Kind: model.KindRead, Status: model.StatusUntested, Detail: "no sample resource had an id",
		}}
	}

	path := resourceType + "/" + url.PathEscape(id)
	resp, err := cl.Get(ctx, path)
	f := model.Finding{ID: resourceType + "/read", Resource: resourceType, Claim: path, Kind: model.KindRead, Request: "GET " + path}
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		return []model.Finding{f}
	}
	f.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		f.Status = model.StatusRejected
		f.Detail = fmt.Sprintf("%d reading %s", resp.StatusCode, path)
		return []model.Finding{f}
	}
	got, err := sample.ParseResource(resp.Body)
	if err != nil {
		f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("200 but body did not parse: %v", err)
		return []model.Finding{f}
	}
	gotID, _ := got["id"].(string)
	if gotID != id {
		f.Status = model.StatusIgnored
		f.Detail = fmt.Sprintf("returned id %q does not match requested %q", gotID, id)
		return []model.Finding{f}
	}
	f.Status = model.StatusVerified

	if !tryVRead {
		return []model.Finding{f}
	}
	meta, _ := got["meta"].(map[string]any)
	vid, _ := meta["versionId"].(string)
	if vid == "" {
		// Nothing to vread against; no finding either way (mirrors §5.2b's
		// "vread if claimed" being conditional on a versionId existing).
		return []model.Finding{f}
	}
	vpath := fmt.Sprintf("%s/_history/%s", path, url.PathEscape(vid))
	vf := model.Finding{ID: resourceType + "/vread", Resource: resourceType, Claim: vpath, Kind: model.KindVRead, Request: "GET " + vpath}
	vresp, verr := cl.Get(ctx, vpath)
	switch {
	case verr != nil:
		vf.Status, vf.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", verr)
	case vresp.StatusCode < 200 || vresp.StatusCode >= 300:
		vf.StatusCode = vresp.StatusCode
		vf.Status = model.StatusRejected
		vf.Detail = fmt.Sprintf("%d reading %s", vresp.StatusCode, vpath)
	default:
		vf.StatusCode = vresp.StatusCode
		vf.Status = model.StatusVerified
		vf.Detail = "vread OK"
	}
	return []model.Finding{f, vf}
}
