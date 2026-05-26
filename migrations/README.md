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
7. **Migrations are applied as the `ratesengine` app role, never as
   a superuser.** Always go through `ratesengine-migrate` /
   `RATESENGINE_POSTGRES_DSN` (the DSN under "Running" above) — that
   DSN is the `ratesengine` role, so every object a migration
   creates is owned by `ratesengine` and the application has full
   access to it by construction. This is why a bare
   `CREATE TABLE …` needs no explicit `GRANT … TO ratesengine` — on
   a correctly-applied deploy (R2/R3/fresh) the app *is* the owner.
   Applying a migration manually as the `postgres` superuser
   instead makes the object superuser-owned and the app loses
   access to it. That happened to `source_entry_counts` (migration
   0035) on r1 on 2026-05-19 — `permission denied for table
   source_entry_counts (42501)` from the indexer's always-on entry
   tally and from `ratesengine-ops seed-entry-counts`. Hot-fixed in
   place with `ALTER TABLE source_entry_counts OWNER TO ratesengine`
   (the canonical shape — matches `trades` and every other table).
   Do **not** "fix" this class of issue with a follow-up GRANT
   migration: run as the `ratesengine` role it cannot `GRANT`/
   `ALTER` on a superuser-owned object (errors in exactly the
   anomaly case), and on a correctly-owned object it is a redundant
   self-grant no-op. The fix is operational (apply as the app
   role), not schema.

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
| 0005 | [`0005_create_asset_supply_history.up.sql`](0005_create_asset_supply_history.up.sql) | `asset_supply_history` hypertable per ADR-0011 — append-only per-asset supply snapshots backing the F2 fields on `/v1/assets/{id}` |
| 0006 | [`0006_create_discovered_assets.up.sql`](0006_create_discovered_assets.up.sql) | `discovered_assets` table for SEP-41 auto-discovery; every contract emitting a transfer / mint / burn / clawback event lands here for operator triage |
| 0007 | [`0007_create_volatility_baseline.up.sql`](0007_create_volatility_baseline.up.sql) | `volatility_baseline_1m` per-pair statistical baseline per ADR-0019 Phase 2 — robust median + MAD baseline used by the anomaly-freeze policy |
| 0008 | [`0008_add_multi_window_baseline.up.sql`](0008_add_multi_window_baseline.up.sql) | Adds 1d + 7d baseline columns to `volatility_baseline_1m` per ADR-0019 §"Multi-window safeguard against frog-boiling" |
| 0009 | [`0009_create_blend_auctions.up.sql`](0009_create_blend_auctions.up.sql) | `blend_auctions` hypertable — one row per observed Blend auction event (new_auction, etc.) |
| 0010 | [`0010_create_account_observations.up.sql`](0010_create_account_observations.up.sql) | `account_observations` hypertable per ADR-0021 — one row per AccountEntry-delta touching an operator-watched account, backs Algorithm 1 (XLM) reserves |
| 0011 | [`0011_create_trustline_observations.up.sql`](0011_create_trustline_observations.up.sql) | `trustline_observations` hypertable per ADR-0022 — backs Algorithm 2 classic-credit supply: Σ trustline component |
| 0012 | [`0012_create_claimable_observations.up.sql`](0012_create_claimable_observations.up.sql) | `claimable_observations` hypertable per ADR-0022 — backs Algorithm 2: Σ claimable-balance component |
| 0013 | [`0013_create_lp_reserve_observations.up.sql`](0013_create_lp_reserve_observations.up.sql) | `lp_reserve_observations` hypertable per ADR-0022 — backs Algorithm 2: Σ LP-reserve component |
| 0014 | [`0014_create_sac_balance_observations.up.sql`](0014_create_sac_balance_observations.up.sql) | `sac_balance_observations` hypertable per ADR-0022 — backs Algorithm 2: Σ SAC-wrapped contract balance component |
| 0015 | [`0015_create_sep41_supply_events.up.sql`](0015_create_sep41_supply_events.up.sql) | `sep41_supply_events` hypertable per ADR-0023 — backs Algorithm 3 SEP-41 supply: Σ mint − Σ burn − Σ clawback per contract |
| 0016 | [`0016_create_anomaly_freezes.up.sql`](0016_create_anomaly_freezes.up.sql) | `anomaly_freezes` table — durable record of anomaly-freeze decisions per ADR-0019 |
| 0017 | [`0017_create_archive_completeness.up.sql`](0017_create_archive_completeness.up.sql) | `archive_completeness` tables backing the dual-archive completeness daemon per ADR-0017 |
| 0018 | [`0018_create_external_poller_state.up.sql`](0018_create_external_poller_state.up.sql) | `external_poller_state` table — persists last-success / last-error per external poller for restart-safe resume |
| 0019 | [`0019_create_market_observations.up.sql`](0019_create_market_observations.up.sql) | `market_observations` — per-pair / per-source observation log for divergence and `flags.single_source` provenance |
| 0020 | [`0020_create_supply_state.up.sql`](0020_create_supply_state.up.sql) | `supply_state` rollup populated by the aggregator from the per-class hypertables; backs F2 fields without recomputing on every read |
| 0021 | [`0021_create_change_summary.up.sql`](0021_create_change_summary.up.sql) | `change_summary_5m` hypertable — multi-window delta strip data backing every "+12.3% / -4.1%" surface on the explorer |
| 0022 | [`0022_create_incidents.up.sql`](0022_create_incidents.up.sql) | `incidents` + `incident_components` — public-facing SEV register backing `/v1/incidents` and `/v1/incidents.atom` |
| 0023 | [`0023_create_redstone_extras.up.sql`](0023_create_redstone_extras.up.sql) | Redstone-specific feed-id resolution table — bridges WritePrices events to operator-named feeds per redstone discovery note |
| 0024 | [`0024_create_divergence_runs.up.sql`](0024_create_divergence_runs.up.sql) | `divergence_runs` table — one row per CoinGecko / CMC / Chainlink-HTTP cross-check execution; backs the divergence dashboards |
| 0025 | [`0025_create_routers_and_attribution.up.sql`](0025_create_routers_and_attribution.up.sql) | Soroswap router + attribution tables — tracks which router contract emitted each multi-hop swap so attribution is per-router |
| 0026 | [`0026_create_source_contributions_and_sdex_offers.up.sql`](0026_create_source_contributions_and_sdex_offers.up.sql) | `source_contributions` + `sdex_offers` — per-source weight history and per-offer SDEX state for the deep-SDEX feed |
| 0027 | [`0027_platform_v1_schema.up.sql`](0027_platform_v1_schema.up.sql) | Platform v1 schema — accounts / users / sessions / API keys / Stripe subscriptions / dashboard webhook store, the dashboard's authority surface |
| 0028 | [`0028_create_fx_quotes.up.sql`](0028_create_fx_quotes.up.sql) | `fx_quotes` hypertable — long-form persisted ECB / exchangerates / polygon-forex FX history backing `/v1/chart` fiat:fiat past 7d |
| 0029 | [`0029_drop_unused_blend_jsonb_gin_indexes.up.sql`](0029_drop_unused_blend_jsonb_gin_indexes.up.sql) | Drops two unused JSONB GIN indexes on `blend_auctions` — the auction query path uses btree on the typed columns |
| 0030 | [`0030_asset_supply_history_unique_constraint.up.sql`](0030_asset_supply_history_unique_constraint.up.sql) | Promotes `asset_supply_history_asset_ledger_idx` from UNIQUE INDEX → UNIQUE CONSTRAINT (`DROP INDEX` + `ADD CONSTRAINT … UNIQUE (asset_key, ledger_sequence, time)`) so the supply-snapshot writer's `ON CONFLICT` clause matches it on Timescale hypertables. Decompresses chunks + disables compression around the DDL and restores the 0005 compression settings after the swap (F-1261 codex audit-2026-05-13 — mirrors the 0004/trades pattern). F-1205 follow-up |
| 0031 | [`0031_remove_trades_retention.up.sql`](0031_remove_trades_retention.up.sql) | Removes the 90-day retention policy on `trades` and the 30-day retention on `prices_1m` / `prices_15m` — operator wants every raw trade preserved forever (postgres data dir is on a 1.5 TB ZFS volume with room for a decade of raw trades) |
| 0032 | [`0032_seed_soroswap_router.up.sql`](0032_seed_soroswap_router.up.sql) | Pre-seeds `routers` with the operator-vetted Soroswap router contract id (`auto_discovered = false`), so the router-attribution observer can populate the dispatcher's `ContractCallDecoder` match-set at startup |
| 0033 | [`0033_seed_defindex_vaults.up.sql`](0033_seed_defindex_vaults.up.sql) | Pre-seeds `routers` with the three Phase-A DeFindex autocompound vaults (`kind = 'aggregator-vault'`); hand-curated until factory-event vault discovery ships |
| 0034 | [`0034_oracle_price_aggregates.up.sql`](0034_oracle_price_aggregates.up.sql) | Continuous aggregates for `oracle_updates` (7-tier grain set, sister to 0002) — first/last/min/max/count per `(source, asset, quote, bucket)`, preserving per-oracle identity rather than collapsing to a single VWAP |
| 0035 | [`0035_create_source_entry_counts.up.sql`](0035_create_source_entry_counts.up.sql) | `source_entry_counts` — an always-on, per-source running tally of ingested entries (trades + oracle_updates) so `/v1/diagnostics/ingestion` has a coverage number that stays cheap to read even during an all-time backfill |
| 0036 | [`0036_create_pools_per_source_cagg.up.sql`](0036_create_pools_per_source_cagg.up.sql) | `pools_per_source_1h` continuous aggregate — durable 1h-bucket backing for `/v1/pools`, eliminating the cold full-`trades`-scan the handler used to pay (#25) |
| 0037 | [`0037_trades_pair_source_ts_index.up.sql`](0037_trades_pair_source_ts_index.up.sql) | `trades_pair_source_ts_idx (base_asset, quote_asset, source, ts DESC, ledger DESC)` — covering index for `Store.LatestTradePerSource` / `/v1/observations`; turns its `DISTINCT ON (source)` from an O(rows_in_pair) scan+sort into an O(num_sources) skip-scan. On a populated node build it `CONCURRENTLY` by hand first (see the `.up.sql` header) — the in-transaction build would block ingest. #30 |
| 0038 | [`0038_create_cctp_events.up.sql`](0038_create_cctp_events.up.sql) | `cctp_events` hypertable — one row per observed Circle CCTP v2 bridge event (deposit_for_burn / mint_and_withdraw / message_sent / message_received) on Stellar. Promoted typed columns (amount, fee, token, counterparty_domain) + a jsonb `attributes` blob for the event-type-specific remainder. Class=bridge, never VWAP. #40 |
| 0039 | [`0039_create_rozo_events.up.sql`](0039_create_rozo_events.up.sql) | `rozo_events` hypertable — one row per observed Rozo v1 intent-bridge event (payment / flush) on Stellar. Fully typed (amount, destination always present; from_addr/memo payment-only; token flush-only) — no jsonb blob, v1 Payment is simple enough. Class=bridge, never VWAP. #41 |
| 0040 | [`0040_remove_oracle_updates_retention.up.sql`](0040_remove_oracle_updates_retention.up.sql) | Removes the 90-day retention policy on `oracle_updates` (sister to 0031 for `trades`) — every raw oracle observation is now preserved indefinitely. The 0034 CAGGs are unchanged; the migration header documents the per-grain `refresh_continuous_aggregate` calls that re-backfill them over the full raw range. #14 |
| 0041 | [`0041_create_soroban_events.up.sql`](0041_create_soroban_events.up.sql) | `soroban_events` hypertable per ADR-0029 — raw-event landing zone capturing every Soroban contract event the dispatcher routes, with topics + body + op_args stored as raw XDR for future per-source decoder backfills (`INSERT … SELECT` rather than MinIO re-walks). PK leads with `ledger_close_time` (TS103 lesson); compression after 7 days segmented by `contract_id`. |
| 0042 | [`0042_create_comet_liquidity.up.sql`](0042_create_comet_liquidity.up.sql) | `comet_liquidity` hypertable — one row per `(ledger, tx_hash, op_index, event_kind, token)` covering Balancer-v1 `join_pool` / `exit_pool` / `deposit` / `withdraw` events under the `POOL` namespace. join/exit are loop-emitted per token; PK includes `token` so per-token rows don't collide. Direction column ('add'/'remove') keeps Sum(amount) ordered. PK leads with `ledger_close_time` (TS103); compression after 7 days segmented by `contract_id`. Historical fill via `soroban_events` (0041). #26 |
| 0043 | [`0043_create_soroswap_skim_events.up.sql`](0043_create_soroswap_skim_events.up.sql) | `soroswap_skim_events` hypertable — one row per observed Soroswap pair-contract `skim` event (Uniswap-v2-style claim of excess pool balance above reserves). Closes the "every emitted topic gets classified" gap — previously skim was declared in `events.go` but unreachable through `classify()`. Not a trade; never feeds VWAP. PK leads with `ledger_close_time` (TS103); compression after 7 days segmented by `contract_id`. Historical fill via `soroban_events` (0041). #28 |

F-1241 (codex audit-2026-05-12): the table previously stopped at
0015, leaving 0016..0029 (14 migrations) undocumented even though
they shipped. Future migrations: continue adding one row per
`.up.sql` landed on `main`.

## References

- [ADR-0003 i128 no-truncation](../docs/adr/0003-i128-no-truncation.md)
- [ADR-0006 TimescaleDB](../docs/adr/0006-timescaledb-for-price-time-series.md)
- [HA plan §3.3](../docs/architecture/ha-plan.md) — hypertable + retention design
- [Coverage matrix S6/S7](../docs/architecture/coverage-matrix.md) — RFP rows mapping to these schemas
