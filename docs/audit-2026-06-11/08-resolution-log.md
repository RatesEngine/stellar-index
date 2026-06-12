# Resolution Log — 2026-06-11 audit fixes

Fixes applied on branch `audit-fixes-tier0` (off `main` @ `dc82888a`). **Read-only audit code untouched; nothing deployed; `make verify` green at every checkpoint.** 30 commits.

The substrate/completeness verification path was treated as in-scope (per operator direction) and improved with extra care; an independent adversarial review (Wave-3-style, fresh eyes) confirmed the four coverage-critical changes are SOUND and that two of them *strengthen* the coverage guarantee.

## Resolved (with the commit theme)

| Finding | Resolution |
|---|---|
| F-1316 (sep41 projector silent loss) | Projector now uses the dispatcher's **watched set** (not a firehose — revised after realizing a firehose would expand r1's data + diverge Phase-3/4); skipped when no contracts watched. Config `Default()` backstop sets `PersistPerSource=true`. |
| F-1317 (dispatcher Stats() race) | Mutex on the counter maps; `-race` test. |
| F-1318 / F-1350 (durability/shutdown) | Worker buffers flush with a fresh ctx on shutdown; `defer cancel()` ordering; API dry-run gate moved; producer joined before `close(events)`. |
| F-1319 (VWAP oldest-10k) | `TradesInRange` keeps the **newest** N (DESC+reverse); truncation counter + WARN. |
| F-1320 (supply dormant rejection) | Dormant assets accepted (`dormant` outcome); per-asset alert (`by (asset_key)`, excludes dormant). |
| F-1321 (market-cap 10^7× inflation) | `display_decimals` no longer used as unit scale; separate `display_decimals` wire field; test pins decimals=7. |
| F-1322 (Stripe never-upgrades) | Dedup short-circuits only on `processed_at IS NOT NULL`; failed-first-retry regression test. |
| F-1323 / G10-02/03/04 (chainlink + CEX) | Full uint80 round-id dedup; all-feeds-failed returns error; F-0029 reconnect parity to kraken/coinbase; API keys off URL query. |
| F-1324 (coarse-PK silent loss) | `event_index` PK discriminator on 5 tables (migrations 0057-0060) + decoder/writer plumbing; lint enforces it. |
| F-1325 (observations CEX suppression) | Fast-path narrowed to `fiat:USD` only; stream timeout (G2-04). |
| F-1326 (assets pagination dead) | Overfetch-by-one + store clamp fix; pagination tests. |
| F-1327 (config default drift) | `Default()` matches every `default:` tag; reflective drift-test backstop; `Supply.Validate()` wired (G19-02). |
| F-1329 (dead alert layer) | 18/106 alerts repointed to real metrics (pg_up, node_zfs_zpool_state, pgbackrest_*, verify_archive_mismatches) or marked INERT; new `lint-metric-refs.sh` CI guard. |
| F-1330/1331/1333/1352 (ops/arch docs) | rollback.md + projector/port/metric runbooks; ingest-pipeline.md/ha-plan rewritten; OpenAPI regenerated; proposal-corrections registered. |
| F-1332 (dashboard headers) | `web/dashboard/public/_headers` (CSP/HSTS/frame-ancestors/no-store). |
| F-1334 (test rot) | Integration retention assertions flipped; PG15 literal; ops compile-break; build-check broadened + wired into CI. |
| F-1335/1336/1337/1338 (security) | Anon rate-limit IP-only; XFF rightmost-untrusted; SEP-1 proxy SSRF; API-key URL leak. |
| F-1339/1340/1344/1345/1351 (API/agg correctness) | SEP-40 stale flag; XLM alias on sibling surfaces; per-pair divergence key; freeze LKG TTL; OpenAPI contract. |
| F-1346 / F-1349 (perf/lake) | Bounded coin/fx scans + caches; LiveSink bounded buffers + exported metric + ledger_entry_changes gap documented (ADR-0034 exclusion). |
| F-1353/1354/1355 (entry docs/ADR) | CLAUDE.md recipe/repo-map/invariant fixes; ADR amendment banners + index. |
| F-1357 (ansible destructive) | prometheus role rules-source path fixed + non-empty assert (was deleting all host alert rules). |
| F-1358 / W0-01 (CI/deps) | ci.yml `permissions: contents: read`; openapi-on-push; x/net v0.55 (GO-2026-5026 cleared). |
| G15-05/06 (census tx-event blindness) | `GetTransactionEvents` errors counted in dispatcher+census+lake; census-backfill declines a "complete" row on error. |
| Long-tail (G6/G7/G8/G9/G14/G18 low/info) | Oracle ts overflow clamp; fail-closed timestamps; panic-safe asset keys; changesummary math; FanoutOpIndex guard; NaN validation; sorted sources; ~50 stale-comment/dead-code fixes. |

## Deliberately NOT changed (documented decisions)

- ~~**F-1347 (blend/phoenix/soroswap topic-only Matches pollution)**~~ — **REOPENED + being fixed.** Operator rejected the "noise not loss" framing: counting non-protocol events as belonging to a protocol is itself a correctness bug (a `swap` from a foreign AMM is not a Soroswap trade), and topic symbols collide across protocols open-endedly. Decision recorded in **ADR-0035 (factory-anchored contract gating)**: decoders gate `Matches()` on contract identity — anchored on each protocol's factory, fanning out to every factory-created child. Soroswap is the reference impl (done); blend/aquarius/phoenix/comet/defindex follow. This REVERSES the old CLAUDE.md "match broadly, filter downstream" policy. Per-source deploy precondition: a one-time historical re-derive from the lake to purge existing foreign rows (else the reconcile flags them as phantoms).
- **sep41 in the ADR-0033 reconcile** — correctly deferred: historical rows predate migration 0057's `event_index`, so a re-derive would false-flag collapsed rows as "missing." Add after 0057 + a lake re-derive (documented in reconciliation_catalogue.go).
- **Go toolchain bump (GO-2026-5039 net/textproto, GO-2026-5037 crypto/x509)** — fixed in go1.25.11; a deploy/CI toolchain bump, not a code change (local toolchain is 1.25.10, so not verifiable here).
- **WA-01 (explorer baked "live" prices)** — a client-refetch rework, out of scope for this pass.

## Deploy preconditions (not done here — operator steps)

- Migrations 0057-0060 (PK `event_index`): stop indexer → migrate → redeploy the event_index sink → `TRUNCATE` + re-derive from the lake (per each migration's rollout note). The historical collapsed rows are only recovered by the re-derive.
- The out-of-band `ratesengine-ops` binary on r1 (rc.108-18) and the sla-probe interim `-freshness-target 150s` flag should be reconciled at the next release that carries these commits.
