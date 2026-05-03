---
title: Runbook — core-lag
last_verified: 2026-05-03
status: draft
severity: P1
---

# Runbook — `ratesengine_stellar_core_ledger_age`

> **Deployment posture (2026-04-30).** stellar-core is **not running
> on r1** — the daemon was removed 2026-04-23
> ([r1-deployment-state.md §Services](../r1-deployment-state.md)).
> The metric `ratesengine_stellar_core_last_ledger_time_unix` has no
> producer, so this alert is *inert* on r1: there are no series to
> evaluate against. Galexie's embedded captive-core is intentionally
> not exposed to the prometheus exporter (the exporter scraped the
> standalone daemon's `/info`).
>
> The alert remains in `deploy/monitoring/rules/stellar.yml` for
> Phase-3 (Tier-1 validator rollout, ADR-0004); operators bringing
> a validator online will reactivate this signal by re-enabling
> `run_stellar_core` in the ansible role and exposing
> `stellar-core-prometheus-exporter`. Until then this runbook is
> *future-tense*: keep it discoverable rather than delete it.

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_stellar_core_ledger_age` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/stellar.yml` |
| Typical MTTR | 10 min – 2 h |
| Impact | stellar-core hasn't applied a ledger in > 60 s. captive-core for stellar-rpc also stalls. All downstream data (source events via RPC, archive publishing) stops. Everything behind this halts. |

## Symptoms

- `time() - ratesengine_stellar_core_last_ledger_time_unix > 60`
  for ≥ 2 min.
- `stellar-core-dbinfo` / `info` endpoint shows an old `current_ledger`
  timestamp.
- `rpc-lag.md` fires downstream shortly after.

## Quick diagnosis (≤ 5 min)

```sh
# Core's own view
curl -s http://stellar-core:11626/info | jq
#   Look at: status (Synced vs Syncing vs Catching up), current ledger,
#   quorum info, last close time.

# How many peers are we connected to?
curl -s http://stellar-core:11626/peers | jq '.peers | length'

# Any catastrophic log lines?
ssh root@<val-host> "journalctl -u stellar-core -n 200 --no-pager" \
  | grep -iE 'panic|fatal|deadlock|corrupt'
```

## Typical root causes

1. **Stellar network itself is having issues**. Rare but has
   happened. Check SDF's status page / #stellar-core on Keybase /
   stellar.expert/explorer — if the whole network is halted, you
   wait.

2. **We lost quorum** — too many of our configured quorum-set
   members are unreachable. Core refuses to close ledgers without
   quorum.
   - Signal: `info` shows `quorum` section with `disagree` count
     high; status stuck at "Syncing".

3. **captive-core catchup stalled** (for stellar-rpc). Out of RAM,
   out of disk, or hit a bug mid-replay.

4. **Corruption of the core DB**. Rare but possible after an
   unclean shutdown. `core new-db` + catchup is the recovery path.

## Mitigation

- [ ] Step 1 — network-wide or us? Cross-check via SDF's Horizon /
      stellar.expert. If the network is down, this is a P0 for
      Stellar, not for us.
- [ ] Step 2 — if quorum: verify our quorum set members are
      reachable. Update the quorum-set if a chosen validator is
      permanently offline.
- [ ] Step 3 — if catchup stalled: check disk space + memory +
      logs. Restart as a last resort (losing a few minutes of
      progress).
- [ ] Step 4 — if corruption: follow the `core new-db` + catchup
      procedure (captured in `bootstrap-archival-node.md`).
- [ ] Verification: `info` status returns to "Synced"; ledger age
      drops below 30 s; `rpc-lag.md` (if it fired) clears on its
      next evaluation.

## Root cause analysis

- Full `stellar-core` log for the incident window.
- Quorum-set config at time of incident (did someone change it
  recently?).
- Network status from external sources (SDF status page,
  stellar.expert's ledger-age panel).
- Hardware: was the host OOM / IOwait-pegged?

## Known false-positive patterns

- **Deliberate "catch up" mode after a restart** — stellar-core
  intentionally lags while it replays. The alert's `for: 2m` can
  absorb a quick boot, but a long catchup will trip it.
- **Network genuinely slow at times** — 5–6 s ledger close times
  during heavy traffic can tip the alert if you set the threshold
  too aggressively. Current threshold is 60 s which is well past
  any normal variation.

## Related

- `core-peers.md` — the "we're being cut off" variant.
- `rpc-lag.md` — downstream effect.
- `archive-publish.md` — can cascade.
- `bootstrap-archival-node.md` — recovery procedure for
  corrupted core.

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — top-of-file deployment-posture callout: this alert
  is inert on r1 (stellar-core removed 2026-04-23) and is retained
  for Phase-3 validator rollout. Avoids on-call confusion when the
  runbook is opened from an unrelated incident.
