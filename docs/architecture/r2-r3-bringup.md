---
title: R2 / R3 multi-region bringup
last_verified: 2026-05-12
status: draft
---

# R2 / R3 bringup — multi-region cutover playbook

R1 (Hetzner FSN1) is the only deployed region as of 2026-05-12.
The multi-region architecture per [ADR-0008](../adr/0008-ha-topology.md)
+ [ADR-0016](../adr/0016-per-region-storage-strategy.md) calls for
three geographically-separated regions (R1 / R2 / R3) so the
cross-region byte-identical-VWAP property of
[ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) becomes
verifiable + the p95 SLA of 200 ms is achievable from any geography.

This document is the operator playbook for adding R2 and R3 once
the contracted infrastructure is in place. Until then, references
to it across the codebase (`scripts/dev/verify-cross-region.sh`,
`cmd/ratesengine-ops/cross_region_{check,monitor}.go`) just point
operators here for "why the command is a no-op today, and what
needs to land to make it useful."

## Pre-launch posture (today)

- R1 deployed; everything in
  [docs/operations/r1-deployment-state.md](../operations/r1-deployment-state.md)
- R2 / R3 inventory entries don't exist; `groups['ratesengine_api']`
  in ansible is single-host
- Cross-region tooling fails gracefully when only one region is
  configured (F-1234 audit-2026-05-12) — operators can run the
  commands as part of pre-flight smoke without spurious failures

## R2 bringup checklist

The R2 region is **AWS** per ADR-0016 (cloud-hybrid, different
provider from R1's Hetzner colo for blast-radius isolation). The
choice of cloud + region affects only the host-provisioning and
S3-endpoint configuration; everything else mirrors R1.

1. **Provision the host.**
   - Ubuntu 24.04 LTS, x86_64, ≥ 32 GB RAM, ≥ 1 TB NVMe.
   - Public IPv4 + IPv6.
   - Inbound firewall: 22 (mgmt), 80/443 (Caddy), 11625 (validator
     — phase 3 only), 26379 (sentinel within VPC).

2. **DNS.**
   - `api-r2.ratesengine.net` → R2 host's IP.
   - Cloudflare LB record for `api.ratesengine.net` adds R2 as a
     pool member with health check on `/v1/readyz`.

3. **Ansible inventory.**
   - Add R2 to `configs/ansible/inventory/multi-region/hosts.yml`
     under `ratesengine_api`, `ratesengine_aggregator`,
     `ratesengine_indexer`, `redis_cluster`, `postgres_cluster`.
   - Per-region overrides under
     `configs/ansible/inventory/multi-region/group_vars/r2/`.
   - Vault: regenerate `redis_password`, `postgres_pass_*`,
     `ratesengine_*_env` secrets and seed into `r2.secrets.yml`.

4. **Galexie path.**
   Per [ADR-0016](../adr/0016-per-region-storage-strategy.md):
   R2 reads `aws-public-blockchain` S3 directly rather than running
   its own galexie. Set `cfg.Storage.S3Endpoint` to the public
   bucket; no local mirror.

5. **Postgres.**
   Patroni-replicated cluster across R1's primary + R2's replica.
   First-boot: pg_basebackup from R1; pg_logical for ongoing.
   Verify lag stays under 5 s before promoting R2's API to serve
   any read traffic.

6. **Redis.**
   Per-region Redis (no cross-region replication). Sentinel
   tracks its own host. Cross-region cache is the source-of-truth
   prices_1m + Postgres-replicated; Redis only caches.

7. **Bring up the binaries.**
   ```sh
   gh workflow run deploy.yml -f region=r2 -f version=vX.Y.Z \
     -f binaries=ratesengine-indexer,ratesengine-aggregator,ratesengine-api
   ```

8. **Run verify-cross-region.sh.**
   ```sh
   R1=https://api-r1.ratesengine.net \
     R2=https://api-r2.ratesengine.net \
     bash scripts/dev/verify-cross-region.sh
   ```
   Closed-bucket VWAPs MUST be byte-identical across both
   regions for the configured pair set. Any divergence is a
   pre-launch blocker — replication lag, an aggregator bug, or
   per-region trade ingest drift.

## R3 bringup checklist

Identical to R2 with provider Vultr (per ADR-0016). Repeat steps
1–8 with `r3` substituted for `r2`.

After R3 lands, R1 / R2 / R3 form the Tier-1 three-region quorum
that [ADR-0004](../adr/0004-tier1-validator-aspiration.md) calls
for. Phase 3 work then adds the validator keys (separate doc when
that's in scope — outside this playbook's bringup-only focus).

## Verification post-bringup

| Check | Command | Expected |
|-------|---------|----------|
| All regions serve | `curl -sf https://api-{r1,r2,r3}.ratesengine.net/v1/healthz` | All 200 |
| Cross-region consistency | `scripts/dev/verify-cross-region.sh` | All pairs identical |
| Replication lag | `psql -c "SELECT * FROM pg_stat_replication"` | < 5 s |
| Trade ingest parity | `ratesengine-ops cross-region-check -metric vwap -window 1h -samples 24 -regions r1=…,r2=…,r3=…` | 0 divergences |

## Pre-flip blocker if any check fails

A single divergent VWAP across regions invalidates the
[ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) contract
the API depends on. Don't flip DNS until every check is clean over
a 24-hour observation window.

## Related

- [ADR-0004](../adr/0004-tier1-validator-aspiration.md) — Tier-1
  three-validator aspiration.
- [ADR-0008](../adr/0008-ha-topology.md) — Per-region HA topology.
- [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) —
  Cross-region byte-identical-VWAP contract.
- [ADR-0016](../adr/0016-per-region-storage-strategy.md) — Per-
  region storage strategies (R1 full / R2 hybrid / R3 hybrid).
- [docs/operations/r1-deployment-state.md](../operations/r1-deployment-state.md)
  — Snapshot of the single-region state R2 + R3 expand from.
