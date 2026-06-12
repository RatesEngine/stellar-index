# CLAUDE.md — repo orientation for AI agents

If you are an AI agent (Claude, any other assistant) opening this
repo cold, **this file is your entry point**. Read this first.

---

## What this repo is

**Stellar Atlas** (formerly Rates Engine; renamed + repositioned
2026-06-12, ADR-0036) is a **protocol explorer for the Stellar network**:
complete, verified, per-protocol on-chain data — every contract, event,
and trade for every major Stellar protocol — captured from a certified
raw ledger lake (ADR-0034), verified for completeness (ADR-0033), and
attributed by contract identity (ADR-0035; per-protocol verification
pages live in docs/protocols/). Long-term it grows into a comprehensive
blockchain explorer (classic/native + Soroban).

Its flagship product is the **pricing API**: it ingests on-chain and
off-chain price data, aggregates into VWAP / TWAP / OHLC, and serves
the result through a public REST + SSE API.

Built against two customer RFPs ([docs/stellar-rfp.md](docs/stellar-rfp.md),
[docs/freighter-rfp.md](docs/freighter-rfp.md)) and the awarded
[docs/ctx-proposal.md](docs/ctx-proposal.md).

The repo is Go (primary), Apache-2.0, pre-v1 at time of writing.

---

## Build + test commands

```sh
make help              # list all targets
make dev               # docker-compose up the local dependency stack (TimescaleDB + Redis + MinIO); the app binaries run on the host, and there is no API/ClickHouse service in the compose file
make test              # unit tests (fast; ~2 min)
make test-integration  # integration tests — spins its own containers via testcontainers-go (requires Docker)
make lint              # golangci-lint (gofumpt runs as a golangci formatter; architectural import boundaries enforced by scripts/ci/lint-imports.sh)
make build             # all binaries into bin/
make docs-all          # regenerate docs/reference/ from OpenAPI + struct tags + obs/*.go metric Name: fields
make verify            # canonical pre-push gate (fmt, vet, lint, docs, vuln, test) — run this before every push
```

For verifying a live deployment (R1 or local), use:

```sh
bash scripts/dev/r1-smoke.sh                       # localhost:3000 by default
API_BASE_URL=http://r1:3000 bash scripts/dev/r1-smoke.sh
```

13 GETs across health / catalogue / pricing / diagnostics with jq
shape assertions; exit code = number of failures so cron and
Healthchecks.io can consume it. R1 runs the same script every 5
min via `stellaratlas-smoke.timer` (see `configs/healthchecks/`).

No command should ever require manual network access during
development. If one does, it's a bug.

---

## Repo map

```
.
├── README.md                  project overview, user-facing
├── CLAUDE.md                  this file — agent orientation
├── LICENSE                    Apache-2.0
├── CHANGELOG.md               Keep-a-Changelog format
├── CONTRIBUTING.md            how to contribute
├── CODE_OF_CONDUCT.md         Contributor Covenant
├── SECURITY.md                vuln-disclosure process
├── CODEOWNERS                 review routing
├── VERSIONS.md                pinned SHAs of upstream deps we audit
├── Makefile                   canonical build/test commands
├── go.mod / go.sum            single Go module for the whole repo
├── .golangci.yml              lint config
├── .github/                   workflows + issue/PR templates
│
├── cmd/                       binary entry points (six in total)
│   ├── stellaratlas-indexer/              ingestion pipeline: Galexie → ClickHouse raw lake + Timescale served tier (dual-sink, ADR-0034)
│   ├── stellaratlas-aggregator/           VWAP/TWAP + continuous aggregates
│   ├── stellaratlas-api/                  REST + SSE API server
│   ├── stellaratlas-ops/          admin CLI: backfill, detect-gaps, verify-archive, wasm-history, …
│   ├── stellaratlas-migrate/      db migration runner
│   └── stellaratlas-sla-probe/    SLA-evidence harness: p50/p95/p99 latency + freshness pass/fail vs RFP targets
│
├── internal/                  private packages (Go-enforced, not importable externally)
│   ├── canonical/                core types: Trade, Price, Asset, Pair, Amount
│   ├── config/                   config loading + schema
│   ├── consumer/                 transport-neutral ingest contracts — the load-bearing `consumer.Event` / `consumer.Source` types used across indexer/ops/dispatcher/pipeline. (`consumer.Orchestrator` is a legacy seam with no callers; prod ingest is dispatcher-based.)
│   ├── ledgerstream/             archive/live LedgerCloseMeta streaming
│   ├── dispatcher/               production ledger walker + decoder router
│   ├── pipeline/                 shared ingest-pipeline glue used by both indexer + `stellaratlas-ops backfill`
│   ├── projector/                ONLY writer for Soroban-derived events — projects per-source tables from soroban_events (ADR-0031/0032)
│   ├── completeness/             ADR-0033 coverage verification: substrate + recognition + projection reconcile → completeness_snapshots
│   ├── events/                   transport-neutral Soroban contract-event types (RPC or LCM-extracted)
│   ├── scval/                    narrow SCVal primitives wrapper around go-stellar-sdk/xdr
│   ├── stellarrpc/               JSON-RPC client for diagnostics + fixture capture, not prod ingest
│   ├── sources/                  one package per source (on-chain + CEX + FX)
│   ├── aggregate/                VWAP/TWAP/outlier/triangulation
│   ├── storage/                  TimescaleDB (served tier) + ClickHouse (raw lake, ADR-0034) + Redis + MinIO adapters
│   ├── archivecompleteness/      dual-archive completeness daemon (ADR-0017)
│   ├── hashdb/                   on-disk (ledger_seq → sha256(LCM)) record for drift-detection-vs-upstream-rewrites. LIBRARY ONLY — currently has zero production callers; not yet wired into any binary (the ADR-0033 "feeder" role is aspirational).
│   ├── api/                      REST/SSE handlers (v1)
│   ├── ratelimit/                Redis-backed token bucket
│   ├── metadata/                 SEP-1 / stellar.toml resolution
│   ├── cachekeys/                canonical Redis key builders (ADR-0007)
│   ├── version/                  build-time version info (ldflags-populated)
│   ├── obs/                      metrics, tracing, logging
│   ├── supply/                   circulating/total/max supply derivation
│   ├── auth/                     API-key + SEP-10 auth primitives
│   ├── currency/                 verified-currency catalogue (hand-curated seed; R-018)
│   ├── divergence/               cross-check against CoinGecko + Chainlink-HTTP (CMC is deferred — no implementation yet)
│   ├── customerwebhook/          drains the webhook delivery queue — HMAC-signs + POSTs pending rows, backoff/retry on failure
│   ├── incidents/                loads + parses embedded customer-facing incident post-mortems for the API + status page
│   ├── notify/                   transactional-email abstraction (Sender iface; Resend impl + Noop for dev/tests)
│   ├── obstest/                  test helpers for asserting against Prometheus metrics (HistogramVec child counts)
│   ├── platform/                 customer + staff dashboard primitives (accounts, API keys, webhooks, usage) per platform-spec.md
│   └── usage/                    per-subject daily request counters backed by Redis (feeds /v1/account/usage)
│
├── pkg/                      public surface (SemVer-stable)
│   └── client/                   Go client SDK + wire-shape types
│                                 (Envelope, Flags, AssetDetail, …)
│                                 — types live alongside the client
│                                 in pkg/client/types.go rather than
│                                 a separate pkg/types directory.
│

├── migrations/                TimescaleDB migrations (golang-migrate)
├── configs/                   example.toml + Ansible roles (configs/ansible/{roles,inventory,playbooks}/) + R1 single-host overlays
│   ├── ansible/                  multi-host roles + playbooks for R1/R2/R3
│   ├── prometheus/               R1 single-host: prometheus.r1.yml + rules.r1/ (job names rewritten for the R1 scrape config)
│   ├── alertmanager/             R1 single-host: alertmanager.r1.yml + apply.sh (severity-routing for page/ticket/informational + deadmansswitch heartbeat)
│   ├── caddy/                    R1 reverse proxy — TLS termination via Let's Encrypt
│   ├── loki/                     R1 single-host log aggregation
│   ├── audit/                    curated auditor inputs (wasm-walk contract lists) feeding `stellaratlas-ops wasm-history`
│   └── healthchecks/             per-binary heartbeat + 5-min API smoke timers (Healthchecks.io)
├── openapi/                   stellar-atlas.v1.yaml — source of truth for API
├── examples/                  curl scripts + Postman collection (auto-gen) for the public API
├── deploy/                    docker-compose (dev), systemd (production unit files), monitoring (Prometheus rules — multi-host), clickhouse/ (tier-1 lake DDL, ADR-0034), comms/ (customer-facing incident/launch templates). The shipped status-page lives at `web/status/` (Cloudflare Pages static export); earlier scaffolds were retired (F-1211 / wave 57).
├── web/explorer/              Next.js 15 static-export explorer rendered at stellaratlas.xyz (Cloudflare Pages)
├── scripts/                   dev/ops/ci helpers (incl. ci/lint-docs.sh, dev/r1-smoke.sh)
├── test/                      integration / fixtures (build tag: integration), load (k6), chaos
│
└── docs/
    ├── architecture/             narrative designs (last_verified checked in CI)
    ├── adr/                      Architecture Decision Records (immutable)
    ├── reference/                auto-generated from OpenAPI + struct tags
    ├── operations/               runbooks (1 per alert), SEV playbook, release-process
    ├── discovery/                Phase-1 audit archive (read-only, closed 2026-04-22)
    ├── methodology/              public methodology docs (how prices are computed/aggregated)
    ├── blog/                     dated blog posts published to the explorer/site
    ├── audit-2026-04-29/         post-Phase-1 cross-cutting findings register
    ├── audit-2026-05-02/         second-pass audit working dir (May 2; codex review)
    ├── audit-2026-05-12/         May-12 audit working dir
    ├── audit-2026-05-12-codex/   May-12 codex-review pass
    ├── audit-2026-05-26/         May-26 audit working dir
    └── audit-2026-06-11/         June-11 full-product cold audit (findings register + per-slice evidence)
```

---

## Invariants — never violate these

These are the architectural commitments binding every PR. See
[docs/adr/](docs/adr/) for the long-form rationale per ADR.

### 1. i128 / u128 never truncates to int64 (ADR-0003)

Token amounts, reserves, prices, supplies from Soroban are always:

- `*big.Int` in Go.
- `NUMERIC` column in Postgres / TimescaleDB.
- Strings in JSON (JSON numbers are IEEE 754 doubles — precision
  loss above 2^53).

Parsing `xdr.Int128Parts` to `int64(parts.Lo)` is a bug we will
find and reject in review every single time.

### 2. Horizon is not in our architecture (ADR-0001)

We don't run Horizon. We don't ingest from Horizon. We don't proxy
to Horizon. If a third-party protocol's only path to us is via
Horizon, we do not integrate it.

### 3. Self-hosted storage is S3-compatible — not local filesystem (ADR-0002)

Galexie's local filesystem backend silently drops per-object
metadata + has explicit multi-process-write warnings in its own
docstring. MinIO is our colo default; any S3-compatible service
works via `endpoint_url` override.

### 4. One Go module, monorepo (ADR-0005)

`github.com/StellarAtlas/stellar-atlas` is the root module. `internal/` is
private (Go-enforced). `pkg/` is the public surface with SemVer
compatibility promise.

### 5. Tier-1 three-validator aspiration (ADR-0004)

Post-launch we run three geographically-separated full validators
with independent history archives. Validator keys live in an HSM;
never on disk unencrypted.

### 6. Ingest goes via Galexie → dispatcher → decoder. Never stellar-rpc.

**Production ingest:** `Galexie MinIO → internal/ledgerstream →
internal/dispatcher → internal/sources/<venue>/decode`. Decoders are
pure functions; no RPC client, no pagination loop, no per-source
goroutine. stellar-rpc was removed from r1 on 2026-04-23; it exists
only for the `rpc-probe` operator diagnostic and for capturing test
fixtures via `scripts/dev/`. Any new source that adds a
`rpc *stellarrpc.Client` field or a `BackfillRange` / `StreamLive`
method is wrong.

Full picture + binding rules: [docs/architecture/ingest-pipeline.md](docs/architecture/ingest-pipeline.md).

### 7. One writer per data domain (ADR-0031 + ADR-0032)

**Soroban-derived events** (`trades`, `blend_*`, `phoenix_*`,
`comet_*`, `soroswap_skim`, `cctp_events`, `rozo_events`,
`defindex_*`, `sep41_*`, `reflector`/`redstone` `oracle_updates`)
are written by **`internal/projector`** — and ONLY by the projector — from
the `soroban_events` raw landing zone (ADR-0029). Adding a new
Soroban source means adding a case in
`internal/projector/registry.go::buildSource` AND an arm in
`internal/pipeline/sink.go::IsProjectedEvent`. Catch-up after a
missing window is `stellaratlas-ops projector-replay -source <name>
-from <ledger>` — never a bespoke `<source>-backfill` subcommand
(those were deleted in rc.97 / ADR-0032 Phase 5).

**Non-projected events** (`sdex`, external CEX/FX, `band`,
`soroswap_router` (ContractCall-derived, log-only — excluded from
`IsProjectedEvent`), supply observers) continue writing through the
dispatcher's events goroutine — they don't flow through
`soroban_events` and have their own catch-up paths.

**Coverage signal** is data-derived, not cursor-derived (ADR-0031):
cursor-derived helpers under `internal/api/v1` were deleted in
rc.93/94. The authoritative coverage VERDICT is `completeness_snapshots`
(ADR-0033: substrate + recognition + projection reconcile against the
ClickHouse lake); the gap-detector's `source_coverage_snapshots` is a
supporting signal.

### 8. ClickHouse is the raw lake; Postgres is the served tier (ADR-0034)

The certified raw history of every ledger + event lives in **ClickHouse**
(`galexie → ClickHouse → decoders → Postgres`). **Postgres/TimescaleDB is
the SERVED tier** — the recent working set the API queries, NOT the full
archive. Re-backfilling Postgres for full history was abandoned as
infeasible (OLTP-for-OLAP, billions of rows). Consequences:

- **"100% coverage" = the ClickHouse substrate captured everything**
  (provable: ledgers contiguous + hash-chained to genesis). The served
  tier is verified faithful *within what it holds* (ADR-0033 projection
  reconcile, retention-scoped).
- **Raw `trades` are kept forever** in Postgres — migration 0031 removed
  the old 90-day retention (storage is not a constraint). If you see a
  `drop_after` retention policy on `trades`, it's drift — remove it.
- **Continuous aggregates**: `prices_1h/4h/1d`+ are indefinite (daily
  OHLC spans back to 2015); `prices_1m/15m` retention was also removed.
- **Decoder backfills re-derive from the lake** (SQL / `ch-rebuild`), not
  MinIO walks. Projected-source catch-up is `projector-replay`.

---

## Things that will surprise you

Traps / counter-intuitive facts surfaced during Phase-1 discovery.
If you're about to do something that touches any of these, read the
linked doc first.

- **Soroswap's `SwapEvent` has no post-state reserves.** Reserves
  come in the immediately-following `SyncEvent`. Correlate by
  `(ledger, tx_hash, op_index)`. →
  [docs/discovery/dexes-amms/soroswap.md](docs/discovery/dexes-amms/soroswap.md)
- **Phoenix emits 8 events per swap** (one per field with a 2-tuple
  topic `("swap", "<field>")`). A single swap reconstruction
  requires grouping all 8. →
  [docs/discovery/dexes-amms/phoenix.md](docs/discovery/dexes-amms/phoenix.md)
- **Comet uses a shared `("POOL", <event>)` topic across every pool
  contract**, not a per-protocol namespace. Any pubnet contract that
  deploys Balancer-v1 Comet code will look identical on the wire.
  **As of ADR-0035, decoders gate `Matches()` on contract identity, not
  topic bytes** — but Comet has no factory namespace to anchor on, so it
  is the one open case: it adopts an operator-configured pool allowlist
  or a WASM-hash gate rather than the factory-fan-out model the other
  Soroban decoders use. Until that lands, narrow coverage is still a
  downstream filter on `Trade.Source = "comet"` + contract address. →
  [docs/discovery/dexes-amms/comet.md](docs/discovery/dexes-amms/comet.md),
  [docs/adr/0035-factory-anchored-contract-gating.md](docs/adr/0035-factory-anchored-contract-gating.md)
- **Reflector v3 has no on-chain `twap` or `x_*` methods.**
  Proposal says it does; it doesn't. We compute TWAP and cross-pair
  locally. →
  [docs/discovery/oracles/reflector.md](docs/discovery/oracles/reflector.md)
- **Reflector is three separate contracts** (DEX / CEX / FX), not
  one. →
  [docs/discovery/oracles/reflector.md](docs/discovery/oracles/reflector.md)
- **Band stores pair rates at E18 scale**. Relayed single-asset
  rates are at E9. →
  [docs/discovery/oracles/band.md](docs/discovery/oracles/band.md)
- **Band's Soroban contract emits zero events.** A conventional
  topic-match Decoder never fires on Band. We observe the
  `relay()` / `force_relay()` InvokeContract call instead via
  the dispatcher's `ContractCallDecoder` interface (PR 168). Any
  future Soroban source that updates storage without publishing
  events plugs into the same hook — match by (contract_id,
  function_name), decode from op args. →
  [docs/discovery/oracles/band.md](docs/discovery/oracles/band.md)
- **Off-chain sources (CEX/FX) live in `internal/sources/external/`,
  not `internal/sources/<venue>/`.** They run their own goroutines
  speaking HTTPS / WebSocket to vendor APIs — parallel to the
  Galexie → dispatcher path, not under it. Source-class metadata
  (`exchange`/`aggregator`/`oracle`/`authority_sanity`) lives in
  `external.Registry` — a single Go map the aggregator queries at
  VWAP compute time to decide which sources contribute. Only
  `ClassExchange` contributes by default; aggregators and oracles
  are reported alongside but excluded (mixing them double-counts
  upstream markets or imposes their methodology on our output).
- **External-source amount scaling is NOT uniform.** On-chain
  sources stamp `canonical.Trade.BaseAmount` / `QuoteAmount` at
  per-asset decimals (XLM=7, Soroban tokens vary). Off-chain CEX +
  reference-aggregator sources normalise to a fixed **10^8** integer
  scale (`binance.externalAmountDecimals`), but the **FX pollers**
  (`ecb` / `exchangeratesapi` / `polygonforex`) use **10^6**
  (`DefaultDecimals = 6`). Always read the per-source `Decimals`
  field; don't assume 10^8. Aggregator looks up
  `external.Lookup(trade.Source).Class` to know which side of
  the boundary a trade came from.
- **Stablecoin fiat-proxy is aggregator policy, not decoder
  policy.** Ingest stores the real pair (`XLM/USDT`, `XLM/USDC`).
  The aggregator maps `USDT→USD`, `USDC→USD`, `DAI→USD`,
  `PYUSD→USD`, `USDP→USD`, `EURC→EUR`, `EUROC→EUR`, `EUROB→EUR`,
  `MXNe→MXN` at VWAP compute time (full map: `internal/aggregate/stablecoin.go`).
  Eager normalisation at ingest would hide a depeg event; late
  binding keeps data honest.
- **Redstone Adapter DOES emit events** (topic `"REDSTONE"`) — one
  per batch push containing all updated feeds. Subscribe rather
  than poll all 19 per-feed contracts. →
  [docs/discovery/oracles/redstone.md](docs/discovery/oracles/redstone.md)
- **Redstone's event body carries no feed_id.** `WritePrices
  { updater, updated_feeds: Vec<PriceData> }` gives prices +
  timestamps, not which feed each entry is. Feed IDs live in the
  tx's `write_prices(updater, feed_ids, payload)` InvokeContract
  op args — plumbed through `events.Event.OpArgs` (PR 166). The
  adapter's freshness verifier can filter `updated_feeds` to a
  subset of `feed_ids`; zip only when lengths match, else skip
  the whole event (`ErrFeedIDCountMismatch`). Any new decoder that
  needs tx args follows the same `events.Event.OpArgs` pattern.
- **Post-P23 (Whisk, mainnet 2025-09-03) every classic asset
  movement emits a unified transfer/mint/burn event with a 4th
  `sep0011_asset` topic.** Our decoder handles both event SHAPES —
  the 3-topic SEP-41 form and the 4-topic CAP-67 form (the 4th topic
  is `sep0011_asset`). It does NOT, however, parse pre-P23 classic
  movements: there is no operations+effects fallback for the era
  before unified events existed, so historical classic-asset movement
  before P23 is not reconstructed from this path. →
  [docs/discovery/notes/cap-67-unified-events.md](docs/discovery/notes/cap-67-unified-events.md)
- **SEP-41 `transfer` data can be EITHER a simple `i128` OR a map**
  containing `amount` + `to_muxed_id`. Type-test before
  `MustI128()`. →
  [docs/discovery/notes/sep-41-token-events.md](docs/discovery/notes/sep-41-token-events.md)
- **stellar/go monorepo was archived 2025-12-16.** The new Go SDK
  lives at `github.com/stellar/go-stellar-sdk`. Horizon, Galexie,
  stellar-rpc, stellar-archivist are each in their own repos now. →
  [docs/discovery/data-sources/stellar-archivist.md](docs/discovery/data-sources/stellar-archivist.md)
- **`withObsrvr/cdp-pipeline-workflow` has verified correctness
  bugs** in its i128 decoding and SDEX trade extraction. We do
  **not** inherit from it. →
  [docs/discovery/data-sources/withobsrvr-cdp-pipeline-workflow.md](docs/discovery/data-sources/withobsrvr-cdp-pipeline-workflow.md)
- **stellar-rpc is NOT in our production ingest path.** The
  standalone `stellar-rpc` and `stellar-core` watcher services were
  removed from r1 on 2026-04-23, along with the core prometheus
  exporter. `stellar-core` itself still runs on r1 — but only as a
  *captive-core* subprocess spawned by Galexie
  (`/usr/bin/stellar-core … --metadata-output-stream fd:3`) — which
  is the supported Galexie deployment shape per ADR-0002. The
  indexer reads Galexie's MinIO output directly via
  `go-stellar-sdk/ingest.ApplyLedgerMetadata`. If you catch yourself
  writing `rpc.GetEvents` for ingest, stop and read
  [docs/architecture/ingest-pipeline.md](docs/architecture/ingest-pipeline.md). →
  [docs/operations/r1-deployment-state.md](docs/operations/r1-deployment-state.md)
- **Soroban DeFi contracts upgrade in place.** Soroswap / Phoenix /
  Aquarius / Reflector can each `update_contract` without changing
  their contract address. Event body schemas (field names, types,
  arity) and topic shapes can change across an upgrade. Live ingest
  only sees current WASM; **backfill sees every prior version** that
  ran for the replayed range. Decode by Map-field-name not position,
  dispatch on topic[0] symbol not contract address, and gate
  backfill behind a per-WASM-hash decoder audit. →
  [docs/architecture/contract-schema-evolution.md](docs/architecture/contract-schema-evolution.md)
- **`/v1/assets/{slug}` returns two different wire shapes.**
  When `{slug}` is a verified-currency catalogue slug (`usdc`,
  `eurc`, `aqua`, …) the handler returns `GlobalAssetView`
  (cross-chain identity + `networks[]`); when it's a canonical
  asset_id (`USDC-G…`, `native`, `C…`, `fiat:USD`) it returns
  `AssetDetail` (per-Stellar-asset detail). Same route, two
  shapes — Go's mux dispatches on the catalogue lookup before
  parsing as canonical. Clients distinguish via wire-shape
  discriminators (`ticker` + `networks` array vs `asset_id` +
  `type`). See R-018 / `docs/architecture/multi-network-assets-migration.md`.
- **`internal/currency` is the verified-currency trust surface.**
  Hand-curated YAML at `internal/currency/data/seed.yaml`, embedded
  in the binary via `//go:embed`. Adding a verified currency means
  a code change + redeploy. The catalogue feeds the CG poller's
  ticker map, the indexer's aggregator pair set, the
  unverified-collision warning on `/v1/assets/{id}`, the
  `/v1/assets/verified` listing endpoint, and the explorer's
  verified-badge UI. Do NOT auto-populate from CG / CMC — the
  whole point is that it's hand-vetted.

---

## Common task recipes

### "Bring up a new archival node" / "recover from disaster"

End-to-end recipe in [docs/operations/archival-node-bringup.md](docs/operations/archival-node-bringup.md).
Six steps from a fresh box to a running indexer (greenfield ≈ 10–13 h
wall, mostly bandwidth). Same doc has the disaster-recovery triage
tree (corrupt history-archive, partial galexie-archive partition,
wiped postgres, lost MinIO data dir).

Per-region storage shape varies by provider — see
[ADR-0016](docs/adr/0016-per-region-storage-strategy.md): R1
(Hetzner) is the full-mirror integrity leader; R2 (AWS) reads
galexie data direct from `aws-public-blockchain` S3, no local
mirror; R3 (Vultr) keeps galexie-archive on Vultr Object Storage
hybrid. R2 + R3 trust R1's primary verification but run their own
Tier A + Tier D periodically as defence-in-depth. The cross-region
"all regions serve the same rate" property is preserved by
[ADR-0015](docs/adr/0015-last-closed-bucket-rate-serving.md)'s
closed-bucket-only API contract.

### "Add a new CEX connector"

CEX/FX venues are NOT under the Galexie → dispatcher path and do NOT
use `internal/sources/<venue>/` or the on-chain five-file convention.
They live directly at `internal/sources/external/<venue>/` and
implement the `external.Connector` framework
(`internal/sources/external/framework.go`), not `consumer.Source`.
Copy the `binance` / `kraken` package as the template.

1. Read [docs/discovery/external-refs/cex-feeds.md](docs/discovery/external-refs/cex-feeds.md).
2. Pick the right reference in the existing Dash Retail Rates code
   (`~/code/rates/rate_source_<venue>.go`) — those connectors have
   the vendor's real endpoints + pair conventions documented.
3. Create `internal/sources/external/<venue>/` following the
   actual per-package layout: `events.go` (wire types), `parse.go`
   (vendor JSON → `canonical.Trade`), `streamer.go` (live WS/REST;
   implements `external.Streamer`) and/or a poller, `backfill.go`
   (historical OHLC; implements `external.Backfiller`), and
   `pairs.go` (symbol map). Tests sit alongside as `*_test.go`.
4. Implement the relevant `external.Connector` sub-interface(s) —
   `Streamer` (live push, e.g. binance/kraken), `Poller` (REST quote
   board, used by the FX pollers), and/or `Backfiller` (historical
   candles). NOT `consumer.Source` — that's the legacy on-chain seam.
5. Register the venue's `Metadata` (class / subclass / weight /
   `IncludeInVWAP` / `BackfillSafe`) in the `Registry` map in
   `internal/sources/external/registry.go`, then wire it into
   `buildExternal` in `cmd/stellaratlas-indexer/main.go` (and the
   parallel block in `stellaratlas-ops`) behind a `cfg.<Venue>.Enabled`
   gate.
6. Fixtures are inline golden frames in the package's `*_test.go`
   (e.g. `binance/streamer_test.go`) — there is no
   `test/fixtures/external/` directory.
7. Add an ADR if the venue has unusual constraints (e.g. requires
   paid tier, or has licensing restrictions on redistribution).

### "Add a new on-chain Soroban DEX"

Use the five-file convention (`README.md`, `events.go`, `decode.go`,
`consumer.go`, `source_test.go`). Template PR: look at how Soroswap was
added (`internal/sources/soroswap/`). Differences per DEX usually
boil down to event topic shape + amount-decoding quirks.

### "Add a new supply observer"

Read [docs/architecture/supply-pipeline.md](docs/architecture/supply-pipeline.md)
first — it covers the three-domain split (Algorithm 1 XLM /
Algorithm 2 classic / Algorithm 3 SEP-41), which dispatcher hook
each observer plugs into, and where the per-class hypertables
live. New observers ship as a Go package with package-level docs
in `doc.go` (not `README.md` — supply observers follow Go
package-doc convention; `events.go`, `decode.go`, `consumer.go`,
and a `dispatcher_adapter*.go` pair complete the layout). Pick the
right dispatcher hook based on what the source emits:

- `LedgerEntryChangeDecoder` for `LedgerEntry` mutations (current
  use: AccountEntry / trustline / claimable / LP-reserve
  observers).
- `OpDecoder` for classic operations (e.g. `change_trust_op`).
- `Decoder` (event-based) for Soroban contract events (current
  use: SEP-41 mint/burn/clawback observer).

The reader/storage seam is the same across all three: each
observer writes to a per-class hypertable
(`migrations/0011-0014_*.sql` etc.), and `StorageClassicSupplyReader`
/ `StorageSEP41SupplyReader` aggregate the rows at refresh time.
Wire the new observer into `cmd/stellaratlas-indexer/main.go`
alongside the existing supply observers and add an integration
test under `test/integration/` if it touches NUMERIC arithmetic
(see PR #316 / #317 for the testcontainers-go pattern).

### "Audit a Soroban source's WASM history (flip BackfillSafe)"

Procedure: [docs/operations/wasm-audits/README.md](docs/operations/wasm-audits/README.md).
One audit log per source under that directory; each is the
evidence trail for flipping `internal/sources/external/registry.go`'s
`BackfillSafe` flag from `false` → `true`. The flag gates
`stellaratlas-ops backfill` from running an unaudited Soroban
source against historical ranges (CLAUDE.md "Soroban DeFi
contracts upgrade in place").

### "Investigate a price divergence"

Start at [docs/operations/runbooks/price-divergence.md](docs/operations/runbooks/price-divergence.md).
Aggregator-layer alerts (silent / outlier-storm / class-drop-spike)
have their own runbooks under the same directory; see
[docs/architecture/aggregation-plan.md](docs/architecture/aggregation-plan.md)
for the policy chain that drives them.

### "Find why a metric is alerting"

Every alert references a runbook at `docs/operations/runbooks/<alert-name>.md`.
If it doesn't, that's a CI failure.

### "Add a new Prometheus metric"

1. Declare the metric in `internal/obs/metrics.go` (one of the
   typed `*Vec` variables near the bottom) and register it in
   the `init()` block.
2. Wire it at the point of observation. For goroutine workers
   doing IO, the established pattern is paired:
   - `*Total{outcome}` counter for outcomes (per-attempt count).
   - `*DurationSeconds{outcome}` histogram for latency.
   Operators chart `outcome="ok"` p95/p99 separately from
   failure outcomes to detect "endpoint slow" vs "endpoint
   failing" independently. The wave-88/89/90/91 series
   (`customer_webhook_delivery`, `divergence_refresh`,
   `aggregator_supply_refresh`, `anomaly_freeze_recovery_sweep`)
   are the canonical examples.
3. Document in `docs/reference/metrics/README.md` with a
   when-to-look-at-this prose block.
4. If the metric warrants alerting, add a rule to BOTH
   `deploy/monitoring/rules/<area>.yml` (multi-host) and
   `configs/prometheus/rules.r1/<area>.yml` (R1 overlay). CI's
   `monitoring-rules` job validates both directories with
   promtool (wave 96); the wave-82 lint catches sibling-file
   presence and the wave-78 template-presence lint catches
   missing runbook structure.
5. Write a regression test in the worker's test file using
   `obstest.HistogramSampleCount` (`internal/obstest/`,
   wave 100) — the helper exists because
   `HistogramVec.WithLabelValues(...)` returns a
   `prometheus.Observer` not a Collector, so the official
   `testutil.CollectAndCount` can't act on per-label children
   directly.

### "Change the OpenAPI spec"

1. Edit `openapi/stellar-atlas.v1.yaml`.
2. `make docs-api` regenerates reference docs.
3. Handlers in `internal/api/v1/` get updated; contract tests
   verify they match.
4. Bump the API minor version if the change is additive, major
   if breaking.
5. CHANGELOG entry under `[Unreleased]`.

### "Cut a release"

We use SemVer (`vX.Y.Z`) for binary releases — see
[docs/architecture/semver-policy.md](docs/architecture/semver-policy.md)
for what bumps the major / minor / patch.

End-to-end (operator side):

1. **Curate `CHANGELOG.md` `[Unreleased]`** — make sure every entry
   cites a PR, every empty section is deleted.
2. **Promote the section** in a one-commit PR: replace `## [Unreleased]`
   with `## [vX.Y.Z] — YYYY-MM-DD` and add a fresh empty `[Unreleased]`
   block above it. Title the PR `release: vX.Y.Z`. Squash-merge once
   CI is green.
3. **Cut the tag** via the guard-rail script:
   ```sh
   git checkout main && git pull --ff-only origin main
   bash scripts/dev/cut-release.sh vX.Y.Z
   ```
   The script verifies branch + clean tree + sync + non-empty CHANGELOG
   section + green `verify.sh` before tagging and pushing. Pass
   `--dry-run` first to see the plan.
4. **`release.yml` fires automatically** on the tag push:
   - Cross-compiles every binary in `cmd/` for `linux/amd64`
     (arm64 was dropped 2026-05-08 — every region is amd64; re-add
     when an arm64 host is provisioned).
   - Computes SHA256SUMS
   - Auto-extracts the matching CHANGELOG section as release notes
   - Creates the GitHub Release (marked `--prerelease` if the tag
     contains a `-suffix`)
   - **Does NOT publish container images.** The previous GHCR job
     was dropped (no consumer existed); see
     `docs/operations/release-process.md` for the rationale.
     F-1221 (2026-05-13): pre-fix this paragraph claimed both
     amd64+arm64 and GHCR pushes — both were stale.

Full runbook + manual fallback in
[docs/operations/release-process.md](docs/operations/release-process.md).

### "Deploy a release to R1"

Deploys are operator-triggered, never automatic on tag.

```sh
gh workflow run deploy.yml \
  -f region=r1 \
  -f version=vX.Y.Z \
  -f binaries=stellaratlas-indexer,stellaratlas-aggregator,stellaratlas-api
```

The workflow downloads the binaries from the GitHub Release,
verifies SHA256SUMS, and runs an Ansible playbook over SSH that
does **stage → backup → atomic install → restart → health probe →
automatic rollback on failure**. Backups land at
`/usr/local/bin/<binary>.prev-<previous-tag>` with the most-recent
5 retained.

One-time setup: 4 GitHub secrets per region. Full operator runbook
including the rollback path: [docs/operations/deploy-workflow.md](docs/operations/deploy-workflow.md).

R2 / R3 are deferred — adding them is mechanical (4 secrets +
~4 lines of workflow YAML), no playbook changes needed.

---

## Where to find design rationale

- **What we decided + why:** [docs/adr/](docs/adr/) (numbered,
  immutable, accept-only-or-supersede).
- **What we discovered about Stellar / our deps:**
  [docs/discovery/](docs/discovery/). This is the Phase-1 audit
  archive; read-only going forward but referenced often.
- **What the customer asked for:**
  [docs/stellar-rfp.md](docs/stellar-rfp.md) +
  [docs/freighter-rfp.md](docs/freighter-rfp.md).
- **What we committed to deliver:**
  [docs/ctx-proposal.md](docs/ctx-proposal.md) + its corrections
  register at [docs/discovery/proposal-corrections.md](docs/discovery/proposal-corrections.md).
- **How every RFP row maps to a source:**
  [docs/discovery/rfp-requirements-matrix.md](docs/discovery/rfp-requirements-matrix.md).
- **Our 10-week calendar:**
  [docs/discovery/delivery-plan.md](docs/discovery/delivery-plan.md).
- **Engineering policy (mandatory reading before a PR):**
  [docs/discovery/engineering-standards.md](docs/discovery/engineering-standards.md).

---

## How to ask for help

- **Code review:** the appropriate CODEOWNER (see
  [CODEOWNERS](CODEOWNERS)).
- **Architectural decision:** propose an ADR in `docs/adr/` with
  status `Proposed`. Discuss in PR.
- **Security issue:** `security@stellaratlas.xyz` — do not open
  a public issue. See [SECURITY.md](SECURITY.md).
- **General contribution questions:** [CONTRIBUTING.md](CONTRIBUTING.md).

---

## If in doubt

Make the smallest possible PR that advances one thing and is easy
to review. Never "ship and clean up later." See
[docs/discovery/engineering-standards.md §2](docs/discovery/engineering-standards.md)
for the full Definition of Done.

---

## Working in a long session: commit-merge-repeat, not stack-then-split

If you are an AI agent running a multi-hour task (e.g. `/loop keep
going`), the default cadence is **one PR → one merge → next PR**.
Do NOT accumulate multiple narrative PRs of uncommitted work in the
tree and try to split them later — shared files
(`cmd/stellaratlas-indexer/main.go`, `internal/config/*`,
`CHANGELOG.md`, `CLAUDE.md`) will be touched by several narrative
PRs and cannot be cleanly split into per-PR commits without hunk
surgery.

The rule:

1. Pick one logical unit of work.
2. Make it build + its tests pass.
3. Commit, push, open PR, merge. (`gh pr merge --squash` once CI is
   green; merge with failing optional checks only if the failure is
   pre-existing CI infra, not caused by this PR.)
4. Pull main, branch again, return to step 1 for the NEXT unit.

Never plan a pipeline of 3–4 PRs before the first one has landed.
If you realise a task is bigger than one PR, split it into linear,
merge-as-you-go units — not parallel branches that will collide on
shared files.

The one exception: if the user is actively reviewing mid-session
and explicitly says "don't merge yet, I want to see the whole thing
first." Otherwise, merge.

---

_This file is hand-maintained. If you find a fact here that is no
longer true, update this file in the same PR as the change that
invalidated it. Freshness checked in CI._
