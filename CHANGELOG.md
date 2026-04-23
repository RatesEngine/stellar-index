# Changelog

All notable changes to Rates Engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to dual versioning — SemVer for `pkg/*`
and CalVer (`YYYY.MM.DD`) for binary releases. See
[docs/discovery/repo-structure-plan.md §10](docs/discovery/repo-structure-plan.md)
for the rationale.

Every release lists the Stellar protocol version it was tested
against.

---

## [Unreleased]

### Added

- Repository foundation: `LICENSE` (Apache-2.0), `README.md`,
  `CLAUDE.md`, `CHANGELOG.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CODEOWNERS`.
- ADRs 0001–0007 + 0010: Horizon deprecated, MinIO S3-compat,
  i128 no-truncation, Tier-1 validator aspiration, monorepo,
  TimescaleDB for price time-series, Redis cache schema, and
  off-chain fiat representation.
- Root-level `VERSIONS.md` — pinned SHAs of all audited
  upstream deps.
- Makefile targets `dev`, `dev-teardown`, `dev-seed`, `lint`,
  `test`, `test-integration`, `build`, `docs-all`, `verify`.
- `.golangci.yml` strict lint config per
  [engineering-standards.md §8](docs/discovery/engineering-standards.md).
- GitHub Actions `ci.yml`, PR template, CODEOWNERS,
  `dependabot.yml`.
- Phase-1 discovery artefacts under `docs/discovery/`, closure
  doc at `docs/discovery/phase1-closure.md`, RFP × proposal ×
  delivery coverage matrix at `docs/architecture/coverage-matrix.md`.
- HA + multi-region design: `docs/architecture/ha-plan.md`,
  `docs/architecture/infrastructure/{archival-node-spec,
  multi-region-topology, validator-rollout, hosting-options}.md`.
- API design: `docs/reference/api-design.md` + OpenAPI spec at
  `openapi/rates-engine.v1.yaml` (shared error responses,
  pagination, asset / price / history / OHLC / VWAP / TWAP /
  markets / oracle schemas — source of truth for the wire
  contract).
- Repo hygiene + tech-debt prevention plan at
  `docs/architecture/repo-hygiene-plan.md`.
- `internal/canonical/`: `Amount` (i128-safe big.Int wrapper with
  JSON-as-string, SQL Scanner/Valuer, KALIEN regression test,
  `MaxAmountStringLen` DoS cap), `Asset` (tagged union —
  native/classic/soroban/fiat), `Pair` (directional base/quote
  with Flip / EqualEitherWay helpers), `Trade` (stable ID via
  source/ledger/tx_hash/op_index), `Price`, `OracleUpdate`,
  `FiatRate`, and `strkey.go` format validators for G/C addresses.
- `internal/config/`: root `Config` + seven substructs (Region,
  Stellar, Storage, Ingestion, Aggregate, API, Obs) with struct-
  tag–driven doc generator. `Load` + `ApplyEnvOverrides` +
  `Validate` pipeline so env overrides are always validated.
  Startup error-log when `auth_mode != "none"` (auth middleware
  not yet wired). S3 config validated all-or-nothing.
  `docs-config` subcommand on `ratesengine-ops` emits
  `docs/reference/config/README.md` with the mandatory
  generated-file banner.
- `internal/stellarrpc/`: JSON-RPC client wrapping `getHealth`,
  `getLatestLedger`, `getNetwork`, `getVersionInfo`, `getEvents`,
  `getLedgers`, `getFeeStats`. Context-aware, concurrent-safe,
  mockable; identifiable `User-Agent`; post-decode sanity checks
  on GetEvents response (ledger bounds, event order). Tested
  against httptest.Server. `rpc-probe` subcommand on
  `ratesengine-ops`.
- `internal/consumer/`: stable `Source` interface (StreamLive /
  BackfillRange) that every on-chain, oracle, and CEX/FX source
  implements.
- `internal/sources/{soroswap,aquarius,phoenix,reflector}`:
  five-file per-source packages (doc/events/decode/consumer/tests)
  decoding canonical trades from Soroban events with compile-time
  `consumer.Source` assertions. Handles Soroswap Swap+Sync
  correlation, Phoenix 8-event-per-swap fanout, Aquarius
  multi-op-per-tx flat-counter fanout, and Reflector
  three-contract (DEX/CEX/FX) price-vector decoding.
  `sweepStale` uses event `ClosedAt` (not wall-clock) so backfill
  does not synthesise false orphans.
- `internal/storage/timescale/`: typed adapters for trades
  (InsertTrade idempotent, TradesInRange[After] cursor-paged),
  oracle updates, ingestion cursors (DB-level monotonic-advance
  guard), distinct assets + distinct pairs (cursor-paged,
  `hasMore` flag). Pool tuned for Patroni failover windows.
- `internal/api/v1/`: REST server with envelope-wrapped responses
  (`data` / `as_of` / `sources` / `flags` / `pagination`),
  RFC 9457 problem+json errors, handlers for `/healthz`,
  `/readyz` (parallel dependency pings under shared deadline),
  `/version`, `/assets`, `/assets/{asset_id}`, `/price`,
  `/history`, `/ohlc`, `/vwap`, `/twap`, `/markets`,
  `/oracle/latest`, and `/metrics` (unversioned, operator-facing).
- `internal/api/v1/middleware/`: RequestID → HTTPMetrics →
  Logger (slog access + remote_ip context) → Recoverer →
  SecurityHeaders → CORS (allow-list) → RateLimit (per-IP, Redis
  token bucket, skips health + /metrics). Stack order
  audited for preflight-free CORS and ratelimit-after-remote-ip
  invariants.
- `internal/ratelimit/`: Redis-backed atomic Lua token bucket
  with window-remaining Retry-After semantics,
  `url.QueryEscape` key-sanitisation, and bounded key length.
- `internal/metadata/`: SEP-1 / stellar.toml resolver with
  SSRF guard (loopback + RFC 1918 + link-local + metadata-IP
  deny), singleflight fan-in, and a Redis-backed cache that
  tolerates a nil client.
- `internal/obs/`: Prometheus non-default registry, HTTP
  metrics middleware (`http_requests_total`,
  `http_request_duration_seconds`), shared slog factory.
- `migrations/0001_create_trades_hypertable.{up,down}.sql` —
  `trades` hypertable (1-day chunks, compression policy after 7
  days, retention 90 days), four secondary indexes, and
  `ingestion_cursors` table.
- `migrations/0002_create_price_aggregates.{up,down}.sql` — the
  seven RFP-grain continuous aggregates (1m/15m/1h/4h/1d/1w/1mo)
  with VWAP + TWAP + OHLC tuple + per-CAGG refresh & retention
  policies.
- `migrations/0003_create_oracle_updates_hypertable.{up,down}.sql`
  — `oracle_updates` hypertable with compression + retention +
  `(asset_id, source, ts DESC)` index for "latest per source".
- `cmd/ratesengine-migrate`: golang-migrate wrapper with
  subcommands `up`, `down [N]`, `status`, `version`, `force`,
  `help`. DSN via `-dsn` flag or `RATESENGINE_POSTGRES_DSN` env.
- `cmd/ratesengine-indexer`: orchestration binary for the source
  pipeline with graceful shutdown, per-source supervisor +
  restart policy, and an embedded Prometheus scrape server on
  `obs.MetricsListen` so ingestion alerts actually have a target.
- `cmd/ratesengine-api`: REST server binary with `-dry-run` (now
  pings Postgres + Redis for real), signal-driven graceful
  shutdown (30 s drain), SEP-1 cache wiring, optional CORS, and
  optional rate-limit middleware.
- `cmd/ratesengine-aggregator`: scaffold for the VWAP/TWAP +
  continuous-aggregate refresh orchestrator.
- `cmd/ratesengine-ops`: admin CLI with `docs-config`,
  `rpc-probe`, backfill, and gap-detect subcommands.
- `deploy/docker-compose/dev.yaml`: local TimescaleDB (pg15) +
  Redis 7 + MinIO with a one-shot bucket initialiser. Driven by
  `.env.example`. `make dev` end-to-end works.
- `test/integration/`: testcontainers-go round-trip proofs for
  migrations, API (readyz, oracle/latest), trades (multi-op
  fanout, cursor regressions), CHECK-constraint enforcement,
  CAGG policy attachment, DistinctPairs pagination. Guarded by
  `//go:build integration`.
- `configs/ansible/roles/archival-node/`: full Ubuntu-22.04
  bootstrap role (ZFS raidz2, Postgres 15, stellar-core,
  Galexie, stellar-rpc, MinIO, nftables, node_exporter,
  SSH hardening). Hardware-agnostic via inventory.
- `docs/operations/runbooks/`: 38 runbooks covering every
  currently-defined Prometheus alert (ingestion-lag,
  decode-errors, cursor-stuck, rpc-lag, source-stopped,
  orphan-events, cagg-stale, compression-lag, insert-errors,
  price-divergence, price-stale, oracle-stale, api-down,
  api-5xx, api-latency, redis-*, timescale-primary-down,
  archive-*, replica-lag, scrape-failing, deadmansswitch,
  backup-failed, db-disk-full, host-*, nvme-*, pg-conns-saturated,
  zfs-degraded, alertmanager-bad-config, core-lag, core-peers,
  bootstrap-archival-node). CI enforces alert ↔ runbook
  bijection via `scripts/ci/lint-docs.sh`.
- `scripts/ci/lint-docs.sh`: BSD-sed-compatible pre-merge doc
  linter — config drift, OpenAPI routes ↔ handlers, metrics
  catalogue, stale refs, TODOs, frontmatter, banners, ADR
  index, runbook URLs, alerts-catalog drift.

### Tested against

- Stellar protocol 25.x (mainnet passphrase
  `"Public Global Stellar Network ; September 2015"`).
- stellar-core `v26.0.1`, stellar-rpc `v26.0.0`,
  stellar-galexie `v26.0.0`.
- `go-stellar-sdk v0.5.0`, `withObsrvr/stellar-extract v0.1.2`.
- `timescale/timescaledb:2.17.2-pg15`, `redis:7.4-alpine`,
  `minio:RELEASE.2024-11-07`.
- `golang-migrate v4.19.1`, `testcontainers-go v0.38+`.

---

<!--
Release sections will be added here as versions ship. Keep the
[Unreleased] block at the top; the release workflow moves it
under the new version header on tag push.

Example of a future release entry:

## [2026.06.30.1] — 2026-06-30 — Initial public release

### Added
- Full SDEX / Soroswap / Aquarius / Phoenix / Comet / Blend indexing.
- Reflector / Redstone / Band oracle integration.
- Since-inception OHLC for top-20 pairs.
- REST + SSE API v1.

### Tested against
- Stellar protocol 25.x.
- stellar-core v26.0.1, stellar-rpc v26.0.0.
-->
