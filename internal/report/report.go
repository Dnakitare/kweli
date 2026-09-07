// Package report renders kweli findings in text, JSON, and Markdown formats.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Dnakitare/kweli/internal/model"
)

// Text renders r as the human-readable table format to w.
// color enables ANSI color codes (red for lies, yellow for untested, green for verified).
func Text(w io.Writer, r *model.Report, color bool) error {
	// Helper to apply color
	colorize := func(text, code string) string {
		if !color {
			return text
		}
		return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, text)
	}

	// Header line
	header := fmt.Sprintf("kweli  %s   FHIR %s", r.Server, r.FHIRVersion)
	if r.Software != "" {
		header += fmt.Sprintf("   software: %s", r.Software)
	}
	fmt.Fprintf(w, "%s\n\n", header)

	// Resource table
	fmt.Fprintf(w, "Resource      Claimed  Verified  Rejected  Ignored  Untested\n")
	for _, res := range r.Resources {
		fmt.Fprintf(w, "%-13s %8d %9d %9d %8d %9d\n",
			res.Resource, res.Claimed, res.Verified, res.Rejected, res.Ignored, res.Untested)
	}

	// Summary line for paging/includes/operations
	summaryParts := []string{}

	// Paging
	pagingFindings := filterByKind(r.Findings, model.KindPaging)
	if len(pagingFindings) == 0 {
		summaryParts = append(summaryParts, "Paging: n/a")
	} else {
		pagingParts := []string{}
		for _, f := range pagingFindings {
			if f.Status == model.StatusVerified {
				pagingParts = append(pagingParts, fmt.Sprintf("OK (%s)", f.Resource))
			} else {
				pagingParts = append(pagingParts, fmt.Sprintf("broken (%s: %s)", f.Resource, f.Detail))
			}
		}
		summaryParts = append(summaryParts, fmt.Sprintf("Paging: %s", strings.Join(pagingParts, "; ")))
	}

	// Includes (include + revinclude)
	includeFindings := filterByKinds(r.Findings, model.KindInclude, model.KindRevInclude)
	if len(includeFindings) > 0 {
		verifiedCount := 0
		for _, f := range includeFindings {
			if f.Status == model.StatusVerified {
				verifiedCount++
			}
		}
		summaryParts = append(summaryParts, fmt.Sprintf("Includes: %d/%d OK", verifiedCount, len(includeFindings)))
	} else {
		summaryParts = append(summaryParts, "Includes: n/a")
	}

	// Operations
	operationFindings := filterByKind(r.Findings, model.KindOperation)
	if len(operationFindings) > 0 {
		summaryParts = append(summaryParts, "Operations: skipped")
	}

	fmt.Fprintf(w, "\n%s\n", strings.Join(summaryParts, "   "))

	// Warnings section
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "\nWARNINGS (%d)\n", len(r.Warnings))
		for _, warning := range r.Warnings {
			fmt.Fprintf(w, "  %s\n", warning)
		}
	}

	// LIES section
	lies := filterByLies(r.Findings)
	if len(lies) > 0 {
		fmt.Fprintf(w, "\n%s\n", colorize(fmt.Sprintf("LIES (%d)", len(lies)), "31"))
		for _, f := range lies {
			statusStr := colorize(strings.ToUpper(string(f.Status)), "31")
			fmt.Fprintf(w, "  %s  %s  %s\n", f.Claim, statusStr, f.Detail)
		}
	}

	// UNTESTED section
	untested := filterByUntested(r.Findings)
	if len(untested) > 0 {
		fmt.Fprintf(w, "\n%s\n", colorize(fmt.Sprintf("UNTESTED (%d)", len(untested)), "33"))
		for _, f := range untested {
			statusStr := colorize(strings.ToUpper(string(f.Status)), "33")
			fmt.Fprintf(w, "  %s  %s  %s\n", f.Claim, statusStr, f.Detail)
		}
	}

	// Final summary line
	durationStr := r.Timing.Duration.Round(time.Second).String()
	fmt.Fprintf(w, "\nClaims %d · Verified %d · Lies %d · Untested %d · %s · %d requests\n",
		r.Summary.Claims, r.Summary.Verified, r.Summary.Lies, r.Summary.Untested,
		durationStr, r.Timing.Requests)

	return nil
}

// JSON renders r as a single JSON object to w with 2-space indent and trailing newline.
func JSON(w io.Writer, r *model.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		return err
	}
	return nil
}

// Markdown renders r as a GitHub-flavored-Markdown document to w.
func Markdown(w io.Writer, r *model.Report) error {
	// Heading and intro
	fmt.Fprintf(w, "## kweli report\n\n")
	header := fmt.Sprintf("%s (FHIR %s)", r.Server, r.FHIRVersion)
	if r.Software != "" {
		header += fmt.Sprintf(" · software: %s", r.Software)
	}
	fmt.Fprintf(w, "%s\n\n", header)

	// Resources table
	fmt.Fprintf(w, "### Resources\n\n")
	fmt.Fprintf(w, "| Resource | Claimed | Verified | Rejected | Ignored | Untested |\n")
	fmt.Fprintf(w, "|----------|---------|----------|----------|---------|----------|\n")
	for _, res := range r.Resources {
		fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %d |\n",
			res.Resource, res.Claimed, res.Verified, res.Rejected, res.Ignored, res.Untested)
	}

	// Lies section
	fmt.Fprintf(w, "\n### Lies\n\n")
	lies := filterByLies(r.Findings)
	if len(lies) == 0 {
		fmt.Fprintf(w, "_None._\n")
	} else {
		fmt.Fprintf(w, "| Claim | Status | Detail |\n")
		fmt.Fprintf(w, "|-------|--------|--------|\n")
		for _, f := range lies {
			fmt.Fprintf(w, "| %s | %s | %s |\n",
				escapeMarkdown(f.Claim), strings.ToUpper(string(f.Status)), escapeMarkdown(f.Detail))
		}
	}

	// Untested section
	fmt.Fprintf(w, "\n### Untested\n\n")
	untested := filterByUntested(r.Findings)
	if len(untested) == 0 {
		fmt.Fprintf(w, "_None._\n")
	} else {
		fmt.Fprintf(w, "| Claim | Detail |\n")
		fmt.Fprintf(w, "|-------|--------|\n")
		for _, f := range untested {
			fmt.Fprintf(w, "| %s | %s |\n",
				escapeMarkdown(f.Claim), escapeMarkdown(f.Detail))
		}
	}

	// Final summary line
	durationStr := r.Timing.Duration.Round(time.Second).String()
	fmt.Fprintf(w, "\n---\n\nClaims %d · Verified %d · Lies %d · Untested %d · %s · %d requests\n",
		r.Summary.Claims, r.Summary.Verified, r.Summary.Lies, r.Summary.Untested,
		durationStr, r.Timing.Requests)

	return nil
}

// Helper functions

func filterByKind(findings []model.Finding, kind model.Kind) []model.Finding {
	var result []model.Finding
	for _, f := range findings {
		if f.Kind == kind {
			result = append(result, f)
		}
	}
	return result
}

func filterByKinds(findings []model.Finding, kinds ...model.Kind) []model.Finding {
	var result []model.Finding
	kindMap := make(map[model.Kind]bool)
	for _, k := range kinds {
		kindMap[k] = true
	}
	for _, f := range findings {
		if kindMap[f.Kind] {
			result = append(result, f)
		}
	}
	return result
}

func filterByLies(findings []model.Finding) []model.Finding {
	var result []model.Finding
	for _, f := range findings {
		if f.Status.IsLie() {
			result = append(result, f)
		}
	}
	return result
}

func filterByUntested(findings []model.Finding) []model.Finding {
	var result []model.Finding
	for _, f := range findings {
		if f.Status == model.StatusUntested || f.Status == model.StatusInconclusive {
			result = append(result, f)
		}
	}
	return result
}

func escapeMarkdown(s string) string {
	// Escape pipe characters for markdown tables
	return strings.ReplaceAll(s, "|", "\\|")
}
