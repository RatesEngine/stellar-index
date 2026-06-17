---
adr: 0001
title: Horizon is not in the Stellar Index architecture
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0001: Horizon is not in the Stellar Index architecture

> **Amendment (2026-06-12, F-1353 / D2-08).** Where this ADR (and
> ADR-0008 / ADR-0013) enumerate `stellar-rpc` and the `stellar-core`
> watcher as data sources in our ingest topology, note that those
> standalone services were **removed from production on 2026-04-23**.
> Production ingest is Galexie MinIO → dispatcher → decoders only
> (invariant 6); `stellar-core` survives solely as a captive-core
> subprocess of Galexie, and `stellar-rpc` only as the `rpc-probe`
> operator diagnostic. See
> [docs/architecture/ingest-pipeline.md](../architecture/ingest-pipeline.md).
> The decision below is preserved as the original record.

## Context

Horizon was the canonical client-facing API for the Stellar network
for most of the project's history. Most existing Stellar indexers,
explorers, and wallet integrations use it.

Two things shifted the ground under this:

1. **`stellar/go` (the monorepo housing Horizon's code path) was
   archived on 2025-12-16.** Horizon itself moved to a dedicated
   repo, but the Stellar developer narrative moved decisively to
   "use `stellar-rpc` + Galexie + the Ingest SDK for new builds."
2. **The Stellar Foundation has been actively promoting
   Galexie + stellar-rpc + Composable Data Platform** as the
   ecosystem's forward path. Horizon is on a maintenance track, not
   a feature track.

A project directive from 2026-04-22 also locked this: "Horizon is
deprecated and we will not be using it."

## Decision

Horizon is **not a component** of the Stellar Index architecture.

- We do not **run** Horizon.
- We do not **ingest from** Horizon.
- We do not **proxy to** Horizon.
- If a third-party integration's only path to us is via Horizon,
  we decline that integration until it supports a supported
  Stellar data path.

The Stellar data sources we use are:

- Our own `stellar-core` watcher(s) (non-validating).
- Our own `stellar-rpc` (self-hosted).
- Galexie writing to our MinIO object store.
- `rs-stellar-archivist` mirrors for history seeding.
- Direct Soroban contract reads (Reflector, Band, etc.).

## Consequences

**Positive**

- We align with the supported ecosystem direction; we don't build
  against an archived codebase.
- Simpler architecture (one ingestion path, not two).
- Our self-hosted deploy kit doesn't include Horizon — smaller
  footprint for operators.

**Negative**

- We forgo Horizon-compatible semantics some clients may expect.
  Mitigation: our OpenAPI spec is explicit about the Stellar Index
  contract; we don't market ourselves as a Horizon replacement.
- We must implement trade / effect / operation decoding ourselves
  (via `stellar-extract`) rather than delegating to Horizon's
  mature query surface.

**Operational impact**

- No Horizon binary or Horizon-Postgres in our colo or cloud tier.
- Runbooks never reference Horizon commands.
- Monitoring dashboards never track Horizon health.

**Downstream design impact**

- Our canonical trade type derives from `xdr.LedgerCloseMeta` →
  `LedgerTransactionReader` → `ClaimAtom`-based parsing. Horizon
  effect IDs are not part of our data model.

## Alternatives considered

1. **Run Horizon as part of our stack and use it for some queries.**
   Rejected: bifurcates the ingestion path, carries a deprecated
   code tree, does not align with ecosystem direction.
2. **Proxy to SDF's hosted Horizon (`horizon.stellar.org`) for
   certain queries.** Rejected: adds an external hard dependency
   on a deprecated service for a production SLA.
3. **Start with Horizon and migrate off later.** Rejected: the
   migration cost at scale would exceed the implementation cost of
   going direct-to-ledger from day one. Our architecture research is
   extensive enough that we know what we're building.

## References

- `stellar/go` monorepo archival:
  <https://github.com/stellar/go> (shows "Archived" banner).
