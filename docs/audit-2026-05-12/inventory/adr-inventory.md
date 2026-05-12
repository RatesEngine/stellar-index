# ADR Inventory

| ADR | Title | Status | Implementation surface | Reconciled (R06) |
| --- | --- | --- | --- | --- |
| `0001-horizon-deprecated` | Horizon is not in the Rates Engine architecture | Accepted | _todo_ | todo |
| `0002-minio-s3-compat-storage` | Self-hosted storage is S3-compatible (MinIO), not local filesystem | Accepted | _todo_ | todo |
| `0003-i128-no-truncation` | i128 / u128 values preserved end-to-end; never truncated to int64 | Accepted | _todo_ | todo |
| `0004-tier1-validator-aspiration` | Tier-1 three-validator aspiration (post-launch) | Accepted | _todo_ | todo |
| `0005-monorepo` | Monorepo with a single Go module | Accepted | _todo_ | todo |
| `0006-timescaledb-for-price-time-series` | TimescaleDB for price time-series storage | Accepted | _todo_ | todo |
| `0007-redis-cache-schema` | Redis as hot-path cache + rate-limit + ephemeral state | Accepted | _todo_ | todo |
| `0008-ha-topology` | Per-region HA topology — colo primary + cloud DR, three-tier hot/warm/cold storage | Accepted | _todo_ | todo |
| `0009-latency-budget` | API latency budget — per-component time slices summing to p95 ≤ 200ms / p99 ≤ 500ms | Accepted | _todo_ | todo |
| `0010-off-chain-fiat-representation` | Off-chain fiat currencies as AssetType "fiat" | Accepted | _todo_ | todo |
| `0011-supply-algorithm` | Three-domain supply algorithm — XLM hard-coded, classic from ledger entries, SEP-41 from event sums | Accepted | _todo_ | todo |
| `0013-go-stellar-sdk-xdr-for-scval` | Adopt go-stellar-sdk/xdr for SCVal decoding in source connectors | Accepted | _todo_ | todo |
| `0014-crypto-ticker-representation` | Crypto tickers as AssetType "crypto" | Accepted | _todo_ | todo |
| `0015-last-closed-bucket-rate-serving` | API rates served from last-closed bucket, never in-progress | Accepted | _todo_ | todo |
| `0016-per-region-storage-strategy` | Per-region storage strategies for archival nodes (Hetzner full / AWS hybrid / Vultr hybrid) | Accepted | _todo_ | todo |
| `0017-archive-completeness-invariants` | Archive completeness invariants and dual-archive integrity model | Accepted | _todo_ | todo |
| `0018-api-consistency-surfaces` | API consistency surfaces — closed-bucket, tip, and observations | Accepted | _todo_ | todo |
| `0019-anomaly-response-and-confidence-scoring` | Anomaly response policy and confidence scoring — per-asset statistical baselines | Accepted | _todo_ | todo |
| `0020-chart-api-contract` | Chart API contract — timeframe + granularity + price_type | Accepted | _todo_ | todo |
| `0021-account-entry-observer` | AccountEntry observer — live home-domain + reserve-balance tracking | Accepted | _todo_ | todo |
| `0022-classic-supply-observers` | Classic-supply observers — Trustline / ClaimableBalance / LiquidityPool / ContractData entry tracking | Accepted | _todo_ | todo |
| `0023-sep41-supply-observer` | SEP-41 supply observer — mint / burn / clawback event-stream tracking | Accepted | _todo_ | todo |
| `0024-redis-ha-via-sentinel` | Redis HA via Sentinel (not Cluster) | Accepted | _todo_ | todo |
| `0025-caddy-cloudflare-trusted-proxy` | Caddy trusts Cloudflare for client-IP signal via CIDR-pinned static list | Accepted | _todo_ | todo |
| `0026-stablecoin-fiat-proxy-late-binding` | Stablecoin → fiat proxy is late-binding aggregator policy, not eager ingest normalisation | Accepted | _todo_ | todo |
