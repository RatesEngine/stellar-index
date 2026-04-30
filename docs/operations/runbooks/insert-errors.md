---
title: Runbook — insert-errors
last_verified: 2026-04-30
status: draft
severity: P2
---

# Runbook — `ratesengine_ingestion_insert_errors`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_insert_errors` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | 15–60 min |
| Impact | Trades / oracle updates silently dropped — downstream price staleness, visible to API clients as stale prices. No rollback; the events are lost. |

## Symptoms

- `ratesengine_source_insert_errors_total{source=...,kind=trade|oracle}` rises above 6/min sustained.
- `ratesengine_source_events_total` may still rise — the consumer is pulling events, it's the writer that's failing.
- Dashboard view: *Ingestion → Insert errors* panel non-zero for > 5 min.
- The offending source's `ratesengine_source_last_event_unix` may freeze (if persistence blocks until retry).

## Quick diagnosis (≤ 5 min)

```sh
# Which source + which kind is failing?
curl -s http://api:9464/metrics |
  grep ratesengine_source_insert_errors_total

# Is it the storage layer itself?
ratesengine-ops rpc-probe https://mainnet.sorobanrpc.com   # rules out upstream — r1 has no local stellar-rpc, point at a public endpoint
kubectl exec -it timescale-0 -- psql -c "SELECT now(), pg_is_in_recovery();"

# Actual failure reason is in the indexer's logs:
kubectl logs deploy/ratesengine-indexer | grep "insert trade failed\|insert oracle update failed" | tail
```

If the log line says:
- `connection refused` → Timescale is down or network partitioned. Jump to `timescale-primary-down.md`.
- `disk full` / `no space` → Timescale volume out of space. Scale PVC or evict old chunks.
- `duplicate key value` → should be impossible; the idempotent ON CONFLICT swallows these. If you see this, the primary-key invariant is broken and this is a data-integrity incident, not a capacity one. Escalate.
- `violates check constraint` → a source sent malformed data (negative amounts, bad tx_hash). Decode bug, not a storage bug; check `ratesengine_source_decode_errors_total` on the same source.

## Mitigation (≤ 15 min)

Events that fail here are LOST (we have no retry queue today — see the alert annotation). Prioritise:

- [ ] Step 1 — stop the bleeding. If Timescale is the root cause, follow `timescale-primary-down.md` first; insert errors are a symptom.
- [ ] Step 2 — if disk-full: extend the PVC (see `deploy/k8s/` templates) and let the indexer auto-retry. Backfill the gap afterwards:
  ```sh
  ratesengine-ops detect-gaps -config /etc/ratesengine/config.toml -threshold 50
  # if any source lags, run backfill for the affected range (TODO(#0) —
  # backfill subcommand lands later; until then, operator manually
  # extends ingestion.backfill_from_ledger and restarts the indexer)
  ```
- [ ] Step 3 — if the underlying issue is fixed: watch the rate decline. Alert clears when the 5m rate drops below 0.1/s.
- [ ] Verification: `ratesengine_source_insert_errors_total` stops incrementing (use `rate()[1m]` to see it go to 0).

## Root cause analysis

For the postmortem, gather:
- Timescale logs from `/var/log/postgresql/` covering the affected window.
- `ratesengine_source_insert_errors_total` series by `(source, kind)` over the incident.
- Indexer stderr with the full error strings (Timescale wraps them verbosely).
- Did the alert fire for ONE source or ALL? One-source = decode/schema issue; all-sources = shared storage issue.

## Known false-positive patterns

- None yet. This alert's threshold (6/min sustained 5 min) was chosen to ride through a 1-commit-at-a-time restart; we haven't seen legitimate brief spikes.

## Related

- `timescale-primary-down.md` — root cause when the shared storage layer is the issue.
- `decode-errors.md` — decode failures look similar but are upstream (source-side).
- ADR-0003 (i128 precision) — check-constraint violations here indicate a decoder is sending values that violate NUMERIC bounds.

## Changelog

- 2026-04-23 — initial draft after the `SourceInsertErrorsTotal` alert landed.
- 2026-04-30 — rpc-probe URL points at a public stellar-rpc; r1
  doesn't run its own (removed 2026-04-23).
