---
adr: 0021
title: AccountEntry observer — live home-domain + reserve-balance tracking
status: Accepted
date: 2026-04-30
supersedes: []
superseded_by: null
---

# ADR-0021: AccountEntry observer — live home-domain + reserve-balance tracking

## Context

Two operator-static config knobs in the codebase are placeholders
for live data we don't currently index:

1. **`metadata.issuer_home_domains`** —
   ([`internal/config/config.go`](../../internal/config/config.go))
   A G-strkey → home-domain map populated by hand. The struct
   docstring explicitly notes: "AccountEntry.HomeDomain isn't
   currently indexed in our trades hypertable; deriving it would
   require either a separate account-entry observer in the indexer
   (deferred) or a per-request stellar-rpc lookup (latency hit on
   the hot path)."

2. **`supply.reserve_balances_stroops`** —
   ([`internal/config/config.go`](../../internal/config/config.go),
   shipped in #285) A G-strkey → stroop-balance map operators
   update by hand whenever SDF announces a reserve move. The
   `ConfigReserveBalanceReader` docstring marks it as the interim
   implementation pending an LCM-derived live reader (Task #54).

Both gaps have the same root cause: we don't currently observe
`AccountEntry` ledger-entry changes during ingestion. Every
LedgerCloseMeta XDR carries the deltas (`AccountEntry` rows
created / updated / removed per tx), but the dispatcher's three
existing hooks (`Decoder`, `OpDecoder`, `ContractCallDecoder`)
all operate on transaction-level artifacts (events, ops,
contract calls) — none observe ledger-entry deltas directly.

Per ADR-0001 (Horizon-not-in-our-architecture) we cannot fall
back to Horizon's pre-computed `accounts` table. Per the
"stellar-rpc not in production ingest path" rule (CLAUDE.md
"Things that will surprise you"), we cannot fall back to
per-request RPC. The path forward is in-house observation from
the LCM stream we already consume.

The `wasm-history` walker
([`cmd/ratesengine-ops/main.go::scanLCMForWasmChanges`](../../cmd/ratesengine-ops/main.go))
proves the technique works: it iterates
`LedgerEntryChange` rows from `tx.Operations[].Changes` and the
fee-meta block, filters to `LedgerEntryDataType == ContractCode`,
and tracks per-contract WASM-hash transitions. The AccountEntry
observer is the same technique with a different filter and a
different sink.

## Decision

Add a fourth dispatcher hook —
`LedgerEntryChangeDecoder` — and ship a single canonical decoder
implementing it: `internal/sources/accounts.AccountEntryObserver`.
Persist observations to a new `account_observations` hypertable.
Surface them via two readers consuming the same table:

- `metadata.LCMHomeDomainResolver` (replaces operator-static
  `issuer_home_domains` → live home-domain lookup).
- `supply.LCMReserveBalanceReader` (replaces operator-static
  `reserve_balances_stroops` → live reserve-balance reads).

### Hook interface

```go
// LedgerEntryChangeDecoder observes raw LedgerEntryChange deltas
// from each LCM, regardless of which transaction or fee-meta
// block produced them. Used for sources that derive their state
// from ledger-entry changes rather than events/ops/calls.
type LedgerEntryChangeDecoder interface {
    Name() string
    // Matches reports whether this decoder owns the given change
    // type. Cheap pre-filter — typically checks the entry's Data
    // discriminant (AccountEntry vs Trustline vs ContractCode etc.).
    Matches(change xdr.LedgerEntryChange) bool
    // Decode emits zero or more canonical outputs for one change.
    Decode(ctx LedgerEntryChangeContext) ([]consumer.Event, error)
}

type LedgerEntryChangeContext struct {
    Ledger   uint32
    ClosedAt time.Time
    TxHash   string  // empty for fee-meta-block changes
    OpIndex  int     // -1 for fee-meta-block changes
    Change   xdr.LedgerEntryChange
}
```

Same non-fatal-error contract as the other three hooks: returning
an error is a "skip + count" signal, not "stop dispatching."

### `account_observations` hypertable

```sql
CREATE TABLE account_observations (
    account_id      TEXT       NOT NULL,        -- G-strkey
    ledger          INTEGER    NOT NULL,
    observed_at     TIMESTAMPTZ NOT NULL,        -- ledger close time
    balance_stroops NUMERIC    NOT NULL,         -- native XLM balance
    home_domain     TEXT,                        -- AccountEntry.HomeDomain (NULLable)
    flags           INTEGER    NOT NULL,         -- AccountFlags bitmask
    seq_num         BIGINT     NOT NULL,         -- AccountEntry.SeqNum
    PRIMARY KEY (account_id, ledger)
);

SELECT create_hypertable('account_observations', 'observed_at',
                          chunk_time_interval => INTERVAL '7 days');

CREATE INDEX account_observations_account_observed_idx
    ON account_observations (account_id, observed_at DESC);
```

Schema rationale:

- **Per-(account, ledger) row** — a single account that's touched
  in many ledgers within a chunk window writes many rows. The
  observer dedupes within a tx (one row per leaf change) but does
  not coalesce across ledgers; the readers query
  `ORDER BY observed_at DESC LIMIT 1` to get the latest.
- **`balance_stroops` as NUMERIC** — XLM amounts are i64 in XDR
  but ADR-0003 mandates NUMERIC end-to-end for consistency and
  future-proofing if Stellar ever migrates to wider amount types.
- **`home_domain` nullable** — many accounts have no home_domain.
  NULL is the correct representation (vs empty string).
- **`flags` + `seq_num` carried** — operationally useful and
  cheap to capture. `flags` lets us spot accounts that have been
  authorized for a SEP-1 issuer; `seq_num` is a cross-check for
  ordering when an account is touched multiple times in a single
  ledger.

### Backfill semantics

Same path as every other source: an operator runs
`ratesengine-ops backfill -source accounts -from N -to M` to
replay an LCM range. The dispatcher's existing range-walker
delivers `LedgerEntryChange` rows in chronological order; the
observer's `Decode` writes one row per matched change.

The `Insert` path is `ON CONFLICT (account_id, ledger) DO NOTHING`
— a backfill that re-walks an already-observed range is idempotent
(the observation for a given (account, ledger) is deterministic
from XDR; re-deriving it would write the same value).

### Reader contracts

#### `metadata.LCMHomeDomainResolver`

```go
type LCMHomeDomainResolver struct {
    db *sql.DB
}

func (r *LCMHomeDomainResolver) HomeDomainFor(ctx context.Context, issuer string) (string, bool, error) {
    // Reads the most-recent home_domain for the issuer's G-strkey.
    // Returns "", false, nil when not observed yet (caller falls
    // back to operator-static map then defaults).
    // Returns "", false, nil when home_domain is NULL — issuer
    // exists but has no domain set.
}
```

The existing `metadata.MetadataConfig.HomeDomainFor` becomes the
fallback when `LCMHomeDomainResolver` returns `(_, false, nil)`.
Operators can keep entries in `[metadata.issuer_home_domains]`
to override the live value or to seed before the observer
backfill catches up.

#### `supply.LCMReserveBalanceReader`

```go
type LCMReserveBalanceReader struct {
    db *sql.DB
}

func (r *LCMReserveBalanceReader) ReserveBalanceTotal(
    ctx context.Context, accounts []string, ledger uint32,
) (*big.Int, error) {
    // For each account in `accounts`, reads the most-recent
    // balance_stroops at ledger ≤ `ledger`. Sums across accounts.
    // Returns an error if any account has no observation at or
    // before the requested ledger.
}
```

The existing `supply.ConfigReserveBalanceReader` (shipped in
#285) stays in tree as the bootstrap fallback — operators flip
to the LCM reader by changing one line in
`cmd/ratesengine-ops/supply.go::supplySnapshot`. Until the
observer has backfilled the configured reserve accounts to a
deep enough range, the config reader remains the safer choice
(its values are explicitly operator-blessed; the LCM reader
would silently return historical balances if the backfill
lagged).

### Dispatcher integration

```go
// In ProcessLedger:
for _, ed := range tx.OperationChanges() { // helper that walks
                                            // op-meta + fee-meta
    for _, decoder := range d.entryDecoders {
        if !decoder.Matches(ed.Change) { continue }
        events, err := decoder.Decode(ctx)
        // … same metrics + non-fatal-error path as other hooks
    }
}
```

The walker visits both per-op `Changes` and the tx-level
`FeeChanges` block. AccountEntry deltas appear in both —
fee-debit changes the account's XLM balance.

### Why not piggyback on an existing hook

- `Decoder` is event-based; AccountEntry changes don't emit
  events.
- `OpDecoder` operates on classic ops; AccountEntry changes can
  be a side-effect of any op (Payment, ChangeTrust,
  ManageData, …) and inflation, so filtering at op-type level
  doesn't cover the surface.
- `ContractCallDecoder` is for Soroban; AccountEntry changes are
  classic-state.

A single observer plugged into the new fourth hook is the right
shape.

### Why one canonical decoder, not per-source

Soroswap / Phoenix / Aquarius / Reflector / Band / Redstone all
need to emit canonical events; per-source decoders make sense.
AccountEntry observation has exactly one shape across the
network: read the entry, write to the table. Multiple decoders
would all do the same thing. Ship one canonical observer in
`internal/sources/accounts/` and let operator config drive which
accounts are watched (via the existing
`[supply] sdf_reserve_accounts` and a new
`[metadata] watched_issuers` knob).

## Consequences

- New hypertable migration (0010 — first migration after the
  blend_auctions migration shipped in #274).
- New `internal/sources/accounts` package with the observer +
  storage writer.
- New dispatcher hook (`LedgerEntryChangeDecoder`) +
  `Dispatcher.AddEntryDecoder` registration.
- New readers in `internal/metadata/` and `internal/supply/`.
- Drive-by migration: `cmd/ratesengine-ops backfill` learns a
  `-source accounts` flag.
- `ConfigReserveBalanceReader` stays as the bootstrap fallback
  with a clearer comment pointing at this ADR. Operator-config-
  managed reserve balances remain valid until live data has
  caught up.
- Task #57 (periodic supply-snapshot worker in aggregator)
  becomes implementable — once the LCM reader is live, the
  aggregator can refresh supply snapshots per tick rather than
  per cron-fire.
- The observer is operator-watched-set-driven by default to keep
  the table small. Switching to "watch every account" is a
  config change, but the table size implications (50M+ accounts
  × N observations each) need a separate ADR before we'd default
  it on.

## References

- Task #54: LCM-AccountEntry observer (the implementation work
  this ADR bounds).
- ADR-0001: Horizon-not-in-our-architecture.
- ADR-0003: i128 / u128 never truncates.
- ADR-0011: Supply algorithm (sets the reserve-exclusion
  invariant Algorithm 1 needs the live reader for).
- PR #285: ships `ConfigReserveBalanceReader` as the interim
  implementation this ADR replaces.
- `cmd/ratesengine-ops/main.go::scanLCMForWasmChanges`:
  reference implementation of the LedgerEntryChange iteration
  pattern.
