# kweli

[![CI](https://github.com/Dnakitare/kweli/actions/workflows/ci.yml/badge.svg)](https://github.com/Dnakitare/kweli/actions/workflows/ci.yml)

FHIR CapabilityStatement truth-tester. Point it at a FHIR base URL and it
reads `/metadata`, then actually exercises every claim in the
CapabilityStatement — resource types, interactions, search parameters,
includes, paging — and reports which claims are true, which are false, and
which are silently ignored. Read-only, single static binary, non-zero exit
code when lies are found so it drops into CI.

See `kweli-BRIEF.md` (the original handoff brief) for the full design
rationale. This file just covers what's built and what isn't yet.

## Example

Real output, unedited except for length, from a run against the public
`https://hapi.fhir.org/baseR4` sandbox — a well-maintained reference
server, not a vendor with something to hide, and it still has real gaps
between what it claims and what it does:

```
$ kweli https://hapi.fhir.org/baseR4 --resources Patient,Observation,Condition,Encounter,MedicationRequest,DiagnosticReport,ServiceRequest,DocumentReference,CarePlan,Location

kweli  https://hapi.fhir.org/baseR4   FHIR 4.0.1   software: HAPI FHIR Server 8.11.16-SNAPSHOT/7e7129efb5/2026-07-08

Resource      Claimed  Verified  Rejected  Ignored  Untested
CarePlan           144        29         0        0       115
Condition          139        34         0        0       105
DiagnosticReport      131        26         0        0       105
DocumentReference      140        34         1        0       105
Encounter          170        35         0        0       135
Observation         166        44         1        2       119
Patient             292        21         0        0       271
...

LIES (5)
  DocumentReference?relationship=kweli-nonexistent-760110  REJECTED  400 searching relationship=kweli-nonexistent-760110
  MedicationRequest?_include=MedicationRequest:medication  IGNORED  no entry with search.mode=include, despite a populated reference in the sample set
  Observation?_include=Observation:encounter  IGNORED  no entry with search.mode=include, despite a populated reference in the sample set
  Observation?code-value-string=kweli-nonexistent-781397  REJECTED  500 searching code-value-string=kweli-nonexistent-781397
  Observation?date=2026-07-20  IGNORED (PARTIAL)  server filtered loosely: returned a different result set than the unfiltered baseline, but not every entry in it matches the searched value

Claims 1626 · Verified 274 · Lies 5 · Untested 1347 · 2m59s · 1076 requests
$ echo $?
1
```

`Observation?code-value-string=...` returning a bare `500` instead of a
clean empty result or a `400` is the kind of thing an integration engineer
finds by hand with curl an hour into writing a client — kweli finds it in
under three minutes, unattended, with a CI-friendly exit code attached.

## Status: Phase 1 (core) + Phase 2 (SMART auth) + Phase 3 (--expect us-core) complete

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
- Verified manually against `https://hapi.fhir.org/baseR4` (brief §6):
  rate limiting held, budget expiry degraded gracefully (partial results
  + a warning, nothing misreported as a lie), and it surfaced real
  findings against a live production HAPI instance — a 500 on a nonsense
  composite-param query, a loose date match, and a few `_include` misses
  — alongside hundreds of correctly verified claims.
- A deep-dive code review before the public push found and fixed 10 real
  correctness bugs (each with a regression test in
  `internal/probe/regression_test.go`), including a PHI leak in
  `--verbose` logging and a failed `vread` that was silently reported as
  a pass. See the git log for the full list.
- **`--smart-backend`** (SMART Backend Services: `client_credentials` +
  `private_key_jwt`, RS384/ES384, token discovery via
  `.well-known/smart-configuration` with a fallback to the
  CapabilityStatement's `oauth-uris` security extension, and token
  caching/refresh) is implemented in `internal/smart`, with a
  self-contained fixture (`internal/testserver/smart.go`) exercising the
  whole flow end to end offline. Also validated against a real,
  independent implementation: dynamically registered a client with
  SMART Health IT's public bulk-data reference server
  (`bulk-data.smarthealthit.org`), and its `/auth/token` endpoint verified
  a real RS384-signed assertion built by this code and issued a genuine
  access token — the brief's own Phase 2 validation step (§6).
- **`--expect us-core`** adds a fourth finding class, `missing`: a search
  param (or whole resource type) US Core 6.1 requires that the
  CapabilityStatement never claims at all — a static comparison
  (`internal/expect`), not a probe, so it costs no extra requests. The
  table is sourced directly from US Core 6.1.0's own reference
  CapabilityStatement (not hand-transcribed from the narrative spec),
  taking the union of individually-mandatory search params and params
  referenced inside mandatory *combination* requirements — most US Core
  resources (Observation, CarePlan, MedicationRequest, ...) mark every
  individual param only "should"/"may" and express the real requirement as
  a combination like `patient+category`, so using only individually-
  mandatory params would make the table nearly useless for exactly the
  resources people care about most. What it deliberately does not check:
  whether a server supports those params search *together* — only that
  each one is individually claimed (see `internal/expect/table.go`'s doc
  comment; this is the brief's own "just a table; no IG parsing" scope).
  Validated both offline (`internal/expect/check_test.go`, plus an
  acceptance test reusing the Phase 1 fixture) and against real data: run
  against HAPI's public sandbox's actual CapabilityStatement, it reports
  zero missing findings — the same "expect it to pass almost everything"
  control as Phase 1's own HAPI validation.

## Tracked debt (named explicitly, not hidden)

- **`--probe-operations` / `$everything` / `$export` / `$validate` are not
  implemented.** Every claimed operation is reported `untested` with a
  clear reason instead of silently skipped or guessed at. Kicking off
  `$export` safely (and cancelling it) against a real production server is
  real engineering, not a Phase 1 shortcut — deferred on purpose. Flag
  exists so `--help` documents the future shape.
- The curated search-param path table (`internal/sample/paths.go`) covers
  ~125 params across 26 resource types — the common base params plus
  most of US Core's clinical and administrative resources (Patient,
  Observation, Condition, Encounter, Procedure, MedicationRequest,
  MedicationDispense, Medication, AllergyIntolerance, Immunization,
  DiagnosticReport, DocumentReference, CarePlan, CareTeam, Goal,
  ServiceRequest, Specimen, Device, Location, Coverage, Provenance,
  QuestionnaireResponse, RelatedPerson, Practitioner, PractitionerRole,
  Organization). Grown from the original ~55/10 by running against a real
  server and watching which resource types stayed stuck at "untested"
  (brief's "Phase 1.5"). Deliberately still missing: any telecom-family
  param (see the comment in paths.go for why a wrong-typed guess is worse
  than no table entry). Params outside the table still get a definitive
  verdict via the nonsense-only query, they just can't produce the
  stronger "every entry matched a real value" verification.

## Layout

```
cmd/kweli/          flag parsing, exit codes
internal/model/      shared Finding/Report vocabulary (probe -> report contract)
internal/capstmt/    minimal CapabilityStatement struct + parser
internal/client/     http client: auth, rate limit, retry, redaction
internal/smart/      SMART Backend Services: JWK loading, private_key_jwt, token discovery/exchange/caching
internal/probe/      one file per probe kind (search, include, paging, read, count, system)
internal/sample/     sample-resource fetch + curated path table (two-query test)
internal/expect/     --expect us-core: US Core 6.1 required-param table + comparison (Phase 3)
internal/report/     text/json/markdown renderers
internal/testserver/ httptest fake server with planted lies + truthful control, plus a SMART auth fixture
```

## Exit codes

- `0`: ran cleanly, nothing in `--fail-on` (default `rejected,ignored`) was found.
- `1`: at least one finding matched `--fail-on`.
- `2`: config or network failure before probing started (bad URL, `/metadata`
  unreachable or malformed, bad flags).
