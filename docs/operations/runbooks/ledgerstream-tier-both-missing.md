---
title: Runbook — ledgerstream-tier-both-missing
last_verified: 2026-05-22
status: draft
severity: P1
---

# Runbook — `ratesengine_ledgerstream_tier_both_missing`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ledgerstream_tier_both_missing` |
| Severity | P1 (page) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/ledgerstream-tier.yml` |
| Typical MTTR | 10–30 min (rehydrate from AWS) up to a few hours (cross-region rebuild) |
| Impact | The indexer (or a backfill job) cannot read an LCM from EITHER tier — the affected cursor is stalled until the gap is filled. Customer-facing impact depends on which cursor: live-tip stall → API freshness lag growing; backfill stall → completing a historical range is blocked but live serving is unaffected. |

## Background — ADR-0027 in one paragraph

`internal/ledgerstream/tiered.go` reads each LCM from the local
`galexie-archive` MinIO bucket (hot) and, on a `NoSuchKey`, falls
back to the AWS public bucket
`s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/` (cold).
`both_missing` is when neither tier has the object. Pre-§3 of the
ADR-0027 rollout the cold tier is disabled entirely, so this alert
cannot fire; once tiering is enabled and the bulk trim has removed
old hot ranges, the cold tier is the only fallback.

## Symptoms

- Prometheus counter
  `ratesengine_ledgerstream_tier_read_total{outcome="both_missing"}`
  has increased in the last 5 min.
- The affected binary's logs show `cold read: ... not found` (or
  the AWS-SDK equivalent) for one or more ledger sequences.
- If the affected cursor is live ingest, the
  `ratesengine_indexer_ledger_lag_seconds` metric is climbing.
- If it's a backfill, the backfill range's progress has frozen
  (see `ratesengine-ops list-cursors`).

## Quick diagnosis (≤ 5 min)

Identify the failing ledger(s), which tier is at fault, and whether
AWS is reachable at all.

```sh
# 1. Which ledgers are missing — the indexer log line names them.
ssh r1 'journalctl -u ratesengine-indexer --since="10 min ago" | grep -iE "both.missing|tiered.*not.found" | tail -5'

# 2. Is the local hot tier intact for the affected range?
#    Replace SEQ with the ledger from step 1.
SEQ=...   # e.g. 23000000
ssh r1 "mc ls local/galexie-archive/$(printf 'FFFFFFFF--%d' $((4294967295 - SEQ / 64 * 64)))/ | head -5"

# 3. Is the AWS public bucket reachable from r1?
ssh r1 'curl -sf -m 10 https://aws-public-blockchain.s3.us-east-2.amazonaws.com/v1.1/stellar/ledgers/pubnet/ -I | head -3'

# 4. Cold-read latency in the last 30 min (background context for
#    whether AWS is generally slow vs flat-out unavailable).
ssh r1 'curl -s localhost:9100/metrics | grep ratesengine_ledgerstream_cold_read_duration_seconds_count'
```

## Decision tree

### A. AWS unreachable

Step 3 returns a non-2xx or times out. The AWS Open Data bucket is
either down or r1 has lost outbound HTTPS.

- **If r1 has lost outbound HTTPS**, page the on-call sysadmin to
  restore networking. Once restored, the indexer/backfill should
  retry automatically — the cursor isn't advanced past the failure.
- **If AWS Open Data is down**, this is rare but the ADR-0027
  "external dependency on AWS sponsorship" risk. Rehydrate from a
  peer region:
  ```sh
  # Pull the missing range from R2 or R3's mirror (R3 keeps
  # galexie-archive locally on Vultr Object Storage per ADR-0016).
  ratesengine-ops rehydrate-galexie-archive \
    --from <SEQ> --to <SEQ_END> \
    --source vultr   # or aws-r2, depending on region.
  ```
  Document the AWS outage window in `docs/operations/incidents/`.

### B. AWS reachable but the specific partition is missing

Step 3 succeeds; step 1 lists ledger sequences; AWS GET on those
partitions returns NoSuchKey. Most likely a recent partition that
hasn't propagated to the public bucket yet (the bucket's freshness
SLA is "~30 min behind tip").

- Wait 30 min and check again — most cases self-resolve.
- If sustained beyond 1 h, escalate to AWS Open Data via the
  contact in the bucket's `README.md`.

### C. The local hot tier was trimmed too aggressively

Step 2 confirms the local partition is gone; step 3 also fails.

- A trim job (`galexie-archive-trim`) deleted a range that wasn't
  yet safely in cold storage. This is the failure mode
  `--verify-upstream` is meant to prevent — check the trim log:
  ```sh
  ssh r1 'journalctl -u galexie-archive-trim.service --since="24 h ago" | tail -50'
  ```
- Rehydrate from a peer (see option A above), then **disable the
  trim timer** until the trim operator is fixed:
  ```sh
  ssh r1 'sudo systemctl disable --now galexie-archive-trim.timer'
  ```
  File a ticket against ratesengine-ops to add the missing safety.

### D. The indexer/backfill is mis-configured

Step 2 shows the partition is present locally and step 3 reaches
AWS, yet the metric increments. The binary is reading from the
wrong endpoint — its TOML points at a stale bucket or the AWS
config is missing.

- Check the binary's env / config for the right endpoints:
  ```sh
  ssh r1 'grep -E "s3_bucket|s3_endpoint|cold_tier" /etc/ratesengine.toml'
  ssh r1 'grep -E "AWS_|S3_" /etc/default/ratesengine'
  ```
- Restart the binary after fixing the config.

## Aftermath

Once the gap is filled, the counter stops incrementing and the
alert resolves on its own (no manual reset). Confirm the affected
cursor has resumed advancing:

```sh
ssh r1 'curl -s localhost:9100/metrics | grep -E "ratesengine_indexer_ledger_lag_seconds|ratesengine_backfill_cursor"'
```

## Related

- ADR-0027 — LCM cache tiering, the rollout sequence (§Sequencing).
- ADR-0016 — per-region storage strategy (R2 reads AWS direct; R3
  has its own Vultr mirror — both viable rehydration sources).
- `feedback_cold_tier_premature_enable` — bare §3 (enable tiering)
  without §4 (bulk trim) introduces this failure mode without
  benefit; the rollout always lands them together.
