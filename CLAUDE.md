# CLAUDE.md — repo orientation for AI agents

If you are an AI agent (Claude, any other assistant) opening this
repo cold, **this file is your entry point**. Read this first.

---

## What this repo is

**Rates Engine** is a Stellar-network pricing API. It ingests on-chain
and off-chain price data, aggregates into VWAP / TWAP / OHLC, and
serves the result through a public REST + SSE API.

Built against two customer RFPs ([docs/stellar-rfp.md](docs/stellar-rfp.md),
[docs/freighter-rfp.md](docs/freighter-rfp.md)) and the awarded
[docs/ctx-proposal.md](docs/ctx-proposal.md).

The repo is Go (primary), Apache-2.0, pre-v1 at time of writing.

---

## Build + test commands

```sh
make help              # list all targets
make dev               # docker-compose up the full stack locally
make test              # unit tests (fast; ~2 min)
make test-integration  # integration tests — spins its own containers via testcontainers-go (requires Docker)
make lint              # gofumpt + golangci-lint + archlint
make build             # all binaries into bin/
make docs-all          # regenerate docs/reference/ from OpenAPI + struct tags
```

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
├── cmd/                       binary entry points (four in total)
│   ├── ratesengine-indexer/              ingestion pipeline: Galexie → Timescale
│   ├── ratesengine-aggregator/           VWAP/TWAP + continuous aggregates
│   ├── ratesengine-api/                  REST + SSE API server
│   ├── ratesengine-ops/          admin CLI: backfill, gap-detect, …
│   └── ratesengine-migrate/      db migration runner
│
├── internal/                  private packages (Go-enforced, not importable externally)
│   ├── canonical/                core types: Trade, Price, Asset, Pair, Amount
│   ├── config/                   config loading + schema
│   ├── consumer/                 the Source interface + orchestration
│   ├── stellarrpc/               JSON-RPC client for stellar-rpc (getEvents, getTransaction, …)
│   ├── sources/                  one package per source (on-chain + CEX + FX)
│   ├── aggregate/                VWAP/TWAP/outlier/triangulation
│   ├── storage/                  TimescaleDB + Redis + MinIO adapters
│   ├── api/                      REST/SSE handlers (v1)
│   ├── ratelimit/                Redis-backed token bucket
│   ├── metadata/                 SEP-1 / stellar.toml resolution
│   ├── cachekeys/                canonical Redis key builders (ADR-0007)
│   ├── version/                  build-time version info (ldflags-populated)
│   ├── obs/                      metrics, tracing, logging
│   ├── supply/        (planned)  circulating/total/max supply derivation
│   ├── auth/          (planned)  API-key + optional SEP-10
│   └── divergence/    (planned)  cross-check against CoinGecko/CMC/Chainlink-HTTP
│
├── pkg/            (planned)  public surface — no stable API yet
│   ├── client/                   Go client SDK for our API
│   └── types/                    stable types API consumers depend on
│
├── migrations/                TimescaleDB migrations (golang-migrate)
├── configs/                   default + example YAML + Ansible roles
├── openapi/                   rates-engine.v1.yaml — source of truth for API
├── deploy/                    docker-compose dev stack, k8s / baremetal kits
├── scripts/                   dev/ops/ci helpers (incl. ci/lint-docs.sh)
├── test/                      integration / fixtures (build tag: integration)
│
└── docs/
    ├── architecture/             narrative designs (last_verified checked in CI)
    ├── adr/                      Architecture Decision Records (immutable)
    ├── reference/                auto-generated from OpenAPI + struct tags
    ├── operations/               runbooks (1 per alert), SEV playbook
    └── discovery/                Phase-1 audit archive (read-only, closed 2026-04-22)
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

`github.com/RatesEngine/rates-engine` is the root module. `internal/` is
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
  contract**, not a per-protocol namespace. The decoder matches by
  topic bytes, not pool contract ID — any pubnet contract that
  deploys Balancer-v1 Comet code will look identical on the wire.
  Operators who want narrow coverage (e.g. only Blend's backstop
  pool) filter downstream by `Trade.Source = "comet"` +
  contract-address context rather than at dispatch time. →
  [docs/discovery/dexes-amms/comet.md](docs/discovery/dexes-amms/comet.md)
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
- **External-source amount scaling: uniform 10^8.** On-chain
  sources stamp `canonical.Trade.BaseAmount` / `QuoteAmount` at
  per-asset decimals (XLM=7, Soroban tokens vary). Off-chain
  sources normalise to a fixed 10^8 integer scale
  (`binance.externalAmountDecimals`). Aggregator looks up
  `external.Lookup(trade.Source).Class` to know which side of
  the boundary a trade came from.
- **Stablecoin fiat-proxy is aggregator policy, not decoder
  policy.** Ingest stores the real pair (`XLM/USDT`, `XLM/USDC`).
  The aggregator maps `USDT→USD`, `USDC→USD`, `PYUSD→USD`,
  `EUROC→EUR`, `EUROB→EUR`, `MXNe→MXN` at VWAP compute time.
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
  `sep0011_asset` topic.** Pre-P23 you parse operations+effects.
  Our decoder handles both. →
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
- **stellar-rpc is NOT in our production ingest path.** Removed
  from r1 on 2026-04-23 alongside stellar-core and the core
  prometheus exporter. The indexer reads Galexie's MinIO output
  directly via `go-stellar-sdk/ingest.ApplyLedgerMetadata`. If you
  catch yourself writing `rpc.GetEvents` for ingest, stop and read
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

1. Read [docs/discovery/external-refs/cex-feeds.md](docs/discovery/external-refs/cex-feeds.md).
2. Pick the right reference in the existing Dash Retail Rates code
   (`~/code/rates/rate_source_<venue>.go`) — those connectors have
   the vendor's real endpoints + pair conventions documented.
3. Create `internal/sources/external/cex/<venue>/` following the
   five-file convention: `README.md`, `events.go`, `decode.go`,
   `consumer.go`, `source_test.go`.
4. Implement the `consumer.Source` interface.
5. Register in `internal/sources/registry.go`.
6. Add golden-file fixtures in `test/fixtures/external/<venue>/`.
7. Add an ADR if the venue has unusual constraints (e.g. requires
   paid tier, or has licensing restrictions on redistribution).

### "Add a new on-chain Soroban DEX"

Same five-file convention. Template PR: look at how Soroswap was
added (`internal/sources/soroswap/`). Differences per DEX usually
boil down to event topic shape + amount-decoding quirks.

### "Investigate a price divergence"

Start at [docs/operations/runbooks/price-divergence.md](docs/operations/runbooks/price-divergence.md).
Aggregator-layer alerts (silent / outlier-storm / class-drop-spike)
have their own runbooks under the same directory; see
[docs/architecture/aggregation-plan.md](docs/architecture/aggregation-plan.md)
for the policy chain that drives them.

### "Find why a metric is alerting"

Every alert references a runbook at `docs/operations/runbooks/<alert-name>.md`.
If it doesn't, that's a CI failure.

### "Change the OpenAPI spec"

1. Edit `openapi/rates-engine.v1.yaml`.
2. `make docs-api` regenerates reference docs.
3. Handlers in `internal/api/v1/` get updated; contract tests
   verify they match.
4. Bump the API minor version if the change is additive, major
   if breaking.
5. CHANGELOG entry under `[Unreleased]`.

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
- **Security issue:** `security@ratesengine.net` — do not open
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
(`cmd/ratesengine-indexer/main.go`, `internal/config/*`,
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
