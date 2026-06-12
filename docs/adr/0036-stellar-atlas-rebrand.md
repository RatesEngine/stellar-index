---
adr: 0036
title: Rebrand to Stellar Atlas; reposition as a protocol explorer
status: Accepted
date: 2026-06-12
supersedes: []
superseded_by: null
---

# ADR-0036: Rebrand to Stellar Atlas; reposition as a protocol explorer

## Context

The product launched as **Rates Engine** (ratesengine.net) — a Stellar
pricing API built against the Stellar/Freighter RFPs. The work since has
outgrown that name: the ADR-0033 completeness machinery, the ADR-0035
contract-identity gating with per-protocol verification pages
(`docs/protocols/`), the EVERY-event capture policy, and the certified
ClickHouse lake make the system's real identity **deep, verified,
per-protocol on-chain data** — of which prices are one product.

## Decision

1. **Name**: the product is **Stellar Atlas**, at **stellaratlas.xyz**.
2. **Positioning**: Stellar Atlas is a **protocol explorer for the
   Stellar network** — complete, verified coverage of every major
   protocol's contracts and events, with the pricing API as a flagship
   product — evolving toward a **comprehensive blockchain explorer**
   (classic/native and Soroban).
3. **Code identity**: module path `github.com/StellarAtlas/stellar-atlas`;
   binaries `stellaratlas-*`; env prefix `STELLARATLAS_*`; metric
   namespace `stellaratlas_*`.
4. **Historical record is immutable**: ADRs 0001–0035, `docs/discovery/`,
   `docs/audit-*/`, CHANGELOG history, and dated blog posts keep the
   Rates Engine name — they are records of what happened, and the repo
   policy (accept-only-or-supersede; immutable archives) applies to the
   brand exactly as it applies to decisions.

## Consequences

- Full mechanical rename of code, configs, deploys, web properties
  (the module-path + sweep commits); migration plan + r1 cutover runbook
  at `docs/operations/stellar-atlas-migration.md`.
- Prometheus metric history restarts under the new namespace (accepted:
  pre-launch, no external consumers).
- Operator follow-ups: DNS for stellaratlas.xyz, Cloudflare Pages domain
  attach, GitHub org creation + repo transfer, security@ mailbox.
- The name uses "Stellar" — review the SDF trademark/brand-usage policy
  before the public launch.

## Related

- ADR-0035 (the verification-page/protocol-explorer machinery that
  motivated the repositioning).
- `docs/operations/stellar-atlas-migration.md` (execution plan).
