# W09 — Storage, schema, cache, migrations

## Scope

TimescaleDB schema + Redis key contracts + the readers/writers
that bind code to schema.

In scope:
- `migrations/0001..0028` (28 up + 28 down + README)
- `internal/storage/timescale/*.go` (every reader / writer)
- `internal/storage/redisclient/*.go`
- `internal/cachekeys/*.go`
- ADR-0006 (Timescale), ADR-0007 (Redis cache schema),
  ADR-0015 (last-closed-bucket), ADR-0024 (Redis HA via Sentinel)

## Inputs

- `inventory/migration-inventory.md`
- `evidence/cross-file-interactions.md` XFI-1206

## Per-migration audit (per `02-protocol.md` §7)

For each migration, fill:

| # | Up file | Down file | Up + down symmetry | Concurrent-safe DDL | Hypertable / cagg semantics | NUMERIC vs BIGINT | Index coverage | Reader correspondence | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 0001 | `0001_create_trades_hypertable.up.sql` | `0001_create_trades_hypertable.down.sql` | | | | | | `internal/storage/timescale/trades.go` | |
| 0002 | 0002_create_price_aggregates | … | | | | | | `aggregates.go` | |
| 0003 | 0003_create_oracle_updates_hypertable | | | | | | | `oracle.go` | |
| 0004 | 0004_relax_trades_ledger_for_offchain | | | | | | | `trades.go` | |
| 0005 | 0005_create_asset_supply_history | | | | | | | `supply.go` | |
| 0006 | 0006_create_discovered_assets | | | | | | | `discovery.go` | |
| 0007 | 0007_create_volatility_baseline | | | | | | | `baseline.go` | |
| 0008 | 0008_add_multi_window_baseline | | | | | | | `baseline.go` | |
| 0009 | 0009_create_blend_auctions | | | | | | | `blend_auctions.go` | |
| 0010 | 0010_create_account_observations | | | | | | | `account_observations.go` | |
| 0011 | 0011_create_trustline_observations | | | | | | | _verify_ | |
| 0012 | 0012_create_claimable_observations | | | | | | | _verify_ | |
| 0013 | 0013_create_lp_reserve_observations | | | | | | | _verify_ | |
| 0014 | 0014_create_sac_balance_observations | | | | | | | _verify_ | |
| 0015 | 0015_create_sep41_supply_events | | | | | | | `sep41_supply_events.go` | |
| 0016 | 0016_create_soroswap_pairs | | | | | | | `soroswap_pairs.go` | |
| 0017 | 0017_create_wasm_history | | | | | | | _verify_ | |
| 0018 | 0018_create_freeze_events | | | | | | | `freeze_events.go` | |
| 0019 | 0019_create_divergence_observations | | | | | | | `divergence_observations.go` | |
| 0020 | 0020_create_decoder_stats_5m | | | | | | | `decoder_stats.go` | |
| 0021 | 0021_create_tvl_and_mev | | | | | | | _verify_ | |
| 0022 | 0022_create_change_summary_5m | | | | | | | `change_summary.go` | |
| 0023 | 0023_create_classic_asset_registry | | | | | | | `asset_registry.go` | |
| 0024 | 0024_create_classic_asset_stats_5m | | | | | | | _verify_ | |
| 0025 | 0025_create_routers_and_attribution | | | | | | | _verify_ | |
| 0026 | 0026_create_source_contributions_and_sdex_offers | | | | | | | `price_source_contributions.go` | |
| 0027 | 0027_platform_v1_schema (19 KB!) | | | | | | | `internal/platform/postgresstore/*` | per-statement audit |
| 0028 | 0028_create_fx_quotes | | | | | | | `fx_quotes.go` | |

## Per-reader/writer file checklist

| File | Backing table(s) | Tests | Status |
| --- | --- | --- | --- |
| `internal/storage/timescale/store.go` | connection / pool | | |
| `internal/storage/timescale/trades.go` | trades hypertable | `trades_usd_volume_test.go` | |
| `internal/storage/timescale/aggregates.go` | continuous aggregates | `aggregates_test.go` | |
| `internal/storage/timescale/oracle.go` | oracle_updates | | |
| `internal/storage/timescale/supply.go` | asset_supply_history | `supply_test.go` | |
| `internal/storage/timescale/discovery.go` | discovered_assets | | |
| `internal/storage/timescale/baseline.go` | volatility_baseline + multi_window | | |
| `internal/storage/timescale/blend_auctions.go` | blend_auctions | `blend_auctions_test.go` | |
| `internal/storage/timescale/account_observations.go` | account_observations | `account_observations_test.go` | |
| `internal/storage/timescale/classic_supply_observations.go` | classic-supply observers | `classic_supply_observations_test.go` | |
| `internal/storage/timescale/sep41_supply_events.go` | sep41_supply_events | `sep41_supply_events_test.go` | |
| `internal/storage/timescale/soroswap_pairs.go` | soroswap_pairs | `soroswap_pairs_test.go` | |
| `internal/storage/timescale/freeze_events.go` | freeze_events | | |
| `internal/storage/timescale/divergence_observations.go` | divergence_observations | | |
| `internal/storage/timescale/decoder_stats.go` | decoder_stats_5m | | |
| `internal/storage/timescale/change_summary.go` | change_summary_5m | | |
| `internal/storage/timescale/asset_registry.go` | classic_asset_registry | | |
| `internal/storage/timescale/sources_stats.go` | sources contributions | | |
| `internal/storage/timescale/network_stats.go` | network_stats | | |
| `internal/storage/timescale/price_source_contributions.go` | price_source_contributions | | |
| `internal/storage/timescale/usd_volume_quote_spec.go` | spec → SQL | `trades_usd_volume_test.go` | |
| `internal/storage/timescale/markets.go` | markets | `markets_cursor_validate_test.go` | |
| `internal/storage/timescale/coins.go` | coins (legacy?) | `coins_cursor_validate_test.go` | |
| `internal/storage/timescale/issuers.go` | issuers | | |
| `internal/storage/timescale/assets.go` | assets | | |
| `internal/storage/timescale/cursors.go` | cursor validation | | |
| `internal/storage/timescale/fx_quotes.go` | fx_quotes | | |
| `internal/storage/redisclient/redisclient.go` | Redis pool | `redisclient_test.go` | |
| `internal/cachekeys/*.go` | sole key builder (ADR-0007) | | |

## Cache key audit

For each prewarm goroutine, verify it calls the cached reader
with byte-identical args to the corresponding handler. The
historical lesson (`feedback_prewarm_handler_drift`) found three
bugs from drifting Order / Sources / Limit. Re-check all dimensions:

- `Order` (asc/desc/`""` empty)
- `Sources` (set/order/case)
- `Limit` (default vs explicit)
- `Quote` (canonical case)
- `Asset` (canonical id)
- `Window` (bucket size)

## ADR-0024 Sentinel awareness

- `internal/storage/redisclient/redisclient.go` should use
  `redis.UniversalClient` or sentinel-aware factory
- Verify ansible role provisions sentinels

## Adversarial vectors

- C1.1..C1.6 storage layer
- C2.1..C2.4 cache layer
- B3.2 prewarm/handler drift
- B3.4 cache key length blowup
- B4.1 crafted cursor bypassing bounds

## Cross-workstream dependencies

- W05 owns NUMERIC + cachekey identity contracts
- W11 verifies handler-side cache + cursor use
- W14 verifies cagg-stale + db-disk-full alerts + runbooks
- W18 owns Patroni + Sentinel ansible roles

## Closure criteria

- Every migration row complete
- Every reader/writer row complete
- Cache key audit covers all prewarmers
- ADR-0007 sole-builder grep verifies cachekeys is sole writer
