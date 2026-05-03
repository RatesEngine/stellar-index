---
title: Rollback procedures
last_verified: 2026-05-03
status: operator runbook
---

# Rollback procedures

What to do when a launch — or any subsequent release — needs to
be reverted. Per
[`release-process.md`](release-process.md#post-flight) the
default escalation is "any first-hour alert ⇒ SEV-2 minimum",
but rollback is a **separate** decision from incident response —
this doc covers the reversal mechanics.

The principle: **roll back fast, write the postmortem after.**
A wrong rollback is recoverable; a slow rollback that lets a
broken release accumulate state isn't.

## Decision tree — should we roll back?

```
                    ┌─ p99 latency > 1s sustained 5 min
                    │
Customer impact?    ├─ price returned with confidence < 0.05 for popular pair
                    │
                    ├─ /v1/healthz returns 5xx for > 60s
                    │
                    └─ ALL OF: any non-2xx > 1% rate, sustained 2 min
                              ↓
                        YES → ROLL BACK
                              (then file SEV-1 + open postmortem)

       ┌─ Single-component degraded (e.g. one source dropped)
       │
       ├─ Latency ≤ 500ms p95
       │
       └─ Documented graceful-degradation path engaged
                              ↓
                        NO → DO NOT ROLL BACK
                              (file SEV-2 + diagnose forward)
```

If unsure, **roll back**. The cost of an unnecessary rollback
is one extra release tag; the cost of letting bad data
accumulate is corrupted history that has to be backfilled-
or-truncated later.

## Failure-mode triage

### A. The release didn't take

Symptoms: API binary won't start, indexer panics on boot,
aggregator won't connect to Redis.

Diagnosis is fast — `systemctl status ratesengine-{api,indexer,
aggregator}` on each region. If the new release crashes at
startup, it never served real traffic; rollback is just
re-pinning the previous tag.

```sh
# Per region:
ansible-playbook -i inventory/r1.yml deploy/ansible/roles/api/version-pin.yml \
  --extra-vars 'release_tag=YYYY.MM.DD.<previous>'
# Watch the unit until it's active (running):
ssh r1 "systemctl status ratesengine-api"
```

Skip directly to **§Post-rollback** below.

### B. The release runs but breaks `/v1/price` correctness

Symptoms: prices reading wrong values (ratio inverted, peg
expansion off, FX leg unsnapped), confidence scores
collapsing to zero, freeze flags fired everywhere.

Highest-priority rollback. Bad data accumulates in the trades
hypertable + the CAGGs every minute the broken release runs.

```sh
# 1. Stop the aggregator on every region — preserves the cache
#    in its last-good state.
for region in r1 r2 r3; do
  ssh $region "systemctl stop ratesengine-aggregator"
done

# 2. Re-pin all three binaries to the previous tag, restart.
for region in r1 r2 r3; do
  ansible-playbook -i inventory/${region}.yml \
    deploy/ansible/roles/api/version-pin.yml \
    --extra-vars "release_tag=YYYY.MM.DD.<previous>"
done

# 3. Restart the aggregator after the binaries are re-pinned.
for region in r1 r2 r3; do
  ssh $region "systemctl start ratesengine-aggregator"
done

# 4. Smoke-check.
ratesengine-sla-probe -base-url https://api.ratesengine.net/v1 \
  -duration 30s -concurrency 4
```

If any rows landed in the trades hypertable from the broken
release, decide post-rollback whether to truncate or leave —
typically the broken decoder produced *missing* data rather
than *wrong* data, so leave-and-backfill is the cheap
recovery. The trades schema's `(source, ledger, tx_hash,
op_index)` primary key prevents duplicate inserts on
re-ingest.

### C. The release runs but a single source is broken

Symptoms: `ratesengine_source_decode_errors_total{source="X"}`
spiking; `ratesengine_source_events_total{source="X"}` dropping
to zero; the `decode-errors` runbook fires.

DON'T roll back the whole release. Instead disable just the
broken source via config:

```sh
# /etc/ratesengine/indexer.toml
[sources.<broken-source>]
enabled = false
```

```sh
ansible-playbook -i inventory/all deploy/ansible/roles/indexer/reload.yml
```

Then file a SEV-2 against the broken source's package. The
release stands; the source enters degraded mode. Re-enable
once the fix lands.

### D. Public-flip went wrong

Symptoms: public repo content doesn't match private; orphan-
branch initial commit had unintended files; secrets accidentally
included; license/CONTRIBUTING/etc. headers wrong.

Public-repo rollback is a `git push --force` to an empty repo
or — safer — delete the public repo entirely and re-create.
Either way, do NOT touch the private repo (it's the source of
truth).

```sh
# OPTION A — repo is empty enough that nobody cloned it:
gh repo delete RatesEngine/rates-engine --yes
# Then re-do the cut-over per public-flip.md from step 5.

# OPTION B — repo has been observed (someone might have cloned):
# Force-push a corrected initial commit. Coordinate with anyone
# who already cloned to re-pull.
git push origin +public-v1:main
```

Per [`public-flip.md`](public-flip.md), the
`git clone --no-local --no-hardlinks` step makes Option A
genuinely safe — the private repo is untouched.

### E. Status page misbehaving

Symptoms: Upptime is reporting components "down" when
production is fine, or vice versa.

Lowest-stakes rollback. The status page is a derived view; it
doesn't affect production traffic. Either fix the
`.upptimerc.yml` (e.g. probe URL was wrong, expected status
codes mismatched) and let the next probe cycle correct it, or
manually edit the issue Upptime created and resolve it.

If the status page is fundamentally broken and can't be
corrected within the SEV-2 detection window:

```sh
# DNS revert — point status. directly at GitHub Pages of a
# known-good prior commit, or to a temporary maintenance page.
# Cloudflare → DNS → status → (pause proxy + edit target).
```

## Post-rollback

After any rollback above:

1. **Confirm rollback took.** Re-run the SLA probe; verify the
   per-pair freshness gauges return to nominal.
2. **File the SEV.**
   - Title: `SEV-1: <YYYY.MM.DD.N> rolled back due to <symptom>`
   - Body: which decision-tree branch fired; what the rollback
     command was; current state.
3. **Customer comms.** If the broken release was live for any
   non-trivial window, send a follow-up to the launch-day comm
   thread. Honest is better than apologetic — say what was
   wrong, what was rolled back, what the customer-visible
   impact was.
4. **Open the postmortem.** Same template as any other SEV-1.
   Bias toward writing it the same day; details fade.
5. **Block forward releases.** Until the postmortem identifies
   the root cause and a fix has landed + been re-tested,
   pause the release-cut cadence. A second cut on top of an
   un-fixed problem is a force-multiplier on the original
   incident.

## Cross-references

- [`launch-day-checklist.md`](launch-day-checklist.md) — the
  cut-over runbook this rollback procedure protects.
- [`release-process.md`](release-process.md) — the per-release
  procedure; §Post-flight has the rollback one-liner this doc
  expands on.
- [`sev-playbook.md`](sev-playbook.md) — incident escalation
  for the SEV file step.
- [`public-flip.md`](public-flip.md) — public-repo cut-over
  mechanics; rollback shape D references this.
- [`docs/operations/postmortems/`](postmortems/) — where the
  postmortem lands after the dust settles.
