---
title: Runbook — verify-archive-unit-failed
last_verified: 2026-04-29
status: ratified
severity: P3
---

# Runbook — `ratesengine_verify_archive_unit_failed`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_verify_archive_unit_failed` |
| Severity | P3 (ticket — no immediate customer impact) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/verify-archive.yml` |
| Typical MTTR | 30 min (diagnosis) – several hours (re-run) |
| Impact | None immediate. The API still serves correct data from existing bytes. Cross-region trust property degrades if multiple consecutive nights fail (see the `_run_stale` page-level alert). |

## Symptoms

- The most-recent run of `verify-archive-tier-a.service` (R1's nightly
  chain-link integrity check) exited in `failed` state.
- Sustained for 5 minutes — long enough to rule out transient
  systemd state during a manual restart.

## Quick diagnosis (≤ 5 min)

```sh
# What state is the unit in?
ssh r1 'systemctl status verify-archive-tier-a.service'

# Why did the last run fail? Logs land in journald.
ssh r1 'journalctl -u verify-archive-tier-a.service --since "yesterday" --no-pager | tail -50'

# Has the daily archive-completeness run also been failing? A
# missing-file gap is the most-common cause of a verify-archive
# failure — both alerts will fire together.
ssh r1 'journalctl -u archive-completeness.service --since "yesterday" --no-pager | tail -20'
```

The journal output's last lines indicate the failure mode:

| Pattern | Cause |
| ------- | ----- |
| `chain-link mismatch at ledger N: expected H1 got H2` | Real corruption — escalate (see RCA) |
| `file does not exist: <ledger>.xdr.zst` | Missing file in galexie-archive; should also fire `archive_files_missing` |
| `context deadline exceeded` / `max-runtime` | Run hit the 8h cap; likely an upstream slowdown |
| `access denied` / `403` | AWS / MinIO credentials in `/etc/default/ratesengine-ops` rotated or wrong |

## Mitigation (≤ 15 min)

- [ ] **Re-run manually with verbose logging** to confirm the failure mode is reproducible and not a transient blip:
  ```sh
  ssh r1
  set -a; source /etc/default/ratesengine-ops; set +a
  /usr/local/bin/ratesengine-ops verify-archive \
    -config /etc/ratesengine.toml \
    -from 2 \
    -tier chain \
    -workers 8 \
    -max-runtime 1h
  ```
  A 1h budget on an 8-worker run gets enough coverage to confirm
  whether the original failure persists. Don't push the full
  run yet — the next scheduled timer will retry.
- [ ] **If the failure mode is `file does not exist`**: trigger a manual `archive-completeness fix` to backfill the missing file from the public-archive fallback chain:
  ```sh
  ssh r1 '/usr/local/bin/ratesengine-ops archive-completeness fix \
    -config /etc/ratesengine.toml -from 2'
  ```
  Re-run verify-archive afterwards.
- [ ] **If the failure mode is `chain-link mismatch`**: STOP. This is the corruption-detection signal the system exists to surface — do NOT auto-recover. Escalate per RCA.
- [ ] **Verification**: the next scheduled `verify-archive-tier-a.service` run completes cleanly. Watch the unit state via Prometheus or `systemctl is-active`.

## Root cause analysis

**Chain-link mismatch is the heavy case.** The chain-integrity check is what makes our archive trustworthy as a cross-region anchor; a real mismatch means either:

1. **Upstream archive corruption** — Stellar's published history archive disagrees with what we mirrored. Rare but real (see incident NNN if logged). Action: pull the offending ledger from a different upstream archive (SDF / Lobstr / SatoshiPay) and compare.
2. **Mirror corruption during transit** — the bytes we hold differ from what the upstream serves. Action: re-fetch the offending range; confirm the new copy verifies.
3. **Decoder bug** — verify-archive itself is misreading. Action: check git log for recent decoder changes; reproduce against the same range under the previous binary.

Gather for the postmortem:

- Full journal: `journalctl -u verify-archive-tier-a.service --since '24h ago'`
- The reported chain-mismatch ledger pair — the offending hash + the expected hash
- Comparison against another archive's same-range hashes
- Recent commits to `cmd/ratesengine-ops/` and `internal/ledgerstream/`

## Known false-positive patterns

- **Manual run started during scheduled run** — both runs race; one fails on a lock or duplicate work. Fix: don't start manual runs while the timer is active.
- **MinIO restart mid-run** — connection-reset surfaces as a chain-walk failure. The next scheduled run completes cleanly. Don't escalate unless the failure repeats two nights in a row.

## Related

- The page-level alert `ratesengine_verify_archive_run_stale` —
  fires when the unit hasn't completed cleanly in 36h+, indicating
  this ticket-level alert wasn't actioned in time.
- ADR-0016 — per-region trust model that this nightly run anchors.
- `docs/operations/archival-node-bringup.md` §"Per-region trust + verification model"
- The `archive-files-missing.md` runbook — adjacent failure mode that
  often co-fires with this alert.

## Changelog

- 2026-04-29 — initial draft alongside the L4.12 systemd-timer ship.
