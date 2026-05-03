---
title: Runbook — core-peers
last_verified: 2026-05-02
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

> When this alert un-inerts (Phase-3 Tier-1 validator rollout),
> stellar-core will run as the `stellar-core.service` systemd
> unit on each validator host (per the `archival-node` ansible
> role's `templates/systemd/stellar-core.service.j2` and
> ADR-0008). The procedure below assumes that bare-metal shape;
> there is no Kubernetes deployment for stellar-core anywhere
> in this architecture.

```sh
# Per-validator host — stellar-core's HTTP admin port is 11626 by
# default; localhost-only.
ssh root@val-01 "curl -s http://localhost:11626/peers | jq"
# Look at: total count, which peers we're connected to, any
#   "attempting" that aren't making it to "authenticated".

# Are we network-reachable? (11625 is the stellar-core P2P port.)
ssh root@val-01 "nc -zv <a-random-quorum-peer> 11625"
# Should report "succeeded", or refuse / timeout cleanly.

# Did we recently change the preferred-peer list?
ssh root@val-01 "grep -A20 'PREFERRED_PEERS' /etc/stellar-core/stellar-core.cfg"

# Is a firewall dropping our outbound?
ssh root@val-01 "journalctl -u stellar-core -n 200 --no-pager \
  | grep -iE 'connect|refused|timed out'"
```

## Typical root causes

1. **Firewall / egress change** — a new iptables / ufw rule on
   the validator host or the colo perimeter dropped our outbound
   11625.

2. **Preferred-peer list drift**. The SDF / LOBSTR / Satoshipay
   peers we explicitly trust changed IPs or retired them.
   - Mitigation: update `PREFERRED_PEERS` in
     `archival-node` role's stellar-core config template +
     re-apply.

3. **Large-scale network partition.** We can reach a few peers
   but not most. Usually correlates with BGP / upstream issues.

4. **Our own peer has been flagged for misbehaviour** by the
   wider network (too many invalid ledger submissions etc.) and
   peers are dropping us. Rare but has happened historically in
   the network.

## Mitigation

- [ ] Step 1 — identify whether we can *reach* the wider network
      (`nc -zv <known-peer> 11625` from the validator host).
- [ ] Step 2 — if firewall: unblock. Check the host's local rules
      (`iptables -L -n`, `ufw status`) and the colo perimeter
      egress policy.
- [ ] Step 3 — if preferred-peer list is stale: update config in
      the `archival-node` ansible role + apply with
      `--limit val-XX` rolling across hosts so quorum stays up.
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
- 2026-05-02 — diagnosis converted from kubectl ConfigMap +
  DaemonSet log commands to the bare-metal `val-XX` /
  `stellar-core.service` shape that the `archival-node` ansible
  role actually deploys (ADR-0008 + ADR-0004). The cited
  `deploy/k8s/network-policy.yaml` was a fictional file —
  iptables / ufw + the colo perimeter are the real egress
  controls.
