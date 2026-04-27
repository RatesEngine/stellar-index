# Architecture Decision Records

Every significant architectural choice in Rates Engine is captured
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
| 0008 | *Planned* | HA topology (referenced by coverage-matrix S9.1) | — |
| 0009 | *Planned* | Possible follow-up to 0007 (referenced by coverage-matrix S9.2) | — |
| [0010](0010-off-chain-fiat-representation.md) | Accepted | Off-chain fiat currencies as AssetType "fiat" | 2026-04-22 |
| 0011 | *Planned* | Supply policy (referenced by coverage-matrix F2.4) | — |
| 0012 | *Planned* | Quorum-set composition (referenced by multi-region-topology) | — |
| [0013](0013-go-stellar-sdk-xdr-for-scval.md) | Accepted | Adopt go-stellar-sdk/xdr for SCVal decoding in source connectors | 2026-04-23 |
| [0014](0014-crypto-ticker-representation.md) | Accepted | Crypto tickers as AssetType "crypto" | 2026-04-23 |
| [0015](0015-last-closed-bucket-rate-serving.md) | Accepted | API rates served from last-closed bucket, never in-progress | 2026-04-27 |
| [0016](0016-per-region-storage-strategy.md) | Accepted | Per-region storage strategies (Hetzner full / AWS hybrid / Vultr hybrid) | 2026-04-27 |

## Related

- [docs/discovery/decisions.md](../discovery/decisions.md) — the
  Phase-1 decisions log these ADRs are extracted from. Read the
  discovery doc for the narrative; read the ADRs for the binding
  commitment.
- [docs/discovery/engineering-standards.md](../discovery/engineering-standards.md)
  §5.5 — why decisions live in ADRs, not scattered architecture
  docs.
