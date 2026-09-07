// Package model defines the shared vocabulary every other kweli package
// speaks: probe results ("findings"), their status/kind enums, and the
// top-level Report that internal/report renders as text/json/markdown.
//
// This file is the contract between internal/probe (which produces Findings)
// and internal/report (which renders them). Keep it small and stable; other
// packages should not need to import each other directly.
package model

import "time"

// Kind identifies what sort of claim a Finding is about.
type Kind string

const (
	KindSearch     Kind = "search"
	KindRead       Kind = "read"
	KindVRead      Kind = "vread"
	KindInclude    Kind = "include"
	KindRevInclude Kind = "revinclude"
	KindPaging     Kind = "paging"
	KindCount      Kind = "count"
	KindHistory    Kind = "history"
	KindOperation  Kind = "operation"
	// KindMissing is produced by internal/expect (--expect us-core, Phase
	// 3), not by any probe: it's a static comparison against a table, not
	// something exercised against the live server.
	KindMissing Kind = "missing"
)

// Status is the verdict for a single claim.
type Status string

const (
	StatusVerified       Status = "verified"
	StatusRejected       Status = "rejected"
	StatusRejectedStrict Status = "rejected (strict)"
	StatusIgnored        Status = "ignored"
	StatusIgnoredPartial Status = "ignored (partial)"
	StatusUntested       Status = "untested"
	StatusInconclusive   Status = "inconclusive"
	// StatusMissing is the fourth finding class from brief Phase 3
	// (--expect us-core): a search param or resource type US Core requires
	// that the CapabilityStatement never claims at all. Distinct in kind
	// from IsLie() (the server never claimed it, so it never lied about
	// it) and from IsUntestedCategory() (kweli isn't uncertain here — the
	// comparison against the table is definitive).
	StatusMissing Status = "missing"
)

// IsLie reports whether a status represents a claim the server did not
// honour (used for --fail-on classification and the "LIES" report section).
func (s Status) IsLie() bool {
	switch s {
	case StatusRejected, StatusIgnored, StatusIgnoredPartial:
		return true
	default:
		return false
	}
}

// IsUntestedCategory reports whether a status belongs in the report's
// "UNTESTED" bucket: StatusUntested and StatusInconclusive are the literal
// "couldn't determine" cases, and StatusRejectedStrict joins them even
// though it's a definitive, honest result (a server correctly refusing an
// unsupported param under `Prefer: handling=strict` is not a lie) — it
// still isn't "verified working" either, so it belongs in the same bucket
// as the rest of "not a lie, not fully confirmed" rather than vanishing
// from every count and section that isn't LIES. This is the single
// definition every package (probe's tallies, report's UNTESTED section,
// FailOnCategory's --fail-on bucket) uses, so they can't drift apart.
func (s Status) IsUntestedCategory() bool {
	switch s {
	case StatusUntested, StatusInconclusive, StatusRejectedStrict:
		return true
	default:
		return false
	}
}

// FailOnCategory maps a Status to one of the --fail-on bucket names:
// "rejected", "ignored", "paging", "untested", or (only ever produced
// under --expect) "missing". Only StatusVerified returns "" (fully
// confirmed, nothing to flag). "missing" isn't in the brief's literal
// --fail-on list (written before Phase 3 existed) but is the obvious
// extension: a required-but-unclaimed param is exactly the kind of thing
// --fail-on exists to catch in CI, once a user has opted into --expect.
func (s Status) FailOnCategory() string {
	switch {
	case s == StatusRejected:
		return "rejected"
	case s == StatusIgnored || s == StatusIgnoredPartial:
		return "ignored"
	case s == StatusMissing:
		return "missing"
	case s.IsUntestedCategory():
		return "untested"
	default:
		return ""
	}
}

// Finding is one verified (or refuted) claim from the CapabilityStatement.
type Finding struct {
	// ID is the stable claim identifier, e.g. "Patient/search/birthdate",
	// "Observation/read", "Condition/include/Condition:subject".
	ID string `json:"id"`
	// Resource is the FHIR resource type this claim is about, or "system"
	// for system-level claims.
	Resource string `json:"resource"`
	// Claim is a short human-readable description of what was claimed,
	// e.g. "Observation?code=...".
	Claim string `json:"claim"`
	Kind  Kind   `json:"kind"`
	// Category is "paging" for paging findings so --fail-on can select them
	// even though their Status is one of the generic values above.
	Category   string `json:"category,omitempty"`
	Status     Status `json:"status"`
	Detail     string `json:"detail,omitempty"`
	Request    string `json:"request,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
}

// FailOnCategory returns the --fail-on bucket for this finding, preferring
// an explicit Category (used for paging) over the one derived from Status.
func (f Finding) FailOnCategory() string {
	if f.Category != "" {
		return f.Category
	}
	return f.Status.FailOnCategory()
}

// ResourceSummary is one row of the per-resource claims table.
type ResourceSummary struct {
	Resource string `json:"resource"`
	Claimed  int    `json:"claimed"`
	Verified int    `json:"verified"`
	Rejected int    `json:"rejected"`
	Ignored  int    `json:"ignored"`
	Untested int    `json:"untested"`
}

// Summary is the one-line totals footer. Missing is only ever non-zero
// under --expect (Phase 3) and is deliberately excluded from Claims — a
// required-but-unclaimed param was never a claim the server made, so it's
// not something kweli "tested" the way the other buckets are.
type Summary struct {
	Claims   int `json:"claims"`
	Verified int `json:"verified"`
	Lies     int `json:"lies"`
	Untested int `json:"untested"`
	Missing  int `json:"missing,omitempty"`
}

// Timing records how long the run took and how much traffic it generated.
// Duration is kept as a time.Duration for internal use (text rendering);
// DurationMS is the stable JSON field people script against.
type Timing struct {
	Duration   time.Duration `json:"-"`
	DurationMS int64         `json:"durationMs"`
	Requests   int           `json:"requests"`
}

// Report is the full result of a kweli run: everything the text/json/
// markdown renderers in internal/report need, and nothing they have to
// recompute.
type Report struct {
	Server      string            `json:"server"`
	FHIRVersion string            `json:"fhirVersion"`
	Software    string            `json:"software,omitempty"`
	Resources   []ResourceSummary `json:"resources"`
	Findings    []Finding         `json:"findings"`
	Warnings    []string          `json:"warnings,omitempty"`
	Summary     Summary           `json:"summary"`
	Timing      Timing            `json:"timing"`
}
