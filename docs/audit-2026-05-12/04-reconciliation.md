# Mandatory Reconciliation Passes

Each pass is a comparison sweep across two surfaces. Findings from
each pass are recorded in [05-findings-register.md](05-findings-register.md)
with a `pass:R##` tag.

## R00. Per-Area Workstream Ownership Roll-up

Single-page proof that no top-level area is unowned. Generate by
joining `inventory/area-counts.md` against the workstream tags in
`inventory/file-coverage.tsv`.

| Top-level area | Tracked files (prior count) | Owning workstreams |
| --- | ---: | --- |
| `(root)` | 18 | W01 |
| `.github` | 13 | W01, W03, W04, W15 |
| `cmd` | 36 | W03, W06, W11, W13, W19 |
| `configs` | 163 | W05, W14, W18 |
| `deploy` | 41 | W14, W18, W22 |
| `docker` | 7 | W03, W18 |
| `docs` | 421 | W01, W02, W14, W16, W22, W23 (drift) |
| `examples` | 14 | W11, W17, W22, W23 (gen drift), W25 (alias) |
| `internal` | 651 | W02, W05, W06, W07, W08, W09, W10, W11, W12, W13, W14, W15, W19, W26 |
| `migrations` | 57 | W09, W12 |
| `openapi` | 1 | W11, W23 (gen drift) |
| `pkg` | 10 | W11 |
| `scripts` | 27 | W03, W13 |
| `test` | 89 | W15 |
| `web` | 199 | W17 |

If a row's "owning workstreams" column is empty, the audit cannot
close — open a new workstream or extend an existing one.

## R0a. Drift Searches (copy-pasteable)

Run from repo root and capture results in
[evidence/commands.md](evidence/commands.md):

```sh
# Tracked-file census (sanity)
git ls-files | wc -l
git status --short

# Live build/lint/test reality
go list ./...
go test ./...
make verify

# Generic code-rot greps (TODOs, FIXMEs, panics, skipped tests, lint suppressions, ADR-0003 traps)
grep -RnE 'TODO\(|FIXME|XXX:|panic\(|t\.Skip|Skip\(|nolint|//nolint' --include='*.go' . | grep -v '\.discovery-repos'

# ADR-specific traps
grep -RnE 'int64\([a-zA-Z_]+\.Lo\)' --include='*.go' internal cmd     # ADR-0003 truncation grep
grep -RniE 'horizon|horizonclient' --include='*.go' internal cmd       # ADR-0001
grep -RnE 'github\.com/stellar/go/' --include='*.go' internal cmd      # archived monorepo leak

# Secret / config / boundary surfaces
grep -RnE 'RATE|REDIS|DATABASE|JWT|SECRET|TOKEN|KEY|CORS|TRUST|PROXY' internal cmd configs deploy web

# Metric registration vs alert reference
grep -RnE 'Name:[[:space:]]*"[a-z][a-z0-9_]+"' internal/obs internal/api internal/aggregate internal/divergence internal/ledgerstream internal/dispatcher
grep -RnE '^[[:space:]]*expr:' deploy/monitoring/rules

# Schema surface (every DDL line in migrations)
grep -RnE 'CREATE TABLE|CREATE INDEX|CREATE MATERIALIZED|ALTER TABLE|CREATE EXTENSION' migrations

# Removed-route hygiene (rc.48)
grep -RnE '/v1/coins|/v1/currencies' internal cmd web examples openapi docs/reference docs/operations

# Cache key sole-builder (ADR-0007)
grep -RnE 'redis\.NewClient|UniversalClient' --include='*.go' internal cmd | grep -v 'cachekeys/' | grep -v '_test\.go'

# Discovery-repo import leak
grep -RnE 'discovery-repos' --include='*.go' internal cmd

# Embedded data files (//go:embed)
grep -RnE '//go:embed' --include='*.go' internal cmd
```

Every grep hit is a candidate finding or evidence row.

## R01. Code vs Docs

Compare live code against:

- `README.md`, `CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md`
- `docs/adr/` (every ADR's invariant)
- `docs/architecture/` (every claim about topology, dataflow,
  contracts, schema, plans)
- `docs/operations/` (runbooks, deployment state docs,
  release process, public-flip plan)
- `docs/reference/` (auto-generated; verify the generators
  produce the doc that's checked in)
- `docs/discovery/` (read-only archive but referenced by code
  decisions; verify referenced facts still hold)
- `docs/blog/` (published claims must match shipped behavior)
- per-package README.md files
- per-source `events.go` / `decode.go` / `consumer.go` package
  comments

Output: per-doc truth state in evidence log; doc rewrite
recommendations in remediation plan.

## R02. Code vs Tests

For each critical package, compare implementation risk to what
tests actually cover.

Critical packages by risk surface:

- numeric / serialization (`internal/canonical`, `internal/scval`,
  `internal/cachekeys`)
- ingest (`internal/ledgerstream`, `internal/dispatcher`,
  `internal/pipeline`, `internal/hashdb`,
  `internal/archivecompleteness`)
- decoders (`internal/sources/*`)
- aggregation (`internal/aggregate/*`)
- divergence (`internal/divergence`)
- supply (`internal/supply`)
- API handlers (`internal/api/v1`)
- streaming (`internal/api/streaming`, `internal/api/streampublish`)
- auth (`internal/auth`, `internal/platform`)
- storage (`internal/storage/timescale`, `internal/storage/redisclient`)
- ratelimit
- usage / billing (`internal/usage`, `internal/platform/billing.go`)
- notify (`internal/notify`)

For each, record:

- test file count vs source file count
- happy-path-only packages (a finding)
- packages with no malformed-input test (a finding)
- packages with no integration test wiring (depends on risk)

## R03. Tests vs CI

For each test type, verify whether CI actually runs it and
whether `make verify` runs it.

- unit tests (`go test -race -coverprofile`) — must run on every PR
- integration tests (`make test-integration`, build-tag
  `integration`) — must run on PRs that touch storage/decoder
  paths; verify a workflow exists
- chaos (`test/chaos/run.sh`) — local-only? CI only weekly?
  documented?
- load (`test/load/`, k6 weekly) — `.github/workflows/k6-weekly.yml`
  schedule + budgets vs measured perf
- import-boundary lint (`scripts/ci/lint-imports.sh`) +
  baseline integrity
- doc lint (`scripts/ci/lint-docs.sh`)
- monitoring rule lint (`make monitoring-check` + promtool
  installed in `monitoring-rules` job)
- OpenAPI URL lint
- gitleaks secret scan
- govulncheck — is it actually in CI? (note: CI workflow
  shows govulncheck version env var but no job uses it
  yet — verify)

## R04. Handlers vs OpenAPI

Walk every route registered in `internal/api/v1/server.go`
(or wherever routes are mounted). For each:

- present in `openapi/rates-engine.v1.yaml`?
- method, path-params, query-params match?
- request schema (where applicable) matches Go struct used?
- response schema matches Go struct used?
- status codes documented?
- error responses use RFC 7807 type field documented?

Then walk the OpenAPI in reverse: every documented operationId
has a registered handler. Removed routes (rc.48: `/v1/coins`,
`/v1/currencies`) must be absent in both.

## R05. Proposal / RFP vs Implementation

Walk these line-by-line:

- `docs/stellar-rfp.md` — every customer requirement
- `docs/freighter-rfp.md` — F1..F12 fields specifically
- `docs/ctx-proposal.md` + `docs/discovery/proposal-corrections.md`
  — every item we committed vs what shipped
- `docs/discovery/rfp-requirements-matrix.md` — re-test each row
- `docs/discovery/delivery-plan.md` — calendar vs actual

For each row: covered / partial / gap / corrected (with
finding ID where partial/gap).

## R06. ADRs vs Implementation

For each ADR in `docs/adr/`:

| ADR | Invariant | How to verify | Status |
| --- | --- | --- | --- |
| 0001 | No Horizon | grep + `lint-imports.sh` | **verified** — CMD-1212 zero hits; 2 false positives (retention horizon + ADR-0001 comment) |
| 0002 | S3-compat storage only | `internal/storage` + galexie config | **verified** — EV-1226 (Galexie spawns captive-core); EV-1233 (R1 MinIO 4.5T/6.3T); ansible role `galexie.toml.j2:10-18` declares `type="S3"` only |
| 0003 | i128/u128 never to int64 | grep `int64(.*\.Lo)` + tests | **verified** — EV-1235; only `internal/scval/scval.go:231,242` correct Hi/Lo destructure |
| 0004 | Tier-1 three-validator aspiration | infra docs + roadmap | **partial** — R1 only deployed today; R2/R3 docs honest about skeleton state (F-1234) |
| 0005 | Monorepo | `go.mod` location + count | **verified** — single `go.mod` at repo root |
| 0006 | Timescale for prices | `migrations/` + `internal/storage/timescale` | **verified** — 28 migrations + 22 store files + Timescale 2.26.4 on R1 |
| 0007 | Redis cache schema | `internal/cachekeys` is sole builder | **verified** — EV-1237; only canonical builder in `redisclient/redisclient.go:51` |
| 0008 | HA topology | docs + ansible roles | **partial** — ansible roles for patroni/sentinel/haproxy/loki present; R1 single-host today |
| 0009 | Latency budget | SLA probe + measurement | **broken** — F-1221 SLA probe textfile not written; live R1 latency_p95_high alert firing |
| 0010 | Off-chain fiat representation | `AssetFiat` + handler tests | **verified** — `internal/canonical/asset_fiat.go` + live `/v1/price?asset=native&quote=fiat:USD` 200 |
| 0011 | Supply algorithm | `internal/supply/` (algorithms 1/2/3) | **verified** — algorithm files xlm.go/classic.go/sep41.go all wired |
| 0013 | go-stellar-sdk/xdr scoped to scval | lint-imports + grep | **verified** — `scripts/ci/lint-imports.sh` enforces; CMD-1213 returns zero |
| 0014 | Crypto ticker representation | canonical + test | **verified** — `internal/canonical/asset_crypto.go` + round-trip tests |
| 0015 | Last-closed-bucket rate serving | `/v1/price` reader logic | **verified** — `aggregates.go:247,295` `bucket + INTERVAL '1 minute' <= now()` |
| 0016 | Per-region storage strategy | r1/r2/r3 deployment-state docs | **partial** — R2/R3 docs are skeleton with explicit ADR-0016 deltas; tooling refuses single-region (F-1234) |
| 0017 | Archive completeness invariants | `internal/archivecompleteness` | **verified** — code present; F-1219 alert family NOT loaded on R1 (deploy gap, not code gap) |
| 0018 | API consistency surfaces | envelope + pagination + cursor | **verified** — `envelope.go` + per-handler use; F-1235 dashboard handlers bypass |
| 0019 | Anomaly response + confidence scoring | `aggregate/{anomaly,confidence}` | **verified** — `aggregate/confidence/score_test.go:38-260` 13 unit tests; F-1228/F-1229 freeze-row residuals |
| 0020 | Chart API contract | `/v1/chart` handler vs spec | **verified** — handler + OpenAPI agree; recent rc.44 fiat:fiat fix + rc.46 market_cap chart |
| 0021 | Account-entry observer | `internal/sources/accounts` | **verified** — `accounts/decode.go:30-110` wired in indexer + tested |
| 0022 | Classic supply observers | `sources/{trustlines,claimable_balances,liquidity_pools,sac_balances}` + `supply/classic` | **verified** — 4 observer packages + classic.go reader + tests |
| 0023 | SEP-41 supply observer | `sources/sep41_supply` + `supply/sep41` | **verified** — `sep41_supply/decode.go:14-97` + tests |
| 0024 | Redis HA via Sentinel | `redisclient` + ansible role | **partial** — `redisclient.Build` uses redis.UniversalClient; `configs/ansible/roles/redis-sentinel/` exists; R1 single-host today |
| 0025 | Caddy + Cloudflare trusted proxy | Caddy config + middleware | **verified** — `Caddyfile.api:41-66` + `cmd/ratesengine-api/main.go:234-241` |
| 0026 | Stablecoin fiat-proxy late binding | `aggregate/stablecoin.go` | **verified** — `aggregate/stablecoin.go:24-37` map; F-1230 no depeg integ test |

ADR 0012 number is missing from disk — investigate (was it
deleted? renumbered? superseded by another?). If deleted,
record an exclusion or a doc-truth finding.

## R07. Monitoring / Systemd / Runbook Alignment

Three-way reconciliation:

- alert in `deploy/monitoring/rules/<area>.yml` →
- runbook at `docs/operations/runbooks/<alert-name>.md` →
- runbook documents diagnostic, dashboard, SEV escalation,
  postmortem template

Plus systemd:

- service / timer in `deploy/systemd/` →
- runbook references the unit name →
- ansible role provisions the unit

Plus healthchecks:

- `configs/healthchecks/*.timer` →
- script invoked → Healthchecks.io URL pinged →
- failure notification path

Plus alertmanager:

- `configs/alertmanager/alertmanager.r1.yml` route →
- receiver (page/ticket/info) →
- documented in runbook escalation section

## R08. Prior-Audit vs Current Snapshot

Use `docs/audit-2026-04-29/` and `docs/audit-2026-05-02/` only as
delta sources:

- which prior findings are still real
- which were remediated
- which new surfaces appeared since 05-02
- whether remediation introduced drift

For each prior finding (regardless of status), open a fresh test
in this audit. If still real, file a new finding ID; do not
recycle the old ID.

## R09. CG/CMC Parity

Execute [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md).

For each row: covered / partial / gap / non-goal / n/a.

Each `gap` becomes a high or medium finding depending on whether
it blocks the launch claim "competitive with CG/CMC."

## R10. Stellar Coverage Depth

Execute [09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md).

This is the inverse of R09: surfaces where we should be deeper
than CG/CMC. Each `gap` is a launch-quality finding — the
Stellar-depth claim is the durable differentiator.

## R11. Release Surface Reconciliation

For the rc.42..rc.48 commit window:

- `release: v0.5.0-rc.42` — assets-unification: CNY market cap
  goes live → verify CNY market cap actually populates today
- `release: v0.5.0-rc.43` — fiat market cap fix → verify
  VWAPMinTradeCount=1 for FX pairs is still in place
- `release: v0.5.0-rc.44` — Frankfurter FX backfill + fiat
  chart history → verify backfill completion + chart for
  fiat:fiat pair
- `release: v0.5.0-rc.45` — `/v1/markets` query fix +
  explorer version surface → verify markets reads
  `prices_1m` not `trades`
- `release: v0.5.0-rc.46` — `/v1/assets?network=` filter +
  AssetDetail extension + fiat market cap chart → verify
  network filter on R1
- `release: v0.5.0-rc.47` — `/v1/assets` full coin-equivalence
  + explorer migration → verify every old `/v1/coins` field
  is now on `/v1/assets`
- `release: v0.5.0-rc.48` — `/v1/coins` + `/v1/currencies`
  removed → verify the routes return 404/410, not 200; verify
  the explorer no longer references them; verify R1 access log
  shows no internal callers

For each release: changelog entry vs PRs vs deployed reality.

## R14. Third-Pass Adversarial Review of the Audit Itself

Try to falsify the audit. Treat the audit workspace itself as the
target.

Searches:

- **Unowned files.** Any TSV row with empty `workstream` or `evidence_refs` after the audit declares closure.
- **Ambiguous statuses.** Rows in `in_progress` or `blocked` with no exclusion entry or follow-up.
- **Unmapped seams.** Cross-file interactions ledger missing required-class rows (binary→config→pkg, decoder→sink→store, etc.).
- **Findings without evidence.** Any `F-####` with no `EV/CMD/XFI/J/R1` cite.
- **Evidence without source anchors.** Any `EV-####` row whose source ref doesn't resolve to a real file:line.
- **Doc-only claims.** Any finding that cites only a doc, not code/runtime.
- **Severity drift.** Spot-check that severities respect the rubric in `11-severity-rubric.md` *and* the adversarial-multiplier rule.
- **Closed-but-fragile.** Any `closed-by-PR-####` finding where the PR didn't include a verify run + (where applicable) post-deploy R1 probe.
- **Rubber-stamped ADRs.** Any R06 row marked `verified` without a code reference.
- **Matrix gaps.** Any `08-cgcmc-parity-matrix.md` or `09-stellar-coverage-matrix.md` cell still blank.
- **Orphan workstreams.** Any W## that closed without all its checklist rows terminal.
- **Audit-control drift.** Any control doc (00..12) that contradicts another (e.g. tracker says `done` but findings register has open items in that workstream).

Each falsification hit becomes:
- a new finding (severity = the underlying defect's severity if real, `note` if process-only)
- *and* a remediation-plan entry that fixes the audit-control gap

R14 cannot be marked `done` until all R01..R13 are also `done`,
and the auditor has executed at least one falsification grep
returning zero hits.

## R13. Parallel-audit cross-comparison (`audit-2026-05-12-codex`)

A parallel "codex" audit workspace exists at
`docs/audit-2026-05-12-codex/` for the same commit
`80c57e38`. It is *not* a control input to this audit (per
EX-1217), but its *findings* — once both audits land — are
worth comparing:

- findings raised by both = high-confidence (likely real)
- findings raised by only one = investigate why one missed it
- exclusions claimed by both = stronger justification

R13 is intentionally executed only **after** both audits have
populated their findings registers. Do not look at the codex
findings in advance; cold execution requires independence.

When R13 fires:

1. Diff the two findings registers
2. Diff the two exclusion sets
3. Diff the two parity / depth matrices
4. Capture the deltas in a new evidence row + open a finding
   for any *missed* item where the auditor agrees with the
   parallel audit's claim

## R12. Live R1 vs Documented R1

Compare:

- `docs/operations/r1-deployment-state.md` claims →
- live R1 service inventory (SSH probe) →
- live R1 Caddy/Prometheus/Loki/MinIO/Galexie processes →
- live R1 disk + memory pressure
- live R1 access log patterns

Discrepancies (e.g. stellar-core still running on R1, smoke
script in different path, removed-route hits, MinIO 78% disk
usage, full swap) → findings.
