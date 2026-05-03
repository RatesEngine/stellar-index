---
title: Runbook — archive-files-missing
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `ratesengine_archive_files_missing`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_archive_files_missing` |
| Severity | P2 |
| Detected by | Prometheus rule in `deploy/monitoring/rules/archive-completeness.yml` |
| Typical MTTR | 5–15 min (auto-repair); 1–4 h (manual after auto-repair fails) |
| Impact | Reduced redundancy on cross-region integrity guarantee. API responses get `flags.reduced_redundancy = true` while the gap persists; rate data itself remains served correctly from CAGGs. |

## Symptoms

- Prometheus gauge `archive_files_missing{archive="galexie-archive"}` or `archive_files_missing{archive="cross-anchor"}` has been > 0 for > 4 h.
- The daily `archive-completeness.timer` ran (`archive_completeness_runs_total` incremented within last 26 h) but didn't get to zero on its own — the multi-source fallback chain failed for at least one file.
- The status page may already be in *Degraded performance* if R1 is affected, or *Operational* if only R2/R3.

## Quick diagnosis (≤ 5 min)

Identify which archive, which region, how many files, and which fallback layers were exhausted.

```sh
# 1. Which archive + region is affected
ssh r1 'curl -s localhost:9100/metrics | grep archive_files_missing'

# 2. Which fallback sources are degraded (gives a hint about the gap shape)
ssh r1 'curl -s localhost:9100/metrics | grep archive_completeness_repair_failures_total'

# 3. The current gap report (last successful detect run)
ssh r1 'cat /var/lib/galexie/last-completeness-report.json | jq ".missing | length, .missing[0:5]"'

# 4. Was AWS unreachable from r1?
ssh r1 'curl -sf -m 10 https://aws-public-blockchain.s3.us-east-2.amazonaws.com/v1.1/stellar/ledgers/pubnet/ -I | head -3'
```

If `archive="galexie-archive"` and AWS is reachable: most likely a transient AWS S3 throttling event. The next daily run typically clears it.

If `archive="cross-anchor"` and SDF is reachable: probably a per-checkpoint 404 in `core_live_001` that the fallback chain didn't resolve. Re-run with all fallback sources explicitly enabled (mitigation step 2).

If neither AWS nor SDF is reachable from R1: external network/source outage — this is a different incident, escalate to `network-uplink` runbook.

## Mitigation (≤ 15 min)

- [ ] **Step 1 — Re-run the daemon manually.** It's idempotent + only re-fetches what's still missing. Catches transient source-side hiccups that resolved since the cron last ran.

  ```sh
  ssh r1 'systemctl start archive-completeness.service'
  ssh r1 'journalctl -u archive-completeness.service -f --since="5 min ago"'
  ```

- [ ] **Step 2 — If step 1 didn't clear it, run with all fallback sources forced.**

  ```sh
  ssh r1 'ratesengine-ops archive-completeness fix \
    -input-file /var/lib/galexie/last-completeness-report.json \
    -workers 16 \
    -force-all-sources'
  ```

  `-force-all-sources` tries every layer (AWS → SDF 001/002/003 → all tier-1 validators → galexie scan-and-fill) for every file regardless of which one a previous run got from. Slower but exhaustive.

- [ ] **Step 3 — If step 2 still leaves files unfilled, log the residual list as a known incident** and escalate to the responder for `archive-completeness-stale`. The remaining files are unrecoverable from any public archive; recovery requires either:
  - Spinning up our own validator and catching up from peers (multi-week, ADR-0004 territory)
  - Out-of-band request to a full-archive operator

  Both are outside the scope of this runbook.

- [ ] **Verification:** `archive_files_missing` should drop to 0 within ~5 min after step 1 succeeds, or within the wall-clock duration of step 2. The `flags.reduced_redundancy` flag clears on every region's next health-poll cycle (~60 s after the gauge clears).

## Root cause analysis

For the postmortem, capture:

- The gap report JSON from `/var/lib/galexie/last-completeness-report.json` (which specific ledgers were missing, in which archive).
- The full `archive_completeness_repair_*_total` counter snapshot before mitigation, broken out per source.
- `journalctl -u archive-completeness.service` covering the last 3 daily runs (was the gap growing, stable, or sudden?).
- For AWS-source failures specifically: `mc admin info local`, the MinIO mc trace from r1, and the AWS S3 dashboard for `aws-public-blockchain` for the relevant time window.
- For SDF-source failures: `curl -sf -w "%{http_code}" https://history.stellar.org/prd/core-live/core_live_001/.well-known/stellar-history.json` from r1 to confirm SDF's archive is reachable + valid.

## Known false-positive patterns

- **First few minutes after a fresh region bring-up.** A new R3 (Vultr) box starts with an empty primary archive; the first daily run after bring-up will report a large missing-files gauge until `mc mirror` from R1 completes. Expected; suppress the alert for the bring-up window using the bring-up runbook's silencing step.
- **Network head moved past a partition boundary in the last hour.** The currently-open partition has `network_head − partition.start + 1` files, not 64,000; if the daemon misclassifies it as closed, it counts the not-yet-written ledgers as missing. The `archive-completeness` tool uses a staleness margin (1-min) but rapid partition rollovers around protocol-upgrade ledgers can briefly trip this. Resolves on its own within 1 hour.

## Related

- [ADR-0017](../../adr/0017-archive-completeness-invariants.md) — the policy decision (4 hard contracts).
- [archive-completeness.md](../archive-completeness.md) — the operational procedure overview.
- [archive-completeness-stale](archive-completeness-stale.md) — companion runbook for when the daily cron itself isn't running.
- [archive-divergence](archive-divergence.md) — different alert, fires when **content** of the archive diverges (hash mismatch); this runbook is for **presence** gaps only.
- Postmortems tagged `archive-files-missing` — `docs/operations/postmortems/`.

## Changelog

- 2026-04-27 — initial draft alongside ADR-0017.
