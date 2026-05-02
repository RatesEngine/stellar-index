---
title: Supply pipeline — three-algorithm derivation, per-asset refresh
last_verified: 2026-05-02
status: binding
---

# Supply pipeline

**Every supply value on `/v1/assets/{id}` flows through one path,
parameterised by one of three algorithms keyed on the asset class:**

```
operator config: [supply] sdf_reserve_accounts /
                          watched_classic_assets /
                          watched_sep41_contracts /
                          sac_wrappers
                                │
                                ▼
                     one supply.Refresher per asset
                                │
                                ▼
                  (Algorithm 1)  (Algorithm 2)  (Algorithm 3)
                  XLMComputer    ClassicComputer SEP41Computer
                  ▼              ▼               ▼
                  (reads)        (reads)         (reads)
   ┌──────────────────────┐  ┌────────────────┐ ┌─────────────────┐
   │ account_observations │  │ trustline_obs  │ │ sep41_supply_   │
   │  (XLM balances of    │  │ claimable_obs  │ │  events         │
   │   SDF reserves)      │  │ lp_reserve_obs │ │  (mint / burn / │
   │                      │  │ sac_balance_obs│ │   clawback)     │
   └──────────────────────┘  └────────────────┘ └─────────────────┘
                                │
                                ▼
                     supply.Supply struct
                                │
                                ▼
                     Store.InsertSupply
                                │
                                ▼
                  asset_supply_history (hypertable)
                                │
                                ▼
                     Store.LatestSupply
                                │
                                ▼
                     /v1/assets/{id} F2 fields:
                       total_supply
                       circulating_supply
                       max_supply
                       market_cap_usd  (× current price)
                       fdv_usd         (× current price)
                       supply_basis
```

## The three algorithms (per ADR-0011)

| Algorithm | Asset class       | Total derivation                              | ADR        |
|-----------|-------------------|-----------------------------------------------|------------|
| 1         | Native XLM        | frozen 50,001,806,812 × 10⁷ stroops          | ADR-0011 §1 |
| 2         | Classic credit    | Σ trustline + Σ claimable + Σ LP + Σ SAC     | ADR-0011 §2 |
| 3         | SEP-41 Soroban    | Σ mint − Σ burn − Σ clawback over lifetime   | ADR-0011 §3 |

**Circulating** (per ADR-0011) is `total − issuer/admin balance −
Σ operator-locked-set balances` for all three. The locked-set is
operator-curated via `supply.Policy.PerAsset`.

**Max supply** is `total` for hard-capped assets (XLM), nil
otherwise unless the operator supplies an override or a SEP-1
declaration overlay populates it.

## The six observers

Every component the algorithms read is sourced from one of six
LCM-stream observers. Each plugs into the dispatcher's hooks
without changing dispatcher source — they're pure additive
sources per the ingest-pipeline contract:

| Observer | Hook | Watched-set config | Backs |
|----------|------|--------------------|-------|
| `internal/sources/accounts` | `LedgerEntryChangeDecoder` | `[supply] sdf_reserve_accounts` (XLM) + per-issuer for metadata | Algorithm 1 + metadata overlay |
| `internal/sources/trustlines` | `LedgerEntryChangeDecoder` | `[supply] watched_classic_assets` | Algorithm 2 trustline component |
| `internal/sources/claimable_balances` | `LedgerEntryChangeDecoder` | `[supply] watched_classic_assets` | Algorithm 2 claimable component |
| `internal/sources/liquidity_pools` | `LedgerEntryChangeDecoder` | `[supply] watched_classic_assets` | Algorithm 2 LP-reserve component |
| `internal/sources/sac_balances` | `LedgerEntryChangeDecoder` | `[supply.sac_wrappers]` (contract→asset_key map) | Algorithm 2 SAC component + Algorithm 3 locked-set lookups |
| `internal/sources/sep41_supply` | `Decoder` (events) | `[supply] watched_sep41_contracts` | Algorithm 3 mint/burn/clawback running sum |

The first five are LCM ledger-entry observers (ADR-0021 +
ADR-0022). The sixth is an event-stream observer (ADR-0023) — it
classifies topics and accumulates amounts rather than reading
state.

All six observers are now wired into the indexer's dispatcher
(L2.12a closed via PRs #411 / #412 / #413). Registration is
opt-in per the corresponding `[supply]` watched-set —
`pipeline.RegisterSupplyEntryDecoders` handles the five
`LedgerEntryChangeDecoder`s (accounts / trustlines /
claimable_balances / liquidity_pools / sac_balances) keyed off
`sdf_reserve_accounts` / `watched_classic_assets` /
`[supply.sac_wrappers]`, and `pipeline.RegisterSupplyEventDecoders`
attaches sep41_supply when `watched_sep41_contracts` is non-empty.
Empty watched-set → observer skipped → no behaviour change. With
any watched-set populated, the corresponding hypertable starts
filling on every matching ledger close.

## The chained-fallback reader pattern

Per ADR-0021, the supply readers compose a "live LCM-derived
reader" with an "operator-static fallback" so the system works
during observer bootstrap:

```
supply.Refresher.Tick()
    │
    ▼
supply.<Algorithm>Computer.Compute(asset, ledger, observedAt)
    │
    ▼
supply.<Algorithm>SupplyReader.Read(asset, locked, ledger)
    │
    ▼
chain reader:
    1. try live: query account_observations / trustline_observations / etc.
    2. on ErrNoObservation: fall through to operator-static config
       (reserve_balances_stroops / per-asset locked-set / etc.)
    3. otherwise: bubble error
```

For Algorithm 1 (XLM) specifically: `supplyAggregatorChainReader`
in `cmd/ratesengine-aggregator/main.go` wraps
`supply.LCMReserveBalanceReader` (live) with
`supply.ConfigReserveBalanceReader` (static). When the
AccountEntry observer hasn't backfilled the SDF reserves yet,
the static config produces the answer; once the observer covers
the live set, the static config can be left stale (the live
reader wins).

For Algorithms 2 + 3: similar pattern, but the static fallback
is per-component (operators populate
`reserve_balances_stroops` for XLM analogues; they DON'T
typically maintain manual trustline-component snapshots, so the
classic / SEP-41 paths require the observer to be backfilled).

## Two refresh paths (operator choice)

Per ADR-0011 / ADR-0021 / Task #57, operators have two paths to
write `asset_supply_history` rows:

### A. systemd-timer driven

`ratesengine-ops supply snapshot` subcommand, fired by
`deploy/systemd/supply-snapshot.timer` daily at 04:42 UTC. Per
[supply-snapshot runbook](../operations/supply-snapshot.md).

XLM only at v1; the CLI rejects classic + SEP-41 with a "use
the goroutine path" message.

Metrics: `ratesengine_supply_snapshot_*` textfile-emitted via
`internal/supply/textfile.go`. Alerts in
`deploy/monitoring/rules/supply-snapshot.yml`.

### B. Aggregator-resident goroutine

`[supply] aggregator_refresh_enabled = true` runs a
`supply.Refresher` goroutine per watched asset inside
`ratesengine-aggregator`. One goroutine per
`(XLM | classic asset | SEP-41 contract)` on the
`aggregator_refresh_cadence` (default 5m).

Covers all three algorithms. Per-tick outcome counter
`ratesengine_aggregator_supply_refresh_total{asset_key, outcome}`
labels by both asset and outcome so operators can chart per-
asset bootstrap progress + isolate failure modes per asset.
Alerts in `deploy/monitoring/rules/supply-refresh.yml`.

### Choice rules

- Classic + SEP-41 supply requires path B (the CLI doesn't
  support those assets).
- XLM supply works on either path. Path A is simpler (no
  aggregator dependency); path B is preferred when the LCM
  observer has backfilled (per-cadence freshness vs. per-day).

The two paths are mutually exclusive at the operator level —
write idempotency makes a double-fire correctness-safe (the
hypertable's `(asset_key, ledger_sequence)` PK and `ON CONFLICT
DO NOTHING` dedupe), but operators should disable one when
flipping to the other to avoid redundant work.

## Cross-check between Algorithm 2 and Algorithm 3

A SAC-wrapped classic asset's supply is observable two ways: as a
classic credit (Algorithm 2 — sums trustline + claimable + LP-reserve
+ SAC-wrapped contract balances) and as a SEP-41 token (Algorithm 3 —
sums mint − burn − clawback events on the SAC contract). Per ADR-0011
the two MUST agree within 1 stroop because both observe the same
underlying ledger state through different lenses. Disagreement beyond
the float-rounding tolerance signals indexer corruption upstream.

The aggregator's `supply.CrossCheckRefresher`
(`internal/supply/crosscheck_refresher.go`, wired in
`cmd/ratesengine-aggregator/main.go::buildCrossCheckRefresher`) ticks
on the same `aggregator_refresh_cadence` as the per-asset supply
refreshers above. Pairs are derived at boot from the ∩ of:

- `[supply].sac_wrappers` (operator-declared classic↔SAC mapping)
- `[supply].watched_classic_assets` (Algorithm 2 watched-set)
- `[supply].watched_sep41_contracts` (Algorithm 3 watched-set)

Per tick, for each pair the refresher reads the latest snapshot for
both the classic and the SAC sides via `Store.LatestSupply`, runs
`supply.CrossCheck`, and emits:

- `ratesengine_supply_cross_check_divergence_stroops{classic_key}` —
  gauge holding the absolute stroop divergence on within/over outcomes.
- `ratesengine_supply_cross_check_total{outcome}` — counter labelled
  by `within | over | missing_snapshot | read_error`.

The supply.yml alert (`ratesengine_supply_cross_check_divergence`)
fires when the gauge stays > 1 for ≥ 5 min. Runbook:
[`supply-cross-check-divergence`](../operations/runbooks/supply-cross-check-divergence.md).

Empty pair-set is a no-op — operators that haven't declared any
SAC-wrapper pairs (e.g. an SEP-41-only deployment with no classic
side) get no gauge updates and no alerting noise.

## Per-class storage tables (live-data side)

| Table | Migration | Identity | Holders columns |
|-------|-----------|----------|-----------------|
| `asset_supply_history` | 0005 | `(asset_key, ledger_sequence)` | total / circulating / max / basis |
| `account_observations` | 0010 | `(account_id, ledger, observed_at)` | balance_stroops / home_domain / flags / seq_num / is_removal |
| `trustline_observations` | 0011 | `(account_id, asset_key, ledger, observed_at)` | balance_stroops / is_removal |
| `claimable_observations` | 0012 | `(claimable_id, ledger, observed_at)` | asset_key / balance_stroops / is_removal |
| `lp_reserve_observations` | 0013 | `(pool_id, asset_key, ledger, observed_at)` | balance_stroops / is_removal |
| `sac_balance_observations` | 0014 | `(contract_id, holder, ledger, observed_at)` | asset_key / balance_stroops / is_removal |
| `sep41_supply_events` | 0015 | `(contract_id, ledger, tx_hash, op_index, observed_at)` | event_kind / amount / counterparty |

All hypertables on `observed_at`, 7-day chunks, compression
segment-by the most-common reader-query column. PK convention
drags `observed_at` into the key per Timescale's partition-
column-in-PK rule.

## Reader contracts

Each algorithm has a `<X>SupplyReader` interface in
`internal/supply/`; the production impl is `Storage<X>SupplyReader`
composing the storage primitives:

| Reader | Composes |
|--------|----------|
| `XLMComputer.reader` (`ReserveBalanceReader`) | `LCMReserveBalanceReader` (account_observations) + `ConfigReserveBalanceReader` (operator-static) |
| `StorageClassicSupplyReader` | 4 × `Sum*BalancesAtOrBefore` + 2 × per-entity lookups (`TrustlineBalanceForAccountAtOrBefore`, `SACBalanceForContractAtOrBefore`) |
| `StorageSEP41SupplyReader` | `SEP41KindTotalsAtOrBefore` + `SACBalanceForContractAtOrBefore` (for locked-set lookups via shared SAC observer storage) |

Each reader returns a `<X>SupplyComponents` struct that the
matching `<X>Computer` reduces to a `Supply` snapshot.

## API surface

`/v1/assets/{id}` reads from `asset_supply_history` via
`Store.LatestSupply`; the F2 fields (`total_supply` /
`circulating_supply` / `max_supply` / `market_cap_usd` /
`fdv_usd` / `supply_basis`) populate when a row exists, stay
JSON null when no snapshot has been written (per ADR-0011 "we
don't fabricate"). The handler does NOT consult observer state
directly — the snapshot table is the API source of truth.

## Failure modes (per outcome label)

The aggregator-refresh metric labels each tick with one of:

| Outcome | Means | Operator action |
|---------|-------|-----------------|
| `ok` | Snapshot written | none — steady state |
| `no_ledger` | `ListCursors` returned no max_ledger | wait for indexer's first cursor; check ingestion is alive |
| `no_observation` | Live reader has no row + static fallback empty | bootstrap window — wait for backfill OR populate static config |
| `compute_error` | Algorithm returned non-OK (e.g. negative SEP-41 total) | code bug or operator config drift; check logs + roll back if recent deploy |
| `write_error` | `InsertSupply` failed | storage layer down; route to `pg-conns-saturated` runbook |

Sustained non-`ok` for ≥ 30 min triggers
`ratesengine_aggregator_supply_refresh_error_dominant`; no `ok`
in ≥ 30 min triggers `_stalled`.

The cross-check refresher emits its own per-outcome counter:

| Outcome | Means | Operator action |
|---------|-------|-----------------|
| `within` | Both snapshots loaded; divergence ≤ 1 stroop | none — steady state |
| `over` | Both snapshots loaded; divergence > 1 stroop | follow `supply-cross-check-divergence` runbook |
| `missing_snapshot` | One/both sides have no row in `asset_supply_history` yet | bootstrap window — no action unless sustained past first refresh of each side |
| `read_error` | Transient storage read failure | check `pg-conns-saturated` / `timescale-primary-down` runbooks |

Bootstrap-state (`missing_snapshot`) is intentionally NOT escalated
— it's the normal state during first-tick warmup and the first
moments after a new operator-watched asset is added. Sustained
`read_error` would surface via the same storage-layer alerts the
per-asset refreshers ride.

## ADR map

- [ADR-0011](../adr/0011-supply-algorithm.md) — three-algorithm
  spec
- [ADR-0021](../adr/0021-account-entry-observer.md) —
  AccountEntry observer for live home-domain + reserve balances
- [ADR-0022](../adr/0022-classic-supply-observers.md) —
  Trustline / Claimable / LP / SAC observers
- [ADR-0023](../adr/0023-sep41-supply-observer.md) — SEP-41
  supply event observer
- [ADR-0003](../adr/0003-i128-no-truncate.md) — i128 / NUMERIC
  end-to-end (every amount in this pipeline)
- [ADR-0006](../adr/0006-timescale-storage.md) — TimescaleDB
  storage, the hypertable convention
- [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) —
  why the API serves CLOSED snapshots only

## Repo map

```
internal/sources/{accounts,trustlines,claimable_balances,liquidity_pools,sac_balances,sep41_supply}/
        ↓ (LedgerEntryChange or events.Event hooks)
internal/dispatcher/             (4 hooks: Decoder, OpDecoder, ContractCallDecoder, LedgerEntryChangeDecoder)
        ↓ (consumer.Event)
internal/pipeline/sink.go        (type-switch routing)
        ↓
internal/storage/timescale/      (Insert{Supply, AccountObservation, TrustlineObservation, …}, Sum*, Latest*)
        ↓
internal/supply/                 (XLMComputer, ClassicComputer, SEP41Computer, Refresher, CrossCheckRefresher, chained readers)
        ↓
cmd/ratesengine-aggregator/      (buildSupplyRefreshers + buildCrossCheckRefresher; runSupplyRefresh + runCrossCheckRefresh — one goroutine per asset, plus one for cross-check)
        ↓
internal/api/v1/assets_f2.go     (populateMarketCap, F2 field rendering)
        ↓
GET /v1/assets/{id}              (asset_supply_history via Store.LatestSupply)
```

## When to update this doc

Add a row, update a table, or extend the diagram when:

- A new algorithm class lands (no current candidates; the
  three above cover all on-chain Stellar supply types).
- A new observer plugs in (e.g. operator-watched-set expansion
  to issuer accounts triggering SEP-1 metadata refresh).
- A new operator-config knob materially changes the data flow.
- An ADR in the ADR map above supersedes another.

The matrix in `coverage-matrix.md` is the row-by-row tracker;
this doc is the architecture-level overview.
