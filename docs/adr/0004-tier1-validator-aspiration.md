---
adr: 0004
title: Tier-1 three-validator aspiration (post-launch)
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0004: Tier-1 three-validator aspiration (post-launch)

## Context

Stellar Index requires us to run **non-validating** watcher nodes
for data ingestion. Validator participation is **not** required for
the core indexing and pricing product.

However, the project's long-term posture is to become a
**Tier-1 organisation** on the Stellar network: an organisation
running **three geographically-separated full validators** with
independent history archives, included in the quorum sets of enough
other Tier-1 organisations to be load-bearing for network safety
and liveness.

Stellar's validator / Tier-1 playbook is documented in
`stellar-docs/docs/validators/tier-1-orgs.mdx`.

This ADR captures the commitment so infrastructure, procurement,
and security decisions in the interim do not close off the
Tier-1 path.

## Decision

Stellar Index's long-term operational posture is three Tier-1
full validators, geographically distributed, with:

- **One validator in our Vancouver colo** (our existing R640 +
  dedicated validator host).
- **Two validators in other geographic regions** (region selection
  during post-launch procurement; candidates include a second North
  American location and a European location).
- **Independent history archives** — one per validator. Each
  served via MinIO-behind-nginx with its own public DNS hostname.
- **SEP-20 self-verification** — our domain's stellar.toml
  publishes three `[[VALIDATORS]]` entries, one per validator.
- **HSM-protected seed keys.** No validator key material ever
  lives on a disk unencrypted.

Tier-1 inclusion is **emergent** — SDF does not grant it. We earn
it by proving uptime, coordinating with existing Tier-1 operators,
and being included in enough quorum sets. Target: apply for
widespread inclusion within 12 months post-launch.

## Consequences

**Positive**

- Full ownership of our data path from the network tip — no
  dependency on SDF's hosted Horizon / RPC / archive for
  production.
- Contribution to network decentralisation; strengthens our
  standing with ecosystem stakeholders.
- Differentiator against competitors: we operate infrastructure,
  not just a query layer.
- Product narrative: "the pricing API run by a Tier-1 Stellar
  validator" is materially stronger than "a pricing API."

**Negative**

- Capital cost: three dedicated machines (or machine-classes) +
  bandwidth + DC fees + HSM hardware.
- Operational cost: 24/7 uptime discipline, on-call rotation,
  protocol-upgrade voting responsibility.
- Lead time: HSM procurement (2-6 weeks), geographic data-centre
  setup, community participation (listening before applying) —
  all require weeks-to-months.
- Ongoing maintenance: stellar-core releases must be applied
  promptly; protocol votes must be cast correctly.

**Operational impact**

- Validator track runs as a **separate workstream** from the
  Stellar Index API. Separate runbooks, separate SLOs, separate
  on-call.
- Our three-validator identity is published via the same domain
  (`stellarindex.io`) as the API — a compromise of the domain
  affects both.

**Downstream design impact**

- Archive servers (one per validator) need high-bandwidth
  public egress. MinIO cluster sized to handle external
  archive-fetch traffic on top of Galexie internal writes.
- History archives are **published**, not just consumed. Our
  `rs-stellar-archivist mirror` use is one-way during the initial
  bootstrap; post-validator, we are also producing archives.
- SEP-20 stellar.toml publication path must be secured like a
  production secret — a compromise lets an attacker redirect
  trust to rogue validator keys.

## Timeline (non-binding)

| Phase | When | Milestone |
| ----- | ---- | --------- |
| Phase 1 | Pre-launch | Watcher nodes only. Validator prep in parallel: HSM vendor research, region research, Discord `#validators` presence. |
| Phase 2 | Months 1-3 post-launch | Testnet validator stood up. Source-of-truth playbook for ops. |
| Phase 3 | Months 3-6 | First pubnet validator (Basic). Observe uptime. |
| Phase 4 | Months 6-9 | Two more pubnet Basic Validators in separate regions. |
| Phase 5 | Months 9-12 | Promote all three to Full Validator (history-archive publishing). Apply for Tier-1 inclusion. |

Actual schedule depends on procurement lead times; this is
directional, not contractual.

## Alternatives considered

1. **Skip validator participation, stay as a pure data consumer
   forever.** Rejected: weaker narrative, leaves us dependent on
   SDF infrastructure we have no control over.
2. **Run one validator (not three).** Rejected: Tier-1 convention
   is three for redundancy. Running one is weaker than running
   none — it suggests we care but aren't serious.
3. **Run the three validators pre-launch.** Rejected: out of
   scope for the initial build; HSM + geographic lead times
   exceed the window; would divert attention from the core
   indexing and pricing product.

## References

- Stellar Tier-1 playbook (upstream):
  `stellar-docs/docs/validators/tier-1-orgs.mdx`.
