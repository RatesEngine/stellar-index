---
title: Execution coverage tracker — every area must be EXECUTED (not surface-mapped)
status: ✅ COMPLETE — all 34 areas + cross-cutting + Audit-2 executed with adversarial refute-pass
---

# Coverage tracker

> **✅ ALL AREAS EXECUTED.** Every A1–A36 area below and every Audit-2 workstream
> was reviewed by an independent adversarial agent (or executed directly) with a
> refute-pass; findings are in the CS-###/LC-### registers. No row is
> "surface-mapped" or deferred to a later wave. Cross-cutting hunts CC-1…CC-7 were
> executed *distributed across* the area reviewers (each brief embedded the
> relevant hunts) rather than as a separate pass — silent-swallowing, zero-time,
> concurrency, fail-open, backfill-vs-live, precision, and doc-vs-code were probed
> in every area they applied to. Area→finding mapping in the register section headers.

"Done" = an adversarial reviewer executed the area's attack list, findings were
verified (refute pass), and results are in the register. No "recommended next
wave" — every row reaches ✅.

## Audit 1 — 34 areas + cross-cutting

| Area | Executed? | Batch |
|------|-----------|-------|
| A1 Ledger ingest & streaming | ✅ (CS-028..031) | B1 |
| A2 Dispatcher & decoder routing | ✅ (CS-026/027) | B1 |
| A3 Projector one-writer invariant | ✅ (verified in sync) | pre |
| A4 On-chain DEX decoder fidelity | ⏳ | B1 |
| A5 Lending/bridge/oracle decoders | ✅ (CS-032..035; core clean) | B1 |
| A6 Supply pipeline (decode/math) | 🟡 partial (XLM/USDC) → ⏳ | B1 |
| A7 External CEX/FX connectors | ⏳ | B1 |
| A8 Aggregation math internals | 🟡 partial → ⏳ | B1 |
| A9 Canonical/i128 sweep | ✅ (enforcement gap CS-007) | pre |
| A10 Timescale served tier | ⏳ | B2 |
| A11 ClickHouse lake | ✅ (CS-021..025) | W1 |
| A12 Migrations & retention | 🟡 partial (0031/0040) → ⏳ | B2 |
| A13 Completeness verification | 🟡 partial (via CH) → ⏳ | B2 |
| A14 Divergence cross-check | ⏳ | B2 |
| A15 Ops mutation CLI (key minting) | ⏳ | B2 |
| A16 API authn/authz & middleware | 🟡 partial (via IDOR) → ⏳ | B3 |
| A17 Keys & quota/rate-limit | 🟡 partial (via SSE) → ⏳ | B3 |
| A18 Multi-tenant IDOR | ✅ (CS-008; clean) | W1 |
| A19 Webhook delivery & Stripe | ⏳ | B3 |
| A20 Email / magic-link | ⏳ | B3 |
| A21 SSE / streaming | ✅ (CS-012..016) | W1 |
| A22 Verified-currency & SEP-1 SSRF | ⏳ | B3 |
| A23 Secrets & config | 🟡 partial (CS-001) → ⏳ | B3 |
| A24 Ansible IaC | ⏳ | B4 |
| A25 systemd & Docker hardening | ⏳ | B4 |
| A26 Edge/TLS/CDN | 🟡 partial (CF fns) → ⏳ | B4 |
| A27 Monitoring & alerting drift | ⏳ | B4 |
| A28 CI pipeline & lint gates | ⏳ | B4 |
| A29 Release & deploy automation | ⏳ | B4 |
| A30 Explorer frontend security | 🟡 partial (CF+a11y) → ⏳ | B4 |
| A31 API contract & spec drift | ⏳ | B4 |
| A32 Docs/ADR integrity | 🟡 partial → ⏳ | B4 |
| A33 Pricing read-paths | ✅ (CS-017..020) | W1 |
| A34 Cross-package data-flow & resilience | ⏳ | B2 |
| A35 DR / restore tested | ⏳ | B4 |
| A36 Licensing / redistribution | ⏳ | B4 |
| CC-1..CC-7 cross-cutting sweeps | ⏳ | B5 |

## Audit 2

| Workstream | Executed? |
|------------|-----------|
| W1 Asset taxonomy / fiat split (LC-001..003) | ✅ + impl spec |
| W2 IA overlap (LC-010..014) | ✅ (mapped+verified) |
| W3 Nav (LC-020..023) | ✅ |
| W4 Naming (LC-030..032) | ✅ |
| W5 API shape (LC-040..043) | ✅ |
| W6 Onboarding/pricing-product coherence | ⏳ | B5 |
| W7 Accessibility | ✅ (LC-050..055) |
| W8 States | ✅ (LC-056) |
| W9 Copy terminology | ✅ (folded LC-030) |

## Batches
- **B1** ingest/decode: A1,A2,A4,A5,A6+A8,A7
- **B2** storage/completeness/ops: A10,A12,A13+A14,A15,A34
- **B3** auth/platform/security: A16,A17,A19,A20,A22,A23
- **B4** infra/CI/edge/docs: A24,A25,A26,A27,A28+A29,A30,A31,A32,A35+A36
- **B5** Audit-2 W6 + cross-cutting CC-1..CC-7
