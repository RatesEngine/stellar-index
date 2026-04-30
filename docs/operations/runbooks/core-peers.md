---
title: Runbook — core-peers
last_verified: 2026-04-30
status: draft
severity: P2
---

# Runbook — `ratesengine_stellar_core_peers_low`

> **Deployment posture (2026-04-30).** stellar-core is **not running
> on r1** — the daemon was removed 2026-04-23
> ([r1-deployment-state.md §Services](../r1-deployment-state.md)).
> The metric `ratesengine_stellar_core_peer_count` has no producer,
> so this alert is *inert* on r1. Galexie's embedded captive-core
> connects out for ledger replay but does not expose a `/peers`
> endpoint to the prometheus exporter.
>
> The alert remains in `deploy/monitoring/rules/stellar.yml` for
> Phase-3 (Tier-1 validator rollout, ADR-0004). Until a validator
> is brought online, treat any alert that reaches you for this
> rule as a misconfiguration rather than a real signal.

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_stellar_core_peers_low` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/stellar.yml` |
| Typical MTTR | 15–60 min |
| Impact | stellar-core connected to < 5 peers. It may still be tracking the ledger fine, but its view of quorum is fragile — any further partition and we lose sync. Pre-emptive ticket, not an outage. |

## Symptoms

- `ratesengine_stellar_core_peer_count < 5` for ≥ 5 min.
- `stellar-core/peers` endpoint shows < 5 connected peers.
- Dashboard: *Stellar → peer count* sitting well below steady
  state (usually 20+).

## Quick diagnosis (≤ 5 min)

```sh
curl -s http://stellar-core:11626/peers | jq
# Look at: total count, which peers we're connected to, any
#   "attempting" that aren't making it to "authenticated".

# Are we network-reachable? (11625 is the stellar-core P2P port.)
telnet <a-random-quorum-peer> 11625
# Should connect and show the handshake, or refuse cleanly.

# Did we recently change the preferred-peer list?
kubectl describe cm stellar-core-config | grep -A20 'PREFERRED_PEERS'

# Is a firewall dropping our outbound?
kubectl logs ds/stellar-core --tail=200 | grep -iE 'connect|refused|timed out'
```

## Typical root causes

1. **Firewall / egress change** — a new NetworkPolicy or the
   colo's firewall dropped our outbound 11625.

2. **Preferred-peer list drift**. The SDF / LOBSTR / Satoshipay
   peers we explicitly trust changed IPs or retired them.
   - Mitigation: update `PREFERRED_PEERS` in core config.

3. **Large-scale network partition.** We can reach a few peers
   but not most. Usually correlates with BGP / upstream issues.

4. **Our own peer has been flagged for misbehaviour** by the
   wider network (too many invalid ledger submissions etc.) and
   peers are dropping us. Rare but has happened historically in
   the network.

## Mitigation

- [ ] Step 1 — identify whether we can *reach* the wider network
      (telnet to a known peer's 11625).
- [ ] Step 2 — if firewall: unblock. Check egress policy in
      `deploy/k8s/network-policy.yaml`.
- [ ] Step 3 — if preferred-peer list is stale: update config
      via GitOps + rolling restart.
- [ ] Step 4 — if we're the one getting dropped: check our own
      core logs for invalid-ledger or misbehaviour warnings, then
      reach out to the quorum-set operators.
- [ ] Verification: peer count climbs back to ≥ 10 sustained.

## Known false-positive patterns

- **Cold boot** — stellar-core needs a few minutes to authenticate
  all peers. The `for: 5m` threshold covers normal boot; a long
  catchup can tip it.
- **Rolling restart of our own validators** — we run three
  (`ADR-0004`); if you're restarting them in sequence, briefly
  each instance has fewer peers.

## Related

- `core-lag.md` — when losing peers escalates to losing sync.
- `host-down.md` — if the host hosting stellar-core is down,
  peers-low is redundant (the bigger problem).
- ADR-0004 (three-validator aspiration).

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — top-of-file deployment-posture callout: this alert
  is inert on r1 (stellar-core removed 2026-04-23) and is retained
  for Phase-3 validator rollout.
