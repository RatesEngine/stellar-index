---
title: Audit remediation — items requiring operator (human) action
status: living — populated as remediation proceeds
---

# Operator-action register (audit remediation 2026-07)

Everything the two audits (correctness/security 2026-06-30 + maintainability
2026-07-01) surfaced that an agent **cannot** safely do alone — because it needs a
GitHub/cloud/vendor account, a secret, a legal decision, prod infra, or a
judgment call that's yours. The code-side fixes are being applied separately;
these wait for you.

## Repo / CI settings (highest leverage — one setting unlocks every guard)
- [ ] **Branch-protect `main` + require status checks** (CS-097). This is the single
  highest-leverage action in either audit: it converts the entire existing lint/test
  guard layer from *advisory* to *blocking*. GitHub → Settings → Branches → protect
  `main`, require the `ci` checks + a review. Also enable Dependabot/security alerts.

## Accounts / secrets (launch-blocking, operator-only)
- [ ] **Buy CoinGecko Pro** → set `COINGECKO_API_KEY` on r1 + restart indexer (P0-3).
- [ ] **Create Healthchecks.io account + Discord webhooks** → paste `DISCORD_WEBHOOK_URL_PAGES`/
  `_ALERTS` + the 4× `HEALTHCHECKS_URL_*` into r1 env files; rerun `pre-launch-check.sh`.
- [ ] **Rotate the postgres_exporter DSN password** (leaked in earlier session output;
  in `/etc/default/prometheus-postgres-exporter`).
- [ ] **Relocate + rotate the GCP service-account key** (CS-001) — `rates-engine-data-
  validation-*.json` sits in the repo working tree (gitignored, not committed). Move it
  out of the repo dir; rotate if it was ever shared; confirm the SA is still used.

## Set config values (code is ready; values are yours)
- [ ] **Set `sdf_reserve_accounts` (+ their balances) in the r1 inventory** (CS-010). The
  code now emits an honest `xlm_total_only` basis when the list is empty (so the "+58%
  market cap" no longer *lies*), but the *correct* circulating supply needs SDF's
  directed/reserve account list. This is the config half of the XLM fix.
- [ ] **Re-raise `min_usd_volume` to the 10000 default** once CEX/CoinGecko data flows
  (currently 0 to serve on-chain-only micro-volume; CS-040 makes the gate scale-correct).

## Disaster recovery (infra decisions — CS-110/111/112)
- [ ] **Off-host the pgBackRest repo** (add `repo2` offsite) — backups currently sit in the
  same-host MinIO/ZFS pool as the DB (single failure domain).
- [ ] **Drill a real restore** to scratch (backups are verified only by `pgbackrest info`,
  never restored) + a `drop_chunks`-on-staging chaos drill.
- [ ] **Back up (or document + test the rebuild of) the ClickHouse lake** — the ADR-0034
  source of truth has zero backup and isn't Ansible-provisioned.

## Multi-region / HA (gated on hosts existing — P3)
- [ ] Provision R2 (AWS) + R3 (Vultr); then the `redis-sentinel`/patroni/bringup roles run.
- [ ] **Patroni REST auth** (CS-122) — set `patroni_rest_basic_auth_user/password` in vault
  before Patroni deploys (defaults to unauth on 0.0.0.0).
- [ ] Narrow `allowed_ssh_cidrs` from `0.0.0.0/0` once a stable admin range exists.
- [ ] Optional: Cloudflare orange-cloud in front of `api.` (WAF/L7).

## Decoder contract-gating (CS-026) — blocked on per-source data/design
The mechanism exists (`childgate.Registry` seeded from the `protocol_contracts`
table + hard-coded factories, gated `Matches()` — as `blend`/`soroswap` already
do). Extending it to the 4 ungated sources is blocked on data only the team /
operator can supply — a gate on an UNCONFIRMED factory either drops real trades
(fail-closed, unseeded) or bakes in a wrong trust root, so it must not be guessed:
- [ ] **phoenix** — confirm the pool-factory contract ID with the Phoenix team
      (docs/protocols/phoenix.md marks it "confirm the factory"); then hard-code
      it as `phoenix.MainnetPoolFactories` + `seed-protocol-contracts` + lake
      re-derive. Then I wire the childgate gate (mechanical, ~blend-shaped).
- [ ] **defindex** — confirm the full `DeFindexFactory` set (the doc notes >1
      factory + an open question on vault enumeration); then same as phoenix.
- [ ] **aquarius** — pin the complete pool set (docs/protocols/aquarius.md: "pool
      enumeration not yet pinned"). Until enumerated, no gate is possible.
- [ ] **comet** — has NO factory namespace (shared `("POOL",…)` topic). Decide the
      gate design: a curated pool allowlist OR a WASM-hash gate (only decode
      contracts whose code-hash matches the Balancer-v1 Comet WASM). Needs the
      WASM hash + a design call.
Once each source's factory/allowlist is confirmed, wiring the gate is a small
mechanical change per source (add to `gatedSources`, make the decoder
childgate-aware, gate `Matches()` on `reg.Has(contractID)`).

## Legal / vendor (before commercial launch — CS-115/116)
- [ ] **Vendor-ToS review of raw CEX data redistribution** — `/v1/history` + `/v1/observations?
  source=binance` re-serve raw per-trade source-attributed records; Binance/Kraken/Coinbase
  terms generally prohibit this. Blended outputs (`/v1/price|vwap|…`) are defensible. Decide
  whether to gate raw source-attributed endpoints for restricted venues.
- [ ] **External security review** booking (P2-3).
- [ ] Confirm CoinGecko Pro redistribution terms; add a `NOTICE`/`THIRD_PARTY` file + the
  goxdr dual-license (GPL/Apache→Apache elected) note for the pre-flip SBOM gate.

## Launch cutover (operator/DNS — P2-5/P2-6)
- [ ] DNS flip finalization, public rate-limit tier, announcement, 24h watch (endpoints are
  already DNS+TLS live; this is the go-live decision + comms).

---
_Each item cross-references its CS-###/finding in `docs/audit-2026-06-30/` or
`docs/maintainability-audit-2026-07-01/`. Code-side fixes tracked in the commit log._
