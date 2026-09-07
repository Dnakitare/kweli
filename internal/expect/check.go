package expect

import (
	"fmt"
	"sort"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/model"
)

// Check compares cs against USCoreRequiredParams and returns one Finding
// per gap: a required resource type the CapabilityStatement doesn't claim
// at all, or a required search param not present on a resource type that
// is claimed. Findings are returned in a stable (resource, then param)
// order so text/JSON output doesn't jitter between runs.
func Check(cs *capstmt.CapabilityStatement) []model.Finding {
	if cs == nil {
		return nil
	}

	types := make([]string, 0, len(USCoreRequiredParams))
	for t := range USCoreRequiredParams {
		types = append(types, t)
	}
	sort.Strings(types)

	var findings []model.Finding
	for _, rtype := range types {
		required := USCoreRequiredParams[rtype]
		entry, claimed := cs.FindResource(rtype)
		if !claimed {
			findings = append(findings, model.Finding{
				ID:       rtype + "/expect",
				Resource: rtype,
				Claim:    rtype,
				Kind:     model.KindMissing,
				Status:   model.StatusMissing,
				Detail:   "US Core 6.1 requires this resource type; the CapabilityStatement doesn't claim it at all",
			})
			continue // nothing to check per-param if the resource itself is absent
		}

		claimedParams := make(map[string]bool, len(entry.SearchParam))
		for _, sp := range entry.SearchParam {
			claimedParams[sp.Name] = true
		}

		sortedRequired := append([]string(nil), required...)
		sort.Strings(sortedRequired)
		for _, param := range sortedRequired {
			if claimedParams[param] {
				continue
			}
			findings = append(findings, model.Finding{
				ID:       rtype + "/expect/" + param,
				Resource: rtype,
				Claim:    fmt.Sprintf("%s?%s=...", rtype, param),
				Kind:     model.KindMissing,
				Status:   model.StatusMissing,
				Detail:   fmt.Sprintf("US Core 6.1 requires %s search by %q (individually or as part of a mandatory combination); not present in the CapabilityStatement's searchParam list", rtype, param),
			})
		}
	}
	return findings
}
