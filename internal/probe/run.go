// Package probe implements the per-resource and system-level probes from
// brief §5: it takes a parsed CapabilityStatement and an HTTP client, and
// produces a model.Report of which claims are true, false, or untested.
package probe

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
)

// Options configures a probe run.
type Options struct {
	// Resources restricts probing to these types. Empty means every type
	// claimed in the CapabilityStatement.
	Resources []string
	// Concurrency is how many resource types are probed in parallel.
	// Probes within one resource type always run sequentially (§5.2).
	Concurrency int
	// ProbeOperations opts into $everything/$export/$validate probing.
	// Phase 1 does not implement these probes yet (tracked debt, see
	// README); when true, operation claims are reported untested with a
	// clear reason instead of silently skipped.
	ProbeOperations bool
	// Seed makes nonsense-value suffixes reproducible (tests pass a fixed
	// seed; main.go can pass 0 to mean "use time.Now()").
	Seed int64
}

func (o Options) rngSeed() int64 {
	if o.Seed != 0 {
		return o.Seed
	}
	return time.Now().UnixNano()
}

// resourceOutcome is what one resource-type's probes contribute to the
// final report.
type resourceOutcome struct {
	summary  model.ResourceSummary
	findings []model.Finding
	warnings []string
}

// Run probes every claimed resource (or opts.Resources, if set) and
// returns the assembled report. It does not set Report.Server; the caller
// (cmd/kweli) fills that in from the CLI argument.
func Run(ctx context.Context, cs *capstmt.CapabilityStatement, cl *client.Client, opts Options) (*model.Report, error) {
	start := time.Now()

	rest, ok := cs.ServerRest()
	if !ok {
		return nil, fmt.Errorf("CapabilityStatement has no server-mode rest entry")
	}

	types := cs.ResourceTypes()
	if len(opts.Resources) > 0 {
		want := make(map[string]bool, len(opts.Resources))
		for _, t := range opts.Resources {
			want[t] = true
		}
		filtered := types[:0:0]
		for _, t := range types {
			if want[t] {
				filtered = append(filtered, t)
			}
		}
		types = filtered
	}

	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	outcomes := make([]resourceOutcome, len(types))
	var rngMu sync.Mutex
	rng := rand.New(rand.NewSource(opts.rngSeed()))
	nextSuffix := func() string {
		rngMu.Lock()
		defer rngMu.Unlock()
		return fmt.Sprintf("%06d", rng.Intn(1_000_000))
	}

	for i, t := range types {
		i, t := i, t
		entry, _ := cs.FindResource(t)
		g.Go(func() error {
			outcomes[i] = probeResource(gctx, cl, t, entry, nextSuffix, opts.ProbeOperations)
			return nil
		})
	}
	// errgroup.Go's func never returns an error (probeResource degrades to
	// findings instead), so g.Wait() only ever reports ctx cancellation.
	_ = g.Wait()

	report := &model.Report{
		FHIRVersion: cs.FHIRVersion,
	}
	if cs.Software.Name != "" {
		report.Software = cs.Software.Name
		if cs.Software.Version != "" {
			report.Software += " " + cs.Software.Version
		}
	}

	for _, o := range outcomes {
		report.Resources = append(report.Resources, o.summary)
		report.Findings = append(report.Findings, o.findings...)
		report.Warnings = append(report.Warnings, o.warnings...)
	}

	// System-level probes (§5.4): history, operations. Kept out of the
	// per-resource concurrency group since there's only ever one of each.
	sysFindings, sysWarnings := probeSystem(ctx, cl, rest, opts)
	report.Findings = append(report.Findings, sysFindings...)
	report.Warnings = append(report.Warnings, sysWarnings...)

	sort.SliceStable(report.Findings, func(i, j int) bool {
		return report.Findings[i].ID < report.Findings[j].ID
	})

	report.Summary = summarize(report.Findings)
	report.Timing = model.Timing{
		Duration:   time.Since(start),
		DurationMS: time.Since(start).Milliseconds(),
		Requests:   cl.RequestCount(),
	}

	if ctx.Err() != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("run stopped early: %v", ctx.Err()))
	}

	return report, nil
}

func summarize(findings []model.Finding) model.Summary {
	var s model.Summary
	s.Claims = len(findings)
	for _, f := range findings {
		switch {
		case f.Status.IsLie():
			s.Lies++
		case f.Status.IsUntestedCategory():
			s.Untested++
		case f.Status == model.StatusVerified:
			s.Verified++
		}
	}
	return s
}
