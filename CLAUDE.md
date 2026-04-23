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
- **Redstone Adapter DOES emit events** (topic `"REDSTONE"`) — one
  per batch push containing all updated feeds. Subscribe rather
  than poll all 19 per-feed contracts. →
  [docs/discovery/oracles/redstone.md](docs/discovery/oracles/redstone.md)
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

---

## Common task recipes

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

Start at [docs/operations/runbooks/price-divergence.md](docs/operations/runbooks/price-divergence.md)
(TBD — runbook lands with the aggregation layer).

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

_This file is hand-maintained. If you find a fact here that is no
longer true, update this file in the same PR as the change that
invalidated it. Freshness checked in CI._
