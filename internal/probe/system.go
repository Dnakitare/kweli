package probe

import (
	"context"
	"fmt"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
)

// probeSystem implements brief §5.4: system-level _history, and (opt-in)
// $everything/$export/$validate. The operation probes are tracked tech
// debt for Phase 1 (see README) — they need side-effect-safe handling
// ($export kickoff+cancel, $validate as the one allowed POST) that's easy
// to get wrong against a production server, so Phase 1 reports them
// untested with a clear reason rather than guessing.
func probeSystem(ctx context.Context, cl *client.Client, rest capstmt.Rest, opts Options) ([]model.Finding, []string) {
	var findings []model.Finding

	if rest.HasSystemInteraction("history-system") {
		path := "_history?_count=1"
		resp, err := cl.Get(ctx, path)
		f := model.Finding{ID: "system/history", Resource: "system", Claim: path, Kind: model.KindHistory, Request: "GET " + path}
		switch {
		case err != nil:
			f.Status, f.Detail = model.StatusUntested, fmt.Sprintf("request failed: %v", err)
		default:
			f.StatusCode = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				f.Status = model.StatusVerified
			} else {
				f.Status = model.StatusRejected
				f.Detail = fmt.Sprintf("%d requesting %s", resp.StatusCode, path)
			}
		}
		findings = append(findings, f)
	}

	for _, op := range rest.Operation {
		findings = append(findings, model.Finding{
			ID: "system/operation/" + op.Name, Resource: "system", Claim: "$" + op.Name,
			Kind: model.KindOperation, Status: model.StatusUntested,
			Detail: operationDetail(opts.ProbeOperations),
		})
	}

	return findings, nil
}

func operationDetail(probeOperations bool) string {
	if probeOperations {
		return "operation probing not implemented in Phase 1 (tracked debt, see README)"
	}
	return "skipped: pass --probe-operations to attempt (still unimplemented in Phase 1)"
}
