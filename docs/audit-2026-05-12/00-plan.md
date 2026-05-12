# Audit Plan — 2026-05-12

## Objective

Execute a maximally granular, cold, adversarial audit of the current
repository snapshot **and** the live R1 deployment, with explicit
coverage of:

- every tracked file (1700+ in this snapshot)
- every material cross-file interaction
- every runtime binary (`cmd/*`) and operator path
- every claimed invariant (ADR-0001 through ADR-0026)
- every API route, response envelope, and OpenAPI declaration
- every SQL migration (up + down) and the resulting Timescale schema
- every Redis key contract, prewarmer, and cache-miss path
- every external source (CEX/FX/aggregator/oracle) adapter
- every on-chain decoder (Soroban + classic)
- every Prometheus rule, Alertmanager route, runbook, and SLA probe
- every CI workflow, lint script, and release control
- every web frontend (`web/explorer`, `web/dashboard`, `web/status`)
- every config knob, env var, and feature flag
- every code-to-doc, code-to-test, code-to-RFP, code-to-proposal contract
- every gap relative to CoinGecko / CoinMarketCap parity
- every Stellar-specific surface that CG/CMC cannot match

This audit is **cold**. Prior audits (04-29, 05-02) and CLAUDE.md
identify areas to inspect, but **no claim is accepted** without live
code, live test, or live R1 evidence in **this audit's** evidence
log. Closed prior findings are re-opened in scope and re-tested.

This audit is **adversarial**. The frame is: "if someone wanted to
break this system, mis-trust this data, exfiltrate keys, exhaust the
budget, or embarrass the brand on launch day, how would they do it?"
See [10-attack-tree.md](10-attack-tree.md).

## Why now

- The system is **deployed live to R1** with real services running
  but no public consumer traffic yet. This is the last cheap window
  to find correctness, security, and contract bugs before launch.
- The product surface is in flux: `/v1/coins` and `/v1/currencies`
  were just removed in rc.48 (commit `28ac6ac9`), live access logs
  still show traffic to those paths. Renames and route deletions
  are a common audit-finding surface.
- The competition baseline is CoinGecko + CoinMarketCap. Parity is
  not optional. Where parity is impossible (e.g. cross-chain
  on-chain trades for non-Stellar chains), the gap must be either
  closed via fallback (federated metadata) or *documented* as a
  product positioning choice — never silently absent.
- The Stellar-specific edge (DEX/AMM trades, oracle feeds, supply
  derivation, SEP-1 overlays, SEP-40 compatibility, classic +
  Soroban unification) must be deeper than CG/CMC's, since that is
  the only durable differentiator.

## Workstream Catalogue

Each workstream has its own sub-plan under [workstreams/](workstreams/).

### W01 — Snapshot, governance, repo hygiene

Audit unit: the repo as a managed artifact.

- exact commit SHA + dirty-worktree caveats
- `.gitignore` truth — what's ignored vs what should be
- `.gitleaks.toml` rules vs actual secrets in tracked files
- root docs: `README.md`, `CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CODEOWNERS`, `VERSIONS.md`,
  `LICENSE`, `CHANGELOG.md`, `commitlint.config.js`
- `.github/` content: workflows, dependabot, issue/PR templates,
  release notes template
- discovery repo checkouts under `.discovery-repos/` — purpose,
  freshness, whether anything in product code reads them
- residue: prebuilt binaries at repo root (`ratesengine-api`,
  `ratesengine-aggregator`, `ratesengine-indexer`), `.DS_Store`,
  `.wrangler/`, audit residue
- `commitlint.config.js` rules vs `git log` reality

### W02 — Architecture, ADRs, negative space

Audit unit: stated invariants vs implementation.

- every ADR in `docs/adr/` (0001..0026): re-prove each invariant
  is honoured by current code or mark it stale
- architecture docs in `docs/architecture/` — for each, verify
  the described system matches `internal/` reality
- package graph (`go list -deps ./...`) vs the import-boundary
  lint rules in `scripts/ci/lint-imports.sh` and its baseline
- new packages since the prior audit
  (`internal/aggregate/anomaly`, `internal/aggregate/baseline`,
  `internal/aggregate/changesummary`, `internal/aggregate/confidence`,
  `internal/aggregate/freeze`, `internal/aggregate/orchestrator`,
  `internal/api/streampublish`, `internal/canonical/discovery`,
  `internal/currency/data`, `internal/dispatcher/statsflush`,
  `internal/incidents`, `internal/notify`,
  `internal/platform/postgresstore`, `internal/sources/forex`,
  `internal/sources/frankfurter`, `internal/usage`,
  `internal/auth/sep10`) — verify wired, exercised, documented
- legacy / dead packages
  (`internal/consumer`, `internal/stellarrpc`) — wired anywhere
  that matters?
- ADR drift: `docs/adr/0012-*` is missing from the directory —
  is the number deliberately skipped or was an ADR deleted?
- the gap between "documented architecture" and what `go list`
  + dispatcher wiring actually produces

### W03 — Build, reproducibility, CI/CD, release controls

Audit unit: from source to deployable artifact.

- `Makefile`: each target traced; reproducibility under clean clone
- `scripts/dev/verify.sh` vs `.github/workflows/ci.yml` parity
- every workflow under `.github/workflows/`:
  `ci.yml`, `api-audit.yml`, `api-docs.yml`, `deploy.yml`,
  `docs-deploy.yml`, `explorer-deploy.yml`, `k6-weekly.yml`,
  `release-validate.yml`, `release.yml`, `status-page.yml`
- triggers (push vs PR vs tag vs cron), cost model
- secrets used vs documented secret list
- container build path (`docker/*.Dockerfile`) — minimal-image
  validation, reproducibility, multi-arch, SBOM
- `scripts/dev/cut-release.sh` + `scripts/ci/verify-launch-ready`
- `release.yml` artifact integrity (SHA256SUMS, signing, GHCR push)
- `deploy.yml` rollback semantics — atomic install + automatic
  rollback path
- `scripts/dev/wait-pr-checks.sh` correctness
- `commitlint` and conventional-commit enforcement
- branch protection on `main` (probe GitHub API or operator
  testimony)
- gofumpt / golangci-lint / gitleaks / govulncheck versions vs
  most recent upstream advisories

### W04 — Dependency, provenance, supply chain

Audit unit: trust boundary of every third-party byte.

- every direct dependency in `go.mod`: licence, maintenance
  status, last release date, known CVEs
- transitive surface: `go list -m all`
- pinned SHAs in `VERSIONS.md` — re-verify each line is real
- `go.sum` integrity vs `go mod verify`
- `govulncheck` actually runs in CI? (vs only existing as a
  Makefile target)
- `gitleaks` actually runs in CI? (rule coverage in
  `.gitleaks.toml`)
- `.discovery-repos/` checkouts: are they ever imported into
  product code? If yes, fail. If no, document the
  read-only-reference status.
- `web/explorer`, `web/dashboard`, `web/status` Node deps:
  pnpm lockfile audits, advisories
- Dockerfile base-image pins (`golang:1.x-alpine`, `gcr.io/distroless/static`)
- GitHub Actions deps: `actions/checkout@v6`, `setup-go@v6`,
  `golangci-lint-action@v9`, etc. — pinned by major or by SHA?

### W05 — Canonical identity, numeric safety, serialization

Audit unit: how value flows from XDR/wire to API JSON without loss
or impersonation.

- every type in `internal/canonical/`: `Asset`, `AssetCrypto`,
  `AssetFiat`, `Amount`, `Pair`, `Trade`, `Oracle`, `Strkey`
- ADR-0003 (i128/u128 never truncates to int64): for every
  `xdr.Int128Parts` parse site, prove no `int64(x.Lo)` happens
- ADR-0014 (crypto-ticker representation): test the parse +
  format paths
- SCVal helpers in `internal/scval/`
- canonical asset slug + `asset_id` boundary: where does
  `native` map, where does `code-issuer` map, where does
  `C…` (SEP-41 contract id) map
- JSON boundary: amounts as strings (no IEEE 754); test for
  any handler that emits `*big.Int` directly
- cache key construction in `internal/cachekeys/` — round-trip
  stability under quoting / casing / order
- discovery package `internal/canonical/discovery` — what does
  it discover, where does it write, what's its trust boundary

### W06 — Ingest transport, dispatcher, persistence pipeline

Audit unit: from a raw LedgerCloseMeta blob to a hypertable row.

- `internal/ledgerstream/`: live and archive readers, backpressure,
  checkpoint semantics
- `internal/dispatcher/`: routing logic, contract-event vs
  ledger-entry-change vs op-decoder vs contract-call dispatch,
  fall-through and error policy
- `internal/dispatcher/statsflush/`: decoder stats emission, what
  hypertable it writes, drift vs `migrations/0020_*`
- `internal/pipeline/`: dispatcher glue, sink, processor,
  datastore, soroswap_registry
- `internal/hashdb/`: ledger_seq → sha256(LCM) record, drift
  detector, dual-archive completeness invariants (ADR-0017)
- `internal/archivecompleteness/`: tier A/B/C/D verifier flow,
  what counts as "verified", what counts as "stale"
- ADR-0001 invariant (no Horizon), ADR-0002 (S3-compat storage)
- removal of stellar-rpc from production ingest path
  (lint-imports.sh enforcement + actual absence)
- `internal/stellarrpc/` residue: is it still used by rpc-probe
  and fixture capture only?
- `cmd/ratesengine-indexer/main.go` wiring of every decoder +
  every observer
- pipeline error paths: malformed XDR, decode panic, sink write
  failure, cursor lag, dispatcher fan-out

### W07 — On-chain source decoders + auxiliary ledger readers

Audit unit: every Soroban/classic decoder. For each, the same
seven-check loop (see [02-protocol.md](02-protocol.md) §5).

- `internal/sources/soroswap/` — full files
- `internal/sources/aquarius/`
- `internal/sources/phoenix/` — 8-event grouping invariant
- `internal/sources/comet/` — shared `("POOL", *)` topic surface
- `internal/sources/blend/` — auction storage + decoder
- `internal/sources/reflector/` — DEX/CEX/FX three-contract split
- `internal/sources/redstone/` — RedStone Adapter, `WritePrices`
  + feed_id absence; op-args plumbing through `events.OpArgs`
- `internal/sources/band/` — emits zero events; ContractCallDecoder
  observation of `relay()` / `force_relay()`
- `internal/sources/sdex/` — classic operations + effects
- `internal/sources/accounts/` (ADR-0021 account-entry observer)
- `internal/sources/trustlines/`
- `internal/sources/claimable_balances/`
- `internal/sources/sac_balances/` (SAC wrappers)
- `internal/sources/sep41_supply/` (ADR-0023)
- `internal/sources/liquidity_pools/`

For each:
- claim surface and field schema
- decode entry function(s)
- malformed-input handling (no panics on hostile XDR)
- storage / consumer integration
- fixture realism (golden files exist? captured from real ledgers?)
- tests vs actual risk (what's asserted, what isn't)
- WASM audit status in `docs/operations/wasm-audits/<source>.md`
  and `BackfillSafe` flag in `internal/sources/external/registry.go`
- contract-id allowlist vs topic-based dispatch (ADR-0001-adjacent)

### W08 — External source fleet + policy

Audit unit: every non-Stellar venue feeding our aggregator.

- `internal/sources/external/binance/` — REST + WS streamer +
  backfill + parsing
- `internal/sources/external/bitstamp/`
- `internal/sources/external/coinbase/`
- `internal/sources/external/kraken/`
- `internal/sources/external/cryptocompare/`
- `internal/sources/external/coingecko/` — poller, backoff
- `internal/sources/external/coinmarketcap/`
- `internal/sources/external/ecb/`
- `internal/sources/external/exchangeratesapi/`
- `internal/sources/external/polygonforex/`
- `internal/sources/frankfurter/` (note: lives outside
  `internal/sources/external/`; document why)
- `internal/sources/forex/` (circulation_data.csv, worker, cache)
- `internal/sources/external/registry.go` — class/subclass,
  paid/free, BackfillSafe flags

For each:
- vendor API truth (endpoints exist, rate limits documented)
- auth handling (no keys in code; env-only)
- normalisation (uniform 10^8 amount scale per CLAUDE.md)
- retry, backoff, clock-skew tolerance
- registry class (exchange / aggregator / oracle / authority_sanity)
- divergence-only vs aggregator-feeding inclusion policy
- ToS / redistribution licence
- failure modes: vendor outage, rate-limit, API change

### W09 — Storage, schema, cache, migrations

Audit unit: TimescaleDB schema + Redis key contracts.

- `migrations/0001..0028`: read each `.up.sql` and `.down.sql`
  in order; prove every up has a matching down; verify
  hypertable + continuous-aggregate semantics
- `migrations/README.md` truth
- `internal/storage/timescale/`: every file is a reader/writer;
  re-verify the SQL inside vs the migration target tables
- ADR-0006 (Timescale) invariants
- ADR-0007 (Redis cache schema): `internal/cachekeys/` is the
  only key builder; every reader/writer uses it
- ADR-0024 (Redis HA via Sentinel) — runtime wiring vs ADR claim
- `internal/storage/redisclient/`: connection pool, retry,
  Sentinel awareness, ACL
- per-table NUMERIC vs BIGINT use (i128 invariant)
- cache windows: last-closed bucket (ADR-0015), prewarm
  symmetry, drift between prewarm and handler args
  (the historical `feedback_prewarm_handler_drift` lesson)
- `migrations/0027_platform_v1_schema` is 19k bytes — single
  largest migration, demands per-statement audit
- typed not-found errors vs nil + nil-error returns
- `internal/storage/timescale/usd_volume_quote_spec.go` —
  spec correctness vs SQL queries

### W10 — Aggregation, divergence, freeze, confidence, anomaly

Audit unit: from raw trades to served price + confidence.

- `internal/aggregate/`: `vwap.go`, `twap.go`, `ohlc.go`,
  `outliers.go`, `triangulate.go`, `stablecoin.go`, `global.go`
- `internal/aggregate/anomaly/` — surface and triggers
- `internal/aggregate/baseline/` — volatility baseline windows
  (multi-window per migrations 0007/0008)
- `internal/aggregate/changesummary/` — % change derivation
- `internal/aggregate/confidence/` (ADR-0019)
- `internal/aggregate/freeze/` (freeze events table 0018)
- `internal/aggregate/orchestrator/` — wiring of the above
- `internal/divergence/`: CoinGecko + Chainlink references,
  worker, compare, config-driven enabling
- ADR-0026 (stablecoin late binding) vs implementation
- triangulation paths (e.g. `XLM -> USDT -> USD`) — provenance
  markers, transitive divergence
- `cmd/ratesengine-aggregator/main.go` wiring + scheduler
- aggregator class policy: exchange contributes by default;
  aggregators + oracles excluded; FX snap fallback rules
- VWAP min-trade-count gating (fiat had min=1; investigate why)

### W11 — API runtime, contracts, streaming, auth

Audit unit: every HTTP route + every WS/SSE stream.

- `internal/api/v1/`: every handler file (≈60 files), every
  registered route, middleware chain, auth gates, rate-limit
  identity, request-id propagation
- middleware: `internal/api/v1/middleware/` — order matters
- `internal/api/streaming/`: SSE hub, ring buffer, redis-pub
- `internal/api/streampublish/`: aggregator-side stream publish
- `internal/auth/`: apikey (Postgres + Redis stores), SEP-10
  challenge/JWT, signup tracker
- `cmd/ratesengine-api/main.go` wiring (CORS, trusted proxies,
  rate limit, divergence references)
- OpenAPI: `openapi/rates-engine.v1.yaml` (165k bytes, single
  file) — schema drift vs handlers, error envelope
  consistency, deprecated/sunset response headers
- response envelopes (`internal/api/v1/envelope.go`): `data`,
  `as_of`, `flags`, `pagination` — every handler conforms
- error envelopes: RFC 7807 type/title/status/detail/instance/request_id
- per-route: 404 latency budget; the live R1 log shows ~300ms
  on `/v1/price` 404s — investigate root cause
- removed routes (`/v1/coins`, `/v1/currencies`) — verify
  every handler is gone; verify no client-side dead links;
  verify deprecation headers / redirects / 410 responses
- caching headers, ETag, Vary, max-age vs Cloudflare config
- ratelimit: `internal/ratelimit/` Redis token bucket; identity
  derivation; per-key vs per-IP; exemption for `127.0.0.1`
- dashboardauth + dashboardkeys — admin surface security
- SSE backpressure + slow-consumer disconnect

### W12 — Supply, metadata, asset detail enrichment

Audit unit: every field in `/v1/assets/{id}` and `/v1/assets/verified`.

- `internal/supply/`: xlm (algorithm 1), classic (algorithm 2),
  sep41 (algorithm 3), reader/writer split, textfile output,
  cross-check, refresher, policy, overlay
- `internal/currency/`: hand-curated verified-currency catalogue,
  YAML seed at `internal/currency/data/seed.yaml`, `//go:embed`
  binding, propagation into:
  - CG poller ticker map
  - indexer aggregator pair set
  - unverified-collision warning on `/v1/assets/{id}`
  - `/v1/assets/verified` listing
  - explorer verified-badge UI
- `internal/metadata/`: SEP-1 / stellar.toml resolution,
  caching, failure modes
- `internal/api/v1/assets*.go` set (full coin-equivalence after
  rc.47): `assets.go`, `assets_coin_extension.go`, `assets_f2.go`,
  `assets_global.go`, `assets_sep1.go`, `assets_verified.go`
- `R-018` verified-currency surface vs catalogue
- ADR-0021/0022/0023 supply observer wiring vs `cmd/ratesengine-indexer/main.go`
- `cmd/ratesengine-ops/supply.go` snapshot CLI — current help/
  error text vs supply pipeline reality (this was a prior
  finding — re-verify cold)
- `cmd/ratesengine-ops/sep1_refresh.go`
- ATH / sparkline / top_markets / fiat market cap chart paths
  (rc.43..rc.46 features) — back to migrations 0023+0024

### W13 — Operator tooling, archive completeness, DR

Audit unit: every ops binary subcommand + every operator runbook.

- `cmd/ratesengine-ops/`: backfill, cross_region_check,
  cross_region_monitor, discovery, hubble_check,
  hubble_soroban_events, mint_key, seed_soroswap_pairs,
  sep1_refresh, supply, upgrade_key, verify_archive_chunks,
  wasm_extract, wasm_history
- `cmd/ratesengine-sla-probe/`: probe config, output schema,
  textfile-collector contract
- `cmd/ratesengine-migrate/`: migration runner truth (does
  it apply all 28 migrations in order?)
- `internal/archivecompleteness/`: tier A/B/C/D verifier
- `configs/audit/wasm-walk-contracts.yaml`
- `docs/operations/wasm-audits/`: 8 source files + decoder
  WASM matrix + protocol epochs
- `scripts/ops/`: cf-pages-bootstrap, circulation-fetch,
  fx-history-backfill, pre-launch-check,
  recompute-usd-volume-soroban.sql
- runbook completeness: every alert in `deploy/monitoring/rules/`
  must have a matching runbook at `docs/operations/runbooks/<alert-name>.md`

### W14 — Observability, metrics, alerts, SLA

Audit unit: every metric, every rule, every dashboard, every alert.

- `internal/obs/`: log, metrics, http_middleware, middleware_unit
- structured-log fields used vs Loki Grafana queries (need to
  be inferred — operator probe required)
- metric names emitted by binary vs `deploy/monitoring/rules/*.yml`
  Prometheus rules referencing them
- alert rule files (per area): aggregator, anomaly, api,
  archive-completeness, cache, divergence, external-pollers,
  infra, ingestion, meta, sla-probe, slo, stellar, storage,
  supply, supply-refresh, supply-snapshot, verify-archive
- `configs/alertmanager/alertmanager.r1.yml` route tree:
  severity-based routing (page / ticket / informational +
  deadmansswitch heartbeat)
- `configs/prometheus/prometheus.r1.yml` scrape config
- `configs/healthchecks/`: heartbeat, smoke, SLA probe systemd
  units + Healthchecks.io endpoints
- `docs/operations/alerts-catalog.md` accuracy
- `docs/operations/runbooks/` — every runbook must (a) exist
  for an active alert, (b) match the alert's expression, and
  (c) cite the correct dashboard
- SLA probe path: `cmd/ratesengine-sla-probe` -> textfile ->
  Prometheus node_exporter textfile_collector -> rules.r1
- `scripts/dev/r1-smoke.sh` and `configs/healthchecks/smoke.sh`
  divergence (live R1 reports `/opt/ratesengine/scripts/dev/r1-smoke.sh`
  missing — investigate)

### W15 — Tests, CI reality, regression confidence

Audit unit: what is actually proven vs what we claim is proven.

- repo-wide `go test ./...` reality (race detector, timeout)
- `test/integration/` 19 files — each requires Docker / testcontainers
- `test/chaos/` — scenarios + reports + design note
- `test/load/` — k6 scenarios, weekly cron, reports
- `test/fixtures/` — aquarius, phoenix, reflector, soroswap
- per-package test density vs risk; identify packages where
  the only test is happy-path
- CI matrix coverage: is `go test -race -coverprofile` actually
  the gating job? Are integration tests in CI or local-only?
- k6 weekly: cron schedule, perf budgets vs measured perf
- linter parity between local and CI (gofumpt, goimports
  versions; ensure caching is correct)

### W16 — Documentation truth, RFP/proposal/ADR alignment

Audit unit: every doc claim re-tested against code or runtime.

- `docs/stellar-rfp.md` line items vs current implementation
- `docs/freighter-rfp.md` line items
- `docs/ctx-proposal.md` line items, especially F1..F12 Freighter
- `docs/discovery/proposal-corrections.md` — what's been
  corrected, what's still wrong
- `docs/discovery/rfp-requirements-matrix.md` per-row truth
- `docs/discovery/engineering-standards.md` compliance audit
- `docs/architecture/coverage-matrix.md`
- `docs/architecture/aggregation-plan.md`
- `docs/architecture/ingest-pipeline.md`
- `docs/architecture/oracle-manipulation-defense.md`
- `docs/architecture/multi-network-assets-migration.md`
- `docs/architecture/coins-to-assets-migration.md`
- `docs/architecture/contract-schema-evolution.md`
- `docs/architecture/launch-readiness-backlog.md`
- `docs/operations/r1-deployment-state.md` vs live R1
- `docs/operations/r2-deployment-state.md`, `r3-deployment-state.md`
- `docs/operations/launch-day-checklist.md`
- `docs/operations/pre-launch-hardening.md`
- `docs/operations/release-process.md`
- `docs/operations/public-flip.md`
- `docs/operations/customer-demo-script.md`
- `docs/reference/api/`, `docs/reference/config/`, `docs/reference/metrics/`
- `docs/blog/` — published claims vs reality
- `docs/launch-task-list.md`, `docs/getting-started.md`
- `CHANGELOG.md` — every entry's PR claim must be accurate
- ADRs 0001..0026 — each invariant re-verified or marked stale

### W17 — Web frontends (explorer, dashboard, status)

Audit unit: every page, every API call, every static export.

- `web/explorer/` — Next.js 15 static export
  - every page in `src/app/`: rendered output vs API contract
  - API client wrapper(s): retry, error handling, request-id
  - dead links to removed `/v1/coins/*`, `/v1/currencies/*`
  - SEO: sitemap, robots, meta tags, canonical URLs
  - verified-badge UI vs catalogue
  - sparkline / chart components vs API shape
  - build config: `next.config.mjs`, `wrangler.toml`,
    Cloudflare Pages headers + `_redirects`
- `web/dashboard/` — admin SPA
  - auth: dashboardauth + dashboardkeys
  - what surfaces it touches (usage, billing, signup)
  - build + deployment path
- `web/status/` — public status page
  - data source, incidents linkage to `docs/operations/incidents/`
  - what backs each "system OK / degraded" indicator
- pnpm lockfile audits, supply chain
- accessibility, mobile-first
- Cloudflare Pages deploy: explorer-deploy.yml, status-page.yml,
  docs-deploy.yml workflows; cache rules; preview env handling

### W18 — Deployment, infrastructure, ansible roles

Audit unit: every artifact that runs in production.

- `deploy/systemd/`: 11 unit files (3 services + 8 timers/services)
- `deploy/docker-compose/dev.yaml`: local dev stack
- `deploy/docker-compose/init/`: bootstrap scripts
- `deploy/monitoring/`: rules + README
- `deploy/comms/`: incident, launch, maintenance, onboarding,
  rollback templates
- `deploy/status-page/`: cstate scaffold
- `docker/*.Dockerfile`: 6 files — base image, security, CMD,
  USER, HEALTHCHECK, multi-arch
- `configs/ansible/playbooks/`: archival-node, deploy-binary,
  monitoring
- `configs/ansible/roles/`: archival-node, haproxy, loki,
  patroni, prometheus, redis-sentinel
- `configs/ansible/tasks/deploy-one-binary.yml` semantics
- `configs/ansible/requirements.yml`
- `configs/caddy/`: TLS termination, header policy, trusted
  proxy headers (Cloudflare → Caddy → Caddy → Go)
- `configs/loki/`: log aggregation rules + retention
- `configs/prometheus/`: scrape config, rules.r1, retention,
  WAL, alertmanager wiring
- `configs/alertmanager/`: routing tree, receivers, secrets
- `configs/healthchecks/`: heartbeat, smoke, sla-probe
- `examples/curl/` and `examples/postman/` shipping examples
- live R1 reality vs the above: SSH probe of running services,
  systemd unit drift, ansible inventory accuracy

### W19 — Security, secrets, auth, billing

Audit unit: every authentication, authorization, and money path.

- `internal/auth/apikey*`: postgres + redis stores, list_keys
- `internal/auth/sep10/`: challenge, verify, JWT, expiry
- `internal/auth/store*`: update semantics
- `internal/auth/subject*`: identity derivation
- `internal/auth/validators*`: input validation
- `internal/platform/`: account, apikey, audit, billing,
  errors, token, usage, user, webhook
- `internal/platform/postgresstore/`: persistence path
- `internal/usage/counter.go` — usage metering correctness
- `internal/notify/`: notify, resend, sender, templates,
  noop — webhook + email path
- billing model: cost-per-request semantics, anti-abuse
- secret management: `.gitleaks.toml` rules vs every file in
  the tree; env-var truth via `internal/config/`; secret
  rotation procedure documented anywhere?
- CORS policy in `cmd/ratesengine-api/main.go`
- trusted-proxy list (Caddy + Cloudflare IPs)
- request signing or HMAC? (probably not — verify gap)
- dashboardauth admin gate
- rate limit identity: API key > IP > anonymous
- DoS surface: `/v1/markets` (was scanning 41M trades pre-rc.45);
  `/v1/assets/{id}` SEP-1 fetch DoS via slow upstream
- `cmd/ratesengine-ops/mint_key.go`, `upgrade_key.go` — key
  lifecycle audit
- audit log integrity (`internal/platform/audit.go`)
- TLS termination: Caddy + Let's Encrypt — cert auto-renew
  observability + alerting on expiry
- gitleaks coverage: re-scan tracked files; verify zero leaks

### W20 — CG/CMC parity (execution against the matrix)

Audit unit: feature-by-feature CoinGecko + CoinMarketCap parity
checklist in [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md).

Each row is one feature; mark "covered / partial / gap /
deliberate-non-goal" with evidence ref.

### W21 — R1 live state vs claimed state

Audit unit: live deployment behavior.

- service health: ratesengine-api, ratesengine-aggregator,
  ratesengine-indexer
- timers: smoke, heartbeat, SLA probe, verify-archive,
  supply-snapshot, archive-completeness
- listening ports inventory vs Caddy routing vs firewall
- disk usage: /var/lib/postgresql (1.5T allocated, 40G used),
  /var/lib/minio (6.4T / 4.9T used = 78% — investigate),
  /var/lib/galexie (1.5T / 7G used)
- memory pressure: 179G/188G used, swap full — root cause?
- top CPU consumers
- live access log analysis (rate of removed-route hits,
  latency p50/p95/p99, 4xx/5xx rate)
- Caddy → API hop measurement
- Postgres + Redis + MinIO health; replication if any
- stellar-core process still running on r1 (port 11726) —
  CLAUDE.md says removed 2026-04-23 — reconcile
- Galexie process still running (port 6061)
- Prometheus 2.45.3 + node_exporter + Loki + promtail
- TLS cert expiry (`acme-v02.api.letsencrypt.org`)
- live probe protocol: [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md)

### W22 — Launch readiness, public-flip

Audit unit: everything that must be true before the public flip.

- `docs/operations/public-flip.md` step-by-step verified
- `docs/operations/launch-day-checklist.md`
- `docs/operations/pre-launch-hardening.md`
- `docs/architecture/launch-readiness-backlog.md`
- `docs/launch-task-list.md`
- public-flip strategy memory: NEW repo at v1.0; private repo
  not force-pushed — verify the migration playbook exists
- branding consistency (ratesengine.net DNS, domains, README)
- legal: LICENSE, third-party ToS for paid feeds (CMC, etc.)
- privacy / GDPR posture for analytics
- billing surface — Stripe? ASG Card? something else?
- onboarding email + maintenance templates in `deploy/comms/`
- customer-demo-script truth (everything in it must work)
- SEO: explorer canonicals, OpenGraph, sitemap on
  ratesengine.net

### W23 — Multi-region determinism (R2, R3) vs ADR-0015/0016

Audit unit: cross-region invariants.

- ADR-0015 (last-closed-bucket-rate-serving) honoured by all
  regions? Live R1 only — verify the *contract* would hold
  in R2/R3.
- ADR-0016 (per-region storage strategy) — re-read claims for
  R2 (AWS direct from aws-public-blockchain S3 with no local
  mirror) and R3 (Vultr hybrid). Documented vs reality.
- `docs/operations/r2-deployment-state.md`, `r3-deployment-state.md`
  — content vs the lack of R2/R3 deployments today
- `cmd/ratesengine-ops/cross_region_check.go` +
  `cross_region_monitor.go` — semantics; what does "drift"
  mean here; how is it surfaced
- `scripts/dev/verify-cross-region.sh`
- `docs/operations/multi-region-cutover.md`
- `docs/architecture/ha-plan.md`, `infrastructure/multi-region-topology.md`

### W25 — Generated artifacts + drift

Every artifact produced by a script or build step rather than
hand-edited. Drift between generator and checked-in output is its
own failure mode.

- `openapi/rates-engine.v1.yaml` regeneration drift
- `docs/reference/{api,config,metrics}/` regeneration drift
- `examples/postman/*.json` regeneration drift
- `examples/curl/*.sh` vs current routes (we already filed F-1202
  under W11; W25 owns the systemic question)
- `pkg/client/{types,endpoints}.go` drift vs OpenAPI
- `web/*/out/` static-export shape vs API contract
- `web/*/pnpm-lock.yaml` and `go.sum` integrity
- `inventory/*` (this audit's own generated files)

Sub-plan: [workstreams/W25-generated-artifacts-and-drift.md](workstreams/W25-generated-artifacts-and-drift.md).

### W26 — Cross-file interactions and system coupling (audit-blocking gate)

W26 is the cross-file workstream gate. It owns
`evidence/cross-file-interactions.md` and the canonical
interaction-class taxonomy. **W26 cannot close** until every
other workstream W01..W25 has terminal status and every required
class in the taxonomy has at least one fully-traced
`XFI-####` row.

- 14 required interaction classes (binary→config→pkg, decoder→sink→store,
  workflow→script→artifact, alert→runbook→service, etc.)
- closure rule: any other workstream marked `done` while W26
  has unmapped classes triggers an automatic R14 finding

Sub-plan: [workstreams/W26-cross-file-interactions.md](workstreams/W26-cross-file-interactions.md).

### W24 — Contract schema evolution + WASM history

Audit unit: every Soroban contract upgrade risk.

- `docs/architecture/contract-schema-evolution.md` vs reality
- `docs/operations/wasm-audits/`: aquarius, band, blend, comet,
  phoenix, redstone, reflector, soroswap — per-source per-WASM
  audit log
- `docs/operations/wasm-audits/decoder-wasm-matrix.md` truth
- `docs/operations/wasm-audits/protocol-epochs.md` truth
- `migrations/0017_create_wasm_history.up.sql` schema
- `cmd/ratesengine-ops/wasm_extract.go` +
  `cmd/ratesengine-ops/wasm_history.go` — fetches; uses
  Stellar SDK; trust boundary
- `internal/sources/external/registry.go` `BackfillSafe` flag
  per source vs WASM audit evidence
- decoder-level handling of WASM version dispatch (map-field
  lookup not position; topic[0] symbol not contract address)

## Mandatory Passes

- **Top-down architecture pass.** Re-derive the system from
  `cmd/*` entry points; map every reachable function call.
- **Bottom-up file pass.** Every tracked file gets a terminal
  status in `inventory/file-coverage.tsv`.
- **End-to-end user journey pass.** Execute J01..J24 from
  [03-journeys.md](03-journeys.md). Record traces in
  [journeys-traces/](journeys-traces/).
- **End-to-end operator journey pass.** Execute the operator
  subset of journeys (J11..J20 in the same file).
- **Hostile-environment pass.** Execute the adversarial
  vectors in [10-attack-tree.md](10-attack-tree.md).
- **Live-runtime pass.** Execute the R1 probe protocol from
  [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md).
- **Documentation-truth pass.** Reconcile docs against code
  per [04-reconciliation.md](04-reconciliation.md) R01.
- **Negative-space pass.** Identify what we *don't* test, don't
  alert on, don't monitor, and don't document.
- **CG/CMC parity pass.** Execute the matrix in
  [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md).
- **Stellar-depth pass.** Execute the matrix in
  [09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md).
- **Prior-audit delta pass.** Re-test every closed finding
  in 04-29 and 05-02 cold; record whether the closure is real.
- **Recent-change pass.** Re-audit the rc.42..rc.48 commits
  individually; deleted routes, migrated explorer consumers,
  schema additions.

## Required Deliverables

- complete tracker state ([01-tracker.md](01-tracker.md))
- complete file inventory with terminal statuses
  ([inventory/file-coverage.tsv](inventory/file-coverage.tsv))
- per-workstream sub-plans completed
  ([workstreams/](workstreams/))
- evidence log ([evidence/log.md](evidence/log.md))
- cross-file interaction ledger
  ([evidence/cross-file-interactions.md](evidence/cross-file-interactions.md))
- R1 probe transcripts ([evidence/r1-probes/](evidence/r1-probes/))
- journey traces ([journeys-traces/](journeys-traces/))
- findings register ([05-findings-register.md](05-findings-register.md))
- exclusions register ([06-exclusions-register.md](06-exclusions-register.md))
- remediation plan tied to findings
  ([07-remediation-plan.md](07-remediation-plan.md))
- CG/CMC parity matrix completed
  ([08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md))
- Stellar coverage matrix completed
  ([09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md))
- attack-tree outcomes recorded
  ([10-attack-tree.md](10-attack-tree.md))

## Closure Criteria

The audit is complete only when:

- every workstream W01..W26 has terminal status in the tracker
- every mandatory pass is terminal
- `inventory/file-coverage.tsv` has no `todo`
- every finding has evidence references and a disposition
- every exclusion is explicit and re-entry-evidence-listed
- the remediation plan maps to every open finding
- the CG/CMC parity matrix is fully filled (no blank rows)
- the Stellar coverage matrix is fully filled
- at least one full R1 probe transcript is in
  `evidence/r1-probes/`
