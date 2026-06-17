---
adr: 0022
title: Classic-supply observers — Trustline / ClaimableBalance / LiquidityPool / ContractData entry tracking
status: Accepted
date: 2026-04-30
supersedes: []
superseded_by: null
---

# ADR-0022: Classic-supply observers — Trustline / ClaimableBalance / LiquidityPool / ContractData entry tracking

## Context

[ADR-0011](0011-supply-algorithm.md) Algorithm 2 derives classic
credit-asset supply from four ledger-entry-domain components:

```
total_supply       = Σ trustline + Σ claimable + Σ LP-reserve + Σ SAC-wrapped
circulating_supply = total − issuer_balance − Σ locked-set
```

The algorithm itself is implemented in
`internal/supply/classic.go::ClassicComputer` (since #199, fully
covered by tests). What's missing is the production
`ClassicSupplyReader` — the storage-side primitive that returns
the four component sums for one (asset, ledger) pair.

[ADR-0021](0021-account-entry-observer.md) shipped the pattern for
one ledger-entry-domain observer (`AccountEntry`, watched-set
driven). The four classic-supply components extend the pattern to
the remaining entry types we need:

| Component  | XDR entry type                  | Observer kind                |
|------------|---------------------------------|------------------------------|
| Trustline  | `LedgerEntryTypeTrustline`      | per-(account, asset)         |
| Claimable  | `LedgerEntryTypeClaimableBalance` | per-claimable-balance-id    |
| LP-reserve | `LedgerEntryTypeLiquidityPool`  | per-pool, per-asset-side     |
| SAC-wrapped| `LedgerEntryTypeContractData`   | per-(SAC contract, holder)   |

The four observers share the dispatcher hook from #297
(`LedgerEntryChangeDecoder`); they differ in the hypertable shape
they write to and the reader query they back.

## Decision

Ship four observer + storage + reader stacks under
`internal/sources/`, each mirroring the AccountEntry pattern.
Compose into a single `internal/supply/StorageClassicSupplyReader`
that satisfies `ClassicSupplyReader` by querying all four tables.

### Per-component scope

**Watched-set driven, NOT global.** Per ADR-0021's table-size
note, indexing every classic-asset trustline / claimable / LP /
SAC entry network-wide is a non-trivial scaling decision. The
v1 scope tracks operator-curated assets — operators list the
classic assets they want supply data for in `[supply] watched_classic_assets`,
and the observers filter per-event by whether the entry's asset
matches that list.

Switching to "watch every classic asset" is a separate ADR;
table sizes (Stellar has ~100M trustlines today) need their own
storage strategy.

### Schema family

One hypertable per component. Shared shape rules:

- Hypertable on `observed_at`, 7-day chunks (matches
  `account_observations` shape).
- PK `(<entry-id>, ledger, observed_at)` — the partition column
  drags into the PK for Timescale's hypertable rule.
- `balance_stroops NUMERIC NOT NULL` per ADR-0003.
- `is_removal BOOLEAN NOT NULL DEFAULT false` for the
  Removed-variant case.
- `ingested_at TIMESTAMPTZ NOT NULL DEFAULT now()` for ops
  trace.

Per-component identity columns:

```sql
-- 0011_create_trustline_observations.up.sql
trustline_observations (
    account_id   text NOT NULL,    -- holder G-strkey
    asset_key    text NOT NULL,    -- supply.AssetKey form
    -- ... shared columns
    PRIMARY KEY (account_id, asset_key, ledger, observed_at)
)

-- 0012_create_claimable_observations.up.sql
claimable_observations (
    claimable_id text NOT NULL,    -- ClaimableBalanceID hex
    asset_key    text NOT NULL,
    -- ... shared columns
    PRIMARY KEY (claimable_id, ledger, observed_at)
)

-- 0013_create_lp_reserve_observations.up.sql
lp_reserve_observations (
    pool_id      text NOT NULL,    -- PoolID hex
    asset_key    text NOT NULL,    -- which side of the pool
    -- ... shared columns
    PRIMARY KEY (pool_id, asset_key, ledger, observed_at)
)

-- 0014_create_sac_balance_observations.up.sql
sac_balance_observations (
    contract_id  text NOT NULL,    -- C-strkey of SAC wrapper
    holder       text NOT NULL,    -- G-strkey or C-strkey
    -- ... shared columns
    PRIMARY KEY (contract_id, holder, ledger, observed_at)
)
```

Migrations land in numerical sequence after #299's
`account_observations` (migration 0010).

### Observer packages

Each component gets its own package:

- `internal/sources/trustlines/`
- `internal/sources/claimable_balances/`
- `internal/sources/liquidity_pools/`
- `internal/sources/sac_balances/`

Same five-file convention as the existing source packages
(`doc.go`, `events.go`, `consumer.go`, `decode.go`,
`dispatcher_adapter.go`) plus tests. Each implements
`dispatcher.LedgerEntryChangeDecoder` from #297; the dispatcher
already routes per-tx ledger-entry changes through every
registered entry decoder so adding the four packages is purely
additive.

`Match` filters by `xdr.LedgerEntryType` discriminant + the
entry's asset (extracted from the `Asset` field on the relevant
XDR struct) against the operator-watched-set.

### Reader composition

```go
// internal/supply/storage_classic_reader.go
type StorageClassicSupplyReader struct {
    trustlines   TrustlineSummer
    claimables   ClaimableSummer
    lpReserves   LPReserveSummer
    sacBalances  SACBalanceSummer
    accounts     AccountObservationLookup // for issuer balance
}

func (r *StorageClassicSupplyReader) ClassicSupplyAt(
    ctx context.Context, asset canonical.Asset,
    locked LockedSet, ledger uint32,
) (ClassicSupplyComponents, error) {
    // Fan out four queries; combine; subtract issuer + locked.
    // Errors from any one tabulate; we do NOT publish a partial sum.
}
```

Each Summer is a small interface satisfied by
`*timescale.Store`. Tests pass in-memory fakes (mirroring
`fakeLookup` in #300).

### Aggregator integration

`cmd/stellarindex-aggregator/main.go::buildSupplyRefresher`
extends to construct a `ClassicComputer` per watched classic
asset alongside the existing `XLMComputer`. The refresher's
existing `Tick` shape covers both — the per-tick goroutine just
iterates the watched-asset list and calls each computer in
turn.

### `[supply] watched_classic_assets` config

```toml
[supply]
sdf_reserve_accounts        = [...]              # XLM (Algorithm 1)
reserve_balances_stroops    = {...}              # XLM static fallback
watched_classic_assets      = [                   # NEW
    "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
    "AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA",
]
```

Validate at config-load: every entry parses as a valid
`canonical.AssetClassic`; no SAC C-strkeys here (those track
under the SAC observer's own watched-set).

## Implementation plan (PRs)

| PR  | Scope                                                                  | Size estimate |
|-----|------------------------------------------------------------------------|---------------|
| 1/5 | Migrations 0011-0014 (four hypertables) + corresponding `Insert*` storage methods + `*Summer` storage queries | ~600 LOC |
| 2/5 | `internal/sources/trustlines/` observer + dispatcher registration + sink wiring | ~400 LOC |
| 3/5 | `internal/sources/claimable_balances/` observer + reuse pattern        | ~300 LOC |
| 4/5 | `internal/sources/liquidity_pools/` + `internal/sources/sac_balances/` observers | ~500 LOC |
| 5/5 | `StorageClassicSupplyReader` composition + aggregator wiring + `watched_classic_assets` config | ~300 LOC |

Each PR ships a coherent slice. PRs 2/5–4/5 produce data even
without the reader (5/5) — they populate their hypertables; the
operator can audit by hand via SQL until the reader lands.

## Consequences

- Four new hypertables (~10× the row volume of `account_observations`,
  even watched-set restricted, since trustlines proliferate per
  asset). Need to validate compression rates at deploy time;
  the migration's `compress_segmentby = 'account_id'` /
  `'asset_key'` is the most common reader-query shape.
- Each observer package is operator-watched-set driven by
  default; switching to "watch every entry" needs its own ADR
  per the table-size implications.
- Once shipped, `/v1/assets/{id}` for classic credit assets
  populates total/circulating/max via the same F2-fields path
  the XLM case uses today.
- Task #57's aggregator-resident refresher loop iterates over
  the watched-asset list — the existing single-asset path
  (XLM only) becomes the multi-asset case naturally.
- The classic-supply tests in `classic_test.go` already cover
  the algorithm's edge cases (locked-set application, max-supply
  override, negative-component rejection). The reader-side tests
  follow the AccountEntry observer pattern with in-memory fakes.

## References

- Task #55: Classic-asset supply computer (the implementation
  work this ADR bounds).
- ADR-0011: Three-domain supply algorithm (Algorithm 2 spec).
- ADR-0021: AccountEntry observer (the pattern this ADR extends).
- ADR-0003: i128 / u128 never truncates.
- `internal/supply/classic.go`: `ClassicComputer` already implemented.
- `internal/supply/classic_test.go`: algorithm tests already in place.
