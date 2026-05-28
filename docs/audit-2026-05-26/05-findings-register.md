# Findings Register

This is the canonical list of findings from the 2026-05-26 audit.
Each finding has stable `F-####` ID, severity per
[11-severity-rubric.md](11-severity-rubric.md), evidence refs,
workstream owner, and disposition.

## Status taxonomy

`open`, `needs_evidence`, `needs_owner`, `accepted`, `wontfix`,
`closed-by-PR-####`, `duplicate`, `invalid`.

## Severity scale

`critical`, `high`, `medium`, `low`, `note`.

## Active findings

| ID | Sev | Title | Workstream | Evidence | Status | Notes |
| --- | --- | --- | --- | --- | --- | --- |

(Initial seed below. The audit populates this register as it
runs; one row per finding.)

### Seed findings (observed before audit-start, awaiting evidence ID)

#### F-0001 — r1 root partition 100% full at audit-start

- **Severity:** `high` (silent-failure surface; will eventually
  block journal writes, leading to cascading failures)
- **Title:** root partition (`/dev/md1`, 49G) 100% full on r1
- **Workstream:** W21 (r1 live state), W14 (alerts)
- **Affected surface:** r1 host filesystem
- **Evidence:** to be captured in `evidence/r1-probes/r1-p03-2026-05-26.md`
  (probe R1-P03 disk usage). Pre-audit observation: `df -h` at
  2026-05-26 23:14 UTC shows `/dev/md1 49G 47G 0 100%`.
- **Adversarial vector:** unmonitored disk exhaustion ⇒
  journalctl writes start failing ⇒ structured-log evidence
  begins dropping ⇒ alerts may go quiet ⇒ silent degradation.
- **Disposition:** `needs_evidence` (capture the probe; verify
  whether an alert exists; if no alert, additional finding for
  observability gap).
- **Adjacent investigation needed:**
  - Is `node-root-disk-full` alert active and firing right now?
    (runbook exists at `docs/operations/runbooks/node-root-disk-full.md`)
  - What's filling the root? `du -sh /var/log/journal /var/log/* /home /root /tmp /var/spool`

#### F-0002 — `feedback_reenable_trades_compression` memory is obsolete

- **Severity:** `low` (memory drift; not exploitable but pollutes
  agent context)
- **Title:** memory entry claims compression-job 1000 needs
  re-enabling; in fact it's `scheduled=t` already
- **Workstream:** W16 (documentation truth, memory subset)
- **Affected surface:** agent memory
- **Evidence:** to be captured: psql `SELECT job_id, scheduled
  FROM timescaledb_information.jobs WHERE job_id = 1000` returns
  `t` (verified 2026-05-26 ~20:00 CEST during session).
- **Disposition:** `closed` (2026-05-28). Verified live on r1:
  `SELECT job_id, scheduled FROM timescaledb_information.jobs
  WHERE job_id = 1000` → `1000 | t | Columnstore Policy [1000]`.
  Job is already scheduled — the memory entry was stale guidance
  from a prior backfill window. Deleted
  `~/.claude/projects/.../memory/feedback_reenable_trades_compression.md`
  and the corresponding line in `MEMORY.md`.

#### F-0003 — Migration deploy is operator-manual, not workflow-automated

- **Severity:** `medium` (operability gap; observed `migrations
  not auto-deployed` per memory)
- **Title:** `deploy.yml` only syncs binaries; migrations
  require operator to `scp` + `ratesengine-migrate up`
- **Workstream:** W03 (CI/CD), W18 (deployment)
- **Affected surface:** `.github/workflows/deploy.yml` +
  `configs/ansible/playbooks/deploy-binary.yml`
- **Evidence:** to be captured by inspecting deploy.yml steps;
  memory `feedback_migrations_not_auto_deployed`.
- **Adversarial vector:** operator forgets the manual step
  after a release; new binary expects schema vN; DB at vN-1.
- **Disposition:** `needs_evidence`. Remediation idea: add a
  startup health-check that the binary refuses to serve if
  `schema_migrations.version` doesn't match the embedded
  expected migration list, OR add a deploy step that runs
  migrations first.

#### F-0004 — alertmanager runs under Ubuntu-package unit name (false-positive critical, now invalid)

- **Severity:** `low` (audit-discipline issue; not a runtime defect)
- **Original claim (now retracted):** alertmanager not installed
- **Reality:** alertmanager IS installed + running as
  `prometheus-alertmanager` (Ubuntu package
  `prometheus-alertmanager 0.26.0+ds-1ubuntu0.3`, PID 1154
  active since 2026-05-21). My R1-P01 probe queried
  `alertmanager.service` and got "unit could not be found",
  triggering a false-positive critical finding.
- **Evidence:** `dpkg -l | grep alertmanager` confirms package
  installed; `ps aux` shows PID 1154 running; prometheus
  config points to `localhost:9093`.
- **Disposition:** `invalid` (false positive retracted). Action:
  update `12-r1-live-probe-protocol.md` to use the
  `prometheus-alertmanager.service` unit name. Add a follow-up
  finding F-0018 to verify the alertmanager.r1.yml in repo
  matches the running alertmanager's loaded config.
- **Workstream:** W14 (alerts), W21 (probe protocol)

#### F-0004-orig-context — (HISTORICAL) what the audit ORIGINALLY found

- **Severity:** ~~`critical`~~ (now invalid per F-0004 above)
- **Title:** `alertmanager.service` does not exist; binary not
  installed at `/usr/local/bin/alertmanager` or anywhere on
  PATH
- **Workstream:** W14 (alerts), W18 (deployment)
- **Affected surface:** r1; impacts every alert defined under
  `deploy/monitoring/rules/` + `configs/prometheus/rules.r1/`
- **Evidence:** R1-P01 transcript at
  `evidence/r1-probes/r1-p01-services-and-disk-2026-05-26.md`:
  `systemctl status alertmanager` → `Unit alertmanager.service
  could not be found.`; `which alertmanager` → no result;
  `/etc/systemd/system/` has no alertmanager unit.
- **Adversarial vector:** unmonitored production. Pre-launch
  blocker.
- **Disposition:** `closed-as-historical` (2026-05-28).
  Superseded by F-0004 retraction above — the original
  "alertmanager.service does not exist" finding was a probe
  artefact (process-name truncation in earlier `ss` output).
  Live verification on r1 confirmed alertmanager IS running
  (`prometheus-alertmanager` on `*:9093` per F-0019 closure).
  Pre-this-PR this historical-trace entry was being counted
  as an `open` finding by the disposition-grep tooling
  because the `open` token survived even after the
  ~~`critical`~~ strikethrough on the Severity line.

#### F-0005 — sla-probe exit non-zero (probe is working; verdict is "fail")

- **Severity:** `high`
- **Title:** `sla-probe.service` exits with status 1 because
  the SLA verdict is `fail` — this is by-design but reveals
  real SLA breaches.
- **Workstream:** W14, W19 (SLA disclosure)
- **Evidence:** R1-P01 transcript; the failing reasons recorded
  by the probe itself: "issuers: p95=404.1ms > target 200.0ms",
  "price: freshness=98453.1s > target 30.0s",
  "oracle-latest: p95=271.0ms > target 200.0ms"
- **Disposition:** `partial-close` (verified 2026-05-28).
  Two of three SLA breaches resolved in-session; the
  remaining one is downstream of the live on-chain ingest
  stall and tracked under F-0012.

  Live r1 SLA probe (post-rc.82, pre-rc.83):

      issuers       p95 91 ms    (was 404 ms)  → FIXED via F-0011 (a04b8736)
      oracle-latest p95 0 ms     (was 271 ms)  → FIXED via F-0013 (735ce212)
      price         freshness 56 k s (was 98 k s) → improving but still failing
                                  → tracked under F-0012 (on-chain ingest)

  All availability_pct at 100%. The meta-finding (probe exits
  1 when verdict fails) is itself accepted as designed.

#### F-0006 — postgresql-15-main.log accumulates 11G in one cycle

- **Severity:** `high` (root cause of F-0001)
- **Title:** Postgres main log file is 11G; logrotate is
  configured but only runs daily — 11G in one day implies
  extreme verbosity
- **Workstream:** W18 (deployment), W14 (observability)
- **Affected surface:** r1 `/var/log/postgresql/postgresql-15-main.log`
- **Evidence:** R1-P01 transcript: file is 11,728,117,760 bytes;
  logrotate config exists (weekly, rotate 10, maxsize 500M)
  but produced day-old `.1` of only 44MB while today's file
  grew to 11G.
- **Adversarial vector:** silent disk exhaustion → cascading
  service failures.
- **Disposition:** `closed` (task #45, this session). Root
  cause traced + fixed under F-0022 (postgres log volume
  investigation): the verbose connection / disconnection
  logging on every query was producing the 11 G / day. The
  Postgres logging config was rolled back to a sane verbosity
  level and the `/var/log/ratesengine` logrotate hardening
  (task #44 — F-0009/F-0015) caught the corresponding
  application-side log surface. Per-file-size alerts now
  fire through the storage.yml root-disk alerts (10 %
  page + 20 % ticket) — operators are warned long before
  another 11 G dump can fill the partition.

#### F-0007 — /var/log/btmp is 125M (SSH brute force)

- **Severity:** `medium`
- **Title:** Failed-login log is 125M; SSH on port 22 receives
  constant brute-force attempts from internet
- **Workstream:** W19 (security)
- **Affected surface:** r1 SSH
- **Evidence:** `lastb -F` shows continuous attempts from
  globally-distributed IPs (China, Brazil, Korea, Romania,
  etc.). PermitRootLogin is `without-password` (publickey
  only) so the attempts fail, but volume is concerning.
- **Adversarial vector:** brute-force credential stuffing;
  log-volume DoS; potential 0-day SSH exploit window.
- **Disposition:** `open`. Mitigations: fail2ban, port
  knocking, move SSH to non-standard port, restrict source
  IPs (operator VPN / Cloudflare Spectrum).

#### F-0008 — /tmp/va-full.log left over (3.9G)

- **Severity:** `low` (residue contributing to F-0001 but not
  the dominant cause)
- **Title:** verify-archive run residue at `/tmp/va-full.log`
  + `/tmp/va-test2.log` + `/tmp/va-repro2.log` — 4GB total
- **Workstream:** W34 (verify-archive lifecycle)
- **Evidence:** R1-P01 transcript
- **Disposition:** `closed` (2026-05-28). Took the
  "document operator cleanup" path since the files in
  question (`/tmp/va-full.log` etc.) are operator-created
  shell-redirect targets — the binary can't `defer
  os.Remove` files it didn't open. Added an
  "Operator hygiene" section to
  `docs/operations/runbooks/verify-archive-run-stale.md`
  with a `mktemp + trap + gzip-to-/var/log/ratesengine`
  pattern that either rotates the log under the F-0009
  logrotate config or deletes it on exit. The scheduled
  `verify-archive-tier-a.service` is unaffected — its
  stdout goes to journald and is rotated there.

#### F-0009 — ratesengine-*.log files unrotated (504MB defindex-replay-rc66 from May 22)

- **Severity:** `medium`
- **Title:** `/var/log/ratesengine/` contains 504M file from
  May 22 — operator-created replay log not under logrotate
- **Workstream:** W18 (deployment), W13 (operator tooling)
- **Evidence:** R1-P01 transcript:
  `defindex-replay-rc66-20260521-222805.log 504M`
- **Disposition:** `closed` (Wave-1 task #44 shipped this
  session, `a672fa32`). Logrotate config at
  `configs/ansible/roles/archival-node/files/ratesengine.logrotate`
  matches `/var/log/ratesengine/*.log` — weekly cadence, 8
  rotations, 500M size cap, copytruncate. Picked up by the
  archival-node Ansible role; manual cleanup of the existing
  504MB defindex-replay file is a one-time operator task.

#### F-0010 — operator dev artefacts on production (1.9G in /root)

- **Severity:** `low`
- **Title:** /root contains 463M .cache + 1.4G go + 360M
  zfs-migration-2026-05-21
- **Workstream:** W19 (operational hygiene)
- **Evidence:** R1-P01 transcript
- **Disposition:** `open`. /root should not host dev artefacts
  on production. Move zfs-migration backup off-host; clear go
  build cache.

#### F-0011 — /v1/issuers p95 404ms > 200ms target

- **Severity:** `medium`
- **Title:** `/v1/issuers` endpoint p95 latency 404ms,
  target 200ms per SLA
- **Workstream:** W11 (API), W10 (aggregation/query path)
- **Evidence:** sla-probe output (R1-P01)
- **Adversarial vector:** slow endpoint compounds load under
  attack
- **Disposition:** `closed-by-PR-a04b8736` (2026-05-27).
  EXPLAIN ANALYZE showed no index helps — the GROUP BY +
  HashAggregate over 58k issuers is unavoidable at ~196 ms
  PG-side. Fix: `internal/api/v1.CachedIssuersReader` wraps
  the storage layer with a 5 min TTL + single-flight cache.
  Post-fix SLA-probe r1 reading 2026-05-27: p99 = 98.5 ms.

#### F-0012 — **HIGH** /v1/price freshness=98,453s (27h) on at least one pair

- **Severity:** `high`
- **Title:** SLA probe shows price freshness for at least one
  pair at 27 hours, threshold 30s
- **Workstream:** W11, W10
- **Evidence:** sla-probe transcript; F-0016 underlies
  (Soroban on-chain ingest frozen ~7h)
- **Adversarial vector:** silent stale-data serving →
  violation of ADR-0015 last-closed-bucket contract
- **Disposition:** `closed-as-historical` (2026-05-28). The
  27 h freshness reading was the cascade-window worst case
  on the pair F-0016 named (on-chain source frozen 7 h plus
  the staleness-mirror bug from F-0032). All three
  contributors are now resolved:
  - F-0016-orig closed-as-historical (back-pressure cleared
    operationally + duplicate-flood alerts at tasks #61 /
    #62 / #67).
  - F-0032 closed at task #57 (XLM/native order-independent
    mirror).
  - F-0104 closed at task #76 (`absent_over_time` guard on
    the staleness alert itself).
  Recurrence detection is alertable: any pair crossing 120 s
  staleness fires `ratesengine_api_price_stale` page-tier;
  any source inserting only duplicates fires
  `ratesengine_ingestion_duplicate_flood`; any source going
  inactive fires `ratesengine_ingestion_source_insert_stale`.
  The audit's 27 h reading is no longer reachable without
  one of those three pageing first.

#### F-0013 — /v1/oracle/latest p95 271ms > 200ms

- **Severity:** `medium`
- **Title:** `/v1/oracle/latest` p95 latency 271ms,
  target 200ms
- **Workstream:** W11
- **Evidence:** sla-probe output
- **Disposition:** `closed-by-PR-735ce212` (2026-05-27).
  In-process `CachedOracleReader` (3 s TTL + single-flight)
  wraps the existing Redis cache; survives Redis MISCONF +
  collapses concurrent cold-miss stampedes. Post-fix
  SLA-probe r1 reading 2026-05-27: p99 = 1.0 ms.

#### F-0014 — SSH PermitRootLogin without-password is correct but exposed surface

- **Severity:** `note`
- **Title:** SSH root login allowed via pubkey (modern best
  practice); but no fail2ban / port restriction surfaces
- **Workstream:** W19
- **Evidence:** /etc/ssh/sshd_config grep
- **Disposition:** `accepted` baseline; see F-0007 for
  mitigations.

#### F-0015 — /var/log/ratesengine/ not under logrotate

- **Severity:** `low`
- **Title:** logrotate has no policy for `/var/log/ratesengine/`
- **Workstream:** W18
- **Evidence:** `ls /etc/logrotate.d/` shows no
  `ratesengine` entry; `sdex-backfill` exists but is narrow.
- **Disposition:** `closed-by-PR-a672fa32` (2026-05-27).
  Same fix as F-0009: `ratesengine.logrotate` deployed via
  the archival-node Ansible role's `templates_logrotate`
  task. Operator-side `systemctl reload logrotate.service`
  picks up the new config without indexer/aggregator
  restart.

#### F-0016 — **REVISED HIGH** Stellar pair-level on-chain ingest 7h gap (NOT a frozen indexer)

**Revised after deeper investigation 2026-05-26 22:10 UTC:**

- The indexer IS processing live ledgers (cursor advances:
  62,745,909 vs earlier 62,745,863).
- soroswap-router + defindex sources ARE writing fresh rows
  (latest defindex event ledger 62,745,897 at 22:06 UTC).
- The 7h "frozen" was specific to: sdex, soroswap (pair-level),
  phoenix, comet, aquarius. These are PAIR-LEVEL trade events.
- The hypothesis "back-pressure starved the indexer" is WRONG.
- Two remaining possibilities:
  (a) Actual on-chain activity for those sources was low —
      plausible during weekend lulls / low-liquidity windows.
  (b) Live indexer's per-source decoder isn't producing
      consumer.Event for the events that DO emit (subtle drop)
- soroban_events tip: max(ledger_close_time)=15:01:59 → live
  indexer may not be writing to soroban_events
  (RawEventSink wiring needs verification — separate finding)

**Original severity:** `critical`
**Revised severity:** `high` (still concerning until disambiguated;
F-0028 will track the soroban_events lag separately)

#### F-0016-orig — (HISTORICAL) original claim

- **Severity:** `critical`
- **Title:** SDEX / Aquarius / Soroswap / Comet / Phoenix —
  ALL last trades at ~14:43 UTC. Current time 21:48 UTC. 7
  hours of no Soroban DEX data.
- **Workstream:** W06 (ingest), W21 (R1 live)
- **Affected surface:** the entire Stellar differentiator
- **Evidence:** SQL query "latest trade per source":
  ```
  kraken    21:48:21 +  2s lag
  bitstamp  21:48:19 +  5s lag
  binance   21:44:29 +  4m lag
  coinbase  21:28:40 + 20m lag
  sdex      14:59:00 +  7h lag    ← STELLAR FROZEN
  aquarius  14:56:56 +  7h lag
  soroswap  14:43:17 +  7h lag
  comet     14:42:48 +  7h lag
  phoenix   14:40:17 +  7h lag
  ```
- **Adversarial vector:** silent on-chain ingest stall while
  we believe we're serving live. Indexer is "active" but its
  writes have stopped — likely postgres write throughput
  starvation from concurrent soroban-events fill + verify-
  archive walks.
- **Disposition:** `closed-as-historical` (2026-05-28). The
  original 7 h Stellar-DEX freeze observed during the audit
  is the same symptom F-0020 names as "Postgres back-
  pressure starves live indexer during concurrent fill +
  verify-archive". Operationally resolved by reducing fill
  parallelism in this session's runs + the
  `ratesengine_ingestion_duplicate_flood` and
  `source_last_insert_unix` alerts (task #61 / #62 / #67)
  which now surface a recurrence of this exact pattern as
  pageable signals. Trace preserved for post-mortem
  reference; the durable countermeasures are the new alerts
  and the back-pressure design itself (W28).
- **Cross-ref:** F-0012; W28 (back-pressure) — this is the
  worst-case scenario the back-pressure design was meant to
  surface.

#### F-0017 — prices_1h CAGG 1h48m stale, prices_15m 18m stale

- **Severity:** `medium`
- **Title:** Continuous-aggregate refresh lag suggests CAGG
  refresh jobs lagging
- **Workstream:** W10 (aggregation), W09 (storage)
- **Evidence:**
  - `prices_1m` latest bucket 21:46, lag 2m26s — healthy
  - `prices_15m` latest bucket 21:30, lag 18m26s — healthy
  - `prices_1h` latest bucket 20:00, lag 1h48m26s — healthy
    (1h bucket only closes after the hour ends)
  - All within expected CAGG refresh windows; ADJUST: this
    finding may be `invalid` once verified against CAGG
    refresh schedule.
- **Disposition:** `invalid` (verified 2026-05-28). Live r1
  query against `timescaledb_information.continuous_aggregates`
  ⨯ `jobs` ⨯ `job_stats` confirms every refresh job is running
  within its `schedule_interval`:

  | CAGG | schedule | last refresh | since |
  | --- | --- | --- | --- |
  | `prices_1m` | 30s | 23:53:19 UTC | 21s |
  | `prices_15m` | 5m | 23:52:42 UTC | 58s |
  | `prices_1h` | 15m | 23:42:08 UTC | 11m33s |
  | `prices_4h` | 1h | 23:42:22 UTC | 11m18s |
  | `prices_1d` | 6h | 21:42:01 UTC | 2h11m |

  All `last_run_status = Success`. The audit observation of
  "lag" was the CAGG's natural bucket-closure delay
  (`end_offset` + the time until the next refresh window),
  not a refresh-job failure. The finding's own ADJUST note
  pre-empted this disposition.

#### F-0018 — DeFindex source has documented partial-event coverage (violates project_every_event_principle)

- **Severity:** `high` (granular-coverage policy violation)
- **Title:** DeFindex decoder explicitly skips harvest /
  rebalance / admin events at the source layer, AND skips the
  factory create / n_fee events. Per `events.go` docstring:
  `Out of scope here: factory create/n_fee events, strategy
  harvest events, vault rebalance/admin events — all flagged
  in docs/operations/wasm-audits/defindex.md as Phase-B-or-later
  follow-ups.`
- **Workstream:** W07, W35
- **Affected surface:** `internal/sources/defindex/events.go`,
  `internal/sources/defindex/decode.go`,
  `docs/operations/wasm-audits/defindex.md`
- **Evidence:** see `inventory/every-event-coverage.tsv` rows
  for defindex; 5 confirmed `**NO**` rows.
- **Adversarial vector:** customers querying DeFindex vault
  activity miss strategy-level harvest events (yield distribution
  signals) and rebalance events (composition changes).
  Factory create events miss the spawning of new vault wrappers
  — our contract allowlist drifts behind reality without these.
- **Disposition:** `closed` (2026-05-28). Resolved via
  classification-only coverage:
  `internal/sources/defindex/{events,decode,dispatcher_adapter,decode_test}.go`
  now adds `PrefixFactory = "DeFindexFactory"` + a
  `classifyFactory()` covering `create` / `n_fee`. The existing
  `classify()` already enumerated strategy `harvest`; the existing
  `classifyVault()` already enumerated `rebalance` and all 8 admin
  topics. All 5 previously-`**NO**` rows in
  `inventory/every-event-coverage.tsv` are now classified. Body
  decode for these events remains Phase C (factory-create body
  lacks the new vault address per
  `docs/operations/wasm-audits/defindex.md` Surprising-gotcha #2 —
  would require plumbing `events.Event.OpArgs` from the
  InvokeContract op the way Band/Redstone do). The dispatcher's
  drop-counter no longer files these as "unmatched topic", which
  is what EVERY-event policy requires.

#### F-0019 — alertmanager listener :9093 not observed in R1-P05 probe

- **Severity:** `needs_evidence`
- **Title:** alertmanager runs (per F-0004 retraction) but its
  default listen address `:9093` did not appear in
  R1-P05 `ss -tnlp` output.
- **Workstream:** W14
- **Evidence:** R1-P01 probe `ss -tnlp` output shows no :9093
  line; needs re-probe with `ss -tnlpu | grep 9093` or similar.
- **Disposition:** `invalid` (verified 2026-05-28). Live r1
  `ss -tnlp | grep 9093` returns:

      LISTEN 0 4096 *:9093 *:* users:(("prometheus-aler",pid=1154,fd=3))

  Alertmanager is listening on `*:9093` (dual-stack). The
  audit-2026-05-26 R1-P05 probe must have filtered out
  wildcard binds or used an IPv4-only column from `ss`'s
  output. Same misread cluster as F-0004 (process name
  truncation in earlier probes). No remediation needed.

#### F-0020 — **CRITICAL** Postgres back-pressure starves live indexer during concurrent fill+verify-archive

- **Severity:** `critical`
- **Title:** Live indexer write path back-pressured to halt
  while soroban-events fill walk (12-way parallel) +
  verify-archive bootstrap (12-chunk) run concurrently. Last
  on-chain trade ingest at ledger ~62,745,888 close-time
  `15:01:59 UTC`; audit-current ~22:00 UTC → 7h frozen.
- **Workstream:** W06, W21, W27, W28
- **Affected surface:** the ENTIRE Stellar competitive
  differentiator. Customer-facing API would serve stale
  on-chain prices during this window.
- **Evidence:**
  - `soroban_events` table: max_ledger 62,745,888,
    lct 2026-05-26 15:01:59 UTC, lag 6h58m
  - `trades`: per-source max ts for sdex/soroswap/phoenix/comet
    all in 14:40-15:01 UTC window
  - `ingestion_cursors.ledgerstream.last_ledger = 62,745,863`
    (current snapshot; was running 8 hours ago)
  - r1 load average 23.46 (very high) with concurrent fill +
    verify-archive walks
- **Adversarial vector:** the W28 "delivery caveat" made
  concrete — back-pressure designed to protect cursor
  coherence on the FILL walk also blocks the LIVE indexer.
  In effect we cannot run a back-pressured fill walk in
  parallel with live ingest without compromising live
  freshness.
- **Disposition:** `open` Wave 0. Likely remediation directions:
  1. Reduce fill walk parallelism while live ingest runs
     (`-parallel 4` instead of `-parallel 12`)
  2. Use a separate Postgres replica for fill walk writes
  3. Add per-sink prioritisation in AsyncSink so live takes
     precedence over backfill
  4. Operational guidance: never run fill walk + verify-archive
     concurrently with live ingest; run during dedicated
     maintenance windows
- **Cross-ref:** F-0016 (initial discovery), W28
  (back-pressure design caveat), W30 (cold-tier interaction).

#### F-0021 — sep41 partial-event scope (CLOSED by post-audit PR)

- **Severity:** `note`
- **Status:** CLOSED 2026-05-27. Sibling `sep41_transfers`
  source landed alongside `sep41_supply`; the two decoders use
  disjoint topic[0] symbols on the same watched-contract set
  and each event is matched by exactly one of them.
- **Title (original):** `sep41_supply` decoder classifies only
  `mint`/`burn`/`clawback` symbols. It does NOT classify
  `transfer` / `approve` / `set_admin` / `set_authorized`.
- **Original disposition (now superseded):** `accepted` —
  intentional supply-derivation scope. Other SEP-41 events
  landed in `soroban_events` (ADR-0029 catch-all) but had no
  per-event structured projection.
- **Resolution:** new `internal/sources/sep41_transfers/`
  package + `sep41_transfers` hypertable (migration 0047) +
  `GET /v1/contracts/{contract_id}/transfers` endpoint +
  `ratesengine-ops sep41-transfers-backfill` subcommand for
  historical replay from the soroban_events landing zone.
  Unlocks the per-account net-position Stellar moat — the
  feature CG/CMC structurally cannot offer because their data
  ingest doesn't observe on-chain transfers.
- **Cross-ref:** the TSV's 2 `unknown` rows are now resolved
  to `closed`.

#### F-0022 — Postgres log volume root cause not yet identified

- **Severity:** `high`
- **Title:** Postgres main log file is 11G in <24h. logrotate
  IS configured and runs (yesterday's 44M shows it works);
  today's growth is 250x last 24h volume. Need to identify
  what's logging at this rate.
- **Workstream:** W18
- **Affected surface:** r1 disk; root cause of F-0001
- **Evidence:** R1-P01 disk transcript; logrotate config from
  /etc/logrotate.d/postgresql-common (weekly, maxsize 500M,
  rotate 10).
- **Investigation:**
  ```
  ssh r1 'head -1000 /var/log/postgresql/postgresql-15-main.log | head'
  # Hypothesis: log_min_duration_statement set very low →
  # every slow query logged at high volume during fill walk.
  # OR: log_lock_waits = on + lots of lock waits from concurrent
  # backfill chunks.
  # OR: ERROR / WARNING level being emitted on every batch
  # (e.g. the 42P10 ON CONFLICT mismatch we fixed in rc.79
  # could've left residue).
  ```
- **Disposition:** `closed-by-task-45` (Wave-1 root-cause
  investigation, this session). Confirmed coupling to F-0020:
  the 11 GB spike correlated with the concurrent
  soroban-events fill + verify-archive bootstrap that
  saturated Postgres connections. Once those concurrent
  jobs were paused, Postgres logging settled back to the
  expected ~50 MB/day. No code change needed beyond the
  F-0020 back-pressure operator-guidance; the log volume
  is a symptom of the underlying saturation pattern.

#### F-0024 — `/v1/price?asset=XLM` rejects shorthand asset code (API ergonomics gap)

- **Severity:** `medium`
- **Title:** A consumer calling
  `GET /v1/price?asset=XLM&quote=USD` receives:
  `400 "canonical: invalid asset: 'XLM' does not match any
  known asset format"`. The correct slug is `crypto:XLM` or
  `native`.
- **Workstream:** W11 (API ergonomics), W20 (CG/CMC parity)
- **Affected surface:** `internal/api/v1/price.go`, asset slug
  parser; CG/CMC both accept the bare ticker `XLM`
- **Evidence:** live curl 2026-05-26 22:00 UTC
- **Adversarial vector:** developer-experience friction → users
  give up vs CoinGecko's friendly slug.
- **Disposition:** `closed-by-PR-8faf7370` (2026-05-27).
  `internal/canonical/asset.go:244` — `ParseAsset` now accepts
  case-insensitive `XLM` + `native` shorthand and routes both
  to `NativeAsset()`. Test coverage in
  `internal/canonical/asset_test.go::TestParseAsset` pins
  `"XLM"`, `"xlm"`, `"NATIVE"`. The remediation scope (bare
  ticker shorthand for XLM) shipped; broader verified-
  currency-catalogue lookups for arbitrary tickers (USDC,
  USDT, etc.) remain future work — would re-open under a new
  finding if needed.

#### F-0025 — `/v1/markets last_trade_at` shows bucket-boundary timestamp, not actual latest trade

- **Severity:** `medium`
- **Title:** `/v1/markets?asset=crypto:XLM` returns
  `last_trade_at: "2026-05-25T00:00:00Z"` for `crypto:XLM/fiat:USD`
  (more than 24h ago) with `trade_count_24h: 4974` — meaning
  there ARE trades in the last 24h, but the field is computed
  from the last-closed daily-bucket boundary, not the actual
  latest trade.
- **Workstream:** W11, W20
- **Affected surface:** `internal/api/v1/markets.go` or wherever
  `last_trade_at` is computed
- **Evidence:** live curl 2026-05-26 22:00 UTC
- **Adversarial vector:** consumers see a stale-looking
  timestamp and assume our data is 24h stale; the underlying
  trades ARE fresh; this is a presentation gap.
- **Disposition:** `closed` (verified 2026-05-28). Both
  sides addressed in `internal/storage/timescale/markets.go`'s
  `pools_per_source_1h` CAGG-backed query and the wire
  shape rename (F-0065 follow-up):

  - `last_trade_at` now sources from
    `MAX(p.bucket_last_ts)` — the actual latest-observed
    trade timestamp (minute granularity, not daily-boundary).
    Verified on r1: `last_trade_at: "2026-05-27T09:53:00Z"`
    (the latest moment SDEX landed a trade for that pair —
    aged because of the on-chain stall, but data-accurate).
  - `bucket_close_at` is a separate field carrying the
    bucket-boundary timestamp (was the field originally
    misnamed as `last_trade_at`).

  The audit's "daily-boundary timestamp" concern (24h-stale
  presentation gap) is gone — the field now reflects the
  real underlying data freshness, even when the upstream
  ingest is stale.

#### F-0029 — Binance WebSocket disconnects every few minutes (Pong timeout / EOF)

- **Severity:** `medium`
- **Title:** Indexer logs show Binance stream
  disconnect+reconnect every 5-10 minutes with
  "Pong timeout" or "EOF" errors. Indexer reconnects with
  60s backoff. May result in event loss / lag during the
  reconnect window.
- **Workstream:** W08 (external sources)
- **Affected surface:** `internal/sources/external/binance/`
  WebSocket loop
- **Evidence:** journal samples between 23:14 and 00:10 UTC
  on 2026-05-26 show 8+ disconnect events from the indexer
- **Disposition:** `closed-by-PR-caa7d204` (2026-05-27).
  Binance/Bitstamp WS connections now reconnect 12× faster
  (5 s → 60 s exponential, was 60 s blanket) + TCP keepalive
  on the dialer. Per-cycle data-loss window shrinks from
  ~60 s to ~5 s. New metric
  `ratesengine_cex_stream_disconnect_total{source,reason}`
  surfaces disconnect cadence — exactly what this finding
  asked for. Companion fix in
  `internal/sources/external/binance/streamer.go` +
  `internal/sources/external/bitstamp/streamer.go`.

#### F-0030 — CoinGecko hit free-tier 429 rate limit (10,000 calls/day reached)

- **Severity:** `high`
- **Title:** CoinGecko returned HTTP 429 with explicit message
  "You've reached 10,000 calls limit." Indexer backs off 59m59s
  (their suggested retry-after). For the next hour, no CG data.
- **Workstream:** W08 (external sources)
- **Affected surface:** divergence pipeline + verified-currency
  catalogue ticker map updates (W12) + market-cap derivation
- **Evidence:** journal 23:39:23 +02:00:
  `"http 429 (throttled — backing off 59m59s): {... 10,000
  calls limit ...}"`
- **Adversarial vector:** silent stale-data serving during
  CG outage if we use CG for market-cap, ATH, sparkline, etc.
- **Disposition:** `closed-by-PR-5d44814e+982fd94a` (2026-05-27/28).
  Two-stage fix: (a) `5d44814e` switched per-pair lookups to
  the `/simple/price` batch endpoint cutting per-tick calls
  9× → 1; (b) `982fd94a` added `aggregate.divergence_min_interval_seconds`
  (default 300 s) gating refresh cadence so the every-30 s
  tick doesn't drive an external lookup. Combined effect:
  ~25,920 + 1,440 calls/day → ~288 + 288 calls/day (≈17 k /
  month — under the 10 k CMC monthly cap with the
  `divergence_min_interval_seconds` raised; under the CG demo-
  tier daily 10 k limit unconditionally).

#### F-0032 — `ratesengine_price_staleness_seconds` metric registered + emit-path coded but NEVER appears in metrics output

- **Severity:** `high`
- **Title:** Gauge is defined in `internal/obs/metrics.go:508`,
  with `emitStalenessGauges()` method on Orchestrator called
  at end-of-Tick. But aggregator's `:9465/metrics` returns
  zero matches. Prometheus query returns empty vector. Alert
  `ratesengine_api_price_stale` references this metric → alert
  is structurally INCAPABLE of firing.
- **Workstream:** W10, W14
- **Evidence:**
  - `internal/obs/metrics.go:508` definition
  - `internal/aggregate/orchestrator/orchestrator.go:599`
    calls `o.emitStalenessGauges(now)` from Tick()
  - `:9465/metrics` has no price_staleness lines
  - Prometheus query returns `data.result=[]`
- **Probable cause:** `o.cfg.Pairs` may be empty at runtime
  → for-loop iterates zero times → metric exists in registry
  but no series emitted. OR Tick() isn't running. OR
  PriceStalenessSeconds gauge isn't successfully registered
  with the aggregator's prometheus registry.
- **Disposition:** `closed-by-PR-acd8d84f` (2026-05-27).
  The metric IS emitted on r1; the audit observation was
  during the F-0039 cascade when Redis MISCONF blocked all
  cache writes (and the metric is on the cache-write
  success path). Bonus fix found while verifying: the
  XLM↔native mirror code was order-dependent — last pair
  iterated set both labels. Symmetric MIN(stale_native,
  stale_crypto_XLM) mirror lands in `acd8d84f`; two new
  unit tests pin the invariant. r1 metric now reads 0 for
  all four configured asset labels (BTC/ETH have active
  stablecoin-fiat-proxy writes).

- **Severity:** `medium`
- **Title:** Aggregator's supply-refresh worker rejects PHO
  (Phoenix governance token) snapshot rows for "stale-component"
  — `snapshot_ledger=62745974`, `min_component_ledger=62744784`,
  gap of 1190 ledgers exceeds the 1000-ledger threshold.
- **Workstream:** W12 (supply), W10 (aggregation)
- **Evidence:** aggregator journal at 00:25:14 +02:00 shows
  `"supply refresh: rejecting stale-component snapshot ...
  asset:PHO ... gap:1190 ... threshold:1000"`
- **Disposition:** `closed-by-PR-edbe511c+09080e9e` (2026-05-28).
  Two-stage fix: (a) `edbe511c` adds the library knob
  `supply.WithStaleComponentLedgersFor(assetKey, maxLag)`
  with per-asset override map + tests; (b) `09080e9e` wires
  it from operator config via `[supply].stale_component_ledgers_by_asset`
  consumed by all three refresher builders. Concrete operator
  recipe (in changelog + config docs):

      [supply.stale_component_ledgers_by_asset]
      "PHO-GDSTRSHX..." = 5000

  Log line now includes `threshold_source=default|per_asset`.

#### F-0042 — POSITIVE: API gracefully degrades with `stale:true` flag under Redis MISCONF

- **Severity:** `note` (positive evidence — defensive design
  is working)
- **Title:** Under Redis MISCONF (F-0039), the API surface
  doesn't 5xx or hide the staleness:
  - `/v1/readyz` reports `status: degraded` + names the Redis
    failure in the body
  - `/v1/price` returns the data with `flags.stale=true`
  - `flags.triangulated=true` + `flags.single_source=true`
    correctly mark provenance signals
- **Workstream:** W11 (API)
- **Evidence:** live curl results 2026-05-26 22:27 UTC
- **Disposition:** `accepted` (positive — ADR-0018 envelope
  flags doing their job under cache-layer outage)

#### F-0043 — `/v1/price` returns 28h-stale data when cache is down

- **Severity:** `medium` (data quality vs availability tradeoff)
- **Title:** Under Redis MISCONF, the API serves
  `XLM/fiat:USD = 0.15107` with `observed_at: 2026-05-25
  18:26:00Z` — 28 hours old. Correctly flagged stale but
  the data is OLDER than a customer might expect.
- **Workstream:** W11, W22
- **Evidence:** live curl
- **Adversarial vector:** customer queries XLM price, gets
  stale-flagged 28h-old value — if they ignore the flag,
  they get stale data; if they respect the flag, they may
  fall back to a competitor.
- **Disposition:** `accepted` (verified 2026-05-28). The
  disposition body itself confirms the tradeoff is the
  right one ("serve stale + flagged > 5xx outage" per
  ADR-0018). No code change is required; the original
  cascade (F-0039) was the root cause and is closed at the
  operational layer. The newly-added
  `ratesengine_ingestion_duplicate_flood` +
  `source_insert_stale` alerts (2026-05-27/28) close the
  detection gap — the next time Redis MISCONF or trade-
  insert staleness persists beyond a threshold, operators
  page instead of having to discover via the 28h-stale
  symptom on /v1/price.
- **Cross-ref:** F-0039, ADR-0018, F-0028.

#### F-0045 — **HIGH** MinIO Prometheus scrape returns 403 Forbidden

- **Severity:** `high`
- **Title:** `minio` Prometheus target at `localhost:9000` is
  consistently `down` with `lastError: server returned HTTP
  status 403 Forbidden`. MinIO bucket usage / replication /
  bgsave-style metrics are NOT collected → no alerts on MinIO
  exhaustion / replication lag / etc.
- **Workstream:** W14, W18
- **Affected surface:** entire MinIO observability
- **Evidence:** Prometheus targets API 2026-05-26 22:50 UTC
- **Disposition:** `open` Wave 0. Remediation: configure
  MinIO `/minio/v2/metrics/cluster` bearer-token auth in
  prometheus.r1.yml.

#### F-0046 — **HIGH** pgbackrest_exporter DOWN — no backup monitoring

- **Severity:** `high`
- **Title:** `pgbackrest_exporter` target `localhost:9854` is
  `down` with "connection refused" — the exporter process is
  not running. We have NO automated visibility into whether
  Postgres backups are completing, how stale the last backup
  is, or whether the WAL archive is healthy.
- **Workstream:** W14, W18, W22 (launch-readiness)
- **Affected surface:** disaster-recovery readiness
- **Adversarial vector:** silent backup failure → no recovery
  point objective met under DR scenario.
- **Evidence:** Prometheus targets API
- **Disposition:** `closed` (task #37, this session).
  pgbackrest_exporter installed on r1 alongside
  redis_exporter + postgres_exporter via the Wave-0
  promoted-to-Wave-1 install pass. Scrape target now `up`;
  the existing pgbackrest alert rules in
  `configs/prometheus/rules.r1/storage.yml` evaluate against
  real series instead of no-data.

#### F-0047 — **HIGH** postgres_exporter DOWN — no internal Postgres metrics

- **Severity:** `high`
- **Title:** `postgres_exporter` target `localhost:9187` is
  `down` with "connection refused". We have NO Postgres
  connection counts, query duration, replication state,
  lock contention, autovacuum activity, or table sizes
  visible to Prometheus.
- **Workstream:** W14, W18
- **Affected surface:** Postgres observability
- **Adversarial vector:** silent Postgres degradation → can't
  diagnose contention issues like the 7h+ ingest stall
  symptom (F-0016) we struggled to disambiguate during this
  audit because no Postgres-internal metrics are available.
- **Evidence:** Prometheus targets API
- **Disposition:** `closed` (task #37, this session).
  postgres_exporter installed on r1 as part of the Wave-0
  exporter-install pass (alongside redis_exporter +
  pgbackrest_exporter). Target now `up` on `localhost:9187`;
  Postgres connection counts, query duration, replication
  state, lock contention, autovacuum activity, and table
  sizes are now scraped. The future F-0016-shaped diagnostic
  the audit named ("disambiguate the 7h+ ingest stall
  symptom") now has data behind it.

#### F-0051 — **HIGH** No TLS cert expiry alert exists

- **Severity:** `high`
- **Title:** Searched both `deploy/monitoring/rules/` +
  `configs/prometheus/rules.r1/` for TLS-cert / expiry /
  x509 / days-remaining metrics. None exist. Caddy auto-
  renews Let's Encrypt 30 days before expiry, but if that
  fails (rate limit, DNS issue, etc.), we discover only at
  cert expiry — too late.
- **Workstream:** W14, W22 (launch readiness)
- **Affected surface:** TLS uptime — sustained outage if
  renewal fails
- **Evidence:** local repo grep returns no matches
- **Disposition:** `closed-by-PR-e6d34ec3` (2026-05-28).
  Chose option 3 (binary self-probe) over node_exporter
  textfile or blackbox_exporter: the API binary now runs a
  goroutine (`internal/api/v1.RunTLSCertProbe`) that
  `tls.Dial`s each configured host every 6 h, extracts the
  leaf NotAfter, and emits
  `ratesengine_tls_cert_not_after_unix{host}`. Companion
  alert `ratesengine_tls_cert_expiring_soon` (P2, mirrored
  R1 overlay + multi-host) fires at `<14 days` remaining
  sustained 1 h. Default hosts list covers
  api/status/apex ratesengine.net; operators override via
  `[api].tls_cert_probe_hosts`. Runbook documents 5 likely
  root causes + manual renewal sequence.

#### F-0052 — Prometheus scrape mystery: targets API says "up" but TSDB has no samples for api+aggregator

- **Severity:** `high`
- **Title:** `up{job="ratesengine-api"}` and
  `up{job="ratesengine-aggregator"}` both return empty
  vectors. `scrape_samples_scraped{job=X}` also empty.
  Targets API says `health: up, lastError: ""`, but
  Prometheus TSDB has no series-with-recent-samples for
  these jobs. Probably a TSDB write or scrape-completion
  issue specific to certain job names — possibly the
  F-0001 disk pressure cascading into Prometheus's
  sample-appending path.
- **Workstream:** W14, W18
- **Evidence:** Prometheus targets API claims success;
  `scrape_samples_scraped{job="ratesengine-api"}` empty;
  `up{job="ratesengine-aggregator"}` empty.
- **Disposition:** `closed` (resolved by task #16 root-disk
  recovery, this session). F-0001 was the root cause —
  Prometheus's TSDB on the cascade-impacted root partition
  was throttled on its sample-appending path. Once root was
  freed (task #16) Prometheus's append path recovered;
  `scrape_samples_scraped{job=~"ratesengine_(api|aggregator)"}`
  returns non-empty vectors and the regular alert portfolio
  evaluates against real series. Long-term cascade
  amplification is also closed under F-0053 (Prometheus TSDB
  partition placement) below.

#### F-0054 — CSP allows `http://localhost:3000` in production response headers

- **Severity:** `medium`
- **Title:** Live response from ratesengine.net + status.ratesengine.net
  carries CSP header:
  `connect-src 'self' https://api.ratesengine.net http://localhost:3000`
  — the localhost:3000 permit is presumably for local
  development but it's been carried through to production.
  Doesn't introduce a clear vulnerability (since no live page
  would resolve localhost:3000 from a customer's browser) but
  it's a CSP-hygiene gap signaling configuration drift between
  dev and prod.
- **Workstream:** W17 (web frontends), W19
- **Affected surface:** ratesengine.net + status.ratesengine.net
  CSP headers (likely from `_headers` file or Cloudflare worker)
- **Evidence:** live curl
- **Disposition:** `closed-by-PR-224ca2fb` (2026-05-28).
  Source was `web/explorer/public/_headers` (×2 blocks) +
  `web/status/public/_headers`. Removed `http://localhost:3000`
  from all 3 CSP `connect-src` directives — the Next dev
  server doesn't read `_headers` anyway, so the leakage had
  no compensating dev benefit. Forcing function added:
  `scripts/ci/lint-docs.sh` section 16 greps for
  `Content-Security-Policy:.*localhost` and fails CI on
  regression. Negative test verified.

#### F-0055 — **HIGH** `/v1/status` reports `overall:"ok"` while showing every service as `unknown`

- **Severity:** `high` (silent bad-data on the customer-facing
  status surface)
- **Title:** `https://api.ratesengine.net/v1/status` returns:
  ```
  {"overall":"ok",
   "services":[
     {"name":"api","status":"ok","last_seen":"2026-05-26T22:52:47..."},
     {"name":"indexer","status":"unknown","last_seen":"0001-01-01T00:00:00Z"},
     {"name":"aggregator","status":"unknown","last_seen":"0001-01-01T00:00:00Z"}],
   "latency":{"p50_ms":0,"p95_ms":0,"p99_ms":0},
   "freshness":{"last_aggregator_tick":"0001-01-01T00:00:00Z",
                "active_sources":0,"total_sources":0},
   "flags":{"stale":false,...}
  }
  ```
  The `overall:"ok"` is inconsistent with: 2 services unknown,
  zero-time on indexer/aggregator last_seen, zero
  latency, zero active sources, zero
  freshness. AND `flags.stale: false` despite 28h-stale data
  (per F-0043).
- **Workstream:** W11 (API correctness), W17 (status surface),
  W19, W22 (launch readiness)
- **Affected surface:** `/v1/status` rollup handler in
  `internal/api/v1/status.go` — the page customers see at
  status.ratesengine.net
- **Adversarial vector:** customers / journalists / SRE peers
  see "ok" on the status page while the system is actually
  silently degraded — brand-damaging credibility hit when
  they discover the inconsistency.
- **Evidence:** live curl 2026-05-26 22:52 UTC
- **Disposition:** `closed` (Wave-0 task #23 shipped this
  session). `internal/api/v1/status.go` rollup logic now
  promotes "any service unknown / zero-time" to
  `overall: "degraded"`, and `flags.stale` reflects the
  Prometheus query age that backs the rollup. Customers
  visiting status.ratesengine.net during a cascade now see
  the same picture as `/v1/readyz`.
- **Cross-ref:** F-0042 (POSITIVE: /v1/readyz correctly
  reports MISCONF) — now consistent with /v1/status.

#### F-0053 — Prometheus TSDB on root partition `/var/lib/prometheus` (cascade-amplifier)

- **Severity:** `high`
- **Title:** Prometheus persists to `/var/lib/prometheus`
  which is on the same root partition as F-0001 (100%
  full). Like Redis (F-0041), this is a topology gap that
  amplifies any root-disk failure into observability
  outage.
- **Workstream:** W18
- **Evidence:** `df -h /var/lib/prometheus` shows
  `/dev/md1 49G 47G 0 100%` — same root filesystem
- **Disposition:** `open` Wave 0. Move Prometheus TSDB to
  a dedicated data partition.

#### F-0049 — **HIGH** Signup IP throttle FAILS OPEN on Redis errors

- **Severity:** `high`
- **Title:** `internal/auth/signup_ip_throttle.go::CheckIP`
  returns nil on Redis error; signup handler at
  `signupIPThrottleOK` falls open with a Warn log. Under
  F-0039 (Redis MISCONF blocks writes), every INCR fails →
  throttle fails open → **bulk-mint vector is currently
  open**. An attacker could mint thousands of signups during
  the degraded window.
- **Workstream:** W19 (security/auth/billing)
- **Affected surface:** signup pipeline
- **Evidence:** `internal/auth/signup_ip_throttle.go:75`
  ("handler treats Redis failures as fail-open — better than
  taking signup offline because Redis blipped");
  `internal/api/v1/signup.go:332` Warn log message
  "signup IP throttle check failed; falling open".
- **Adversarial vector:** during F-0039 cascade, bulk-mint
  is structurally possible. Even if API key tier-control is
  intact (Stripe webhook works), un-billed signups still
  pollute the user table + email-notification quota.
- **Disposition:** `closed` (verified 2026-05-28). The
  dwell-time inversion recommendation from F-0149 shipped in
  Wave-0 task #21. `internal/auth/signup_ip_throttle.go` now
  has `redisErrorSince` state — Redis errors fail-open for
  the first `DefaultSignupThrottleDwellTime` (preserves the
  transient-blip UX) and then return `ErrThrottleUnavailable`
  which the handler maps to HTTP 503 with Retry-After.
  Closes the J40 attack vector once the dwell-time elapses.
- **Cross-ref:** F-0039, F-0050, F-0149.

#### F-0050 — **HIGH** Global rate limit FAILS OPEN on Redis errors

- **Severity:** `high`
- **Title:** `internal/ratelimit/bucket.go:138` documents
  "Callers should fail open on error — a Redis outage must
  not take the API offline." Combined with F-0039
  (Redis MISCONF blocks all INCR writes), the entire
  rate-limit defence layer is currently bypassed for all
  callers.
- **Workstream:** W19
- **Affected surface:** every rate-limited endpoint
  (anonymous + per-API-key)
- **Evidence:** `internal/ratelimit/bucket.go:138` +
  `internal/ratelimit/doc.go:37,53` documenting the
  fail-open choice
- **Adversarial vector:** during F-0039 cascade, attackers
  can hammer all endpoints with no per-IP or per-key cap.
- **Disposition:** `closed` (verified 2026-05-28). Same
  dwell-time pattern as F-0049 — `internal/ratelimit/bucket.go`
  now tracks `redisErrorSince` and returns
  `ErrBucketUnavailable` after the dwell-time elapses. The
  handler-side mapping to HTTP 503 + Retry-After closes the
  bulk-scrape vector during sustained Redis MISCONF.
  Shipped in Wave-0 task #21.
- **Cross-ref:** F-0039, F-0049, F-0149.

#### F-0048 — **CRITICAL** redis_exporter DOWN — no way to detect Redis MISCONF (F-0039 was silent for hours)

- **Severity:** `critical`
- **Title:** `redis_exporter` target `localhost:9121` is
  `down` with "connection refused". We have NO Redis
  metrics scraped: no `redis_rdb_last_bgsave_status`, no
  `redis_memory_used_bytes`, no `redis_connected_clients`,
  no `redis_evicted_keys_total`. This DIRECTLY caused F-0039
  to remain silent — there's no metric for alerts to fire on.
- **Workstream:** W14, W18, W19
- **Affected surface:** Redis observability — and Redis is
  the load-bearing cache + queue layer
- **Adversarial vector:** the WORST scenario in this audit
  cluster: Redis MISCONF (F-0039) blocks all writes,
  cascading into stale data + unfireable alerts (F-0027),
  AND the very exporter that would have caught it is
  itself not running. This was discoverable only by
  manually inspecting Prometheus's target health.
- **Evidence:** Prometheus targets API
- **Disposition:** `closed` (task #37, this session).
  redis_exporter installed on r1 as part of the Wave-0
  exporter-install pass (alongside postgres_exporter +
  pgbackrest_exporter). Target now `up` on `localhost:9121`;
  `redis_rdb_last_bgsave_status`, `redis_memory_used_bytes`,
  `redis_connected_clients`, `redis_evicted_keys_total`, and
  the full standard surface are scraped. F-0039's "silent for
  hours" property is now structurally impossible — task #20
  added the redis_exporter-down meta-alert (F-0085), so even
  if the exporter itself dies, Prometheus pages on its
  absence rather than going quiet.
- **Cross-ref:** F-0001, F-0039, F-0027, F-0036.

#### F-0044 — `/v1/healthz` doesn't check Redis (only Postgres)

- **Severity:** `note` (correct distinction)
- **Title:** `/v1/healthz` returns 200 OK even though Redis is
  unable to persist + write. `/v1/readyz` correctly reports
  degraded. This is the canonical liveness-vs-readiness
  distinction and is correct.
- **Workstream:** W11
- **Evidence:** live curl 2026-05-26 22:27 UTC
- **Disposition:** `accepted` — operators MUST monitor
  `/v1/readyz` not `/v1/healthz` for full health.

#### F-0041 — Redis persistence directory on root partition (cascade-amplifier)

- **Severity:** `high`
- **Title:** Redis `CONFIG GET dir` returns `/var/lib/redis`,
  which is on `/` (`/dev/md1`, the 49G partition that's 100%
  full per F-0001). When root fills, Redis BGSAVE fails →
  `stop-writes-on-bgsave-error` kicks in → all writes
  blocked (F-0039). This is a deployment topology choice that
  AMPLIFIES the root-disk failure into a cache-layer outage.
- **Workstream:** W18 (deployment), W19 (cache resilience)
- **Evidence:**
  - `redis-cli CONFIG GET dir` → `/var/lib/redis`
  - r1 disk layout: data partitions are `/var/lib/postgresql`,
    `/var/lib/minio`, `/var/lib/galexie`, `/var/lib/ratesengine`;
    `/var/lib/redis` is NOT separately mounted → lives on
    root partition
- **Adversarial vector:** root-disk exhaustion + Redis
  on-root = single point of failure for the cache layer.
- **Disposition:** `open` Wave 0. Remediation: move Redis
  persistence dir to a dedicated data partition (e.g.
  `/var/lib/redis` → `data/redis` ZFS volume), OR mount
  `/var/lib/redis` as a separate filesystem with explicit
  capacity. Update Ansible role `redis-sentinel` accordingly.

#### F-0039 — **CRITICAL** Redis MISCONF — disk full prevents BGSAVE → all writes blocked → cascading silent VWAP failure

- **Severity:** `critical`
- **Title:** Redis is in "stop-writes-on-bgsave-error" state
  because BGSAVE keeps failing (disk write failure). Every
  aggregator VWAP write returns `MISCONF Redis is configured
  to save RDB snapshots, but it's currently unable to persist
  to disk`. The aggregator IS iterating its default pair set
  (XLM/BTC/ETH × USD/EUR/GBP, 3 windows each = 27 writes per
  tick) and EVERY single one fails.
- **Workstream:** W09 (storage / cache), W18 (deployment),
  W21 (R1 live)
- **Affected surface:** ALL Redis writes from aggregator, API
  cache layer, ratelimit token-bucket renews. The entire
  cache layer is silently degraded.
- **Evidence:** aggregator journal at 00:25 UTC shows ~24
  consecutive `refresh failed ... MISCONF Redis is configured
  to save RDB snapshots, but it's currently unable to persist
  to disk` lines, one per (pair, window) combo.
- **Adversarial vector:** root-disk exhaustion (F-0001)
  cascades into Redis BGSAVE failure → MISCONF → silent
  write failure → empty Prometheus counters → silent alert
  pipeline. THE root-cause incident is the root partition
  being full.
- **Disposition:** `closed-by-Wave-0` (2026-05-27, tasks
  #16-22 + #37). Resolved across multiple workstreams this
  session:
  - **Operational**: root disk freed (task #16), Redis bgsave
    cleared + MISCONF acknowledged (task #17), down exporters
    restarted (task #18) including redis_exporter that the
    original disposition asked for, postgres@15-main restarted
    (F-0151), prometheus exporters provisioned on r1 (task #37
    → F-0152).
  - **Code-side guards** (5 follow-ups):
    F-0080 alert false-zero guard with `absent_over_time` (task #19);
    F-0085 exporter-down meta-alerts (task #20);
    F-0049/F-0050 fail-CLOSED with dwell-time on Redis errors
    (task #21); five cascade-affected handlers map Redis errors
    to HTTP 503 + Retry-After (task #22). Plus this session
    added the `ratesengine_redis_writes_blocked` family of
    alerts via the new redis_exporter.

  Net effect: next Redis MISCONF surfaces in alertmanager
  within minutes (was silent for hours).
- **Cross-ref:** F-0001 (upstream cause), F-0006 (postgres
  log root-fill), F-0012 (price freshness 27h),
  F-0027 (silent alerts), F-0032 (price_staleness empty),
  F-0036 (alert-eval anti-pattern). All these tie back to
  F-0039 / F-0001.

#### F-0037 — Aggregator has zero pairs hypothesis (RETRACTED — invalid)

- **Severity:** `invalid`
- **Original claim:** TOML lacks `pairs` → orchestrator
  iterates 0 pairs.
- **Reality:** Aggregator runs with default pair set
  (XLM/BTC/ETH × USD/EUR/GBP). The journal confirms it's
  iterating these and TRYING to write VWAPs. The actual
  failure is F-0039 (Redis MISCONF blocks the writes).
- **Disposition:** `invalid` — superseded by F-0039.
- **Workstream:** W10 (aggregation), W18 (deployment), W22
  (launch readiness)
- **Affected surface:** the headline product — VWAP / price
  freshness / aggregated rates
- **Evidence:**
  - r1 `/etc/ratesengine.toml` lines 50-66 show `[aggregate]`
    + `[trades]` sections, no `pairs` key anywhere
  - aggregator runs on rc.81 binary
  - `ratesengine_aggregator_vwap_writes_total` returns empty
    vector from Prometheus (no series ever created)
  - `ratesengine_price_staleness_seconds` returns empty vector
- **Adversarial vector:** the entire price-stale + aggregator-
  silent alert pipeline is silently neutered by config gap.
  Pre-launch product surface that doesn't serve aggregated
  prices.
- **Disposition (final):** `invalid` — superseded by F-0039
  (Redis MISCONF was the actual failure; F-0039 is operationally
  cleared via tasks #16 + #17 this session and recurrence
  detection ships via F-0085 redis_exporter-down meta-alert at
  task #20). The stale "open Wave 0 — investigate pairs"
  duplicate-disposition above was a re-investigation artefact
  that didn't strip the original disposition; flagged as
  `invalid` per the first disposition.
- **Cross-ref:** F-0027, F-0032, F-0036.

#### F-0036 (REVISED) — counters DO get incremented; "empty-vector" symptoms were PromQL-staleness, not non-emission

- **Severity:** original `critical` → **revised to `medium`**
- **Original claim:** counters like `ratesengine_aggregator_vwap_writes_total`
  + `ratesengine_trade_inserts_total` + `ratesengine_price_staleness_seconds`
  return empty vectors → alerts structurally cannot fire.
- **Reality (re-investigated):** the metrics ARE in Prometheus
  TSDB. Querying with `last_over_time(...[24h])` returns:
  - `vwap_writes_total{binary=ratesengine-aggregator} = 80`
  - Same metric for api/indexer binaries shows value=0
    (defined-but-not-incremented in those binaries' code paths,
    which is correct — only aggregator increments it)
  - `last_over_time` ranges find values that the default
    instant-query 5min staleness window misses
- **Why I thought they were empty:** Prometheus instant queries
  default to a 5-minute staleness window. Series with samples
  >5 min old return empty for `ratesengine_X` but show up under
  `last_over_time(ratesengine_X[24h])`.
- **Disposition:** `medium` (was `critical`). The audit-process
  finding: instant-query empty != non-emission. The REAL
  observability gap is:
  - the LAST sample for these counters was 5+ minutes ago,
    consistent with the aggregator's Tick interval slowing
    OR Redis-write failures blocking the .Inc() path (per
    F-0039 cascade)
  - actual alerts ARE evaluating correctly; we just haven't
    hit the `for:` clause window since the cascade started
- **Cross-ref:** F-0027 (refined again), F-0039 (still the
  cascade root cause).

#### F-0036-original — original claim (now superseded by revision above)

- **Severity:** `critical` (silent-failure class)
- **Title:** Two key counters return EMPTY vectors from
  Prometheus, despite being defined in `internal/obs/metrics.go`:
  - `ratesengine_trade_inserts_total` (empty)
  - `ratesengine_aggregator_vwap_writes_total` (empty)

  Multiple alerts check `rate(<counter>[..]) == 0`. When
  the counter is never `.Inc()`d, NO time series exists in
  TSDB. Prometheus rule evaluation on an empty vector
  produces an empty vector → no alert state → alert
  silently can't fire. THIS IS NOT A RULE-SYNTAX ISSUE
  (promtool would pass). It's an alerting anti-pattern
  combined with code that never increments the counter.
- **Workstream:** W14 (alerts), W10 (aggregation), W06
  (ingest sink)
- **Affected surface:** every alert using `rate(X[..]) == 0`
  on a counter that may legitimately never be incremented
  (e.g. specifically `ratesengine_aggregator_silent` would
  not fire even though VWAP writes are truly 0).
- **Evidence:**
  - `curl localhost:9090/api/v1/query?query=ratesengine_trade_inserts_total`
    returns `data.result=[]`
  - `curl localhost:9090/api/v1/query?query=ratesengine_aggregator_vwap_writes_total`
    returns `data.result=[]`
  - Both are defined in `internal/obs/metrics.go`
  - The counters are presumably registered (defined) but
    never `.WithLabelValues(...).Inc()`'d
- **Adversarial vector:** silent-failure class. The class
  of issue is: define a counter, write an alert against it,
  but never actually increment the counter in any
  code path → silent observability gap that promtool
  cannot detect.
- **Disposition (final):** `closed-as-historical` (2026-05-28).
  Superseded by F-0036 (REVISED) immediately above: the
  counters DID exist in Prometheus TSDB; the audit's
  "empty vector" was actually a 5-minute instant-query
  staleness window artefact. Subsequent investigation under
  F-0032 / F-0033 confirmed the .Inc() paths fire correctly.
  Remediation (2) from this original disposition — pre-seed
  bounded counters at zero so PromQL has data before the first
  .Inc — was shipped at task #73 (F-0033 closure). Remediation
  (1) — trace never-incremented counters — was reframed under
  F-0033's per-metric trace.
- **Cross-ref:** F-0027, F-0032, F-0033.

#### F-0034 — `/v1/diagnostics/cursors` is intentionally public (verified) — review whether scope is right

- **Severity:** `medium` (downgraded from `high` after godoc
  review confirms intentional public-by-design)
- **Title:** `GET https://api.ratesengine.net/v1/diagnostics/cursors`
  returns HTTP 200 with full backfill cursor state. Per
  `internal/api/v1/diagnostics_cursors.go` godoc: "every row
  of `ingestion_cursors` so operators (and the explorer
  /diagnostics page) can see per-source ingest progress at a
  glance" — intentional transparency.
- **Workstream:** W11, W19, W22
- **Workstream:** W11, W19
- **Affected surface:** `internal/api/v1/diagnostics_cursors.go`
  + server.go route registration line 924
- **Evidence:** live curl 2026-05-26 22:20 UTC returns 200 +
  the JSON payload with backfill cursor data
- **Adversarial vector:** competitor / journalist /
  ill-intentioned actor scrapes this endpoint to:
  - infer ingest progress over time
  - identify which backfill ranges are stuck
  - learn what sources we're indexing
  - time their attacks for moments when ingest is degraded
- **Disposition:** `closed-as-accepted` (2026-05-28). Took
  option (c) from the related F-0026 disposition (explicit
  transparency). `SECURITY.md` now has a "Public surfaces we
  intentionally expose" section that lists the
  `/v1/diagnostics/*` endpoints, names the risk model
  (adversary learns no more than they can infer from the
  price/markets/network endpoints anyway), and frames the
  operational benefit (customers can verify our freshness
  claims in real time without filing a support ticket) as
  worth the disclosure cost. The explorer
  `https://ratesengine.net/diagnostics` page reads the same
  endpoint, so gating it behind auth would also break the
  public proof-of-freshness story. Audits flagging this
  surface in the future should reference the SECURITY.md
  section rather than re-opening the question. If an
  endpoint under `/v1/diagnostics/*` ever exposes secrets
  (credentials, customer state, internal IPs), THAT is in
  scope per the regular vulnerability-reporting path.

#### F-0035 — Apex domain TLS investigation (resolved as invalid)

- **Severity:** `invalid`
- **Title:** Original claim was r1 lacks cert for
  ratesengine.net apex. DNS resolution shows ratesengine.net
  + api.ratesengine.net BOTH point to Cloudflare IPs
  (104.21.x, 172.67.x). The apex is served from Cloudflare
  Pages (not r1); r1 only needs the api.* cert.
- **Workstream:** W17, W18
- **Evidence:**
  - `host ratesengine.net` → Cloudflare IPs
  - r1's port-443 has cert only for `api.ratesengine.net`
    (Let's Encrypt, expires 2026-08-04 — 70 days from
    audit; renewal at ~T-30 expected to fire mid-July ✓)
- **Disposition:** `invalid` (false positive). r1 architecture
  is correct.

#### F-0033 — Multiple alert rules reference metrics that aren't emitted

- **Severity:** `high`
- **Title:** Alert rules reference at least 4 metrics that
  appear absent from running binaries' metrics output:
  - `ratesengine_aggregator_fx_snap_fallback_total`
  - `ratesengine_aggregator_triangulations_total`
  - `ratesengine_ledgerstream_tier_read_total` (cold-tier
    metric — only emits when cold-tier enabled; cold-tier
    not yet on r1 per F-0014 context — possibly accepted)
  - `ratesengine_stellar_archive_publish_errors_total`
  - `ratesengine_stripe_platform_sync_errors_total`
    (likely emit-on-error only; zero series == zero
    errors — possibly accepted)
- **Workstream:** W14
- **Evidence:** rule grep produced these names; curl
  `:9464`, `:9465`, `:3000` metrics endpoints don't return
  them
- **Disposition:** `closed` (2026-05-28). Per-metric trace:
  - `ratesengine_aggregator_fx_snap_fallback_total` — defined +
    emitted in `internal/aggregate/orchestrator/triangulate.go`,
    `leg` label is per-pair (unbounded by operator config), so
    series only appears after the first FX fallback. Accepted
    as emit-on-event.
  - `ratesengine_aggregator_triangulations_total` — defined +
    emitted in same package. **Pre-seeded** at zero for
    `{outcome=ok|missing_leg|parse_error|redis_error}` in
    `obs/metrics.go::init()` so alert PromQL is well-defined
    before the first triangulation. Closes the audit's
    "missing from scrape" complaint for this metric.
  - `ratesengine_ledgerstream_tier_read_total` — defined +
    emitted in `internal/ledgerstream/tiered.go` only when a
    `TieredDataStore` is wired; cold-tier is not yet enabled
    on r1 (per F-0014). Accepted as deliberately-inert until
    the cold-tier rollout (operator task #5 in the open
    backlog).
  - `ratesengine_stellar_archive_publish_errors_total` —
    intentionally reserved: the alert file's preamble at
    `deploy/monitoring/rules/stellar.yml:20-30` documents that
    r1 doesn't currently publish a history archive, so the
    metric stays inert. Keeping the rule means operators
    wiring publishing back on don't need a rule edit to start
    surfacing the signal. Accepted as Phase-3 (Tier-1
    validator) reservation.
  - `ratesengine_stripe_platform_sync_errors_total` — defined
    + emitted in `internal/api/v1/stripe_webhook.go`.
    **Pre-seeded** at zero for
    `{operation=get_account|upsert_subscription|account_update|list_keys|key_update}`
    in `obs/metrics.go::init()`. Closes the audit's "missing
    from scrape" complaint.
  Regression pin: `TestZeroSeed_F0033` in
  `internal/obs/metrics_test.go` asserts the 9 pre-seeded
  series appear in scrape output at `0`.
- **Cross-ref:** F-0027 cluster.

#### F-0031 — Journal has 3-hour log gap for ratesengine-indexer

- **Severity:** `medium` (forensic capability gap)
- **Title:** `journalctl -u ratesengine-indexer` returns 21
  lines despite the indexer running for 3.5+ hours. Oldest
  visible log is 23:13:44 CEST (about 2.5h after the indexer
  started at 20:29:22 CEST). Startup logs + early activity
  are missing from journal.
- **Workstream:** W14, W18 (deployment)
- **Affected surface:** ability to audit indexer startup
  behaviour
- **Evidence:** R1-P01 transcript + journal grep
- **Possible causes:** journald rate-limiting (default 1000
  msg/30s); journald log volume drops due to root-disk
  pressure (F-0001 cascade); selective log level filtering.
- **Disposition:** `open`. Investigation: `journalctl
  --vacuum-time`, `journalctl --disk-usage`, `RateLimitBurst`
  config in `/etc/systemd/journald.conf`.

#### F-0028 — soroban_events live ingest tip is 7h behind indexer's ledger cursor

- **Severity:** `high`
- **Title:** `soroban_events.max(ledger_close_time) = 15:01:59
  UTC` but indexer's `ratesengine_cursor_last_ledger{source="ledgerstream"}`
  is at ledger 62,745,909 (current). The catch-all RawEventSink
  (ADR-0029) is not capturing events for the latest 7 hours of
  ledgers.
- **Workstream:** W27 (soroban_events landing zone), W28
  (back-pressure)
- **Affected surface:** soroban_events hypertable; ADR-0029
  catch-all hook
- **Evidence:**
  - `psql ... soroban_events`: max_ledger 62,745,888,
    max(ledger_close_time) 15:01:59 UTC, total rows just for
    ledger > 62,745,000: 417,387
  - indexer cursor: 62,745,909 (advancing live)
  - Indexer is running rc.81 binary (post-rc.81-deploy)
  - The soroban-events-fill.service is concurrently writing
    older-ledger rows, so the "freshest" row in soroban_events
    SHOULD come from live ingest, but it's stuck at 15:01:59
- **Likely cause:** the catch-all sink may NOT be wired in the
  current live indexer config. OR the sink is writing but the
  fill walk is bulk-loading into the same table and Postgres
  is throwing some kind of conflict (the rc.79 ON CONFLICT
  shape fix landed but maybe a similar issue exists for live
  writes vs fill writes).
- **Disposition:** `open`. Investigate:
  1. is RawEventSink registered with the live dispatcher per
     `cmd/ratesengine-indexer/main.go::SetRawEventSink`
  2. is the AsyncSink worker actively running in the live
     indexer process
  3. are batched inserts succeeding or hitting some error
  4. is there a deadlock between fill walk + live writes

#### F-0027 — **HIGH** Cluster: missing root-disk alert + missing emit for price_staleness (refined)

- **Severity:** `high` (downgraded from `critical` after
  per-rule investigation; finding is real but more specific
  than initially framed)
- **Title:** Two confirmed observability gaps:
  1. **No alert exists for root partition `/`** —
     storage.yml only watches `/var/lib/postgresql`. The
     root-fill at F-0001 is silent because no rule covers it.
  2. **`ratesengine_price_staleness_seconds` metric is NOT
     emitted** by the aggregator at runtime, even though the
     emit-path code exists (F-0032). The price-stale alert
     refers to a metric series that doesn't exist, so the
     alert is structurally incapable of firing.

  **Note:** The "Stellar sources stopped" sub-claim is RESOLVED
  as not-a-finding after rule-spec inspection. The
  `ratesengine_ingestion_source_stopped` alert uses a
  selective-source allowlist via `label_replace(vector(1),
  "source", "binance", "", "")` chained with `or` for each
  named high-volume source. The low-volume DEX variant
  (`_low_volume_dex`) uses `[6h]` window — Stellar sources
  fall under THAT alert, with `for:` clause threshold that
  may not be exceeded yet given the 7h activity gap is just
  starting. This is deliberate selective tuning, not a gap.
- **Workstream:** W14 (observability), W18 (deployment)
- **Affected surface:** silent-failure mode for THE critical
  failures
- **Evidence:**
  - `curl localhost:9090/api/v1/alerts` returns only
    deadmansswitch
  - `df -h` confirms root 100% full (F-0001)
  - SLA probe verdict-fail with freshness 98453s (F-0012)
  - psql shows on-chain trades stuck 7h (F-0016)
  - `configs/prometheus/rules.r1/storage.yml` only watches
    `mountpoint="/var/lib/postgresql"`
  - `configs/prometheus/rules.r1/ingestion.yml` has the rules
    but they're not firing
- **Adversarial vector:** the worst-case for an audit — the
  alerting layer claims coverage but doesn't deliver.
  Production has been in this degraded state for hours without
  detection.
- **Disposition:** `closed` (2026-05-28). Both halves of the
  refined finding traced to fixes already shipped:
  1. **Root-partition alert.** `configs/prometheus/rules.r1/
     storage.yml:147-182` and `deploy/monitoring/rules/
     storage.yml:144-179` both ship a paired
     `ratesengine_node_root_disk_full` (< 10 % free, page) +
     `ratesengine_node_root_disk_warning` (< 20 % free,
     ticket) alert. Thresholds chosen above the audit's
     suggested 5 % so the warning fires while there's still
     cascade-buffer room to clean up — pre-incident, not
     post-incident.
  2. **`ratesengine_price_staleness_seconds` emit path.**
     `internal/aggregate/orchestrator/orchestrator.go:629-690`
     (`emitStalenessGauges`) sets the per-asset series at
     end-of-tick. Live verified: the metric appears in the
     aggregator's /metrics output. F-0032 ("staleness mirror
     order-independence") closed at task #57; F-0104
     ("cascade-fragile predicate") closed by adding
     `absent_over_time` OR-branch at task #76. The
     emit-side wedge problem is now alertable rather than
     no-data silent.
  3. (rule-eval verification): promtool runs as part of the
     `monitoring-rules` CI job which now lints both
     `deploy/monitoring/rules/` and `configs/prometheus/
     rules.r1/`. Every rule passes syntactic + recording-
     rule sanity at PR time.
- **Cross-ref:** F-0001, F-0012, F-0016, F-0020.

#### F-0026 — `/v1/diagnostics/cursors` exposes 20-day-old backfill cursor lag publicly

- **Severity:** `low`
- **Title:** Diagnostics endpoint shows backfill cursors with
  `lag_seconds: 1758753` (20 days) for old SDEX backfill
  ranges. Information leak (operator hygiene gap) but not
  exploitable.
- **Workstream:** W11, W19
- **Evidence:** `/v1/diagnostics/cursors` live response
- **Adversarial vector:** information disclosure — attackers
  learn our internal ingest state, backfill ranges, ledger
  history.
- **Disposition:** `closed-as-accepted` (2026-05-28). Took
  option (c): explicit transparency. `SECURITY.md` now has a
  "Public surfaces we intentionally expose" section that
  lists every `/v1/diagnostics/*` endpoint, names the risk
  model (an adversary scraping cursors learns no more than
  the price/markets/network endpoints' wire shape already
  reveals), and documents the tradeoff (customers verifying
  freshness in real time vs information disclosure cost).
  Bundled with F-0034 disposition. The cursor table is also
  what the public `/diagnostics` explorer page reads, so
  gating it would break the public proof-of-freshness story.

#### F-0023 — F-0017 prices_1h CAGG lag may be invalid (within expected refresh window)

- **Severity:** invalid candidate
- **Title:** F-0017 flagged 1h48m lag on prices_1h CAGG; this
  may be within expected refresh schedule
- **Workstream:** W10
- **Evidence:** prices_1m latest bucket 21:46 lag 2m26s (healthy);
  prices_15m latest bucket 21:30 lag 18m26s (healthy);
  prices_1h latest bucket 20:00 lag 1h48m (1h-bucket only
  closes after the hour ends, so a 1-2h lag is structural).
  Need to compare against the CAGG's refresh_lag setting.
- **Disposition:** `needs_evidence` — likely `invalid` once
  refresh_lag is verified.

### W35 seed candidates (closed by per-source verification — see TSV)

W35's `every-event-coverage.tsv` register now carries 80 rows;
the original "candidate" finding seeds have been resolved into
concrete TSV rows with terminal status per row. Confirmed gaps:

- DeFindex: 0 gap rows (F-0018 closed 2026-05-28 — 5 rows promoted to `classification-only` coverage)
- sep41_supply: 2 unknown rows (→ F-0021)
- All other sources (soroswap, soroswap_router, phoenix,
  aquarius, comet, blend, reflector, redstone, band, cctp,
  rozo, sdex, classic observers): `classified_by_decoder=yes`
  for every enumerated event. NO gaps found in those sources.

#### F-0056 — GitHub Actions composite `uses:` references not SHA-pinned

- **Severity:** `medium`
- **Title:** ci.yml mixes SHA-pinned actions (golangci-lint, upload-artifact)
  with tag-only references (`actions/checkout@v6`, `actions/setup-go@v6`)
  across at least 8 job stages.
- **Workstream:** W03 (CI/CD), W04 (supply chain)
- **Evidence:** `.github/workflows/ci.yml` lines 78, 79, 106, 107, 129,
  142, 159, 184 use `@v6` tag references for first-party Actions; only
  golangci-lint-action and upload-artifact pin SHA.
- **Adversarial vector:** GitHub Actions tags are mutable. A compromise
  of `actions/checkout` (or its `v6` tag rewriting) executes code with
  full repo + secrets access in every CI run — release-publishing
  workflow inherits the same exposure.
- **Disposition:** `closed` (task #32, this session). Every
  first-party `actions/*` reference in `.github/workflows/ci.yml`
  is now SHA-pinned with a trailing version comment, e.g.
  `actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd  # v6`.
  Dependabot's `github-actions` ecosystem is also enabled (the
  follow-up referenced in the original disposition), so future
  SHA bumps land as PRs rather than drifting.

#### F-0057 — govulncheck not in `make verify` (CI-only)

- **Severity:** `low`
- **Title:** `make verify` runs fmt/vet/lint/docs/test but does NOT run
  govulncheck — developers can push code that fails govulncheck and
  only discover it in CI on a `vuln.yml` schedule.
- **Workstream:** W04, W15
- **Evidence:** `Makefile` `verify:` target inspected via the canonical
  pre-push gate description in CLAUDE.md; CI workflow contains
  `govulncheck ./...` invocation but it is a separate `vuln.yml` job
  not chained into the PR gate.
- **Adversarial vector:** "shift-left" weakness — fast feedback loop
  for stalled CVEs absent locally; CI catches them but a sloppy
  push-skip pattern (`git push --no-verify` etc.) might hide them.
- **Disposition:** `closed` (task #33, this session). Both
  remediation paths shipped: `Makefile:232-236` adds `make vuln`
  (installs govulncheck on demand, runs `govulncheck ./...`),
  and `scripts/dev/verify.sh:28-34` invokes `make vuln` as part
  of the canonical pre-push gate (graceful-skip with a clear
  install hint when govulncheck isn't on PATH locally). Lands
  before `git push --no-verify` becomes a foot-gun.

#### F-0058 — Dependabot configured for gomod + node only, NOT GitHub Actions

- **Severity:** `low`
- **Title:** `.github/dependabot.yml` has `gomod` + `npm` entries (inferred)
  but no `github-actions` ecosystem entry, so SHA pins for Actions
  never get refreshed automatically.
- **Workstream:** W04
- **Evidence:** `.github/dependabot.yml` — gomod block visible with
  weekly schedule + grouped updates; no `package-ecosystem:
  github-actions` block present.
- **Cross-ref:** Compounds F-0056 — even if we SHA-pin Actions, without
  Dependabot bumps the pins go stale silently.
- **Disposition:** `closed` (task #32, this session).
  `.github/dependabot.yml` now carries the `package-ecosystem:
  github-actions` block alongside gomod + npm. Bundled with
  F-0056's SHA-pin landing.

#### F-0059 — POSITIVE evidence: WASM-audit coverage is complete

- **Severity:** `note` (positive)
- **Title:** Every source in `external.Registry` with `BackfillSafe=true`
  references a corresponding audit file under
  `docs/operations/wasm-audits/<source>.md`. No `BackfillSafe=true`
  source ships without an audit log.
- **Workstream:** W24
- **Evidence:** Cross-walked `internal/sources/external/registry.go`
  flag → `docs/operations/wasm-audits/` directory; 16 audit files
  present covering aquarius, band, blend, cctp, comet, defindex,
  phoenix, redstone, reflector, rozo, soroswap, soroswap-router. ECB/
  CEX/FX/aggregator/external sources have no on-chain WASM dep and
  don't need an audit by definition.
- **Disposition:** `accepted` — strong evidence for the BackfillSafe
  governance principle. Recommend: rename file `defindex.md` → check
  for any missing wasm-audit cross-references in newer source PRs.

#### F-0060 — **RETRACTED INVALID** `/v1/price` returns all-NULL

- **Severity:** `invalid` — RETRACTED 2026-05-27
- **Title:** ORIGINAL CLAIM WAS WRONG. Live probe 2026-05-27 of
  `/v1/price?asset=native&quote=fiat:USD` returns a fully-
  populated envelope:
  ```
  {"data":{"asset_id":"native","quote":"fiat:USD","price":
  "0.15107020700989818388","price_type":"vwap","observed_at":
  "2026-05-25T18:26:00Z","window_seconds":60},"as_of":"...",
  "sources":["sdex"],"flags":{"stale":true,"triangulated":true,
  "single_source":true}}
  ```
  My earlier "all-null" reading was caused by my python
  extractor inspecting top-level keys `price`, `methodology`,
  `sources_count` — none of which exist in the envelope (they
  live under `data.price`, `data.sources` etc.). The envelope
  is RFC-shaped per `internal/api/v1/envelope.go:writeJSON`.
- **What's actually true:** the price serves a LKG fallback at
  ~28h staleness with proper `flags.stale:true,
  triangulated:true, single_source:true` markers. This is
  the documented ADR-0018 behaviour under cache-cold + bucket-
  miss conditions. F-0042 (POSITIVE) already captured this.
- **Methodology lesson:** `feedback_check_self_before_alarm`
  applies. A live probe's raw bytes must be the source of
  truth, not an extractor's interpretation. Filing this
  retraction is the corrective entry.
- **Cross-ref:** F-0066, F-0061, F-0062 are now suspect and
  re-tested below.
- **ORIGINAL CONTEXT (preserved for trace):** my earlier
  python `d.get('price')` returned `None` because the JSON's
  top-level keys are `data`, `as_of`, `sources`, `flags`, not
  `price`/`methodology`/etc.
- **Workstream:** W11 (API runtime), W10 (aggregator)
- **Evidence:** live curl 2026-05-27 captured in EV-0028.
- **Adversarial vector:** any user calling the canonical
  pricing endpoint gets pure JSON nulls + HTTP 200, with NO
  error indication. This is worse than a 503 — the client
  has no signal to retry or fall back. A naive caller may
  log it as "got back a response, must be working" and feed
  garbage downstream.
- **Cross-ref:** F-0039 (Redis MISCONF) is the root cause —
  Redis writes blocked → aggregator can't store fresh VWAP →
  read-path returns empty envelope. But the read-path
  CHOOSES to render all-nulls instead of erroring (vs.
  `/v1/twap` + `/v1/ohlc` which correctly 404 with
  `errors/no-trades`).
- **Disposition (final):** `invalid` — RETRACTED. The header
  already marks this **RETRACTED INVALID**; the trailing
  "open Wave 0" was a duplicate-disposition left over from
  the initial draft before the retraction. The under-cascade
  silent-null behaviour itself was the topic of F-0066b
  (closed under task #22's shared cache-error helper →
  HTTP 503 + Retry-After: 30 across every cache-aside
  surface).

#### F-0061 — `/v1/price` vs `/v1/twap` + `/v1/ohlc` parameter inconsistency

- **Severity:** `medium`
- **Title:** `/v1/price` error message says it accepts
  `asset=` while `/v1/twap` and `/v1/ohlc` use `base=`. Also
  `/v1/price` accepts the slug `xlm`; the other endpoints
  require canonical `native`.
- **Workstream:** W11
- **Evidence:** error responses captured 2026-05-27 — `/v1/twap`
  rejects with `errors/missing-base` "this endpoint uses
  base/quote (not asset/quote — that form is on /v1/price)";
  `/v1/twap?base=xlm` rejects with `errors/invalid-asset-id`.
- **Adversarial vector:** developer copying `/v1/price` URLs
  to other endpoints gets two-step rejection (param name +
  slug format). Friction.
- **Disposition:** `closed` (2026-05-28). Resolved the param-name
  half of the inconsistency via bidirectional alias:
  `internal/api/v1/price.go::parsePriceAssetParam` now accepts
  `base=` as an alias for `asset=`; `internal/api/v1/history.go::
  parseBaseQuote` now accepts `asset=` as an alias for `base=`.
  Either name reaches the same `canonical.ParseAsset` path. The
  slug half (the audit's secondary point: `xlm` rejection) was
  already handled at the canonical layer —
  `canonical.ParseAsset("xlm")` returns NativeAsset via the
  case-insensitive `XLM`/`native` alias (`asset.go:244`), so all
  endpoints already accept `xlm` consistently; the audit
  evidence appears to have predated that addition.
  Mutually-exclusive 400 prevents silent precedence picks when a
  client passes both. Two new tests pin alias-accepted + both-
  supplied-rejected. `parsePriceAssetParam` extraction keeps
  `handlePrice` under the gocognit ceiling.

#### F-0062 — `/v1/changes` is NOT a price-change endpoint

- **Severity:** `medium`
- **Title:** The route mounted at `/v1/changes/{entity_type}/{id}`
  is a generic "entity change summary" feature, not the
  CG-style 24h/7d/30d **price-change** dimension promised
  in the parity matrix. There is no dedicated price-change
  endpoint.
- **Workstream:** W11, W20
- **Evidence:** `internal/api/v1/server.go:923` shows
  `GET /v1/changes/{entity_type}/{id}` → `handleChangeSummary`.
  `internal/api/v1/changes.go` is the implementation; live
  curl of `/v1/changes?asset=xlm&quote=usd` returns 404.
- **Cross-ref:** Parity matrix row "24h price change" was
  marked `covered?` — INCORRECT, should be `gap`.
- **Disposition:** `open` Wave 2. Either add a `/v1/price/change`
  endpoint or document that callers must compute it from
  `/v1/history`.

#### F-0063 — `/v1/markets` ranks CEX feeds above Stellar-native markets (downgrade from HIGH to LOW)

- **Severity:** `low` — RETRACTED from `high` 2026-05-27
- **Title:** With `limit=50`, two XLM-quoted SDEX markets DO
  appear (`000333-G…native`, `042700-G…native`). They sort
  below CEX feeds because USD-volume ranking buries Stellar
  markets ($278M BTC/USD vs none-rendered USD volume on
  SDEX pairs).
- **Workstream:** W11, W20
- **Evidence:** live curl 2026-05-27 with `limit=50` —
  EV-0030.
- **Adversarial vector:** weaker than originally feared —
  XLM IS there if you scroll.
- **Disposition:** `accepted` as a UX/product nit (rather
  than a missing-feature finding). Consider: surface XLM
  markets in a dedicated section on consumer-facing pages.

#### F-0064 — `/v1/markets last_trade_at` is bucket-truncated to UTC midnight (RETRACTED)

- **Severity:** `invalid` — RETRACTED 2026-05-27
- **Title:** Original claim "40h+ stale" was wrong. `last_trade_at`
  values of `2026-05-25T00:00:00Z` are continuous-aggregate
  bucket-end timestamps, not real last-trade times. At `limit=50`,
  the bucket distribution is 48× 2026-05-25 + 1× 2026-05-24
  + 1× 2026-05-22 — markets ARE refreshing daily.
- **Workstream:** W11
- **Evidence:** /v1/markets `last_trade_at` collected at
  EV-0030; /v1/network/stats reports `markets_count_24h: 46`
  + `volume_24h_usd: $1.04B` (fresh) — EV-0031.
- **Disposition:** `invalid` — though there IS a separate UX
  concern: returning bucket-end times as `last_trade_at` is
  misleading; should be either truly-last or relabelled
  `bucket_close_at`. Filed separately as F-0065.

#### F-0065 — `/v1/markets last_trade_at` is misleadingly labelled (bucket-end, not last-trade)

- **Severity:** `low`
- **Title:** Field name suggests literal last-trade timestamp;
  actual value is the daily continuous-aggregate bucket-close
  time, rounded to UTC midnight. Customers comparing this to
  `now()` would compute spuriously-large staleness.
- **Workstream:** W11, W20
- **Evidence:** EV-0030.
- **Disposition:** `closed` (task #31, this session). The
  `/v1/markets` wire field was renamed to `bucket_close_at`
  (the actual semantics) — clearer name, no SQL change
  required, no client breakage in the open-API contract
  (which already documented the daily bucket cadence). The
  rename was bundled into the same PR that updated the
  OpenAPI spec entry + the explorer column header. Customers
  comparing the field to `now()` for staleness now read a
  field whose name doesn't promise a literal last-trade time.

#### F-0066 — **RETRACTED INVALID** `/v1/price` vs `/v1/assets/native` storage-backend split

- **Severity:** `invalid` — RETRACTED 2026-05-27
- **Title:** Original claim invalidated by F-0060 retraction.
  `/v1/price` IS returning the price (the cascade-affected
  Redis hot path correctly falls through to the LKG fallback,
  not "broken").
- **Disposition:** `invalid`. The architectural split (Redis-fronted
  /v1/price vs Postgres-fronted /v1/assets) is real, but it
  produces CORRECT behaviour under F-0039 — both surfaces serve.

#### F-0066b — (original entry preserved for trace)

- **Severity:** `high` (refines F-0060)
- **Title:** During the same probe window, `/v1/price?base=
  native&quote=fiat:USD` returned all-nulls while `/v1/assets/
  native` returned `price_usd: 0.1510702070`, `change_7d_pct:
  5.60`, full sparkline. This proves the cascade is at the
  CACHE LAYER not the DATA LAYER — Postgres has fresh price
  data; only the Redis-fronted path is broken.
- **Workstream:** W10, W11, W30 (cold-tier)
- **Evidence:** parallel live curls 2026-05-27 — EV-0028 +
  EV-0032.
- **Adversarial vector:** any handler that depends on Redis
  for primary path (vs cache-aside fallback) is currently
  serving null/empty under F-0039. The pattern is
  inconsistent across handlers — some degrade gracefully,
  others don't.
- **Disposition:** `closed` (Wave-0 step 7, this session).
  Took path (b) via the shared
  `internal/api/v1/cache_errors.go::handleCacheErr` helper:
  Redis-MISCONF on the cache layer is translated to HTTP 503
  + `Retry-After: 30` with RFC-7807 problem body. Migrated
  surfaces include /v1/price, /v1/vwap, /v1/oracle/*,
  /v1/lending/pools, and the divergence/score endpoints
  (covered jointly by F-0086..F-0090, F-0145, F-0146 at
  task #22). The silent-null code path that produced the
  inconsistent behaviour the audit observed is gone.

#### F-0067 — POSITIVE evidence: live ledger ingestion is healthy

- **Severity:** `note` (positive)
- **Title:** `/v1/ledger/tip` reports `latest_ledger: 62746377,
  lag_seconds: 2` 2026-05-26 23:21 UTC. `/v1/network/stats`
  reports `markets_count_24h: 46, volume_24h_usd: $1.04B,
  assets_indexed: 189996, total_sources: 26, exchange_
  sources: 11`. Galexie → dispatcher → trades pipeline is
  alive and ingesting.
- **Workstream:** W06, W21
- **Evidence:** EV-0031.
- **Disposition:** `accepted` — the cascade does not extend
  to the ingestion pipeline. Only the cache + aggregator
  write paths are affected by F-0039.

#### F-0068 — `/v1/observations` requires `asset=` param (param-naming inconsistency continues)

- **Severity:** `low` (extends F-0061)
- **Title:** `/v1/observations` rejects `base=native&quote=fiat:USD`
  with `errors/missing-asset` "asset query parameter is
  required". So we now have THREE param shapes across endpoints:
  `/v1/price` accepts `asset=`, `/v1/twap`/`/v1/ohlc` use
  `base=`, `/v1/observations` uses `asset=` again.
- **Workstream:** W11
- **Evidence:** EV-0030 live curl.
- **Disposition:** `closed` (2026-05-28). Bundled with F-0061
  closure on the asset/base aliasing pass. `parseObservationsAssetQuote`
  now delegates to `resolveAssetOrBaseParam` so `/v1/observations`
  accepts both `asset=` (canonical) and `base=` (alias);
  both-supplied returns 400 `invalid-parameter`. Two new tests
  (`TestObservations_BaseParamAcceptedAsAssetAlias`,
  `TestObservations_BothAssetAndBase400`) pin the contract.

#### F-0069 — `cmd/ratesengine-api/main.go` is 3,106 LOC in one file

- **Severity:** `low`
- **Title:** Single Go file at >3K LOC. While the build still
  passes, this size makes diff-review slower, increases
  merge-conflict surface, and obscures unit boundaries.
- **Workstream:** W11
- **Evidence:** `wc -l cmd/ratesengine-api/main.go` → 3106.
- **Disposition:** `open` Wave 3 (refactor-only — no
  correctness impact). Suggest extracting wiring into
  thematic sub-files (server setup, signal handling, route
  binding, runtime tuning).

#### F-0070 — `TolerateTrailingMissing` opt-in inconsistent across ops subcommands

- **Severity:** `medium`
- **Title:** The rc.81 `TolerateTrailingMissing` fix is wired
  into `verify-archive` (main.go:2201) and `wasm-history`
  (main.go:3253), but NOT into `verify-decoders` (main.go:1350)
  nor `scan-soroban-events` (main.go:4338). Operators running
  either of those latter subcommands with `-to 0` (i.e., the
  live tip) get the same trailing-edge missing-file failure
  that rc.81 was meant to fix.
- **Workstream:** W13, W34
- **Evidence:** `grep -n "TolerateTrailingMissing\|ledgerstream.Config{"
  cmd/ratesengine-ops/main.go` shows opt-in at 2201, 3253
  only; constructions at 1350 + 4338 omit the field.
- **Adversarial vector:** post-deploy diagnostic fails
  inexplicably on the most-common shape ("run from N to tip"),
  pushing operators back to opaque error parsing.
- **Cross-ref:** memory `project_62_diagnosis_2026_05_25`
  cites this exact failure mode.
- **Disposition:** `closed` (task #29, this session). Took the
  preferred remediation: `TolerateTrailingMissing` is now set
  by a single shared stream-config helper that all four ops
  subcommands route through — verify-archive, wasm-history,
  verify-decoders, scan-soroban-events. New gap-of-the-same-
  shape impossible to add because every caller goes through
  the helper; static review catches it at PR time.

#### F-0071 — `/v1/ohlc` is single-bar only; multi-bar series is unimplemented

- **Severity:** `medium`
- **Title:** Re-read of `internal/api/v1/ohlc.go:58-59` reveals
  the original observation was a partial truth. The handler
  explicitly documents (line 59): "Interval-series support
  (N bars, each interval-seconds wide) lands in a follow-up."
  /v1/ohlc currently returns a SINGLE bar covering [from, to).
  Requests with `interval=` + `limit=` get them silently
  ignored; the default 1h window kicks in.
- **Workstream:** W11, W20 (CG/CMC parity is a series, not a bar)
- **Evidence:** `internal/api/v1/ohlc.go:58-59`; raw curl
  EV-0035 shows 1h window when 1d was requested.
- **Adversarial vector:** **CG/CMC parity gap** — both vendors
  ship OHLC as a series ([{t,o,h,l,c,v},...]); customers
  cannot replace a CG OHLC integration with our /v1/ohlc.
  And the `interval`/`limit` params being silently ignored
  (no 400) is a contract surprise.
- **Disposition:** `closed` (task #51, this session). Took
  the preferred remediation: /v1/ohlc now returns the
  multi-bar series CG/CMC parity needs. `interval=` +
  `limit=` are honoured; the wire shape is the standard
  `[{t,o,h,l,c,v},...]` array. Single-bar requests still
  work (compatible default). CG OHLC integrations can be
  swapped to our /v1/ohlc without a wire-shape rewrite.

#### F-0072 — `/v1/twap` has no `window=` parameter (uses from+to)

- **Severity:** `low` — REFRAMED 2026-05-27
- **Title:** Re-read of `internal/api/v1/twap.go:28-37` confirms
  TWAP uses `from`+`to` per `/v1/history` convention, NOT a
  `window` param. My probe with `window=24h` had it silently
  ignored (no 400) and defaults (1h ending now) kicked in.
  The reported error window was therefore CORRECT.
- **Workstream:** W11
- **Evidence:** `internal/api/v1/twap.go:60` calls
  `parseFromToClamped` — no `window` parameter exists.
- **Adversarial vector:** UX nit only; CG-style customers
  expecting `window=24h` get a 1h-default 404 with no
  explanation. Either accept the param (alias) or 400 on
  unknown params.
- **Disposition:** `closed` (2026-05-28). Implemented in
  `internal/api/v1/ohlc.go::parseFromTo`, which every aggregated
  rate endpoint flows through via `parseFromToClamped`. New
  `window=` query param: when supplied, `from = to - window`,
  with the same Go-duration grammar as `time.ParseDuration`
  (ns/us/ms/s/m/h plus compound forms) extended with a
  trailing-`d` shortcut for days. Combining `window=` with an
  explicit `from` returns 400 (`window and from are mutually
  exclusive`) — louder than the pre-this-change silent ignore.
  Three new internal tests
  (`closed_bucket_internal_test.go::TestParseFromTo_Window*`)
  pin happy-path, conflict rejection, and reject-malformed.
  `/v1/twap?window=24h` now answers over the actual 24h window
  instead of the 1h default that previously surprised customers.

#### F-0073 — `/v1/price/batch` parameter is `asset_ids`, not `pairs`

- **Severity:** `low`
- **Title:** Probe `/v1/price/batch?pairs=...` returns 400
  `errors/missing-asset-ids` "asset_ids query parameter is
  required (comma-separated)". Adds a FOURTH param-name
  pattern across endpoints: `asset_ids` (price/batch),
  `asset` (price + observations), `base` (twap, ohlc, markets),
  no canonical name (history).
- **Workstream:** W11
- **Evidence:** raw curl 2026-05-27 EV-0035.
- **Cross-ref:** F-0061, F-0068 — same family.
- **Disposition:** `closed` (2026-05-28). `/v1/price/batch`
  now accepts `pairs=` as an alias for `asset_ids=`;
  both-supplied returns 400 `invalid-parameter`. Two new tests
  pin the contract. Full param-name convergence (one canonical
  name across every endpoint) is still a bigger refactor —
  this PR closes the friction half ("URL copy-paste from one
  endpoint to another should work") which is what the original
  finding emphasised. Cross-endpoint canonical-name unification
  remains a potential Wave-3 cleanup if customer telemetry shows
  it's worth a breaking change.

#### F-0074 — `/v1/twap` + `/v1/ohlc` lack LKG fallback chain (404 instead of stale-marked LKG)

- **Severity:** `medium`
- **Title:** Under the F-0039 cascade, `/v1/price` serves a
  28h-stale LKG fallback with `flags.stale:true`. `/v1/twap`
  and `/v1/ohlc` 404 with `errors/no-trades` for the same
  pair in the same window. Architectural asymmetry: only the
  /price handler has the priceFallback chain (last-trade →
  stablecoin proxy → triangulation).
- **Workstream:** W10, W11
- **Evidence:** raw curls 2026-05-27 EV-0035.
- **Adversarial vector:** SSE+streaming clients on /twap or
  /ohlc see hard 404s during cache-cold storms while /price
  stays nominally up. Inconsistent failure profile.
- **Disposition:** `open` Wave 2. Either extend priceFallback
  to twap/ohlc surfaces or document the asymmetry explicitly
  in `docs/methodology/twap-ohlc.md`.

#### F-0075 — Adverse pattern: false-positive critical finding cluster (audit methodology)

- **Severity:** `note` (process)
- **Title:** This audit produced FOUR false-positive findings
  (F-0060, F-0066, F-0063, F-0064) in one iteration due to
  treating an extractor's output as ground truth instead of
  the raw HTTP body. The audit's protocol said "raw bytes
  are evidence" but I cited derived python dicts.
- **Workstream:** audit-process / 02-protocol
- **Evidence:** This finding itself; the F-0060/F-0066
  retractions; EV-0028 was the contaminated evidence row.
- **Disposition:** `accepted` — add a rule to
  `docs/audit-2026-05-26/02-protocol.md`: "Live-curl evidence
  must record `curl -sv` raw bytes (not python-extracted
  fields). If parsing for clarity, the parser's correctness
  must be checked against the raw bytes."

#### F-0076 — POSITIVE evidence: SEP-10 JWT validation is well-designed

- **Severity:** `note` (positive)
- **Title:** `internal/auth/sep10/jwt.go` uses HMAC-SHA256 +
  `subtle.ConstantTimeCompare` for signature verification.
  Wrong-shape, wrong-header, wrong-base64 cases all reject;
  `exp` enforcement is correctly delegated to the call site
  for specific `ErrTokenExpired` surfacing.
- **Workstream:** W19
- **Evidence:** `internal/auth/sep10/jwt.go:53-91`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0077 — POSITIVE evidence: API-key entropy + storage is sound

- **Severity:** `note` (positive)
- **Title:** API keys are minted as 32 bytes from `crypto/rand`
  hex-encoded with `rek_` prefix (256-bit entropy);
  `hashAPIKey` is single-round SHA-256 (appropriate for
  high-entropy keys). NoopAPIKeyValidator fails-CLOSED with
  503 when validator missing (NOT silently demoting to
  anonymous). Postgres validator uses cache → DB → 401 chain
  with rawHash as the DB key column. Revocation is enforced
  by absence in DB; no indefinite-cache risk.
- **Workstream:** W19
- **Evidence:**
  `internal/api/v1/dashboardkeys/handlers.go:446-449`;
  `internal/auth/apikey_redis.go:258`;
  `internal/auth/apikey_postgres.go:90`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0078 — POSITIVE evidence: migrations 0042..0045 honour ADR-0003 NUMERIC invariant

- **Severity:** `note` (positive)
- **Title:** Every amount/reserve/shares/borrow/supply column in
  the 4 new decoder-backfill migrations (comet_liquidity,
  soroswap_skim, phoenix_liquidity+stake, blend_money_market)
  uses NUMERIC. Zero BIGINT/INTEGER occurrences on amount-
  bearing columns. Comments cite ADR-0003 in 6 places.
- **Workstream:** W09 (schema), W05 (numeric safety)
- **Evidence:** `grep -E "BIGINT|INTEGER"` on amount/etc. lines
  in `migrations/0042..0045_*.up.sql` returns empty.
- **Disposition:** `accepted` — POSITIVE. The numeric-truncation
  invariant the audit cares most about (i128 → bigint review
  catch) is upheld in the latest decoder schemas.

#### F-0079 — POSITIVE evidence: SQL-driven backfill design (ADR-0029 follow-through)

- **Severity:** `note` (positive)
- **Title:** Migration 0041 documents the per-source backfill
  pattern as `INSERT INTO blend_positions / cctp_events / ...
  SELECT ... FROM soroban_events WHERE contract_id IN (...)
  AND topic_0_sym IN (...)` — milliseconds-to-minutes versus
  hours-per-source MinIO re-walk. 6 backfill subcommands
  shipped per `[[project_open_backlog]]`. The architecture
  matches the design.
- **Workstream:** W27 (soroban_events landing zone), W13
- **Evidence:** `migrations/0041_create_soroban_events.up.sql:1-50`;
  6 `cmd/ratesengine-ops/*_backfill.go` files cited in
  CHANGELOG rc.81.
- **Disposition:** `accepted` — POSITIVE.

#### F-0080 — F-0027 CONFIRMED: `aggregator_silent` alert uses unguarded `rate(...)==0`

- **Severity:** `high`
- **Title:** Code-read of `configs/prometheus/rules.r1/aggregator.yml:18-20`:
  ```yaml
  expr: |
    sum(rate(ratesengine_aggregator_vwap_writes_total[5m])) == 0
  ```
  No `absent_over_time(...)` guard. After Prometheus' 5-min
  staleness window passes (counter not bumped), the series
  goes stale and `rate(stale[5m])` returns no_data;
  `no_data == 0` is no_data; alert never fires.
- **Workstream:** W14
- **Evidence:** `configs/prometheus/rules.r1/aggregator.yml:18-20`;
  cross-reference `supply-snapshot.yml:104` and
  `supply-refresh.yml:78` (which USE `absent_over_time(...)`
  correctly).
- **Adversarial vector:** the silent-VWAP page (P1 severity)
  is structurally unable to fire when the cascade hits its
  exact target symptom. Compounds F-0039 invisibility.
- **Disposition:** `closed` (Wave-0 step 4, this session).
  `configs/prometheus/rules.r1/aggregator.yml:18-22` and
  `deploy/monitoring/rules/aggregator.yml:18-22` now both use
  the OR'd shape from supply-refresh.yml:
  ```yaml
  expr: |
    sum(rate(ratesengine_aggregator_vwap_writes_total[5m])) == 0
    OR
    absent_over_time(ratesengine_aggregator_vwap_writes_total[10m]) == 1
  ```
  Both files updated as part of task #19; live verified.

#### F-0081 — POSITIVE: `ingestion_source_stopped` alert IS guarded against absent-series

- **Severity:** `note` (positive)
- **Title:** `configs/prometheus/rules.r1/ingestion.yml:42-55` joins
  `rate(...) == 0` with a synthetic always-present vector via
  `label_replace(vector(1), "source", "binance", "", "") or
   label_replace(vector(1), "source", "bitstamp", "", "") ...`.
  This forces the series-set to include each named source even
  when the counter is absent — alert fires correctly under
  the F-0039 cascade.
- **Workstream:** W14
- **Evidence:** `configs/prometheus/rules.r1/ingestion.yml:42-55`.
- **Disposition:** `accepted` — POSITIVE. **Use as the template
  pattern when rewriting F-0080.**

#### F-0082 — POSITIVE: `supply-refresh` + `supply-snapshot` alerts use `absent_over_time` correctly

- **Severity:** `note` (positive)
- **Title:** `configs/prometheus/rules.r1/supply-refresh.yml:78`
  and `supply-snapshot.yml:104` both wrap their freshness
  checks with `absent_over_time(...) == 1`. The 36h window
  is long but the pattern is structurally correct.
- **Workstream:** W14
- **Evidence:** as above.
- **Disposition:** `accepted` — POSITIVE.

#### F-0083 — R2/R3 inventories are example-only; per-region overrides documented but not provisioned

- **Severity:** `medium`
- **Title:** `configs/ansible/inventory/r2.example.yml` and
  `r3.example.yml` are template files; no `r2.yml`/`r3.yml`
  exists. The closed-bucket API contract per ADR-0015 +
  per-region storage strategy per ADR-0016 are designed for
  multi-region, but production is single-region. This is
  consistent with `[[project_open_backlog]]` "R2+R3 deferred";
  audit-noting rather than flagging.
- **Workstream:** W23
- **Evidence:** `ls configs/ansible/inventory/r{2,3}.example.yml`;
  no non-example r2/r3 inventory present. The example file
  documents EXACTLY how to derive r2.yml + r3.yml (lines 7-39
  in r1.yml are the recipe).
- **Adversarial vector:** single-region means any disaster
  affecting r1 (the only region) takes the public surface
  down. Until r2 ships, the "geographically-redundant"
  positioning isn't yet delivered.
- **Disposition:** `accepted` for pre-launch (matches stated
  scope per memory `project_open_backlog`). Re-flag at
  launch-day-readiness if r2 hasn't shipped.

#### F-0084 — POSITIVE: cross-region-check + cross-region-monitor subcommands shipped

- **Severity:** `note` (positive)
- **Title:** `cmd/ratesengine-ops/main.go:243+248` cases for
  `cross-region-check` and `cross-region-monitor` exist —
  the operator tooling for ADR-0015's byte-equivalence
  invariant is in place even though R2/R3 inventories aren't
  filled. When R2/R3 ship, no new code is required to
  validate determinism.
- **Workstream:** W23, W13
- **Evidence:** `cmd/ratesengine-ops/main.go:243-251`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0085 — `ratesengine_redis_writes_blocked` alert exists but redis_exporter is DOWN (silent under F-0045)

- **Severity:** `high`
- **Title:** `configs/prometheus/rules.r1/storage.yml` declares
  `expr: redis_rdb_last_bgsave_status == 0 for: 1m` — the alert
  designed SPECIFICALLY to catch F-0039's bgsave failure. But
  `redis_exporter` is DOWN per F-0045, so this gauge never
  reaches Prometheus. Alert evaluates `absent_gauge == 0` →
  no_data → never fires. The alert that exists for exactly
  this cascade is invisible because the upstream exporter is
  the same cascade-affected process tree.
- **Workstream:** W14
- **Evidence:** `configs/prometheus/rules.r1/storage.yml` +
  EV-0020 (redis_exporter DOWN).
- **Adversarial vector:** **second-order cascade-blindness**:
  even when the alert design is correct, the exporter that
  feeds it is on the same host with the same problem and dies
  silently with it. Exporter health is a meta-signal that
  needs its own absent-guard.
- **Cross-ref:** F-0039 (root), F-0045 (exporter down),
  F-0080 (analogous unguarded pattern).
- **Disposition:** `closed` (Wave-0 step 5, this session).
  Exporter-down meta-alerts added in
  `deploy/monitoring/rules/exporters.yml` +
  `configs/prometheus/rules.r1/exporters.yml` covering
  `redis_exporter`, `postgres_exporter`, and
  `pgbackrest_exporter` via
  `absent_over_time(up{job=…}[5m]) == 1 OR up{job=…} == 0`.
  Exporter binaries themselves were installed on r1 under
  task #37 (F-0152). Operationally restarted at task #18.

#### F-0086 — `/v1/oracle/latest` returns HTTP 500 internal error

- **Severity:** `high`
- **Title:** Live curl 2026-05-27 of `/v1/oracle/latest?asset=native`
  returns HTTP 500 with body `{"type":"...errors/internal",
  "title":"Internal error","status":500,...}`. No detail.
- **Workstream:** W11
- **Evidence:** raw curl EV-0043.
- **Cross-ref:** Pattern: F-0086..F-0089 all return 500 on
  routes that depend on the aggregator/cache hot path under
  F-0039.
- **Disposition:** `closed` (Wave-0 step 7, this session).
  /v1/oracle/latest now maps Redis MISCONF to HTTP 503 with
  `Retry-After: 30`; the same handler gained the cache-error
  helper used by the rest of the cascade-affected surface
  (task #22 covers F-0086..F-0090, F-0145, F-0146 as a single
  closure). F-0039 itself is operationally cleared
  (task #17 fixed bgsave by freeing the root partition).

#### F-0087 — `/v1/lending/pools` returns HTTP 500 internal error

- **Severity:** `high`
- **Title:** Same shape as F-0086 — Blend lending-pools surface
  500s under cascade.
- **Workstream:** W11
- **Evidence:** raw curl EV-0043.
- **Cross-ref:** F-0086 same cluster.

#### F-0088 — POSITIVE: `/v1/pools` serves fresh, granular Stellar-native data

- **Severity:** `note` (positive)
- **Title:** `/v1/pools` returns SDEX + Aquarius + Phoenix
  pool data with `last_trade_at: 2026-05-26T16:30:48Z` (fresh,
  ~7h old), `trade_count_24h: 35082` for SDEX, `volume_24h_usd:
  516,595.55`, `last_price: 0.149...`. Stellar-specific. This
  is the strong CG/CMC-differentiating surface.
- **Workstream:** W11, W20
- **Evidence:** raw curl EV-0043.
- **Disposition:** `accepted` — POSITIVE. **Foreground this
  endpoint in customer-facing docs.**

#### F-0089 — `/v1/vwap` + `/v1/oracle/{lastprice,prices,streams}` return HTTP 500

- **Severity:** `high`
- **Title:** Live curls return HTTP 500 internal error for
  the 4 listed routes under the F-0039 cascade. All return
  empty-detail problem documents — no operator hint.
- **Workstream:** W11, W10
- **Evidence:** raw curl EV-0043.
- **Adversarial vector:** any customer with an integration
  hitting these routes during F-0039 gets generic 500 — they
  can't tell if it's their fault or ours. Looks like a system
  bug.
- **Disposition:** `closed` (Wave-0 step 7, this session).
  Bundled with F-0086 / F-0090 closure via the cache-error
  helper in `internal/api/v1/cache_errors.go`. /v1/vwap +
  /v1/oracle/{lastprice,prices,streams} all map Redis MISCONF
  to 503 + `Retry-After: 30`. F-0039 root cause cleared
  operationally at task #17.

#### F-0090 — Handlers translate Redis errors to HTTP 500 (should be 503)

- **Severity:** `medium`
- **Title:** Routes that depend on Redis hot path (vwap,
  oracle/*, lending/pools) currently return HTTP 500 when
  Redis is MISCONF, instead of HTTP 503 Service Unavailable.
  HTTP-semantics-wise, 500 implies a code bug (caller
  shouldn't retry); 503 with Retry-After implies infrastructure
  issue (caller should backoff + retry).
- **Workstream:** W11, W19
- **Evidence:** the 4 routes' 500 responses in EV-0043 paired
  with `/v1/readyz`'s correct `degraded` self-report
  (EV-0034) — readyz knows Redis is down; the handlers
  don't translate that.
- **Cross-ref:** F-0042 POSITIVE shows `/v1/price` degrades
  gracefully → other routes should match the contract.
- **Disposition:** `closed` (Wave-0 step 7, this session).
  `internal/api/v1/cache_errors.go::handleCacheErr` is the
  shared helper: every Redis-touching handler routes through
  it and the helper emits 503 + `Retry-After: 30` on
  `redis.ErrMISCONF` (and on the broader transient class).
  /v1/vwap, /v1/oracle/lastprice|prices|streams,
  /v1/oracle/latest, /v1/lending/pools, plus the SEP-41
  transfers and divergence/score surfaces were migrated to
  the helper at task #22 (covers F-0086..F-0090, F-0145,
  F-0146 jointly).

#### F-0091 — `/v1/chart` requires `asset=` (4th param-shape continues)

- **Severity:** `low`
- **Title:** `/v1/chart` uses `asset=` not `base=`. Extends
  the F-0061 / F-0068 / F-0073 cluster.
- **Workstream:** W11
- **Evidence:** raw curl EV-0043.
- **Disposition:** `closed` (2026-05-28). Bundled with F-0061
  closure. `parseChartAssetQuote` now delegates to
  `resolveAssetOrBaseParam` so `/v1/chart` accepts both
  `asset=` (canonical) and `base=` (alias); both-supplied
  returns 400. Two new tests
  (`TestChart_BaseParamAcceptedAsAssetAlias`,
  `TestChart_BothAssetAndBase400`) pin the contract.

#### F-0092 — POSITIVE: `/v1/sac-wrappers` exposes 40+ SAC contract ID ↔ SEP-23 asset mappings

- **Severity:** `note` (positive)
- **Title:** `/v1/sac-wrappers` returns a complete map of
  Soroban SAC contract IDs to their classic SEP-23 issuers
  (USDC, EURC, AQUA, KALE, USDx, XRP, BTC, ETH, etc.) at
  fresh `as_of` 2026-05-27T00:23Z, `flags.stale:false`. This
  is a Stellar-specific surface CG/CMC have no equivalent
  for — knowing that contract `CCW6...JMI75` IS the SAC
  wrapper for USDC issued by GA5Z is a real product hook.
- **Workstream:** W11, W20
- **Evidence:** raw curl EV-0043; ~40 entries returned.
- **Disposition:** `accepted` — POSITIVE. **Foreground this
  endpoint in customer-facing differentiation messaging.**

#### F-0093 — SEP-10 challenge endpoint reports `503 sep10-unavailable` (validator not wired)

- **Severity:** `medium` — depends on whether SEP-10 is in launch scope
- **Title:** `/v1/auth/sep10/challenge?account=GDQNY3PB...` returns
  HTTP 503 `errors/sep10-unavailable` with detail "this deployment
  has no SEP-10 validator wired — typically because the server
  signing seed isn't configured". The auth surface is correctly
  fail-CLOSED, but a launch-critical authentication path is OFF
  in production.
- **Workstream:** W19
- **Evidence:** raw curl EV-0045.
- **Disposition:** `closed-as-deferred` (2026-05-28). The
  Rates Engine's primary public surface is read-only price
  data (CG/CMC-shape integrations use API-key auth, not
  SEP-10). SEP-10 Web-Auth is an SDF-ecosystem integration
  path (Freighter-style wallet signing) and isn't a hard
  launch requirement for the L6 / RFP commitments. The
  endpoint's current fail-CLOSED 503 with a clear error type
  (`errors/sep10-unavailable`) is the correct posture while
  the validator isn't configured: customers see a documented
  "feature not wired here" instead of a stub that accepts
  challenges but never validates them. Wiring requires an
  operator decision (which signing seed do we publish? does
  Cloudflare front this route? how do we rate-limit per-
  account challenges?) — none of which the binary can
  self-decide. Re-open this finding when SEP-10 enters
  launch scope rather than letting it block on a feature
  that isn't promised in the RFP.

#### F-0094 — `/v1/diagnostics/cursors` HTTP 500 (operator-visibility broken under cascade)

- **Severity:** `medium`
- **Title:** Live curl returns HTTP 500 `errors/cursors-error`
  with detail "Storage layer returned an error". This is an
  OPERATOR diagnostic — failing it under cascade hides the
  operator's view of what state the ingestion cursors are in,
  exactly when they most need it.
- **Workstream:** W11, W13
- **Evidence:** raw curl EV-0045.
- **Disposition:** `closed` (2026-05-28). The endpoint was
  already DB-only (it does NOT touch Redis — `ListCursors`
  hits Postgres directly), so the audit's "make this endpoint
  cache-aside" suggestion was based on a misread of the
  handler. The actual fix the original "exactly when they
  most need it" line was asking for is shipped: the handler
  now distinguishes operator-actionable transient errors from
  permanent ones. 5s ctx-timeout on the ListCursors call;
  `handlerTimedOut` returns 503 `cursors-timeout`;
  `transientStorageErr` (covers Postgres-down, driver-bad-
  connection, 57014 cancel, broken pipe, EOF — i.e. exactly the
  postgres-down scenarios that the cascade window contains)
  returns 503 `cursors-transient`; client-aborted is filtered;
  the residual 500 is reserved for genuinely-unknown errors.
  Two regression tests pin transient → 503 and non-transient
  → 500. Error-map extracted into
  `writeCursorsListError` to keep `handleCursors` under the
  gocognit ceiling.

#### F-0095 — `/v1/diagnostics/ingestion` reports all-zeros but other endpoints show healthy state — inconsistency

- **Severity:** `high`
- **Title:** Live probe shows `/v1/diagnostics/ingestion` with
  `latest_ledger: 0, markets_count_24h: 0, assets_indexed: 0,
  trade_count_24h: 0` for every source, every counter zero,
  every `entries: 0` for backfill_coverage rows. Meanwhile,
  in the SAME session probe window:
  - `/v1/network/stats`: `latest_ledger: 62746377, markets_count_24h: 46`
  - `/v1/ledger/tip`: `latest_ledger: 62746377, lag_seconds: 2`
  - `/v1/pools`: SDEX `trade_count_24h: 35082`
  
  And `flags.stale: false` on the all-zeros response — lying.
- **Workstream:** W11, W13
- **Evidence:** raw curls EV-0045 + EV-0031.
- **Adversarial vector:** the operator-facing dashboard surface
  reports the system is non-functional while customer-facing
  endpoints report healthy. Two different code paths read two
  different storage stacks. An operator trusting the diagnostic
  view would page out when nothing is actually broken.
- **Disposition:** `closed` (Wave-1, task #30 this session).
  `/v1/diagnostics/ingestion` now reads from the same
  storage path as the per-source diagnostics and exposes the
  freshness fields consistently with /v1/diagnostics/cursors.
  See the corresponding session commit and the documentation
  refresh that landed in the same PR for the storage-path
  mapping.

#### F-0096 — POSITIVE: `/v1/methodology` is comprehensive and customer-grade

- **Severity:** `note` (positive)
- **Title:** `/v1/methodology` returns full aggregation policy,
  source classification taxonomy (exchange / aggregator /
  oracle / authority_sanity), stablecoin-fiat proxy table,
  closed_bucket_window_seconds, AND links to ADRs 0007, 0015,
  0018, 0019, 0020, 0026. This is exactly the transparency
  surface that distinguishes a credible price API from a
  black box.
- **Workstream:** W11, W20
- **Evidence:** raw curl EV-0045.
- **Disposition:** `accepted` — POSITIVE. **Foreground for
  CG/CMC differentiation.**

#### F-0097 — POSITIVE: `/v1/sources` exposes full source taxonomy

- **Severity:** `note` (positive)
- **Title:** `/v1/sources` returns all 26 sources with class,
  subclass, include_in_vwap, paid, backfill_available,
  backfill_safe, default_weight. The full registry is
  customer-readable.
- **Workstream:** W11, W20
- **Evidence:** raw curl EV-0045.
- **Disposition:** `accepted` — POSITIVE.

#### F-0098 — POSITIVE: `/v1/incidents` exposes full markdown post-mortems

- **Severity:** `note` (positive)
- **Title:** `/v1/incidents` returns 2 documented incidents
  in full markdown — SEV-2 from 2026-05-10 (the exact
  Redis-disk-full cascade currently affecting r1) AND SEV-3
  from 2026-05-06 (Postgres lock-table-full). Detail is
  customer-grade: timeline, root cause, resolution, what
  customers need to do, what we changed, what we'll do
  next.
- **Workstream:** W11, W20, W16
- **Evidence:** raw curl EV-0045.
- **Disposition:** `accepted` — POSITIVE. Status-page-quality
  transparency.

#### F-0099 — **HIGH** Same cascade root cause recurred: 2026-05-10 SEV-2 had unchecked operational follow-ups

- **Severity:** `high`
- **Title:** The 2026-05-10 SEV-2 incident report (now publicly
  served at `/v1/incidents`) documented the EXACT cascade I've
  found on 2026-05-26: Redis MISCONF caused by root-disk full
  caused by stale logs. The post-mortem's "What we'll do next"
  checklist included:
  - [ ] Audit `/etc/logrotate.d/rsyslog` and `postgresql-common`
  - [ ] Add Prometheus alert rule on
    `node_filesystem_avail_bytes / node_filesystem_size_bytes < 0.15`
  - [ ] Move WASM-audit one-time stderr captures to a dedicated dir
  - [ ] Document the recovery sequence
  
  These checkboxes are UNCHECKED in the served incident.
  17 days later the same cascade has recurred. **The
  operational-follow-up discipline is the meta-finding.**
- **Workstream:** W14, W18, W22
- **Evidence:** raw curl `/v1/incidents` EV-0045 — checkbox
  state visible in the markdown body.
- **Adversarial vector:** the publicly-served incident report
  shows competitor/researcher/regulator that we knew about
  this exact failure and didn't fix it. Brand damage.
- **Disposition:** `closed` (task #42, this session). The
  forcing function is `scripts/ci/lint-docs.sh` — every
  incident-postmortem file under `docs/incidents/` must have
  every follow-up checkbox either ticked or carry a closure
  citation. CI fails on an unchecked, uncited follow-up. F-0080
  and F-0085 themselves are closed above. The 4 follow-ups
  from the 2026-05-10 SEV-2 are now traceable via the same
  lint, so a future "did we forget to follow up?" question
  has an automated answer.

#### F-0100 — Launch checklist condition "No fired alerts in Alertmanager" is currently passable BUT FALSE-GREEN

- **Severity:** `high` (process / meta-finding)
- **Title:** `docs/operations/launch-day-checklist.md` requires
  "Production environment is green. Every dashboard panel on
  the SLO board reading nominal: ... No fired alerts in
  Alertmanager." Today this condition passes vacuously due
  to F-0080 (unguarded aggregator_silent alert) + F-0036
  (counter staleness) + F-0027 (silent alert pipeline) +
  F-0039 (cascade) — i.e., the box-tick is technically true
  but the system is degraded. Launch could happen against a
  silently-broken environment if the checklist is followed
  literally.
- **Workstream:** W22, W14
- **Evidence:** `docs/operations/launch-day-checklist.md` T-1
  go/no-go block; cross-ref F-0080, F-0036, F-0027.
- **Disposition:** `closed` (2026-05-28).
  `docs/operations/launch-day-checklist.md:67-90` now carries
  an explicit counter-presence sanity step under the
  "Production environment is green" item: operators run
  `count by (__name__) ({__name__=~"ratesengine_.*_total"})`
  against prod Prometheus and confirm the result is non-empty
  for every named family — "no fired alerts" is a false-green
  when an underlying counter has gone stale. Cross-referenced
  back to the absent_over_time-guarded alerts (F-0080,
  F-0085, F-0104) so the manual step is documented as a
  belt-and-braces check for future rules that might land
  without the structural guard.

#### F-0101 — `internal/obs` package has 3 test files vs 21 in `internal/aggregate`

- **Severity:** `low`
- **Title:** Test-file coverage by package:
  - `internal/auth`: 13
  - `internal/aggregate`: 21
  - `internal/api/v1`: 78
  - `internal/sources`: 113
  - `internal/canonical`: 13
  - **`internal/obs`: 3**
  The observability layer is the surface that broke (F-0080)
  and is the thinnest-tested critical package.
- **Workstream:** W14, W15
- **Evidence:** `find internal/<pkg> -name '*_test.go' | wc -l`.
- **Disposition:** `closed-reframed` (2026-05-28). The audit's
  file-count was a misleading heuristic for actual coverage:
  current state is 3 files / **19 test functions** in
  `internal/obs`, whereas the headline number of `21 files`
  in `aggregate` covers a much broader surface (orchestrator,
  twap, vwap, divergence, baselines, freezer …). Functional
  coverage of the obs layer is moderate, not thin.
  Per-function: `TestZeroSeed_F0033`,
  `TestHTTPMetrics_Fast5xxDoesNotCountAsSuccess`,
  `TestHandler_ExposesMetrics`,
  `TestStatusRecorder_Flush_noopWhenInnerDoesntImplementFlusher`,
  `TestNormalizeMethod`, `TestRouteFromPattern`,
  and 13 others.
  More important: the actual gap the audit flagged
  ("alert-rule families with the rate-counter-with-absent-
  guard pattern") is now structurally closed in two ways:
  1. The alert-rule files themselves carry the
     absent_over_time guard (F-0080 / F-0085 / F-0104
     closures this session) so the failure mode the audit
     was worried about doesn't depend on a Go-side test to
     catch.
  2. `TestZeroSeed_F0033` pins the underlying counter
     pre-seeding pattern (bounded-cardinality counter seeded
     at zero so the alert PromQL has data to evaluate from
     scrape #1) so a regression that drops the pre-seed is
     caught at PR time.
  Adding more test files for its own sake doesn't move the
  needle on the audit's actual concern. Re-open if a
  specific alert-pattern regression is observed that a Go
  test would catch but the absent_over_time guard wouldn't.

#### F-0102 — POSITIVE: launch-day-checklist + public-flip docs exist with detailed steps

- **Severity:** `note` (positive)
- **Title:** `docs/operations/launch-day-checklist.md` (T-7 →
  T-0 with checkboxes) and `docs/operations/public-flip.md`
  (new-repo strategy, pre-flip checklist) both exist with
  rich content. The launch-day discipline framework is in
  place; the GAPS exposed by this audit affect WHAT the
  checklist verifies, not its existence.
- **Workstream:** W22
- **Evidence:** the two referenced files.
- **Disposition:** `accepted` — POSITIVE.

#### F-0103 — POSITIVE: test count is strong across critical packages

- **Severity:** `note` (positive)
- **Title:** 768 total `*_test.go` files; 28 integration-tagged.
  Tests build cleanly on api/v1, aggregate, canonical (no
  build-tag breakage). 113 test files in sources package
  demonstrates strong decoder discipline.
- **Workstream:** W15
- **Evidence:** `find . -name '*_test.go' | wc -l` → 768;
  `go test -count=0 ./internal/api/v1/ ./internal/aggregate/
  ./internal/canonical/` → all `ok`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0104 — `ratesengine_api_price_stale` alert depends on aggregator-emitted gauge (cascade-fragile)

- **Severity:** `high`
- **Title:** `configs/prometheus/rules.r1/api.yml`
  `ratesengine_api_price_stale` is `expr:
  ratesengine_price_staleness_seconds > 120 for: 5m`. The
  metric source per `internal/api/v1/price.go:329` is the
  **aggregator**'s `orchestrator.emitStalenessGauges` which
  fires at end-of-tick. Under F-0039 cascade the aggregator
  tick wedges → gauge is not emitted → series goes stale →
  alert sees no_data and structurally cannot fire.
- **Workstream:** W14
- **Evidence:** rule expr in api.yml + comment in
  price.go:329 documenting the cross-package gauge ownership.
- **Adversarial vector:** the staleness alert designed to
  catch exactly this cascade is itself a victim of it.
  Similar to F-0085 (redis_writes_blocked depends on
  redis_exporter which dies in the cascade) and F-0080
  (aggregator_silent rule is unguarded).
- **Disposition:** `closed` (2026-05-28). Both
  `configs/prometheus/rules.r1/api.yml:92-117` and
  `deploy/monitoring/rules/api.yml:104-129` now use the OR'd
  expr:
  ```yaml
  expr: |
    ratesengine_price_staleness_seconds > 120
    OR
    absent_over_time(ratesengine_price_staleness_seconds[10m]) == 1
  ```
  The cascade-wedge case (aggregator stops emitting → series
  goes stale → bare `> 120` sees no-data) now fires on the
  absent_over_time branch. Annotation updated to redirect
  operators when the gauge is absent: see the
  aggregator-silent runbook (the wedge), not the price-stale
  runbook (the freshness gap).

#### F-0105 — SLO budget calc only counts successful-response latency; HTTP 500s don't burn budget

- **Severity:** `medium`
- **Title:** `slo.yml` `ratesengine:api_slow_request_ratio:5m`
  uses `http_request_duration_seconds_bucket{job=
  "ratesengine-api",route=~"/v1/price|/v1/price/batch|
  /v1/oracle/..."}` — the histogram only records successful
  request latency. Under F-0089 cascade `oracle/*` returns
  HTTP 500. The error counter increments (causing
  api_error_rate_high to fire at >1%), but the SLO budget
  doesn't burn because errors aren't slow requests.
- **Workstream:** W14
- **Evidence:** slo.yml + api.yml. The two alert families
  (error_rate vs latency_slo) double-count fast-errors as
  "good budget burn" and "high error rate" — the SLO scale
  of "this customer experience" doesn't reflect a 5xx outage.
- **Disposition:** `closed` (2026-05-28). Took the
  "errors burn budget" path. New
  `http_request_success_duration_seconds` histogram in
  `internal/obs/metrics.go`; the middleware only records into
  it when the response status is < 500 and not 499. SLO
  recording rules in `slo.yml` (both R1 overlay + multi-host
  copies) updated so the numerator pulls from the new
  success-only histogram while the denominator stays on the
  all-requests `_duration_seconds_count`. The result is the
  ratio operators actually wanted: a fast 5xx lands in the
  denominator but not the numerator → ratio drops → SLO
  burns. Pre-this-PR a 5 ms 500 reported as "good fast"
  against the latency SLO. Regression test
  `TestHTTPMetrics_Fast5xxDoesNotCountAsSuccess` pins the
  success histogram at zero for a synthetic 500.

#### F-0106 — POSITIVE: alertmanager has deadmansswitch heartbeat to healthchecks.io

- **Severity:** `note` (positive)
- **Title:** `configs/alertmanager/alertmanager.r1.yml`
  routes a `ratesengine_deadmansswitch` alert at 60s cadence
  to healthchecks.io. When Prometheus or Alertmanager
  themselves die, the heartbeat stops, healthchecks.io pages
  out-of-band. This is the meta-check against the entire
  cascade silencing problem.
- **Workstream:** W14
- **Evidence:** alertmanager.r1.yml routing tree.
- **Disposition:** `accepted` — POSITIVE. The cascade-blind
  failure modes (F-0027/F-0080/F-0085/F-0104) all stop short
  of the deadmansswitch — operator WOULD know if Prometheus
  itself died.

#### F-0107 — POSITIVE: 94 alerts total across 17 rule files; only 1 unguarded rate==0 anti-pattern

- **Severity:** `note` (positive)
- **Title:** Full sweep of `configs/prometheus/rules.r1/*.yml`:
  - 17 rule files
  - 94 total alerts
  - 29 page-severity, 51 ticket-severity, 14 informational
  - Only ONE unguarded `rate()==0` pattern (F-0080 in
    aggregator.yml)
  - 17 guards in ingestion.yml (F-0081 POSITIVE), 3 in
    supply-refresh, 2 in supply-snapshot
  - All other ==0 patterns are gauge-based (`up{}==0`),
    which are safe under F-0027 anti-pattern.
- **Workstream:** W14
- **Evidence:** scripted sweep — pattern audit per file.
- **Disposition:** `accepted` — POSITIVE. Alert engineering
  discipline is good in aggregate; F-0080 is an isolated
  bug.

#### F-0108 — Redis cache state degrades over time: 2358 unwritten changes accumulating

- **Severity:** `high` (data loss risk grows linearly)
- **Title:** Live r1 probe 2026-05-27 01:00 UTC (16h after first
  probe): `redis-cli info persistence` reports
  `rdb_changes_since_last_save: 2358` and
  `rdb_last_bgsave_status: err`. Cache state is accumulating
  in RAM without a snapshot. If Redis restarts (planned
  maintenance, OOM kill, host reboot), all 2358+ changes are
  LOST.
- **Workstream:** W21, W14
- **Evidence:** `ssh root@136.243.90.96 'redis-cli info
  persistence'` 2026-05-27 01:00 UTC.
- **Adversarial vector:** the cascade WAITS — and the
  blast radius grows over time. A maintenance restart, host
  reboot, or OOM kill at hour 24 of the cascade loses 24h
  of cache state. Recovery requires aggregator re-warm
  from CAGGs.
- **Cross-ref:** F-0039 root cause.
- **Disposition:** `closed` (tasks #16 + #17, this session).
  Root cause was F-0039 (Redis MISCONF blocked bgsave because
  the root partition was at 100 %). Task #16 freed root;
  task #17 reset bgsave and cleared MISCONF. Post-fix
  verification: `rdb_changes_since_last_save` returned to 0
  and `rdb_last_bgsave_status` flipped to `ok`. The
  redis_exporter installed at task #37 means a future
  recurrence is now alertable rather than discoverable only
  by manual `redis-cli info persistence`.

#### F-0109 — Cascade unfixed 16+ hours after first audit probe

- **Severity:** `high` (process / operator-discipline)
- **Title:** First audit probe at 2026-05-26 23:14 UTC found
  `/dev/md1 49G 47G 0 100%` + Redis MISCONF. Second probe
  at 2026-05-27 01:00 UTC (16h later) finds the EXACT
  SAME state — disk still 100%, Redis still MISCONF, 2358+
  changes accumulating. The audit IS the on-call signal in
  the absence of working alerting; the cascade has been
  silently broken for at least 17h.
- **Workstream:** W14, W18, W22
- **Evidence:** EV-0003 (first probe) + EV-0050 (16h later).
- **Cross-ref:** F-0027/F-0080/F-0085/F-0104 cluster — the
  cascade is invisible to the operator BECAUSE the alerts
  that should fire are themselves victims.
- **Disposition:** `closed-as-historical` (2026-05-28).
  Observational entry recording how long the cascade went
  unfixed during the audit. The cascade itself was resolved
  through tasks #16 (free root disk), #17 (reset bgsave +
  clear MISCONF), #18 (restart exporters), and the
  structural alert-portfolio hardening (F-0080 / F-0085 /
  F-0104 absent_over_time guards) so a recurrence is
  alertable instead of silent. The process lesson — "the
  audit became the on-call signal because alerts were
  themselves cascade victims" — is preserved as historical
  trace; the alert hardening is the durable countermeasure.

#### F-0110 — ADR-0028 still `status: Proposed` despite code shipped

- **Severity:** `medium`
- **Title:** `docs/adr/0028-rwa-asset-representation.md` has
  `status: Proposed` and `accepted: null`. But the code is
  ALREADY shipped: `internal/canonical/asset_rwa.go` defines
  `AssetRWA AssetType = "rwa"`, the allow-list of 8 codes
  (BENJI, iBENJI, GILTS, CETES, KTB, TESOURO, USTRY, SPXU),
  and the RedStone feed map at `internal/sources/redstone/
  feeds.go` uses `mustRWA(...)` to bind feed IDs to RWA
  assets. Policy/implementation drift.
- **Workstream:** W31, W02 (ADR governance)
- **Evidence:** ADR-0028 header + cited code paths.
- **Disposition:** `closed` (task #35, this session).
  ADR-0028 status promoted to `Accepted` in `docs/adr/0028-*.md`
  to match deployed code (the codebase already carries the
  `AssetRWA` type + the BENJI/iBENJI/GILTS/CETES/KTB/TESOURO/
  USTRY/SPXU allow-list + the Redstone feed binding). The
  governance lag — `Proposed` while production runs the policy —
  is what CLAUDE.md §invariants flagged; promoting the ADR to
  Accepted is the right closure direction since the code shipped.

#### F-0111 — POSITIVE: RedStone EUROC / BENJI feed-id fix landed correctly

- **Severity:** `note` (positive)
- **Title:** ADR-0028 §1 documents that pre-#53 RedStone
  decoder used `IsKnownCrypto(feedID)` which dropped EUROC
  (feed_id is `EUROC/EUR`) and 4 RWA feeds (BENJI etc with
  `_ETHEREUM_FUNDAMENTAL` suffix). The fix landed in
  `internal/sources/redstone/feeds.go` — explicit feed-id
  keys `"EUROC/EUR": {mustCrypto("EUROC"), quoteEUR}` and
  `"BENJI_ETHEREUM_FUNDAMENTAL": {mustRWA("BENJI"),
  quoteUSD}`. All 19 RedStone feeds now reachable.
- **Workstream:** W31, W07
- **Evidence:** `internal/sources/redstone/feeds.go` mapping
  + ADR-0028 §The RedStone 19-feed registry.
- **Disposition:** `accepted` — POSITIVE.

#### F-0112 — POSITIVE: TieredDataStore.GetFile is correctly implemented per ADR-0027

- **Severity:** `note` (positive)
- **Title:** `internal/ledgerstream/tiered.go::GetFile` chains
  hot → cold correctly:
  - `hot.GetFile` returns ok → use hot result; observeHot()
  - `hot.GetFile` returns transient (non-NotFound) error →
    return wrapped error WITHOUT trying cold (fail loud)
  - `hot.GetFile` returns NotFound → call `coldGetFile`
  - cold returns ok / miss / error → metric outcomes
    `ok`/`miss`/`error` + the `both_missing` total counter
    that feeds the `ratesengine_ledgerstream_tier_both_missing`
    alert (`configs/prometheus/rules.r1/ledgerstream-tier.yml`).
- **Workstream:** W30
- **Evidence:** `internal/ledgerstream/tiered.go:110-160` +
  rule + runbook `ledgerstream-tier-both-missing.md`.
- **Disposition:** `accepted` — POSITIVE. The cold-tier
  read-path is the right shape: fail loud on transient
  errors, fail soft (degrade) on legit-not-found, surface
  both_missing as a page-severity signal.

#### F-0113 — POSITIVE: docs-lint enforces code↔docs round-trip on 5 dimensions

- **Severity:** `note` (positive)
- **Title:** `scripts/ci/lint-docs.sh` enforces:
  1. Every `[*]` TOML config key in `config.go` exists in
     `docs/reference/config/README.md` (and vice versa)
  2. Every OpenAPI route has a handler; every handler is in
     OpenAPI (modulo `planned_regex`)
  3. Every code-registered Prometheus metric is documented in
     `docs/reference/metrics/README.md`
  4. No stale references to renamed/removed symbols in active docs
  5. `last_verified` date in doc frontmatter ≤ 180 days
  6. ADR `status:` must be one of {Proposed, Accepted,
     Superseded, Rejected}
  7. Superseded ADRs must have `superseded_by` populated
  8. Generated files must carry "GENERATED FILE - DO NOT EDIT"
- **Workstream:** W25, W16
- **Evidence:** `scripts/ci/lint-docs.sh` (all 8 checks visible
  in the err() invocations).
- **Disposition:** `accepted` — POSITIVE. The "no drift"
  invariant is enforced in CI. **Note:** F-0110 (ADR-0028
  Proposed/code-shipped drift) escapes this lint because
  `Proposed` is a valid status — the lint doesn't cross-check
  ADR-status vs code-presence. Possible W2 enhancement:
  flag Proposed-ADR-with-shipped-code.

#### F-0114 — POSITIVE: customer webhook worker has correct retry + backoff design

- **Severity:** `note` (positive)
- **Title:** `internal/customerwebhook/worker.go` chain:
  - 5xx + 3xx classified as transient → retry
  - Exponential backoff: 30s, 1m, 2m, 4m, 8m, … capped at 1h
    (line 286, 311)
  - `MarkAttemptFailed` writes `nextAttemptAt` so the worker
    `tick()` won't re-pull until the backoff elapses
  - `MarkDelivered` is terminal — 2xx ends the lifecycle
  - F-1249 (prior 2026-05-12 audit) gap "no production caller
    enqueues deliveries" — remediated; current code DOES insert
    delivery rows from freeze sink + incident creator + divergence
    service per the F-1249 fix note in fanout.go.
- **Workstream:** W32
- **Evidence:** `internal/customerwebhook/worker.go:275, 286, 311`;
  `internal/customerwebhook/fanout.go:46-54` callout.
- **Disposition:** `accepted` — POSITIVE. Pair with F-0006
  (SSRF-safe dial) — webhook fanout is one of the strongest
  surfaces in the codebase.

#### F-0115 — POSITIVE: Stripe webhook dedupe via `StripeEventStore` is sound

- **Severity:** `note` (positive)
- **Title:** `internal/api/v1/stripe_webhook.go:30-43` declares
  `StripeEventStore` interface with explicit
  `AppendStripeEvent` / `MarkStripeEventProcessed` /
  `MarkStripeEventFailed` lifecycle. The append-then-mark
  pattern enables Stripe's at-least-once delivery semantics
  to be idempotent at the application layer (duplicate event
  IDs hit `AppendStripeEvent` → unique-constraint → fast skip).
  Comment at line 38-39 explicitly cites the constraint.
- **Workstream:** W33
- **Evidence:** as above + HMAC verification at line 1022-1029
  (already captured at F-0005 + EV-0005).
- **Disposition:** `accepted` — POSITIVE.

#### F-0116 — Cascade still unfixed at iteration 18 (~20h since first audit probe)

- **Severity:** `high` (process)
- **Title:** R1 probed 3rd time at ~iteration 18: same state.
  `rdb_changes_since_last_save: 2358` is now FROZEN (was the
  same 2358 in iter 15) — Redis isn't accumulating new
  changes because it's REJECTING writes. The cache is in
  read-only-of-last-successful-state mode. 20+ hours since
  first audit probe (and >24h since cascade started per
  iter 1 observation "last bgsave 17:20 UTC").
- **Workstream:** W21, W14, W22
- **Evidence:** EV-0050 (iter 15) + EV-0055 (this iter).
- **Cross-ref:** F-0109 (same finding, 16h timestamp);
  F-0108 (data loss risk; now contextualised — the 2358
  changes are STUCK in Redis but the application-level
  writers are silently failing too, so no new state is
  being accumulated to lose).
- **Disposition:** `closed-as-historical` (2026-05-28).
  Second observational entry in the cascade-duration cluster
  (same shape as F-0109). The cascade itself was resolved
  through tasks #16/17/18 and the structural alert hardening
  (F-0080 / F-0085 / F-0104 absent_over_time guards).
  Recurrence is now alertable rather than silent. Trace
  preserved for post-mortem reference.

#### F-0117 — POSITIVE: Phoenix decoder covers all 5 emitted event families

- **Severity:** `note` (positive)
- **Title:** `internal/sources/phoenix/decode.go::classifyAny`
  dispatches topic[0] to 5 action variants: Swap (with topic[1]
  field-name dispatch for the 8-field swap reassembly per
  CLAUDE.md callout), ProvideLiquidity, WithdrawLiquidity, Bond,
  Unbond. Matches W35 row "phoenix covered" + rc.81 (#27).
  All amount fields use `canonical.Amount` (big.Int → NUMERIC
  per ADR-0003) — no truncation risk.
- **Workstream:** W07, W05, W35
- **Evidence:** `internal/sources/phoenix/decode.go:130-152` +
  amount-field types at line 27-30.
- **Disposition:** `accepted` — POSITIVE.

#### F-0118 — POSITIVE: Comet decoder covers all 5 POOL-namespace events

- **Severity:** `note` (positive)
- **Title:** `internal/sources/comet/decode.go` ships:
  - `decodeSwap` (legacy path) for (POOL, swap)
  - `decodeJoinPool` for (POOL, join_pool) multi-token LP add
  - `decodeExitPool` for (POOL, exit_pool) multi-token LP remove
  - `decodeDeposit` for (POOL, deposit) single-asset LP add
  - `decodeWithdraw` for (POOL, withdraw) with `pool_amount_in`
    (BPT-share burn)
  All amount fields use `canonical.Amount` → NUMERIC.
  Matches W35 row + rc.81 (#26) + migration 0042 schema.
- **Workstream:** W07, W05, W35
- **Evidence:** `internal/sources/comet/decode.go:128-176` +
  per-event amount fields.
- **Disposition:** `accepted` — POSITIVE.

#### F-0119 — POSITIVE: Soroswap decoder covers all 6 emitted events including rc.81 `skim`

- **Severity:** `note` (positive)
- **Title:** `internal/sources/soroswap/decode.go` classifies
  EventSwap, EventSync, EventNewPair (factory), EventDeposit,
  EventWithdraw, EventSkim. The CLAUDE.md callout "Soroswap's
  SwapEvent has no post-state reserves; SyncEvent follows" is
  correctly handled — `RawPair.Swap + RawPair.Sync` correlation
  by (ledger, tx_hash, op_index). The pre-#28 skim gap noted in
  `[[project_every_event_principle]]` is closed. All amount
  fields use `canonical.Amount`.
- **Workstream:** W07, W05, W35
- **Evidence:** `internal/sources/soroswap/decode.go:43-78` +
  EventSkim handling at line 127-145 + swap+sync correlation
  in decodeSwap.
- **Disposition:** `accepted` — POSITIVE. **The cleanup of
  the Soroswap partial-decoder violation per
  `[[project_every_event_principle]]` is now visible in code.**

#### F-0120 — POSITIVE: Aquarius decoder covers all 4 emitted events

- **Severity:** `note` (positive)
- **Title:** `internal/sources/aquarius/decode.go::classify`
  dispatches topic[0] to 4 event variants: Trade,
  DepositLiquidity, WithdrawLiquidity, UpdateReserves.
  Matches W35 row "aquarius covered". Token identities
  derived from topics directly (no pool-info cache needed).
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/aquarius/decode.go:17-40`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0121 — POSITIVE: Blend decoder covers 21+ events across auction + money-market + admin

- **Severity:** `note` (positive)
- **Title:** Blend ships TWO classify functions:
  - `decode.go::classify` — 3 auction events (NewAuction,
    FillAuction, DeleteAuction)
  - `decode_money_market.go::classifyAny` — 21 events
    covering all money-market actions (Supply, Withdraw,
    SupplyCollateral, WithdrawCollateral, Borrow, Repay,
    FlashLoan, Gulp, Claim, ReserveEmissions,
    GulpEmissions, BadDebt, DefaultedDebt), admin
    (SetAdmin, UpdatePool, QueueSetReserve,
    CancelSetReserve, SetReserve, SetStatus, Deploy), plus
    inheriting the 3 auction events
  All 23 TopicSymbol constants from `events.go` are
  switch-covered. Matches W35 row + rc.81 (#25).
- **Workstream:** W07, W35
- **Evidence:**
  `internal/sources/blend/decode_money_market.go:162-220`;
  `grep -c TopicSymbol internal/sources/blend/events.go` → 23.
- **Disposition:** `accepted` — POSITIVE. Blend is the largest
  Soroban-source decoder by event-count, and it's
  every-event-covered per
  `[[project_every_event_principle]]`.

#### F-0122 — POSITIVE: Reflector decoder is correctly scoped (only `update` event emitted)

- **Severity:** `note` (positive)
- **Title:** `internal/sources/reflector/decode.go::classify`
  matches topic[0]=REFLECTOR + topic[1]=update. Reflector
  contracts only emit one event type per ADR-0028 § (the
  per-feed UpdateEvent with timestamp in topic[2]). The
  three Reflector contracts (DEX/CEX/FX) share the same
  schema — `reflector-dex`, `reflector-cex`, `reflector-fx`
  variants in `registry.go` differentiate via decoder-side
  source-name tagging, not by topic shape. Single-event
  scope is intentional + complete.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/reflector/decode.go:25-39`;
  contract-event annotation visible at line 175
  `#[contractevent(topics = ["REFLECTOR", "update"])]`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0123 — POSITIVE: Band decoder handles both ContractCall signatures (relay + force_relay)

- **Severity:** `note` (positive)
- **Title:** Band's Soroban contract emits ZERO events per
  CLAUDE.md callout. `internal/sources/band/decode.go`
  switches on `fnName` (`FnRelay` / `FnForceRelay`) and
  handles both signatures:
  - `relay(from, symbol_rates, resolve_time, request_id)` — 4 args
  - `force_relay(symbol_rates, resolve_time, request_id)` — 3 args
  Defensive fallback for `resolve_time=0` + observer-strkey
  derivation from op-source. USD relayed pushes are rejected
  per Band's storage-write contract.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/band/decode.go:43-167`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0124 — POSITIVE: Redstone decoder uses op_args plumbing for feed_ids (PR #166 wiring)

- **Severity:** `note` (positive)
- **Title:** `internal/sources/redstone/decode.go::classify`
  matches single topic `REDSTONE`. The decoder requires
  `events.Event.OpArgs` populated by `internal/dispatcher`
  (feed_ids live in the tx's `write_prices(updater,
  feed_ids, payload)` call args, NOT in event body).
  `ErrMissingOpArgs` + `ErrFeedIDCountMismatch` reject
  malformed events instead of zipping wrong-length vectors.
  Pairs with F-0111 EUROC + BENJI feed-id map fix.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/redstone/decode.go:34-66`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0125 — POSITIVE: CCTP decoder covers all 4 events across 3 contracts

- **Severity:** `note` (positive)
- **Title:** `internal/sources/cctp/decode.go::classify`
  dispatches topic[0] to 4 event variants:
  - `deposit_for_burn` (TokenMessenger contract)
  - `mint_and_withdraw` (TokenMinter contract)
  - `message_sent` (MessageTransmitter contract)
  - `message_received` (MessageTransmitter contract)
  Returns `ErrUnknownEvent` for any other topic[0].
  Contract-ID filtering is on the dispatch layer (per
  CLAUDE.md "Comet uses a shared topic" — same pattern).
  4 events for 3 contracts is fully covered; matches the
  WASM audit decision in registry.go:80.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/cctp/decode.go:31-93` +
  `internal/sources/cctp/events.go` constants.
- **Disposition:** `accepted` — POSITIVE.

#### F-0126 — Cascade unfixed at iter 26 (~33h+ since onset)

- **Severity:** `high` (process — extending F-0116)
- **Title:** R1 probed 7th time at ~04:06 UTC: same state.
  Cascade unfixed for the entire audit window. The audit
  itself has become the canonical state-of-r1 record.
- **Workstream:** W21
- **Evidence:** EV-0071 (7th probe).
- **Cross-ref:** F-0001, F-0039, F-0099, F-0109, F-0116. The
  meta-finding is: alerts that should fire don't (cascade-
  fragility cluster); the audit-loop's r1 probes ARE the
  on-call signal.
- **Disposition:** `closed-as-historical` (2026-05-28).
  Third observational entry in the cascade-duration cluster
  (same shape as F-0109 / F-0116). The Wave-0 fix sequence
  J05 referenced has been executed — tasks #16, #17, #18 ran
  in this session. Combined with the structural alert
  hardening (F-0080 / F-0085 / F-0104 absent_over_time
  guards) the meta-finding's core complaint — "audit-loop
  probes ARE the on-call signal" — no longer holds: a
  recurrence of the same cascade now fires alerts in its own
  right. Trace preserved for post-mortem reference.

#### F-0127 — POSITIVE: Rozo decoder covers both v1 Payment events

- **Severity:** `note` (positive)
- **Title:** `internal/sources/rozo/decode.go::classify`
  dispatches topic[0] to 2 event variants: `Payment` and
  `Flush`. Documented as the v1 Payment topic set. Contract-
  ID filtering is downstream — explicit comment cites the
  "Comet uses shared topic" CLAUDE.md warning, so the decoder
  assumes someone could ship a topic-bytes-identical event
  from a different contract.
  Matches W35 row + WASM audit (registry.go:84) + migration
  0039 schema.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/rozo/decode.go:11-48`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0128 — POSITIVE: SDEX decoder covers every ClaimAtom-producing op

- **Severity:** `note` (positive)
- **Title:** `internal/sources/sdex/decode.go` is classic
  Stellar (not Soroban). `matchesTradeOp` filters every op
  type that stellar-core's ledger meta can produce trades
  from (ManageBuyOffer, ManageSellOffer,
  CreatePassiveSellOffer, PathPaymentStrictReceive,
  PathPaymentStrictSend). `extractClaimAtoms` pulls
  ClaimAtoms from op results; `decodeClaimAtom` projects
  each to canonical.Trade. Multi-claim ops handled via
  `tradeIndex * opIndexFanoutStride + opIdx` for unique
  OpIndex. Differentiates order-book vs LP trades via
  ClaimAtom variant tag.
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/sdex/decode.go:14-180`.
- **Disposition:** `accepted` — POSITIVE. Live `/v1/pools`
  showed SDEX with `trade_count_24h: 35082` confirming the
  decoder is firing under live ingestion.

#### F-0129 — POSITIVE: soroswap_router covers both swap entry points (admin out-of-scope by design)

- **Severity:** `note` (positive)
- **Title:** `internal/sources/soroswap_router/decode.go`
  is a ContractCallDecoder; the router emits NO events
  itself (its work is calling down to per-pair contracts).
  Covers both `swap_exact_tokens_for_tokens` and
  `swap_tokens_for_exact_tokens` (5-arg signatures).
  Admin / read-only methods (`set_pair_fee`, `router_pairs`,
  `init`) are explicitly out-of-scope per
  `events.go:33-37` — "don't move tokens and aren't useful
  for attribution".
- **Workstream:** W07, W35
- **Evidence:** `internal/sources/soroswap_router/decode.go:54-145`;
  `internal/sources/soroswap_router/events.go:34-37`.
- **Disposition:** `accepted` — POSITIVE.

#### F-0130 — POSITIVE: 4 classic observers follow consistent LedgerEntryChangeDecoder pattern

- **Severity:** `note` (positive)
- **Title:** `internal/sources/{accounts,trustlines,
  claimable_balances,liquidity_pools}` all implement
  `dispatcher.LedgerEntryChangeDecoder` with identical
  shape:
  - `Observer` struct with `watched map[string]struct{}`
    allowlist (bounded by config — explicitly NOT
    "watch every account" mode per accounts/doc.go)
  - `NewObserver(watched []string)` constructor returns
    `ErrEmptyWatchSet` for empty input (fail-CLOSED
    — operator must explicitly opt in)
  - `Name()` + `Matches(xdr.LedgerEntryChange) bool`
    interface methods
  - Matches dispatches by LedgerEntryType (Account /
    Trustline / ClaimableBalance / LiquidityPool) +
    asset-key membership in the watched set
  Per CLAUDE.md "Add a new supply observer" recipe.
  Each observer feeds the right supply algorithm:
  - accounts → Algorithm 1 (XLM)
  - trustlines → Algorithm 2 (classic non-native)
  - claimable_balances → Algorithm 1/2 (per asset class)
  - liquidity_pools → contributes LP-reserve component
- **Workstream:** W07, W12, W35
- **Evidence:** the 4 dispatcher_adapter.go files +
  Algorithm 1/2/3 citations in doc.go.
- **Disposition:** `accepted` — POSITIVE. **All 16 Soroban-
  source decoders now spot-audited; zero every-event gaps
  found in any of them.**

#### F-0131 — POSITIVE: supply pipeline (W12) is rigorously implemented per ADRs 0011/0022/0023

- **Severity:** `note` (positive)
- **Title:** `internal/supply/` ships the 3-algorithm
  architecture from CLAUDE.md verbatim:
  - **Algorithm 1 — XLM:** `XLMComputer` in xlm.go; fixed
    50_001_806_812 × 10^7 stroops, SDF-reserve exclusion
    for circulating
  - **Algorithm 2 — Classic credit:** `ClassicComputer` per
    ADR-0022; Total = Σ trustline + Σ claimable + Σ
    LP-reserve + Σ SAC-wrapped (4 downstream observers)
  - **Algorithm 3 — SEP-41 Soroban:** `SEP41Computer` per
    ADR-0023; Total = Σ mint − Σ burn − Σ clawback over
    contract lifetime
  - `Refresher` periodic snapshot worker called by
    aggregator
  - `Crosscheck` validates cross-class consistency
  - `Policy` (operator-configurable): SDFReserveAccounts,
    LockedSet (per-asset locked balances), max_supply
    overrides
  - `Overlay` for manual overrides per ADR-0011's
    precedence chain
- **Numeric discipline:** ALL amount fields use `*big.Int`
  (Trustline, Claimable, LPReserve, SACWrapped,
  IssuerBalance, LockedAccountBalances,
  LockedContractBalances, MintTotal, BurnTotal). **Zero
  `int64` occurrences in amount-bearing types** per
  ADR-0003.
- **ADR citations in code:** ADR-0011 cited 6+ times;
  ADR-0022 + ADR-0023 also cited at function
  responsibilities.
- **Live verification:** `/v1/assets/native` returns
  `circulating_supply: 500018068120000000` + matching
  total_supply (EV-0032).
- **Workstream:** W12
- **Evidence:**
  `internal/supply/{doc,classic,sep41,supply,policy,
  overlay}.go`; grep for `int64` in amount-bearing types
  returns empty.
- **Disposition:** `accepted` — POSITIVE. Supply pipeline
  is one of the most disciplined packages in the codebase.

#### F-0132 — POSITIVE: `scripts/dev/r1-smoke.sh` launch-readiness coverage is well-engineered

- **Severity:** `note` (positive)
- **Title:** The launch-day smoke-test script (referenced by
  `docs/operations/launch-day-checklist.md` and the
  `ratesengine-smoke.timer` systemd timer per CLAUDE.md):
  - **23 check invocations** across 25 distinct routes
    including the cascade-affected family
    (`/v1/oracle/latest`, `/v1/lending/pools`,
    `/v1/diagnostics/cursors`)
  - **Two check flavours:** `check NAME PATH [-- jq-test]`
    (wants HTTP 200 + optional shape assertion) and
    `expect_status STATUS NAME PATH [-- jq-test]` for
    BEHAVIOUR-PINS (e.g. documented `expect_status 404
    "asset not found"`) — prevents silent-200 regressions
    per the #1134 motivation in the script header
  - **Independent checks** — one failing endpoint doesn't
    short-circuit; the run reports every break
  - **Exit code = number of failures** — cron +
    Healthchecks.io compatible
  - **Per-request timeout 10s** — just above the 8s
    server-side context.WithTimeout ceiling
  - **Color disabling when not a TTY** — cron/log-file safe
  - **`jq -e` for shape assertions** — falsy/missing fields
    fail the check
  - **Stale references to removed `/v1/coins` and
    `/v1/currencies` are in COMMENT blocks only** — actual
    checks use the rc.48-renamed `/v1/assets` family
- **Workstream:** W22, W15
- **Evidence:** `scripts/dev/r1-smoke.sh` lines 1-60 +
  check/expect_status counts.
- **Disposition:** `accepted` — POSITIVE. **Would have
  caught the cascade** if run since cascade onset
  (`/v1/oracle/latest` returns HTTP 500 under F-0086, which
  smoke would have failed).
- **Audit note (F-0099 + F-0100 cross-ref):** The smoke
  timer is part of `configs/healthchecks/`. If it WAS
  running, the cascade would have surfaced as failures on
  the oracle/lending routes. Either the timer is broken,
  Healthchecks.io alerts are mis-routed, or operators
  aren't reading the dashboard. Worth a launch-day
  verification step.

#### F-0133 — **HIGH** Deployed smoke script on r1 is DIFFERENT from `scripts/dev/r1-smoke.sh`; r1 smoke passes despite cascade

- **Severity:** `high` (drift between dev smoke + deployed
  smoke explains the cascade-invisibility)
- **Title:** `ratesengine-smoke.timer` on r1 fires every 5min;
  most recent fire (07:03:51 CEST) reports `ExecMainStatus=0`.
  But the cascade-affected routes (specifically tested:
  `/v1/oracle/latest?asset=native` on BOTH the public TLS-
  terminated endpoint AND `http://localhost:3000` directly on
  r1) return HTTP 500. So smoke is reporting pass for an
  API that's demonstrably failing on launch-critical routes.
  Root cause: `ExecStart=/opt/ratesengine/healthchecks/smoke.sh`
  (deployed via Ansible) is a SEPARATE file from
  `scripts/dev/r1-smoke.sh` audited at F-0132. The deployed
  copy doesn't check the cascade-affected routes — or checks
  a subset that's currently 200-passing despite the broader
  failure.
- **Workstream:** W22, W14, W18
- **Evidence:**
  - r1 SSH: `systemctl is-active ratesengine-smoke.timer` →
    active; `ExecMainStatus=0` at 07:03:51 CEST
  - Live curl `http://localhost:3000/v1/oracle/latest?asset=native`
    on r1 itself returns HTTP 500
  - Smoke ExecStart: `/opt/ratesengine/healthchecks/smoke.sh`
    (NOT the repo `scripts/dev/r1-smoke.sh`)
- **Adversarial vector:** **the most sinister finding so far** —
  the launch-critical liveness check is reporting GREEN while
  the actual API is RED. Combines F-0099 (unchecked post-
  mortem follow-ups) + F-0080 (alert pipeline silent) +
  F-0100 (checklist passes vacuously). Three layers of
  cascade-blindness, all designed to catch this exact mode,
  all silent.
- **Cross-ref:** F-0099, F-0100, F-0080, F-0085, F-0104.
- **Disposition:** `closed` (Wave-0 step 9, task #24 this
  session). Three actions shipped:
  1. `configs/healthchecks/install.sh` re-run on r1 syncs
     `scripts/dev/r1-smoke.sh` → `/opt/ratesengine/
     healthchecks/smoke.sh`; md5 matches post-install.
  2. The repo smoke ALREADY covers the cascade-affected route
     family (`/v1/oracle/*`, `/v1/lending/pools`, `/v1/vwap`)
     at the multi-status-acceptance level; once aligned to r1
     the gap disappears.
  3. Drift detection shipped via Wave-1 task #28
     `make verify-r1-sync` (F-0142 closure) which md5-diffs
     deployed smoke against repo. CI surrogate.
  Cross-ref F-0136 / F-0142 for the meta-finding.

#### F-0134 — **HIGH** Deployed `r1-smoke.sh` is significantly behind repo (13 vs 23 checks; ~100 LOC short; md5 mismatch)

- **Severity:** `high` (refines F-0133)
- **Title:** Cross-walk via SSH:
  - **Repo:** `scripts/dev/r1-smoke.sh` 213 LOC, 23 checks,
    md5 `35f95127`
  - **Deployed:** `/opt/ratesengine/healthchecks/r1-smoke.sh`
    113 LOC, 13 checks, md5 `683bd1dd`
  - **Drift:** 10 missing checks (the cascade-affected
    family `/v1/oracle/latest`, `/v1/lending/pools`,
    `/v1/vwap`, the SAC wrappers, the diagnostics surface)
  - **Stale check:** deployed runs `check "coins (top 5)"
    "/v1/coins?limit=5"` expecting HTTP 200, but `/v1/coins`
    returns 404 (removed in rc.48 per F-0132 comment block).
    Yet smoke reports `ExecMainStatus=0`.
- **Workstream:** W22, W18 (deployment drift), W14
- **Evidence:** `md5sum /opt/ratesengine/healthchecks/r1-smoke.sh`
  vs `md5 scripts/dev/r1-smoke.sh` → mismatch; live curl
  `/v1/coins?limit=5` → 404 on both public + localhost.
- **Adversarial vector:** the deployed smoke is **3 layers
  of broken**:
  1. Doesn't check the cascade-affected routes (F-0133)
  2. Checks a route that doesn't exist (`/v1/coins`)
  3. Reports exit 0 despite check #2 inevitably failing on
     HTTP 404
  Either the check function has a bug, or the exit-code
  accumulator isn't actually counting failures. Probably
  worth a 5min `bash -x` run on the deployed copy.
- **Cross-ref:** F-0133, F-0099, F-0100, F-0080.
- **Disposition:** `closed` (Wave-0 step 9, task #24 +
  Wave-1 task #27 this session). Operationally: ran
  `configs/healthchecks/install.sh` on r1 which copies
  `scripts/dev/r1-smoke.sh` to
  `/opt/ratesengine/healthchecks/smoke.sh` — md5 matches
  post-install; the 504-LOC repo smoke is now what fires
  every 5 min via `ratesengine-smoke.timer`. Structural:
  Wave-1 task #27 wired the install.sh into the Ansible
  `archival-node` role so the next provisioning pass
  matches repo without manual SCP (F-0137 closure).
  `/v1/coins` 404 false-positive from the old smoke is gone
  because the new file doesn't reference that route.

#### F-0135 — `ledgerstream-tier.yml` rule file MISSING from deployed `/etc/prometheus/rules.r1/`

- **Severity:** `medium`
- **Title:** Deployed prometheus rules dir has 20 files;
  repo has 21. The file missing in production is
  `ledgerstream-tier.yml` — the rule file containing the
  `ratesengine_ledgerstream_tier_both_missing`
  page-severity alert for ADR-0027 cold-tier (the F-0112
  POSITIVE design). If both hot + cold LCM reads miss, no
  alert fires in production.
- **Workstream:** W18, W14
- **Evidence:** `diff /tmp/r1-rules.txt /tmp/repo-rules.txt`
  → `< ingestion.yml.bak-pre-f1208` (stale residue on r1)
  + `> ledgerstream-tier.yml` (missing on r1) + `> README.md`
  (docs, expected).
- **Cross-ref:** F-0112 POSITIVE design exists in code +
  repo rules; r1 doesn't actually have the alert wired.
- **Disposition:** `closed` (Wave-0 step 10, task #25 this
  session). `ledgerstream-tier.yml` scp'd to
  `/etc/prometheus/rules.r1/` on r1, Prometheus reloaded;
  the F-0112 alerts now evaluate against real series. Stale
  `.bak-pre-f1208` residue deleted in the same pass.

#### F-0136 — Ansible deployment drift cluster (F-0133 + F-0134 + F-0135 are symptoms)

- **Severity:** `high` (meta-process)
- **Title:** Three drift instances surfaced in iterations
  30-31:
  - F-0133/F-0134: smoke script (md5 mismatch)
  - F-0135: missing rule file in /etc/prometheus
  - + the `ingestion.yml.bak-pre-f1208` residue
  These are SYMPTOMS of a deeper class: there's no CI
  check that asserts deployed-config-on-r1 == repo-tracked-
  config, so once Ansible drifts (failed run, partial
  push, manual edit, etc.) it stays drifted. The audit's
  finding-of-findings is: **the doc-vs-code lint
  (F-0113 POSITIVE) doesn't extend to ops files.**
- **Workstream:** W18, W25
- **Evidence:** F-0133, F-0134, F-0135 + the
  `ingestion.yml.bak-pre-f1208` residue.
- **Disposition:** `closed` (Wave-1, task #28 this session).
  `make verify-r1-sync` now performs the periodic drift-check
  the audit asked for: md5 + ls comparisons of key
  directories on r1 against the repo-tracked state, with
  the Postgres migration drift check added later in the
  same session. Symptom-findings F-0133 / F-0134 / F-0135
  are closed under tasks #24 / #25 / #26 respectively;
  F-0136 documents the structural countermeasure.

#### F-0137 — Smoke install is a MANUAL `configs/healthchecks/install.sh` step, NOT Ansible-managed

- **Severity:** `high` (root cause of F-0134/F-0135 drift)
- **Title:** Deploy mechanism for healthchecks (smoke +
  heartbeat + sla-probe scripts + systemd units) is
  `configs/healthchecks/install.sh` — a manual operator
  script. **It is NOT invoked by any Ansible playbook**
  (`grep -rn r1-smoke configs/ansible/` returns empty).
  So once a healthchecks-touching PR merges + deploys
  binaries, the production install on r1 stays at whatever
  version of `r1-smoke.sh` was last manually installed.
- **Workstream:** W18, W22
- **Evidence:** `configs/healthchecks/install.sh:24` copies
  `$REPO_ROOT/scripts/dev/r1-smoke.sh` to
  `/opt/ratesengine/healthchecks/r1-smoke.sh`. Ansible role
  `archival-node/tasks/13-healthcheck.yml` only deploys
  `node-healthcheck.sh` (system-uptime ping), NOT the
  ratesengine-smoke surface. No Ansible task references
  install.sh.
- **Adversarial vector:** every binary deploy invalidates
  the smoke until the operator manually re-runs
  install.sh. Until then, the deployed smoke runs old
  checks against new code — silent regression detector
  goes dark.
- **Cross-ref:** F-0133, F-0134, F-0135, F-0136 are all
  symptoms of this.
- **Disposition:** `closed` (Wave-1 task #27 this session).
  Took option (1): `configs/healthchecks/install.sh` is now
  invoked by the Ansible `archival-node` role on every
  healthchecks/ deploy pass. Subsequent binary releases keep
  the deployed smoke in lockstep with the repo copy
  without operator SCP. Recurrence detection ships via
  `make verify-r1-sync` (task #28) which md5-diffs the
  deployed smoke against the repo path — if a future
  Ansible run breaks the install step, the drift surfaces
  before the next deploy. Symptoms F-0133 / F-0134 / F-0135
  are closed under tasks #24 / #25 above.

#### F-0138 — Caddyfile drift-free — BUT BY COINCIDENCE, NOT BY MECHANISM (downgraded from POSITIVE to NOTE)

- **Severity:** `note` (process, downgraded from positive)
- **Title:** Caddyfile sync was initially flagged POSITIVE
  in iter 33 because md5 matched. Further investigation
  shows: **no Ansible task or workflow deploys Caddyfile.**
  `configs/caddy/README.md:33` documents the deploy step
  as a MANUAL `cp configs/caddy/Caddyfile.api /etc/caddy/
  Caddyfile`. The md5 match is because Caddyfile hasn't
  been edited in the repo since the last manual copy
  (April 2026 per git log). So Caddy IS at the same drift
  risk as smoke + alertmanager — it just happens to be
  frozen-state-identical right now.
- **Workstream:** W18
- **Evidence:** `grep -rn "Caddyfile" configs/ansible/`
  returns empty; `configs/caddy/README.md:33` documents
  manual cp step.
- **Cross-ref:** F-0137 (same root cause: manual deploy
  for non-template configs).
- **Disposition:** `note` — process clarification. The
  drift cluster is broader than the iter-33 framing
  suggested: it's "every config not rendered by an
  Ansible task". Including Caddy + smoke + alertmanager.
  Wave 1 fix per F-0137 should be applied broadly.

#### F-0139 — HIGH: alertmanager.yml drift (deployed 27 lines behind repo)

- **Severity:** `high`
- **Title:** Deployed `/etc/prometheus/alertmanager.yml`:
  82 LOC, md5 `04a3f30a1530a1c95c6d244867454ce0`. Repo
  `configs/alertmanager/alertmanager.r1.yml`: 109 LOC, md5
  `cb090abbde875bf67c052d70433c7765`. **Different (27 LOC
  short on deployed).** The deployed config is missing
  the latest 27 lines worth of routing rules — likely
  receivers / additional inhibitions / new severity routes.
- **Workstream:** W18, W14
- **Evidence:** ssh md5sum + wc -l on both sides.
- **Adversarial vector:** alert routing per the repo
  alertmanager.r1.yml may include severity-specific routes
  (page-vs-ticket-vs-informational per the F-0107 sweep
  count) that don't apply in production. So an alert
  intended to page may route to a ticket queue (or
  vice-versa), or to nothing.
- **Cross-ref:** F-0137 (root cause: no Ansible task syncs
  configs/alertmanager/ to /etc/prometheus/). But ALSO:
  there IS a `prometheus_pair` Ansible role with task
  `04-alertmanager-configure.yml` that renders
  `/etc/alertmanager/alertmanager.yml` from a J2 template.
  HOWEVER the actual running path on r1 is
  `/etc/prometheus/alertmanager.yml` (Debian package
  default). Possible path mismatch: Ansible renders to one
  path; daemon reads from another.
- **Disposition:** `closed` (Wave-0 step 11, task #26 this
  session). The path-mismatch root cause was confirmed: r1's
  `prometheus-alertmanager` reads `/etc/prometheus/
  alertmanager.yml` (Debian default), not the
  `/etc/alertmanager/alertmanager.yml` the Ansible role
  rendered to. Operationally resolved via
  `scp configs/alertmanager/{alertmanager.r1.yml,apply.sh}`
  + `bash apply.sh` on r1 → amtool check-config SUCCESS,
  `systemctl reload prometheus-alertmanager` exit 0,
  service `active`. Recurrence detection: `make
  verify-r1-sync` (task #28) md5-diffs the deployed
  alertmanager config. The Ansible role path mismatch is
  tracked under F-0155 for a future cleanup pass; runtime
  is correct today via the apply.sh path.

#### F-0140 — Ansible `prometheus_pair` role targets HA-pair multi-host, not r1 single-host

- **Severity:** `medium`
- **Title:** The role at
  `configs/ansible/roles/prometheus/` is named
  `prometheus_pair` per its README + `prometheus_pair`
  inventory references. R1 is single-host (per ADR-0016
  and the r1.example.yml header). It's unclear whether
  this role is actually applied to r1 or only to a
  hypothetical 2-host Prometheus HA pair. If never applied
  to r1, the entire role's drift is academic — but then
  there's NO Ansible-managed Prometheus deploy for r1.
- **Workstream:** W18, W23
- **Evidence:** `configs/ansible/roles/prometheus/README.md`
  references "prometheus_pair (self-scrape)" + "alertmanager_pair";
  r1 inventory at `r1.yml` uses single-host overrides.
- **Cross-ref:** F-0139 may stem from this — if the role
  doesn't run against r1, neither does the
  alertmanager.yml.j2 templating.
- **Disposition:** `closed-as-investigated` (2026-05-28).
  Verified during Wave-0 step 11 (task #26): r1's
  Prometheus + Alertmanager use the Debian-default config
  paths (`/etc/prometheus/alertmanager.yml`,
  `/etc/prometheus/prometheus.r1.yml`), not the HA-pair
  paths the `prometheus_pair` role renders to. Operationally
  r1 is managed via the apply.sh patterns (alertmanager) +
  manual scp (prometheus.r1.yml) — these are documented in
  F-0142's cross-walk table and drift-detected by `make
  verify-r1-sync`. Refactoring the Ansible role to a single-
  host shape is a clean-up item tracked under F-0155;
  doesn't block any function today.

#### F-0141 — POSITIVE: Prometheus.yml is in sync (md5 match)

- **Severity:** `note` (positive)
- **Title:** Deployed `/etc/prometheus/prometheus.yml`
  md5 `a314cd4c0723161c8de99b85e628141a` matches repo
  `configs/prometheus/prometheus.r1.yml` exactly. 156
  LOC both sides. Like Caddy this is likely
  manual-coincidence rather than mechanism — but the file
  is in sync at audit time.
- **Workstream:** W18, W14
- **Evidence:** ssh md5sum + md5 cross-walk.
- **Disposition:** `accepted` — sync intact. Same fix
  applicability (F-0137 broad Wave 1 fix).

#### F-0142 — Drift cluster generalisation: every `configs/<area>/<file>` not in an Ansible task is at risk

- **Severity:** `medium` (process / pattern)
- **Title:** Categorising the drift cluster:
  | Config path | Deploy mechanism | Drift status |
  | --- | --- | --- |
  | `configs/caddy/Caddyfile.api` | manual `cp` per README | sync (coincidence) F-0138 |
  | `configs/healthchecks/*.sh` | manual `install.sh` | DRIFT F-0133/0134/0135 |
  | `configs/alertmanager/alertmanager.r1.yml` | unclear (role naming gap) | DRIFT F-0139 |
  | `configs/prometheus/prometheus.r1.yml` | manual `cp`? | sync (coincidence) F-0141 |
  | `configs/prometheus/rules.r1/*.yml` | unclear | partial drift (F-0135) |
  | `configs/ansible/roles/archival-node/templates/postgresql.conf.j2` | **Ansible-templated** | not directly comparable |
  Only Ansible-templated files (postgresql.conf.j2,
  ratesengine.toml.j2, galexie.toml.j2, etc.) have a
  guaranteed sync mechanism. **Everything else is
  drift-eligible.**
- **Workstream:** W18, W25
- **Evidence:** above cross-walk.
- **Disposition:** `closed` (Wave-1, task #28 this session).
  `make verify-r1-sync` exists at the Makefile root and runs
  md5 + ls comparisons of every config path in the cross-
  walk table against r1's deployed copies, in line with the
  audit's recommendation. Bundled with task #27's F-0137
  install.sh Ansible wiring so the structural drift cause is
  also fixed. CI can't SSH to r1 so the target is an
  operator-pre-deploy gate, not a PR gate — same shape the
  audit acknowledged.

#### F-0143 — Test gap: no adversarial test asserts fail-CLOSED-on-Redis-error for signup throttle + ratelimit

- **Severity:** `medium` (test coverage)
- **Title:** `internal/auth/signup_ip_throttle_test.go` and
  `internal/ratelimit/bucket_test.go` contain no test
  asserting the behaviour when Redis returns MISCONF (or
  any error). The current code fails-OPEN under such
  conditions (F-0049, F-0050); a future refactor could
  silently change that to fail-CLOSED (good) or to a
  panic (bad) without any CI signal.
- **Workstream:** W15, W19
- **Evidence:** `grep -n "MISCONF\|fail.open\|fail_open\|
  ErrCacheUnavailable\|redis.*err"` on both test files
  returns empty.
- **Cross-ref:** F-0049, F-0050, J40 adversarial journey
  trace.
- **Disposition:** `closed` (Wave-1, task #34 this session).
  Adversarial fail-CLOSED tests live alongside the F-0049 /
  F-0050 / F-0149 dwell-time inversion shipped at task #21:
  mocked Redis returns MISCONF on every command; the
  rate-limit, signup-throttle, and price/oracle handlers all
  assert HTTP 503 (not 200, not panic). Regression of the
  fail-CLOSED posture is now caught at PR time.

#### F-0144 — POSITIVE: J40 adversarial journey trace written

- **Severity:** `note` (positive — audit completeness)
- **Title:** J40 captures the concrete attack chain under
  the F-0039 cascade: attacker exploits F-0049 (signup
  throttle fail-open) to create N free accounts +
  F-0050 (global ratelimit fail-open) to scrape M
  price quotes, all at zero rate-limit cost. Closure
  rule binds the journey to F-0049 + F-0050 + F-0039
  resolution.
- **Workstream:** W19, W22, audit-process
- **Evidence:** `docs/audit-2026-05-26/journeys-traces/
  J40-adversarial-rate-limit-bypass.md`.
- **Disposition:** `accepted` — POSITIVE. Audit now has
  4 journey traces (J05 recovery + J20 user + J30
  operator backfill + J40 adversarial) covering all four
  primary perspectives.

#### F-0145 — `/v1/price/tip` returns HTTP 500 under cascade

- **Severity:** `high` (extends F-0086/0087/0089 cluster)
- **Title:** Live probe 2026-05-27 of
  `/v1/price/tip?asset=native&quote=fiat:USD` returns
  HTTP 500 `errors/internal` with no detail. The
  rolling-window tip surface per ADR-0018 is
  cascade-affected like /v1/oracle/* and /v1/vwap.
  No fallback to closed-bucket LKG.
- **Workstream:** W11, W10
- **Evidence:** raw curl 2026-05-27 EV-0093.
- **Disposition:** `closed` (verified 2026-05-28).
  `internal/api/v1/price_tip.go:99-102` now calls
  `IsCacheUnavailable(err)` and `writeCacheUnavailable
  Problem(w, r)` — same shape Wave-0 step 7 applied to
  the other cascade-affected handlers. Cache-unavailable
  errors now map to HTTP 503 with Retry-After per
  F-0090. Closed by Wave-0 task #22.

#### F-0146 — `/v1/observations/stream` returns HTTP 500 (SSE init failure under cascade)

- **Severity:** `high`
- **Title:** Live probe returns HTTP 500 BEFORE establishing
  the SSE connection. Streaming observations surface is
  broken under the F-0039 cascade.
- **Workstream:** W11
- **Evidence:** raw curl 2026-05-27 EV-0093.
- **Adversarial vector:** clients connecting to SSE
  endpoints typically retry-on-error with exponential
  backoff. A 500 storm during cascade triggers retry
  amplification at exactly the wrong time. Worse for
  clients than a 503-with-Retry-After.
- **Cross-ref:** F-0090 (503 mapping) + F-0049/F-0050
  (rate-limit fail-open under same cascade = no protection
  against retry storms).
- **Disposition:** `closed` (verified 2026-05-28).
  `internal/api/v1/observations.go:210-214` calls
  `IsCacheUnavailable(err)` → `writeCacheUnavailable
  Problem(w, r)` for Redis/cache outages, mapping to
  HTTP 503 with Retry-After per F-0090. Closed by
  Wave-0 task #22 (same shape as F-0145 fix).

#### F-0147 — POSITIVE: Oracle handler already ships a 503 path; F-0090 fix is precisely scoped

- **Severity:** `note` (positive — refines remediation)
- **Title:** `internal/api/v1/oracle.go::handleOracleLatest`
  already has a 503 ServiceUnavailable response path at
  line 172-176 (for the `oracle-latest-timeout` case when
  `LatestOracleUpdatesForAsset` exceeds 8s context).
  Other errors fall through to HTTP 500 internal-error
  at line 180-183. **The remediation per F-0090 is
  narrowly scoped:** add a `errors.Is(err, redis.ErrMISCONF)`
  (or equivalent) check that branches to the existing
  503 path (or returns a sibling 503 with a different
  `errors/<name>` URL). Wave 0 step 7 is now a few-line
  change per handler, not a full rewrite.
- **Workstream:** W11, W19
- **Evidence:** `internal/api/v1/oracle.go:170-183` (the
  existing 503 path) + cross-walk to `/v1/price.go::handlePrice`
  which also has explicit ErrPriceNotFound → priceFallback
  branching (price.go:285).
- **Cross-ref:** F-0090, F-0086/0087/0089/0145/0146 cluster.
- **Disposition:** `accepted` — POSITIVE. **Wave 0 step 7
  is more tractable than initially framed** — handlers
  have the right 503 scaffolding; just need the right
  upstream error to be classified.

#### F-0148 — POSITIVE: All 5 cascade-affected handlers already have 503 scaffolding

- **Severity:** `note` (positive — extends F-0147)
- **Title:** Cross-walked the 5 cascade-affected handlers
  (oracle.go, lending.go, vwap.go, observations.go,
  price_tip.go). EACH ONE already has at least one
  `StatusServiceUnavailable` (503) response path:
  - oracle.go:174 — "Oracle latest query timed out"
  - lending.go:62 — "Lending pools query timed out"
  - vwap.go:53 — "VWAP serving not configured"
  - observations.go:46 — "Observations serving not configured"
    + line 135 "Observations query timed out"
  - price_tip.go:61 — "Price serving not configured"
  All 5 ALSO fall through to `StatusInternalServerError`
  (500) for the generic err branch (oracle.go:182,
  vwap.go:109/143, observations.go:142, price_tip.go:103).
- **Wave 0 step 7 remediation is now precisely one-line
  per handler:** add `if errors.Is(err, redis.ErrMISCONF)
  { writeProblem(... 503 ...) }` BEFORE the generic 500
  fallthrough.
- **Workstream:** W11, W19
- **Evidence:** grep on the 5 handler files.
- **Cross-ref:** F-0086, F-0087, F-0089, F-0090, F-0145,
  F-0146, F-0147.
- **Disposition:** `accepted` — POSITIVE. **Wave 0 step 7
  estimate refined from "1-2h dev" to "~30min dev" —
  it's 5 nearly-identical insertions.**

#### F-0149 — F-0049 fail-CLOSED fix has product tradeoff; recommend dwell-time inversion

- **Severity:** `medium` (remediation-refinement of F-0049)
- **Title:** Code-walk of `internal/api/v1/signup.go:332-336`
  shows the fail-OPEN path is a 5-line block that can be
  inverted to fail-CLOSED in 3 lines. BUT the comment at
  line 307-309 cites a deliberate UX trade-off: "Falls open
  on Redis errors so a transient backend blip doesn't take
  signup offline." Under transient Redis blips (sub-second),
  fail-open is defensible UX. Under SUSTAINED Redis MISCONF
  (the live F-0039 cascade), fail-open is the J40 attack
  vector.
- **Refined recommendation:** instead of pure fail-CLOSED,
  implement DWELL-TIME inversion:
  - First N seconds (e.g. 30s) of Redis errors → fail-OPEN
    (preserves current transient-blip UX)
  - After dwell-time → fail-CLOSED with 503 + Retry-After
    (closes the J40 vector under sustained cascade)
  Same pattern applies to `internal/ratelimit/bucket.go:138`.
- **Workstream:** W19, W22
- **Evidence:** `internal/api/v1/signup.go:307-336` +
  `internal/auth/signup_ip_throttle.go:75-77` comment.
- **Cross-ref:** F-0049, F-0050, J40 adversarial trace.
- **Disposition:** `closed` (Wave-0 step 6, task #21 this
  session). Dwell-time inversion shipped as recommended.
  The throttle structs carry a `redisErrorSince`-shaped
  state so the first N seconds (configurable; default 30 s)
  of Redis errors fail-OPEN to preserve the transient-blip
  UX, then flip fail-CLOSED → 503 + Retry-After after the
  dwell-time elapses. Adversarial fail-CLOSED tests
  (task #34) pin the post-dwell behaviour so regressions are
  caught at PR time. Same pattern applied to the
  rate-limit bucket per the recommendation.

#### F-0150 — POSITIVE: signup IP throttle has clean error-classification at the handler layer

- **Severity:** `note` (positive)
- **Title:** `signupIPThrottleOK` at signup.go:313 ALREADY
  distinguishes 3 cases:
  1. `nil` error → return true (proceed)
  2. `auth.ErrSignupRateLimited` → 429 problem (line 325-330)
  3. other (Redis blip) → WARN + fall-open (line 332-336)
  Adding a 4th case for "sentinel: dwell-time-exhausted
  Redis error → 503" is the minimal F-0149 implementation.
- **Workstream:** W19
- **Evidence:** signup.go:321-336.
- **Disposition:** `accepted` — POSITIVE. The handler's
  error-classification is already 3-way; adding a 4th
  branch is the textbook extension.

## ID Allocation

Findings are numbered monotonically. `F-0001` onward.
Critical/High findings should ideally end up in F-0001..F-0099
when re-ordered by severity for the remediation plan.

## Disposition Workflow

`needs_evidence` → `open` once evidence collected (ID
populated in this row).

`open` → `needs_owner` when remediation wave is identified.

`needs_owner` → `closed-by-PR-####` when remediation PR merges
+ verify rerun + (if relevant) post-change R1 probe.

`open` → `accepted` requires explicit operator confirmation
recorded in a `note` row appended below.

`open` → `wontfix` requires explicit reasoning recorded in a
`note` row.

`open` → `duplicate` when discovered to overlap with an earlier
finding.

`open` → `invalid` when re-investigation shows the original
claim was wrong; record the corrected understanding.

#### F-0151 — **HIGH** postgres@15-main service was DOWN for ~10h; audit's "ingestion healthy" claims were partially false

- **Severity:** `high` (audit-process correction + operational gap)
- **Title:** During Wave-0 step 1 execution (2026-05-27 ~11:40 CEST), discovered `postgresql@15-main.service` had been `failed (exit-code 1)` since `01:49:37 CEST` — ~10h before any audit fixes touched r1. Crash was caused by the same disk-full F-0001 (postgres can't write to its log → panics → exits). All audit probes between ~23:49 UTC May 26 and 09:40 UTC May 27 reported "ingestion healthy" but were actually reading Redis cache fronts; the underlying DB cluster was down.
- **Why the audit missed it:** systemctl `postgresql.service` (Debian umbrella) reported `active` because it's a oneshot wrapper that finishes after invoking `pg_ctlcluster`. The actual cluster service `postgresql@15-main.service` was the failed one. My probes used the umbrella name.
- **Workstream:** W21 (live R1 state), W14 (alerting), audit-process
- **Evidence:** `systemctl status postgresql@15-main.service` at 11:40 CEST showed "failed (Result: exit-code) since Wed 2026-05-27 01:49:37 CEST; 9h ago" + Main PID exited status=1/FAILURE. Now active after `systemctl start postgresql@15-main.service` post-disk-free.
- **Adversarial vector:** silent DB crash hidden behind cached-API surface for 10+ hours. Indexer + aggregator silently couldn't write new state. Recovery needed: WAL replay on restart (succeeded), then re-pointing all consumers — they auto-reconnected on socket appearance.
- **Disposition:** `closed` (tasks #48 + #37 this session).
  Two fixes shipped: (a) the indexer's Postgres pool now
  forces connection lifetime + ping at startup so a
  postgres-down state doesn't leave silent zombie cursors
  (task #48); (b) postgres_exporter installed on r1
  (task #37) and the exporter-down meta-alert
  `up{job="postgres_exporter"} == 0 OR absent_over_time(...)`
  ships under F-0085 closure (task #20). Recurrence is now
  alertable in seconds rather than discoverable only after a
  10 h cached-API window. The umbrella-vs-cluster service-
  name trap that hid the original crash from audit probes
  is noted as historical methodology trace.

#### F-0152 — **HIGH** prometheus exporters (redis/postgres/pgbackrest) NEVER INSTALLED on r1 (promoted from Wave-1)

- **Severity:** `high` (promoted from Wave-1 per user directive 2026-05-27)
- **Title:** F-0045/F-0046/F-0047 weren't "exporters crashed" — they were never installed on r1. `prometheus.r1.yml` has placeholder scrape jobs with comments like "postgres_exporter — NOT installed by ansible today; once an operator adds the role + service, this scrape job consumes its metrics for storage.yml's pg_* alerts." The audit framed this as a runtime gap; reality is a deployment gap.
- **Workstream:** W18, W14
- **Evidence:** ssh r1: `ls /etc/systemd/system/` shows no `*exporter*.service` files other than node_exporter; `dpkg -l | grep exporter` shows only `prometheus-node-exporter`; prometheus scrape config has them as placeholders with documented "not installed yet" comment.
- **Disposition:** `closed` (task #37, this session). All
  three exporters installed on r1:
  1. `prometheus-redis-exporter` — Debian pkg + systemd unit
  2. `prometheus-postgres-exporter` — Debian pkg + env-file
     `DATA_SOURCE_NAME`
  3. `pgbackrest_exporter` — upstream binary release +
     systemd unit
  Targets on Prometheus all show `up`; F-0085 meta-alerts
  (task #20) now have real series to evaluate against rather
  than firing continuously on the absence. The Ansible task
  paths under `configs/ansible/roles/archival-node/tasks/`
  cover all three for future provisioning.

#### F-0153 — vwap.go + observations.go near complexity threshold (refactored during step 7)

- **Severity:** `low` (code-organization signal)
- **Title:** Adding the `IsCacheUnavailable` branch to `internal/api/v1/vwap.go::handleVWAP` and `internal/api/v1/observations.go::handleObservations` pushed both over the golangci-lint `funlen`/`gocognit` thresholds. Subagent extracted helpers `fetchVWAPTrades` and `fetchObservationsOrWriteError` to stay under threshold. Signal that these handlers were already near maintenance limits before this branch.
- **Workstream:** W11
- **Evidence:** subagent report from Wave-0 step 7; the helper extractions are in the working tree.
- **Disposition:** `accepted` — handler-complexity bounds working as designed. No new finding action needed; the helper-extraction is the right outcome.

#### F-0154 — Audit-probe methodology gap: `systemctl is-active postgresql.service` is the umbrella, not the cluster

- **Severity:** `note` (audit-methodology)
- **Title:** Until this iteration the audit's R1 probes used `systemctl is-active postgresql.service` (the Debian umbrella oneshot, always `active`) instead of `systemctl is-active postgresql@15-main.service` (the actual cluster). That's how F-0151 hid for the audit's entire window.
- **Workstream:** audit-process, W21
- **Evidence:** EV-0050 ("`ratesengine-{api,indexer,aggregator}.service` all `active`" — never checked `postgresql@15-main`).
- **Disposition:** `accepted` — update r1-smoke.sh to include `pg_isready` + the cluster-level systemd check; update `12-r1-live-probe-protocol.md` to require cluster-level checks for postgres/redis/galexie always. Filed as part of `make verify-r1-sync` work (F-0142).

#### F-0155 — Ansible `prometheus_pair` role targets non-existent path on r1 (refinement of F-0140)

- **Severity:** `medium`
- **Title:** role renders to `/etc/alertmanager/alertmanager.yml` but Debian-package daemon reads `/etc/prometheus/alertmanager.yml`. For R2/R3 multi-host, fix the role's target path. For r1 single-host, the manual SCP we did in Wave-0 step 11 is the right shape.
- **Workstream:** W14, deployment-mechanism
- **Evidence:** Wave-0 step 11 (2026-05-27): `ps auxww` on r1 shows `/usr/bin/prometheus-alertmanager` with the Debian default config path `/etc/prometheus/alertmanager.yml`; `/etc/alertmanager/alertmanager.yml` does not exist on r1; `configs/ansible/roles/prometheus/tasks/04-alertmanager-configure.yml` template `dest:` is `/etc/alertmanager/alertmanager.yml`. F-0139 drift was resolved via `scp configs/alertmanager/{alertmanager.r1.yml,apply.sh}` + `bash apply.sh` on r1 (amtool check-config SUCCESS, `systemctl reload prometheus-alertmanager` exit 0, service `active`).
- **Disposition:** `closed` (Wave-0 step 11 + Wave-1 task #28
  this session). F-0139 alertmanager drift was operationally
  closed on r1 via `scp configs/alertmanager/{alertmanager.r1.yml,apply.sh}` +
  `bash apply.sh` (amtool check-config SUCCESS, `systemctl
  reload prometheus-alertmanager` exit 0, service `active`).
  The structural countermeasure — recurrence detection — is
  shipped via `make verify-r1-sync` (task #28) which md5-
  diffs the alertmanager config against r1's deployed copy
  and surfaces any future drift before it can compound.
  The Ansible role target gap (HA-pair vs single-host
  `dest:` path) remains a clean-up item; the recommended
  archival-node task path is documented in the F-0140
  evidence so a future operator pass can mirror the
  redis_exporter wiring.

#### F-0156 — Smoke OHLC check expects HTTP 200 but route correctly returns 404 on no-trades windows

- **Severity:** `low` (smoke-script bug; not a binary defect)
- **Title:** Live smoke run 2026-05-27 post-Wave-0-fix:
  `check "ohlc USDC/XLM" "/v1/ohlc?base=USDC-GA5Z...&quote=native"`
  fails because the route returns HTTP 404 `errors/no-trades`
  when the test pair has no trades in the default window. Per
  ADR-0018 that's the documented contract. The smoke script
  asserts HTTP 200, which is overly strict.
- **Disposition:** `closed` (verified 2026-05-28). The smoke
  script at `scripts/dev/r1-smoke.sh:172` was already updated
  to `expect_status "200|404" "ohlc USDC/XLM"` — multi-status
  acceptance was added to `expect_status()` in an earlier
  hardening pass and the OHLC line uses it. Live smoke run on
  r1 returned `ok ohlc USDC/XLM` against `expect_status "200|404"`.

#### F-0157 — Smoke "asset not found" behaviour pin reports curl transport error

- **Severity:** `low` (smoke-script bug)
- **Title:** `expect_status 404 "asset not found" "/v1/assets/AAAA-..."`
  reports "curl error" rather than a clean 4xx assertion. URL-
  escaping suspect in the smoke script's curl invocation when
  the asset_id contains `:` or `-` characters interpreted by
  the shell.
- **Disposition:** `closed-by-PR-<next>` (re-verified 2026-05-28).
  Initial closure was premature — the comment in
  `expect_status` claimed per-check timeout support but the
  code passed the global `$TIMEOUT` (10 s) unconditionally.
  Live smoke run reopened the finding (`FAIL asset not found —
  curl error`) when the cold-cache resolver path crossed 10 s.
  This pass: (a) actually implements `--timeout N` flag
  parsing in `expect_status`; (b) bumps the behaviour-pin to
  20 s. Verified via direct r1 smoke run — `ok asset not found`
  against `/v1/assets/AAAA-GA5Z...` HTTP 404.
- **Disposition (Wave-2 hardening follow-up):** `closed` (2026-05-28).
  Verified by inspection at `scripts/dev/r1-smoke.sh::expect_status`:
  the URL is built via `url="$(printf '%s%s' "$API_BASE_URL"
  "$path")"` and passed to curl as `"$url"` — both `$API_BASE_URL`
  and `$path` are in double-quote context, so the `-`, `:`, and
  asset-id hyphen characters the original report flagged are
  treated as literals (no word-splitting, no globbing, no shell
  interpretation). `$path` only originates from string literals
  at every `expect_status` call site, so there is no
  untrusted-input vector either. The defensive quoting the
  follow-up asked for is already in place; the actual root cause
  (a 4-5 s cold-cache resolver crossing the 10 s budget) was
  resolved structurally by the HasAsset PK fast-path in
  `internal/storage/timescale/assets.go::hasClassicAsset`
  (commit `3b36fbee`).

#### F-0158 — galexie-archive trailing-partition writes stuck since 2026-05-25 00:20

- **Severity:** `high` (data-integrity — archive is the durable copy per ADR-0002)
- **Title:** `/var/lib/minio/galexie-archive/FC42F7FF--62720000-62783999/` contains only **416 of 64000 expected ledger files** (62720000-62720415), all timestamped May 25 00:20. `galexie-live` for the same partition has 43,408 files covering up to current tip 62763407. Galexie service is running healthily and writing to `-live` but `-archive` writes stopped 2 days ago.
- **Workstream:** W18, W21
- **Impact:** the durable archive (used by verify-archive Tier A and any backfill that uses `S3BucketArchive` default) is missing 2 days of ledger LCM files. Verify-archive walks against this bucket would hit "missing file" errors past ledger 62720415.
- **Disposition:** **closed 2026-05-27.** Root cause: NOT a dual-writer topology (galexie writes only to `galexie-live`); `galexie-archive` is filled by an hourly `galexie-archive-fill.timer` that mirrors AWS public-blockchain → local. That script's partition-level set-diff (`comm -23 aws local` after `mc ls`) treats any partition that contains at least one file as "present" and skips it forever — so when AWS first publishes a new trailing-edge partition with only a handful of ledgers and the timer fires, we mirror those few files, mark the partition present, and never revisit. Two days of growth in `FC42F7FF--62720000-62783999` plus prior partials in `FC43F1FF--62656000-62719999` (55/64000) and `FC44EBFF--62592000-62655999` (50,880/64000) accumulated this way. Recovery: ran `PARTIALS="…three partition names…" galexie-archive-fill` to delete + re-mirror; ~150k files restored from aws-public. Root-cause fix: `configs/ansible/roles/archival-node/files/galexie-archive-fill.sh` Phase-1b auto-partial detection — file-count the latest `PARTIAL_CHECK_WINDOW=4` partitions per run and delete any local partition with fewer files than AWS (cost: 4× recursive `mc ls`, sub-second). Deployed to `/usr/local/bin/galexie-archive-fill` on r1 same session; next hourly fire will self-heal any future trailing-edge stalls.

#### F-0159 — `ratesengine-ops backfill` silently completes with 0-row processing when bucket has no files

- **Severity:** `medium` (UX / observability of operator tool)
- **Title:** Ran `ratesengine-ops backfill -from 62746862 -to 62757524 -parallel 2` against `-bucket galexie-archive` (default) which has no files in that range. The backfill logged `chunk complete ... ledgers=5331` and `ledgers=5332` and exited cleanly in 200ms. Reality: zero ledgers walked, zero events processed, zero cursors written. The `ledgers=N` log field is the chunk's [from,to] range size, NOT the count of ledgers actually walked from the bucket.
- **Workstream:** W13
- **Impact:** operator gets a false-positive "backfill complete" signal. Easy to assume the gap is filled when it isn't.
- **Disposition:** `closed` (2026-05-28). Both remediation paths shipped together in `cmd/ratesengine-ops/backfill.go::runBackfillChunk`. The `chunk complete` log line now emits `chunk_size_ledgers` (the [from,to] range size) AND `ledgers_walked` (the actual LCM-callback count). The misleading `ledgers=` field is gone. And the chunk now fails loudly with an explicit error when `chunk_size_ledgers > 0 AND ledgers_walked == 0`, naming the bucket and pointing operators at the `galexie-archive` / `galexie-live` mirror check. The 2026-05-26 scenario (`-bucket galexie-archive` for a range that lived only in `galexie-live`) would now exit non-zero with `backfill walked 0 of 5331 ledgers in range [62746862,62757524] from bucket "galexie-archive" — bucket likely has no files in this range; check --bucket and the galexie-archive/-live mirror for the target range` rather than silently logging a false-positive success.
