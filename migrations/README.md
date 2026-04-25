# Database migrations

TimescaleDB / PostgreSQL schema migrations, `golang-migrate` format.

Numbering is four-digit sequential. Each migration has a matching
`up.sql` / `down.sql` pair. `down.sql` must fully reverse `up.sql`
where possible; for irreversible operations (e.g. dropping a
hypertable chunk), the `down.sql` contains a comment explaining the
asymmetry.

## Running

Through the `ratesengine-migrate` binary (preferred):

```sh
make db-migrate-status    # what's applied
make db-migrate-up        # apply everything pending
make db-migrate-down      # roll back one
```

Direct via `golang-migrate` CLI:

```sh
migrate -path migrations -database "${RATESENGINE_POSTGRES_DSN}" up
migrate -path migrations -database "${RATESENGINE_POSTGRES_DSN}" down 1
```

## Rules

1. **Never edit a migration that has run in production** (this
   includes staging). Add a new migration instead.
2. **Numbering must be dense** — no gaps, no duplicates.
3. **Changes to TimescaleDB features** (hypertables, compression,
   continuous aggregates) must be done with the extension's API
   (`create_hypertable`, `add_compression_policy`,
   `refresh_continuous_aggregate`) — not by touching the internal
   `_timescaledb_*` schemas directly.
4. **Every migration that creates a continuous aggregate** also adds
   its refresh policy + retention policy in the same file. A CAGG
   without a refresh policy is a silent bug.
5. **Amounts are always `NUMERIC`** (arbitrary precision). Never
   `bigint` — breaks i128 per ADR-0003.
6. **IDs follow canonical wire form** as text: `<code>-<issuer>` for
   classic, `C…` for Soroban, `native` for XLM. See
   `internal/canonical/asset.go`.

## Conventions

- Statement terminators on their own line; always semicolon-end.
- `CREATE … IF NOT EXISTS` where idempotent; otherwise plain `CREATE`
  so a rerun after manual poking fails loudly.
- Comments above the statement (not inline) and explain the *why*.
- Timestamp columns are `timestamptz`, stored + served in UTC.
- Transactions: each migration runs in its own transaction by default
  (golang-migrate); disable with `-- migrate:no-transaction` when
  creating a hypertable on a very large existing table.

## Current migrations

Sequential index of what each migration adds (read the `.up.sql`
header for the full motivation). Update this table when a new
migration lands.

| Number | File | Adds |
| --- | --- | --- |
| 0001 | [`0001_create_trades_hypertable.up.sql`](0001_create_trades_hypertable.up.sql) | Core `trades` hypertable, retention policy, primary indexes |
| 0002 | [`0002_create_price_aggregates.up.sql`](0002_create_price_aggregates.up.sql) | Continuous aggregates (1m/15m/1h/4h/1d/1w/1mo) + refresh + retention. **CAVEAT**: `twap` column is `avg(quote/base)` — arithmetic mean of trade prices, NOT a time-weighted average. True TWAP needs inter-trade durations the CAGG definitions don't capture; computed in Go via `internal/aggregate/twap.go` instead |
| 0003 | [`0003_create_oracle_updates_hypertable.up.sql`](0003_create_oracle_updates_hypertable.up.sql) | `oracle_updates` hypertable for Reflector / Redstone / Band observations + compression + retention |
| 0004 | [`0004_relax_trades_ledger_for_offchain.up.sql`](0004_relax_trades_ledger_for_offchain.up.sql) | Relaxes the `trades.ledger > 0` constraint so off-chain sources (Binance / Kraken / etc) can stamp `ledger = 0` |

**Pending future work** (not yet numbered, takes the next free
slot when it lands): a materialised `asset_catalogue` +
`market_catalogue` populated incrementally by the indexer. Today
`internal/storage/timescale/{assets,markets}.go` does on-query
DISTINCT scans across `trades`, which works at current scale but
won't at millions-of-rows scale. See those packages' performance
notes for the call site.

## References

- [ADR-0003 i128 no-truncation](../docs/adr/0003-i128-no-truncation.md)
- [ADR-0006 TimescaleDB](../docs/adr/0006-timescaledb-for-price-time-series.md)
- [HA plan §3.3](../docs/architecture/ha-plan.md) — hypertable + retention design
- [Coverage matrix S6/S7](../docs/architecture/coverage-matrix.md) — RFP rows mapping to these schemas
