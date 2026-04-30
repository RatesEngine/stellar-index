---
title: Runbook — rpc-lag
last_verified: 2026-04-30
status: draft
severity: P2
---

# Runbook — `ratesengine_stellar_rpc_lag`

> **Deployment posture (2026-04-30).** stellar-rpc is **not running
> on r1** — the daemon was removed 2026-04-23
> ([r1-deployment-state.md §Services](../r1-deployment-state.md)).
> The metric `ratesengine_stellar_rpc_latest_ledger_age_seconds`
> has no producer, so this alert is *inert* on r1.
>
> Production ingest reads Galexie's MinIO output directly via
> `go-stellar-sdk/ingest.ApplyLedgerMetadata`
> ([architecture/ingest-pipeline.md](../../architecture/ingest-pipeline.md));
> stellar-rpc is preserved only for the `rpc-probe` operator
> diagnostic and for fixture capture in `scripts/dev/`. The alert
> remains in `deploy/monitoring/rules/stellar.yml` for revival when
> a customer-facing RPC façade is on the roadmap (none today). If
> this alert reaches you on r1 it indicates a Prometheus
> misconfiguration, not an upstream lag.

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_stellar_rpc_lag` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/stellar.yml` |
| Typical MTTR | 5–30 min |
| Impact | Every indexing source reads from stellar-rpc. When the RPC's `latestLedger` lags wall-clock, our entire ingestion pipeline falls behind — affects the freshness of every API-served price. |

## Symptoms

- `ratesengine_stellar_rpc_latest_ledger_age_seconds > 300` sustained 5 min.
- Upstream: all `ratesengine_source_lag_ledgers` climb together — the hallmark of a shared-upstream issue (vs single-source).
- API `/v1/readyz` may still return 200 (we only probe Timescale + Redis there, not the RPC).

## Quick diagnosis (≤ 5 min)

```sh
# Direct probe — version + latestLedger + event retention
ratesengine-ops rpc-probe http://stellar-rpc:8000

# Check the RPC's own self-reported health. It returns an
# error envelope when stale rather than HTTP error:
curl -s -XPOST http://stellar-rpc:8000 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}' | jq

# Is the RPC's captive-core process alive + caught up?
# (when running via docker-compose / k8s)
kubectl logs -f stellar-rpc | tail -50
```

Key signals:
- `rpc-probe` reports `health: ⚠ latency ... is too high (>30s)` → RPC's captive-core is catching up. Wait or investigate captive-core logs.
- `rpc-probe` reports connection refused / times out → RPC process is dead. Check the orchestrator / restart.
- `rpc-probe` returns `latestLedger` that equals the tip on stellar.expert → RPC is fine, something else is wrong. Go back to `source-stopped.md`.
- `rpc-probe` returns lagging `latestLedger` but health=OK → possible clock skew on the RPC host. Compare with `date` on the host.

## Typical root causes

1. **captive-core catching up after a restart**. Usually resolves on its own within 10–30 min. Stellar-core replays from the last ledger header it has; fresh boots or volume loss force a full catchup.

2. **Network partition between RPC host and peer validators**. captive-core can't advance ledgers it hasn't heard about.

3. **Host resource exhaustion**. captive-core is CPU-bound on replay + memory-bound at steady state. OOM or CPU throttle stalls it.

4. **Our own dep is behind**. The upstream stellar-rpc project ships breaking changes quarterly. Version drift shows up first as subtle lag, then total failure. `rpc-probe` prints the version — compare to the upstream's current mainline.

## Mitigation

- [ ] Step 1 — identify the root cause via the probes above.
- [ ] Step 2 — if captive-core catching up: wait. Inform stakeholders the API's price-freshness SLA is in a degraded window.
- [ ] Step 3 — if host resource issue: scale the RPC node or restart. Keep in mind a restart triggers another catchup window.
- [ ] Step 4 — if network partition: check peer connectivity, firewall rules, and core-peers metric if you have one.
- [ ] Verification: `ratesengine_stellar_rpc_latest_ledger_age_seconds` drops back under 300 s; source-stopped alerts that tracked this clear on their own within the next poll cycle.

## Related

- `source-stopped.md` — downstream effect when the RPC is completely unavailable vs just lagging.
- `core-lag.md` — captive-core-side version of the same issue.
- `all-ingestion-down.md` — P1 when the RPC failure takes ALL sources down simultaneously.
- stellar-rpc docs — `https://developers.stellar.org/docs/data/rpc/` for operator-side recovery guidance.

## Changelog

- 2026-04-23 — initial draft. Pairs with `source-stopped.md`'s "jump here for upstream" branch.
- 2026-04-30 — top-of-file deployment-posture callout: this alert
  is inert on r1 (stellar-rpc removed 2026-04-23) and is retained
  for any future RPC-façade deployment.
