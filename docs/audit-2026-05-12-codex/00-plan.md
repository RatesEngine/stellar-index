# Full Audit Plan

## Objective

Run a cold, adversarial audit of the full Rates Engine system at the
current repository snapshot, with coverage deep enough to compete with
CoinGecko and CoinMarketCap on general market-data correctness while
also exceeding them on Stellar-native assets, issuers, DEXs, Soroban
events, supply semantics, archive completeness, and operational
transparency.

The audit must verify code and runtime behavior directly. Markdown,
ADRs, RFPs, prior audits, discovery notes, generated references, and
runbooks are claims to test, not facts to trust.

## Snapshot And Scope

| Item | Value |
| --- | --- |
| Audit label | `2026-05-12-codex` |
| Plan prepared | `2026-05-11` |
| Requested audit date label | `2026-05-12` |
| Snapshot anchor | `80c57e38eeee729ec2d879d54286419206cee864` |
| Initial dirty worktree | clean before audit-plan files were created |
| Tracked files in scope | `1,747` from `git ls-files` before this directory |
| Primary file inventory | [inventory/file-coverage.tsv](inventory/file-coverage.tsv) |
| Non-tracked environment inputs | `.discovery-repos/`, ignored build output, local caches, R1 runtime |

Tracked repository files are the mandatory app audit scope. Ignored
mirrors and build outputs are not source-of-truth application files, but
they are audit inputs when they influence provenance, generated output,
operator practice, or runtime state.

## Non-Negotiable Completion Bar

The audit cannot close until all of these are true:

- every row in `inventory/file-coverage.tsv` has a terminal status:
  `done`, `excluded`, or `blocked`
- every `blocked` or `excluded` row has an entry in
  [06-exclusions-register.md](06-exclusions-register.md)
- every finding in [05-findings-register.md](05-findings-register.md)
  cites evidence IDs
- every evidence ID is backed by file references, command output, test
  behavior, generated artifacts, or R1 observations
- every major runtime journey in [03-journeys.md](03-journeys.md) is
  traced from input to output and failure mode
- every cross-file interaction class is represented in
  [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md)
- second-pass and third-pass reconciliation in
  [04-reconciliation.md](04-reconciliation.md) is complete

## Audit Mindset

Assume:

- source adapters lie by omission under malformed data
- schema migrations drift from stores and handlers
- generated docs drift from code
- CI gives false confidence through skipped suites or local-only checks
- live infra differs from deploy code
- external market-data sources fail, manipulate, throttle, or change
  schemas
- Stellar-specific semantics are easy to flatten into misleading
  generic crypto-market abstractions
- every cache, aggregation window, cursor, and fallback can create stale
  or inconsistent customer-visible data

## Workstreams

### W01. Snapshot, Repository Hygiene, And Ownership

Audit:

- exact snapshot SHA and dirty-state caveats
- tracked vs ignored material
- ownership, contribution, release, and security policy files
- stale scaffolding, duplicate audit residue, generated artifacts
- root orientation files against actual code shape

Primary files:

- `.gitignore`, `.gitleaks.toml`, `.golangci.yml`, `.spectral.yaml`
- `AGENTS.md`, `CLAUDE.md`, `README.md`, `CONTRIBUTING.md`
- `CODEOWNERS`, `SECURITY.md`, `CHANGELOG.md`, `VERSIONS.md`
- `.github/*`

Granular checks:

- confirm all policy files are referenced by workflows or docs where
  expected
- find executable scripts lacking lint/test coverage
- find generated files without generation commands
- find docs that describe nonexistent commands, ports, env vars, or
  services

### W02. Architecture, ADRs, And Negative Space

Audit:

- all ADR claims against packages, binaries, migrations, configs, and
  deploy artifacts
- architecture docs against import graph and runtime wiring
- removed or deprecated systems that still exist in code
- missing ADRs for material design choices
- negative space: expected competitive features not present in code

Primary files:

- `docs/adr/**`
- `docs/architecture/**`
- `cmd/**`
- `internal/**`

Project-specific focus:

- no Horizon dependency in runtime architecture
- S3-compatible storage posture rather than local-only assumptions
- single-module monorepo discipline
- i128/u128 and amount handling without truncation
- last-closed-bucket consistency and cross-surface API semantics

### W03. Build, Toolchain, Reproducibility, And Release

Audit:

- `Makefile` target truth and dependency assumptions
- Go module, JS package managers, lockfiles, Dockerfiles, and Cloudflare
  Wrangler configs
- generated API, Postman, config, and metrics references
- release validation, deploy, rollback, and branch protection claims
- whether local commands match CI commands

Primary files:

- `Makefile`, `go.mod`, `go.sum`
- `web/*/package.json`, `web/*/pnpm-lock.yaml`
- `docker/**`
- `.github/workflows/**`
- `scripts/ci/**`, `scripts/dev/**`, `scripts/ops/**`

### W04. Dependency, Provenance, And Supply Chain

Audit:

- Go direct and transitive dependencies
- JS dependency trees for dashboard, explorer, and status apps
- pinned upstream SHAs in `VERSIONS.md`
- Dependabot and release workflows
- gitleaks, lint, OpenAPI lint, and import-boundary controls
- `.discovery-repos/` influence on docs and fixtures

Granular checks:

- dependency update paths do not silently bypass tests
- third-party API clients have timeouts, retries, and rate-limit handling
- generated docs do not embed stale dependency claims
- external reference repos are not accidentally runtime dependencies

### W05. Configuration And Secret Boundaries

Audit:

- config schema, defaults, env parsing, validation, and examples
- service templates and runtime env files
- secret handling in logs, errors, metrics, frontend bundles, and CI
- Caddy, HAProxy, Cloudflare, trusted proxy, CORS, and header posture

Primary files:

- `internal/config/**`
- `configs/example.toml`
- `configs/**`
- `deploy/**`
- `web/**/wrangler.toml`

### W06. Canonical Identity, Asset Semantics, And Numeric Safety

Audit:

- asset IDs, slugs, pairs, issuer keys, contract IDs, currencies, and
  canonicalization
- `*big.Int`, decimal, i128/u128, JSON, SQL, Redis, and SCVal boundaries
- pair ordering, route attribution, synthetic assets, stablecoin fiat
  proxies, and late binding
- user-facing symbols vs canonical internal identifiers

Primary files:

- `internal/canonical/**`
- `internal/currency/**`
- `internal/scval/**`
- `internal/cachekeys/**`
- `internal/events/**`
- `pkg/client/types.go`

### W07. Ledger Ingest, Transport, Backfill, And Dispatch

Audit:

- ledger streaming inputs, backfill inputs, cursor persistence, replay,
  idempotency, and backpressure
- dispatcher topic matching, source registration, fall-through behavior,
  malformed payload handling, and stats flushing
- pipeline sinks and write ordering

Primary files:

- `internal/ledgerstream/**`
- `internal/dispatcher/**`
- `internal/pipeline/**`
- `internal/consumer/**`
- `cmd/ratesengine-indexer/**`
- `cmd/ratesengine-ops/backfill.go`

### W08. Stellar DEX And Soroban Source Decoders

Audit every source directory file-by-file:

- `internal/sources/soroswap`
- `internal/sources/aquarius`
- `internal/sources/phoenix`
- `internal/sources/comet`
- `internal/sources/blend`
- `internal/sources/sdex`
- `internal/sources/liquidity_pools`

For each decoder:

- event and operation claim surface
- exact topic and SCVal parsing
- asset normalization and amount precision
- pool/pair/route attribution
- fixture realism and coverage
- malformed, partial, reordered, duplicate, and upgraded contract events
- storage integration and downstream consumers

Competitive focus:

- Stellar DEX coverage depth beyond generic market-data competitors
- path attribution and router detection
- AMM vs orderbook distinction
- TVL, reserves, fees, MEV, lending, auctions, and source contribution
  visibility

### W09. Stellar Account, Supply, And Balance Observers

Audit:

- account entry observations
- trustlines, claimable balances, classic liquidity pools, SAC balances,
  SEP-41 supply events, issuer flags, freeze/clawback semantics
- classic vs Soroban token supply policy

Primary files:

- `internal/sources/accounts/**`
- `internal/sources/trustlines/**`
- `internal/sources/claimable_balances/**`
- `internal/sources/sac_balances/**`
- `internal/sources/sep41_supply/**`
- `internal/supply/**`

### W10. Oracle And Reference-Price Source Decoders

Audit:

- Reflector, Redstone, Band, Frankfurter, forex, and generic oracle
  ingest
- message authenticity assumptions
- timestamps, stale data, confidence, decimals, symbols, and pair maps
- fallback and conflict behavior against DEX and CEX observations

Primary files:

- `internal/sources/reflector/**`
- `internal/sources/redstone/**`
- `internal/sources/band/**`
- `internal/sources/frankfurter/**`
- `internal/sources/forex/**`
- `internal/storage/timescale/oracle.go`

### W11. External Market-Data Source Fleet

Audit:

- external runner, registry, source policy, polling, backfill, retry,
  rate limits, schema drift handling, and paid/free source metadata
- adapters for Binance, Bitstamp, Coinbase, CoinGecko, CoinMarketCap,
  CryptoCompare, ECB, ExchangeRatesAPI, Kraken, Polygon Forex
- how off-chain observations enter storage and aggregation

Primary files:

- `internal/sources/external/**`
- `cmd/ratesengine-indexer/main.go`
- `scripts/ops/fx-history-backfill/**`

Competitive focus:

- source breadth parity with CoinGecko/CoinMarketCap where relevant
- clearer provenance and confidence than generic aggregators
- robust degradation under source outages and throttling

### W12. Storage, Migrations, And Query Correctness

Audit:

- every migration up/down pair
- Timescale hypertables, continuous aggregates, indexes, constraints,
  retention, idempotency, and rollback
- store method SQL vs schema reality
- migration tests and integration test coverage

Primary files:

- `migrations/**`
- `internal/storage/timescale/**`
- `test/integration/**`
- `deploy/docker-compose/init/**`

### W13. Redis, Cache Keys, Streaming Pub/Sub, And Freshness

Audit:

- Redis client behavior, connection lifecycle, sentinel assumptions,
  cache key versioning, TTLs, stale reads, and cache invalidation
- streaming publish/subscribe, SSE backpressure, reconnect behavior,
  fanout correctness, and payload contracts

Primary files:

- `internal/storage/redisclient/**`
- `internal/cachekeys/**`
- `internal/api/streaming/**`
- `internal/api/streampublish/**`
- `deploy/monitoring/rules/cache.yml`

### W14. Aggregation, Baselines, Anomaly, Freeze, And Confidence

Audit:

- VWAP, TWAP, OHLC, volume, buckets, windows, and closed-bucket behavior
- anomaly detection, freeze rules, confidence scoring, divergence,
  triangulation, stablecoin fiat proxying, and fallback rules
- precision and source weighting

Primary files:

- `internal/aggregate/**`
- `internal/divergence/**`
- `cmd/ratesengine-aggregator/**`
- `internal/storage/timescale/aggregates.go`
- `internal/storage/timescale/baseline.go`

### W15. API Runtime, Middleware, Contracts, And Client SDK

Audit:

- route registration, middleware ordering, request validation, error
  envelopes, pagination, cursors, cache-control, CORS, trusted proxies,
  rate limiting, auth, response compatibility, and OpenAPI drift
- public Go client parity with API behavior

Primary files:

- `cmd/ratesengine-api/**`
- `internal/api/v1/**`
- `internal/api/v1/middleware/**`
- `internal/ratelimit/**`
- `internal/auth/**`
- `openapi/rates-engine.v1.yaml`
- `pkg/client/**`

### W16. Dashboard, Explorer, Status Page, SEO, And Embeds

Audit:

- Next routes, server/client boundaries, data fetching, auth callback,
  API base URLs, error states, cache behavior, accessibility, SEO,
  sitemap/robots, embeds, Cloudflare headers, and static build posture
- content pages that expose docs, ADRs, changelog, research, operations,
  blog, pricing, careers, contact, status incidents

Primary files:

- `web/dashboard/**`
- `web/explorer/**`
- `web/status/**`
- `docs/blog/**`
- `docs/operations/incidents/**`

### W17. Observability, Metrics, Alerts, Status, And Incident Flow

Audit:

- metric names, labels, cardinality, textfile collectors, alert rules,
  Alertmanager config, Loki/Promtail config, status-page data path,
  runbooks, postmortems, and drill history
- alert-to-runbook mappings and missing alert surfaces

Primary files:

- `internal/obs/**`
- `configs/prometheus/**`
- `configs/alertmanager/**`
- `configs/loki/**`
- `deploy/monitoring/**`
- `configs/healthchecks/**`
- `docs/operations/**`
- `web/status/**`

### W18. Operations, R1 Runtime, Archive Completeness, And DR

Audit:

- every `ratesengine-ops` subcommand
- archive completeness, verify-archive, Hubble, wasm extract/history,
  cross-region checks, SEP-1 refresh, Soroswap seeding, mint/upgrade key
  commands, supply tools
- systemd timers/services and R1 runtime reality
- R2/R3 plans and multi-region failover readiness

Primary files:

- `cmd/ratesengine-ops/**`
- `cmd/ratesengine-sla-probe/**`
- `internal/archivecompleteness/**`
- `deploy/systemd/**`
- `configs/ansible/**`
- `docs/operations/r1-deployment-state.md`
- `docs/operations/r2-deployment-state.md`
- `docs/operations/r3-deployment-state.md`

R1 checks are allowed and should be performed through the protocol in
[02-protocol.md](02-protocol.md), with command output captured as
evidence.

### W19. Security, Auth, Abuse, And Privacy

Audit:

- API key auth, dashboard auth, SEP-10 auth, JWT/session handling,
  passwordless/sign-in flows, key minting/upgrades, rate limits,
  authorization boundaries, logs, sensitive fields, CORS, headers,
  webhook/notification surfaces, and abuse economics

Primary files:

- `internal/auth/**`
- `internal/api/v1/dashboardauth/**`
- `internal/api/v1/dashboardkeys/**`
- `internal/ratelimit/**`
- `cmd/ratesengine-ops/mint_key.go`
- `cmd/ratesengine-ops/upgrade_key.go`
- `web/dashboard/src/lib/auth.tsx`
- `.gitleaks.toml`

### W20. Tests, Fixtures, Chaos, Load, And CI Reality

Audit:

- every Go test package, integration test, fixture, chaos scenario, k6
  scenario, frontend build/lint posture, skipped tests, race coverage,
  testcontainers, CI matrices, nightly/weekly jobs, and local-only gaps

Primary files:

- `*_test.go`
- `test/**`
- `.github/workflows/**`
- `Makefile`

### W21. Documentation Truth And Customer Commitments

Audit:

- RFPs, proposal, docs, reference docs, runbooks, tutorials, API docs,
  architecture docs, ADRs, and generated material against code and live
  runtime

Primary files:

- `docs/stellar-rfp.md`
- `docs/freighter-rfp.md`
- `docs/ctx-proposal.md`
- `docs/reference/**`
- `docs/discovery/**`
- `examples/**`

### W22. Competitive Product Completeness

Audit:

- breadth and freshness of asset catalogue, market catalogue, DEX/CEX
  venues, issuers, networks, lending, oracles, MEV, divergences,
  anomalies, methodology, widgets, embeds, pricing, SDK docs, and
  customer-facing credibility
- gaps where generic competitors already expose expected market data
- gaps where Rates Engine should exceed generic competitors through
  Stellar-native specificity

Primary files:

- `internal/api/v1/**`
- `web/explorer/src/app/**`
- `openapi/rates-engine.v1.yaml`
- `docs/architecture/coverage-matrix.md`
- `docs/discovery/external-refs/**`

### W23. Generated Artifacts And Drift

Audit:

- OpenAPI, Postman examples, config reference, metrics reference,
  dashboard/explorer/status static output expectations, generated docs,
  and generated lockfiles

Primary files:

- `docs/reference/**`
- `examples/**`
- `scripts/dev/docs-api.sh`
- `scripts/dev/docs-postman.sh`
- `cmd/ratesengine-ops/main.go`
- `internal/obs/**`

### W24. Cross-File Interaction And System Coupling

Audit:

- every material boundary crossing:
  binary -> config -> package -> storage -> cache -> API -> web -> docs
- all store interfaces and implementations
- migration/store/API/OpenAPI alignment
- workflow/script/artifact alignment
- alert/runbook/service alignment
- frontend/API/client type alignment

This workstream owns the cross-file ledger and must stay open until all
other workstreams have terminal evidence.

## Mandatory Audit Passes

### Pass 1. Inventory And Topology

Generate inventory, package map, route map, migration map, workflow map,
service map, frontend route map, test map, and docs map from live files.

### Pass 2. Top-Down Architecture

Start from binaries, deploy units, workflows, and public products. Trace
into packages, stores, migrations, tests, docs, and alerts.

### Pass 3. Bottom-Up File Review

Every tracked file receives a role, inbound dependency, outbound
dependency, trust boundary, test surface, docs surface, and terminal
status.

### Pass 4. End-To-End Journeys

Trace every journey in [03-journeys.md](03-journeys.md), including happy
path, degraded path, malicious input, stale data, and observability.

### Pass 5. Hostile Environment

Inject or reason through malformed payloads, external outages, rate
limits, stale Redis, missing Timescale, partial migrations, clock skew,
ledger gaps, network partitions, and region divergence.

### Pass 6. Docs-Truth And Customer-Truth

Compare docs, RFPs, proposal claims, generated references, examples,
runbooks, and status pages against code and runtime.

### Pass 7. Competitive Completeness

Compare shipped surfaces against generic crypto-market expectations and
Stellar-specific expectations. Log missing breadth, misleading depth, or
weak methodology as findings or notes.

### Pass 8. Second-Pass Reconciliation

Use [04-reconciliation.md](04-reconciliation.md) to prove the plan covers
every top-level area, package, migration, route class, frontend route
class, workflow, deploy artifact, docs family, and journey.

### Pass 9. Third-Pass Adversarial Review

Try to falsify the audit itself. Search for unowned files, ambiguous
statuses, missing cross-file seams, findings without evidence,
evidence without source anchors, and claims that came from docs alone.

## Required Deliverables

- terminal tracker in [01-tracker.md](01-tracker.md)
- completed file inventory in [inventory/file-coverage.tsv](inventory/file-coverage.tsv)
- evidence log in [evidence/log.md](evidence/log.md)
- command log in [evidence/commands.md](evidence/commands.md)
- cross-file ledger in [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md)
- completed journey traces in [03-journeys.md](03-journeys.md)
- completed competitive parity matrix in
  [08-competitive-parity-matrix.md](08-competitive-parity-matrix.md)
- completed Stellar-depth matrix in
  [09-stellar-depth-matrix.md](09-stellar-depth-matrix.md)
- completed attack-tree dispositions in
  [10-adversarial-attack-tree.md](10-adversarial-attack-tree.md)
- completed R1 live probe transcript set following
  [11-r1-live-probe-protocol.md](11-r1-live-probe-protocol.md)
- findings in [05-findings-register.md](05-findings-register.md)
- exclusions in [06-exclusions-register.md](06-exclusions-register.md)
- remediation plan in [07-remediation-plan.md](07-remediation-plan.md)
