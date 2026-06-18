# Architecture Decision Records

Every significant architectural choice in Stellar Index is captured
here as an **ADR** — a numbered, dated, immutable record.

## Rules

1. **Immutable.** Once an ADR is `Accepted`, its body is never
   edited for content. Typo fixes and formatting are allowed; rationale
   is not.
2. **Supersede, don't rewrite.** If a decision changes, write a new
   ADR that supersedes the old one, and add one line to the old ADR's
   metadata:

   ```yaml
   superseded_by: 0017
   ```

3. **Numbered sequentially.** ADR-0001, ADR-0002, … No renumbering.
   Gaps are allowed when a number has been reserved for a planned
   ADR that hasn't been written yet — list it in the index below
   with status *Planned* and the doc/ref that depends on it, so the
   reservation is visible and the number isn't silently re-used.
4. **One topic per ADR.** Don't bundle.
5. **Every PR that makes an architectural decision includes an ADR.**
   Review-gate: ADR + code together or neither.

## Status values

- `Proposed` — under discussion; safe to move to `Accepted` only
  after the PR lands.
- `Accepted` — in force. Code adheres.
- `Superseded` — replaced by a later ADR; pointer in metadata.
- `Rejected` — proposed and explicitly turned down; kept so we don't
  re-litigate the same idea.

## Template

See [_template.md](_template.md) for the boilerplate.

## Index

| # | Status | Title | Landed |
| - | ------ | ----- | ------ |
| [0001](0001-horizon-deprecated.md) | Accepted | Horizon is not in the architecture | 2026-04-22 |
| [0002](0002-minio-s3-compat-storage.md) | Accepted | Self-hosted storage is S3-compatible, not local filesystem | 2026-04-22 |
| [0003](0003-i128-no-truncation.md) | Accepted | i128 / u128 preserved end-to-end; never int64 | 2026-04-22 |
| [0004](0004-tier1-validator-aspiration.md) | Accepted | Tier-1 three-validator aspiration | 2026-04-22 |
| [0005](0005-monorepo.md) | Accepted | Monorepo with one Go module | 2026-04-22 |
| [0006](0006-timescaledb-for-price-time-series.md) | Accepted | TimescaleDB for price time-series storage | 2026-04-22 |
| [0007](0007-redis-cache-schema.md) | Accepted | Redis as hot-path cache + rate-limit + ephemeral state | 2026-04-22 |
| [0008](0008-ha-topology.md) | Accepted | Per-region HA topology — colo primary + cloud DR, three-tier hot/warm/cold storage | 2026-04-27 |
| [0009](0009-latency-budget.md) | Accepted | API latency budget — per-component time slices summing to p95 ≤ 200ms / p99 ≤ 500ms | 2026-04-27 |
| [0010](0010-off-chain-fiat-representation.md) | Accepted | Off-chain fiat currencies as AssetType "fiat" | 2026-04-22 |
| [0011](0011-supply-algorithm.md) | Accepted | Three-domain supply algorithm — XLM hard-coded, classic from ledger entries, SEP-41 from event sums | 2026-04-27 |
| [0012](0012-quorum-set-composition.md) | *Planned* | Quorum-set composition (referenced by multi-region-topology) | — |
| [0013](0013-go-stellar-sdk-xdr-for-scval.md) | Accepted | Adopt go-stellar-sdk/xdr for SCVal decoding in source connectors | 2026-04-23 |
| [0014](0014-crypto-ticker-representation.md) | Accepted | Crypto tickers as AssetType "crypto" | 2026-04-23 |
| [0015](0015-last-closed-bucket-rate-serving.md) | Accepted | API rates served from last-closed bucket, never in-progress | 2026-04-27 |
| [0016](0016-per-region-storage-strategy.md) | Accepted | Per-region storage strategies (Hetzner full / AWS hybrid / Vultr hybrid) | 2026-04-27 |
| [0017](0017-archive-completeness-invariants.md) | Accepted | Archive completeness invariants — dual-archive integrity model with 4 hard contracts | 2026-04-27 |
| [0018](0018-api-consistency-surfaces.md) | Accepted | API consistency surfaces — closed-bucket / tip / observations, three URLs three contracts | 2026-04-28 |
| [0019](0019-anomaly-response-and-confidence-scoring.md) | Accepted | Anomaly response policy and confidence scoring — per-asset statistical baselines | 2026-04-28 |
| [0020](0020-chart-api-contract.md) | Accepted | Chart API contract — timeframe + granularity + price_type | 2026-04-30 |
| [0021](0021-account-entry-observer.md) | Accepted | AccountEntry observer — live home-domain + reserve-balance tracking | 2026-04-30 |
| [0022](0022-classic-supply-observers.md) | Accepted | Classic-supply observers — Trustline / ClaimableBalance / LiquidityPool / ContractData entry tracking | 2026-04-30 |
| [0023](0023-sep41-supply-observer.md) | Accepted | SEP-41 supply observer — mint / burn / clawback event-stream tracking | 2026-04-30 |
| [0024](0024-redis-ha-via-sentinel.md) | Accepted | Redis HA via Sentinel (not Cluster) | 2026-04-30 |
| [0025](0025-caddy-cloudflare-trusted-proxy.md) | Accepted | Caddy trusts Cloudflare for client-IP signal via CIDR-pinned static list | 2026-05-10 |
| [0026](0026-stablecoin-fiat-proxy-late-binding.md) | Accepted | Stablecoin → fiat proxy is late-binding aggregator policy, not eager ingest normalisation | 2026-05-10 |
| [0027](0027-lcm-cache-tiering.md) | Accepted | LCM cache tiering — local galexie-archive as hot, aws-public-blockchain as cold | 2026-05-20 |
| [0028](0028-rwa-asset-representation.md) | Accepted | Tokenized real-world assets as `AssetType = "rwa"` — RedStone RWA feeds (BENJI, GILTS, …) | 2026-05-27 |
| [0029](0029-soroban-events-landing-zone.md) | Superseded by [0034](0034-tiered-clickhouse-architecture.md) | soroban_events raw-event landing zone | 2026-05-25 |
| [0030](0030-per-source-coverage-invariant.md) | Accepted | Per-source coverage invariant | 2026-05-28 |
| [0031](0031-data-derived-coverage-signal.md) | Accepted | Coverage signal is data-derived from authoritative stores | 2026-05-29 |
| [0032](0032-per-source-tables-as-projections.md) | Accepted | Per-source tables are projections of soroban_events | 2026-05-29 |
| [0033](0033-completeness-verification-model.md) | Accepted | Completeness verification — substrate continuity, recognition, projection reconciliation | 2026-06-02 |
| [0034](0034-tiered-clickhouse-architecture.md) | Accepted | Tiered data architecture — ClickHouse raw lake, Postgres served tier (supersedes 0029) | 2026-06-05 |
| [0035](0035-factory-anchored-contract-gating.md) | Accepted | Factory-anchored contract gating for Soroban decoders (reverses match-broadly/filter-downstream) | 2026-06-12 |
| [0038](0038-network-explorer.md) | Accepted | Network explorer (full Stellar + Soroban) over the certified lake | 2026-06-14 |
| [0039](0039-soroban-contract-state-reader.md) | Accepted | Soroban contract current-state reader — read-time decode from the lake | 2026-06-18 |

## Related

- [docs/engineering-standards.md](../engineering-standards.md)
  §5.5 — why decisions live in ADRs, not scattered architecture
  docs.
