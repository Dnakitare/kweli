# kweli — FHIR CapabilityStatement truth-tester

Handoff brief for Claude Code. Read fully before writing code.
Working name `kweli` (Swahili: "true"). Rename freely; keep the binary name short.

## 1. One-paragraph pitch

Point `kweli` at a FHIR base URL. It reads `/metadata`, then actually exercises
every claim in the CapabilityStatement — resource types, interactions, search
parameters, includes, paging, operations — and reports which claims are true,
which are false, and which are silently ignored. Runs in under a minute,
read-only, single static binary, non-zero exit code when lies are found so it
drops into CI. This is the check every integration engineer does by hand with
curl before writing a line of integration code. Nobody has packaged it.

## 2. Goals and non-goals

Goals
- Answer one question: does this server do what its CapabilityStatement says?
- Detect the three failure modes that matter in practice:
  1. **Rejected** — claimed feature returns 4xx/OperationOutcome.
  2. **Silently ignored** — search param accepted but has no effect on results.
     This is the most common lie and the most expensive to discover late.
  3. **Broken paging** — `next` link 404s, loops, or drops results.
- Read-only against production systems. Safe to run with real credentials.
- Zero runtime dependencies. `brew install`, `go install`, or download a binary.
- Machine-readable output for CI.

Non-goals (do not build these)
- Not a validator. Does not check resource conformance to profiles. Inferno,
  HAPI validator, and Firely Terminal own that.
- Not a conformance suite. Does not test US Core requirements unless the user
  opts in with `--expect us-core` (Phase 3).
- No writes, ever. No create/update/delete/patch/transaction probes.
- No web UI. No hosted service. No database.
- No FHIRPath engine. See §5.3 for how we avoid needing one.

## 3. CLI UX

```
kweli https://hapi.fhir.org/baseR4
kweli https://fhir.example.org/r4 --token "$TOKEN"
kweli https://fhir.example.org/r4 --smart-backend --client-id X --jwk key.json
kweli https://fhir.example.org/r4 --resources Patient,Observation,Condition
kweli https://fhir.example.org/r4 --format json > report.json
kweli https://fhir.example.org/r4 --expect us-core          # Phase 3
kweli https://fhir.example.org/r4 --fail-on ignored,rejected # CI mode
```

Flags
- `--token` bearer token. `--smart-backend --client-id --jwk --token-url`
  SMART Backend Services (client_credentials with private_key_jwt). Phase 2.
- `--resources` comma list; default = every type in the CapabilityStatement.
- `--concurrency` default 4. `--rps` default 8. Never exceed either.
- `--timeout` per request, default 30s. `--budget` total wall clock, default 5m.
- `--format` text (default, TTY-aware), json, markdown.
- `--fail-on` comma list from {rejected, ignored, paging, untested}. Default
  `rejected,ignored`. Exit 1 if any finding in the set; exit 2 on config or
  network failure before probing starts; exit 0 otherwise.
- `--probe-operations` opt-in for `$everything`, `$export` kickoff+cancel,
  `$validate`. Default off because `$export` costs the server real work.
- `--no-color`, `--quiet`, `--verbose` (prints every request line, never bodies).

Output (text)

```
kweli  https://fhir.example.org/r4   FHIR 4.0.1   software: Foo Server 3.2

Resource      Claimed  Verified  Rejected  Ignored  Untested
Patient            14        11         1        2         0
Observation        18        12         3        2         1
Condition          10        10         0        0         0
...
Paging: OK (Patient, 3 pages)   Includes: 2/4 OK   Operations: skipped

LIES (6)
  Observation?code=...          IGNORED   result set identical to unfiltered query
  Observation?value-quantity=.. REJECTED  400 "unknown search parameter"
  Patient?_include=Patient:general-practitioner
                                 REJECTED  400 (claimed in rest.resource.searchInclude)
  ...

UNTESTED (1)
  Observation?component-code   no sample value found in first 50 resources

Claims 214 · Verified 180 · Lies 22 · Untested 12 · 41s · 187 requests
```

JSON output is the same data as a single object: `{server, fhirVersion,
software, findings: [{resource, claim, kind, status, detail, request,
statusCode}], summary, timing}`. Stable field names — people will script
against it.

## 4. Architecture

Single Go module, stdlib-first. Target Go 1.23+.

```
cmd/kweli/main.go          flag parsing, exit codes
internal/capstmt/          minimal CapabilityStatement struct + parser
internal/client/           http client: auth, rate limit, retry, redaction
internal/probe/            one file per probe kind (search, include, paging, read, ops)
internal/sample/           fetch sample resources, extract param values (§5.3)
internal/report/           text/json/markdown renderers
internal/testserver/       httptest fake server with planted lies (§7)
testdata/                  real CapabilityStatements from public servers
```

Dependencies allowed: `golang.org/x/sync/errgroup`, `golang.org/x/time/rate`,
`github.com/spf13/cobra` (or stdlib `flag` if you prefer — either is fine),
`github.com/golang-jwt/jwt/v5` for SMART backend auth in Phase 2. Nothing else
without a reason written in the PR. Specifically do **not** pull a generated
FHIR model library; treat resources as `map[string]any` and the
CapabilityStatement as a hand-written struct covering only the fields we read:

```go
type CapabilityStatement struct {
    FHIRVersion string
    Software    struct{ Name, Version string }
    Rest []struct {
        Mode     string
        Resource []struct {
            Type          string
            Interaction   []struct{ Code string }
            SearchParam   []struct{ Name, Type, Definition string }
            SearchInclude []string
            SearchRevInclude []string
            Operation     []struct{ Name, Definition string }
        }
        Interaction []struct{ Code string }
        Operation   []struct{ Name, Definition string }
    }
}
```

`encoding/json` into that struct; unknown fields drop on the floor. R4, R4B,
R5 all parse with this shape.

## 5. Probe strategy

Order matters. Cheap probes gate expensive ones.

### 5.1 Bootstrap
1. `GET {base}/metadata` with `Accept: application/fhir+json`. Fail fast on
   non-200, non-JSON, or `resourceType != CapabilityStatement` (exit 2).
2. If `--smart-backend`, read `rest.security.extension` for the OAuth URIs
   extension to find `token`; fall back to `--token-url`. Also try
   `.well-known/smart-configuration`.
3. Record `fhirVersion`, `software`, and the claim inventory. Every claim gets
   a stable ID: `Patient/search/birthdate`, `Patient/include/Patient:organization`,
   `Observation/read`, `system/operation/export`.

### 5.2 Per-resource probes (run concurrently across resources, sequentially
within a resource)

For each claimed resource type `T`:

a. **Unfiltered search** `GET T?_count=50`. Records: status, `total` if
   present, entry count, `link[rel=next]`, and keeps the entries as the
   **sample set** for this resource. If this returns 0 entries the resource is
   marked `untested` for everything downstream except `read` (skip) — you
   can't prove a param filters if there's nothing to filter.

b. **read** (if claimed): `GET T/{id}` for the first sample id. Verify 200 and
   `id` matches. **vread** if claimed: `GET T/{id}/_history/{vid}` if
   `meta.versionId` present.

c. **Search params** (if `search-type` claimed): for each `searchParam` in
   the claim, run the **two-query test** (§5.3). Skip params of type
   `special` and params starting with `_` except `_id`, `_lastUpdated`,
   `_tag`, `_profile`, `_security` (test these; they're commonly lied about).

d. **Includes**: for each `searchInclude` `T:param`, run
   `GET T?_count=5&_include=T:param`. Verify 200 and that at least one entry
   has `search.mode == "include"` **when** the sample set has a populated
   reference at that param; otherwise `untested`. Same for `_revinclude`.

e. **Paging**: if (a) returned a `next` link, follow it up to 3 hops. Verify
   each hop is 200, returns a Bundle, and no `fullUrl` repeats across hops.
   Report `paging: broken` with the hop that failed.

f. **_count honoured**: `GET T?_count=2`, expect ≤ 2 entries. Servers that
   ignore `_count` will ignore other things.

### 5.3 The two-query test (search param verification without FHIRPath)

The naive approach evaluates each param's FHIRPath expression against a sample
resource to find a real value, then searches for it. That needs a FHIRPath
engine. Don't build one. Do this instead:

1. **Value extraction by table, not by FHIRPath.** Ship a curated map from
   `(resourceType, paramName)` → a plain JSON path in the sample resource, for
   the ~120 params that matter (US Core's set plus the common base params):
   `Patient/birthdate → birthDate`, `Observation/code → code.coding[0]` (render
   as `system|code`), `Condition/patient → subject.reference`, and so on. Store
   as a Go map in `internal/sample/paths.go`. Params not in the table fall
   through to step 3.

2. **Positive query.** Search `T?{param}={extracted value}&_count=50`. Expect
   200 and that every returned entry, when the same path is extracted, matches
   the value (token: system|code match on system+code; date: prefix match on
   day; reference: id match; string: case-insensitive prefix). If all match →
   `verified`. If some don't match → `ignored (partial)` — server filtered
   loosely or not at all. If 4xx → `rejected`.

3. **Nonsense query** (also run for params with no table entry). Search
   `T?{param}=kweli-nonexistent-{random}&_count=50` with a type-appropriate
   nonsense value (token: `urn:kweli|zz`, date: `1800-01-01`, reference:
   `T/kweli-nonexistent`, string: `kweliZZZ`, number/quantity: `-999999`).
   Compare against the unfiltered result from 5.2(a):
   - 200 with 0 entries → param **has effect** → `verified` (weak) if step 2
     couldn't run, else confirms step 2.
   - 200 with the **same entries** as unfiltered (compare the set of
     `fullUrl`/`id`) → **IGNORED**. This is the headline finding.
   - 4xx → `rejected`.
   - 200 with a different non-empty set → `inconclusive`, report as untested
     with detail.

4. Servers that return `OperationOutcome` with `severity=warning` inside the
   Bundle for unknown params get classed as `ignored` and the warning text is
   surfaced. Check `Bundle.entry[].resource.resourceType == OperationOutcome`
   and `Bundle.issue` (R5).

Also honour `Prefer: handling=strict` — send it on the nonsense query. A
server that properly rejects unsupported params under strict handling is
telling the truth about a gap; report that as `rejected (strict)` not a lie.

### 5.4 System-level probes
- `_history` at system level if claimed: `GET _history?_count=1`.
- `--probe-operations` only: `$everything` on first Patient sample;
  `$export` kickoff with `Prefer: respond-async` then immediately `DELETE`
  the content-location; `$validate` POST of a sample resource back to the
  server (this is the one POST we allow — it's non-mutating by spec).

### 5.5 Politeness and safety
- Global `rate.Limiter` from `--rps`, `errgroup` with `--concurrency`.
- On 429 or 503: honour `Retry-After`, else exponential backoff 1s→16s,
  max 3 retries, then mark the claim `untested (throttled)` and continue.
- `--budget` is a hard `context.WithTimeout`; on expiry, report what finished
  and list the rest as `untested (budget)`.
- Never log response bodies. `--verbose` prints method, URL, status, latency
  only. Redact `Authorization` and any query param named `identifier`,
  `name`, `birthdate`, `address*`, `telecom`, `email`, `phone` in verbose
  output (these carry PHI when the sample values come from real data).
- Sample sets live in memory only. No `--dump`. If someone wants that, they
  can use curl.

## 6. Phases

Phase 1 — core (target: one weekend)
- Bootstrap, per-resource probes 5.2(a–f), two-query test with the curated
  path table (start with ~40 US Core params; grow the table as a Phase 1.5),
  text + JSON output, exit codes, `--token`.
- Fake server with planted lies (§7) and CI running against it.
- Verified manually against `https://hapi.fhir.org/baseR4` and the SMART
  bulk-data reference server. Expect HAPI to pass almost everything; that's
  the control.

Phase 2 — auth
- SMART Backend Services: RS384/ES384 `private_key_jwt`, `.well-known`
  discovery, token caching. Test against SMART's public bulk-data server in
  auth mode.

Phase 3 — expectation mode
- `--expect us-core` loads an embedded list of US Core 6.1 required search
  params per resource and adds a fourth finding class: **missing** (required
  by the IG, absent from the CapabilityStatement). Just a table; no IG parsing.

Phase 4 — distribution
- goreleaser: darwin/linux/windows, amd64/arm64. Homebrew tap. `go install`.
  A GitHub Action wrapper (`uses: Dnakitare/kweli-action@v1` with `url`,
  `token`, `fail-on` inputs). README with a real screenshot of a real server
  lying — that screenshot is the entire marketing plan.

Do not start Phase 2 until Phase 1 is shipped and tagged.

## 7. Acceptance test: the lying server

`internal/testserver` is an `httptest.Server` that serves a synthetic R4
CapabilityStatement and ~200 synthetic resources (generate deterministically
from a seed; no Synthea dependency). It plants these defects. Phase 1 is done
when `kweli` reports every one of them and nothing else:

1. `Observation?code` — claimed, silently ignored (returns unfiltered set).
2. `Patient?birthdate` — claimed, rejected with 400 + OperationOutcome.
3. `Patient?name` — claimed, partially honoured (prefix match ignores case,
   returns extra entries).
4. `Condition?_include=Condition:subject` — claimed, rejected.
5. `Encounter` paging — `next` link on page 2 returns 404.
6. `Procedure` paging — page 3 repeats page 2's entries.
7. `Observation?_count` — ignored (always returns 20).
8. `MedicationRequest/read` — claimed, returns 500.
9. `Patient?_lastUpdated` — claimed, ignored.
10. `AllergyIntolerance` — claimed with search, but search returns 0 entries
    (should be `untested`, not a lie).
11. `Immunization?date` — honoured only with `Prefer: handling=strict` → must
    be reported as `rejected (strict)`, not `ignored`.
12. A search param claimed on `DiagnosticReport` that is not in the path
    table (`based-on`) — nonsense query must still classify it.

Every defect gets a test that asserts the exact finding ID and status. Add a
"truthful" control server (same data, no defects) and assert zero lies.

## 8. Risks and known ugliness

- **`total` is optional and often wrong.** Never use `Bundle.total` for
  comparison; compare entry ids.
- **Servers with `_count` caps below 50** shrink the sample set. Fine; note
  the effective page size in the report header.
- **Multi-tenant / scoped tokens** return a CapabilityStatement broader than
  what the token can see. Report as-is; the doc's claim is still the claim.
  Add a note in the header if any resource returns 403 on unfiltered search.
- **Epic and Oracle sandboxes** rate-limit hard and return non-standard
  errors. Test against them only in Phase 2 with `--rps 2`. Their lies are
  the reason this tool exists, so don't skip them.
- **Search-param `Definition` URLs** sometimes point at custom params. Treat
  unknown params like any other: nonsense query only.
- **Reference params with chained/`:identifier` modifiers** — out of scope.
  Test the bare param only.

## 9. What "done" looks like for the whole project

A stranger runs one command against their vendor's sandbox, gets a table with
red rows in under a minute, and pastes it into a ticket. That's it. If the
README screenshot doesn't make an integration engineer wince, the tool isn't
finished.
