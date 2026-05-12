# Migration Inventory

| # | Up file | Up bytes | Down file | Down bytes | Audit unit | Status |
| --- | --- | ---: | --- | ---: | --- | --- |
| 0001 | `migrations/0001_create_trades_hypertable.up.sql` | 4501 | `migrations/0001_create_trades_hypertable.down.sql` | 372 | create_trades_hypertable | todo |
| 0002 | `migrations/0002_create_price_aggregates.up.sql` | 12032 | `migrations/0002_create_price_aggregates.down.sql` | 450 | create_price_aggregates | todo |
| 0003 | `migrations/0003_create_oracle_updates_hypertable.up.sql` | 3409 | `migrations/0003_create_oracle_updates_hypertable.down.sql` | 191 | create_oracle_updates_hypertable | todo |
| 0004 | `migrations/0004_relax_trades_ledger_for_offchain.up.sql` | 1485 | `migrations/0004_relax_trades_ledger_for_offchain.down.sql` | 1027 | relax_trades_ledger_for_offchain | todo |
| 0005 | `migrations/0005_create_asset_supply_history.up.sql` | 3772 | `migrations/0005_create_asset_supply_history.down.sql` | 197 | create_asset_supply_history | todo |
| 0006 | `migrations/0006_create_discovered_assets.up.sql` | 2157 | `migrations/0006_create_discovered_assets.down.sql` | 124 | create_discovered_assets | todo |
| 0007 | `migrations/0007_create_volatility_baseline.up.sql` | 3432 | `migrations/0007_create_volatility_baseline.down.sql` | 167 | create_volatility_baseline | todo |
| 0008 | `migrations/0008_add_multi_window_baseline.up.sql` | 1928 | `migrations/0008_add_multi_window_baseline.down.sql` | 316 | add_multi_window_baseline | todo |
| 0009 | `migrations/0009_create_blend_auctions.up.sql` | 6877 | `migrations/0009_create_blend_auctions.down.sql` | 101 | create_blend_auctions | todo |
| 0010 | `migrations/0010_create_account_observations.up.sql` | 5272 | `migrations/0010_create_account_observations.down.sql` | 113 | create_account_observations | todo |
| 0011 | `migrations/0011_create_trustline_observations.up.sql` | 2915 | `migrations/0011_create_trustline_observations.down.sql` | 117 | create_trustline_observations | todo |
| 0012 | `migrations/0012_create_claimable_observations.up.sql` | 1998 | `migrations/0012_create_claimable_observations.down.sql` | 117 | create_claimable_observations | todo |
| 0013 | `migrations/0013_create_lp_reserve_observations.up.sql` | 2210 | `migrations/0013_create_lp_reserve_observations.down.sql` | 119 | create_lp_reserve_observations | todo |
| 0014 | `migrations/0014_create_sac_balance_observations.up.sql` | 2496 | `migrations/0014_create_sac_balance_observations.down.sql` | 121 | create_sac_balance_observations | todo |
| 0015 | `migrations/0015_create_sep41_supply_events.up.sql` | 3688 | `migrations/0015_create_sep41_supply_events.down.sql` | 111 | create_sep41_supply_events | todo |
| 0016 | `migrations/0016_create_soroswap_pairs.up.sql` | 3396 | `migrations/0016_create_soroswap_pairs.down.sql` | 296 | create_soroswap_pairs | todo |
| 0017 | `migrations/0017_create_wasm_history.up.sql` | 3989 | `migrations/0017_create_wasm_history.down.sql` | 339 | create_wasm_history | todo |
| 0018 | `migrations/0018_create_freeze_events.up.sql` | 4146 | `migrations/0018_create_freeze_events.down.sql` | 258 | create_freeze_events | todo |
| 0019 | `migrations/0019_create_divergence_observations.up.sql` | 4394 | `migrations/0019_create_divergence_observations.down.sql` | 282 | create_divergence_observations | todo |
| 0020 | `migrations/0020_create_decoder_stats_5m.up.sql` | 3321 | `migrations/0020_create_decoder_stats_5m.down.sql` | 223 | create_decoder_stats_5m | todo |
| 0021 | `migrations/0021_create_tvl_and_mev.up.sql` | 6075 | `migrations/0021_create_tvl_and_mev.down.sql` | 143 | create_tvl_and_mev | todo |
| 0022 | `migrations/0022_create_change_summary_5m.up.sql` | 4272 | `migrations/0022_create_change_summary_5m.down.sql` | 99 | create_change_summary_5m | todo |
| 0023 | `migrations/0023_create_classic_asset_registry.up.sql` | 5845 | `migrations/0023_create_classic_asset_registry.down.sql` | 236 | create_classic_asset_registry | todo |
| 0024 | `migrations/0024_create_classic_asset_stats_5m.up.sql` | 2810 | `migrations/0024_create_classic_asset_stats_5m.down.sql` | 109 | create_classic_asset_stats_5m | todo |
| 0025 | `migrations/0025_create_routers_and_attribution.up.sql` | 5445 | `migrations/0025_create_routers_and_attribution.down.sql` | 365 | create_routers_and_attribution | todo |
| 0026 | `migrations/0026_create_source_contributions_and_sdex_offers.up.sql` | 6537 | `migrations/0026_create_source_contributions_and_sdex_offers.down.sql` | 177 | create_source_contributions_and_sdex_offers | todo |
| 0027 | `migrations/0027_platform_v1_schema.up.sql` | 19376 | `migrations/0027_platform_v1_schema.down.sql` | 1213 | platform_v1_schema | todo |
| 0028 | `migrations/0028_create_fx_quotes.up.sql` | 3771 | `migrations/0028_create_fx_quotes.down.sql` | 149 | create_fx_quotes | todo |

_Reviewer: per W09 protocol §7, walk every up + down sequentially._
