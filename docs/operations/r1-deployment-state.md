---
title: r1 archival node — current state and next-steps
last_verified: 2026-05-12
status: living doc
---

# r1-01 (FSN1) deployment state

Snapshot of what's running on the r1 host (Hetzner FSN1 dedicated;
public IP held in `configs/ansible/inventory/r1.yml`, gitignored)
as of 2026-04-26. Updated at each session.

> **Bringing up a new archival node?** Follow the end-to-end recipe in
> [archival-node-bringup.md](archival-node-bringup.md). This doc is a
> snapshot of one specific node, not a how-to.

## Hardware

- Intel Core Ultra 7 265 (20 cores — 8 P + 12 E)
- 192 GB DDR5 ECC (4 × 48 GB single-bit-ECC verified)
- 4 × 7.68 TB Samsung PM9A3 NVMe (datacenter-grade, 1 DWPD / 14 PBW)
- Hetzner dedicated, FSN1 datacenter (Falkenstein, Germany)
- Ubuntu 24.04.3 LTS noble

## Disk layout

```
nvme0n1 / nvme1n1 : OS mirror (mdadm RAID1)
                     p1  /boot/efi  512M  (per-drive vfat, not RAID)
                     p2  swap       4G    (md0, raid1)
                     p3  /          50G   (md1, raid1)
                     p4  ~7.63T     (ZFS raidz2 vdev)
nvme2n1 / nvme3n1 : untouched, full 7.68T (ZFS raidz2 vdevs)
```

ZFS pool `data` (raidz2, ~13.3 TB usable) with 5 datasets currently:
- `data/os` → `/var/lib/ratesengine`
- `data/postgres` → `/var/lib/postgresql` (recordsize=8K, logbias=throughput)
- `data/galexie` → `/var/lib/galexie`
- `data/minio` → `/var/lib/minio`
- `data/archive` → `/srv/history-archive`

The `data/core` and `data/rpc` datasets are gated behind
`run_stellar_core` / `run_stellar_rpc` in the ansible role
(`defaults/main.yml`); both default false since 2026-04-23 and
neither dataset exists on r1 today. Re-enable when validating
Phase-3 validator work.

## Services (systemd)

| Service | State 2026-05-03 | Notes |
|---------|------------------|-------|
| postgresql@15-main | active | TimescaleDB extension installed 2026-05-03; all 15 migrations applied. `ratesengine` role + DB created. |
| redis-server | active | Single-node, installed 2026-05-03 alongside the application bringup. |
| ~~stellar-core~~ | **REMOVED 2026-04-23** | Primary daemon dropped — archive pipeline doesn't need it; see [archival-nodes.md](../discovery/data-sources/archival-nodes.md) for revival path in Phase 3. |
| ~~stellar-rpc~~ | **REMOVED 2026-04-23** | Redundant for our data path — our own indexer consumes galexie's MinIO output directly via `ingest.ApplyLedgerMetadata`. Public API is `/v1/price` + `/v1/vwap` + `/v1/twap` + `/v1/ohlc` + …, not `/rpc`. See §Architecture below. |
| galexie | active, exporting | Own captive-core; uploading `FC4A....xdr.zst` objects to MinIO galexie-live at ~1/ledger. ~100 objects/5min at steady state. **The single stellar-core on the box.** |
| minio | active | Buckets: `galexie-live`, `galexie-archive`, `backups`. `ratesengine-reader` MinIO user (read-only on both galexie buckets) created 2026-04-26; password rotated + persisted to `/etc/default/ratesengine` 2026-05-03. |
| **ratesengine-indexer** | **active (NEW 2026-05-03)** | Reads galexie-live tail via S3 GetObject; cursor-resumable. Live tail of pubnet from L62,403,000+. Dispatches to 11 source decoders + writes to `trades` + `oracle_updates` hypertables. Listens for /metrics on `127.0.0.1:9464`. |
| **ratesengine-aggregator** | **active (NEW 2026-05-03)** | Tick-driven VWAP/TWAP/divergence/freeze/supply orchestration. Writes per-pair closed-bucket VWAPs to Redis cache (`vwap:<pair>:<window>`) + the `prices_1m` CAGG. Listens for /metrics on `127.0.0.1:9465` (auto-shifted off the indexer's :9464 default per #540). |
| **ratesengine-api** | **active (NEW 2026-05-03)** | Public REST + SSE. `auth_mode=none` for the bringup phase. Listens on `0.0.0.0:3000`. /v1/healthz + /v1/readyz green; /v1/price serves real closed-bucket VWAPs. |
| node_exporter | active | :9100 |
| ~~stellar-core-prometheus-exporter~~ | **REMOVED 2026-04-23** | Scraped primary /info endpoint; captives don't expose one. |
| node-healthcheck.timer | active | 5-min push to Healthchecks.io UUID 4cb3daba |

### 2026-05-03 first application bringup

The ratesengine application stack (indexer + aggregator + api)
ran against r1 for the first time on 2026-05-03. Sequence
captured here so R2 / R3 bringup follows the same path:

1. `apt install redis-server`. Redis-server enabled + started.
2. `apt install timescaledb-2-postgresql-15` (via PackageCloud
   apt repo); enabled `timescaledb` in `shared_preload_libraries`;
   restarted postgres; `CREATE EXTENSION timescaledb`.
3. Generated `ratesengine` postgres password to
   `/etc/ratesengine/postgres-password.txt` (mode 600).
4. Rotated `ratesengine-reader` MinIO user's secret; stored in
   `/etc/default/ratesengine`.
5. scp'd 4 binaries + migrations dir to r1.
6. Ran `ratesengine-migrate up` — 15/15 migrations applied
   after fixing migration 0005's TimescaleDB unique-index bug
   (PR #540 — the partition column `time` must be in the index).
7. Wrote `/etc/systemd/system/ratesengine-{indexer,aggregator,api}.service`
   units pointing at `/etc/default/ratesengine` for env.
8. `systemctl enable --now` for each service in dependency order:
   indexer (live tail) → aggregator (VWAP tick) → api.
9. End-to-end smoke: `/v1/price?asset=native&quote=USDC:GA5Z…` returned a real closed-bucket VWAP.
10. Kicked off historical backfill `L50,457,424 → L62,400,000`
    via `nohup /usr/local/bin/run-historical-backfill.sh`. Logs
    at `/var/log/ratesengine/backfill.log`. Idempotent on
    re-runs (trades hypertable's unique index is the dedupe).

### Architecture after 2026-04-23 trim

```
Stellar pubnet ─(SCP)─► galexie's captive-core ─► galexie ─► MinIO galexie-live
                                                                  │
                                                                  ▼
                                                       cmd/ratesengine-indexer (Galexie → ledgerstream → dispatcher)
                                                                  │
                                                                  ▼
                                                             TimescaleDB
                                                                  │
                                                                  ▼
                                                          `/v1/{price,vwap,twap,ohlc,...}` API
```

One stellar-core on the box. Everything downstream of MinIO is a batch/stream consumer via the Ingest SDK — no more captive-cores.

## Stellar quorum set (trust anchors)

21 tier-1 validators across 7 orgs, identical to SDF's canonical
`packages/docs/examples/pubnet-validator-full/stellar-core.cfg`
fetched 2026-04-23:

- publicnode.org (3): Boötes, Lyra by BP Ventures, Hercules by OG Technologies
- lobstr.co (3): LOBSTR 1/2/5
- www.franklintempleton.com (3): FT SCV 1/2/3 *(archive frequently 404s, known upstream)*
- satoshipay.io (3): Frankfurt, Singapore, Iowa
- stellar.creit.tech (3): Creit Alpha/Beta/Gamma
- www.stellar.org (3): SDF 1/2/3
- stellar.blockdaemon.com (3): Blockdaemon Validator 1/2/3

## What's running in background

- ~~**`stellar-archivist mirror`**~~ — completed; `/srv/history-archive`
  is at 7.0 TB with the full pubnet history (used by `verify-archive`
  Tier B as the trusted reference). 59 zero-byte files left over
  from peer fetch failures during the initial run were re-fetched
  individually on 2026-04-26.

- **`galexie.service`** — live tail running continuously, currently
  appending `.xdr.zst` objects to `galexie-live/` at one per closed
  ledger. Live-tip ~62.3 M and tracking network head.

- **`galexie-archive` bucket** — historical backfill complete as of
  2026-04-26 (4.76 TB, 974 partitions covering ledgers 1 → ~62.3 M).
  Mirrored from the AWS public bucket via per-partition `mc mirror`
  (see [galexie-backfill.md](galexie-backfill.md) for the recovery
  runbook and the `mc mirror --overwrite=false` gotcha).

- **Healthchecks.io push every 5 min** reports service health +
  ledger-age + ZFS health + disk space. Healthchecks watches
  galexie + minio + postgresql + node_exporter (stellar-rpc is no
  longer in the watched set since its 2026-04-23 removal).

## Known gaps / next-session priorities

### Unblocked ✓
1. **Galexie PEER_PORT collision fixed — exports flowing.**
   (Updated 2026-04-23 14:03.) Earlier today galexie + stellar-rpc
   were stuck in a restart loop (152+ restarts); root cause was
   `PEER_PORT = 0` in captive-core.cfg getting stripped by the
   go-stellar-sdk toml marshaller (omitempty on zero), leaving
   stellar-core to default to pubnet's 11625 → collision with
   primary → SIGABRT. Fixed by giving each captive a distinct
   non-zero PEER_PORT (primary 11625, stellar-rpc captive 11725,
   galexie captive 11726) in separate /etc/stellar/captive-core*.cfg
   files. Commit `507e4de`. Post-fix: 0 restarts, captive-core
   reached network head at 62250034, galexie uploading one
   `.xdr.zst` object per closed ledger to `local/galexie-live/`.
   300+ objects landed within 5 min of sync. Ingestion pipeline
   is end-to-end working.

2. **SCVal decoders implemented + audited for 7 of 8 sources.**
   (Updated 2026-05-01.) All 8 source decoders have real bodies —
   soroswap, aquarius, phoenix, reflector, sdex, comet, band,
   redstone — landed across PRs #5, #7, #15, #19 plus subsequent
   fix-ups (Reflector OpIndex stride, Aquarius event fan-out,
   Redstone Bytes unwrap, Band ContractCallDecoder).
   Per-WASM-hash audits **completed 2026-04-29** for soroswap,
   aquarius, phoenix, comet, reflector-{dex,cex,fx}, redstone,
   band → `BackfillSafe=true` flipped in
   `internal/sources/external/registry.go`; evidence under
   `docs/operations/wasm-audits/{<source>.md,evidence/}`. The
   2026-04-30 walk re-verified the picture against r1's full
   archive (`docs/operations/wasm-audits/r1-walk-2026-05-01.md`).
   **Blend audit (Task #53) remains:** Phase 1 done via stellar.expert
   (PR #339, all 9 pool addresses + current WASM bytes); Phase 2
   (mid-life-upgrade walk on r1) is the wide-net re-launch we
   kicked off 2026-05-01 22:05 (PID 1447513, `-to 62249727`).
   `BackfillSafe=false` on `blend` until that finishes + a
   per-WASM-hash review per
   [docs/architecture/contract-schema-evolution.md](../architecture/contract-schema-evolution.md).

5e. **Tagged-release deploy workflow available.**
   (Done 2026-05-05, PRs #645/#647/#648/#650/#651.) `gh workflow run
   deploy.yml -f region=r1 -f version=vX.Y.Z` is now the supported
   path for getting a tagged binary release onto r1. Workflow
   downloads SHA256-verified binaries from the GitHub Release,
   ships them to r1 via SSH, runs the Ansible playbook
   `configs/ansible/playbooks/deploy-binary.yml` which does
   stage → backup → atomic install → restart → /v1/healthz probe →
   automatic rollback on failure. Backups land at
   `/usr/local/bin/<binary>.prev-<previous-tag>` (5 retained).
   Sidecar version files at `/var/lib/ratesengine/deployed-versions/<binary>`
   track the running tag. **One-time setup needed:** 4 GitHub
   secrets per region (`R1_HOST`, `R1_USER`, `DEPLOY_SSH_PRIVATE_KEY`,
   `R1_SSH_KNOWN_HOSTS`). Operator runbook in
   [`docs/operations/deploy-workflow.md`](deploy-workflow.md).
   Until secrets are configured + a release is cut, manual
   `scp + systemctl restart` (the path used through 2026-05-05) is
   still the fallback.

5f. **Self-service signup + apikey_optional auth wired.**
   (Done 2026-05-05, PRs #662 #663 + r1 deploy.) A customer can now
   `POST /v1/signup {"email": "..."}` and get back a freshly-minted
   API key (Starter tier, 1000 req/min). The key authenticates on
   every subsequent request via `Authorization: Bearer <key>`.
   Operator change on r1: `[api].auth_mode = "apikey_optional"` in
   `/etc/ratesengine.toml`. Public surface (price queries,
   /v1/healthz, showcase) keeps serving anonymously at the
   60/min anon-tier rate-limit; authenticated requests get the
   per-key budget. Invalid keys → 401.

   Also installs Caddy + Prometheus + Loki + healthcheck-coverage
   for the application services (this session) and the full release
   pipeline (cut-release.sh + release.yml + deploy.yml + Dockerfiles
   from the prior session). v0.0.0-rc.1 cut as the first pipeline
   smoke test; release page at github.com/RatesEngine/rates-engine/
   releases/tag/v0.0.0-rc.1 with all 12 binaries + SHA256SUMS.

   Verified end-to-end on r1:
   - signup → key returned ✓
   - /v1/healthz anonymous → 200 ✓
   - /v1/account/me anonymous → 401 ✓
   - /v1/account/me with the issued key → 200 + matching key_id ✓
   - /v1/price anonymous → 200 (public surface preserved) ✓
   - /v1/account/me with garbage key → 401 ✓

   **Follow-up gaps** (operator-tracked, not blocking):
   - The mint-key CLI (`ratesengine-ops mint-key`) is operator-side
     bootstrap; signup is the public counterpart. Both produce
     keys via the same `RedisAPIKeyStore.Create` path.
   - Stripe webhook handler that lifts the per-key
     `RateLimitPerMin` on payment is not yet built — Pro / Business
     tier upgrades require operator-side `mint-key` with the
     `-rate-limit-per-min` flag for now.
   - Email verification is not yet enforced — anyone can sign up
     for any email address. The follow-up Stripe-paid flow will
     require a verified email before lifting beyond Starter.

### Important but not urgent
3. **Firewall + SSH hardening (phase 3)** not applied. Intentional —
   avoiding lockout risk until the box is stable. Keep KVM tab
   ready when we do.

3a. **`galexie-archive` is frozen at the historical-fill tip;
   `galexie-live` continues writing newer ledgers but to a
   different bucket.** (Discovered 2026-05-01.) The historical
   backfill that populated `galexie-archive` stopped on 2026-04-28
   at a verified tip of **62,249,727**; live galexie writes to
   `galexie-live` (per `/etc/galexie/galexie.toml`). Anything that
   reads from `galexie-archive` (eg `ratesengine-ops wasm-history`,
   `ratesengine-ops backfill`, `ratesengine-ops verify-archive-chunks`)
   MUST bound `-to` ≤ 62,249,727 OR fail with "ledger object …
   does not exist" on the partial trailing partition
   (FC49CDFF--62272000-62335999, currently 24,695/64,000 files).
   `detect-gaps` from 2026-04-28 02:39 found 242 gaps totalling
   459,966 missing ledgers, all clustered in the 40M-41M and
   44M-45M ranges (see `/var/lib/galexie/detect-gaps.json` on r1);
   the [50,457,424, 62,249,727] range that the wide-net walk
   targets is gap-free per that report.

   Fix options (operator):
   - Periodic `mc cp --recursive local/galexie-live/ local/galexie-archive/`
     after live ingest catches up, OR
   - Configure live galexie to write to `galexie-archive` directly
     and decommission `galexie-live`, OR
   - Make `ratesengine-ops` walkers consult both buckets at
     read-time (code change in `internal/datastore`).
   The first option is the simplest and matches the discovery
   plan's original design ("aws s3 sync post-mirror into
   galexie-archive bucket for durability" — see item 6 below).

4. **Layer-2 monitoring (Prometheus on a separate box) — role
   exists, deploy still pending on r1.** (Updated 2026-05-01.) The
   ansible role + AlertManager pair shipped in PR #363 — see
   `configs/ansible/roles/prometheus/`. Companion design note at
   `docs/architecture/prometheus-ansible-role-design-note.md`.
   Loki + Promtail sibling role landed in #364 (`docs/architecture/loki-ansible-role-design-note.md`).
   Both wait on a staging deploy to actually run; until then,
   Layer-1 (Healthchecks.io push) catches total-death but the
   28+ alert rules in `deploy/monitoring/rules/` (including the
   X2.5 fx-snap-fallback alert added 2026-05-01) have no
   evaluator wired up.

5. **pgBackRest** not configured. Postgres has no backups.

5a. ~~**Aggregator emitting zero VWAP rows on-chain pairs.**~~
   **RESOLVED 2026-05-04 20:09 UTC.** Headline product
   `/v1/price?asset=native&quote=fiat:USD` now serves real XLM/USD
   prices from on-chain Stellar SDEX/Soroswap data:

   ```
   {"data":{"asset_id":"native","quote":"fiat:USD",
            "price":"0.157384502084","price_type":"vwap",
            "observed_at":"2026-05-04T20:05:00Z","window_seconds":300},
    "flags":{"stale":false,"triangulated":false,"divergence_warning":false}}
   ```

   The fix required four PRs and a config edit:

   - **PR #629** — aggregator stablecoin-fiat-proxy expansion now
     reads `[trades].usd_pegged_classic_assets`, so a target
     `native/fiat:USD` expands to include source `native/USDC-GA5Z…`
     (matches actual on-chain Circle-USDC trades).
   - **PR #630** — `defaultPairs()` emits BOTH `crypto:XLM/fiat:*`
     and `native/fiat:*`. The API resolves the caller's asset
     literally; on-chain trades store the `native` form, so without
     this every default-pair tick produced an empty window.
   - **PR #631** — `/v1/price` Redis-VWAP fallback serves direct
     rewrites, not just triangulated values. The "Timescale is the
     source of truth for direct VWAPs" invariant only applies to
     LITERAL trade pairs; for aggregator-rewritten pairs the Redis
     `vwap:` key IS the source of truth.
   - **PR #632** — fallback lookup window 1m → 5m. The aggregator's
     default windows are `[5m, 1h, 24h]` — a 1m lookup missed every
     read.

   Operator config on r1 (added to `/etc/ratesengine.toml`):

   ```toml
   [aggregate]
   enable_stablecoin_fiat_proxy = true
   min_usd_volume = 0  # default 10000; r1 is on-chain-only until CEX
                       # connectors land — micro-volume XLM/USDC trades
                       # are the data we have

   [trades]
   usd_pegged_classic_assets = [
     "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
   ]
   ```

   Indexer was restarted to pick up the new `[trades]` config; new
   on-chain trades populate `usd_volume` correctly. Aggregator now
   reports `vwap_writes_total` climbing steadily (75 writes / 25
   ticks at the time of resolution).

   **Remaining gaps** (operator-tracked, not blocking):
   - `/v1/price/tip?asset=native&quote=fiat:USD` still 404s — uses
     a different reader path that doesn't share the Redis fallback.
     Fix is parallel to #631 + #632 but lives in a different
     handler; track separately if launch needs it.
   - ~~`/v1/price?asset=crypto:XLM&quote=fiat:USD` (abstract form)
     still 404s — would require enabled CEX connectors emitting
     `crypto:XLM/<quote>` trades.~~ **RESOLVED 2026-05-05 — see 5d
     below.**
   - `change_summary_5m`, peg-health, source-diversity, baseline
     refresh outputs all begin populating now that VWAPs land —
     give them ~1 baseline cycle to catch up.

5c. ~~**Discovery sink dropping ~3 k hits/min sustained.**~~
   **RESOLVED 2026-05-04 18:40 UTC, PR #621.** The async discovery
   sink now keeps a process-local `(contract_id, event_type)`
   seen-set and silently skips repeats before they hit the buffered
   channel — the recorder upserts on the same key, so re-enqueue
   was wasted work. Pre-fix snapshot for the record: 1,080,366
   dropped since process start; `discovered_assets` had 4,968 rows;
   drop rate ≈ 99.5%. Post-deploy snapshot at T+90s: dropped = 0,
   skipped = 6,353, `discovered_assets` = 4,975 (7 new rows in
   90s). Drop counter has flatlined; skipped counter exposes the
   dedup volume for capacity-planning visibility. The seen-mark is
   rolled back on genuine buffer-saturation drops so a later push
   for the same key can retry; restart resets the set and the
   first push for any key after restart still records.

5b. **`classic_assets` table seeded from trades.**
   (Done 2026-05-04, PR #595 context.) Direct SQL backfill
   from `DISTINCT issuer_g_strkey FROM classic_assets WHERE
   issuer_g_strkey IS NOT NULL` populated `issuers` with
   25,256 rows so `/v1/issuers/{g_strkey}` returns real data
   instead of 404. The accounts decoder will overwrite these
   rows with auth flags + home_domain as it observes account
   state on-chain.

5d. **CEX/aggregator/sanity connectors enabled on r1.**
   (Done 2026-05-05 12:31 CEST.) Until today r1 ran on-chain-only —
   `[external]` section was absent from `/etc/ratesengine.toml`
   and every venue defaulted to `enabled=false`. Closed both the
   `crypto:XLM/fiat:USD` 404 (gap noted in 5a above) and the
   RFP §4.7 CEX-coverage commitment.

   Operator change: appended `[external]` block enabling six free
   venues (no API keys provisioned for the paid tier today):

   ```toml
   [external]
     [external.binance]   enabled = true   # 4 pairs
     [external.kraken]    enabled = true   # 8 pairs
     [external.bitstamp]  enabled = true   # 7 pairs
     [external.coinbase]  enabled = true   # 3 pairs
     [external.coingecko] enabled = true   # 9 pairs (poller, divergence)
     [external.ecb]       enabled = true   # 9 pairs (poller, daily anchor)
   ```

   Backup at `/etc/ratesengine.toml.bak.pre-cex-20260505-123044`.

   Post-restart verification (T+~90s):
   - All 4 CEX streamers connected; trade rate at T+2min: binance
     460, coinbase 289, kraken 29, bitstamp 16 (binance dominates,
     as expected for XLMUSDT depth).
   - `/v1/price?asset=crypto:XLM&quote=fiat:USD` → VWAP $0.15871,
     3 sources (bitstamp/coinbase/kraken), 60s window, no flags.
   - `/v1/price?asset=crypto:BTC&quote=fiat:USD` → VWAP $80,761,
     3 sources (NEW endpoint).
   - `/v1/price?asset=crypto:ETH&quote=fiat:USD` → VWAP $2,374,
     3 sources (NEW endpoint).
   - `/v1/price?asset=native&quote=fiat:USD` continues to serve
     5m on-chain VWAP unchanged ($0.15897).
   - Zero errors in 60s of journalctl post-restart.

   **Follow-up gaps** (not blocking):
   - Binance's XLMUSDT trades not yet appearing in the
     `crypto:XLM/fiat:USD` source list. The aggregator's
     stablecoin-fiat-proxy (USDT→USD) is enabled but the proxy
     path may only collapse on-chain trades. Worth a 5-min trace.
   - `/v1/price?asset=USDC:GA5Z…&quote=fiat:USD` still 404s.
     Separate aggregator pair-synthesis gap: operator-declared
     `usd_pegged_classic_assets` are consumed when valuing
     XLM/USDC trades for `usd_volume`, but the aggregator does
     not synthesize a USDC/USD ≈ 1.0 published price.
   - `[aggregate].min_usd_volume = 0` stop-gap can revert to
     the default `10000` once CEX volume is confirmed sustained;
     defer to a separate one-line PR.

6. **stellar-archivist mirror → MinIO sync.** Our mirror lands on
   ZFS (`/srv/history-archive/`), not in MinIO. Phase-1 plan
   called for `aws s3 sync` post-mirror into `galexie-archive`
   bucket for durability. TODO after mirror completes.

### Backlog (no urgency)
7. ~~Scope Galexie to a dedicated MinIO user with bucket-scoped
   write-only policy.~~ **Done — PR #156 (2026-04-23):** `galexie-writer`
   has write-only on `galexie-live`; `galexie-archive-writer` has
   write-only on `galexie-archive` (used during the historical fill);
   `ratesengine-reader` has read on both (PR #162, 2026-04-26).

8. Galexie's resume-from-last-ledger behaviour: wrapper currently
   restarts from `archive_tip - 128` every time. Long-term should
   probe MinIO for last-exported-ledger and resume past it.

## Configuration pitfalls captured during first deploy

These are all now fixed in the role, but noted so the lessons survive:

1. Captive-core runs WITH a separate primary stellar-core on one box
   → every captive-core child MUST set `HTTP_PORT=0` and a **non-zero**,
   **distinct** `PEER_PORT`. `PEER_PORT = 0` looks right but gets
   stripped by the go-stellar-sdk toml marshaller (the `PeerPort`
   field has `toml:"PEER_PORT,omitempty"` in toml.go:90), and
   stellar-core then falls back to the pubnet default 11625 — which
   the primary daemon owns. Collision manifests as
   `std::system_error(98, "Address already in use", "bind: …")` →
   SIGABRT → ingestion restart loop. Our layout: primary 11625,
   stellar-rpc captive 11725, galexie captive 11726. Every captive
   needs its OWN captive-core.cfg file since the port must differ
   per consumer. The parent must also pass
   `STELLAR_CAPTIVE_CORE_HTTP_PORT=0` to match `HTTP_PORT=0` in the
   captive file (stellar-rpc validates parent↔child agreement).

2. Galexie's config schema is `[datastore_config]` / `[stellar_core_config]`
   — not what older docs suggest. Match `config/config.example.toml`
   from the galexie source tree at the pinned tag.

3. stellar-core's config parser has no "return to root" — anything
   after `[SECTION]` is scoped to that section. Top-level directives
   (KNOWN_PEERS, NETWORK_PASSPHRASE, DATABASE, etc.) must come
   BEFORE any `[...]` or `[[...]]` block.

4. `apt-stellar-rpc`'s shipped `/etc/default/stellar-rpc` hard-codes
   futurenet. systemd's `EnvironmentFile` takes precedence over
   `--config-path`. Either blank the file, or drop `EnvironmentFile`
   from the unit. Our role does the latter.

5. Galexie's append subcommand requires an explicit `--start`
   ledger; there's no auto-discover. Wrapper must query an archive's
   `.well-known/stellar-history.json` (NOT stellar-core's live
   tip — archives lag 5-15 min) and subtract a safety margin.

6. The stellar-galexie tag format is `galexie-vX.Y.Z`, and there are
   no prebuilt binaries. Build from source via
   `go install github.com/stellar/stellar-galexie@galexie-vX.Y.Z`
   and copy (NOT symlink) out of `/root/go/bin/` into `/usr/local/bin/`
   because `/root` is mode 0700.

7. `[HISTORY.ratesengine]` (our local history-archive block with
   `put`/`mkdir` commands) must ONLY appear in the PRIMARY
   stellar-core config — never in any captive. Captive-cores that
   see `put`/`mkdir` assume they're supposed to publish checkpoints
   too; they then loop "Activating publish for ledger X" every 2s
   against an archive that hasn't been initialised (no
   `.well-known/stellar-history.json`) and ingestion stalls
   mid-ledger. Our template now gates the block on cfg_mode=='full'.

8. **stellar-rpc v26.0.0-189 requires CAPTIVE_CORE_CONFIG_PATH.**
   Tested 2026-04-23: writing a cfg with just the datastore stanzas
   (`[datastore_config]` + `[buffered_storage_backend_config]`)
   and `SERVE_LEDGERS_FROM_DATASTORE = true` but no captive-core
   path makes stellar-rpc exit on startup with "captive-core-config-path
   is required". The `SERVE_LEDGERS_FROM_DATASTORE` flag is an
   augmentation for historical-fallback reads, not a replacement
   for captive-core-driven live ingestion. Closes the open item
   in docs/discovery/data-sources/composable-data-platform.md.
   **For Phase 1 we run captive + datastore-fallback (see
   templates/stellar-rpc.cfg.j2).** To get to 1-captive-core on
   the box, either wait for a stellar-rpc release that supports
   captive-less mode, or drop stellar-rpc and build our own
   consumer around `ingest.ApplyLedgerMetadata` from the galexie
   datastore.

### 2026-05-12 audit-2026-05-12 remediation deploy

Cut `v0.5.0-rc.49` (audit-remediation + F-1201 explorer migration)
and rolled R1 + applied operator-side config changes.

**Binary deploy** via `gh workflow run deploy.yml`:

  - `ratesengine-indexer` / `ratesengine-aggregator` / `ratesengine-api`
    all on `v0.5.0-rc.49` (build 2026-05-12T15:28:59Z, commit
    `e5b684f2`).

**Prometheus config roll** (F-1219 + F-1220 + F-1252):

  - `/etc/prometheus/prometheus.yml` replaced with the new
    `configs/prometheus/prometheus.r1.yml` (rule_files glob now
    `/etc/prometheus/rules.r1/*.yml`; scrape jobs added for
    redis_exporter, alertmanager self-scrape, postgres_exporter
    + pgbackrest_exporter + minio placeholders).
  - All 18 rule families in `configs/prometheus/rules.r1/` copied
    to `/etc/prometheus/rules.r1/`. Pre-roll R1 loaded 6 families;
    post-roll loads 19 (the 18 files + meta self-group).
  - Pre-roll backups at `/etc/prometheus/{prometheus.yml,rules.d}.bak-pre-rc49`.
  - `/etc/prometheus/minio.token` created empty (operator wires
    the real `mc admin prometheus generate` token when needed —
    until then the minio scrape 401s and `up{job=minio}` reports 0).

**TOML config roll** (F-1266):

  - `[supply]` block added to `/etc/ratesengine.toml` with 8
    `watched_classic_assets` (USDC / EURC / AQUA / yXLM / VELO /
    BLND / PHO / KALE — mirroring `internal/currency/data/seed.yaml`).
  - `watched_sep41_contracts` and `sdf_reserve_accounts` set to
    empty (operator opt-in).
  - Aggregator + indexer restarted to pick up the new config.
    Indexer log line `"supply observers wired" watched_classic_assets=8`.
  - Pre-roll backup at `/etc/ratesengine.toml.bak-pre-supply`.

**Alert state delta**:

  - `ratesengine_ingestion_source_stopped` family went from 5
    firing (pre-rc.49) → 2 firing (band + ecb only — both
    genuinely low-volume Soroban/FX sources). F-1212b's
    30m × 15m widened window working as designed.
  - 13 new alert families became visible to the deadmansswitch
    pipeline (supply / supply-snapshot / supply-refresh / cache /
    divergence / archive-completeness / sla-probe / SLO-burn /
    external-pollers / storage / stellar / verify-archive /
    anomaly).

**Audit findings closed by this deploy**:

  - F-1201 (explorer migration) — shipped in rc.49
  - F-1203 (rc.48 → R1) — superseded by rc.49 landing
  - F-1212 (5 false-positive source_stopped alerts) — auto-cleared
    when F-1212b's wider window took effect
  - F-1219 (R1 loaded only 7 rule files) — now loads 19
  - F-1252 (storage.yml not deployed) — now active
  - F-1266 (F2 fields NULL on R1) — supply observers running for
    the 8 watched currencies; F2 fields populate as data accrues

**Still open, operator-discretion**:

  - F-1213 (Redis ACL lockdown) — opt-in flag in ansible role;
    flip when ready to migrate every binary to per-user auth
  - F-1265 (1-year `prices_1m` backfill) — operator runs the
    catch-up per `docs/operations/backfill-procedure.md`
  - F-1267 (p95 over RFP target) — needs the multi-region
    cutover per `docs/architecture/r2-r3-bringup.md`

### 2026-05-12 F-1223 Caddyfile roll (post-rc.49)

R1 was running a pre-F-1223 Caddyfile that didn't block
`/metrics`. Public hit returned 200 with the full Prometheus
metrics surface — Go runtime stats, request counters, per-source
ingest gauges all readable by anyone hitting
`https://api.ratesengine.net/metrics`. Codex audit-2026-05-12.

Roll:

  - `scp configs/caddy/Caddyfile.api root@…:/etc/caddy/Caddyfile.new`
  - `caddy validate --config /etc/caddy/Caddyfile.new` → Valid
  - Backup at `/etc/caddy/Caddyfile.bak-pre-f1223-<ts>`
  - `mv Caddyfile.new → Caddyfile && systemctl reload caddy`
  - Post-roll: `curl /metrics` → 404, `/v1/healthz` → 200.

The new Caddyfile also brings the Cloudflare-real-client-IP
header chain (`client_ip_headers CF-Connecting-IP, X-Forwarded-For`)
that F-1224 ships against — F-1224's app-side fix is now end-to-
end live since Caddy is propagating real IPs.

### 2026-05-12 F-1205 evidence timers installed

The audit flagged R1 as missing every supply / SLA / archive
evidence-producing systemd timer that the in-repo `deploy/systemd/`
directory ships. Heartbeat + smoke timers were running but
neither sla-probe nor supply-snapshot nor archive-completeness
nor verify-archive-tier-a were installed.

Roll:

  - `scp deploy/systemd/{sla-probe,archive-completeness,supply-snapshot,verify-archive-tier-a}.{service,timer}` → `/etc/systemd/system/`
  - Created `/var/lib/node_exporter/textfile_collector/` (missing).
  - Drop-in override at `/etc/systemd/system/<svc>.service.d/override.conf`
    setting `User=root` since the in-repo `User=ratesengine` user
    doesn't exist on R1 yet (every existing ratesengine-* daemon
    runs as root; consistent posture for the oneshot units until
    we cut over to a dedicated user).
  - `systemctl daemon-reload && systemctl enable --now <timer>`.

Post-roll `systemctl list-timers --all` shows all four scheduled:

  - `sla-probe.timer` — every 15 min
  - `archive-completeness.timer` — every 4 h
  - `supply-snapshot.timer` — daily
  - `verify-archive-tier-a.timer` — weekly

**Per-timer test-fire + remediation status**:

  - **`verify-archive-tier-a.timer`** — ✅ active. Self-contained
    binary verifies archive chunks at 10k ledgers/sec. Fires
    weekly.
  - **`supply-snapshot.timer`** — ✅ active (after migration 0030
    + Go-side named-constraint fix in same wave). Test-fire wrote
    a real row to `asset_supply_history`:
    ```
    asset_key=XLM ledger=62539309 total=500018068120000000
    basis=xlm_sdf_reserve_exclusion
    ```
    Root cause: Timescale PG 16 / TS 2.16 rejects `ON CONFLICT
    (cols)` column inference against UNIQUE INDEXES on
    hypertables. Migration 0030 promotes the index → UNIQUE
    CONSTRAINT (DROP INDEX + ADD CONSTRAINT, since Timescale also
    rejects `USING INDEX` on hypertables). Go code switched from
    `ON CONFLICT (asset_key, ledger_sequence, time)` to
    `ON CONFLICT ON CONSTRAINT asset_supply_history_asset_ledger_idx`
    to bypass inference entirely.
  - **`archive-completeness.timer`** — ✅ active. `-to <ledger>`
    now computed at service-start by `/usr/local/sbin/compute-
    archive-to.sh` (Drop-In `ExecStartPre`) which queries the
    indexer cursor and subtracts 64 ledgers of safety margin,
    writing the value to `/run/archive-completeness.env` which
    the EnvironmentFile pulls in. Fires every 4 h.
  - **`sla-probe.timer`** — ⚠️ DISABLED. Binary updated on R1 to
    v0.5.0-rc.49-r1-patch (uses `/assets` not `/coins`). Still
    incompatible with anon-tier rate-limits at any reasonable
    load. Needs vault-minted `RATESENGINE_PROBE_API_KEY` at
    Partner/Operator tier in `/etc/default/sla-probe` before
    re-enabling.
  - In-repo `sla-probe.service` had an unquoted
    `Environment=PAIRS=-pair native,fiat:USD` that systemd
    parsed as two assignments — fixed (now
    `Environment="PAIRS=..."`).

### 2026-05-12 F-1201 host firewall live

R1 was running with `nftables` inactive and an empty ruleset.
External TCP probes from off-host confirmed MinIO 9000/9001,
Prometheus 9090, Loki 3100, Promtail 9080/38563, node_exporter
9100, Galexie 6061 all reachable from the public internet (codex
audit-2026-05-12 F-1201, severity critical).

A minimal default-deny nftables policy was applied
(`/etc/nftables.conf`) keeping only:

  - 22/tcp (SSH, rate-limited to 4/min new connections)
  - 80/tcp + 443/tcp (Caddy)
  - 11625/11626/11725/11726 (stellar-core SCP peer)
  - ICMP rate-limited 10/sec
  - Loopback unrestricted (Prometheus scrapes 127.0.0.1 per
    prometheus.r1.yml — internal services stay reachable)

External re-probe confirmed: 80→308, 443→400 (TLS-only domain
without SNI), 22 reachable, everything else times out.
`nftables.service` enabled + active for boot persistence.

The Ansible-managed `nftables.conf.j2` in the repo is more
thorough (uses `internal_cidrs` allow-lists, separate sets per
trust class); this minimal in-place config is the immediate-fix
drop-in placeholder until the full archival-node role re-runs
on r1 via the deploy workflow.

### 2026-05-12 F-1201 follow-up: SSH rate widened

Wave-23's minimal nftables set SSH at `4/minute new conns`,
which locked out operator work (a /loop session hammering SSH
naturally exceeds 4 new connects per minute when each shell-out
spawns a fresh session). Widened to `30/minute` on r1 + in
`configs/ansible/roles/archival-node/templates/nftables.conf.j2`.

### 2026-05-12 F-1209 mitigation: +16G swap

R1 had been at ~95% memory with the 4G swap partition fully
consumed. `free -h` showed 9.1Gi available out of 188Gi total.
Postgres `shared_buffers=48GB` (25% of total RAM) + minio +
galexie's captive-core working set leaves marginal headroom for
page cache, and the kernel had been swapping under pressure.

Added a 16GB swap file at `/swap_f1209` (`fallocate -l 16G`,
`mkswap`, `swapon`, persistent via `/etc/fstab` entry
`/swap_f1209 none swap sw,pri=1 0 0`). Swap is now 19Gi total
(4Gi original partition + 16Gi file); 16Gi free initially.
Doesn't fix the underlying tuning, but adds breathing room
during a memory-burst event so postgres doesn't trip OOMKiller.

The audit's remediation suggestion was operator-side capacity:
either bump RAM (Hetzner box-swap) or tune postgres
`shared_buffers` down. Adding swap is the low-risk in-place
mitigation; the proper fix remains an operator capacity
decision tracked under F-1209.

### 2026-05-12 alert-state snapshot post-Caddy roll

Firing alerts (14 total):

  - 3× `ratesengine_ingestion_source_stopped` — blend, ecb,
    phoenix (genuinely low-volume; F-1212b's wider window doesn't
    catch the long-tail). Not a deploy-blocker.
  - 2× `ratesengine_api_cache_miss_rate_high`
  - 1× `ratesengine_supply_snapshot_never_initialized` — supply
    pipeline starting fresh post-rc.49 config roll; clears once
    the first snapshot lands per `docs/operations/runbooks/
    aggregator-supply-refresh-never-initialized.md`
  - 1× `ratesengine_slo_latency_burn_slow` (F-1267 territory)
  - 1× `ratesengine_slo_availability_burn_slow`
  - 1× `ratesengine_host_memory_high` (F-1209 — operator action)
  - 1× `ratesengine_external_poller_stale`
  - 1× `ratesengine_deadmansswitch` — alertmanager heartbeat
  - 1× `ratesengine_aggregator_supply_refresh_never_initialized`

## Credentials (pointers, not values)

- Vault password: in ash's password manager
- MinIO root: in `inventory/r1.secrets.yml` (vaulted)
- Postgres stellar-core role password: same
- Healthchecks.io ping URL: in `/etc/default/node-healthcheck` on
  the box (mode 0600), also to be added to `r1.secrets.yml` via
  `ansible-vault edit`
- SSH admin key: ed25519 public key in `inventory/r1.yml`
