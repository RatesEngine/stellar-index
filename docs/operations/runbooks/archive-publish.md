---
title: Runbook — archive-publish
last_verified: 2026-05-03
status: draft
severity: P3
---

# Runbook — `ratesengine_stellar_archive_publish_fail`

> **Deployment posture (2026-04-30).** stellar-core is **not running
> on r1** — the daemon (and its archive-publishing path) was removed
> 2026-04-23
> ([r1-deployment-state.md §Services](../r1-deployment-state.md)).
> `/srv/history-archive` was filled by `stellar-archivist mirror`
> (one-shot, completed) and is read-only today via the verify-archive
> Tier-A integrity check. No process actively *publishes* to it.
>
> The metric `ratesengine_stellar_archive_publish_errors_total` has
> no producer, so this alert is *inert* on r1. It remains in
> `deploy/monitoring/rules/stellar.yml` for Phase-3 (Tier-1
> validator rollout, ADR-0004) when stellar-core resumes
> checkpoint-publish duty. Until then this runbook is *future-
> tense*.

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_stellar_archive_publish_fail` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/stellar.yml` |
| Typical MTTR | 1–4 h |
| Impact | stellar-core couldn't publish a checkpoint to our history archive. Not customer-visible. Matters for Tier-1 posture (ADR-0004) — we advertise continuous archive publishing. Sustained failures reflect badly on validator quality scores. |

## Symptoms

- `increase(ratesengine_stellar_archive_publish_errors_total[1h]) > 0`
  for ≥ 1 h.
- `stellar-core/info` may show a `history_archive_state` that's
  not `Published` for recent checkpoints.
- Archive scanners (GitHub's archive-divergence checker, LOBSTR's
  validator scoring) flag us as lagging on history.

## Quick diagnosis (≤ 5 min)

```sh
# stellar-core publisher logs
ssh root@<val-host> "journalctl -u stellar-core -n 500 --no-pager" \
  | grep -iE 'history|publish|upload'

# Can we write to the archive backend? (S3 / MinIO)
mc ls myminio/history-archive/live/ | tail   # adjust alias
mc stat myminio/history-archive/

# Space + permission on the archive bucket
mc admin info myminio
```

## Typical root causes

1. **Archive backend (MinIO / S3) outage or auth failure**.
   Credentials rotated without updating core, bucket policy
   changed, bucket full.
   - Mitigation: fix auth; confirm bucket has capacity.

2. **Network egress broken** from the stellar-core host to the
   archive endpoint.

3. **Core compiled / configured wrong**. The `HISTORY` archives
   section in `stellar-core.cfg` has wrong `put` / `get` / `mkdir`
   commands. Less common but has happened after an upgrade.

4. **Disk full on the core host** — it stages checkpoints before
   uploading. If `/tmp` or the staging dir is out of space, upload
   fails before it even starts.

## Mitigation

- [ ] Step 1 — look at core's log to see the specific upload
      error.
- [ ] Step 2 — fix the backend cause (auth / space / network).
- [ ] Step 3 — core retries the publish on its next checkpoint
      (every 64 ledgers, ~5 min). No manual retry needed.
- [ ] Step 4 — if gaps exist in the archive, run the
      archive-repair procedure: `stellar-core publish` for specific
      checkpoints. Documented in
      `bootstrap-archival-node.md`.
- [ ] Verification: `increase(... errors_total[1h]) == 0`; archive
      scanners show us caught up.

## Known false-positive patterns

- **Very brief transient** — S3 returns a 503, core retries,
  successfully publishes on retry. Counter still went up. The
  alert's `for: 1h` threshold filters these.
- **Deliberate archive cutover** — during a storage migration we
  may temporarily disable publishing. Silence during the window.

## Related

- `archive-divergence.md` — when what we publish differs from
  other validators (much worse than not publishing at all).
- `db-disk-full.md` — staging-dir disk-full variant.
- ADR-0004 (three-validator + independent archives).

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — top-of-file deployment-posture callout: this alert
  is inert on r1 (stellar-core removed 2026-04-23, no active archive
  publisher). Retained for Phase-3 validator rollout.
