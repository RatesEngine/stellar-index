---
title: Audit 2026-06-30 — cold system audit + logic/coherence audit
status: ✅ COMPLETE — plans frozen + ALL 34 system areas + full Audit-2 executed with adversarial verification (0 Critical, ~24 High)
auditor: cold adversarial pass (new model), expanding all prior audits
---

> **Start here:** [03-synthesis.md](03-synthesis.md) (executive summary) ·
> findings in [01-cold-system-findings.md](01-cold-system-findings.md) (CS-###) +
> [02-logic-coherence-findings.md](02-logic-coherence-findings.md) (LC-###) ·
> flagship deliverable [fiat-split-implementation.md](fiat-split-implementation.md).

# Audit 2026-06-30

Two parallel adversarial audits of **Stellar Index**, commissioned cold:

1. **[Audit 1 — Cold system/site audit](PLAN-1-cold-system-audit.md)** — every
   file, interaction, doc, and piece of infrastructure. Correctness, security,
   data-integrity, operability. Expands (does not repeat) the prior audits
   (2026-04-29 → 2026-06-14, plus the page/SEO audits).
2. **[Audit 2 — Logic / product-coherence audit](PLAN-2-logic-coherence-audit.md)**
   — UX clunkiness and conceptual incoherence now that the product is
   **Stellar Index** (not the multi-chain "Rates Engine" it began as). Flagship
   case: the assets surface still mixes Stellar and non-Stellar (fiat, crypto-
   reference) assets; fiat belongs in a dedicated `/external/fiat-currencies`
   section.

Each audit is **planned first, then re-passed** until the plan is provably
comprehensive (coverage map with no white space), then **executed** area-by-area.

## Adversarial stance

Assume the system is wrong until proven right. For every claim a doc or comment
makes, find the code that contradicts it. For every "verified/complete/safe"
flag, find the input that breaks it. Prior audits are a *baseline to exceed* —
a new model should surface issues the prior passes missed (frontend security,
SSE/streaming correctness, supply-chain/CI, cross-package data-flow, the
ClickHouse lake, ansible correctness, accessibility, perf) and propose *better*
remediations, not just confirm old ones.

## Severity rubric

| Sev | Definition (audit-specific) |
|-----|------------------------------|
| **Critical** | Silent data loss/corruption; auth bypass / secret exposure; a served value that is wrong in a way that misleads a paying consumer or loses money; an invariant (ADR-0003 i128, one-writer, hash-chain) actually violable with a concrete input. |
| **High** | Correctness bug reachable in production; a security weakness needing a precondition; a data-integrity gap that degrades a headline guarantee; an ops failure mode with no alert. |
| **Medium** | Bug behind an unusual path; missing validation; doc that actively lies about behavior; resilience gap with a workaround. |
| **Low** | Papercut, inconsistency, dead code, minor drift. |
| **Info** | Observation / hardening suggestion / future-proofing. |

A finding is only **Critical/High if a concrete failing input or scenario is
named.** "This looks risky" without a repro is Medium at most.

## Logic-audit severity (Audit 2)

Product-coherence findings use a parallel scale keyed to user impact:
**P0** breaks the mental model / actively misleads (e.g. fiat listed as a
Stellar asset); **P1** notable friction or redundancy (duplicate routes);
**P2** polish.

## Finding-ID scheme

- `CS-###` — Cold-system-audit findings (Audit 1).
- `LC-###` — Logic/coherence findings (Audit 2).
- Cross-reference prior IDs (`F-####`) when a finding extends or reopens one.

## Process gates (Definition of Done for the *plans*)

A plan is "comprehensive" only when:
1. Every directory in the repo maps to ≥1 audit area (no unaudited white space).
2. Each area has an explicit **attack list** (what an adversary tries), not just a scope.
3. The plan names what prior audits covered and where THIS pass goes *further*.
4. At least two refinement passes are logged (see each plan's "Pass log").

## Layout

```
docs/audit-2026-06-30/
  README.md                       ← this file
  PLAN-1-cold-system-audit.md     ← Audit 1 plan (+ pass log)
  PLAN-2-logic-coherence-audit.md ← Audit 2 plan (+ pass log)
  01-cold-system-findings.md      ← Audit 1 findings register (CS-###)
  02-logic-coherence-findings.md  ← Audit 2 findings register (LC-###)
  ...per-area evidence files as needed
```
