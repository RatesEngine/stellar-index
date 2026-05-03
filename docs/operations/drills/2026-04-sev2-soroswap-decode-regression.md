---
title: "2026-04 SEV-2 tabletop — Soroswap decoder regression after protocol upgrade"
date: 2026-04-30
type: tabletop
scenario: scenarios/sev2-source-decoder-regression.md
participants: [ash, ash, ash]
last_verified: 2026-05-03
---

# 2026-04 drill writeup — SEV-2 Soroswap decoder regression

Pre-launch tabletop dry-run executed solo (`ash` rotated through
oncall / scribe / commander roles), back-to-back with the SEV-1
Timescale failover drill on the same day. Satisfies the SEV-2
half of L5.7 / Task #76.

## Trigger

Copied verbatim from the scenario script:

> At 09:17 UTC, the dashboard's Ingestion → Decode errors panel
> begins to climb for the soroswap source. The rate goes from
> 0/s baseline to 3/s sustained over 5 minutes.
> `ratesengine_source_events_total{source="soroswap"}` rate is
> still positive (events ARE arriving) but every event is being
> rejected. Aquarius, Phoenix, and Comet are unaffected.
>
> Hypothetical context: Stellar mainnet upgraded to protocol 25
> ("Brillouin") yesterday at 23:00 UTC.

## Response narrative

T+0 = trigger at 09:17 UTC. Times simulated.

- **T+02:30** — `ratesengine_ingestion_decode_error{source="soroswap"}`
  paged via PagerDuty (P3 ticket; SEV-2 severity assigned per
  playbook §1.2). Acknowledged within the SEV-2 30-min window
  comfortably. Opened #incident-2026-04-30-soroswap-decode
  channel.
- **T+05:00** — Pulled `decode-errors.md` from the alert's
  `runbook_url` annotation. Quick-diagnosis flow checked:
  source label = `soroswap` (single-source failure, not
  protocol-wide); other sources healthy. **Recognised the
  pattern matches Typical root cause #2 ("Stellar protocol
  version bump") within ~3 min** — the Brillouin upgrade
  context made this fast.
- **T+08:00** — Pulled indexer logs:
  `decode: SCVal: unknown discriminant 99 at field 'amount'`
  repeating per event. The `unknown discriminant` phrasing is
  the canonical SCVal-enum-extension symptom; the runbook flags
  this in its decoder-vs-source-side discriminator.
- **T+10:00** — Confirmed events still arriving via
  `ratesengine_source_events_total{source="soroswap"}` still
  rising (~30/s baseline, unchanged). Confirms decoder-side
  failure, not source-stopped — followed runbook's discriminator
  cleanly.
- **T+12:00** — `ratesengine_aggregator_class_drop_spike` fires
  for affected pairs (XLM/USDC, XLM/USDT). Confirmed VWAP for
  these pairs has lost soroswap as a contributor; SDEX +
  Aquarius + Phoenix + Comet still contributing. **No customer-
  facing outage**, just degraded redundancy.
- **T+15:00** — Status page set to *Degraded performance*:
  "Soroswap source temporarily unavailable for some pairs. Other
  sources continue to provide price data." Used the SEV-2
  Initial template from sev-playbook.md §5.2.
- **T+18:00** — Customer DM (Freighter): "Why is the XLM/USDC
  price tracking only the SDEX side now?" Replied with the
  §5.3 internal template + an extra note: "Affected pairs may
  show elevated `flags.divergence_warning`; price is still
  served correctly from remaining sources."
- **T+25:00** — Cross-referenced stellar-core 25.0.0 release
  notes (simulated via `https://developers.stellar.org/...`
  changelog read). Confirmed SCVal type-tag enum extension
  introduced a new variant. Identified fix locus:
  `internal/scval/parse.go` switch statement.
- **T+45:00** — Drafted forward-fix plan:
  1. Add the new SCVal type-tag handler in `internal/scval`.
  2. Capture a golden-file fixture from a real protocol-25
     soroswap swap event.
  3. Decode-test against the fixture; CI green.
  4. Ship via the regular release process — no emergency channel
     because the failure is *fail-closed* (events dropped, not
     mis-decoded).
- **T+50:00** — Posted update: "Identified; forward-fix in
  progress; ETA 4h. No customer action needed; affected pairs
  continue to receive valid prices via remaining sources."
- **T+24h (simulated)** — After hotfix lands, ran the gap
  recovery: `ratesengine-ops backfill -from <protocol-25-activation> -to <fix-deploy> -source soroswap`.
  Triangulation rates auto-recompute on the next aggregator
  tick. Status page transitioned *Degraded* → *Operational*.

## Gaps observed

- **Detection latency at slow drift is not covered.** The
  scenario's 3/s decode-error rate trips the alert cleanly. A
  0.5/s slow drift wouldn't fire the alert at all and would
  silently degrade. Action: add a per-source ratio alert
  (`decode_errors / events_total > 5%`) — already a known gap
  in the scenario script's *Common gaps surfaced*.
- **No upstream-changelog watcher.** Discovery that "yesterday's
  protocol upgrade" caused the regression was operator-knowledge,
  not automated. A real on-call hire might miss the timing
  correlation. Action: RSS-or-similar watcher on
  `developers.stellar.org` release notes.
- **Backfill subcommand wiring is partial.** The runbook
  references `ratesengine-ops backfill -source <name>` but the
  source-filtered backfill flag isn't wired end-to-end yet
  (see `cmd/ratesengine-ops/main.go` — backfill takes
  `-from / -to` but per-source gating leans on
  `BackfillSafe` registry filter rather than CLI flag). Action:
  Land per-source `-source` flag + golden test before launch.
- **Status page transition between states wasn't muscle memory.**
  Found the right templates, but the *Degraded → Identified →
  Mitigated → Operational* transitions are easier to flow when
  rehearsed. Action: include in 3-person tabletop post-launch.
- **`flags.divergence_warning` ↔ class-drop-spike correlation
  isn't documented.** The customer-comms note about
  "elevated divergence_warning" was operator-improvised. The
  runbook should explicitly say "if class_drop_spike fires, the
  remaining-source consensus may produce elevated
  divergence_warning — flag this in customer comms." Action:
  add to `decode-errors.md` Mitigation section.

## Action items

- [x] **Cross-link `decode-errors.md` Mitigation to mention
      `flags.divergence_warning` correlation when class-drop-spike
      fires** — owner @ash, done in same PR.
- [ ] **Add per-source decode-error ratio alert** — owner @ash,
      due 2026-Q3 (post-launch alerting hardening).
- [ ] **Add stellar-core release-notes RSS watcher** — owner @ash,
      due 2026-Q3.
- [ ] **Wire `ratesengine-ops backfill -source <name>` flag end-to-end
      + integration test** — owner @ash, due 2026-Q3.
- [ ] **3-person tabletop with state-transition rehearsal** —
      owner @ash, due 2026-Q3.

## Score

| # | Criterion (verbatim from scenario) | Score | Notes |
| --- | --- | --- | --- |
| 1 | Did oncall acknowledge within 30 min? | pass | T+02:30. |
| 2 | Did the team find `decode-errors.md` on the first try? | pass | Followed alert's `runbook_url`. |
| 3 | Did anyone correctly link the timing to "yesterday's protocol upgrade" before T+25:00 narration? | pass | Linked at T+05:00 via Quick-diagnosis flow. |
| 4 | Did the team confirm decoder-side (not source-stopped) by checking `source_events_total` still rising? | pass | T+10:00. |
| 5 | Did the team avoid panic? | pass | SEV-2 path; no restart attempted. |
| 6 | Did the team correctly identify the fix-forward path? | pass | `internal/scval` update + golden fixture + ordinary deploy. |
| 7 | Did anyone propose `ratesengine-ops backfill` for gap recovery? | pass | T+24h step. |
| 8 | Did anyone surface `flags.divergence_warning` as the customer-facing degradation signal? | pass | Used in customer DM reply. |

**Overall:** pass.

## References

- Scenario script:
  [`scenarios/sev2-source-decoder-regression.md`](scenarios/sev2-source-decoder-regression.md)
- Runbook exercised:
  [`runbooks/decode-errors.md`](../runbooks/decode-errors.md)
- SEV playbook: [`../sev-playbook.md`](../sev-playbook.md)
- Launch-readiness backlog: L5.7 / Task #76

## Sign-off

- **Drill leader:** ash
- **Scribe:** ash
- **Posted action items by:** 2026-04-30
- **Promoted to playbook update:** yes — `decode-errors.md`
  cross-link to `flags.divergence_warning` lands in this PR.
