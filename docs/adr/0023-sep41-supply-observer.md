---
adr: 0023
title: SEP-41 supply observer — mint / burn / clawback event-stream tracking
status: Accepted
date: 2026-04-30
supersedes: []
superseded_by: null
---

# ADR-0023: SEP-41 supply observer — mint / burn / clawback event-stream tracking

## Context

[ADR-0011](0011-supply-algorithm.md) Algorithm 3 derives supply
for SEP-41 Soroban tokens from a running event-sum:

```
total_supply       = Σ mint − Σ burn − Σ clawback   (over the contract's lifetime)
circulating_supply = total − admin_balance          (operator-policy)
```

Unlike Algorithm 1 (XLM, frozen total + reserve exclusion) and
Algorithm 2 (classic credits, ledger-entry-component sums), this
algorithm doesn't read state — it accumulates events. Per
ADR-0021 the producer pattern is established for ledger-entry
deltas; for events we already have the
[`internal/canonical/discovery`](../../internal/canonical/discovery)
sniffer that classifies SEP-41-shaped topics. But discovery
*records* contract ids; it doesn't *aggregate* the per-event
amounts into supply totals.

Algorithm 3's piece is an **event-amount aggregator**: every
mint event adds to total, every burn/clawback subtracts. Per
[ADR-0003](0003-i128-no-truncation.md) the amounts are i128 —
NUMERIC end-to-end.

The data already flows through the dispatcher's `Decoder` hook
([dispatcher.go](../../internal/dispatcher/dispatcher.go))
because every SEP-41 contract emits the same standard topics.
What's missing is a per-contract decoder that:

1. Filters topics to `mint` / `burn` / `clawback`,
2. Extracts the amount via `scval.AsAmountFromI128`,
3. Persists the per-event delta into a `sep41_supply_events`
   hypertable,
4. Surfaces a per-contract running-sum reader for the
   `SEP41Computer` (stub in
   [`internal/supply/sep41.go`](../../internal/supply/sep41.go))
   to consume.

## Decision

Ship a four-PR sequence under `internal/sources/sep41_supply/`
+ `internal/supply/sep41_storage_reader.go`, mirroring ADR-0022's
classic-supply pattern.

### Why an event-stream observer, not a ledger-entry observer

SEP-41 contract storage uses `DataKey::Balance(Address) → i128`
per holder (the SAC observer in #306 reads this for classic SAC
balances). The total supply is NOT stored as a single value —
the total emerges from `Σ holder_balance`. We could track it via
a sum query against `sac_balance_observations` (analogous to
Algorithm 2's trustline component), BUT:

- That's the SAC view only — pure SEP-41 contracts that aren't
  classic-asset wrappers store balances under their own contract
  id, not in a shared SAC hypertable. (We don't have a generic
  SEP-41 ContractData observer.)
- Even for SAC-backed classics, summing all holder balances is
  expensive vs. tracking a running event-sum.
- The mint/burn/clawback events ARE the audit trail — every
  state-change is announced as an event, by SEP-41 spec.

The event-sum approach is canonical, cheap, and works for both
classic-SAC and pure-SEP-41 cases.

### Watched-set scope

Operator-curated, like Algorithms 1+2. New
`[supply] watched_sep41_contracts` (C-strkey list). Each entry
identifies a SEP-41 contract whose events we aggregate into a
supply.AssetKey row keyed on the bare contract id (per
`supply.AssetKey` for SEP-41 in
[`internal/supply/key.go`](../../internal/supply/key.go)).

Switching to "watch every SEP-41 contract auto-discovered via
the existing `discovered_assets` hook" is a follow-up — once the
discovery sink stabilises in production, the operator config
becomes "watch all + per-contract opt-out" rather than the
explicit allowlist below.

### Event-decoder surface

`internal/sources/sep41_supply/Decoder` implements
[dispatcher.Decoder] (the events-based hook, NOT
LedgerEntryChangeDecoder — events are different from ledger-
entry deltas). Match: `(contract_id ∈ watched_set) AND
(topic[0].symbol ∈ {"mint", "burn", "clawback"})`. The Sniffer
in `internal/canonical/discovery` already has the
[classifySymbol] helper — reuse it.

Decode body extraction:

| Event     | Topic shape                       | Body shape         |
|-----------|-----------------------------------|--------------------|
| `mint`    | `["mint", admin, to]`             | `i128` (amount)    |
| `burn`    | `["burn", from]`                  | `i128` (amount)    |
| `clawback`| `["clawback", admin, from]`       | `i128` (amount)    |

(Verified against the SEP-41 spec; Soroban token contracts emit
these shapes consistently — see the
[`stellar-tokens`](https://github.com/stellar/stellar-tokens)
reference token contract for the canonical impl.)

### Storage shape

```sql
-- 0015_create_sep41_supply_events.up.sql
sep41_supply_events (
    contract_id   text NOT NULL,    -- SEP-41 contract C-strkey
    ledger        integer NOT NULL,
    tx_hash       char(64) NOT NULL,
    op_index      integer NOT NULL,
    observed_at   timestamptz NOT NULL,

    -- One of 'mint', 'burn', 'clawback'.
    event_kind    text NOT NULL CHECK (event_kind IN ('mint', 'burn', 'clawback')),

    -- The amount, as a NUMERIC. Always non-negative; the kind
    -- discriminates direction (mint is +, burn/clawback are −).
    amount        numeric NOT NULL CHECK (amount >= 0),

    -- The counterparty — for mint, the recipient; for burn/clawback,
    -- the holder whose balance is reduced. NULL for events with
    -- no counterparty (none today; reserved for future variants).
    counterparty  text,

    ingested_at   timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (contract_id, ledger, tx_hash, op_index, observed_at)
);
```

Hypertable on `observed_at`, 7-day chunks per the family
convention.

### Storage methods

```go
// Insert one event row, idempotent on PK.
Store.InsertSEP41SupplyEvent(ctx, ev SEP41SupplyEvent) error

// Sum mint − Σ(burn + clawback) for one contract at-or-before
// the supplied ledger.
Store.SEP41NetMintAtOrBefore(ctx, contractID string, asOfLedger uint32) (*big.Int, error)
```

### Reader composition

```go
// internal/supply/sep41_storage_reader.go
type StorageSEP41SupplyReader struct {
    store SEP41SupplyStore
}

// Implements supply.SEP41SupplyReader (the stub interface
// already in internal/supply/sep41.go).
func (r *StorageSEP41SupplyReader) SEP41SupplyAt(
    ctx context.Context, contractID string, locked LockedSet, ledger uint32,
) (Supply, error) {
    netMint := store.SEP41NetMintAtOrBefore(...)
    // total = netMint
    // admin_balance & locked-set are operator-policy: subtract via
    //   sac_balance_observations.SACBalanceForContractAtOrBefore(...)
    // for each entry in locked.Contracts (the SEP-41 admin is a
    // contract or account in the holder column).
    // circulating = total − admin_balance − Σ locked
}
```

### Aggregator integration

`buildSupplyRefreshers` in `cmd/ratesengine-aggregator/main.go`
(extended in #307) gains a third per-asset loop — one
[`supply.Refresher`](../../internal/supply/refresher.go)
goroutine per watched SEP-41 contract, alongside the existing
XLM + per-classic refreshers. Same per-tick outcome counter
(`ratesengine_aggregator_supply_refresh_total{outcome}`).

## Implementation plan (PRs)

| PR  | Scope                                                                  | Size estimate |
|-----|------------------------------------------------------------------------|---------------|
| 1/4 | Migration 0015 (`sep41_supply_events`) + `Insert*` + `Sum*` storage methods | ~300 LOC |
| 2/4 | `internal/sources/sep41_supply/` decoder + sink wiring | ~400 LOC |
| 3/4 | `StorageSEP41SupplyReader` + `[supply] watched_sep41_contracts` config | ~250 LOC |
| 4/4 | Aggregator wiring — third per-asset loop in `buildSupplyRefreshers`; closes Task #56 | ~150 LOC |

## Consequences

- One new hypertable. Per-event row volume is low — a busy SEP-41
  contract emits a handful of mint/burn/clawback events per day,
  vs hundreds of transfers. Watched-set restriction keeps growth
  bounded.
- The existing `Decoder` hook gains a fifth source. No
  `ProcessLedger` changes needed — the dispatcher already routes
  every event through every registered Decoder.
- The `dispatcher.Decoder.Matches` first-match-wins contract
  means the SEP-41 supply observer must NOT match topics other
  decoders care about. `mint` / `burn` / `clawback` are SEP-41-
  specific so this is naturally clean; the discovery sink runs
  in parallel via a different code path.
- Algorithm 3's `total = Σ mint − Σ burn − Σ clawback` ignores
  the `transfer` event entirely. Transfers don't move supply,
  only ownership; per ADR-0011 the running sum doesn't track
  them.
- Once shipped, `/v1/assets/{id}` for SEP-41 contracts populates
  total/circulating via the same F2-fields path the XLM + classic
  cases use. Three-domain supply coverage closes ADR-0011.

## References

- Task #56: SEP-41 Soroban supply computer (the implementation
  work this ADR bounds).
- ADR-0011: Three-domain supply algorithm (Algorithm 3 spec).
- ADR-0021: AccountEntry observer (the LCM-observer pattern).
- ADR-0022: Classic-supply observers (the four-observer pattern
  this ADR's structure mirrors but at the event-stream level).
- ADR-0003: i128 / u128 never truncates.
- `internal/canonical/discovery/sniffer.go`: existing SEP-41
  topic-classifier; reuse the `classifySymbol` helper.
- `internal/supply/sep41.go`: existing `SEP41Computer` stub.
