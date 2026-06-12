# Discovery & Data Source Audit

> **Brand note:** this archive predates the Stellar Atlas rebrand
> (ADR-0036) and keeps the original Rates Engine naming as historical
> record.

> **Status (2026-05-03):** Phase 1 closed and ratified 2026-04-22
> per [`phase1-closure.md`](phase1-closure.md). This tree is now
> read-only reference — historical record of how the architecture
> was decided. Active design work continues under
> [`docs/architecture/`](../architecture/) and [`docs/adr/`](../adr/);
> open operational issues live under
> [`docs/operations/`](../operations/) + the launch-readiness backlog.

This directory was the living knowledge base for Phase 1 discovery of
**Rates Engine** — our implementation of the Stellar Prices API RFP,
aligned with the Freighter asset-detail RFP.

The goal of Phase 1 is **not** to write ingestion code. It is to make an
informed, documented decision about:

1. **Where** every pricing-relevant piece of data lives (on-chain, public data
   lakes, third-party APIs, oracle contracts, CEX APIs, FX feeds).
2. **How** we should ingest it (self-hosted archival node, Galexie,
   Composable Data Platform, stellar-rpc, direct HTTP/WebSocket, etc.).
3. **What indexes and materialised views** we have to build on top of raw
   ledger data to serve the RFP SLAs (p95 ≤ 200 ms, 30 s freshness,
   indefinite 1h+ retention).
4. **What is missing, unreliable, or risky** — and the mitigation plan.

Self-hosting is preferred wherever practical. Every third-party dependency
must be justified, and every self-hosted option must be audited for hardware
footprint, sync time, maintenance burden, and data completeness.

## Structure

```
discovery/
├── README.md                     # this file (index)
├── decisions.md                  # firm project decisions (Horizon ❌, MinIO, …)
├── protocol-versions.md          # epoch / XDR / semantic-normalization research
├── rfp-requirements-matrix.md    # RFP requirement → data source mapping
├── data-sources/                 # direct ledger ingestion options
│   ├── galexie.md
│   ├── composable-data-platform.md
│   ├── stellar-data-lakes.md
│   ├── hubble-bigquery.md
│   └── archival-nodes.md
├── oracles/                      # SEP-40 and external oracle feeds
│   ├── reflector.md
│   ├── redstone.md
│   ├── chainlink.md
│   └── band.md
├── dexes-amms/                   # Stellar-native trading venues
│   ├── sdex.md
│   ├── soroswap.md
│   ├── aquarius.md
│   └── blend.md
├── external-refs/                # off-chain reference + CEX + FX
│   ├── coingecko.md
│   ├── coinmarketcap.md
│   ├── cex-feeds.md
│   └── fx-feeds.md
├── infrastructure/               # our infra choices
│   ├── storage-timescaledb.md
│   ├── cache-redis.md
│   └── hosting.md
└── notes/                        # open threads, questions, raw link dumps
    └── link-log.md
```

## Status legend (used inside each audit doc)

- ✅ **Recommended** — Proven fit, we plan to use it.
- 🧪 **Evaluating** — Not yet decided.
- ⚠️ **Caveat** — Usable but with known limitations worth tracking.
- ❌ **Rejected** — Considered and ruled out (reason documented).
- ❓ **Unknown** — Needs investigation.

## Standing audit checklist (per source)

Every `data-sources/*.md` and `oracles/*.md` doc should answer:

1. **What is it?** One-paragraph description.
2. **Maintenance & liveness** — Last commit, org, known incidents.
3. **Data completeness** — What fields / granularity / retention we get.
4. **Latency profile** — Freshness under normal and degraded conditions.
5. **Self-hostability** — Container image? Hardware? Licence?
6. **Failure modes** — What breaks and how we detect it.
7. **Redundancy / alternatives** — What fills the gap if this source fails.
8. **Verdict** — Status from the legend above, with rationale.

## Firm decisions & cross-cutting research

| Topic                          | Status | Doc                                          |
| ------------------------------ | ------ | -------------------------------------------- |
| **Self-audit of Phase 1 work** | ⚠️     | [adversarial-audit.md](adversarial-audit.md) — read before Phase 2 |
| **Existing CTX Rates (ref)** | 📚    | [existing-ctx-rates.md](existing-ctx-rates.md) — study for patterns, do not inherit |
| **RFP traceability matrix** | ⚠️    | [rfp-requirements-matrix.md](rfp-requirements-matrix.md) — every RFP requirement → audit doc, open gaps enumerated |
| **Proposal corrections** | ⚠️     | [proposal-corrections.md](proposal-corrections.md) — 11 findings that contradict/extend ctx-proposal.md |
| **VERSIONS.md** — pinned dep SHAs | ✅ | [VERSIONS.md](VERSIONS.md) |
| **Repo structure plan** | ✅ | [repo-structure-plan.md](repo-structure-plan.md) — monorepo layout, CI/CD, doc-staleness enforcement, ADR workflow, migration plan |
| **Engineering standards** | ✅ | [engineering-standards.md](engineering-standards.md) — tech-debt prevention, enterprise practices, agent-readability, doc-drift prevention |
| **Delivery plan (10 weeks)** | ✅ | [delivery-plan.md](delivery-plan.md) — week-by-week calendar + gates + risks |
| SEP-41 token events        | ✅     | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) |
| CAP-67 unified events      | ✅     | [notes/cap-67-unified-events.md](notes/cap-67-unified-events.md) |
| SEPs reference (1/10/20/23) | ✅    | [notes/seps-reference.md](notes/seps-reference.md) |
| Horizon ❌ (not in our arch)   | ✅     | [decisions.md](decisions.md)                 |
| MinIO for self-hosted storage  | 🧪     | [decisions.md](decisions.md)                 |
| i128 no-truncation invariant   | ✅     | [decisions.md](decisions.md)                 |
| Tier-1 3-validator aspiration  | ✅     | [decisions.md](decisions.md)                 |
| Protocol-version normalization | ✅     | [protocol-versions.md](protocol-versions.md) (SDK dispatches; ClaimAtom switch stays explicit; CAP-67 flagged) |

## Current high-level status

> Rows where **Doc** links to a file that does not yet exist are tracked
> work-items, not finished audits. The Status column on those rows
> reflects *initial assumption / priority*, not a verified verdict.
> Verified verdicts live inside the corresponding `.md` file.

| Area                       | Status | Doc                                                    |
| -------------------------- | ------ | ------------------------------------------------------ |
| Galexie                    | ✅     | [data-sources/galexie.md](data-sources/galexie.md)     |
| stellar-archivist (Rust)   | ✅     | [data-sources/stellar-archivist.md](data-sources/stellar-archivist.md) |
| stellar-ledger-data-indexer | ⚠️    | [data-sources/stellar-ledger-data-indexer.md](data-sources/stellar-ledger-data-indexer.md) |
| stellar-etl (Hubble's pipeline) | 📚 | [data-sources/stellar-etl.md](data-sources/stellar-etl.md) |
| getEvents v2 RPC           | 🧪     | [notes/getevents-v2-proposal.md](notes/getevents-v2-proposal.md) |
| withObsrvr ecosystem       | —     | [data-sources/withobsrvr-overview.md](data-sources/withobsrvr-overview.md) |
| └ stellar-extract          | ✅     | [data-sources/withobsrvr-stellar-extract.md](data-sources/withobsrvr-stellar-extract.md) |
| └ nebu                     | 🧪     | [data-sources/withobsrvr-nebu.md](data-sources/withobsrvr-nebu.md) |
| └ flowctl                  | ⚠️     | [data-sources/withobsrvr-flowctl.md](data-sources/withobsrvr-flowctl.md) |
| └ cdp-pipeline-workflow    | ❌     | [data-sources/withobsrvr-cdp-pipeline-workflow.md](data-sources/withobsrvr-cdp-pipeline-workflow.md) |
| Composable Data Platform   | ✅     | [data-sources/composable-data-platform.md](data-sources/composable-data-platform.md) |
| Public data lakes (incl. Hubble) | ✅ | [data-sources/stellar-data-lakes.md](data-sources/stellar-data-lakes.md) |
| Self-hosted archival node  | 🧪     | [data-sources/archival-nodes.md](data-sources/archival-nodes.md) (phase-1 watcher + 3-Tier-1-validator north star) |
| Reflector (SEP-40)         | ✅     | [oracles/reflector.md](oracles/reflector.md) (3 contracts, proposal corrections captured) |
| DIA (new discovery)        | 🧪     | [oracles/dia.md](oracles/dia.md)                       |
| Redstone                   | 🧪     | [oracles/redstone.md](oracles/redstone.md) (mainnet live; validation-only) |
| Chainlink                  | ⚠️     | [oracles/chainlink.md](oracles/chainlink.md) (HTTP cross-check until Scale lands) |
| Band                       | ⚠️     | [oracles/band.md](oracles/band.md) (contract verified, validation-only) |
| SDEX (Classic DEX)         | ✅     | [dexes-amms/sdex.md](dexes-amms/sdex.md) (3 ClaimAtom variants, 5 trade-producing ops) |
| Soroswap                   | ✅     | [dexes-amms/soroswap.md](dexes-amms/soroswap.md) (event schemas verified, proposal correction noted) |
| Aquarius                   | ✅     | [dexes-amms/aquarius.md](dexes-amms/aquarius.md) (3 pool types, central events module) |
| **Phoenix DEX** (new)      | ✅     | [dexes-amms/phoenix.md](dexes-amms/phoenix.md) (unusual 8-event-per-swap schema, mainnet addrs verified) |
| **Comet** (new)            | ✅     | [dexes-amms/comet.md](dexes-amms/comet.md) (Balancer weighted AMM, Blend backstop) |
| Blend                      | ✅     | [dexes-amms/blend.md](dexes-amms/blend.md) (secondary validation only, auctions as signals) |
| Residual DeFi (FxDAO/Orbit/Laina/Slender/DeFindex/EquitX/MaxFX/Hermes) | ⚠️ | [dexes-amms/residual-defi-protocols.md](dexes-amms/residual-defi-protocols.md) (covered via existing indexers) |
| Supply data (classic+SEP41) | ✅   | [data-sources/supply-data.md](data-sources/supply-data.md) |
| SEP-1 / home-domain        | ✅     | [data-sources/sep1-home-domain.md](data-sources/sep1-home-domain.md) |
| Infrastructure (HA design) | ⏳ **Deferred** — scaffold only, to be filled in after audit is complete | [infrastructure/README.md](infrastructure/README.md) |
| CoinGecko                  | ⚠️     | [external-refs/coingecko.md](external-refs/coingecko.md) (divergence detector) |
| CoinMarketCap              | ⚠️     | [external-refs/coinmarketcap.md](external-refs/coinmarketcap.md) (divergence detector) |
| CEX feeds                  | 🧪     | [external-refs/cex-feeds.md](external-refs/cex-feeds.md) (7 existing connectors doc'd as vendor-spec) |
| FX feeds                   | 🧪     | [external-refs/fx-feeds.md](external-refs/fx-feeds.md) (providers enumerated, selection open) |

All `❓` rows are blockers for finalising the architecture spec. All `🧪`
rows need a decision before Phase 2 ingestion work begins.
