# Deliverable-readiness plan — claiming the RFP deliverables

**Date:** 2026-06-12 · **Delivery deadline:** 2026-06-30 (delivery-plan
Week 10) · **Goal:** every acceptance criterion in the Stellar Prices
API RFP, the Freighter RFP, and the awarded proposal is met, evidenced,
and demonstrable.

## 0. What "deliverable" means (the acceptance criteria, verbatim)

From `docs/ctx-proposal.md` §Milestones and Acceptance Criteria +
`docs/stellar-rfp.md` §5 + `docs/freighter-rfp.md` SLAs:

| # | Criterion | Current state | Gap |
|---|---|---|---|
| AC1 | Real-time price staleness ≤ 30 s | `/price/tip` meets it (sla-probe pass); `/price` closed-bucket is structurally 30–150 s | Evidence doc + customer-facing definition |
| AC2 | p95 ≤ 200 ms (p99 ≤ 500 ms) | probe passes at low concurrency | k6 at contract load (1000 req/min) not yet run as evidence |
| AC3 | Historical retention ≥ 1 yr (ideally inception) | OHLC 1d to 2015; 1h+ indefinite | none — evidence only |
| AC4 | ≥ 1000 requests/min | rate-limit tier configured | k6 evidence |
| AC5 | All code publicly accessible + reproducible (open source, Tranche I+II) | repo PRIVATE | **the public flip — biggest open item** |
| AC6 | Production deployment ready ~10 weeks | r1 live, serving | multi-region question (§6) |
| AC7 | API reference docs + self-service onboarding | exist; docs domain pending DNS | DNS + E2E onboarding test |
| — | SEV-1 ≤15 min detect / ≤30 min respond; SEV-2 ≤30/≤60 | runbooks exist | timed drill not done |

## 1. Workstream A — complete the protocol sync (data truth)

The completeness verdicts read 15/15 `complete=true` (watermarks Jun
9–11) — but they were computed against the PRE-gate decoders and
PRE-0057-0061 schemas. Yesterday's deploy changed both, so the next
audit run will (correctly) flag work we already know about. Sequence:

**A1. Historical re-derives (r1, long-running; run serially under the
root<2G watchdog — CH-heavy jobs wedge the root volume otherwise):**

| Table(s) | Why | Mechanism |
|---|---|---|
| `blend_positions/emissions/admin/auctions` | pre-gate foreign-contract purge (ADR-0035) + `event_index` recovery (0053/0054/0058) | TRUNCATE + `ch-rebuild`/projector-replay from lake through the GATED decoder |
| `comet_liquidity` | `event_index` recovery (0059) | TRUNCATE + re-derive |
| `phoenix_liquidity`, `phoenix_stake_events` | `event_index` recovery (0060) | TRUNCATE + re-derive |
| `sep41_supply` | `event_index` recovery (0057) | TRUNCATE + re-derive (watched set only) |
| `trades WHERE source='soroswap'` | pre-gate foreign-row audit | verify-first: count rows from non-registered pairs; purge only if >0 (early pairs were defunct, expect ~0) |

**A2. Reconcile catalogue completion:** add `sep41-transfers` +
`sep41-supply` to the ADR-0033 reconcile (was deferred pending 0057 +
re-derive — A1 unblocks it). 15 sources → 17.

**A3. Fresh verdicts:** run `compute-completeness` across all sources
to current tip; target **17/17 `complete=true` with the gates active**.
This is the headline claim of the demo: *every protocol, verified
complete, provably.*

**A4. Protocol-team verification round:** send the five
`docs/protocols/` pages (blend, soroswap, phoenix, defindex, aquarius)
to the protocol teams. Their confirmations strengthen the deliverable
narrative; their answers unblock the remaining gates (phoenix static
list, defindex fan-out, aquarius enumeration, comet WASM-hash).
**Not deliverable-blocking** — the RFP requires price aggregation from
these venues (working), not contract-gating (our hardening) — but
cheap and high-credibility.

**A5. Steady-state automation:** completeness timer green on the new
binary names; gap-detector + census timers verified post-rename.

## 2. Workstream B — evidence refresh (the coverage matrix is the claim document)

`docs/architecture/coverage-matrix.md` is the RFP×proposal×delivery
traceability doc with a per-row `Prod` column — **last verified
2026-05-11 against rc.39**. Everything since (rc.40→rc.108, ADR-0033/34/35,
audit fixes, rebrand) is unreflected.

- B1. Re-run every curl probe (review-2026-05-10 Appendix B) against
  the current deployment; flip every `Prod` cell; resolve the 13 ⚠ and
  3 🟡 rows (most have since landed — verify, don't assume).
- B2. Re-baseline the matrix to the Stellar Index naming + current API.
- B3. Produce `docs/architecture/deliverable-evidence.md`: one page per
  acceptance criterion → evidence link (probe output, k6 report, matrix
  row, completeness snapshot). This is what the customer sees.

## 3. Workstream C — SLA evidence package

- C1. k6 suite (test/load, repaired in audit G22): `api_steady_state`
  (1000 req/min × keys × 30 min; pass p95≤200/p99≤500/err<0.1%),
  `api_ramp_to_saturation` (document the cliff), `api_spike` (10×,
  recover <60 s), `ingest_peak_ledger` (5× event rate, 1 h). Run
  against r1 from an external vantage; archive reports in-repo.
- C2. Chaos suite pass (test/chaos scenarios).
- C3. 7-day sla-probe evidence window (probe now passes post-cagg-fix;
  let it accumulate; export the pass series).
- C4. Freshness definition doc: `/price/tip` ≤30 s (RFP-meeting);
  `/price` closed-bucket semantics per ADR-0015/0018 (by design).
  Pre-agree this interpretation with the customer — do not let it
  surface at acceptance time.
- C5. SEV drill: kill the API (SEV-1 sim), time detection (target
  ≤15 min — alerts now live post-rename) + response (≤30 min);
  document in the SEV playbook as a dry-run record. Repeat a SEV-2
  (latency degradation).

## 4. Workstream D — the open-source public flip (AC5, hard requirement)

Strategy per the standing decision: **publish a NEW public repo at
v1.0; never force-push the private one.**

- D1. Pre-flip sweep: secret scan (the private history once contained a
  GCP key — the public repo gets a FRESH history, so build it from a
  clean export, not a history push), license headers (Apache-2.0),
  `*-data-validation-*.json`-class gitignores, VERSIONS.md current.
- D2. GitHub org `StellarIndex` (operator) → public repo
  `StellarIndex/stellar-index` matching the module path; CI green on
  the public repo (mind the Actions billing-cap pattern).
- D3. Reproducibility proof: clean-machine `make dev && make verify`
  walkthrough; self-host quickstart doc tested as written.
- D4. Release `v1.0.0`: CHANGELOG promote, cut-release.sh, signed
  artifacts, release notes. ONE release (no multi-RC churn).
- D5. Deploy.yml + release.yml dry-run under the new binary names
  (renamed but never exercised — test before relying on them for the
  launch deploy).

## 5. Workstream E — public-surface completion (operator-heavy)

- E1. DNS: api/docs/status/www.stellarindex.io (Caddy already serves
  api.stellarindex.io pending DNS; old domain keeps working).
- E2. Cloudflare Pages: attach explorer + status to the new domain.
- E3. Mailboxes: security@/hello@ (SECURITY.md already points there).
- E4. SDF brand-policy consent for "Stellar Index" (we're an RFP
  awardee; one email — do it before the public announcement).
- E5. Self-service onboarding E2E: signup → key → first authenticated
  call following docs/getting-started.md exactly; fix any drift.
- E6. Status page + incident comms templates verified on new domain.

## 6. Workstream F — the multi-region decision (biggest honest risk)

The proposal commits multi-zone deployment + 99.99% availability;
R2/R3 are deferred and r1 is a single host. Options:

1. **Provision R2 (AWS) minimally before claim** — serving-tier replica
   + API behind weighted DNS; galexie reads aws-public-blockchain per
   ADR-0016 (no mirror needed). ~2–4 days of ops work, real HA story.
2. **Disclose + schedule** — claim with single-region + documented DR
   (backups, restore runbook, archival-node-bringup tested) and a
   dated R2 commitment. Honest, faster, customer-dependent.

**Recommendation: option 1 if the calendar holds after Workstreams A–D
land by ~Jun 20; otherwise option 2 with explicit customer agreement.**
Decide by Jun 18. Either way: pgbackrest restore drill + DR runbook
walk-through happen this window.

## 7. Sequencing to 2026-06-30

| Window | Focus |
|---|---|
| Jun 12–15 | A1–A3 re-derives + fresh verdicts (long-running; start NOW). B1–B2 matrix re-verification in parallel. A4 send protocol pages. E1–E3 DNS/Pages/mailboxes. |
| Jun 15–19 | C1–C2 k6 + chaos (after A-jobs free r1 headroom); C5 SEV drills; D1 pre-flip sweep; D5 workflow dry-runs; E5 onboarding E2E. |
| Jun 18 | **F decision point** (R2 vs disclose). |
| Jun 19–24 | D2–D4 public flip + v1.0.0; B3 evidence doc; E4 SDF consent; F execution. |
| Jun 24–27 | Customer demo + sign-off; public announcement per Week-10 plan. |
| Jun 27–30 | 24-h post-launch watch; buffer. Claim. |

## 8. Explicitly OUT of deliverable scope (do not let it creep)

The explorer UX plan (P1–P6), the `/v1/protocols*` API surface, the
remaining ADR-0035 gates (phoenix/defindex/aquarius/comet), and the
network-explorer pillar are **post-deliverable roadmap**. They
strengthen demos but are not acceptance criteria; nothing here blocks
on them.
