# Execution Protocol

## 1. Zero-Trust Rule

Docs are not facts. For any claim from markdown, architecture notes,
prior audits, discovery artifacts, runbooks, ADRs, RFP/proposal
text, CHANGELOG entries, or even CLAUDE.md:

1. locate the live code path (file + line)
2. locate the test or runtime wiring that exercises it
3. record whether the doc is true, stale, partial, or contradicted

Past-tense audit artifacts (`docs/audit-2026-04-29`, `docs/audit-2026-05-02`)
list **what someone looked at then**, not **what is true now**. Their
findings (open or closed) are re-tested cold here.

## 2. Adversarial Frame

For every component, ask three additional questions beyond
correctness:

1. **What's the abuse vector?** What does an unauthenticated user,
   a low-tier paying user, a hostile peer in the validator network,
   or a misbehaving upstream feed get if they push this surface?
2. **What's the silent-failure mode?** What does this surface return
   when its dependency is degraded — wrong number, stale number,
   or none?
3. **What's the trust boundary?** Where does external data first
   enter our system? Is it validated, scaled, classed, and clocked
   correctly? Could it impersonate our internal data?

Findings from this frame go in [05-findings-register.md](05-findings-register.md)
with severity per [11-severity-rubric.md](11-severity-rubric.md).

## 3. Evidence Discipline

Nothing is accepted from memory or from prior audits. Every
material claim must cite at least one of:

- a local file reference with line anchor (`internal/foo/bar.go:123`)
- a generated inventory artifact in this directory
- a command output captured in `evidence/log.md` or
  `evidence/r1-probes/*.md`
- a test file and the *behavior it actually asserts*
- an OpenAPI excerpt referenced by line
- a SQL migration referenced by line
- a Prometheus rule expression referenced by file + rule name
- an ADR referenced by ID + invariant text

When two pieces of evidence disagree, both are recorded; the
disagreement itself becomes a finding.

## 4. Evidence Log Format and ID Taxonomy

Evidence is split across multiple ledgers, each with its own ID
prefix. This makes filtering by class trivial and prevents one
file from becoming the catch-all.

| Prefix | Ledger | Used for |
| --- | --- | --- |
| `EV-####` | [evidence/log.md](evidence/log.md) | general code/test/runtime observations |
| `CMD-####` | [evidence/commands.md](evidence/commands.md) | shell command transcripts (with exact output) |
| `XFI-####` | [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md) | every material seam |
| `J-####` | [journeys-traces/J##-*.md](journeys-traces/) | one ID per completed journey trace |
| `R1-####` | [evidence/r1-probes/](evidence/r1-probes/) | live R1 SSH probes |
| `XFI-CLASS-####` | same as XFI but for *interaction classes* (binary→config→pkg, decoder→sink→store, etc.) | covers W26's class-roll-up gate |

Each row across all ledgers records:

- the prefixed ID
- date (UTC, ISO-8601)
- claim or observation
- source refs (file:line or transcript path)
- workstream(s) (W01..W26)
- notes (max 200 chars)

IDs are assigned monotonically inside their ledger. Do not reuse
IDs. If an entry is invalidated, mark it `superseded by <NEW-ID>`
rather than deleting.

A finding can cite IDs from any ledger; cross-ledger citations
are encouraged.

## 5. Per-File Audit Loop

For every tracked file in `inventory/file-coverage.tsv`:

1. **Read the file directly.** No second-hand summaries.
2. **Classify file role** using the controlled vocabulary
   (`file_kind` column in the TSV):
   `runtime`, `test`, `fixture`, `migration`, `config`,
   `deploy`, `workflow`, `script`, `documentation`,
   `generated`, `frontend`, `asset`, `policy`, `unknown`.
   The role determines the rest of the loop's emphasis.
3. **Identify inbound dependencies.** Imports, callers,
   routes, workflows, scripts, docs, generated inputs.
4. **Identify outbound dependencies.** Imports, commands,
   network calls, database objects, cache keys, files,
   env vars, metrics, alerts, docs.
5. **Identify trust boundaries.** External input, user input,
   ledger data, third-party API, DB, Redis, filesystem,
   CI secret, SSH host, browser, Cloudflare, systemd.
6. **Identify invariants.** Precision, idempotency,
   consistency, freshness, auth, ordering, schema, source
   attribution, rate limit, timeout, privacy.
7. **Identify tests that exercise it.** Direct unit tests,
   integration coverage, or no test at all? What do the
   tests actually assert?
8. **Identify docs that describe it.** ADR, architecture note,
   runbook, README, comment block; classify doc truth.
9. **Update status.** Terminal: `done` / `excluded` / `blocked`.
10. **Record evidence refs.** Cite at least one ID per
    ledger (EV / CMD / XFI / J / R1 — not all are required;
    pick what proves the claim).

Allowed statuses:

- `todo`
- `in_progress`
- `done`
- `blocked`
- `excluded`

A file marked `done` must have at least one evidence ref.
`done` means *reviewed with evidence* — it does not mean
bug-free.

## 6. Per-Decoder Audit Loop

For every source decoder in `internal/sources/<source>/`:

1. **Claim surface.** What event topics / op kinds / contract
   IDs / methods does this decoder claim to handle?
2. **Decode entry function(s).** Trace from dispatcher routing
   to the decoder's first XDR read.
3. **Malformed-input handling.** Construct a malformed payload
   in test or by inspection; verify graceful failure (no panic,
   typed error, drop counter increment).
4. **Storage / consumer integration.** Where does the decoded
   trade/observation go? What table? What sink interface?
5. **Fixture realism.** Are golden files in `test/fixtures/`
   captured from real ledgers? Are they regenerated with
   `scripts/dev/capture-*-fixtures.sh`? When was the last
   capture (file mtime + git log on the fixture)?
6. **Tests vs actual risk.** What is asserted? What is left
   unproven? Specifically:
   - happy-path-only: a finding
   - no malformed-input test: a finding
   - no WASM-version dispatch test (when source has multiple
     deployed versions): a finding
   - no decoder/sink integration test: a finding for
     production-routed decoders
7. **WASM audit status.** `BackfillSafe` flag in
   `internal/sources/external/registry.go` vs
   `docs/operations/wasm-audits/<source>.md` evidence trail.
8. **Surprise list compliance.** CLAUDE.md "Things that will
   surprise you" includes per-source caveats (Soroswap reserves,
   Phoenix 8-event grouping, Comet shared topic, Reflector 3-contract
   split, Band zero-event, Redstone feed_id absence, etc.).
   Each caveat is a test claim; verify a test asserts it.

## 7. Per-Migration Audit Loop

For every `migrations/####_*.up.sql` and matching `.down.sql`:

1. **Up + down symmetry.** Does down actually reverse up?
2. **Concurrent-safe DDL.** `CREATE INDEX CONCURRENTLY`,
   `ADD COLUMN ... DEFAULT NULL`, etc., where applicable.
3. **Hypertable / continuous-aggregate semantics.** Window
   intervals, refresh policy, compression policy, retention.
4. **NUMERIC vs BIGINT.** ADR-0003 invariant: any column that
   stores i128 amounts must be NUMERIC.
5. **Index coverage.** Hottest queries hit indexes? (cross-ref
   with `internal/storage/timescale/*.go` queries.)
6. **Trigger / view drift.** Materialized views and triggers
   that depend on this migration.
7. **Reader correspondence.** Find the `internal/storage/timescale/`
   reader/writer that uses this table. Drift = finding.

## 8. Per-Route Audit Loop

For every HTTP route registered in `internal/api/v1/server.go`
(or wherever routes are mounted):

1. **OpenAPI presence.** Route + method present in
   `openapi/rates-engine.v1.yaml`? Parameters, schemas, status
   codes match?
2. **Envelope conformance.** Response uses
   `internal/api/v1/envelope.go` (`data`, `as_of`, `flags`,
   `pagination`)? Errors are RFC 7807?
3. **Auth gate.** Public, API-key, or admin?
4. **Rate-limit identity.** Per-key or per-IP? Exempt internal?
5. **Cache headers.** ETag, max-age, Vary, Cache-Control.
6. **Pagination.** Cursor-based per ADR-0018? Stable across
   inserts?
7. **Empty / not-found shape.** 200 + empty data vs 404 — which?
8. **Latency budget.** Target p50/p95/p99 from
   `docs/operations/sla-probe.md` and ADR-0009; live R1
   measurement.
9. **Test coverage.** Unit handler test + integration test?
   `pkg/client/endpoints_test.go` covers the wire shape?
10. **Removed-route hygiene.** If the route was deleted (e.g.
    `/v1/coins`), no internal callers remain, deprecation
    headers were emitted prior to removal, examples + Postman
    + explorer no longer reference it.

## 9. Per-External-Source Audit Loop

For every adapter in `internal/sources/external/<vendor>/`:

1. **Vendor truth.** API docs URL, current rate limits,
   redistribution licence.
2. **Auth hygiene.** Keys come from env (`internal/config`),
   never from code; key absence = source disabled, not panic.
3. **Normalization.** Amount scale 10^8 (per CLAUDE.md), pair
   normalisation matches our canonical Pair (no XLM/USD
   shorthand silently mapping to XLM/USDT).
4. **Retry + backoff + jitter.** Exponential, capped, jittered.
5. **Clock skew.** Vendor `ts` vs our wall clock — guard rails.
6. **Class.** `ClassExchange` / `ClassAggregator` / `ClassOracle`
   / `ClassAuthoritySanity` per `internal/sources/external/registry.go`.
7. **Inclusion policy.** Aggregator-feeding by default, or
   divergence-only?
8. **Backfill safety.** `BackfillSafe` true requires WASM
   audit (for on-chain) or vendor history determinism (for
   off-chain).
9. **Failure-mode coverage.** Vendor 5xx, 429, 401, network
   timeout — tests assert each.

## 10. Per-Alert Audit Loop

For every Prometheus rule under `deploy/monitoring/rules/<area>.yml`:

1. **Expression provability.** Does the metric name actually
   exist? (Cross-ref `internal/obs/metrics.go` + per-binary
   metric registration.)
2. **Threshold defensibility.** Does the threshold derive from
   ADR / SLA / runbook, or is it a guess?
3. **Severity tier.** Maps to `configs/alertmanager/alertmanager.r1.yml`
   route?
4. **Runbook link.** `runbook_url` annotation points to a
   `docs/operations/runbooks/<name>.md` that exists?
5. **Runbook content.** Runbook describes diagnosis steps,
   dashboard links, escalation, postmortem template?
6. **Firing test.** Has this alert ever fired in R1 (verify
   in Alertmanager history)? If never, is the alert
   reachable in principle, or is it dead?

## 11. Per-Doc Audit Loop

For every doc file:

1. **Frontmatter age.** `last_verified` <90 days = ok,
   90-180 = warn, >180 = stale (mirror `scripts/ci/lint-docs.sh`
   policy).
2. **Truth claims.** Identify every factual claim; trace each
   to code/test/runtime evidence.
3. **Stale references.** Removed packages, removed routes,
   removed tools, replaced libraries.
4. **Contradiction.** Does it disagree with another doc or
   ADR? If so, both go to findings.

## 12. Findings Rules

Use [05-findings-register.md](05-findings-register.md).

Each finding needs:

- stable ID (`F-####`)
- severity per [11-severity-rubric.md](11-severity-rubric.md)
- concise title (≤80 chars)
- affected surface (file paths + line numbers)
- evidence refs (any prefix per §4 — `EV-####`, `CMD-####`,
  `XFI-####`, `J-####`, `R1-####`)
- workstream(s) (W01..W26)
- adversarial vector (if applicable)
- disposition: see status taxonomy below

**Finding status taxonomy:**

| Status | Meaning |
| --- | --- |
| `open` | reviewed, real, not yet remediated |
| `needs_evidence` | suspected but evidence is incomplete; do not promote to `open` until evidence is in a ledger |
| `needs_owner` | confirmed real, awaiting wave/owner assignment in the remediation plan |
| `accepted` | risk explicitly accepted; requires a `note`-class entry recording the reason and date |
| `wontfix` | will not be fixed; requires reasoning + product/operator confirmation |
| `closed-by-PR-####` | code/docs/infra change merged + verify rerun + (where applicable) post-change R1 probe |
| `duplicate` | cross-references a prior finding in this audit; the prior ID owns the remediation |
| `invalid` | retracted after deeper review; record why so future auditors don't re-raise |

Severity scale:

- `critical` — data loss, security breach, money loss, brand-ending
  (see [11-severity-rubric.md](11-severity-rubric.md))
- `high` — silent-bad-data, prolonged outage, contract breach
- `medium` — degraded UX, observability gap, operator confusion
- `low` — doc drift, naming inconsistency, minor inefficiency
- `note` — informational; no action required

## 13. Exclusions Rules

Use [06-exclusions-register.md](06-exclusions-register.md).

Any skipped scope must record:

- exact excluded thing (path or behavior)
- reason (impossible / out-of-scope / requires-external-access)
- temporary or permanent for this audit
- evidence needed to re-enter scope

## 14. Test Interpretation Rules

Tests prove asserted behavior only.

For each important suite, record in evidence:

- what it actually asserts
- what it leaves unproven
- whether CI runs it (gate vs informational)
- whether live runtime wiring could still break despite green tests

## 15. Live R1 Probe Rules

Live probes via SSH (`root@136.243.90.96`) are encouraged. Each
probe is captured as a transcript in
`evidence/r1-probes/<topic>-<YYYYMMDD>.md` with:

- time of probe (UTC)
- exact command(s)
- raw output (truncated to relevant lines)
- claim being tested
- finding ID if a discrepancy is observed

Probe protocol details: [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md).

## 16. CG/CMC Parity Matrix Rules

Use [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md). Each
row is one feature. Mark:

- `covered` — we ship it, with proof
- `partial` — we ship some of it; specify the gap
- `gap` — we don't ship it; finding required
- `non-goal` — explicit product decision; cite the decision
- `n/a` — feature is structurally impossible for our scope

## 17. Stellar Depth Matrix Rules

Use [09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md).
Same scoring as the CG/CMC matrix, but rows describe surfaces
where we *should* be deeper than CG/CMC. Each `gap` row is a
launch-quality finding.

## 18. Docs-Truth Rules

When docs disagree with code:

- do not silently trust either side
- log evidence from both sides
- state whether the doc overstated, understated, or contradicted reality
- prefer changing the doc over weakening the code unless the doc
  describes an *intent* the code never reached (in which case
  the code change is the right fix)

## 19. Cross-File Interaction Log

Use [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md).

Record seams such as:

- binary -> package wiring (`cmd/* main.go` -> `internal/*`)
- package -> package interfaces
- handler -> storage adapter
- decoder -> sink -> storage
- aggregator -> Redis -> API
- workflow -> script -> generated artifact
- runbook or proposal text -> code path it purports to describe
- ADR text -> import-boundary lint rule
- live R1 traffic pattern -> handler -> storage query

Each row should make it possible for a reviewer to find both
ends of the seam without searching.

## 20. Final Condition

The audit is complete only when the control docs, evidence
logs, inventory, findings, exclusions, and remediation plan can
be followed without relying on undocumented context.

A reviewer should be able to:

1. Open this directory cold
2. Read README → 00-plan → tracker
3. Locate every claim's evidence
4. Re-walk every workstream in their own session
5. Re-test every finding without needing the original auditor

If any of those five steps requires asking the original auditor
a question, the audit is not done.
