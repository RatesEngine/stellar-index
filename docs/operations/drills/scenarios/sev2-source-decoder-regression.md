---
title: SEV-2 tabletop — Source decoder regression after protocol upgrade
last_verified: 2026-04-30
status: ratified
severity: P2
exercises_runbook: ../../runbooks/decode-errors.md
playbook_section: ../../sev-playbook.md#4-response-flow
---

# SEV-2 tabletop — Source decoder regression after protocol upgrade

A scripted scenario for the monthly tabletop drill. ~30 min for
3 people. Exercises
[`decode-errors.md`](../../runbooks/decode-errors.md),
[`source-stopped.md`](../../runbooks/source-stopped.md), and the
SEV-2 path of [SEV playbook §4](../../sev-playbook.md).

This is a *deliberately less catastrophic* scenario than the
SEV-1 — many of the launch-readiness gaps surfaced are about
*detection latency* and *triage discipline*, not "we couldn't
recover."

## Initial conditions

Read aloud at drill setup.

- All sources healthy. `ratesengine_source_events_total` rate
  for every enabled source is within ±20% of baseline.
- Stellar mainnet upgraded to **protocol 25 (hypothetical
  "Brillouin" upgrade) yesterday at 23:00 UTC**. The Rates
  Engine team did not pre-test against protocol 25 because the
  testnet rollout was unstable.
- It is **09:15 UTC, Thursday**. Oncall is `<participant 1>`
  starting their shift. Backup is `<participant 2>`.

## Trigger event

Read aloud at drill T+0.

> At 09:17 UTC, the dashboard's *Ingestion → Decode errors*
> panel begins to climb for the **soroswap** source. The rate
> goes from 0/s baseline to 3/s sustained over 5 minutes.
> `ratesengine_source_events_total{source="soroswap"}` rate is
> still positive (events ARE arriving) but every event is being
> rejected.
>
> Aquarius, Phoenix, and Comet are unaffected.

## Injection timeline

Drill leader reads each beat in order; pauses after each for
participants to narrate their response.

| T+ | Beat |
| --- | --- |
| 0:00 | `ratesengine_ingestion_decode_error{source="soroswap"}` fires (>1/s for 5m). PagerDuty pages oncall (P3 → ticket; SEV-2 if it later escalates). |
| 0:05 | Oncall reading the alert + linked runbook. |
| 5:00 | Indexer logs grow: `decode: SCVal: unknown discriminant 99 at field 'amount'`. Repeats per event. |
| 8:00 | `ratesengine_aggregator_class_drop_spike` fires — VWAP for the affected pairs has lost soroswap as a contributor. |
| 12:00 | Customer DM: "Why is the XLM/USDC price tracking only the SDEX side now?" |
| 18:00 | Aquarius/Phoenix/Comet keep working perfectly. Soroswap-only failure. |
| 25:00 | Searching changelog: stellar-core 25.0.0 release notes mention an SCVal type-tag enum extension. Likely the cause. |

## Expected response per the playbook

Drill leader compares participant narratives against this
expected sequence.

### Within 30 minutes — acknowledge + diagnose

Per SEV-2 timelines in [§2](../../sev-playbook.md#2-timelines-the-contractual-promises):

- Oncall acknowledges in PagerDuty within 30 min (looser than
  SEV-1's 5 min).
- Open `#incident-<date>-soroswap-decode` channel — even though
  it's a SEV-2, the pattern matches and helps if it escalates.
- Apply [`decode-errors.md`](../../runbooks/decode-errors.md)
  quick-diagnosis flow:
  - Identify the source (alert label = `soroswap`).
  - Pull recent indexer logs grouped by error pattern.
  - Recognize the *Typical root causes* #2 entry: "Stellar
    protocol version bump."

### Within 1 hour — confirm root cause

- Check the public stellar-rpc (`https://mainnet.sorobanrpc.com`
  per the runbook's updated diagnostic command) for the network's
  current `protocolVersion`. Confirm 25 active.
- Cross-reference the soroswap pair contract: same WASM hash,
  but the SCVal type-tag space changed in the protocol bump.
- Confirm the issue is decoder-side, not source-side:
  `ratesengine_source_events_total{source="soroswap"}` is still
  rising, so events arrive — they just don't decode.

### Within 4 hours — mitigate

Per [`decode-errors.md`'s mitigation section](../../runbooks/decode-errors.md):

- **No emergency runtime fix** — events that fail decode are
  lost; this is a P3-grade alert specifically because there's
  nothing to "restart."
- Mitigation is forward-fix:
  - Identify the new SCVal type-tag in the protocol-25 spec.
  - Update `internal/scval` to handle the new enum value.
  - Add a golden-file fixture reproducing the regression.
  - Ship a hotfix; CI green; deploy via the regular release
    process (no emergency channel).
- During the fix window, communicate the affected pairs to
  customers: their `flags.divergence_warning` may be elevated
  because soroswap is no longer contributing; the price is
  still served from SDEX/Aquarius/Phoenix/Comet.

### Backfill after the fix

Once the decoder is updated and deployed:

- Re-run the indexer over the gap range:
  `ratesengine-ops backfill -from <protocol-25-activation-ledger> -to <now> -source soroswap`.
- The soroswap rows for the gap window are now correct;
  triangulation rates auto-recompute on the next aggregator tick.

## Validation criteria

Score `pass` / `partial` / `fail` per criterion.

| # | Criterion |
| --- | --- |
| 1 | Did oncall acknowledge within 30 min? |
| 2 | Did the team find `decode-errors.md` on the first try? |
| 3 | Did anyone correctly link the timing to "yesterday's protocol upgrade" before the leader narrated T+25:00? |
| 4 | Did the team confirm the issue is decoder-side (not source-stopped) by checking `source_events_total` is still rising? |
| 5 | Did the team avoid panic? (SEV-2, not SEV-1; restart isn't the move.) |
| 6 | Did the team correctly identify the fix-forward path (`internal/scval` update + golden fixture + ordinary deploy)? |
| 7 | Did anyone propose using `ratesengine-ops backfill` to recover the gap window after the fix? |
| 8 | Did anyone surface `flags.divergence_warning` as the customer-facing degradation signal? |

## Common gaps surfaced (from prior simulations)

- **Detection latency.** This drill's decode-error rate of 3/s
  is well above the 1/s alert threshold. But a 0.5/s slow drift
  wouldn't trip the alert at all and would silently degrade
  signal. Action item template: "Add a per-source ratio alert
  (`decode_errors / events_total > 5%`) to catch slow
  drifts."
- **Discovery from upstream changelogs is manual.** No watcher
  exists for the SDF protocol changelog. Action item template:
  "Add an RSS-or-similar watcher on `developers.stellar.org`
  release notes to flag protocol upgrades pre-activation."
- **Backfill subcommand is documented but might not exist.**
  The `decode-errors.md` runbook references `ratesengine-ops
  backfill` but the actual subcommand wiring is partial. Action
  item template: "Audit `ratesengine-ops backfill` for
  per-source / per-range support."

## Variant scenarios

- **Multi-source variant.** Replace soroswap with "every Soroban
  source." Tests whether the team correctly identifies a
  *protocol-wide* regression vs source-specific.
- **Subtle variant.** Decode errors run at 0.7/s — below the
  alert threshold. Triggered by customer report instead of
  PagerDuty. Tests detection of slow drift via
  customer-side signals (more important pre-launch when the
  user community is small).

## Pairs with

- [SEV-1 Timescale primary failover](sev1-timescale-primary-failover.md)
  — different severity tier; can be drilled together as a
  back-to-back if time permits.
