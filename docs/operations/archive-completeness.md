---
title: Archive completeness — invariants, bootstrap, daily cron
last_verified: 2026-06-12
status: living procedure
---

# Archive completeness — invariants, bootstrap, daily cron

Operational companion to [ADR-0017](../adr/0017-archive-completeness-invariants.md)
(the policy decision). This doc covers:

- The two physical archives we maintain and what each one is for
- Per-region behaviour (R1 enforces; R2/R3 delegate)
- Bootstrap procedure (the one-shot historical fill)
- Daily completeness cron (the current steady-state guardrail)
- Multi-source fallback chain
- Prometheus metrics + status-page integration
- The `stellarindex-ops archive-completeness` tool

The implementation lives in `cmd/stellarindex-ops/` (the
`archive-completeness` subcommand) plus the existing
`galexie-archive-fill` and `refetch-history-archive` shell scripts
in `/usr/local/bin/`.

Important scope note for this repo snapshot: the shipped
`archive-completeness` command enforces and repairs the
**cross-anchor** archive only. The broader ADR-0017 four-contract
model remains the target architecture, but the current green path is
not proof that the primary `galexie-archive` checks have run.

## The two archives

### Primary — `galexie-archive/` MinIO bucket (R1)

Per-ledger XDR meta files. One file per ledger, ~62 M objects
covering pubnet history. Layout:

```
galexie-archive/
└── <HASH>--<N>-<N+63999>/         (partition, 64,000 ledgers)
    ├── <HEX>--<N+63999>.xdr.zst   (latest ledger in partition)
    ├── <HEX>--<N+63998>.xdr.zst
    ├── ...
    └── <HEX>--<N>.xdr.zst         (earliest ledger in partition)
```

This is the **source of rate data** — the indexer reads from here
to populate Timescale. Source: AWS public-blockchain S3
(`s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`),
populated via `mc mirror` and the `galexie-archive-fill` script.

### Cross-anchor — `/srv/history-archive/` (R1)

Traditional Stellar history archive in canonical layout:

```
/srv/history-archive/
├── bucket/        (state snapshots, ~256 hex-prefix subdirs)
├── history/       (checkpoint summary JSONs)
├── ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz   ← what verify-archive reads
├── results/
├── scp/
├── transactions/
└── .well-known/
```

Each `ledger-<hex>.xdr.gz` carries 64 `LedgerHeaderHistoryEntry`
records (one per ledger in the checkpoint window). This is the
**verification anchor** — `stellarindex-ops verify-archive -tier
checkpoint` reads each checkpoint's signed hash here and compares
against our LCM-derived hash to prove our primary archive matches
SDF's canonical view.

Source: `https://history.stellar.org/prd/core-live/core_live_001`
(SDF's primary published archive). Mirrored via `stellar-archivist`
or `mc mirror` at one-shot bootstrap time; refreshed by the daily
cron.

## Per-region behaviour

Per [ADR-0016](../adr/0016-per-region-storage-strategy.md) the
three regions have asymmetric storage shapes. Completeness
enforcement reflects that.

### R1 Frankfurt — integrity leader

R1 holds both archives locally. In the current implementation it runs
the cross-anchor portion of the daily control and serves as the fleet's
trust anchor for that narrower check set.

```
stellarindex-ops archive-completeness verify -from 2 -to 0 -workers 8
  ↓
  ├─ implemented today: stat each expected /srv/history-archive/ledger-*.xdr.gz
  └─ implemented today: fetch/repair missing cross-anchor checkpoint files
```

(`-to 0` resolves the tip from the live ledgerstream cursor. The real
flag set is `-archive-root`, `-from`, `-to`, `-workers`,
`-owner-user`, `-owner-group`, `-output-file`, `-textfile-output` —
see `cmd/stellarindex-ops/main.go::archiveCompletenessVerify` and
`deploy/systemd/archive-completeness.service` for the exact
invocation. There is no `-range`/`-checks`/`-trust-leader` flag; the
range-keyword and per-check selectors below describe the *target*
ADR-0017 design, not the shipped command.)

On any cross-anchor failure: attempt repair via the multi-source
fallback chain (below). If repair succeeds, exit clean. If repair
fails, exit non-zero and emit a Prometheus alert.

### R2 US-East — AWS hybrid, trusts R1

R2 doesn't store either archive locally — the indexer streams
galexie data direct from `aws-public-blockchain` S3, and there's no
cross-anchor archive on R2 disks at all. R2's local cron is
narrower:

```
# TARGET DESIGN (not shipped) — the -checks/-trust-leader flags do not
# exist on the current binary. R2/R3 are deferred; today R1 is the only
# host and runs the cross-anchor `verify` above.
stellarindex-ops archive-completeness verify -checks chain-link,multi-peer \
  -trust-leader r1
```

The ADR describes two checks that should run locally:

- **Chain-link (contract 2 only):** walk yesterday's ledgers
  through galexie's S3 reader and confirm `prev.LedgerHash ==
  curr.PreviousLedgerHash`. Catches AWS bucket bit-flips and any
  ingest-side corruption regardless of upstream trust.
- **Multi-peer (Tier D):** sample 20 checkpoints from yesterday's
  range, fetch each from 6 tier-1 validator archives, verify the
  hashes agree. Catches forks (chain internally consistent but
  doesn't match the network's signed reality).

In the current implementation, contract delegation is narrower: query
R1's Prometheus `archive_completeness_last_success_timestamp`
gauge over the metrics endpoint. If R1's last clean run is older
than **26 hours**, R2 marks itself *reduced redundancy* and sets
the API's `ReducedRedundancy` envelope flag on every response.

### R3 Singapore — Vultr hybrid, trusts R1

R3 has the primary archive locally on Vultr Object Storage but no
cross-anchor archive. Same shape as R2 with one addition:

```
# TARGET DESIGN (not shipped) — same caveat as R2 above.
stellarindex-ops archive-completeness verify \
  -checks structural,chain-link,multi-peer \
  -trust-leader r1
```

R3's target design includes contract 1 (structural) on its own local
primary copy because Vultr Object Storage failures are local to R3.
The shipped `archive-completeness` implementation still only enforces
the cross-anchor archive path.

If Vultr Object Storage drops files, R3's local structural check
catches it within 24 h. The repair path is `mc mirror` from R1's
MinIO over the WAN — slower (165 ms RTT to Frankfurt) but
correctness-preserving.

## Bootstrap procedure (one-shot)

Required before the daily cron can begin enforcing the invariants
on R1. Existing R1 gaps as of 2026-04-27:

- `galexie-archive/`: confirmed gap of ~35,000 contiguous ledger
  files in partition `FD9DA5FF--40000000-40063999/`; surrounding
  partitions 40M–40.5M are 22K–35K files short. Total to be
  determined when `galexie detect-gaps` finishes the full-range
  scan.
- `/srv/history-archive/`: ~6,782 of 972,652 expected
  `ledger-<hex>.xdr.gz` files missing (verifier reported
  `checkpointsMissed=6273` over a partial range; full count
  determined by enumeration).

### Steps

1. **Diagnose primary gaps**

   ```sh
   AWS_ACCESS_KEY_ID=stellarindex-admin \
   AWS_SECRET_ACCESS_KEY=<...> \
   AWS_ENDPOINT_URL=http://127.0.0.1:9000 \
     galexie detect-gaps \
       --config-file /etc/galexie/galexie-backfill.toml \
       --start 2 --end <network_head> \
       --output-file /var/lib/galexie/detect-gaps.json
   ```

   Wall-clock: ~2.5 h on r1 (32 parallel workers, 100K ledgers each).

2. **Group missing ledgers into partitions**

   ```sh
   jq -r '.gaps[] | "\(.start) \(.end)"' /var/lib/galexie/detect-gaps.json \
     | awk '{for(i=$1;i<=$2;i++) printf "%d\n", int(i/64000)*64000}' \
     | sort -un \
     > /tmp/partials-starts.txt
   ```

   Map each starting ledger to the partition's hex-prefix name (script
   `partition-name-from-ledger.sh`, planned).

3. **Run `galexie-archive-fill` with `PARTIALS=`**

   ```sh
   PARTIALS="$(cat /tmp/partials.txt)" galexie-archive-fill
   ```

   The script deletes each named partition then re-mirrors it from AWS
   in parallel (8 workers default; tunable via `PARALLEL` env). See
   [galexie-backfill.md "mc mirror gotcha"](galexie-backfill.md#mc-mirror-gotcha-overwritefalse-doesnt-mean-what-it-says)
   for the failure mode this works around.

4. **Diagnose + fill cross-anchor gaps**

   The shipped command does not have separate `check`/`fix` modes —
   `archive-completeness verify` performs the cross-anchor check AND
   fills any missing checkpoints in one pass (Phase 1 check → Phase 2
   fill via the fallback chain). Run it over the full range and write
   the JSON report:

   ```sh
   stellarindex-ops archive-completeness verify \
     -from 2 -to 0 -workers 16 \
     -output-file /var/lib/galexie/cross-anchor-gaps.json
   ```

   For each missing checkpoint it tries `core_live_001` →
   `core_live_002` → `core_live_003` → tier-1 validator archives.
   ~6,782 files at ~100 ms each = ~12 min serial, ~1 min wall-clock
   at 16-way parallel. The exit code is 0 if no residual files remain
   after the fill, 1 if the fallback chain was exhausted on some.

5. **Run end-to-end verify with hardened defaults**

   ```sh
   stellarindex-ops verify-archive -config /etc/stellarindex.toml \
     -tier all -from 2 -to <network_head> \
     -fail-on-missed
   ```

   `-fail-on-missed` (added in PR D, see ADR-0017) treats
   `checkpointsMissed > 0` as a hard failure. Must exit 0 before the
   daily cron is enabled.

6. **Enable the daily timer**

   ```sh
   systemctl enable --now archive-completeness.timer
   ```

After step 6, the system carries the invariants forward.

## Daily completeness cron (steady state)

### What runs

`/etc/systemd/system/archive-completeness.timer`:

```ini
[Timer]
OnCalendar=*-*-* 02:17:00 UTC
Persistent=true
RandomizedDelaySec=300
```

The :17 minute and the 5-minute jitter avoid AWS S3 thundering-herd
patterns and spread the three regions' runs apart from each other.

The timer fires
`/etc/systemd/system/archive-completeness.service`, which runs (see
`deploy/systemd/archive-completeness.service` for the canonical unit):

```sh
stellarindex-ops archive-completeness verify \
  -from 2 -to 0 -workers 8 \
  -textfile-output /var/lib/node_exporter/textfile_collector/archive_completeness.prom \
  -output-file /var/lib/galexie/last-completeness-report.json
```

### Wall-clock budget per run

| Step | Operation | Time |
|---|---|---|
| 1 | LIST yesterday's primary partitions; compare against expected count | ~5 s |
| 2 | Stat each expected cross-anchor checkpoint file | ~1 s |
| 3 | Fetch any missing primary files from AWS | ~50 ms/file |
| 4 | Fetch any missing cross-anchor files via fallback chain | ~100 ms/file |
| 5 | Chain-link walk yesterday's range (Tier A) | ~30 s |
| 6 | Cross-anchor verify yesterday's range (Tier B) | ~30 s |
| 7 | Emit Prometheus gauges, exit | <1 s |

**Clean day:** ~70 s total. **Bad day with 100 missing files:**
~2–5 min. Either fits comfortably inside the 26-h staleness budget
that R2 + R3 use to decide their `ReducedRedundancy` flag.

## Multi-source fallback chain

For each missing file the daemon tries sources in order, falling
through on 404:

```
1. AWS public-blockchain S3       (primary archive only)
   s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/

2. SDF core_live_001              (cross-anchor archive only)
   https://history.stellar.org/prd/core-live/core_live_001/

3. SDF core_live_002              (cross-anchor archive only)
   https://history.stellar.org/prd/core-live/core_live_002/

4. SDF core_live_003              (cross-anchor archive only)
   https://history.stellar.org/prd/core-live/core_live_003/

5. tier-1 validator archives      (both archives, round-robin)
   bootes-history.publicnode.org
   archive.v1/v2/v5.stellar.lobstr.co
   stellar-history-{usc,ins,usw}.franklintempleton.com
   stellar-history-{de-fra,sg-sin,us-iowa}.satoshipay.io
   alpha-history.validator.stellar.creit.tech

6. galexie scan-and-fill          (heavy fallback, primary only)
   replays via captive-core when no public archive has the file
```

Layers 1–5 are fast HTTP fetches (~100 ms each). Layer 6 is the
heavy fallback — captive-core replay at ~200–1,000 ledgers/sec —
used only when every public archive is missing the same file.

Per-source success and failure are tracked in
`archive_completeness_repair_attempts_total` /
`archive_completeness_repair_failures_total` so a degrading source
shows up in dashboards before it becomes the primary problem.

## Prometheus surface

```
archive_files_missing{archive="galexie-archive"}     gauge
archive_files_missing{archive="cross-anchor"}        gauge
archive_completeness_runs_total                      counter
archive_completeness_run_duration_seconds            histogram
archive_completeness_repair_attempts_total{source="aws|sdf|tier1|galexie-replay"} counter
archive_completeness_repair_failures_total{source="..."} counter
archive_completeness_last_success_timestamp           gauge
```

R2 and R3 each emit their own copies (per region). They also scrape
R1's `archive_completeness_last_success_timestamp` over the
internal metrics endpoint to drive the `ReducedRedundancy` decision.

### Alert rules

Defined in `deploy/monitoring/rules/archive-completeness.yml` per
[alerts-catalog.md](alerts-catalog.md):

| Alert | Threshold | Severity | Runbook |
|---|---|---|---|
| `stellarindex_archive_files_missing` | gauge > 0 for 4h on either archive | P2 | [archive-files-missing](runbooks/archive-files-missing.md) |
| `stellarindex_archive_completeness_stale` | last_success_timestamp older than 26h | P2 | [archive-completeness-stale](runbooks/archive-completeness-stale.md) |
| `stellarindex_archive_repair_source_degraded` | repair_failures / repair_attempts > 0.10 over 1h per source | P3 | [archive-repair-source-degraded](runbooks/archive-repair-source-degraded.md) |

## Status-page integration

Public status page at `https://stellarindex.io/status` (planned per
[sev-playbook.md §5.3](sev-playbook.md#53-public-status-page)).

Two status-page indicators tied to archive completeness:

1. **Component: "Historical data integrity"**
   - **Operational** — all three regions' last successful run is
     within 26 h AND `archive_files_missing == 0` everywhere.
   - **Degraded performance** — any region's last successful run
     is 26–48 h old, OR `archive_files_missing > 0` on a non-leader
     region.
   - **Partial outage** — R1's last successful run is older than
     48 h, OR `archive_files_missing > 100` on R1 (the integrity
     leader).
   - **Major outage** — R1 hasn't completed a successful run in
     7 days (cross-anchor verification is dead).

2. **Envelope flag exposure** — when status is *Degraded* or worse,
   the API sets `flags.reduced_redundancy = true` on every response
   (per ADR-0017). Customers monitoring this flag programmatically
   see the same signal humans see on the status page.

The status-page state is computed by a small worker that scrapes
the three regions' Prometheus federations every 60 s; transitions
between states require ≥ 2 consecutive samples to avoid flapping.
The worker also writes a short human-readable note ("R1 archive
backfill in progress, ETA 18:00 UTC") that operators can edit
during incidents — see [sev-playbook.md §5.4](sev-playbook.md#54-what-we-do-not-say)
for the comms policy.

## Tool reference

The shipped command is a single `verify` mode that checks the
cross-anchor archive and fills any missing checkpoints in the same
run (Phase 1 check → Phase 2 fill via the fallback chain). The
`check`/`fix` mode split and the `-range`/`-checks`/`-trust-leader`
flags below the line are part of the **target** ADR-0017 design and
are NOT implemented today.

```
USAGE
  stellarindex-ops archive-completeness verify [flags]

FLAGS (shipped)
  -archive-root PATH    cross-anchor archive root (default
                        /srv/history-archive)
  -from N               first ledger sequence, inclusive (default 2)
  -to N                 last ledger sequence, inclusive; REQUIRED.
                        0 = resolve the tip from the live cursor
  -workers N            parallel fetch workers (default 8)
  -owner-user USER      file owner for placed files (default stellar)
  -owner-group GROUP    file group for placed files (default stellar)
  -output-file PATH     write JSON gap report here (empty = stdout)
  -textfile-output PATH write a node_exporter textfile here
                        (empty = no metrics emit)

EXIT CODES
  0   clean — no missing files after the fill pass
  1   residual missing files (fallback chain exhausted some)
  other  I/O error
```

The reference implementation is
`cmd/stellarindex-ops/main.go::archiveCompletenessVerify` +
`internal/archivecompleteness/`.

## Cross-references

- [ADR-0017](../adr/0017-archive-completeness-invariants.md) — the
  policy decision (4 hard contracts, per-region trust model).
- [ADR-0016](../adr/0016-per-region-storage-strategy.md) — the
  per-region storage shapes this doc operationalises.
- [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) — the
  closed-bucket API contract that the completeness invariants
  protect.
- [galexie-backfill.md](galexie-backfill.md) — the original
  backfill procedure; the bootstrap reuses its `galexie-archive-fill`
  script for primary repair.
- [alerts-catalog.md](alerts-catalog.md) — the
  `stellarindex_archive_*` alert family.
- [sev-playbook.md](sev-playbook.md) — incident-response process;
  this doc's status-page section follows §5.3's conventions.
