---
title: Runbook — archive-divergence
last_verified: 2026-05-03
status: draft
severity: P1
---

# Runbook — `stellarindex_stellar_archive_divergence`

> **Deployment posture (2026-04-30).** r1's `/srv/history-archive/`
> is a static mirror filled by a one-shot `stellar-archivist mirror`
> (completed) — there is no running publisher today
> ([r1-deployment-state.md](../r1-deployment-state.md)). The alert
> consumes `stellarindex_archive_divergence_total` written by
> `scripts/ops/archive-cross-check.sh`, which compares our mirror to
> reference archives on a schedule; the alert is **live** and can
> fire on r1 today. But because we are *not* publishing, root causes
> #2 (core-binary bug producing a different bucket) and the
> "stop advertising" mitigation step do not apply on the current
> posture — they are retained for Phase-3 (Tier-1 validator rollout
> per ADR-0004) when stellar-core resumes producing checkpoints.
> On r1 today, divergence almost always means **bit rot in the
> static mirror** (root cause #1) or — much rarer — a reference
> archive itself changed.

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_stellar_archive_divergence` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/stellar.yml` |
| Typical MTTR | hours |
| Impact | The hash of a checkpoint bucket we published differs from at least one other reference archive (SDF / LOBSTR / Satoshipay). This is a **correctness incident** — we've either got a bug or been compromised. Either way, consumers of our archive get different data from consumers of the reference. |

## Symptoms

- `stellarindex_archive_divergence_total > 0`. Fires immediately
  (`for: 0s`) — there's no such thing as a "transient" divergence.
- Our history-scanner job (runs per checkpoint) reports a hash
  mismatch against the set of reference archives it cross-checks.

## Quick diagnosis (≤ 5 min)

```sh
# Which checkpoint, which bucket, what's the mismatch?
# The verify-archive timer/service writes detail logs — retrieve via:
ssh root@r1 "journalctl -u verify-archive-tier-a -n 200 --no-pager"

# Pull the hash from our archive
mc cat myminio/history-archive/history/*/history-<checkpoint>.json | jq .currentBuckets

# Pull the same checkpoint from a reference
curl -s https://history.stellar.org/prd/core-live/core_live_001/history/.../history-<ckpt>.json | jq .currentBuckets

# Diff the bucket hashes — there'll be at least one row that
# differs. That's the corrupted / diverged bucket.
```

## Typical root causes (in order of how much you should worry)

1. **Bit rot / storage corruption.** ZFS scrubs should catch this
   before publish, but a drive silently returning bad data between
   scrub cycles could publish corrupt buckets.
   - Investigation: `zpool status -v` for any scrub errors on the
     archive storage.

2. **Core binary bug** — stellar-core produced a different bucket
   hash than the rest of the network. Should be near-impossible
   (deterministic spec) but has happened historically when we ran
   a custom patched core.
   - Investigation: are we running stock stellar-core from a
     tagged release?

3. **Compromised archive storage**. Someone (malicious or mistaken
   operator) overwrote our archive with wrong data. Check
   access logs + recent S3 ops.

4. **Scanner bug.** The scanner itself reports divergence when
   the truth is our archive is correct. Rare but possible —
   cross-check one of the reference archives against another to
   confirm it's us.

## Mitigation (urgent)

- [ ] Step 1 — **stop advertising the affected checkpoints** so
      downstream consumers stop relying on our data. Temporarily
      redirect validators.stellar.expert or whatever registry we
      publish to, noting a known-bad window.

- [ ] Step 2 — determine the extent. Is it one bucket or many?
      One checkpoint or several?

- [ ] Step 3 — if storage corruption: restore from a replica
      archive (we run three per ADR-0004). Use the other validators'
      archive as the source of truth.

- [ ] Step 4 — if core-binary bug: this is a sev-1 engineering
      incident. Contact SDF; coordinate with the broader core
      community; potentially do an emergency binary swap.

- [ ] Step 5 — if compromise: SECURITY incident. Rotate archive
      credentials, inspect access logs, engage security-ops. See
      SECURITY.md.

- [ ] Verification: our archive's bucket hashes match references
      for all recent checkpoints; scanner shows 0 divergence
      across a full sweep.

## Root cause analysis

- Forensic copy of the diverged bucket (DO NOT overwrite it until
  analysis is done).
- Hash chain: which checkpoint was the first to diverge? Work
  back to that point.
- Storage access logs from the archive backend.
- Core version + host state when the checkpoint was generated.

## Known false-positive patterns

Very few. The spec is deterministic; a divergence is almost
always real. But:

- **Scanner race with an in-flight publish** — if we scan the
  archive *during* a publish (rare but possible with bad scheduling),
  we may read a partial file. The scanner should retry; if it's
  alerting without retry, fix the scanner.
- **Reference archive is the one that's wrong.** Improbable
  (they're the source of truth) but if two of the three reference
  archives agree with us and only one disagrees, it might be
  them. Cross-verify before panicking.

## Related

- `archive-publish.md` — when we fail to publish at all.
- ADR-0004 (three-validator aspiration + independent archives).
- SECURITY.md — if compromise suspected.

## Changelog

- 2026-04-23 — initial draft. Urgency justified: this is a
  correctness guarantee we've explicitly committed to in ADR-0004.
- 2026-04-30 — top-of-file deployment-posture callout. r1 doesn't
  publish today (stellar-core removed 2026-04-23); the alert
  remains live via the cross-check script, but root causes /
  mitigation steps tied to a publishing core don't apply on the
  current posture. Retained for Phase-3 validator rollout.
