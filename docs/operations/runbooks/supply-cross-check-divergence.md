---
title: Runbook — supply-cross-check-divergence
last_verified: 2026-04-28
status: draft
severity: P3
---

# Runbook — `ratesengine_supply_cross_check_divergence`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_supply_cross_check_divergence` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/supply.yml` |
| Typical MTTR | 1 – 4 hours (RCA-driven; not user-impacting on its own) |
| Impact | The asset's `total_supply` / `circulating_supply` / `market_cap_usd` / `fdv_usd` on `/v1/assets/{id}` will be wrong by the divergence amount until reconciled. Customer-visible only on the affected asset's detail page; aggregate price endpoints are unaffected. |

## Symptoms

- `ratesengine_supply_cross_check_divergence_stroops{classic_key="..."} > 1` for ≥ 5 min.
- The labelled `classic_key` identifies the affected asset (format `CODE:ISSUER`).
- `ratesengine_supply_cross_check_total{outcome="over"}` rate non-zero.

## Background — why both algorithms must agree

A SAC-wrapped classic asset is observable two ways:

1. **Algorithm 2 (classic):** total = Σ trustline + Σ claimable + Σ LP-reserve + Σ SAC-wrapped contract balance, all reconstructed from `TrustLineEntry` / `ClaimableBalanceEntry` / `LiquidityPoolEntry` / `ContractData` ledger meta.
2. **Algorithm 3 (SEP-41):** total = Σ mint − Σ burn − Σ clawback over the contract's lifetime, summed off the SAC contract's events.

Both observe the same underlying state. Honest indexer math may differ by 1 stroop due to NUMERIC truncation at write time. Anything larger means one indexer dropped events / mis-summed.

## Quick diagnosis (≤ 15 min)

```sh
# 1) Confirm the divergence is real and which asset.
curl -fs http://localhost:9464/metrics \
  | grep '^ratesengine_supply_cross_check_divergence_stroops' \
  | awk '$NF != "0" && $NF != "1"'

# 2) Look at both readings + their basis.
psql -d ratesengine -c \
  "SELECT asset_key, time, total_supply::text, basis, ledger_sequence
     FROM asset_supply_history
    WHERE asset_key IN ('USDC:GA5Z...', 'CCW6...')
    ORDER BY time DESC LIMIT 4;"
```

The SAC contract id for a classic asset is deterministic — derive it once and confirm it matches the row in `asset_supply_history` you'd expect. The aggregator orchestrator logs the pairing at INFO when it kicks off the cross-check.

Decision tree:

| Classic > SAC | Classic < SAC | Likely cause | Mitigation |
| ------------- | ------------- | ------------ | ---------- |
| Yes (1+ stroop) | — | Algorithm 3 missed mint events (rare — events are durable) | Replay the SAC contract's event range from Galexie; rerun Algorithm 3 |
| — | Yes (1+ stroop) | Algorithm 2 missed a trustline / claimable / LP entry change (more common — trustline-delta indexer is more recent code) | Replay the affected ledger range through the trustline-delta indexer; rerun Algorithm 2 |
| Both readings stale | Both readings stale | Aggregator orchestrator stalled; cross-check is comparing old data | Check `ratesengine_aggregator_silent` runbook first |

## Mitigation (≤ 60 min)

- [ ] **Identify which side is wrong** by manually computing the
      classic-side total against current ledger meta:
      ```sh
      ratesengine-ops supply audit --asset CODE:ISSUER --ledger <recent>
      ```
      The output prints both algorithms' running sums alongside the
      raw ledger-entry counts they were derived from. The side that
      doesn't match the manual count is the corrupt indexer.

- [ ] **Replay the affected range.** For Algorithm 2 issues:
      `ratesengine-ops backfill --source classic-supply --from <ledger-N> --to <ledger-N+1000>`
      For Algorithm 3 issues:
      `ratesengine-ops backfill --source sep41-events --contract C... --from <ledger-N> --to <ledger-N+1000>`
      (TODO: these subcommands ship with L2.12 PR 6.)

- [ ] **Verify** the divergence gauge drops below 2 within 10 min of
      the replay completing. The gauge updates once per aggregator
      tick; allow ≤ 60 s post-replay before considering the alert
      stale.

- [ ] **Pause** publishing of the affected asset's `/v1/assets/{id}`
      F2 fields if the divergence is large enough to materially
      mislead consumers (>0.1% of total). Set the asset's
      `supply_basis` to `no_metadata` via the supply-policy YAML
      override; redeploy the API; the F2 fields then surface as
      `null`.

## Root cause analysis

Capture for the postmortem:

- The first ledger at which the two readings diverged. (Walk
  `asset_supply_history` backward from the alert-firing time.)
- The replay-range commands you ran + the divergence-after value.
- Which indexer was at fault (Algorithm 2's trustline-delta vs.
  Algorithm 3's event-sum).
- If the corruption was caused by a recent code change: the PR diff
  + the audit log.

## Known false-positive patterns

- **First 5 minutes after a new asset's SAC is deployed**: the two
  indexers ingest the deployment slightly out of sync. Normally
  resolves within a single aggregator tick. The `for: 5m` clause on
  the alert covers this.
- **Backfill catch-up**: if Algorithm 2 is replaying a historical
  range while Algorithm 3 has already advanced past that range,
  divergence reads as the catch-up gap. Suppress the alert during
  active backfills (operator action) — the gauge label is
  per-asset, so you can `ALERTMANAGER silence` just the affected
  `classic_key`.
- **Clock skew between processes**: if the cross-checker's
  Algorithm-2 read happens at ledger N and Algorithm-3 read at
  ledger N+1, a fresh mint between them looks like divergence. The
  aggregator orchestrator pins both reads to the same ledger
  boundary; a regression that breaks that pinning would surface as
  a chronic 1-stroop noise floor.

## Related

- ADR-0011 §"SAC-wrapped classics — both algorithms must agree" —
  the policy this runbook implements.
- `aggregator-silent.md` — if the orchestrator is stalled, the
  cross-check gauge is also stale; investigate that first.
- `internal/supply/crosscheck.go` — the comparison code; any tolerance
  change must update this runbook + ADR-0011.

## Changelog

- 2026-04-28 — initial draft alongside the cross-check landing PR
  (L2.12 PR 5).
