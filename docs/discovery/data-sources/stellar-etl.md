# stellar-etl (Hubble's ETL pipeline)

**Status:** 📚 **Reference only.** Second authoritative reference
implementation for XDR → typed-row extraction, alongside
`withObsrvr/stellar-extract`. We study it, lift patterns, but don't
import or fork.

**Repo:** <https://github.com/stellar/stellar-etl>
**Verified against:** `README.md`, `.claude/CLAUDE.md`,
`internal/transform/trade.go`, transform file inventory at clone
time (2026-04-22).

## What it is

The open-source Go ETL pipeline that **powers Hubble's BigQuery
dataset** (see [stellar-data-lakes.md](stellar-data-lakes.md) §2).
SDF-maintained, Apache-2.0 licensed. Reads `LedgerCloseMetaBatch`
XDR from a **GCS datastore** (Galexie-produced), transforms per-type
into flat structs, writes as newline-delimited JSON or Parquet files
that are loaded into BigQuery.

Direct peer of `stellar-extract` in purpose but **different
optimisation target** — stellar-etl is a **batch BigQuery loader**;
stellar-extract is a **per-request library**.

## Command surface (from README)

```
stellar-etl export_ledgers         <start> <end>
stellar-etl export_transactions    <start> <end>
stellar-etl export_operations      <start> <end>
stellar-etl export_effects         <start> <end>
stellar-etl export_assets          <start> <end>
stellar-etl export_trades          <start> <end>
stellar-etl export_diagnostic_events <start> <end>
stellar-etl export_ledger_entry_changes <start> <end>
stellar-etl get_ledger_range_from_times <start-ts> <end-ts>
```

Each outputs a file: `{start}-{end-1}-{export_type}.{txt|parquet}`.

## Transform-layer inventory (verified)

From `internal/transform/`:

```
account.go              account_signer.go      asset.go
claimable_balance.go    config_setting.go      contract_code.go
contract_data.go        contract_events.go     effects.go
ledger.go               ledger_transaction.go  liquidity_pool.go
offer.go                offer_normalized.go    operation.go
restored_key.go         token_transfer.go      trade.go
transaction.go          trustline.go           ttl.go
```

Plus schema definition files:

```
schema.go               — JSON output struct defs (BigQuery-aligned)
schema_parquet.go       — Parquet struct defs
parquet_converter.go    — SchemaParquet interface + ToParquet() impls
```

**22 transform types.** Matches `stellar-extract`'s count closely.
The list is near-identical except stellar-etl has `offer_normalized`
(denormalised view for analytics) and `diagnostic_events` (Soroban
diagnostics), while stellar-extract has `evicted_keys` (protocol-23
state-archival output).

## Package layout (per .claude/CLAUDE.md)

```
cmd/                     # Cobra CLI commands (one file per export)
internal/
  input/                 # Extraction — reads GCS datastore
  transform/             # XDR → output struct
    schema.go
    schema_parquet.go
    parquet_converter.go
  toid/                  # Transaction Object ID calc
  utils/                 # Flags, env, logger, helpers
```

Four-file pattern for adding a new export command:

1. `cmd/export_<name>.go` — Cobra command, flag parsing.
2. `cmd/export_<name>_test.go` — integration test + golden file.
3. `internal/input/<name>.go` — extraction, channel-based.
4. `internal/transform/<name>.go` — transformation; add the output
   struct to `schema.go`.

Clean convention; we can adopt a similar one.

## Trade extraction — comparison with `stellar-extract`

**stellar-etl's `TransformTrade`** (`internal/transform/trade.go:20-…`)
walks `operationResults.claimedOffers`, same ClaimAtom-variant
switch that [stellar-extract/trades.go](withobsrvr-stellar-extract.md)
uses. Differences worth noting:

| Concern | stellar-etl | stellar-extract |
| ------- | ----------- | --------------- |
| Language / deps | Go + `go-stellar-sdk` + `guregu/null` | Go + `go-stellar-sdk` |
| ClaimAtom variants handled | `V0`, `OrderBook`, `LiquidityPool` ✓ | Same ✓ |
| Output shape | `TradeOutput` (BigQuery-aligned, denormalised, many joins pre-flattened) | `TradeData` (lean Go struct) |
| Liquidity-pool specifics | Pool fee extraction via `findPoolFee()`; pool delta via `liquidityPoolChange()` | Via effects path separately |
| Asset IDs | FarmHash fingerprint (`FarmHashAsset`) | String canonical form |
| Path-payment trades | Integrated in the same `TransformTrade` walk | Separated: orderbook in `trades.go`, path-payment in `effects.go` |
| Tests / fixtures | Extensive golden-file integration tests | Unit tests + some integration |
| Operational model | Batch CLI → files → BigQuery | Library → caller-defined sink |
| Protocol-version dispatch | Direct via SDK helpers | Same |

**Our choice** is still to adopt `stellar-extract` as the embedded
library (see
[data-sources/withobsrvr-stellar-extract.md](withobsrvr-stellar-extract.md)),
but we **steal patterns** from stellar-etl:

1. **Golden-file integration tests** — their `testdata/trades/`,
   `testdata/contract_events/`, etc. directories are full of
   per-ledger fixtures we can lift as known-good inputs for our
   consolidator's regression tests.
2. **Path-payment trade integration** into the same trade-walk
   as orderbook trades — cleaner than stellar-extract's split
   across `trades.go` + `effects.go`. Our consolidator can unify.
3. **Pool-fee extraction as a first-class field** on every LP
   trade — stellar-extract captures it at the pool level, not the
   trade level.

## Testdata / fixture windfall

`testdata/` has pre-captured samples for every export type:

```
testdata/accounts/            testdata/assets/
testdata/changes/             testdata/claimable_balances/
testdata/contract_events/     testdata/effects/
testdata/ledger_transactions/ testdata/ledgers/
testdata/offers/              testdata/operations/
testdata/orderbooks/          testdata/ranges/
testdata/signers/             testdata/token_transfers/
testdata/trades/              testdata/transactions/
testdata/trustlines/
```

Each directory has golden-file `{start}-{end}-{type}.txt` files
that are **real pubnet data** captured at fixture time. For our
fixture set (per
[adversarial-audit.md §7](../adversarial-audit.md)):

- Can use `testdata/trades/` + `testdata/effects/` as regression
  inputs without writing our own capture tooling.
- License is Apache-2.0 so we can include transformed derivatives in
  our own test suite.

## Parquet type constraint (from their CLAUDE.md)

> "`uint32` fields must be converted to `int64` in all `ToParquet()`
> implementations due to a restriction in the `parquet-go` library."

Noted. If we ever add Parquet output (Phase-3+ for cold storage /
analytics export) we inherit this constraint.

## stellar-etl-airflow — the scheduling layer

Separate repo: <https://github.com/stellar/stellar-etl-airflow>.
Airflow DAGs that drive stellar-etl in production to populate
Hubble.

Not in our scope — we don't run Airflow. But the DAG
definitions reveal SDF's production schedule (how often, what
ranges, which exports) — worth a glance if we ever want to mimic
their backfill cadence.

## Comparison table: our three reference implementations

| Dimension | `stellar-etl` (SDF) | `stellar-extract` (withObsrvr) | `cdp-pipeline-workflow` (withObsrvr) |
| --------- | ------------------- | ------------------------------ | ------------------------------------ |
| Role | Batch ETL → BigQuery | Library for typed extraction | Monolithic pipeline |
| Correctness | ✅ authoritative for BQ | ✅ code-verified | ❌ verified bugs |
| License | Apache-2.0 | Apache-2.0 | Apache-2.0 |
| Our use | Reference + test fixtures | **Direct dependency** | Avoid |
| Event-handling | SDK helpers, protocol-aware | Same | Version-naive in places |
| i128 handling | ✓ via `ingest` SDK | ✓ explicit in `scval_converter` | ❌ low-bits only in places |

## Open items

- [ ] Pick a small set of pubnet golden files from
      `stellar-etl/testdata/trades/` to use as our consolidator's
      first regression inputs. Cross-check our outputs against
      theirs; any disagreement is a bug in one of us.
- [ ] Read `internal/transform/token_transfer.go` — SEP-41 token
      transfer extraction. Compare with our plan from
      [../notes/sep-41-token-events.md](../notes/sep-41-token-events.md).
- [ ] Study `findPoolFee()` — we'll need this to attribute the
      correct fee per LP-trade in our canonical row, which
      stellar-extract doesn't do at the trade level.

## Related

- [../data-sources/stellar-data-lakes.md](../data-sources/stellar-data-lakes.md)
  — Hubble (the consumer of stellar-etl output).
- [../data-sources/withobsrvr-stellar-extract.md](../data-sources/withobsrvr-stellar-extract.md)
  — our direct dep; learning target for the comparison.
- [../data-sources/withobsrvr-cdp-pipeline-workflow.md](../data-sources/withobsrvr-cdp-pipeline-workflow.md)
  — the anti-example.
- [../adversarial-audit.md](../adversarial-audit.md) §7 — fixture
  set planning.
