---
title: Runbook — archive-repair-source-degraded
last_verified: 2026-04-27
status: draft
severity: P3
---

# Runbook — `ratesengine_archive_repair_source_degraded`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_archive_repair_source_degraded` |
| Severity | P3 |
| Detected by | Prometheus rule in `deploy/monitoring/rules/archive-completeness.yml` |
| Typical MTTR | None — this is informational, not a customer-facing problem |
| Impact | None directly. One source in the multi-source fallback chain is failing > 10 % of fetches. The fallback chain is doing its job (other sources fill the gap) so customers see no impact, but if multiple sources degrade simultaneously this becomes the upstream of `archive-files-missing`. |

## Symptoms

- `archive_completeness_repair_failures_total / archive_completeness_repair_attempts_total` per source > 0.10 over a 1 h window.
- `archive_files_missing` may or may not be > 0 — usually still 0 because the fallback chain is compensating.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Which source is degraded?
ssh r1 'curl -s localhost:9100/metrics | grep archive_completeness_repair_failures_total'

# 2. Test the source directly
# AWS:
curl -sf -m 10 -I https://aws-public-blockchain.s3.us-east-2.amazonaws.com/v1.1/stellar/ledgers/pubnet/

# SDF core_live_001:
curl -sf -m 10 -I https://history.stellar.org/prd/core-live/core_live_001/.well-known/stellar-history.json

# Per-tier-1 validator (substitute URL):
curl -sf -m 10 -I https://bootes-history.publicnode.org/.well-known/stellar-history.json
```

If a public source returns 5xx persistently: their problem, not ours. The fallback chain is the right answer. Open an issue tracking the source's outage; close the alert once their status page returns to green.

If a public source is reachable but returns 404 for specific paths the daemon expects: their archive has retroactive gaps. This is genuinely lossy — same fix as `archive-files-missing` (let the next-tier source fill it).

## Mitigation (≤ 15 min)

This alert is informational. **No immediate operator action is required** unless multiple sources are simultaneously degraded.

- [ ] **If only one source is degraded:** verify the fallback chain is still successfully filling files (`archive_files_missing` should be 0 or trending down). If yes, no action.

- [ ] **If multiple sources are degraded:** at risk of `archive-files-missing` firing. Pre-emptively re-run the daemon to confirm completeness still holds, and watch the per-source metric.

  ```sh
  ssh r1 'systemctl start archive-completeness.service'
  ```

- [ ] **If AWS S3 (`source="aws"`) is degraded for > 4 h:** R2 is at risk because it reads ingest data from `aws-public-blockchain` directly (no local mirror). Check R2's ingest-lag metric; if R2 is also struggling, this becomes a P2 multi-region incident — escalate to the R2 ingest runbook.

## Root cause analysis

- Capture per-source counter snapshots from the last 24 h.
- Cross-reference with the source's public status page (AWS Health Dashboard, SDF / publicnode / lobstr / etc. status pages where available).
- For the postmortem: was this a known maintenance window we should have suppressed? If yes, document the schedule and add it to `deploy/monitoring/silences.yml`.

## Known false-positive patterns

- **First few hours after a fresh source URL is added.** A new tier-1 validator joining the fallback chain shows up with high failure rates initially while the daemon learns its layout quirks. Suppress for 24 h after adding a new source URL.
- **One specific checkpoint missing from one source.** A single 404 fails for that file, but the fallback handles it instantly. The metric still records the failure even though no customer-visible impact exists. Threshold (10 % over 1 h) is calibrated to ignore single-file misses.

## Related

- [ADR-0017](../../adr/0017-archive-completeness-invariants.md) — multi-source fallback chain design.
- [archive-files-missing](archive-files-missing.md) — fires when the fallback chain itself runs out of options.
- [archive-completeness.md](../archive-completeness.md) — the source list that this alert reports against.
- Postmortems tagged `archive-repair-source-degraded` — `docs/operations/postmortems/`.

## Changelog

- 2026-04-27 — initial draft alongside ADR-0017.
