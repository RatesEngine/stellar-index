---
title: "[SEV-2] ClickHouse log-channel wedge fills root → Postgres crashes — 2026-06-11"
date: 2026-06-11
severity: SEV-2
status: resolved
started_at: 2026-06-10T22:09:00Z
resolved_at: 2026-06-11T09:31:00Z
affected_components:
  - postgres
  - clickhouse
  - indexer
postmortem:
---

# [SEV-2] ClickHouse log-channel wedge fills root → Postgres crashes

## What happened

During heavy SDEX completeness recovery + re-verify work (sustained
ClickHouse query load), r1's 49 GB root filesystem filled to 100%.
That tipped ClickHouse into a **self-sustaining log-spam loop**:
CH's server log lived on the root FS (`<log>/var/log/clickhouse-server/
clickhouse-server.log`, 1000M × 10 rotation). On a full root the
size-based rotation throws, and CH's async logging channel wedges —
emitting `Cannot log message in OwnAsyncSplitChannel channel` +
`Poco::RotateBySizeStrategy::mustRotate` stack traces at **~80,000
lines/minute** to journald → rsyslog → `/var/log/syslog`. That
re-filled root faster than it could be cleared (~3.8 GB/min), and a
full root crashed **Postgres twice** (it couldn't write its log;
`postgresql@15-main` failed to restart until root was freed).

The loop was driven by the *live indexer's* ongoing CH activity, not
just the operator's recovery load — so it persisted after the heavy
jobs were killed and survived three rounds of freeing root (the wedged
channel does not reset just because space reappears).

## Why this surface

PG's data is on the 9 TB ZFS pool (`data/postgres`), which was never
short of space. But PG writes its **log** to `/var/log/postgresql` on
the 49 GB root, and 17 GB of that root is a swap file (`/swap_f1209`),
leaving little headroom. When root hit 0, PG's log write failed and the
cluster went down — a ZFS-tier health check would never have caught it.

## Timeline (UTC)

- **2026-06-10 22:09** — root hits 100% under recovery load; PG log stops; cluster crashes.
- **2026-06-11 ~00:11–09:26** — diagnosis: root 100% (not the ZFS data vol); 3.7 GB PG log + 9 GB syslog; PG won't restart on full root.
- **09:26** — operator-authorized truncation of `syslog` + PG log frees root to 9.8 GB; PG restarted; `log_min_duration_statement=-1`, `log_statement=none`, `log_connections=off` applied (PG-side spam).
- **09:29** — root re-fills to 2.6 GB in minutes; CH still at ~20k lines/min — identified `OwnAsyncSplitChannel` wedge; freeing root alone does **not** reset it.
- **09:31** — CH `<log>` moved to the ZFS pool (`config.d/zzz-logpath.xml`) + `systemctl restart clickhouse-server`. Spam → 0; root stable at 9.8 GB free.

## Root cause + remediation

Root cause: ClickHouse's server log on the chronically-tight root FS,
combined with a full root, wedges CH's async log channel into a spam
loop. Applied + codified (`configs/ansible/roles/archival-node/tasks/
15-log-discipline.yml`):

- **CH logs moved to the ZFS pool** (`/var/lib/clickhouse/logs/`) via
  `config.d/zzz-logpath.xml` — the pool never fills, so the channel
  can't wedge. This is the source fix.
- **rsyslog filter** `if $programname == "clickhouse-server" then stop`
  — keeps any future CH journald flood off `/var/log/syslog`
  (belt-and-braces; `journalctl -u clickhouse-server` still has it).
- **PG logging quieted** — `log_min_duration_statement=-1` +
  `log_statement=none` + `log_connections=off` (slow-query + connection
  logging was the PG-side contributor under load).

## Follow-ups

- [ ] **Root FS is chronically too small (49 GB, 17 GB of it swap).**
      The real durable fix is moving `/var/log` (and ideally the swap)
      onto the ZFS pool, or resizing root. This is the third root-fill
      incident in the class (2026-05-10, 2026-05-13, 2026-06-11).
- [ ] **Heavy ClickHouse jobs must run with a root-disk watchdog.** The
      SDEX re-verify now runs under one (`/tmp/verify-sdex-wd.sh` kills
      on root < 2 GB); fold this into the ops tooling.
- [x] CH log path + rsyslog filter codified in the archival-node role.

## Lessons learned

- A full root is not just "logs pile up" — it can wedge a *service's*
  logging into a feedback loop that fills root faster than you can
  clear it. Freeing space does not reset a wedged async channel; the
  service must be restarted.
- The disk-fill alert class (`ratesengine_node_root_disk_*`, shipped
  after 2026-05-10) is correct but has no pager on the dev host, so it
  was discovered via downstream symptoms (PG down) again.
- Operator recovery load on shared infra (CH) can trigger
  service-level failure modes; heavy jobs need disk guardrails.
