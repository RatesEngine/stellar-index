# Dependency versions — pinned snapshots

**Purpose:** capture the exact commit SHAs, tags, and dates of every
third-party repo we read during discovery. These are the snapshots
every audit doc in this directory is **verified against**. If you
re-audit later against a different SHA and the facts change, those
changes need to flow into the audit docs.

**Captured:** 2026-04-22.

## Audited snapshots

| Repo | SHA | Last commit | Tag | Our dependency? |
| ---- | --- | ----------- | --- | --------------- |
| `stellar/stellar-galexie` | `6dec23e20802202e23d60a6505ead19898636e75` | 2026-04-01 | `galexie-v26.0.0` | Runtime binary — we run Galexie alongside our code, not link as a library. |
| `stellar/rs-stellar-archivist` | `a6a25033dc2dd1783314ff5b009123e6bfc00e7a` | 2026-04-20 | (no tag yet) | Runtime binary — we call it from scripts. Pin SHA since no tag. |
| `stellar/stellar-rpc` | `99a61f337b66635ba6f9d70d2403ee5faed1d7c1` | 2026-04-07 | (no tag visible locally) | Runtime binary — self-hosted RPC. |
| `stellar/go-stellar-sdk` | `9d52d04a911d9ee7451073e8178157a36e66cd42` | 2026-04-22 | (no tag visible locally) | **Go library — direct dep.** `v0.5.0` resolved by `go mod tidy` per the compat test. |
| `withObsrvr/stellar-extract` | `e3658ced9023bc30f0e19871987dd50270dfe192` | 2026-04-20 | `v0.1.2` | **Reference only — not a dep today.** Phase-1 eyed it for SDEX trade extraction; we ended up implementing that path against the SDK directly (`internal/sources/sdex/decode.go`). Kept as the reference fixture source per CLAUDE.md. |
| `stellar/stellar-etl` | `427d2e2565c8cc98c7a2fbc65305a314c114aa33` | 2026-04-09 | `v2.8.18` | Reference implementation + test-fixture source. Not a dep. |
| `withObsrvr/cdp-pipeline-workflow` | `741ac3d206be99dd22589b1ed4c6aa082f76c904` | 2026-04-16 | (no tag) | Not a dep. Reference only; contains verified bugs. |
| `withObsrvr/nebu` | `72eca11c148c03ab1d18dd945e828ada8f3c61f3` | 2026-04-13 | (no tag) | Design inspiration. Not a dep. |
| `withObsrvr/flowctl` | `2f0f4337d105aa7c813d0fc5d9a220e152a8a545` | 2026-01-19 | (no tag) | Reference only. Not a dep. |
| `reflector-network/reflector-contract` | `4c6368f5d66ae848adb9cfa2591198b54c4db6e1` | 2026-03-09 | `v6.0.0` | Oracle we read on-chain; ABI/interface verified against this SHA. |
| `soroswap/core` | `bb90a65556d8eee0dc698ac75de0f280e547fedc` | 2025-12-22 | (no tag) | Soroban AMM we index — contract ABI/events verified. |
| `AquaToken/soroban-amm` | `5ca19bb14ec421340ff4ca9e138ec877550940d7` | 2026-04-22 | `v2.0.2` | Soroban AMM we index. |
| `blend-capital/blend-contracts-v2` | `ba22b487b2c5057a4ecc28b05b5193c28e4bd117` | 2025-08-14 | (no tag) | Lending protocol we index (events only). |
| `Phoenix-Protocol-Group/phoenix-contracts` | `3af5ffafed41f1a5444f79ab1642cf9a7f0f59bc` | 2025-06-07 | (no tag) | Soroban DEX we index. |
| `CometDEX/comet-contracts-v1` | `ef4cbfad0a35202ad267c14d163d2f362995a8d3` | 2024-05-02 | `v1.0.0` | Weighted AMM we index (via Blend backstop minimum). |
| `bandprotocol/band-std-reference-contracts-soroban` | `90e22e1424d357e099118c978f5e7a66073aad8f` | 2024-02-29 | (no tag) | Oracle we read on-chain. |
| `redstone-finance/redstone-public-contracts` | `15133304d0c9eb775ccd3b02ead981280e35e0a6` | 2026-03-17 | (no tag) | Oracle we read on-chain (Adapter + 19 feeds). |
| `zenith-protocols/orbit-contracts` | `1d02ab3ec917ad5ad825567e19840924ab03811d` | 2026-01-23 | `v2.0` | Stablecoin issuer we ingest supply events from. |
| `stellar/stellar-ledger-data-indexer` | `3458befeafbb69dc3d59c3b737820fc22012b3a5` | 2026-04-16 | (no tag) | Reference Soroban-contract-data indexer. Not a dep. |
| `stellar/stellar-protocol` (for SEPs & CAPs) | `2fa80ace9b7e2d22b4ad6d722a8aa007abd29b02` | 2026-04-10 | (no tag, repo evolves) | Spec source — SEP-1/10/20/23/40/41, CAP-67 all verified against this SHA. |
| `stellar/stellar-docs` | `882abe547f16bbd16c0b8a4a2c98a962c95fde53` | 2026-04-21 | (no tag) | Docs source — hardware recommendations, validator admin guide, Hubble catalog. |

## Our actual production dependencies (shortlist)

At deploy-time we will pin these:

```go
// go.mod — verified to resolve + build 2026-04-22 per the compat test
module github.com/RatesEngine/rates-engine
go 1.25

require (
    github.com/stellar/go-stellar-sdk v0.5.0              // 9d52d04a (2026-04-22)
    // + our own deps (timescale driver, redis client, echo/chi, prometheus, etc.)
)
```

`withObsrvr/stellar-extract` was on the Phase-1 candidate list but
didn't make it into go.mod — the SDEX trade-extraction logic is
in `internal/sources/sdex/decode.go` running against the SDK
directly. Reference link kept in the audited-snapshots table
above.

Runtime binaries / Debian packages:

```
stellar-core      v26.0.1      (released 2026-04-13 per stellar-docs)
stellar-rpc       v26.0.0+     (pinned per SHA 99a61f33)
stellar-galexie   v26.0.0      (pinned per tag + SHA 6dec23e2)
rs-stellar-archivist  (pre-tag; pin SHA a6a25033)
```

On-chain contracts we read (the WASM hashes are the strong pin;
the repo SHA is how we audited the source that produced that hash):

| Contract | Mainnet address | WASM hash (verified via stellar.expert) |
| -------- | --------------- | --------------------------------------- |
| Soroswap Factory | `CA4HEQTL…VRYZ7AW2` | (factory hash) |
| Soroswap Pair template | (one per pair) | `18051456…770f73e` |
| Aquarius Router | `CBQDHNBF…WC6QUK` | `8844a760…1096fd` |
| Blend Pool Factory V2 | `CDSYOAVX…3G4QSU` | `31328050…d755ca9` |
| Blend Backstop V2 | `CAQQR5SW…PJKRG3IM7` | `c1f4502a…faffbc2` |
| Redstone Adapter | `CA526Y2N…HDXUSG` | (adapter hash) |
| Redstone per-feed (×19, identical hash) | (see oracles/redstone.md) | `3e464b6d…df5c` |
| Band (StandardReference) | `CCQXWMZV…3NFBGG5M` | (band hash) |
| Reflector — DEX | `CALI2BYU…OB2PLE6M` | (reflector hash) |
| Reflector — External CEX/DEX | `CAFJZQWS…JLN34DLN` | (reflector hash) |
| Reflector — Fiat FX | `CBKGPWGK…KOMJRN63` | (reflector hash) |

Full address tables live in the per-protocol audit docs.

## How to keep this file honest

Rule: **every claim in a `docs/discovery/**/*.md` audit doc must be
re-verifiable by checking out the SHA in that row.** If an audit
cites `stellar-extract/trades.go:55-57`, that line reference must
match at the pinned SHA `e3658ced…`.

Process when upgrading a dep:

1. Bump the SHA + tag + date in this file.
2. `git diff` the repo between old and new SHA on the specific
   files the audit doc references.
3. Update the audit doc with any changed facts — file paths, line
   numbers, struct shapes, event topics.
4. Re-run fixture tests against the new version (per the fixture
   plan in [adversarial-audit.md §7](adversarial-audit.md)).
5. Commit the audit-doc updates in the same PR as the dep bump.

## Related

- [adversarial-audit.md](docs/discovery/adversarial-audit.md) §11 — the "claims
  I stand behind" list is tied to these SHAs.
- [rfp-requirements-matrix.md](docs/discovery/rfp-requirements-matrix.md) — the
  RFP-requirement-to-audit-doc mapping implicitly depends on
  these snapshots.
