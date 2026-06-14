# Comprehensive whole-system audit — plan (2026-06-14)

Goal: a **fully comprehensive** audit of the entire repository — every file
and every cross-file/cross-package interaction. Plan first; iterate the plan
until there are no uncovered areas (§4 self-check); then execute (§5); then
self-verify the executed audit is complete (§6).

Scope at time of writing: 942 Go files (~215k LOC) across 33 `internal/`
packages + `cmd/` (6 binaries) + `pkg/` (public SDK); 122 migrations; ~200
config/ansible files; 41 scripts; 40 ADRs; 153 `web/explorer` source files;
the OpenAPI spec; CLAUDE.md.

## 1. Audit dimensions (applied to every area)

Each area is read against this checklist:

- **D1 Correctness** — logic bugs, off-by-one, wrong defaults, mis-decodes,
  nil derefs, unchecked type assertions, error-swallowing.
- **D2 ADR invariants** — i128 never→int64 (ADR-0003); one-writer-per-domain
  (ADR-0031/0032); ingest only via Galexie→dispatcher, never stellar-rpc
  (CLAUDE.md); ClickHouse=lake / Postgres=served (ADR-0034); factory-anchored
  gating (ADR-0035); closed-bucket serving (ADR-0015); no Horizon (ADR-0001);
  EVERY-event decoders; XLM dual-form alias loops.
- **D3 Security** — authn/authz gaps, API-key permission posture, SQL
  injection (string-built SQL), secrets in code/logs, input validation,
  SSRF/path traversal, rate-limit bypass, PII in logs.
- **D4 Concurrency** — data races, unguarded shared maps, goroutine leaks,
  context propagation, cancellation, deadlocks.
- **D5 Resource/lifecycle** — leaked rows/conns/files/goroutines, unbounded
  queries/scans (the no-unbounded-trade-scan rule), missing timeouts, batch
  sizing, memory blowups.
- **D6 Data/schema** — migration correctness + reversibility, cagg/retention
  drift (no rogue trades retention), index coverage vs query patterns, NUMERIC
  for i128, FK/constraint integrity, ReplacingMergeTree dedup keys.
- **D7 API contract** — OpenAPI ↔ handler parity (paths, params, shapes,
  status codes), envelope consistency, error (problem+json) consistency,
  pagination correctness, cache-key/prewarm drift.
- **D8 Tests** — coverage gaps on critical paths, test rot (asserting stale
  behavior), missing regression tests for known traps, integration-tag gaps.
- **D9 Docs/config drift** — CLAUDE.md / ADRs / reference vs reality; config
  defaults vs deployed; dead alerts/runbooks; stale brand.

## 2. Coverage partition (every file → exactly one area)

> Provable completeness: §4 cross-checks `find` output against this list so no
> file is unassigned.

- **A01 Ingest core** — `internal/ledgerstream`, `internal/dispatcher`,
  `internal/consumer`, `internal/pipeline`.
- **A02 Lake (ClickHouse)** — `internal/storage/clickhouse` (incl. the new
  explorer_reader + extract_entry_changes) + `deploy/clickhouse`.
- **A03 Served tier (Timescale/Redis/MinIO)** — `internal/storage/timescale`,
  `internal/storage/redis`, `internal/storage/minio` (+ any others under
  `internal/storage`).
- **A04 Projector + completeness** — `internal/projector`,
  `internal/completeness`, `internal/archivecompleteness`, `internal/hashdb`.
- **A05 Sources: on-chain Soroban** — `internal/sources/{soroswap,phoenix,
  aquarius,comet,blend,defindex,cctp,rozo,band,redstone,reflector,sep41,
  childgate,...}` (decoders + gating).
- **A06 Sources: external CEX/FX** — `internal/sources/external/*`.
- **A07 Sources: supply observers** — `internal/supply` + supply observer
  packages under `internal/sources`.
- **A08 Canonical + scval + events** — `internal/canonical`, `internal/scval`,
  `internal/events`, `internal/xdrjson`.
- **A09 Aggregation** — `internal/aggregate`, `internal/divergence`,
  `internal/currency`, `internal/metadata`.
- **A10 API: pricing/catalogue** — `internal/api/v1` pricing/assets/markets/
  history/ohlc/oracle/coverage/protocols/supply handlers.
- **A11 API: explorer (new)** — explorer_*.go handlers + `internal/api`
  middleware + envelope + server wiring + routing.
- **A12 API: auth/account/dashboard/platform** — `internal/auth`,
  `internal/api/v1` account/signup/dashboard, `internal/platform`,
  `internal/ratelimit`, `internal/usage`, `internal/cachekeys`.
- **A13 Binaries (cmd)** — `cmd/stellarindex-{indexer,aggregator,api,ops,
  migrate,sla-probe}` (wiring, flags, lifecycle).
- **A14 Public SDK** — `pkg/client`.
- **A15 Migrations + schema** — `migrations/*.sql` (sequence, up/down,
  retention, caggs, indexes).
- **A16 Obs/notify/incidents/divergence-infra** — `internal/obs`,
  `internal/notify`, `internal/incidents`, `internal/customerwebhook`,
  `internal/obstest`, `internal/stellarrpc`, `internal/version`,
  `internal/config`.
- **A17 Explorer UI** — `web/explorer/src/**`.
- **A18 Ops surface** — `configs/**`, `scripts/**`, `deploy/**` (non-clickhouse),
  `.github/workflows`, Makefile.
- **A19 Docs integrity** — CLAUDE.md, `docs/adr/**`, `openapi/`, `docs/reference`,
  README + the freshness/consistency checks.

## 3. Cross-file / cross-package interaction passes (the integration audit)

Beyond per-area reads, explicitly trace these seams end-to-end:

- **X1 Ingest dataflow** — LCM → ledgerstream → dispatcher (4 decoder seams)
  → sink → CH lake → projector → Postgres → API. Verify type contracts +
  one-writer at each hop.
- **X2 Pricing dataflow** — trades/oracle_updates → caggs → aggregate (VWAP,
  stablecoin proxy, USD-peg combine) → API price/ohlc/history → wire.
- **X3 Explorer dataflow** — lake tables → ExplorerReader → xdrjson → handlers
  → OpenAPI → UI; index coverage vs query predicates.
- **X4 Config→wiring** — every `config` field → its consumer (no orphan/unwired
  fields; no consumer reading an unset field).
- **X5 Interface conformance** — every interface ↔ its implementations +
  stubs (e.g. HistoryReader, ExplorerReader, the reader seams) stay in sync.
- **X6 Auth/permission flow** — key mint → storage → validate → middleware →
  handler gates; the closed-posture/permission-mapping class of bugs.
- **X7 Migration↔code** — every table/column the code reads/writes exists in
  migrations (+ the lake DDL); no drift.

## 4. Plan self-check (iterate until no gaps)

Run BEFORE executing. Append a pass each time something is added.

- Pass 1: see §"Plan iteration log" below.

## 5. Execution model

Fan out read-only sub-agents (Agent tool — NOT the workflow tool; no opt-in),
one per area A01–A19 + the cross-cutting X1–X7, in concurrent batches. Each
agent: read all files in its area against D1–D9, return a structured findings
list (severity, file:line, dimension, issue, suggested fix, confidence). Then:

- **Synthesis** — collect into `02-findings-register.md` (dedup, severity-rank).
- **Adversarial verification** — for every High/Critical, a second independent
  agent attempts to REFUTE it (false-positive cull) before it's confirmed.
- **Round 2 (gap sweep)** — a completeness-critic pass: what area/seam/claim
  is unverified? Spawn targeted follow-ups until dry.

## 6. Post-audit self-verification

- Confirm every file in `find internal cmd pkg pkg web/explorer/src migrations
  -type f` maps to an executed area (coverage matrix in `03-coverage-matrix.md`).
- Confirm every High/Critical finding was adversarially verified.
- Confirm cross-cutting seams X1–X7 each have a written conclusion.
- Decide explicitly whether another pass is warranted; record the decision +
  rationale in `04-verdict.md`. Only stop when the answer is "no new areas".

## 2b. Added areas (from plan iteration)

- **A20 Test suites** — `test/integration`, `test/chaos`, `test/load`,
  `test/fixtures` (correctness of the harnesses + fixture validity + what's
  NOT covered). Also: each area A01–A19 audits its own `*_test.go`.
- **A21 Dependencies + build integrity** — `go.mod`/`go.sum`, `VERSIONS.md`
  (pinned upstream SHAs we audit), `.golangci.yml`,
  `scripts/ci/lint-imports.sh` (architectural import boundaries), Makefile
  `verify` gate, `//go:embed`'d data (currency seed.yaml, incidents, etc.).
- **A22 Licensing + secrets sweep** — Apache-2.0 headers/coverage,
  whole-tree secret scan (keys/tokens in code, configs, fixtures, the
  static UI bundle), `.gitignore` adequacy for the public flip.

## 3b. Added cross-cutting seams (from plan iteration)

- **X8 Known-traps still handled** — verify each CLAUDE.md "Things that will
  surprise you" trap is still correctly handled in code: Soroswap SwapEvent
  reserves-in-following-SyncEvent; Phoenix 8-events-per-swap grouping; Comet
  shared `("POOL",…)` topic gating; Reflector = 3 contracts + no on-chain
  twap/x_*; Band E18/E9 scaling + zero-events (ContractCall path); Redstone
  feed_id-from-op-args; CAP-67 4-topic + SEP-41 3-topic dual shape; SEP-41
  transfer i128-or-map; stablecoin proxy = aggregator-layer; external-amount
  non-uniform decimals (10^8 vs 10^6 FX); XLM `native`↔`crypto:XLM` dual-form;
  contract-schema-evolution (WASM-version-aware backfill).
- **X9 Panic-safety in request/ingest paths** — no unrecovered panics in HTTP
  handlers or the per-ledger ingest loop (one bad tx/op/row must not crash);
  unchecked `Must*()` XDR calls in hot paths.
- **X10 Determinism/region-stability** — closed-bucket responses byte-identical
  across regions (ADR-0015); no `time.Now()`/map-order nondeterminism in
  serialized output.

## Plan iteration log

- **v1 (initial)** — areas A01–A19 + seams X1–X7 + dimensions D1–D9.
- **v2** — added A20 (test suites incl. per-area `*_test.go`), A21 (deps /
  build integrity / embeds / import-boundary lint), A22 (licensing + whole-tree
  secret sweep); added X8 (known-traps still handled), X9 (panic-safety),
  X10 (determinism). Rationale: v1 omitted the `test/` tree, dependency/build
  integrity, embedded data, the public-flip licensing/secret surface, the
  CLAUDE.md trap-regression class, panic-safety, and determinism — all
  material to "every file + cross-file interaction".
- **v3 (gap re-check)** — swept for further gaps: (a) generated artifacts
  (`docs/reference`, Postman) → staleness check folded into A19/A07-tests;
  (b) dead/zero-caller seams (`internal/hashdb`, `consumer.Orchestrator`) →
  explicit dead-code verdict in A04/A01; (c) `pkg/client` SemVer wire-shape
  stability → A14; (d) Prometheus metric correctness + alert-rule↔metric
  parity → A16 + A18; (e) static-UI bundle leak check → A17/A22. No further
  uncovered areas found → plan is **complete**; proceed to execute (§5).
