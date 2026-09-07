# kweli

FHIR CapabilityStatement truth-tester. Point it at a FHIR base URL and it
reads `/metadata`, then actually exercises every claim in the
CapabilityStatement — resource types, interactions, search parameters,
includes, paging — and reports which claims are true, which are false, and
which are silently ignored. Read-only, single static binary, non-zero exit
code when lies are found so it drops into CI.

See `kweli-BRIEF.md` (the original handoff brief) for the full design
rationale. This file just covers what's built and what isn't yet.

## Status: Phase 1 (core) complete

```
go build -o kweli ./cmd/kweli
./kweli https://hapi.fhir.org/baseR4
```

Implemented:
- Bootstrap: fetch and parse `/metadata`, fail fast on a bad response.
- Per-resource probes: unfiltered search, read/vread, the two-query search
  param test (positive query via a curated path table + nonsense query,
  with `Prefer: handling=strict` disambiguation), includes/revincludes,
  paging (up to 3 hops, no repeated entries), `_count` honoring.
- `--token`, `--resources`, `--concurrency`, `--rps`, `--timeout`,
  `--budget`, `--format` (text/json/markdown), `--fail-on`, `--no-color`,
  `--quiet`, `--verbose`.
- `internal/testserver`: a synthetic FHIR server planting the 12 defects
  from the brief's acceptance test, plus a truthful control. See
  `internal/probe/acceptance_test.go` — Phase 1 is done when kweli reports
  every planted defect and nothing else; it does.

## Tracked debt (named explicitly, not hidden)

- **`--probe-operations` / `$everything` / `$export` / `$validate` are not
  implemented.** Every claimed operation is reported `untested` with a
  clear reason instead of silently skipped or guessed at. Kicking off
  `$export` safely (and cancelling it) against a real production server is
  real engineering, not a Phase 1 shortcut — deferred on purpose. Flag
  exists so `--help` documents the future shape.
- **`--smart-backend` (SMART Backend Services / `private_key_jwt`) is
  Phase 2.** The CLI refuses to run rather than pretending the flag works.
- **`--expect us-core` (Phase 3 "missing param" detection) is not
  implemented.** Same refusal-not-pretense treatment.
- The curated search-param path table (`internal/sample/paths.go`) covers
  ~55 params across 10 common resource types (Patient, Observation,
  Condition, Encounter, Procedure, MedicationRequest, AllergyIntolerance,
  Immunization, DiagnosticReport, Practitioner, Organization). The brief
  calls for growing this as "Phase 1.5" — params outside the table still
  get a definitive verdict via the nonsense-only query, they just can't
  produce the stronger "every entry matched a real value" verification.

## Layout

```
cmd/kweli/          flag parsing, exit codes
internal/model/      shared Finding/Report vocabulary (probe -> report contract)
internal/capstmt/    minimal CapabilityStatement struct + parser
internal/client/     http client: auth, rate limit, retry, redaction
internal/probe/      one file per probe kind (search, include, paging, read, count, system)
internal/sample/     sample-resource fetch + curated path table (two-query test)
internal/report/     text/json/markdown renderers
internal/testserver/ httptest fake server with planted lies + truthful control
```

## Exit codes

- `0`: ran cleanly, nothing in `--fail-on` (default `rejected,ignored`) was found.
- `1`: at least one finding matched `--fail-on`.
- `2`: config or network failure before probing started (bad URL, `/metadata`
  unreachable or malformed, bad flags).
