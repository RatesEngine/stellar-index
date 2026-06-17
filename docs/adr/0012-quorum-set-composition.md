---
adr: 0012
title: Quorum-set composition (placeholder)
status: Proposed
date: 2026-05-12
supersedes: []
superseded_by: null
---

# ADR-0012 — Quorum-set composition

**Status:** Proposed (placeholder — full ADR pending Phase 3
validator rollout per ADR-0004. README index lists this number as
*Planned* until the content lands.)

## Why this number exists

The ADR numbering had a gap — `docs/adr/` jumped
0011 → 0013 with no file at 0012-*.md. ADR-0004 (the three-validator
aspiration) references "the future Quorum-set composition ADR"; the
README index at [docs/adr/README.md:56](README.md) has been listing
the number as `Planned`. This placeholder
fills the numeric slot so anyone walking the directory sees an
intentional reservation rather than a missing file.

## When this gets written

When the Tier-1 validator work begins (ADR-0004 Phase 3), this file
gets replaced with the full ADR covering:

  - Which third-party validators we include in our quorum set
    (SDF / LOBSTR / Satoshipay / Whalestack / Franklin Templeton
    are the current shortlist per the operator survey).
  - The HALT-LIVE-DROP scoring methodology we apply when one
    validator behaves poorly.
  - Cross-region quorum overlap requirements (does R1's quorum
    set need to differ from R2's / R3's? — likely no, but
    settle that here).
  - The thresholds and majorities we configure on stellar-core's
    `[QUORUM_SET]` block.

## Invariants the future ADR must preserve

  - Tier-1 status (per ADR-0004): three independent regions, three
    validator keys, three history archives.
  - Quorum set MUST NOT include any validator we operate (would
    void the independence claim).
  - No validator may have > 33% effective weight (Stellar's
    safety threshold per the consensus protocol).

## Cross-references

  - [ADR-0004](0004-tier1-validator-aspiration.md) — the parent
    Tier-1 commitment.
  - [ADR-0008](0008-ha-topology.md) — the HA shape this composes
    into.
