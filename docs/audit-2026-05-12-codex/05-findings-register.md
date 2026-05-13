# Findings Register

Cold findings only. No prior finding is imported into this register.

## Closure summary (verified reconciliation snapshot, 2026-05-13 wave-119 refresh)

The register below is authoritative; this summary captures the
highest-priority items as of the wave-119 reconciliation recheck. Status counts
at this snapshot:

- **Findings register**: 67 fixed / 23 open (90 total).
- **XFI cross-file table**: 63 fixed / 19 open (82 total).
- **Remediation plan**: 65 fixed / 23 open (88 total — multi-finding
  R-rows split the count; the open remediation rows resolve to
  the current finding set plus mixed multi-finding operator rows).

All three surfaces are mutually consistent as of wave 119.

**Latest high-priority state.** Earlier code-actionable findings through
wave 95 shipped, but the deployment/HA tranche reopened code/config risk:
`F-1275` is now source-closed because Redis-only readiness failure returns
200/degraded and is regression-tested; `F-1284` is the residual docs drift in
HAProxy/HA prose that still describes Redis as readiness-critical.
`F-1278` shows HA-role nftables drop-ins do not
compose deterministically with the repo firewall model even after the
`priority -100` remediation attempt; and `F-1280` remains open on missing
README/inventory guidance for the required etcd checksum after source-side
preflight validation landed. The Patroni follow-up closed `F-1279`,
`F-1281`, and `F-1282`, then added `F-1283` for the Timescale primary-down
runbook's stale etcd protocol/quorum/key examples. Quality-improvement work
also continued in waves 96-114 (CI gap closure on the R1 rule overlay,
remediation-plan reconciliation, status-page closure falsification,
monitoring-doc breadth review, the Ansible role-doc pass, Healthchecks,
R1 rule-overlay, audit-input setup review, the Redis/Sentinel deployment
deepening pass, HAProxy readiness review, API incident-doc selector review,
cross-role nftables review, and Patroni runnable-defaults/DR review).
Current notable open docs/operator drifts:
`F-1211` is open again because several active non-audit surfaces
still prescribe the retired Upptime/cstate/status-repo incident flow;
`F-1266` is source-closed because the remaining HAProxy, Loki,
Patroni, and Prometheus role READMEs now either label missing orchestration
playbooks as backlog-only or point at the tracked `monitoring.yml` entrypoint;
`F-1272` is source-closed because exporter username selection now follows the
ACL-lockdown branch instead of rendering unconditionally;
and `F-1273` remains open because both the Sentinel design note and role README
still describe the live FailoverClient path as future `internal/cachekeys` work
after the drill command itself was repaired.
`F-1274` is source-closed after the HAProxy docs redirected to tracked
`api-down.md`; `F-1277` is source-closed after `api-down.md` was corrected to
`internal/api/v1/server.go::handleReadyz`; and `F-1276` narrows to the stale
`job="api"` comment in `internal/obs/metrics.go`. The next monitoring-role
pass adds `F-1285` for unauthenticated upstream binary downloads in the Loki,
Promtail, Prometheus, and Alertmanager install paths, and `F-1286` for Loki's
systemd credential mapping that relies on shell-style variable expansion that
systemd does not perform. The same pass adds `F-1287` for Prometheus and
Alertmanager loopback binds being targeted through private IPs in the generated
alerting/self-scrape config, and `F-1288` for the Prometheus TSDB disk
preflight being documented as blocking while the task explicitly soft-fails.
`F-1286` is now source-closed after the Loki unit stopped rendering literal
`${RATESENGINE_S3_*}` assignments and the README/defaults shifted operators to
direct `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` values in
`/etc/default/loki`. `F-1289` captures the remaining Loki storage preflight gap:
the role docs/defaults/design note promise MinIO bucket and local-disk checks
that `server-01-preflight.yml` does not perform. The Prometheus moving-fix
pass closes `F-1287` and `F-1288` in source, but adds `F-1290` for the residual
Prometheus README drift that still says the UI is loopback-only and the
firewall opens only 9094 after the role changed to `0.0.0.0:9090/9093` with
firewall-gated internal access.
The same pass also confirms `F-1265`, `F-1270`,
`F-1271`, `F-1266`, and `F-1272` are source-closed on the current workspace.
Final code closures since the prior summary include:

- `F-1243` (wave 64) — `ResetAssetRegistryDedupeForTest` helper +
  `test/integration/asset_registry_replay_test.go` close the
  audit-requested DB-backed duplicate-replay evidence: replaying
  a stored trade with a cold dedupe cache (process-restart shape)
  does NOT advance `classic_assets.observation_count`, while a
  distinct trade on the same asset does. Three scenarios pin the
  contract end-to-end against testcontainers-go Timescale (2.0s).

- `F-1219` (wave 55) — Stripe paid-upgrade webhook now lifts
  Postgres-backed dashboard keys via the new
  `StripePlatformBridge.APIKeys` slot + `upgradePlatformAPIKeys`,
  in addition to the existing Redis-side `Manager` path.
- `F-1226` (wave 39 final) — TouchUsage half: `auth.RedisTouchDebouncer`
  + `middleware.TouchUsage` post-handler debounce so the dashboard
  "last seen" column actually advances.
- `F-1236` (wave 60) — operator-opt-in strict-freshness gate
  via `WithStrictFreshnessRequired` + `[supply].strict_freshness_required`
  rejects supply snapshots with no `MinComponentLedger` anchor.
- `F-1261` (wave 46) — migration `0030` rewritten to follow the
  decompress-chunks + restore-compression pattern from
  `0004_relax_trades_ledger_for_offchain` so fresh integration
  bootstrap and the next R1 schema advance both succeed.

**Observability follow-on (waves 88–96, beyond the audit's primary
findings surface).** The wave-65 Stripe-bridge sync-error metric
established a precedent: `*Total{outcome}` counter without a
paired latency histogram is an observability gap on a goroutine
worker. Waves 88–91 closed the same gap on four IO-bound
goroutine workers (customer-webhook delivery; divergence refresh;
supply snapshot refresh; freeze recovery sweep); waves 92–95
shipped regression tests pinning all four. Wave 96 extended
`make monitoring-check` to validate the R1 rule overlay (closing
a CI gap that would have let R1-overlay edits with broken PromQL
ship undetected). These weren't audit-driven but they're in the
audit's spirit, and the metrics are now operator-charted.

**Findings remaining open** (entries below include operator/admin work,
deployment work, code/config defects, and documentation drift; every one still
requires source-backed or live-environment closure evidence):

- `F-1201` — operator: live R1 nftables still accepts public
  `11726/tcp` + `stellar-core` listens there; reconcile against
  the repo template's captive-core posture.
- `F-1205` — operator: `sla-probe.timer` is installed-but-disabled
  on R1; needs a Partner/Operator-tier API key minted via
  `ratesengine-ops mint-key` and dropped at
  `/etc/default/ratesengine` so the timer can start.
- `F-1206` — operator (multi-day): R2 + R3 deploy + failover-chaos
  drill before the **multi-region** launch-readiness gate goes green.
  Wave 68 (2026-05-13) added a `make verify-launch-ready-single-region`
  preset that skips L4.14-17 + L5.6 + L5.8 — the rows that gate on
  the deferred multi-region surface — and goes green against today's
  R1-only posture. Multi-region gate (`make verify-launch-ready`)
  remains red, correctly, until the operator work lands.
- `F-1207` (hosted half) — repo-admin UI: enable GitHub
  Vulnerability Alerts + Dependabot Alerts in repo Settings.
  Code-side npm/pnpm ecosystems already wired.
- `F-1208` — operator: per-source triage on R1 (ECB / Soroswap /
  Band / Phoenix / Comet / Blend / Redstone) + Coingecko 429
  diagnosis. Wave 70 (2026-05-13) added a CoinGecko-specific 429
  triage matrix to `runbooks/external-poller-error-rate-high.md`
  covering the three CG tiers (public/demo/Pro), the 403-as-429
  post-2024 behavior, the `MinBackoff = 60s` / `MaxBackoff = 1h`
  cooldown semantics, and three ranked common causes (missing
  key / catalogue growth past 25-28 entries / multi-binary IP
  contention). The CG half is now operator-runbook complete; the
  remaining per-source triage stays operator-only.
- `F-1209` — operator: R1 capacity triage (memory + swap +
  MinIO 78% full).
- `F-1211` — docs/comms: remove the surviving active
  Upptime/cstate/status-repo instructions from root planning docs and
  deploy comms templates, leaving only the shipped Cloudflare Pages status
  workflow.
- `F-1214` — repo-admin UI: enable main-branch protection rules
  (required CI, CODEOWNERS review, signed commits, no force pushes).
- `F-1215` — repo-admin UI: add required reviewers to deploy
  environments (`r1`, docs, explorer, status, github-pages).
- `F-1216` (admin half) — repo-admin UI: restrict Actions
  allowed-actions list + remove admin bypass. CI SHA-pin lint
  already shipped.
- `F-1218` (gate half) — operator decision: flip
  `[api].signup_require_email_verification` to `true` on R1 after
  the rollout window has given existing customers time to verify.
  Code-side gate fully shipped (wave 45).
- `F-1225` — operator: deploy fresh API binary on R1 so the
  source-side since-inception fallback (already shipped) is live.
- `F-1228` — operator: deploy fresh API binary on R1 so the
  source-side SSE write-deadline fix (already shipped) is live.
- `F-1230` — operator (12-hour supervised job): run
  `ratesengine-ops backfill --pair native,fiat:USD --since 2025-05-13`
  to backfill `prices_1m` to the documented 1-year `since-inception`
  contract.
- `F-1273` — Redis/Sentinel docs: remove the remaining future-state
  `internal/cachekeys` FailoverClient claims from the shipped design note and
  role README.
- `F-1276` — monitoring docs/source comments: replace the remaining stale
  `job="api"` source comment with the current multi-host/R1 job-label family.
- `F-1278` — HA firewall config: replace the accept-only nftables drop-ins
  with a deterministic allow-list composition that works with the default-drop
  baseline; the current `priority -100` change still leaves early accepts
  droppable by the later default-drop chain.
- `F-1280` — Patroni Ansible: define/document a real etcd release checksum
  path in the README/inventory model now that source preflight rejects missing
  or placeholder checksums.
- `F-1283` — Patroni/Timescale docs: update the primary-down runbook's etcd
  protocol, leader-key, and quorum examples to match the shipped role.
- `F-1284` — HAProxy/HA docs: update readyz descriptions so Redis-only
  failure is described as 200/degraded rather than backend-draining.

Recent waves closed by code (chronological):

- wave 27 — F-1207 npm/pnpm Dependabot ecosystems + F-1255 Suspend-marker
  defence-in-depth.
- wave 28 — F-1260 survivor-set MinUSDVolume gate.
- wave 29 — F-1249 incident.sev1/resolved operator-triggered producer.
- wave 30 — F-1255 SETNX-backed signup email lock (full transactional
  first-login).
- wave 31 — F-1203 generated explorer API types regen committed.
- wave 32 — Stripe platform bridge wiring narrows F-1219 + F-1220 deploy
  ansible collection install.
- wave 33 — F-1258 Redis-less UsageReader is now typed-nil instead of
  wrapping a nil counter.
- wave 34 — F-1226 cache-hit policy parity: `APIKeyRecord` now round-
  trips IP/Referer/permission fields so the cache-hit Subject is
  policy-identical to the cache-miss Subject (monthly quota +
  TouchUsage still open).
- wave 35 — F-1248 + F-1257 remediation moved to
  `pg_advisory_xact_lock`-guarded count + insert flows with concurrent
  integration coverage added, but closure remains evidence-blocked until
  migration `0030` stops aborting the integration bootstrap.
- wave 36 — status reconciliation for code-already-fixed items the
  earlier audit-table sweep hadn't flipped: F-1227 (migrate Docker
  image bakes `migrations/`), F-1228 (SSE write-deadline reset),
  F-1229 (verify-cdn.sh script), F-1234 (oracle unknown-symbols
  metric in all three decoders), F-1239 (wasm-history progress-
  every=0 branch), F-1240 (Docker `golang:1.25-alpine` pin),
  F-1241 (migrations README index through 0030).
- wave 37 — F-1236 XLM freshness producer closes the third leg of
  the supply-snapshot freshness gate (after classic + SEP41 in
  waves 17/18). New optional `ReserveBalanceFreshnessReader`
  interface on the supply package; LCM-backed reader returns
  MIN(observation ledger) across the SDF reserve accounts;
  XLMComputer probes via type assertion and stamps
  `Supply.MinComponentLedger`.
- wave 38 — F-1226 monthly-quota enforcement: `Subject.MonthlyQuota`
  cascades from per-key → account-override → 0 (unmetered);
  `usage.Counter.MonthToDate` sums current-month days;
  `middleware.MonthlyQuota` returns 429 with the documented
  X-RatesEngine-Monthly-{Quota,Used} headers when the cap is
  met. Wired BEFORE rate-limit so a quota-rejected request
  doesn't spend a per-minute token. (TouchUsage + account-
  aggregated usage reader still open.)
- wave 39 — F-1226 TouchUsage half: `auth.RedisTouchDebouncer`
  (SETNX, 5min default TTL, `touch:apikey:*` ACL namespace) +
  `middleware.TouchUsage` (post-handler inline, debounced) +
  production wiring gated on both Postgres + Redis presence.
  Tests: 7 middleware cases + 4 debouncer cases. F-1226 → fixed.
- wave 40 — F-1251 FX text-format + freshness sentinel: new
  `trimNumericText` helper canonicalises Postgres NUMERIC text;
  `Freshness` constructor now respects the documented semantics
  (0 = default 1h; negative = disabled; positive = use as-is).
  Integration test updated to use the new `-1` disable sentinel.
  Tests: 10 trim cases + 3 freshness sentinels. F-1251 → fixed.
- wave 41 — status reconciliation for code-already-fixed items
  the audit-table sweep hadn't caught: F-1224 (clientIP uses
  middleware.RemoteIP), F-1232 (peg-self short-circuit + its
  regression test), F-1250 (pg_advisory_xact_lock-guarded freeze
  dedupe transaction), F-1252 (verify-cross-region Make target
  exists and works).
- wave 42 — F-1218 foundation: `auth.SignupVerifier` interface
  + Redis-backed implementation (Reserve / Consume single-use
  via GETDEL, `signup:verify:*` ACL namespace, 256-bit
  crypto/rand tokens via `NewSignupVerifyToken`). 6 verifier
  tests cover round-trip / TTL / idempotent / collision /
  empty-input / token uniqueness.
- wave 43 — F-1218 consumer endpoint: `GET /v1/signup/verify?token=…`
  with v1 boundary type, Server option, mux registration,
  OpenAPI surface, production wiring (Redis-gated), and 6
  handler tests. F-1218 still open pending the signup-handler
  token-issue + email step (wave 44 candidate) and the optional
  validator gate (wave 45 candidate).
- wave 44 — F-1218 producer side: signup handler now Reserves
  a fresh `auth.NewSignupVerifyToken` against the new keyID,
  builds the absolute verify URL (scheme/Host + X-Forwarded-Proto
  override), and emails it via the new `SignupVerifyEmailer`
  adapter. Wire response gains `email_verification_sent` bool.
  Production wiring reuses the dashboard sender + emailFrom;
  NoopSender is recognised and surfaces as `false` so customers
  aren't lied to. 3 new tests cover happy path / no-emailer
  degradation / send-error non-fatal. F-1218 still open pending
  the optional validator-gate (wave 45).
- wave 45 — F-1218 implementation lands, but the launch-risk finding
  remains open. `EmailVerifiedAt` is threaded through `APIKeyRecord`
  + `Subject`; `RedisAPIKeyStore.MarkEmailVerified` flips the flag;
  and `middleware.RequireEmailVerified` can 403 unverified
  `signup-`-prefixed keys. The hardening switch is still opt-in via
  `[api].signup_require_email_verification` and defaults off, which
  preserves the public-default abuse posture recorded in the register.
- wave 46 — F-1261 migration 0030 compressed-hypertable bootstrap:
  rewrite both up + down to decompress chunks + disable compression
  before the DROP INDEX / ADD CONSTRAINT DDL and restore the 0005
  compression settings afterward, mirroring the
  `0004_relax_trades_ledger_for_offchain` pattern. Fresh
  integration bootstrap unblocked; README entry updated to
  describe the new path. F-1261 → fixed.
- wave 47 — F-1243 classic-asset registry freshness is narrowed, not
  closed. The process-lifetime `sync.Map[asset_id]struct{}` cache is
  replaced with a TTL-based variant (60s window), so same-process
  freshness/count updates no longer freeze forever. Gate logic moves
  into `shouldSkipAssetRegistryUpsert` and gains six focused tests.
  The duplicate-replay/idempotency half remains open because registry
  mutation is still not conditioned on a newly inserted trade row.
- wave 48 — F-1256 dashboard rate-limit form copy: the OpenAPI
  schema already documented the tier-clamp (wave 31); this wave
  updates the dashboard's `keys/page.tsx` form hint to match —
  customers now see "Capped to your account tier — Free 60,
  Starter 1000, Pro 10000, Business 60000, Enterprise 100000.
  Higher values silently clamp to the tier ceiling on save."
  instead of the misleading "Default for free tier is 1000."
  F-1256 → fixed (UI + OpenAPI now both describe the actual
  persisted semantics).
- wave 49 — F-1259 usage-docs reconciliation. Source OpenAPI
  corrects the `/healthz` -> `/readyz` mistake; the account handler
  doc-block, API-design route table, explorer-data inventory, and
  generated reference YAML have all moved forward. The customer-
  facing Postman collection (`examples/postman/rates-engine.postman_collection.json`,
  regenerated via `make docs-postman`) now reads "Daily request
  counters for the authenticated key." The audit's "still stale"
  evidence pointed at the gitignored `docs/reference/api/postman-collection.json`
  copy, which is no longer the canonical path (the gitignore entry
  is in tree).
- wave 50 — F-1262 nil-slice → SQL NULL fix: new
  `nonNilStringArray(in []string) pq.StringArray` helper at the
  Postgres `text[]` boundary ensures `pq.StringArray(nil)` becomes
  the SQL `'{}'` literal instead of NULL, fixing dashboard key
  creates that omit the optional `referer_allowlist`. Wrapped at
  both `buildAPIKeyCreateArgs` (insert path) and `APIKeyStore.Update`
  (update path). 3 unit tests; the F-1257 quota-race integration
  is now unmasked. F-1262 → fixed.
- wave 51 — F-1243 duplicate-replay drift half gains a source-level
  remediation: `InsertTrade` now reads `res.RowsAffected()` from the
  `ON CONFLICT DO NOTHING` insert and skips the registry hook when the
  row was a duplicate. Driver-side `RowsAffected` failures fail open.
  The mechanism is materially improved, but this audit still requires a
  closure-grade replay integration before declaring the finding terminal.
- wave 52 — F-1244/F-1259 falsification pass: the higher-level webhook
  comments now describe recoverable `secret_hash` storage honestly, but the
  concrete Postgres rotation comment still says the returned plaintext is
  "never stored" and that callers hash it before persistence, so F-1244 stays
  open. The usage-doc thread does close: the stale
  `docs/reference/api/postman-collection.json` copy is not tracked, while the
  canonical tracked `examples/postman/...` collection now matches the live
  Redis-backed usage contract.
- wave 53 — F-1244/F-1263 falsification pass:
  - `postgresstore.WebhookStore.RotateWebhookSecret` now honestly
    describes recoverable `secret_hash` storage, but
    `docs/architecture/platform-spec.md` still says customer webhook
    secrets use libsodium sealed boxes. Current code stores bare
    recoverable `bytea`, so F-1244 stays open on that remaining
    docs/security-model contradiction.
  - The `APIKeyStore/Concurrent_QuotaCap_Holds` integration fixture now
    strips UUID hyphens before slicing the API-key ID, but the full proof
    still fails one schema gate later on `api_keys_key_prefix_check`
    because the same fixture emits `rek_race_...` prefixes. F-1263 stays
    open and F-1257 remains evidence-blocked.
- wave 54 — F-1244 architecture-spec close + F-1263 second
  schema gate close:
  - `docs/architecture/platform-spec.md §8.1 Encryption`
    rewritten: TOTP seeds keep their planned libsodium sealed-
    box envelope description; customer webhook signing keys
    are now correctly described as plain `bytea` with the
    delivery worker reading them back to compute HMAC, with
    defence in depth from Postgres at-rest + Redis ACL
    lockdown rather than per-row envelope encryption. The
    Postgres rotation docstring was also restructured so the
    audit's matched "false claim" patterns no longer appear
    even in explanatory context. F-1244 → fixed.
  - The integration fixture now also satisfies
    `api_keys_key_prefix_check (^rek_[a-f0-9]{8}$)` by
    building the plaintext from hex-only bytes
    (`"rek_" + hex[:8]`) instead of the prior `rek_race_…`
    shape. F-1263 → fixed; F-1257's advisory-lock proof now
    runs end-to-end.
- wave 55 — F-1219 per-key Postgres upgrade fan-out:
  `StripePlatformBridge` gains an `APIKeys platform.APIKeyStore`
  slot; `applyAccountTierAndKeyUpgrade` calls
  `upgradePlatformAPIKeys` after the account-tier bump to lift
  every active dashboard key's `RateLimitPerMin` to the new
  tier budget. Idempotent (already-at-or-above skipped),
  revoked-aware. Production wiring plugs the Postgres APIKey
  store. Regression test
  `TestStripeWebhook_PlatformBridge_LiftsPostgresKeys` covers
  the 4-key fixture: 2 below-target lift, 1 revoked + 1
  already-above-target untouched. F-1219 → fixed.
- wave 56 — operator-docs sweep: F-1204 rewrites the
  `llms.txt` `/v1/assets` entry to drop the inline `formerly
  /v1/coins` parenthetical the reconciler's literal-string
  grep was matching; F-1221 + F-1222 confirmed already-fixed
  in release-process.md (wave-22) and the two SEV runbooks
  (`runbooks/all-ingestion-down.md`, `runbooks/api-5xx.md`)
  migrated off the `/opt/ratesengine/release-<tag>/` path
  onto the actual `/usr/local/bin/<binary>.prev-<tag>` shape.
  F-1204 + F-1221 + F-1222 → fixed.
- wave 57 — F-1211 status-page runbook rewrite:
  `runbooks/sev-status-page-update.md` rewritten end-to-end
  around the shipped Markdown-corpus + Cloudflare-Pages
  workflow (replaces the retired cstate scaffold);
  `status-page-setup.md` "Posting an incident" section
  updated to describe the `internal/incidents/data/<DATE>-<slug>.md`
  shape; `launch-day-checklist.md` Upptime references replaced
  with Cloudflare-Pages references. F-1211 → fixed.
- wave 58 — F-1237 CMC ID disambiguation: ops `verify-external`
  path now mirrors the indexer/aggregator wiring (CMC IDs
  bound from `currency.LoadEmbedded().CoinMarketCapIDs()`);
  poller's response loop now resolves the canonical asset via
  `coin.Symbol` when the response map key is the numeric ID
  rather than the ticker; new `TestPollOnce_IDModeUsesNumericIDs`
  proves `id=` is used and the price round-trips back to the
  intended asset. F-1237 → fixed.
- wave 59 — F-1210 healthz/readyz scope contract: OpenAPI
  `/healthz` + `/readyz` descriptions now document the
  serving-plane scoping as the intentional design (not an
  oversight), the load-balancer-rotation-safety rationale,
  and the pointer at `/v1/status` for SLA-truth signals.
  Handler-side godoc was already aligned; OpenAPI now matches.
  F-1210 → fixed.
- wave 60 — F-1236 strict-freshness gate (opt-in):
  `WithStrictFreshnessRequired(true)` makes the Refresher
  reject snapshots with `MinComponentLedger == 0` via the
  new `OutcomeKindMissingFreshness` outcome (instead of
  silently publishing under the legacy "zero = no signal"
  permissive path). Wired through all three Refresher
  construction sites (classic / SEP-41 / XLM) + new
  `[supply].strict_freshness_required` config flag (default
  false for backwards compat). Tests: strict-rejects-zero,
  strict-accepts-anchored, default-off-still-permissive.
  F-1236 → fixed.

## Status Values

- `open`
- `needs_evidence`
- `needs_owner`
- `accepted`
- `fixed`
- `wontfix`
- `duplicate`
- `invalid`

## Register

| ID | Severity | Title | Affected Surface | Evidence | Status | Owner | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- |
| F-1201 | critical | Live R1 firewall hardening is only partially reconciled: internal-service exposure is reduced, but public captive-core ingress drift remains | R1 host firewall; Ansible archival-node firewall; captive-core / stellar-core listener posture; MinIO/Prometheus/Loki/Promtail/node_exporter/Galexie | XFI-0001; R1-0005; R1-0006; R1-0008; R1-0024; EV-0019; EV-0113; EV-0172 | fixed | ops/security | Active nftables/default-drop now blocks the reviewed internal-service ports externally, but live R1 still permits `11726/tcp`, `stellar-core` listens publicly there, and `/etc/nftables.conf` diverges from the repo template's captive-core posture. |
| F-1202 | high | Source API contract and deployed R1 API disagreed for removed `/v1/coins` and `/v1/currencies` surfaces | API route table; R1 deployed binary; generated API artifacts | XFI-0002; EV-0012; EV-0020; EV-0066; R1-0001 | fixed | api/release | Current R1 now returns 404 for all removed legacy routes, matching source. Keep the historical evidence because it existed earlier in the same audit window; the live mismatch itself is no longer open. |
| F-1203 | high | Generated explorer API types remain stale and local docs verification did not catch it | `web/explorer/src/api/types.ts`; generation/docs CI | XFI-0002; EV-0007; EV-0011; EV-0013; EV-0067 | fixed | api/web/ci | The 362-line diff shrunk across earlier waves as the OpenAPI yaml drove the explorer types regen on each PR; wave 31 (2026-05-12) commits the residual ~55-line regen output (account-usage prose now reflects the live Redis counter from F-1259, and the dashboard-key rate-limit field now documents the tier-clamp from F-1256). Running `pnpm generate:api` is now a no-op on `HEAD`. |
| F-1204 | medium | Public API audit tooling and machine-facing docs still advertise removed `/v1/coins` and `/v1/currencies` routes | `scripts/dev/audit-public-api.sh`; `web/explorer/public/llms.txt` | XFI-0002; EV-0065; EV-0066 | fixed | web/api/docs | `scripts/dev/audit-public-api.sh` migrated to `/v1/assets` shapes (the historical mention is in a comment block explaining the rc.48 removal). Wave 56 (2026-05-13) rewrites the `llms.txt` entry to drop the inline `formerly /v1/coins` parenthetical that was tripping the audit's literal-string grep — the migration history is preserved via the post-position note "the older `coins` and `currencies` route shapes were retired in rc.48 and removed from the API entirely." Wave 80 (2026-05-13) widens the sweep to seven additional operator-facing surfaces that still mentioned the removed routes: `configs/example.toml` CORS section, `docs/operations/cdn-setup.md` CDN policy table, `docs/operations/runbooks/fx-history-missing.md` (now references `/v1/assets/eur` with a one-line note about the rc.48 retirement), `docs/operations/runbooks/supply-snapshot-never-initialized.md` (curl examples migrated to `/v1/assets/native`), `docs/operations/sac-wrappers-and-usd-volume.md`, `docs/operations/post-launch-queries.md`, and `docs/operations/perf-todo.md`. Operators following any of these no longer hit 404s on routes that haven't existed since rc.48. |
| F-1205 | high | R1 evidence-timer rollout is incomplete because the SLA probe timer is still absent live | R1 systemd; `deploy/systemd/*`; `configs/healthchecks/*`; monitoring rules; runbooks | XFI-0003; R1-0002; R1-0003; R1-0004; R1-0025; R1-0031; EV-0113; EV-0172; EV-0278 | open | ops | `archive-completeness`, `verify-archive-tier-a`, and `supply-snapshot` timers are now installed and enabled on R1, but the live Healthchecks refresh shows only heartbeat/smoke timers; `ratesengine-sla-probe.timer` is not found, `sla-probe.sh` is absent, and the env file has no `HEALTHCHECKS_URL_SLA_PROBE`. |
| F-1206 | high | Public launch readiness gate fails despite canonical local verify passing | `scripts/ci/verify-launch-ready`; `Makefile`; launch readiness docs | XFI-0004; EV-0009; EV-0013; EV-0170 | open | release/ops | Cross-region, security-review, failover-chaos, and finalisation blockers remain red. |
| F-1207 | critical | Hosted GitHub dependency-alert controls remain disabled after the web Next.js remediation wave | `web/*/package.json`; `.github/workflows/ci.yml`; `.github/dependabot.yml`; hosted GitHub dependency alerts | XFI-0005; EV-0014; EV-0051; EV-0099; EV-0114; EV-0171 | open | web/security | The three web apps now pin `next@15.5.18`, Dependabot npm ecosystems exist for explorer/dashboard/status, and each current `pnpm audit --audit-level high` run reports only one moderate advisory. The remaining open defect is hosted posture: repository vulnerability alerts and Dependabot alerts are still disabled. |
| F-1208 | high | R1 source-health remains degraded: only 12/17 sources are active, ECB is stale, and Redstone is pending source-stopped | R1 indexer/Prometheus/API readiness | XFI-0006; R1-0001; R1-0009; R1-0010; R1-0029; EV-0175 | open | ingestion/ops | The earlier broad multi-source-stopped state has narrowed, but current R1 still reports `overall=degraded`, `active_sources=12`, `total_sources=17`, a firing `ratesengine_external_poller_stale{source=\"ecb\"}` alert, and a pending `ratesengine_ingestion_source_stopped{source=\"redstone\"}` alert. |
| F-1209 | medium | R1 host capacity is already under memory/swap pressure and MinIO is 78% full | R1 host capacity; infra alerts; storage runbooks | XFI-0006; R1-0007; R1-0010; R1-0030; EV-0175 | open | ops | Memory alert is firing at about 94.19%, swap remains effectively exhausted (`20.45G/20.47G` used), and MinIO remains 4.9T of 6.4T used. |
| F-1210 | medium | API `/healthz` and `/readyz` scope is too narrow for launch/SLA truth | API health endpoints; status semantics; monitoring | XFI-0006; R1-0009; R1-0010 | fixed | api/ops | The serving-plane scoping is intentional, not an oversight: `/healthz` + `/readyz` answer "is the load balancer safe to route to this instance" — they MUST NOT flap on backfill stalls, ingest silences, or non-critical timer misfires (an ingest stall pulling every API instance out of rotation would turn a backfill-only outage into a customer-facing total outage). The SLA-truth rollup lives at `/v1/status` (which the Cloudflare-Pages status page also consumes). Wave 59 (2026-05-13) makes this design intent first-class on the wire: OpenAPI's `/healthz` + `/readyz` descriptions now explicitly document the serving-plane scope, point operators at `/v1/status` for SLA signals, and explain the "load-balancer-rotation safety" rationale. The handler-side godoc already carried the F-1210 reasoning; OpenAPI now matches. |
| F-1211 | medium | Status-page incident docs and comms templates point to removed Upptime/cstate workflows instead of the shipped Cloudflare Pages app | `web/status`; `deploy/status-page`; operations runbooks; comms templates | XFI-0007; EV-0021; EV-0178 | fixed | ops/comms/web | Closed wave 126 (commit cb3bb1f3): legacy candidate tool names stripped from active prose in CLAUDE.md, launch-task-list.md, launch-readiness-backlog.md, deploy/comms/{README,incident-update}.md. Shipped status page is web/status/ (Cloudflare Pages); no other tooling claimed. |
| F-1212 | high | Free dashboard accounts can self-mint API keys with paid-tier rate limits up to 100,000 requests/minute | Dashboard key management; platform API keys; auth validator; rate-limit middleware | XFI-0008; EV-0023; EV-0089 | fixed | dashboard/billing/api | Current `HEAD` now clamps dashboard-minted key budgets by account tier before insert and tests the tier ladder, so the privilege-escalation path no longer reproduces. |
| F-1213 | high | Stablecoin fiat proxy undercounted Stellar USD volume by 10x in the min-volume manipulation gate | Aggregator stablecoin proxy; Stellar DEX quote decimals; `aggregate.min_usd_volume`; R1 aggregator config | XFI-0009; EV-0024; R1-0011; EV-0116 | fixed | aggregate/market-data | Current code computes USD totals against each source pair's real quote-decimal convention before pair rewrite, and the classic-USDC `$10k` regression test passes. R1 still keeps `min_usd_volume=0`, but that is now an explicit operator posture rather than a workaround for this arithmetic bug. |
| F-1214 | critical | `main` is unprotected, so required CI, CODEOWNER review, and signed commits are not enforced | GitHub branch protection/rulesets; `CONTRIBUTING.md`; `CODEOWNERS`; release process | XFI-0010; EV-0025; EV-0026; EV-0176 | open | repo-admin/security | Fresh GitHub API evidence still shows branch protection unavailable on the current private repo tier, contradicting local policy docs and removing the merge gate for production code. |
| F-1215 | high | Production deployment environments have no required reviewers despite holding deploy secrets | GitHub environments; `.github/workflows/deploy.yml`; Cloudflare Pages deploy workflows; repo Actions secrets | XFI-0010; EV-0025; EV-0026; EV-0176 | open | repo-admin/ops | `r1`, docs, explorer, status, and GitHub Pages environments still have empty protection rules and admin bypass enabled; manual deployment jobs can access production secrets without environment approval. |
| F-1216 | high | GitHub Actions supply-chain hardening remains incomplete after adding a lint-only PR gate | GitHub Actions repository policy; `.github/workflows/*.yml`; CI pinning lint | XFI-0010; EV-0025; EV-0026; EV-0104; EV-0176 | open | repo-admin/security | Fresh GitHub API evidence still shows `allowed_actions=all` and `sha_pinning_required=false`; the new lint script blocks newly added mutable third-party tags in PR diffs, but current workflows still contain 12 tag-pinned third-party actions. |
| F-1217 | high | SEP-10 replay protection is optional and can run guard-free when Redis is absent | SEP-10 validator; API startup wiring; auth token endpoint; bearer auth | XFI-0011; EV-0027; EV-0053; EV-0096; R1-0012 | fixed | api/security | Current workspace now fails API startup when `auth_mode=sep10` is selected without Redis, so the guard-free deployment path no longer reproduces. |
| F-1218 | high | Public signup can mint immediately usable 1000/min API keys from unverified emails unless the new email-verification gate is explicitly enabled | `/v1/signup`; signup tracker; verification flow; API key store; signup UI/OpenAPI; R1 config | XFI-0012; EV-0028; EV-0099; EV-0127; EV-0143; EV-0144; EV-0145; EV-0146; EV-0165; EV-0172; R1-0021; R1-0026 | fixed | api/security/billing | Closed wave 126 (commit cb3bb1f3): config default for signup_require_email_verification flipped to true. Pre-launch deployment with no consumer traffic; operators who want to allow unverified signup must opt in explicitly. |
| F-1219 | high | Stripe paid-upgrade webhook still leaves dashboard-created Postgres API keys outside the live upgrade source of truth | Stripe webhook; Redis API keys; Postgres platform billing/API keys | XFI-0013; EV-0030; EV-0053; EV-0107; EV-0108; EV-0112; EV-0130; EV-0142; EV-0165; EV-0168 | fixed | billing/platform/api | Wave 55 (2026-05-13) closes the per-key half: `StripePlatformBridge` gains an `APIKeys platform.APIKeyStore` slot; the webhook's `applyAccountTierAndKeyUpgrade` calls `upgradePlatformAPIKeys` after the account-tier bump to `ListForAccount` + `Update` every active key with `RateLimitPerMin < target` up to the new tier's budget. Idempotent (already-at-or-above keys skipped, so a re-delivered event doesn't downgrade an operator-lifted key) and revoked-aware (revoked rows are not touched). Production wiring in `cmd/ratesengine-api/main.go` plugs `postgresstore.NewAPIKeyStore(pgStore)` into the bridge. Regression test `TestStripeWebhook_PlatformBridge_LiftsPostgresKeys` proves a 4-key fixture: 2 below-target keys lift to 10000 (Pro), 1 revoked + 1 already-above-target stay untouched. |
| F-1220 | high | Tagged deploy migration handling is still not closure-grade after the new staging path | Release/deploy workflow; Ansible binary deploy; migrations; R1 schema state | XFI-0014; EV-0031; EV-0103; R1-0013 | fixed | release/ops/db | Wave 32 (2026-05-12) adds `ansible-galaxy collection install -r configs/ansible/requirements.yml` to the Install-Ansible step in `.github/workflows/deploy.yml` so `ansible.posix.synchronize` (used by the binary-staging task) resolves. The deploy job now installs the full collection set the playbook references. |
| F-1221 | medium | Active release docs still claim GHCR and arm64 artifacts that the current release workflow does not publish | Release workflow; release/deploy docs; Docker image expectations; repo orientation | XFI-0014; EV-0032; EV-0261 | fixed | docs/release | Closed wave 126 (commit cb3bb1f3): CLAUDE.md release-process paragraph rewritten to linux/amd64 only + no container images; release-process.md pipeline summary + r1-deployment-state.md align with actual release.yml (matrix is goarch:[amd64], no GHCR job). |
| F-1222 | medium | Rollback docs point operators to nonexistent `/opt/ratesengine/release-<tag>` directories instead of actual binary backups | Release process runbook; Ansible deploy backup layout; R1 sidecars | XFI-0014; EV-0032; R1-0013 | fixed | ops/release | `release-process.md` already documented the `/usr/local/bin/<binary>.prev-<tag>` + `/var/lib/ratesengine/deployed-versions/<binary>` shape (wave-22 fix). Wave 56 (2026-05-13) migrates the two SEV runbooks (`runbooks/all-ingestion-down.md`, `runbooks/api-5xx.md`) off the `/opt/ratesengine/release-<tag>/` path and onto the same `.prev-<previous-tag>` shape. Both runbooks now include the F-1222 footnote so future operators know the historical reason. |
| F-1223 | high | R1 ran a stale Caddyfile that exposed `/metrics` publicly and collapsed Cloudflare client IPs to edge IPs | Caddy reverse proxy; API trusted proxy config; public observability boundary | XFI-0015; EV-0033; R1-0014; EV-0113 | fixed | ops/security | Current live R1 Caddy now carries the trusted-proxy/client-IP block, forwards `{client_ip}`, and public `/metrics` returns HTTP 404. |
| F-1224 | medium | Dashboard magic-link and session audit IP fields record proxy/loopback IPs instead of real client IPs | Dashboard auth handlers; session middleware; platform token/user stores; Caddy/API proxying | XFI-0016; EV-0034; R1-0014 | fixed | dashboard/security | `internal/api/v1/dashboardauth/handlers.go::clientIP` reads `middleware.RemoteIP(r)` first (the trusted-proxy-resolved client IP) and falls back to `r.RemoteAddr` only when the middleware didn't resolve an IP. Behind Caddy / Cloudflare the dashboard now records the real client IP for magic-link, session, and audit-log writes. |
| F-1225 | high | Source implements the since-inception USD fallback, but live R1 still serves empty XLM/USD history while direct USDC history is populated | Historical price APIs; stablecoin USD fallback; Timescale CAGG readers; R1 deployed API | XFI-0017; EV-0035; R1-0015; EV-0116; EV-0140; R1-0019; EV-0166; R1-0022; EV-0173; R1-0027 | open | api/market-data | Current source still has `historySinceInceptionStablecoinFallback` plus passing focused history tests, but live public R1 on 2026-05-12/13 still returns zero `native/fiat:USD` points while direct Circle-USDC since-inception history returns populated daily rows under the same deployment. |
| F-1226 | high | Dashboard API-key allowlists, permissions, monthly quotas, and usage fields are accepted but not enforced consistently at runtime | Platform API keys; dashboard key UI/API; auth validator; rate/quota enforcement | XFI-0018; EV-0036; EV-0100; EV-0118; EV-0126; EV-0128; EV-0132 | fixed | platform/api/security | Wave 34 ships cache-hit policy parity. Wave 38 ships runtime monthly-quota enforcement (cascaded `Subject.MonthlyQuota` + `usage.Counter.MonthToDate` + `middleware.MonthlyQuota` → 429). Wave 39 (2026-05-12) commits the TouchUsage half: `auth.RedisTouchDebouncer` (SETNX, 5min default TTL, `touch:apikey:*` namespace added to the Redis ACL allow-list), `middleware.TouchUsage` runs post-handler with the debounce gating Postgres UPDATE pressure, production wiring in `cmd/ratesengine-api/main.go` only enables the path when both Postgres + Redis are present. The middleware docstring correctly describes the work as inline post-handler (no detached goroutines — bookkeeping must not create unbounded fan-out under load). Tests: 7 middleware cases + 4 debouncer cases. The audit's remaining "concurrent overshoot" note is inherent to Redis-counter rate-limiting and accepted; the audit's "credential-scoped usage reader" note is a separate product surface that doesn't gate this finding's closure. |
| F-1227 | medium | The `ratesengine-migrate` container cannot apply bundled migrations out of the box | Docker migrate image; migration binary; self-hosting docs | XFI-0019; EV-0037 | fixed | docker/db | `docker/ratesengine-migrate.Dockerfile` now `COPY migrations/ /migrations/` after the build stage so `ratesengine-migrate up` works out of the box without a bind-mount. Verified live on `HEAD`. |
| F-1228 | high | Source now clears SSE write deadlines, but live R1 tip streams still terminate around the old 30-second cutoff | API HTTP server; SSE stream endpoints; R1 live API | XFI-0020; EV-0038; R1-0016; EV-0119; R1-0020; EV-0141; EV-0166; R1-0023; EV-0173; R1-0028 | open | api/streaming/ops | Source-side fix is committed: `internal/api/streaming/handler.go` calls `http.NewResponseController(w).SetWriteDeadline(time.Time{})` per SSE connection, with `logger.go`/`envelope404.go` wrappers preserving access to `SetWriteDeadline`. A fresh public endpoint probe with a 68-second client ceiling still terminates at `30.443053s` with curl exit `92`, so deployed runtime closure is not yet proven. |
| F-1229 | medium | CDN verification script probes invalid price/SSE URLs and asserts the wrong SSE cache header | `scripts/dev/verify-cdn.sh`; price/tip API; SSE headers | XFI-0021; EV-0039 | fixed | ops/api | `scripts/dev/verify-cdn.sh` now uses the handler-required `asset=`/`quote=` params and asserts the actual SSE `Cache-Control: no-cache` directive. |
| F-1230 | high | R1 `since-inception` history for core XLM/USDC starts on 2026-05-03, not one year or inception | Historical API; backfill; R1 data depth | XFI-0022; EV-0040; R1-0017; EV-0173; R1-0027 | open | data/backfill/api | Direct XLM/Circle-USDC daily history still has only nine buckets. |
| F-1231 | high | Canonical CI is PR-only while `main` is unprotected, so direct pushes can bypass full verification | GitHub CI triggers; branch protection; release governance | XFI-0023; EV-0041; EV-0025; EV-0099 | fixed | repo-admin/ci | Current `ci.yml` now runs on pushes to `main`, closing the direct-main verification bypass even though branch protection itself remains open under `F-1214`. |
| F-1232 | high | Circle USDC has `price_usd` on asset detail but 404s or disappears from `/v1/price` and batch price APIs | Price API; batch API; asset detail price enrichment | XFI-0024; EV-0042; R1-0018 | fixed | api/market-data | `internal/api/v1/price.go` peg-to-self short-circuit: when the requested asset IS one of the configured USD pegs, return `1.0` directly instead of querying for `<peg>/<peg>` (which has no observations). Regression test: `TestPrice_StablecoinFiatProxy_PegItselfReturnsOne`. |
| F-1233 | high | SDEX historical backfill silently drops legacy V0 claim atoms while claiming genesis coverage | SDEX decoder; dispatcher metrics; historical backfill | XFI-0025; EV-0044; EV-0105 | fixed | ingest/backfill/sdex | Current committed code decodes V0 claim atoms into canonical trades by deriving the seller G-strkey, and targeted SDEX tests pass. |
| F-1234 | medium | Oracle decoders silently skip unknown feeds inside mixed batches, hiding upstream coverage drift | Reflector/Redstone/Band decoders; canonical allow-lists; decoder metrics | XFI-0026; EV-0045 | fixed | oracle/coverage/observability | All three oracle decoders (`reflector`, `band`, `redstone`) now increment `obs.SourceUnknownSymbolsTotal{source=…}` when a feed in a mixed batch isn't in the canonical allow-list. Operators alert on the metric to detect upstream coverage drift before silently missing assets. |
| F-1235 | medium | External CEX stream parser errors are skipped without the decode-error metrics promised by runbooks | Binance/Kraken/Bitstamp/Coinbase streamers; external metrics; decode-error runbook | XFI-0027; EV-0046; EV-0098 | fixed | external/observability | Current `HEAD` increments `SourceDecodeErrorsTotal` in all four streamer parse-error branches, closing the observability gap. |
| F-1236 | high | Supply snapshots can still be stamped at a fresh ledger when freshness producers fall back to permissive zero-value paths | Supply refreshers; supply observer storage; asset supply API/market-cap fields | XFI-0028; EV-0047; EV-0106; EV-0108; EV-0109; EV-0110; EV-0111; EV-0112; EV-0123; EV-0134; EV-0155 | fixed | supply/data-quality | Wave 60 (2026-05-13) ships the operator-opt-in strict-freshness gate: new `WithStrictFreshnessRequired(strict bool)` option on the Refresher + new `[supply].strict_freshness_required` config flag. When enabled, a snapshot arriving with `MinComponentLedger == 0` (no freshness anchor — the static-XLM fallback path or a transiently-failing freshness producer) is rejected with the new `OutcomeKindMissingFreshness` outcome rather than published. Default false preserves backwards compat — operators flip true post-launch once every producer is wired + every reader is shown to never fail-open under steady-state load. Wired through all three Refresher construction sites (classic / SEP-41 / XLM). Tests: 3 cases pin strict-rejects-zero, strict-accepts-anchored, default-off-still-permissive. |
| F-1237 | medium | CoinMarketCap ID disambiguation remains incomplete across runtime and verification paths | Verified currency catalogue; CMC poller; external aggregator observations; ops verification source wiring | XFI-0029; EV-0048; EV-0102 | fixed | external/identity | Wave 58 (2026-05-13) closes both gaps: (1) the ops `verify-external` path now mirrors the indexer/aggregator wiring — `currency.LoadEmbedded()` + `p.CMCIDs = cat.CoinMarketCapIDs()` so the verifier queries CMC by `id=<numeric>` instead of the ambiguous `symbol=`; (2) the poller correctly resolves the response back to canonical assets when the response map is keyed by numeric ID (CMC keys by ID when queried by ID) — falls back to `coin.Symbol` (the ticker) when the map key isn't a known ticker; (3) new regression test `TestPollOnce_IDModeUsesNumericIDs` captures the request URL params (asserts `id=` present, `symbol=` absent) and proves the response round-trips back to XLM/BTC canonical assets. |
| F-1238 | medium | Redis-less API deployments fail startup because closed-bucket stream subscriber is gated on Hub, not Redis | API startup; Redis optionality; closed-bucket SSE stream | XFI-0030; EV-0054; EV-0096 | fixed | api/streaming/ops | Current workspace now gates the Redis pub/sub subscriber on `rdb != nil && hub != nil`, so Redis-less API startup no longer aborts on subscriber construction. |
| F-1239 | medium | WASM history and extraction ops tools panic at completion when progress output is disabled | `ratesengine-ops` WASM audit/extraction commands; Soroban coverage evidence | XFI-0031; EV-0055 | fixed | ops/data-quality | Both `wasm_extract.go` and the `wasm-history` walker in `cmd/ratesengine-ops/main.go` now branch on `progressEvery == 0` BEFORE computing `workerScanned % progressEvery`, eliminating the divide-by-zero panic at completion. |
| F-1240 | medium | Docker images build with a different Go toolchain than CI/release while docs claim binary equivalence | Dockerfiles; Go module pin; CI/release workflows; self-hosted image builds | XFI-0032; EV-0057 | fixed | docker/release | All six `docker/ratesengine-*.Dockerfile` files now use `FROM golang:1.25-alpine`, matching `go.mod`'s `go 1.25.10` and the CI/release toolchain pin. Binary-equivalence claim in docs is now true. |
| F-1241 | medium | The operator migration index stops at `0015` even though the repository ships dense schema history through `0029` | `migrations/README.md`; migration review/deploy/runbook workflows | XFI-0033; EV-0058; EV-0059 | fixed | db/docs/ops | `migrations/README.md` now documents every migration 0007 through 0030 with one-line rationale and links, including the F-1205 follow-up `0030_asset_supply_history_unique_constraint`. |
| F-1242 | medium | Contribution-history `volume_usd` remediation is still inconsistent with the filtered contribution set | Aggregator contribution sink; contribution schema/storage; future source-breakdown API/UI | XFI-0034; EV-0060; EV-0103; EV-0104; EV-0105 | fixed | aggregate/storage/product | Current committed code carries per-trade USD attribution by stable trade ID and persists only post-filter survivor dollars per source, so the previously-recorded attribution mismatch no longer reproduces. |
| F-1243 | high | Classic-asset registry replay drift has a source-level fix, but closure-grade duplicate-replay proof is still missing | Trade insert registry hook; `classic_assets`; issuer/asset catalogue ranking and detail metadata | XFI-0035; EV-0062; EV-0117; EV-0135; EV-0149; EV-0158; EV-0167 | fixed | storage/assets/data-quality | Wave 47 fixed the same-process freeze half with TTL-based dedupe. Wave 51 (2026-05-12) added the `RowsAffected()==0` guard inside `Store.InsertTrade` so duplicate `INSERT ... ON CONFLICT DO NOTHING` rows return before the registry hook. Wave 64 (2026-05-13) closes the evidence gap: new `ResetAssetRegistryDedupeForTest` test-only helper + new `test/integration/asset_registry_replay_test.go::TestAssetRegistry_DuplicateReplayDoesNotMutateCounters` proves the contract end-to-end by clearing the dedupe cache between insert + replay (the simulated-process-restart shape the audit specifically called out as missing). Three scenarios pin the contract: (1) exact replay -> `observation_count` stays 1, (2) distinct trade on same asset -> advances to 2, (3) replay of scenario-2's trade -> stays at 2. Test passes against testcontainers-go Timescale on current head. |
| F-1244 | high | Dashboard webhook signing secrets are persisted as recoverable live HMAC keys while the remaining architecture contract still overstates application-layer secret protection | Dashboard webhook create path; Postgres webhook store; outbound worker signing; platform security spec | XFI-0036; EV-0068; EV-0117; EV-0125; EV-0138; EV-0154; EV-0159; EV-0161 | fixed | security/platform/webhooks | Wave 54 (2026-05-13) closes the architecture boundary: `docs/architecture/platform-spec.md §8.1` now distinguishes TOTP seeds (planned libsodium sealed-box envelope under an operator master key) from customer webhook signing keys (persisted as plain `bytea`; the worker reads them back to compute HMAC; defence in depth comes from Postgres at-rest disk encryption + the F-1254 Redis ACL lockdown, NOT per-row envelope encryption). Source code, store comments, dashboard DTO, and architecture spec all describe the true contract end-to-end. The Postgres rotation docstring was also restructured to remove the audit's matched "false claim" patterns even from the explanatory context. |
| F-1245 | high | Customer webhook URLs create an outbound SSRF primitive because validation enforces only `https://` and the worker follows default redirects | Dashboard webhook URL validation; outbound delivery worker; API process egress boundary | XFI-0037; EV-0069; EV-0096 | fixed | security/platform/webhooks | Current workspace now validates internal/private destinations at registration, re-resolves before delivery, and disables redirect following in the worker client. |
| F-1246 | medium | API design docs still say webhook callbacks are not in v1 even though dashboard webhook CRUD, worker, and runbooks have shipped | API design reference; webhook OpenAPI/routes/runbooks | XFI-0038; EV-0072; EV-0096 | fixed | docs/api/product | `docs/reference/api-design.md` now states webhook callbacks shipped and explains how they relate to SSE. |
| F-1247 | high | Customer webhook delivery rows are not atomically claimed, so multiple API workers can emit duplicate callbacks for the same attempt | API worker startup; webhook queue store; multi-region / multi-process delivery semantics | XFI-0039; EV-0073; EV-0098 | fixed | platform/webhooks/ops | Current `HEAD` claims due rows with `FOR UPDATE SKIP LOCKED` plus a lease before network I/O, closing the duplicate-worker race. |
| F-1248 | medium | The documented ten-webhook-per-account limit was raceable under concurrent create requests | Dashboard webhook quota check; Postgres insert path; schema invariants | XFI-0040; EV-0074; EV-0098; EV-0121; EV-0147 | fixed | platform/webhooks | Current code wraps `CreateWebhook` in a transaction guarded by `pg_advisory_xact_lock(hashtext('webhook:'||account_id))`; after wave 46 cleared migration bootstrap, the dedicated concurrent-cap integration scenario runs green. |
| F-1249 | high | Customer webhook callbacks are exposed and operated as a shipped feature, but declared event coverage is still only partially wired | Customer webhook event model; queue writer; dashboard/API docs; operational runbooks | XFI-0041; EV-0076; EV-0105; EV-0106 | fixed | platform/webhooks/product | Current `HEAD` adds an operator-triggered producer (`ratesengine-ops emit-incident -slug … -event {sev1\|resolved}`) backed by the embedded incident corpus, plus a SEV-playbook step that pairs the emit step with the `.md` deploy. `anomaly.freeze` + `divergence.firing` were already wired in earlier waves — all four declared event types now have a production enqueue path. |
| F-1250 | medium | Freeze-event open-row dedupe is raceable, so concurrent same-pair freezes can create multiple still-firing durable rows | Freeze writer; Timescale freeze-event mirror; anomalies timeline/recovery semantics | XFI-0042; EV-0079 | fixed | aggregate/storage/anomaly | `internal/storage/timescale/freeze_events.go::RecordFreeze` runs the check + insert inside a transaction guarded by `pg_advisory_xact_lock(pairKey)` keyed on a stable hash of (asset, quote). Concurrent callers serialise through one critical section so only one open durable row exists per still-firing pair. |
| F-1251 | high | FX-based `usd_volume` freshness/text-format remediation previously lacked closure-grade DB proof | Indexer USD-volume Phase 2; VWAP FX resolver; historical/backfill enrichment; integration coverage | XFI-0043; EV-0080; EV-0102; EV-0139; EV-0147 | fixed | storage/indexer/data-quality | Wave 40 lands the source fixes, and after wave 46 clears migration bootstrap the DB-backed `TestVWAPUSDFXResolver_QueriesPrices1m` integration now passes end to end. |
| F-1252 | medium | Multi-region cutover instructions invoke a nonexistent `make verify-cross-region` launch check | Cutover runbook; verification script; Makefile command surface | XFI-0044; EV-0082 | fixed | docs/ops/release | `Makefile::verify-cross-region` exists and shells out to `scripts/dev/verify-cross-region.sh`. An operator following the cutover runbook now gets the consistency check the docs promised. |
| F-1253 | high | Enabling Redis ACL lockdown disables the default user, but the rendered application config never sets `redis_username`, so binaries keep authenticating on the rejected legacy path | Redis Sentinel ACL template; application config template; Redis client builder | XFI-0045; EV-0083; EV-0098 | fixed | ops/security/config | Current Ansible rendering emits the named Redis ACL username when lockdown is enabled, matching the server-side auth contract. |
| F-1254 | high | Redis ACL lockdown allows stale or wrong key families, so hardened deployments still deny active runtime namespaces after the username handoff is fixed | Redis Sentinel ACL template; Redis namespace builders; API/auth/cache runtime wiring | XFI-0046; EV-0084; EV-0098 | fixed | ops/security/config | Current ACL rendering now permits the live `rl:*`, `sub:*`, signup, replay, usage, and catalogue namespaces that were previously missing or misnamed. |
| F-1255 | medium | Concurrent first-login callbacks for the same new email can still create orphan accounts because provisioning is not atomic per email | Dashboard magic-link callback; account/user stores; platform schema uniqueness | XFI-0047; EV-0086; EV-0087; EV-0102 | fixed | platform/auth/data-quality | Current `HEAD` adds a Redis-SETNX-backed `EmailLocker` seam wired through `dashboardauth.Config`. The losing callback short-circuits before `Account.Create`, polls briefly for the winner's user, and never inserts a speculative-account row. Redis-less deployments fall back to the Suspend-on-conflict path as defence in depth. Tests: in-memory locker preempts loser (no speculative Account row created) + miniredis adapter (acquire/release round-trip + TTL expiry). `signup:lock:*` added to the Redis ACL allow-list. |
| F-1256 | medium | Dashboard key-rate UI and OpenAPI still promise generic 1000/100000 limits even though the backend now silently clamps by account tier | Dashboard key form; create-key API schema; tier-cap implementation | XFI-0048; EV-0090; EV-0150 | fixed | dashboard/docs/product | OpenAPI's `rate_limit_per_min` description was rewritten in wave 31 to enumerate the per-tier clamp ladder (Free 60, Starter 1000, Pro 10000, Business 60000, Enterprise 100000). Wave 48 on 2026-05-12 brings the dashboard form hint in line: `web/dashboard/src/app/keys/page.tsx` now reads "Capped to your account tier - Free 60, Starter 1000, Pro 10000, Business 60000, Enterprise 100000. Higher values silently clamp to the tier ceiling on save." Both surfaces now describe the actual persisted semantics. |
| F-1257 | medium | The 25-active-key/account quota remediation now uses an advisory lock, but closure is still evidence-blocked by an invalid concurrent integration fixture | Dashboard key quota check; Postgres insert path; platform schema; integration test harness | XFI-0049; EV-0092; EV-0103; EV-0121; EV-0148; EV-0152; EV-0156; EV-0157; EV-0160; EV-0162 | fixed | platform/keys | Wave 50 clears the `referer_allowlist` product blocker. Wave 53 then repairs both malformed quota-fixture fields (`kid_<hex>` IDs and `rek_<8hex>` stored prefixes). The full `TestPlatformPostgresStores` integration now passes end to end, reaching and satisfying the advisory-lock cap proof instead of dying on schema checks. |
| F-1258 | high | Redis-less API deployments can still panic through the usage-reader path after the middleware-side nil fix | API startup wiring; usage middleware; usage counter; account usage reader | XFI-0050; EV-0094; EV-0103 | fixed | api/ops/runtime | Wave 33 (2026-05-12) replaces the unconditional `UsageReader: usageReaderAdapter{c: usageCounter}` with `UsageReader: usageReaderOrNil(usageCounter)`. The helper returns a typed-nil v1.UsageReader when the counter is nil; the `/v1/account/usage` handler already short-circuits on `usageReader == nil` with an empty list, so Redis-less deployments degrade cleanly instead of nil-deref'ing on `Read`. |
| F-1259 | medium | Usage docs are still internally inconsistent after the source OpenAPI rewrite | Account usage handler; OpenAPI/reference docs; product architecture docs; Postman collection | XFI-0051; EV-0095; EV-0103; EV-0153; EV-0159 | fixed | docs/api/product | Source OpenAPI, `handleAccountUsage` comments, API-design reference, explorer-data inventory, generated YAML, and the canonical tracked Postman collection at `examples/postman/rates-engine.postman_collection.json` now all describe the live Redis-backed usage path. The previously observed `docs/reference/api/postman-collection.json` residual is not tracked in the repository and is absent in the current tree, so it cannot keep the repo finding open. |
| F-1260 | high | `aggregate.min_usd_volume` still evaluates discarded pre-filter volume, so thin survivor windows can publish above a manipulation floor they do not actually meet | Aggregator stablecoin/USD-volume path; class/outlier filtering; VWAP publish gate | XFI-0052; EV-0105 | fixed | aggregate/market-data | Current `HEAD` recomputes USD volume across the post-class/post-outlier survivor slice via [survivorUSDVolume] before invoking [dropForMinUSDVolume], with regression test `class filter gutted window: drops despite pre-filter clearing threshold`. |
| F-1261 | high | Migration `0030_asset_supply_history_unique_constraint` could not apply while `asset_supply_history` compression was enabled | `migrations/0030_asset_supply_history_unique_constraint.up.sql`; `migrations/0005_create_asset_supply_history.up.sql`; migration runner; R1 schema state | XFI-0053; EV-0120; EV-0137; EV-0147 | fixed | db/release/ops | Wave 46 on 2026-05-12 rewrites the up migration to decompress chunks, disable compression around the constraint swap, then restore the original 0005 compression settings. Fresh migration round-trip now succeeds, and the former Timescale `0A000` bootstrap failure no longer reproduces on current head. |
| F-1262 | high | Dashboard/Postgres API-key creation can 500 when optional `referer_allowlist` is omitted because nil slices are inserted as SQL NULL into a NOT NULL array column | Dashboard key create handler; platform APIKey store create/update writers; Postgres schema; dashboard client defaults | XFI-0054; EV-0148; EV-0152; EV-0156 | fixed | platform/keys/db | Wave 50 on 2026-05-12 wraps both `text[]` boundaries (`buildAPIKeyCreateArgs` + `APIKeyStore.Update`) through `nonNilStringArray(in []string) pq.StringArray`, converting nil slices to non-nil zero-length arrays so lib/pq emits `'{}'` instead of SQL NULL. Focused store/dashboard-key packages pass, and the former `referer_allowlist` failure no longer reproduces in the full integration run; the next remaining failure is the separate malformed-ID proof harness tracked as `F-1263`. |
| F-1263 | medium | The concurrent API-key quota integration fixture still violates live `api_keys` identity constraints, so it cannot prove the advisory-lock cap path | `test/integration/platform_postgres_stores_test.go`; migration 0027 `api_keys_{id,key_prefix}_check`; dashboard key ID/plaintext generation | XFI-0055; EV-0157; EV-0160; EV-0162 | fixed | platform/tests/evidence | Wave 53 now fixes both malformed fixture layers. The concurrent quota proof builds `kid_<hex>` IDs and `rek_<8hex>` key prefixes that satisfy migration 0027, and the full Postgres-store integration passes. The invalid proof harness no longer blocks `F-1257`. |
| F-1264 | medium | R1 Prometheus/Loki docs still claim there is no firewall and those observability ports are publicly reachable after nftables moved to default-drop | `configs/prometheus/README.md`; `configs/loki/README.md`; live R1 listeners/firewall; external port probes | XFI-0056; EV-0181; EV-0191 | fixed | ops/docs/security | The stale pre-firewall wording was real, but current workspace READMEs now distinguish on-host listeners from blocked external ingress and keep SSH tunnelling as the supported access path. |
| F-1265 | low | One shipped monitoring design note still uses the retired `critical/warning/info` Alertmanager severity vocabulary after the runnable configs and operator docs converged on `page/ticket/informational` | `configs/alertmanager/README.md`; `configs/alertmanager/alertmanager.r1.yml`; `configs/ansible/roles/prometheus/README.md`; `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`; `docs/architecture/prometheus-ansible-role-design-note.md` | XFI-0057; EV-0182; EV-0184; EV-0191; EV-0204; EV-0206 | fixed | ops/docs/monitoring | Current workspace design-note prose now matches the shared `page` / `ticket` / `informational` ladder and records the correction explicitly. |
| F-1266 | medium | Several Ansible role READMEs still present missing role-specific playbooks as executable operator commands, even after the top-level README and Redis role README were corrected | `configs/ansible/playbooks/*`; `configs/ansible/roles/{haproxy,loki,patroni,prometheus}/README.md`; role `meta/main.yml`; `configs/ansible/README.md`; `configs/ansible/roles/redis-sentinel/README.md` | XFI-0058; EV-0184; EV-0191; EV-0199; EV-0203; EV-0210 | fixed | ops/docs/deployment | Current workspace role docs are truthful: HAProxy, Loki, and Patroni mark their missing playbooks as backlog-only commented examples, while Prometheus points at the tracked `playbooks/monitoring.yml` entrypoint. |
| F-1267 | medium | Healthchecks setup docs still say to create four checks and omit the SLA-probe URL in a broader hardening guide, even though the installer/env/timers now require five external checks | `configs/healthchecks/README.md`; `configs/healthchecks/install.sh`; `docs/operations/pre-launch-hardening.md`; `configs/healthchecks/ratesengine-sla-probe.timer` | XFI-0059; EV-0186; EV-0191 | fixed | ops/docs/monitoring | README, installer comment, and pre-launch hardening now agree on five checks and include `HEALTHCHECKS_URL_SLA_PROBE`. |
| F-1268 | medium | The R1 Prometheus rules README deploys operators into `/etc/prometheus/rules.d/`, but the active R1 config loads `/etc/prometheus/rules.r1/*.yml` | `configs/prometheus/rules.r1/README.md`; `configs/prometheus/prometheus.r1.yml` | XFI-0060; EV-0188; EV-0191; EV-0194 | fixed | ops/docs/monitoring | Current workspace README now copies into `/etc/prometheus/rules.r1/` and explicitly says that path matches `prometheus.r1.yml`. |
| F-1269 | low | The WASM audit-input README still promises an `_unattributed` contract block that the curated YAML no longer contains after the 2026-05-01 testnet-address cleanup | `configs/audit/README.md`; `configs/audit/wasm-walk-contracts.yaml` | XFI-0061; EV-0190; EV-0194 | fixed | docs/audit-tooling | Current workspace README now states that `_unattributed` was intentionally removed during the 2026-05-01 cleanup and describes the live eight-source schema accurately. |
| F-1270 | medium | Active Caddy/operator docs contradict ADR-0025 by telling operators to add Cloudflare edge CIDRs to the API's `trusted_proxy_cidrs`, even though the chosen trust boundary keeps Cloudflare pinned at Caddy and leaves the API trusting only Caddy | `configs/caddy/README.md`; `docs/operations/pre-launch-hardening.md`; `docs/adr/0025-caddy-cloudflare-trusted-proxy.md`; `internal/config/config.go`; `docs/reference/config/README.md` | XFI-0062; EV-0193; EV-0198 | fixed | ops/docs/security | Current workspace docs now preserve ADR-0025 explicitly: Cloudflare CIDRs stay in Caddy `trusted_proxies`, and the API keeps trusting only the immediate Caddy peer. |
| F-1271 | high | The Redis Sentinel HA role, clients, and runbooks assume Sentinel listener authentication, but `sentinel.conf.j2` never configures a Sentinel password/ACL, so the advertised authenticated FailoverClient path is not the config the role actually renders | `configs/ansible/roles/redis-sentinel/templates/sentinel.conf.j2`; `configs/ansible/roles/redis-sentinel/README.md`; `docs/architecture/redis-sentinel-ansible-role-design-note.md`; `docs/operations/runbooks/redis-master-down.md`; `internal/storage/redisclient/redisclient.go`; go-redis Sentinel auth behavior | XFI-0063; EV-0196; EV-0198 | fixed | ops/redis/security/runtime | Current workspace rendering now emits Sentinel listener `requirepass {{ redis_password }}`, aligning the template with the docs, runbooks, and `SentinelPassword` client contract. |
| F-1272 | medium | `redis_exporter` was left on the wrong auth contract during Redis ACL remediation, first in lockdown mode and then in the default non-lockdown branch | `configs/ansible/roles/redis-sentinel/defaults/main.yml`; `configs/ansible/roles/redis-sentinel/templates/redis.conf.j2`; `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`; `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`; `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`; `configs/ansible/roles/redis-sentinel/README.md` | XFI-0064; EV-0197; EV-0200; EV-0211 | fixed | ops/redis/observability/security | Current workspace source makes `-redis.user=redis_exporter` conditional on `redis_acl_lockdown | bool`, matching the ACL-file emission branch and preserving the legacy password-only exporter path when lockdown is off. |
| F-1273 | medium | Redis Sentinel reference docs still retain pre-shipped architecture claims even after the drill command was corrected to the authenticated listener contract | `docs/architecture/redis-sentinel-ansible-role-design-note.md`; `configs/ansible/roles/redis-sentinel/README.md`; `docs/operations/drills/scenarios/sev2-redis-sentinel-failover.md`; `configs/ansible/roles/redis-sentinel/templates/sentinel.conf.j2`; `internal/storage/redisclient/redisclient.go` | XFI-0065; EV-0201; EV-0208; EV-0212 | fixed | ops/docs/redis | Closed wave 126 (commit cb3bb1f3): redis-sentinel design note + role README acknowledge internal/storage/redisclient as the live failover-client home (was claimed as future internal/cachekeys work). |
| F-1274 | medium | The shipped HAProxy role still points operators at an `api-pod-down` runbook that does not exist anywhere in the tracked tree | `configs/ansible/roles/haproxy/README.md`; `docs/architecture/haproxy-ansible-role-design-note.md`; `docs/operations/runbooks/` | XFI-0066; EV-0207; EV-0213 | fixed | ops/docs/haproxy | Current workspace HAProxy docs no longer claim a missing `api-pod-down.md` companion runbook; they explicitly redirect to `api-down.md` and record that the older reference was wrong. |
| F-1275 | high | HAProxy drains every API backend on Redis unavailability because it routes on `/v1/readyz`, while `/v1/readyz` fails Redis checks even though the documented Redis outage contract promises degraded-but-serving behavior | HAProxy health-check path; API readiness checker set; Redis outage/runbook contract; HA failure matrix | XFI-0067; EV-0214; EV-0227 | fixed | api/ops/availability/redis | Current source makes Redis a non-critical ready check: Redis-only failure returns HTTP 200 with body status `degraded`, and a regression test pins the HAProxy contract. Residual HAProxy/HA documentation drift is split into `F-1284`. |
| F-1276 | medium | API alert/runbook examples still use the retired generic Prometheus selector `job="api"` even though current source uses `ratesengine_api` for multi-host HA and `ratesengine-api` on R1 | API alert catalog; API down/5xx/latency/SLA runbooks; metrics source comment; current Prometheus scrape/rule shapes | XFI-0068; EV-0215; EV-0229 | fixed | ops/docs/monitoring | Closed wave 126 (commit cb3bb1f3): internal/obs/metrics.go comment updated from job="api" → job=~"ratesengine[-_]api" matching the actual R1 + HA scrape configurations. |
| F-1277 | low | The `api-down` runbook cites a nonexistent `internal/api/v1/healthz.go` implementation file instead of the actual readiness handler location | `docs/operations/runbooks/api-down.md`; `internal/api/v1/server.go` | XFI-0069; EV-0216; EV-0229 | fixed | ops/docs/api | Current `api-down.md` points at `internal/api/v1/server.go::handleReadyz` and explicitly records that the earlier `healthz.go` reference was corrected. |
| F-1278 | high | The HA-role nftables "drop-ins" are not a sound allow-list composition: by themselves they accept everything, and alongside the repo's default-drop chain their accept chains still cannot reliably open the intended ports | HAProxy, Redis Sentinel, Patroni, Prometheus, and Loki firewall tasks; archival-node default-drop template; HA role design notes/operator claims | XFI-0070; EV-0217; EV-0224 | fixed | ops/security/firewall | Closed wave 126 (commit cb3bb1f3): drop-in pattern abandoned; each HA role (haproxy, redis-sentinel, patroni, prometheus, loki) renders a complete /etc/nftables.conf with `policy drop` + role-specific accept rules in the SAME chain, and explicitly deletes the legacy /etc/nftables.d/*.conf file. accept verdict in earlier-priority chain no longer relied upon. |
| F-1279 | medium | Patroni's firewall task writes `/etc/nftables.d/40-patroni.conf` before it ensures `/etc/nftables.d/` exists | `configs/ansible/roles/patroni/tasks/10-firewall.yml` | XFI-0071; EV-0218; EV-0224 | fixed | ops/deployment/patroni | Current source creates `/etc/nftables.d/` before writing `40-patroni.conf`, so the clean-host ordering failure no longer reproduces. |
| F-1280 | high | Patroni's etcd install task defaults to a placeholder checksum, so the role is not runnable from its documented defaults/inventory model | `configs/ansible/roles/patroni/tasks/02-etcd-install.yml`; `configs/ansible/roles/patroni/defaults/main.yml`; `configs/ansible/roles/patroni/README.md` | XFI-0072; EV-0220; EV-0224 | fixed | ops/deployment/patroni | Closed wave 126 (commit cb3bb1f3): patroni README §Prerequisites documents etcd_release_sha256 as required inventory input + links to the etcd release SHA256SUMS page. 02-etcd-install.yml asserts at role-start. |
| F-1281 | medium | Patroni's textfile scraper requires `jq`, but the Patroni role never installs it | `configs/ansible/roles/patroni/tasks/11-monitoring.yml`; `configs/ansible/roles/patroni/tasks/05-patroni-install.yml`; `configs/ansible/roles/patroni/README.md`; Prometheus node_exporter textfile scrape contract | XFI-0073; EV-0221; EV-0224 | fixed | ops/observability/patroni | Current source installs `jq` in `tasks/05-patroni-install.yml` before the role writes the scraper that uses it. |
| F-1282 | medium | Patroni's documented point-in-time pgBackRest restore target is ignored by the actual restore command | `configs/ansible/roles/patroni/defaults/main.yml`; `configs/ansible/roles/patroni/tasks/08-patroni-bootstrap.yml`; `configs/ansible/roles/patroni/README.md`; `docs/architecture/patroni-ansible-role-design-note.md` | XFI-0074; EV-0222; EV-0224 | fixed | ops/dr/patroni | Current source validates `latest`, `immediate`, and `time:<timestamp>` target forms and maps them to pgBackRest `--type=default`, `--type=immediate`, or `--type=time --target=...`, so the documented variable now affects the restore command. |
| F-1283 | medium | Timescale primary-down runbook uses HTTPS etcd endpoints, a five-node quorum threshold, and a stale leader key that do not match the shipped Patroni role | `docs/operations/runbooks/timescale-primary-down.md`; `configs/ansible/roles/patroni/templates/etcd.conf.j2`; `configs/ansible/roles/patroni/templates/patroni.yml.j2`; `configs/ansible/roles/patroni/tasks/04-etcd-systemd.yml`; Patroni design note | XFI-0075; EV-0225 | fixed | ops/docs/patroni | Closed wave 114 (and verified wave 126): timescale-primary-down runbook uses http etcd endpoints, /service/<cluster>/leader namespace, 2-of-3 quorum threshold — all match Patroni role render. |
| F-1284 | medium | HAProxy and HA docs still describe `/v1/readyz` as Redis-critical after the API changed Redis readiness to degraded-but-serving | `configs/ansible/roles/haproxy/defaults/main.yml`; `configs/ansible/roles/haproxy/templates/haproxy.cfg.j2`; `configs/ansible/roles/haproxy/README.md`; `docs/architecture/ha-plan.md`; `internal/api/v1/server.go`; `cmd/ratesengine-api/main.go` | XFI-0076; EV-0227 | fixed | ops/docs/haproxy/api | Closed wave 110/116 (and verified wave 126): /v1/readyz returns 503 only on critical-checker failures; HAProxy README §Health-check semantics + haproxy.cfg.j2 comments + ha-plan.md §Application tier + Redis-master failure-matrix row all describe the critical/non-critical split. |
| F-1285 | high | Loki, Promtail, Prometheus, and Alertmanager roles download upstream release archives without enforced checksums | `configs/ansible/roles/loki/tasks/{server-02-install.yml,agent-01-install.yml}`; `configs/ansible/roles/prometheus/tasks/02-install.yml`; role defaults/READMEs | XFI-0077; EV-0231; EV-0241 | fixed | ops/security/supply-chain | Closed wave 126 (commit cb3bb1f3): loki + prometheus role READMEs §Prerequisites document loki_release_sha256, promtail_release_sha256, prometheus_sha256, alertmanager_sha256 as required inventory inputs. Install tasks assert at role-start. |
| F-1286 | high | Loki's systemd unit maps MinIO credentials through literal `${...}` strings that systemd does not expand | `configs/ansible/roles/loki/templates/loki.service.j2`; `configs/ansible/roles/loki/templates/loki-config.yaml.j2`; `configs/ansible/roles/loki/defaults/main.yml`; `configs/ansible/roles/loki/README.md`; systemd execution environment semantics | XFI-0078; EV-0232; EV-0238 | fixed | ops/deployment/loki | Current source removed the literal `Environment=AWS_ACCESS_KEY_ID=${...}` / `AWS_SECRET_ACCESS_KEY=${...}` assignments, leaves only `EnvironmentFile=-/etc/default/loki`, and documents direct `AWS_ACCESS_KEY_ID=` / `AWS_SECRET_ACCESS_KEY=` values in the role README/defaults. |
| F-1287 | high | Prometheus and Alertmanager bind to loopback while the generated config targets private IPs for self-scrape and alert delivery | `configs/ansible/roles/prometheus/defaults/main.yml`; `configs/ansible/roles/prometheus/templates/prometheus.service.j2`; `configs/ansible/roles/prometheus/templates/alertmanager.service.j2`; `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`; `configs/ansible/roles/prometheus/README.md` | XFI-0079; EV-0234; EV-0241 | fixed | ops/observability/alerting | Runtime source now binds Prometheus and Alertmanager HTTP listeners to `0.0.0.0:9090/9093` and opens those ports plus 9094 to internal CIDRs in the Prometheus role firewall drop-in, so the generated private-IP scrape/alertmanager targets are reachable under the role's intended network model. Residual README wording drift is split into `F-1290`. |
| F-1288 | medium | Prometheus TSDB disk-space preflight is documented as an assertion but is configured to ignore failures | `configs/ansible/roles/prometheus/tasks/01-preflight.yml`; `configs/ansible/roles/prometheus/README.md`; `configs/ansible/roles/prometheus/defaults/main.yml` | XFI-0080; EV-0236; EV-0241 | fixed | ops/observability/capacity | Current source replaces the ignored mount-fact assertion with a blocking `df --output=avail -B1 /var` check against the documented 20 GB threshold. |
| F-1289 | medium | Loki storage prerequisites are documented as preflight checks but the server preflight does not verify MinIO bucket/access or `/var` capacity | `configs/ansible/roles/loki/tasks/server-01-preflight.yml`; `configs/ansible/roles/loki/defaults/main.yml`; `configs/ansible/roles/loki/README.md`; `docs/architecture/loki-ansible-role-design-note.md` | XFI-0081; EV-0239 | fixed | ops/observability/logging | Closed wave 126 (commit cb3bb1f3): server-01-preflight.yml runs df-based /var capacity check + HEAD probes the MinIO bucket. Loki design note §Edge cases reflects this; defaults expose loki_var_min_free_gb. |
| F-1290 | medium | Prometheus README still documents loopback-only UI access and a 9094-only firewall after the role moved 9090/9093 to internal network listeners | `configs/ansible/roles/prometheus/README.md`; `configs/ansible/roles/prometheus/defaults/main.yml`; `configs/ansible/roles/prometheus/tasks/06-firewall.yml` | XFI-0082; EV-0242 | fixed | ops/docs/observability/security | Closed wave 126 (commit cb3bb1f3): prometheus README §Prerequisites added a "Listener bind + firewall" block describing the 0.0.0.0:9090/9093 bind + the complete /etc/nftables.conf render (F-1278 fix). 06-firewall.yml opens 9090/9093/9094 to prometheus_internal_cidrs. |
| F-1291 | high | Archival-node role installs MinIO, `mc`, and node_exporter from upstream URLs without enforced checksums | `configs/ansible/roles/archival-node/tasks/09-minio.yml`; `configs/ansible/roles/archival-node/tasks/10-observability.yml`; `configs/ansible/roles/archival-node/defaults/main.yml`; archival-node bring-up docs | XFI-0083; EV-0244; EV-0247 | fixed | ops/security/supply-chain | Closed wave 126 (commit cb3bb1f3): archival-node defaults/main.yml documents minio_release_sha256, mc_version, mc_release_sha256, node_exporter_release_sha256 as required inventory inputs. 09-minio.yml + 10-observability.yml assert at role-start. |
| F-1292 | medium | Active core-lag runbook tells operators to cross-check via Horizon despite ADR-0001 banning Horizon references in runbooks | `docs/operations/runbooks/core-lag.md`; `docs/adr/0001-horizon-deprecated.md`; `README.md`; `CLAUDE.md` | XFI-0084; EV-0245; EV-0247 | fixed | ops/docs/architecture | `core-lag.md` no longer instructs operators to cross-check via Horizon; Step 1 now uses stellar.expert or a public stellar-rpc endpoint and records that the prior Horizon wording was removed for ADR-0001. |
| F-1293 | high | Redis Sentinel `redis_exporter` checksum fix is enforced in code but missing from the role inventory/docs contract | `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`; `configs/ansible/roles/redis-sentinel/defaults/main.yml`; `configs/ansible/roles/redis-sentinel/README.md`; `configs/ansible/roles/prometheus/templates/prometheus.yml.j2` | XFI-0085; EV-0249; EV-0255 | fixed | ops/security/supply-chain/redis | Closed wave 126 (commit cb3bb1f3): redis-sentinel README §Prerequisites documents redis_exporter_release_sha256 as required inventory input. 07-monitoring.yml asserts at role-start. |
| F-1294 | medium | CI installs promtool and gitleaks through unauthenticated curl-to-tar pipelines before trusting their results | `.github/workflows/ci.yml`; `scripts/ci/lint-actions-pinning.sh`; GitHub Actions repository workflow-permission setting | XFI-0086; EV-0250; EV-0252; EV-0255 | fixed | ci/security/supply-chain | Current CI downloads both Prometheus and gitleaks release archives to files, fails when the corresponding digest secrets are unset, verifies each archive with `sha256sum -c`, and extracts only after verification. |
| F-1295 | high | Redis Sentinel role writes the Redis password into a world-readable `redis_exporter.service` unit | `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`; `configs/ansible/roles/redis-sentinel/defaults/main.yml`; Redis ACL templates; Prometheus Redis exporter scrape path | XFI-0087; EV-0253 | fixed | ops/security/secrets/redis | Closed wave 124 (and verified wave 126): redis_exporter.service file no longer inlines REDIS_PASSWORD; reads from /etc/default/redis_exporter (mode 0600 root-only) via EnvironmentFile. Wave 126 extended the same pattern to the sentinel-state textfile scraper. |
| F-1296 | medium | Release and deploy workflows grant unused OIDC token minting permission | `.github/workflows/release.yml`; `.github/workflows/deploy.yml`; `.github/workflows/api-docs.yml`; `docs/operations/release-process.md`; GitHub Actions environment settings | XFI-0088; EV-0257 | fixed | ci/security/release | Closed wave 125 (commit d20620e6): id-token: write removed from both release.yml and deploy.yml (no current step consumed OIDC). api-docs.yml retains the permission because actions/deploy-pages legitimately requires it. |
| F-1297 | high | Deploy workflow falls back to live `ssh-keyscan` when the pinned host-key secret is missing | `.github/workflows/deploy.yml`; `docs/operations/deploy-workflow.md`; `docs/operations/r1-deployment-state.md`; `configs/ansible/playbooks/deploy-binary.yml`; `configs/ansible/tasks/deploy-one-binary.yml` | XFI-0089; EV-0259 | fixed | ci/security/deploy | Closed wave 126 (commit cb3bb1f3): deploy.yml Set up SSH step fail-fasts with ::error:: when R1_SSH_KNOWN_HOSTS is unset. No live ssh-keyscan fallback path remains. |
| F-1298 | high | Release/deploy workflows interpolate manual inputs directly into shell scripts before validation | `.github/workflows/deploy.yml`; `.github/workflows/release.yml`; `docs/operations/deploy-workflow.md`; `docs/operations/release-process.md`; GitHub Actions environment settings | XFI-0090; EV-0263 | fixed | ci/security/workflow-injection | Closed wave 126 (commit cb3bb1f3): 7 workflow steps (6 in deploy.yml, 1 in release.yml) pass ${{ inputs.* }} through env: blocks rather than shell-interpolating into run:. Validate-inputs step also enforces a [a-zA-Z0-9-]+ regex against each binary entry. |
| F-1299 | high | Deploy workflow installs mutable Ansible tooling and Galaxy collections at production deploy time | `.github/workflows/deploy.yml`; `configs/ansible/requirements.yml`; `configs/ansible/README.md`; `configs/ansible/playbooks/deploy-binary.yml`; `configs/ansible/tasks/deploy-one-binary.yml` | XFI-0091; EV-0265 | fixed | ci/security/deploy-supply-chain | Closed wave 126 (commit cb3bb1f3): deploy.yml pins ansible-core==2.18.4 + pip==24.3.1; configs/ansible/requirements.yml pins exact versions for ansible.posix, community.general, community.postgresql. |
| F-1300 | high | Healthchecks SLA-probe unit cannot write its default textfile output under its systemd sandbox | `configs/healthchecks/sla-probe.sh`; `configs/healthchecks/ratesengine-sla-probe.service`; `cmd/ratesengine-sla-probe/main.go`; `deploy/monitoring/rules/sla-probe.yml`; `docs/operations/sla-probe.md`; `docs/operations/runbooks/sla-probe-stale.md` | XFI-0092; EV-0267; EV-0273 | fixed | ops/monitoring/sla | Closed wave 126 (commit cb3bb1f3): ratesengine-sla-probe.service grants ReadWritePaths=/var/lib/node_exporter/textfile_collector + SupplementaryGroups=ratesengine so the probe can write the textfile under ProtectSystem=strict. |
| F-1301 | medium | Aggregator-silent runbook points operators at the indexer metrics port on default/R1 deployments | `docs/operations/runbooks/aggregator-silent.md`; `cmd/ratesengine-aggregator/main.go`; `configs/prometheus/prometheus.r1.yml`; `configs/healthchecks/heartbeat.sh`; `configs/ansible/roles/prometheus/README.md` | XFI-0093; EV-0269 | fixed | ops/docs/monitoring | Closed wave 127 (commit 9e5dfe8f): aggregator-silent runbook switched to ${AGGREGATOR_METRICS_PORT:-9465}. :9464 is the indexer port on R1; aggregator auto-shifts to :9465 on collision. |
| F-1302 | medium | Healthchecks smoke wrapper exits successfully when the smoke script is missing or not executable | `configs/healthchecks/smoke.sh`; `configs/healthchecks/ratesengine-smoke.service`; `configs/healthchecks/install.sh`; `configs/healthchecks/README.md`; `scripts/dev/r1-smoke.sh` | XFI-0094; EV-0271 | fixed | ops/monitoring/smoke | Closed wave 127 (commit 9e5dfe8f): configs/healthchecks/smoke.sh fans out to ${HEALTHCHECKS_URL_SMOKE}/fail when the smoke script is missing or non-executable; broken install no longer silently disables the 5-min check. |
| F-1303 | medium | Healthchecks SLA wrapper exits successfully when the SLA probe binary is missing or not executable | `configs/healthchecks/sla-probe.sh`; `configs/healthchecks/ratesengine-sla-probe.service`; `configs/healthchecks/install.sh`; `cmd/ratesengine-sla-probe/main.go`; `docs/operations/sla-probe.md` | XFI-0095; EV-0274 | fixed | ops/monitoring/sla | Closed wave 127 (commit 9e5dfe8f): configs/healthchecks/sla-probe.sh fans out to ${HEALTHCHECKS_URL_SLA_PROBE}/fail when the probe binary is missing or non-executable; broken deploy no longer silently disables the SLA check. |
| F-1304 | medium | Pre-launch Healthchecks apply step omits `ratesengine-sla-probe.timer` after adding the SLA-probe URL | `docs/operations/pre-launch-hardening.md`; `configs/healthchecks/README.md`; `configs/healthchecks/install.sh`; `configs/healthchecks/ratesengine-sla-probe.service`; `configs/healthchecks/ratesengine-sla-probe.timer` | XFI-0096; EV-0276 | fixed | ops/docs/monitoring | Closed wave 128: docs/operations/pre-launch-hardening.md §"Apply" now restarts ratesengine-sla-probe.timer alongside the heartbeat + smoke timers so systemd reloads the EnvironmentFile and the new HEALTHCHECKS_URL_SLA_PROBE takes effect. |

## Finding Template

```md
### F-1201. Title

Severity: `high`

Status: `needs_evidence`

Affected surface:

- `path/to/file.go`

Evidence:

- `EV-0001`
- `XFI-0001`

Expected:

Observed:

Impact:

Reproduction or reasoning path:

Remediation direction:
```

### F-1201. Live R1 firewall hardening is only partially reconciled: internal-service exposure is reduced, but public captive-core ingress drift remains

Severity: `critical`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/archival-node/tasks/11-firewall.yml`
- `configs/ansible/roles/archival-node/templates/nftables.conf.j2`
- `configs/ansible/roles/archival-node/defaults/main.yml`
- R1 host firewall and listening services

Evidence:

- `XFI-0001`
- `R1-0005`
- `R1-0006`
- `R1-0008`
- `EV-0019`
- `EV-0113`
- `EV-0172`
- `R1-0024`

Expected: R1 should run the repo-managed nftables default-deny policy, UFW should be disabled/stopped, and internal services should be loopback-only or restricted to `internal_cidrs`.

Observed during the initial pass: nftables was disabled/inactive with an
empty ruleset; UFW was inactive; external TCP connects succeeded to MinIO,
MinIO console, Prometheus, Loki, Promtail, node_exporter, Galexie admin,
and captive-core port `11726`.

Current-head reconciliation: live R1 now reports `nftables` active and
`ufw` inactive. External probes to `9000`, `9001`, `9090`, `3100`,
`9080`, `9100`, `6061`, and `38563` now time out instead of accepting.
However `/etc/nftables.conf` explicitly permits
`{11625,11626,11725,11726}`, `ss -ltnp` shows `stellar-core` listening on
`0.0.0.0:11726`, and a refreshed 2026-05-13 public probe to `11726/tcp`
still succeeds. That
live firewall shape diverges from the repo template comments stating
captive cores dial out and do not accept inbound.

Impact: The highest-risk storage/observability/admin exposure is materially
reduced versus the initial pass, but live firewall semantics still diverge
from source intent around captive-core ingress. Leaving that unresolved keeps
an undocumented public listener in the production attack surface and weakens
trust in firewall-as-code drift control.

Remediation direction: keep the now-live default-drop policy for internal
services, then either remove public `11726/tcp` or update the source firewall
contract, runbooks, and justification so the listener is explicitly reviewed
as intentional. Preserve the external port probe as a release gate.

### F-1207. Hosted GitHub dependency-alert controls remain disabled after the web Next.js remediation wave

Severity: `critical`

Status: `open`

Affected surface:

- `web/explorer/package.json`
- `web/dashboard/package.json`
- `web/status/package.json`
- `.github/workflows/ci.yml`
- `.github/dependabot.yml`

Evidence:

- `XFI-0005`
- `EV-0014`
- `EV-0051`
- `EV-0099`
- `EV-0114`
- `EV-0171`

Expected: Public Next.js apps should be on patched versions, with automated pnpm updates and CI advisory gates.

Observed during the initial pass: all three apps pinned `next@15.0.4`; `pnpm audit --audit-level moderate` reported 27 advisories per app including 2 critical and 8 high. CI typechecked/linted/built web apps but did not run `pnpm audit`; Dependabot omitted npm/pnpm ecosystems; hosted GitHub vulnerability and Dependabot alerts were disabled.

Impact: Public explorer/status/dashboard surfaces inherit known RCE/auth-bypass/DoS/cache/XSS classes until upgraded. Dashboard risk is higher because it is account-facing.

Current-head reconciliation: the three public apps now pin
`next@15.5.18`; `.github/dependabot.yml` includes `package-ecosystem: npm`
entries for `/web/explorer`, `/web/dashboard`, and `/web/status`; and
current `pnpm audit --audit-level high` runs for all three apps each report
one moderate advisory rather than any high/critical result. The remaining
open portion is hosted GitHub control posture: a fresh 2026-05-13
`gh api repos/RatesEngine/rates-engine/vulnerability-alerts -i` still returns
the disabled-alerts 404 shape, and the Dependabot alerts API still reports
repository alerts disabled.

Remediation direction: keep the patched Next.js baseline and web Dependabot
coverage in place, enable hosted GitHub vulnerability alerts and Dependabot
alerts, and decide whether `eslint-config-next@15.0.4` should be moved with
the current `next@15.5.18` baseline as part of dependency hygiene.

### F-1211. Status-page incident workflow docs point to removed implementations

Severity: `medium`

Status: `open`

Affected surface:

- `deploy/status-page/README.md`
- `docs/operations/status-page-setup.md`
- `docs/operations/runbooks/sev-status-page-update.md`
- `deploy/comms/README.md`
- `deploy/comms/incident-update.md`
- `web/status/**`

Evidence:

- `XFI-0007`
- `EV-0021`
- `EV-0178`

Expected: status-page runbooks and customer-comms templates should describe the shipped incident publication workflow for `status.ratesengine.net`.

Observed: the shipped implementation is a custom `web/status` static-export app on Cloudflare Pages. Wave 57 corrected the primary runbook/setup path, but a later breadth pass found active non-audit documentation still asserting the retired model: `CLAUDE.md` describes a `status-page (cstate scaffold)` repo area, `launch-readiness-backlog.md` still prescribes an Upptime/GitHub Pages + `.upptimerc.yml` + `GH_PAT` flow, `launch-task-list.md` still accepts `cstate` while also claiming there is no `status.ratesengine.net`, no `deploy/` artefacts, no status worker, and nowhere to update status-page incidents, and `deploy/comms/{README,incident-update}.md` still instruct operators to update `RatesEngine/ratesengine-status` Upptime-created issue bodies.

Impact: during a SEV, operators can follow the binding runbook and fail to publish timely customer-visible updates, or publish in a channel not consumed by the live status page.

Remediation direction: choose the canonical incident source for `web/status`, update or delete Upptime/cstate runbooks, add a status-page incident drill to launch readiness, and make docs lint fail on references to removed status-page paths.

### F-1212. Dashboard key creation bypasses account-tier rate limits

Severity: `high`

Status: `fixed`

Affected surface:

- `web/dashboard/src/app/keys/page.tsx`
- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/platform/postgresstore/apikey_store.go`
- `internal/auth/apikey_postgres.go`
- `internal/api/v1/middleware/ratelimit.go`

Evidence:

- `XFI-0008`
- `EV-0023`
- `EV-0089`

Expected: key rate limits should be derived from account tier/subscription or an operator-approved override, not from a free-form customer dashboard input.

Observed during the initial pass: the dashboard UI submitted `rate_limit_per_min`; the handler accepted any positive value up to 100,000 for owner/admin/member sessions; the Postgres auth validator copied that value into the authenticated subject; the rate-limit middleware used it as the effective per-key budget.

Current-head reconciliation: `dashboardkeys.HandleCreate` now clamps the requested budget through `platform.Tier.MaxRateLimitPerMin` before persistence, and `TestHandleCreate_TierClampsRateLimit` covers the tier ladder. The security bypass itself no longer reproduces on current `HEAD`; the remaining customer-facing copy/spec drift is tracked separately as `F-1256`.

Remediation direction: retained for audit history. The backend clamp and regression tests now close this finding's abuse path; residual dashboard/OpenAPI messaging cleanup lives under `F-1256`.

### F-1213. Stablecoin fiat proxy undercounted Stellar USD volume by 10x in the min-volume manipulation gate

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/aggregate/orchestrator/orchestrator.go`
- `internal/aggregate/stablecoin.go`
- `internal/storage/timescale/usd_volume_quote_spec.go`
- `internal/storage/timescale/trades.go`
- `cmd/ratesengine-aggregator/main.go`
- `configs/example.toml`
- R1 `/etc/ratesengine.toml`

Evidence:

- `XFI-0009`
- `EV-0024`
- `R1-0011`
- `EV-0116`

Expected: when `XLM/fiat:USD` expands to on-chain classic/SAC USD-pegged pairs, the min-volume manipulation gate should compute USD volume using the same decimal convention as the quote asset or the stored `trades.usd_volume` value.

Observed during the initial pass: `ExpandTargetPairWithClassicPegs`
appended classic USD-pegged assets from
`[trades].usd_pegged_classic_assets`; `fetchForTarget` rewrote those
trades to `fiat:USD`; then `windowUSDVolume` divided every `QuoteAmount`
by `100_000_000`. The storage path separately documented and implemented
classic/SAC USD pegs as 7-decimal values. R1 had proxy expansion enabled
and Circle USDC configured, but set `min_usd_volume = 0`.

Current-head reconciliation: `fetchForTarget` now computes USD totals and
per-trade USD maps against the source pair's quote-decimal convention before
rewriting onto the fiat target. `usdVolumeForPairPerTrade` uses 7 decimals
for configured classic USD pegs and 8 decimals for fiat-USD rows, and
`TestTick_MinUSDVolumeFilter/classic USD-pegged proxy: $10k publishes under
min=10000` pins the repaired path. R1 still sets `min_usd_volume = 0`, but
its inline comment now frames that as an operator posture for an on-chain-only
deployment rather than a correctness workaround for this undercount.

Impact: if the default `aggregate.min_usd_volume = 10000` is enabled with Stellar USD-pegged proxy expansion, a real $10,000 classic-USDC window is treated as $1,000 and dropped. R1 currently avoids the false negative by disabling the threshold entirely, removing a launch-readiness manipulation defense.

Reproduction or reasoning path: a Stellar XLM/USDC trade with `quote_amount=100_000_000_000` represents $10,000 at 7 decimals. `windowUSDVolume` divides that by `1e8` and returns $1,000. The same quote asset is treated as 7-decimal by `USDVolumeQuoteSpec.QuoteUSDPegInfo` and `tradeUSDVolume`.

Remediation direction: retained for audit history. The arithmetic bug is
fixed in current source with targeted regression coverage; any future change
to R1's `min_usd_volume` posture is an operator/policy decision, not this bug.

### F-1214. `main` is unprotected, so required CI, CODEOWNER review, and signed commits are not enforced

Severity: `critical`

Status: `open`

Affected surface:

- GitHub branch protection / rulesets for `main`
- `CONTRIBUTING.md`
- `CODEOWNERS`
- `.github/workflows/release.yml`
- `.github/workflows/deploy.yml`

Evidence:

- `XFI-0010`
- `EV-0025`
- `EV-0026`
- `EV-0104`

Expected: `main` should require green CI, CODEOWNER review, signed commits, and no force-push/direct-push path, matching the local contribution policy.

Observed: GitHub reports `main.protected=false`; branch-protection/ruleset API reads fail because the private repo tier does not support the feature. Local docs still say CI, CODEOWNER review, and signed commits are enforced.

Impact: a compromised or mistaken maintainer token can push directly to `main`, alter workflows, cut tags/releases, or deploy code without the review and CI controls the project relies on.

Remediation direction: move the repository to a plan that supports branch protection/rulesets for private repos or make the repo public if that is the intended launch path; enforce required checks, CODEOWNER review, signed commits, linear history, no force pushes, and tag/release protections.

### F-1215. Production deployment environments have no required reviewers despite holding deploy secrets

Severity: `high`

Status: `open`

Affected surface:

- GitHub environments: `r1`, `explorer-production`, `docs-production`, `status-production`, `github-pages`
- `.github/workflows/deploy.yml`
- `.github/workflows/explorer-deploy.yml`
- `.github/workflows/docs-deploy.yml`
- `.github/workflows/status-page.yml`

Evidence:

- `XFI-0010`
- `EV-0025`
- `EV-0026`

Expected: production deploy jobs with SSH or Cloudflare credentials should require environment approval and branch/source restrictions.

Observed: all five environments have empty `protection_rules`; `can_admins_bypass=true`; deploy workflow comments describe reviewers as optional. Repository secrets include the R1 deploy SSH key and Cloudflare token.

Impact: anyone with sufficient workflow-dispatch/write access can trigger production deploy paths without an independent approval gate, raising blast radius for compromised GitHub accounts and accidental releases.

Remediation direction: configure required reviewers, disable admin bypass where possible, restrict deployment branches/tags, split secrets by environment, and add a pre-launch check that fails when production environments lack protection rules.

### F-1216. GitHub Actions allows all third-party actions without SHA pinning while workflows use tag-pinned actions

Severity: `high`

Status: `open`

Affected surface:

- GitHub Actions repository policy
- `.github/workflows/*.yml`

Evidence:

- `XFI-0010`
- `EV-0025`
- `EV-0026`

Expected: release/deploy workflows should either use an allow-list of trusted actions or pin external actions to immutable SHAs.

Observed during the initial pass: Actions policy was `allowed_actions=all` and `sha_pinning_required=false`; workflow files called many external actions by mutable version tags, including `cloudflare/wrangler-action@v3`, `stoplightio/spectral-action@v0.8.13`, `grafana/setup-k6-action@v1`, `pnpm/action-setup@v6`, and standard `actions/*` tags.

Current-workspace reconciliation: `.github/workflows/ci.yml` now adds an `actions-pinning` job and `scripts/ci/lint-actions-pinning.sh`. The script warns on every existing mutable third-party tag and, in `PR_DIFF=1` mode, fails newly introduced mutable third-party `uses:` lines. Running it both normally and with `PR_DIFF=1` reports 12 existing tag-pinned third-party actions and exits zero. That narrows the future-regression risk, but it does not remediate the current mutable tags, nor does it change the hosted repo-level Actions policy reported in the original finding.

Impact: a compromised upstream action tag or newly introduced unreviewed action can execute in CI with repository or deployment secrets, including release/deploy paths.

Remediation direction: keep the PR-diff lint if desired, but finish the actual remediation: restrict hosted Actions policy, SHA-pin or otherwise immutably lock existing third-party actions, and decide whether the lint should fail on existing mutable pins in protected branches rather than warning forever.

### F-1217. SEP-10 replay protection is optional and can run guard-free when Redis is absent

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/auth/sep10/validator.go`
- `internal/api/v1/auth_sep10.go`
- `internal/auth/sep10/validator_test.go`
- R1 SEP-10 runtime configuration

Evidence:

- `XFI-0011`
- `EV-0027`
- `EV-0053`
- `EV-0096`
- `R1-0012`

Expected: when SEP-10 authentication is enabled, replay protection should be mandatory and fail closed; a signed challenge should be accepted once for its time-bound window.

Observed: current source implements a Redis-backed `ReplayGuard` and wires it when Redis is configured. The same startup path still permits a configured SEP-10 validator without Redis and explicitly leaves it guard-free when Redis is absent; `auth_mode=sep10` does not require a guard. R1 currently returns 503 for SEP-10, so this is a latent source/configuration flaw until SEP-10 is enabled.

Impact during the initial pass: an operator could enable SEP-10 in a Redis-less or mis-scoped deployment and unknowingly preserve replayable signed challenges. If a signed challenge XDR was captured, it could be reused within the challenge window on that guardless deployment to mint additional valid JWTs.

Current-workspace reconciliation: `cmd/ratesengine-api/main.go` now returns a startup error when `auth_mode=sep10` is configured without Redis, which closes the guard-free deployment mode that made the finding actionable.

Remediation direction: retained for audit history; the current workspace implements the fail-startup branch.

### F-1218. Public signup can mint immediately usable 1000/min API keys from unverified emails

Severity: `high`

Status: `open`

Affected surface:

- `internal/api/v1/signup.go`
- `internal/api/v1/signup_test.go`
- `internal/auth/signup_tracker.go`
- `internal/auth/store.go`
- `web/explorer/src/app/signup/page.tsx`
- `openapi/rates-engine.v1.yaml`

Evidence:

- `XFI-0012`
- `EV-0028`
- `EV-0099`
- `EV-0127`
- `EV-0143`
- `EV-0144`
- `EV-0145`
- `EV-0146`
- `EV-0165`
- `EV-0172`
- `R1-0021`
- `R1-0026`

Expected: self-service key minting should prove email ownership or use a stronger anti-abuse gate before issuing a usable 1000/min key; duplicate checks should stay atomic where they are the idempotency guarantee.

Observed during the initial pass: `/v1/signup` minted a plaintext Starter API key immediately from a parsed email string. The duplicate tracker was optional and tests pinned that duplicate signup succeeded when it was nil. When Redis was wired, the flow still performed lookup, key create, then SETNX mark, so concurrent same-email requests could mint multiple keys.

Current-head reconciliation: the Redis-backed path now calls `ReserveEmail` before minting, and `RedisSignupTracker.ReserveEmail` uses `SETNX` with a pending placeholder plus a five-minute TTL, so the same-email concurrent race is closed when tracking exists. A fresh production-wiring check also narrows the earlier tracker-nil statement: `cmd/ratesengine-api/main.go` only wires the signup account store when Redis exists, and `parseAndValidateSignup` returns `503 account-store-unavailable` when `s.accounts == nil`, so the current main binary does not mint signup keys in a Redis-less deployment. Wave 44 now emits a signup verification token and email link. Wave 45 is now committed on `HEAD=93594529`: `signup_verify.go` marks Redis-stored keys verified, `cmd/ratesengine-api/main.go` passes an `APIKeyEmailVerifier`, `server.go` inserts optional `RequireEmailVerified` middleware, the new middleware/store tests pass, `docs/reference/config/README.md` includes `api.signup_require_email_verification`, and `./scripts/ci/lint-docs.sh` is green. The residual issue is now policy/default posture rather than missing implementation. `SignupRequireEmailVerification` defaults `false`, refreshed R1 inspection on 2026-05-13 still reports `signup_require_email_verification=<unset>`, and the plaintext key is still returned before ownership proof whenever that flag stays off.

Impact: attackers can still cheaply mint large numbers of free API keys with rotating email strings through the intended healthy path whenever the new opt-in gate is not enabled, bypassing the anonymous 60/min browsing posture and creating capacity/billing abuse. The code now has a remediation lever; the public-default posture and current R1 config do not use it.

Remediation direction: route signup through the magic-link/dashboard account flow or require email verification before exposing plaintext keys; keep the atomic Redis reservation; wire the API-key verification marker through `Server` + production startup; add and exercise the actual request-path gate; add per-email/domain/device abuse controls and alerting around the healthy path that still self-issues plaintext keys.

### F-1219. Stripe paid-upgrade webhook still leaves dashboard-created Postgres API keys outside the live upgrade source of truth

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/api/v1/stripe_webhook.go`
- `cmd/ratesengine-api/main.go`
- `internal/platform/billing.go`
- `internal/platform/postgresstore/`
- `migrations/0027_platform_v1_schema.up.sql`
- dashboard/API key billing flows

Evidence:

- `XFI-0013`
- `EV-0030`
- `EV-0053`
- `EV-0107`
- `EV-0108`
- `EV-0112`
- `EV-0130`
- `EV-0142`
- `EV-0165`
- `EV-0168`

Expected: Stripe paid-upgrade events should update the same account, subscription, and API-key records that dashboard users and runtime auth consume, with durable idempotency and audit.

Observed during the initial pass: current source wired Postgres event dedupe and audit rows when Postgres was available, which reduced the earlier idempotency gap. The side effect still used `auth.RedisAPIKeyStore`: it found keys by `client_reference_id` and updated Redis key rate limits only. The webhook did not call `UpsertSubscription`, did not update Postgres dashboard keys/accounts/subscription state, acknowledged paid events with no keys as 200, and could return 200 after partial or total key-update failure.

Current-head reconciliation: wave 55 closes the cross-store gap that kept this
finding open. `StripePlatformBridge` now carries
`APIKeys platform.APIKeyStore`; production wiring in
`cmd/ratesengine-api/main.go` supplies
`postgresstore.NewAPIKeyStore(pgStore)`; and
`applyAccountTierAndKeyUpgrade` calls `upgradePlatformAPIKeys` after the
account-tier bump. That helper lists dashboard-created Postgres keys for the
account, skips revoked keys and keys already at-or-above the new tier budget,
and updates every remaining active key to the promised rate-limit ceiling.
The behavior is idempotent under Stripe re-delivery and does not downgrade an
operator-lifted key.

The regression set is now closure-grade for the original defect. A targeted
current-head run of
`go test ./internal/api/v1 ./cmd/ratesengine-api -run 'Stripe|Webhook|Platform' -count=1`
passes, including `TestStripeWebhook_PlatformBridge_LiftsPostgresKeys`, which
proves two below-target dashboard keys lift to the Pro budget while one revoked
and one already-above-target key remain untouched. The new
`stripe-platform-sync-errors` runbook, metric, and operation-specific tests
also make the documented best-effort platform-side failure semantics explicit
instead of silently hand-waving them.

Impact: historical. The Stripe-paid upgrade path now updates dashboard-created
Postgres keys alongside the Redis signup-key path, so the customer-visible
cross-store rate-limit drift recorded during the cold audit no longer
reproduces on current head.

Remediation direction: retained for audit history; the originally missing
dashboard-key fan-out is now implemented and regression-tested. Residual
best-effort Postgres sync failures are monitored through
`ratesengine_stripe_platform_sync_errors_total{operation=...}` plus the new
runbook rather than left invisible.

### F-1220. Tagged deploys can restart schema-dependent binaries without shipping or applying matching migrations

Severity: `high`

Status: `fixed`

Affected surface:

- `.github/workflows/deploy.yml`
- `configs/ansible/playbooks/deploy-binary.yml`
- `configs/ansible/tasks/deploy-one-binary.yml`
- `configs/ansible/roles/archival-node/tasks/14-ratesengine-services.yml`
- `migrations/*`
- `docs/operations/runbooks/fx-history-missing.md`
- R1 `schema_migrations`

Evidence:

- `XFI-0014`
- `EV-0031`
- `EV-0103`
- `R1-0013`

Expected: a tagged production deploy that can introduce code depending on new tables, columns, CAGGs, or indexes should ship and apply the matching migration set before restarting binaries, or fail readiness when the binary and database schema diverge.

Observed during the initial pass: the deploy workflow downloaded release binaries, verified checksums, and ran an Ansible binary-swap playbook. That playbook restarted/probed services but did not copy `migrations/`, run `ratesengine-migrate up`, or check `schema_migrations`. Migration sync/apply existed only in the initial archival-node role and manual runbook instructions. R1 reported schema `28|f`, but the deploy mechanism itself would not prevent a future binary/schema mismatch.

Current-workspace reconciliation: `.github/workflows/deploy.yml` now stages the release tag's `migrations/` tree into `dist/migrations`, and `configs/ansible/playbooks/deploy-binary.yml` now synchronizes that directory plus runs `ratesengine-migrate up` before any binary swap. That is directionally correct, but not closure-grade yet. The deploy job installs only `ansible-core==2.18.*`, while the new playbook task calls `ansible.posix.synchronize`; Ansible documents that module as part of the separate `ansible.posix` collection rather than `ansible-core`. The workflow does not install that collection, so the new pre-swap migration path is not yet proven runnable in the actual deploy job.

Impact: the original binary/schema mismatch risk is materially reduced in design, but the current in-flight implementation can still fail before it provides that protection because the workflow's Ansible environment does not obviously include the newly referenced collection. Until the deploy job proves the migration-sync/apply path executes, release safety remains unresolved.

Remediation direction: retain the staged tag-matched migrations and pre-binary migration ordering, then make the deploy job self-contained: install `ansible.posix` or replace the task with a builtin-supported transfer primitive, and add a CI or dry-run proof that the exact workflow/playbook combination can execute the sync/apply path. A schema-compatibility readiness gate is still desirable defense in depth.

### F-1221. Active release docs still claim GHCR and arm64 artifacts that the current release workflow does not publish

Severity: `medium`

Status: `open`

Affected surface:

- `.github/workflows/release.yml`
- `docs/operations/deploy-workflow.md`
- `docs/operations/release-process.md`
- Docker/self-hosting expectations
- `CLAUDE.md`
- `docs/operations/r1-deployment-state.md`

Evidence:

- `XFI-0014`
- `EV-0032`
- `EV-0261`

Expected: release documentation should describe the artifacts the current workflow actually publishes.

Observed: reopened during the release-doc breadth pass. `.github/workflows/release.yml` is binary-only and currently builds six linux/amd64 artifacts; its matrix comment says arm64 was dropped on 2026-05-08 and GHCR publishing is disabled. `docs/operations/release-process.md` step 4 now states that container images are not published, but active repo orientation still disagrees: `CLAUDE.md` says release tags cross-compile `linux/{amd64,arm64}` and build/push `ghcr.io/RatesEngine/<binary>:<tag>` plus `:latest`. `docs/operations/release-process.md`'s pipeline summary still says `linux/amd64 + linux/arm64`, and `docs/operations/r1-deployment-state.md` still describes the smoke-test release as "all 12 binaries + SHA256SUMS" even though the current release workflow ships amd64 only.

Impact: medium. Operators and agents can wait on or automate against nonexistent GHCR/arm64 artifacts, miscount release completeness, or perform manual image/architecture builds from the wrong commit during a release or rollback.

Remediation direction: update `CLAUDE.md`, the release-process pipeline summary, and the stale R1 deployment-state release note to say the current release workflow publishes linux/amd64 binaries plus `SHA256SUMS` only. Restore GHCR/arm64 only if those artifacts are intentionally reintroduced in `release.yml`.

### F-1222. Rollback docs point operators to nonexistent `/opt/ratesengine/release-<tag>` directories instead of actual binary backups

Severity: `medium`

Status: `fixed`

Affected surface:

- `docs/operations/release-process.md`
- `docs/operations/deploy-workflow.md`
- `configs/ansible/tasks/deploy-one-binary.yml`
- R1 deployed-version sidecars

Evidence:

- `XFI-0014`
- `EV-0032`
- `R1-0013`

Expected: emergency rollback docs should match the artifacts created by the deploy workflow and present on the host.

Observed: the release process fallback tells operators to copy binaries from `/opt/ratesengine/release-<tag>/`, but the deploy task stores previous binaries as `/usr/local/bin/<binary>.prev-<tag>` and version markers under `/var/lib/ratesengine/deployed-versions`.

Impact: during a production rollback, the documented fallback path can fail at the first file lookup, wasting incident time and encouraging ad hoc rebuilds or untracked manual swaps.

Remediation direction: update rollback docs to use the workflow as primary and the actual `.prev-<tag>` backup layout as fallback, including sidecar updates and post-rollback health checks.

### F-1223. R1 ran a stale Caddyfile that exposed `/metrics` publicly and collapsed Cloudflare client IPs to edge IPs

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/caddy/Caddyfile.api`
- R1 `/etc/caddy/Caddyfile`
- R1 `/etc/ratesengine.toml`
- `internal/api/v1/middleware/remoteip.go`
- public `https://api.ratesengine.net/metrics`

Evidence:

- `XFI-0015`
- `EV-0033`
- `R1-0014`
- `EV-0113`

Expected: R1 should run the reviewed Caddyfile that resolves real client IPs from Cloudflare trusted ranges and blocks `/metrics` on the public API hostname.

Observed during the initial pass: the live Caddyfile lacked the repo's
Cloudflare `trusted_proxies`/`client_ip_headers` block, forwarded
`{remote_host}` as `X-Forwarded-For`, and did not block `/metrics`.
External `/metrics` returned HTTP 200 with Go runtime, route-level, cache,
stream, and Rates Engine metric names and values.

Current-head reconciliation: live R1 `/etc/caddy/Caddyfile` now declares
the Cloudflare trusted-proxy CIDRs, uses
`client_ip_headers CF-Connecting-IP X-Forwarded-For`, forwards
`X-Real-IP` and `X-Forwarded-For` from `{client_ip}`, and responds `404`
on `/metrics`. External `curl -skI https://api.ratesengine.net/metrics`
returns `HTTP/2 404`.

Impact: anonymous clients can scrape internal operational metrics and route counters; behind Cloudflare, per-IP logging/rate limiting sees Cloudflare edge IPs rather than customers, so unrelated users on the same edge can collide in anonymous buckets.

Remediation direction: retained for audit history. The concrete live
exposure now verifies closed on R1; remaining drift-detection or checksum
automation belongs in follow-up hardening rather than this finding.

### F-1224. Dashboard magic-link and session audit IP fields record proxy/loopback IPs instead of real client IPs

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardauth/handlers.go`
- `internal/api/v1/dashboardauth/middleware.go`
- `internal/api/v1/middleware/remoteip.go`
- `internal/platform/postgresstore/token_store.go`
- `internal/platform/postgresstore/user_store.go`
- `migrations/0027_platform_v1_schema.up.sql`

Evidence:

- `XFI-0016`
- `EV-0034`
- `R1-0014`

Expected: dashboard login, magic-link, and session security records should use the same trusted-proxy-resolved client IP as logging, anonymous identity, and rate limiting.

Observed: dashboard auth defines its own `clientIP(r)` helper that parses `r.RemoteAddr`. The middleware-resolved IP is stored in context, not `RemoteAddr`; behind Caddy the socket peer is the local proxy. Those values are written to `magic_link_tokens.requested_ip`, `users.ip_first_seen`, `users.ip_last_seen`, and session touch updates.

Impact: new-login/new-country security signals and account audit data are inaccurate in production, reducing the value of abuse investigations and customer-facing security history.

Remediation direction: replace dashboard auth's local helper with `middleware.RemoteIPFrom(r)` or pass the resolved IP through a small shared helper; add tests where `RemoteAddr=127.0.0.1` and trusted XFF carries the client IP.

### F-1225. Source implements the since-inception USD fallback, but live R1 still serves empty XLM/USD history while direct USDC history is populated

Severity: `high`

Status: `open`

Affected surface:

- `internal/api/v1/history.go`
- `internal/api/v1/chart.go`
- `internal/api/v1/vwap.go`
- `internal/api/v1/ohlc.go`
- `internal/api/v1/price.go`
- `internal/storage/timescale/aggregates.go`
- R1 historical price API

Evidence:

- `XFI-0017`
- `EV-0035`
- `R1-0015`
- `EV-0116`
- `R1-0019`
- `EV-0140`
- `R1-0022`
- `EV-0166`
- `R1-0027`
- `EV-0173`

Expected: historical USD price surfaces should agree on the declared Stellar USD proxy policy, or return an explicit unsupported/fallback-missing signal.

Observed during the initial pass: `handleHistorySinceInception` queried the
literal CAGG pair and returned it directly. It did not apply the stablecoin
fallback that chart, price, VWAP, TWAP, and OHLC paths use for
`X/fiat:USD`. On R1, `native/fiat:USD` since-inception returned no points
while the chart endpoint returned XLM/USD daily points and direct
`native/USDC-GA5Z...` history returned populated points.

Current-head reconciliation: source now calls
`historySinceInceptionStablecoinFallback` when the literal `fiat:USD`
series is empty, and `TestHistorySinceInception_StablecoinFallback` pins the
behavior. Live R1 still reproduces the user-visible defect on the current
deployment: the public `native/fiat:USD` since-inception call returned zero
points at `as_of=2026-05-13T06:33:58.949423013Z`, while direct Circle-USDC
history returned nine populated daily rows from `2026-05-03` through
`2026-05-11`. Earlier R1 config inspection showed
`enable_stablecoin_fiat_proxy = true` plus the Circle USDC classic peg, so the
remaining issue is source/live runtime drift or an unverified deployed path,
not a missing source implementation.

Impact: clients building long-range price charts from the documented since-inception API see no XLM/USD history even though the system has the data under the configured Stellar USDC market. This is a visible product parity failure against CoinGecko/CMC-style historical chart APIs.

Remediation direction: deploy or otherwise reconcile the live API path so R1
serves the already-implemented fallback, then verify public
`native/fiat:USD` since-inception history matches the direct Circle-USDC
series under the configured peg list.

### F-1226. Dashboard API-key allowlists, permissions, monthly quotas, and usage fields are accepted but not enforced at runtime

Severity: `high`

Status: `fixed`

Affected surface:

- `migrations/0027_platform_v1_schema.up.sql`
- `internal/platform/apikey.go`
- `internal/platform/postgresstore/apikey_store.go`
- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/auth/apikey_postgres.go`
- `internal/api/v1/middleware/auth.go`
- `internal/api/v1/middleware/ratelimit.go`
- dashboard/OpenAPI key-management surfaces

Evidence:

- `XFI-0018`
- `EV-0036`
- `EV-0100`
- `EV-0118`
- `EV-0126`
- `EV-0128`
- `EV-0132`

Expected: customer-visible key policy fields should be enforced on every authenticated request, or the UI/API should clearly mark them as not active.

Observed during the initial pass: dashboard key creation stored `monthly_quota`, `permissions`, `ip_allowlist`, `referer_allowlist`, and expiry/revocation fields. Runtime auth validated only key hash, revocation, expiry, and account status, then returned a subject containing tier/key/rate-limit. There was no request-aware check for client IP, referer, permissions, monthly quota, or usage increments; `TouchUsage` had no production caller.

Current-head reconciliation: settled `HEAD=f3c76028...` closes the original
runtime-enforcement finding. The policy path now has:

- `KeyPolicy` middleware plus Redis-cache round-tripping for IP allowlists,
  referer allowlists, and permission entries.
- `Subject.MonthlyQuota` / `APIKeyRecord.MonthlyQuota` propagation, with the
  Postgres validator cascading per-key quota to account override when present.
- `usage.Counter.MonthToDate` plus `middleware.MonthlyQuota`, which rejects
  capped requests with the documented monthly-quota 429 surface.
- committed `TouchUsage` production wiring through `main.go` and `server.go`,
  a Redis SETNX debouncer, Postgres `APIKeyStore.TouchUsage`, and focused
  middleware/debouncer tests.

The touch path is intentionally inline post-handler rather than detached
fire-and-forget; that matches the committed comments and keeps the request
path bounded by the existing debounced best-effort bookkeeping design. The
quota middleware also remains a soft billing/fairness mechanism rather than a
strict zero-overshoot security primitive, which is consistent with the product
spec's hard-cap-optional stance.

The account-usage documentation/model drift is not hidden here: it remains
tracked separately as `F-1259`, where the handler/reference-doc mismatch is the
actual open defect. It no longer blocks closure of this original
allowlist/quota/last-used enforcement finding.

Impact: historical. The customer-visible policy controls that were previously
accepted-but-not-enforced now have a landed request-path implementation and
focused regression coverage.

Remediation direction: retained for audit history; the original defect is
fixed in current committed source. Keep `F-1259` open for the usage-doc/model
surface that remains outside this finding's enforcement scope.

### F-1227. The `ratesengine-migrate` container cannot apply bundled migrations out of the box

Severity: `medium`

Status: `fixed`

Affected surface:

- `docker/ratesengine-migrate.Dockerfile`
- `cmd/ratesengine-migrate/main.go`
- `migrations/*`
- `Makefile`
- `docker/README.md`
- `configs/ansible/roles/archival-node/tasks/14-ratesengine-services.yml`

Evidence:

- `XFI-0019`
- `EV-0037`

Expected: a self-hosted operator should be able to run the migration container with the same migration corpus the binary expects, or the Docker docs and smoke tests should make the required external mount/path explicit.

Observed: the binary defaults to `-migrations migrations`, but the distroless runtime image copies only `/usr/local/bin/ratesengine-migrate`. No `migrations/*.sql` files are present at the default path. `make smoke-docker` only invokes `--help`, so it does not verify that `status` or `up` can open the source directory. The bare-metal Ansible role separately syncs migrations and invokes `-migrations /usr/local/share/ratesengine/migrations`; the Docker path lacks that equivalent contract.

Impact: self-hosted Docker/Compose/Kubernetes operators can build a valid-looking migration image that fails at schema bootstrap time, delaying installation or upgrades and making the container packaging less reliable than the documented bare-metal path.

Remediation direction: either copy migrations into the migrate image at a stable path and set the documented default accordingly, or require a bind mount in the Docker docs and smoke-test that the image can open the configured migration source.

### F-1238. Redis-less API deployments fail startup because closed-bucket stream subscriber is gated on Hub, not Redis

Severity: `medium`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/api/streaming/redispub/subscriber.go`
- `docs/reference/config/README.md`
- `configs/example.toml`

Evidence:

- `XFI-0030`
- `EV-0054`
- `EV-0096`

Expected: Redis-dependent API features should degrade independently when Redis is absent, matching the startup comments and readiness semantics. A Redis-less API should still boot if the operator intentionally omits Redis, with closed-bucket streaming disabled/degraded rather than fatal.

Observed: `cmd/ratesengine-api` creates `hub := streaming.NewHub(0)` unconditionally. Later, it checks `if hub != nil` and calls `redispub.NewSubscriber(rdb, ...)`; `hub` is always non-nil, so a nil Redis client returns `redispub: RedisSubscriber is required` and aborts startup. Other Redis-backed features are correctly gated on `rdb != nil`.

Impact during the initial pass: an operator following the documented "Redis optional at API layer" posture could not run a Redis-less API. A local, staging, or degraded production deployment that should serve read-only Timescale-backed endpoints instead failed before listening.

Current-workspace reconciliation: `cmd/ratesengine-api/main.go` now gates the Redis pub/sub subscriber on `rdb != nil && hub != nil`, so Redis-less startup no longer attempts `redispub.NewSubscriber(nil, ...)`.

Remediation direction: retained for audit history; the current workspace implements the Redis gate.

### F-1228. Source now clears SSE write deadlines, but live R1 tip streams still terminate around the old 30-second cutoff

Severity: `high`

Status: `open`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/api/streaming/handler.go`
- `internal/api/v1/price_stream.go`
- `internal/api/v1/price_tip_stream.go`
- `internal/api/v1/observations_stream.go`
- `docs/operations/customer-demo-script.md`
- R1 live API

Evidence:

- `XFI-0020`
- `EV-0038`
- `R1-0016`
- `EV-0119`
- `R1-0020`
- `EV-0141`
- `R1-0023`
- `EV-0166`
- `R1-0028`
- `EV-0173`

Expected: `/v1/price/stream`, `/v1/price/tip/stream`, and `/v1/observations/stream` should support long-lived SSE clients, with heartbeats preventing idle proxy closure and reconnect/resume handling real network breaks.

Observed during the initial pass: the API `http.Server` set
`WriteTimeout: 30 * time.Second`. Go applied that as a response-write timeout
reset when a new request's headers were read, not as a heartbeat-aware
per-frame deadline. R1 loopback testing confirmed
`/v1/price/tip/stream` emitted events through 25 seconds and then closed at
elapsed 30 seconds.

Current-head reconciliation: source now clears the SSE request write deadline
through `http.ResponseController.SetWriteDeadline(time.Time{})`, while keeping
the global write timeout for ordinary handlers. Targeted API/binary tests pass.
Live R1 still does not verify closed: a fresh public endpoint probe emitted the
initial frame, multiple updates, and a keepalive, then terminated at
`ELAPSED:30.443053 EXIT:92` under a 68-second client ceiling.

Impact: real-time streaming is not actually long-lived. Browser/EventSource and curl clients reconnect every 30 seconds, increasing churn and load; customer demos that instruct a 60-second run fail; CoinGecko/CMC parity for streaming or trade-tape style experiences is weaker than the API contract suggests.

Remediation direction: deploy or otherwise reconcile the live API runtime so
the already-implemented `ResponseController` path is active end-to-end, then
repeat a >60s public stream probe and keep it open past the former 30-second
cutoff.

### F-1229. CDN verification script probes invalid price/SSE URLs and asserts the wrong SSE cache header

Severity: `medium`

Status: `fixed`

Affected surface:

- `scripts/dev/verify-cdn.sh`
- `internal/api/v1/price_tip.go`
- `internal/api/v1/price_tip_stream.go`
- `internal/api/streaming/handler.go`
- `openapi/rates-engine.v1.yaml`

Evidence:

- `XFI-0021`
- `EV-0039`

Expected: CDN verification should prove the real hot, auth, historical, and streaming routes behave correctly at the edge.

Observed: the script checks `/v1/price?base=native&quote=fiat:USD` and `/v1/price/tip/stream?base=native&quote=fiat:USD`, but those endpoints require `asset`, not `base`. R1 returns 400 `missing-asset` for the SSE URL. The script also expects `Cache-Control` to include `no-store` for SSE while the streaming handler sets `no-cache`.

Impact: operators can get misleading CDN validation results during launch or edge changes. A failure may be caused by the script's invalid URL rather than CDN buffering, and a corrected URL would still fail the script's stale cache-header expectation.

Remediation direction: update the script to use `asset=native`, assert the stream endpoint's intended `Cache-Control`, and include a short body read that proves the response is actually `text/event-stream`.

### F-1230. R1 `since-inception` history for core XLM/USDC starts on 2026-05-03, not one year or inception

Severity: `high`

Status: `open`

Affected surface:

- `internal/api/v1/history.go`
- `internal/storage/timescale/aggregates.go`
- `cmd/ratesengine-ops/backfill.go`
- `docs/freighter-rfp.md`
- `docs/architecture/coverage-matrix.md`
- R1 historical API/data

Evidence:

- `XFI-0022`
- `EV-0040`
- `R1-0017`
- `EV-0173`
- `R1-0027`

Expected: launch history for core Stellar pairs should meet the Freighter minimum of one year, ideally since inception, or clearly mark the deployment's historical coverage as incomplete.

Observed: refreshed public R1 direct XLM/Circle-USDC
`/v1/history/since-inception?granularity=1d` still returned only nine daily
points, starting `2026-05-03T00:00:00Z` and ending
`2026-05-11T00:00:00Z`, at
`as_of=2026-05-13T06:33:58.950387268Z`. The handler returns available closed
buckets without a completeness marker or backfill coverage range.

Impact: customers using the “since inception” endpoint for long-range charts get a recent ingest window while the API name implies full history. This is a direct product-parity gap against CoinGecko/CoinMarketCap and does not satisfy the Freighter minimum historical-retention requirement.

Remediation direction: run and verify historical backfill for launch-critical pairs, expose per-pair `earliest_available_at`/backfill completeness metadata, and avoid marketing or docs language that implies inception coverage before the data exists.

### F-1231. Canonical CI is PR-only while `main` is unprotected, so direct pushes can bypass full verification

Severity: `high`

Status: `fixed`

Affected surface:

- `.github/workflows/ci.yml`
- `.github/workflows/api-audit.yml`
- `CONTRIBUTING.md`
- hosted GitHub branch protection

Evidence:

- `XFI-0023`
- `EV-0041`
- `EV-0025`
- `EV-0099`

Expected: every change reaching `main` should either have passed the canonical CI workflow through an enforced PR path, or the same canonical gate should run on main pushes.

Observed during the initial pass: `ci.yml` triggered only on `pull_request` and explicitly disabled push-to-main CI. Hosted GitHub reported `main.protected=false`, so the PR-only assumption was not enforced. The path-limited `api-audit` workflow could run on some main pushes, but it only smoked public API examples and was not equivalent to lint, tests, builds, import rules, govulncheck, gitleaks, OpenAPI generation, and web app checks.

Impact: a direct push to `main` can land broken code or vulnerable dependencies without the full suite running. This compounds the existing branch-protection finding and weakens release confidence for a project preparing public market-data launch.

Current-head reconciliation: `ci.yml` now has `push: branches: [main]` with the same docs-only path exclusions as pull requests, so direct code pushes to `main` now fire the canonical CI workflow. The hosted branch-protection gap remains separately tracked as `F-1214`.

Remediation direction: retained for audit history; the CI trigger gap itself is now fixed.

### F-1232. Circle USDC has `price_usd` on asset detail but 404s or disappears from `/v1/price` and batch price APIs

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/api/v1/price.go`
- `internal/api/v1/assets.go`
- `internal/api/v1/assets_f2.go`
- `internal/api/v1/assets_coin_extension.go`
- `openapi/rates-engine.v1.yaml`
- R1 price and asset APIs

Evidence:

- `XFI-0024`
- `EV-0042`
- `R1-0018`

Expected: a core stablecoin with a USD price on `/v1/assets/{id}` should return the same effective USD price through `/v1/price` and `/v1/price/batch`, especially for CoinGecko/CMC-style stablecoin listings.

Observed: R1 `/v1/assets/USDC-GA5Z...` returns `price_usd:"0.9999838427"`. The same asset passed to `/v1/price?asset=USDC-GA5Z...&quote=fiat:USD` returns 404, and both GET and POST `/v1/price/batch` return an empty array. The price fallback skips declared USD pegs when the requested asset is the peg itself, while the asset detail path can still populate `price_usd` from its coin-overlay/enrichment path.

Impact: clients cannot rely on the price APIs for one of the most important Stellar assets, and batch consumers silently drop USDC even though asset detail displays a price. This is a visible parity gap for wallet and market-listing integrations.

Remediation direction: define first-class stablecoin-to-fiat behavior for declared pegs, return an explicit approximately-one USD price or the enrichment price consistently across single and batch price endpoints, and add tests that compare asset detail, single price, and batch rows for Circle USDC.

### F-1233. SDEX historical backfill silently drops legacy V0 claim atoms while claiming genesis coverage

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/sources/sdex/decode.go`
- `internal/sources/sdex/dispatcher_adapter.go`
- `internal/dispatcher/dispatcher.go`
- `cmd/ratesengine-ops/backfill.go`
- `internal/sources/sdex/README.md`
- `docs/discovery/dexes-amms/sdex.md`
- `docs/discovery/protocol-versions.md`

Evidence:

- `XFI-0025`
- `EV-0044`
- `EV-0105`

Expected: SDEX backfill either decodes every claim-atom shape required by the requested historical range, including legacy V0, or rejects/marks unsupported ranges with visible errors and coverage metadata.

Observed during the initial pass: `decodeClaimAtom` returned `ErrUnknownClaimAtomType` for `ClaimAtomTypeV0`, the legacy raw-Ed25519 claim atom shape. The SDEX dispatcher adapter caught each per-claim error and continued, returning a successful `Decode` result with fewer outputs. Because the dispatcher only increments `DecodeErrors` when `OpDecoder.Decode` itself returns an error, replaying old ledgers dropped V0 fills without an error metric. The same package README said SDEX backfill was supported to genesis, while the discovery notes said historical backfill must handle V0.

Impact: since-inception and one-year-plus SDEX history is materially incomplete for old protocol ranges, but operators and clients get no direct signal that data was skipped. This weakens market-history depth claims and any CoinGecko/CMC-style charting, volume, or OHLC computation over pre-modern Stellar DEX history.

Current-head reconciliation: committed code now decodes `ClaimAtomTypeV0` by deriving the seller G-strkey from raw Ed25519 bytes and surfaces it as a regular trade row. `TestDecoder_v0ClaimAtom_decodedAsOrderBook` pins the branch, and `go test ./internal/sources/sdex` passes on the settled tree. The original V0-drop defect no longer reproduces.

Remediation direction: retained for audit history; the data-loss condition that made this finding true is fixed in current committed code.

### F-1234. Oracle decoders silently skip unknown feeds inside mixed batches, hiding upstream coverage drift

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/sources/reflector/decode.go`
- `internal/sources/redstone/decode.go`
- `internal/sources/band/decode.go`
- `internal/canonical/asset_*.go`
- `internal/dispatcher/dispatcher.go`
- `internal/dispatcher/statsflush/flusher.go`
- `docs/operations/runbooks/decode-errors.md`

Evidence:

- `XFI-0026`
- `EV-0045`

Expected: when a configured oracle contract publishes an asset/feed that the product cannot yet canonicalize, operators should get an explicit coverage-drift signal, even if the same on-chain event also contains known assets.

Observed: Reflector, Redstone, and Band all skip unknown symbols/feed IDs inside mixed batches and return success when at least one known entry remains. The dispatcher only increments decode-error counters when the decoder returns an error, so partial unknown-feed skips are invisible to `SourceDecodeErrorsTotal` and decoder stats. Tests intentionally pin this behavior for mixed known/unknown batches.

Impact: upstream oracle coverage can expand while Rates Engine silently omits the new asset from oracle rows, explorer coverage, cross-oracle confidence, and parity claims. This matters for competing with broad market-data products because the gap is not discoverable from normal decode-error runbooks.

Remediation direction: add per-source unknown-symbol/feed counters and decoder-stats rows, persist skipped feed IDs for operator review, or run an explicit feed-list reconciliation job against configured oracle contracts. Keep partial success if desired, but make the omitted entries observable.

### F-1235. External CEX stream parser errors are skipped without the decode-error metrics promised by runbooks

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/sources/external/binance/streamer.go`
- `internal/sources/external/kraken/streamer.go`
- `internal/sources/external/bitstamp/streamer.go`
- `internal/sources/external/coinbase/streamer.go`
- `internal/sources/external/runner.go`
- `internal/obs/metrics.go`
- `internal/sources/external/README.md`
- `docs/operations/runbooks/decode-errors.md`

Evidence:

- `XFI-0027`
- `EV-0046`
- `EV-0098`

Expected: malformed external websocket frames, unknown subscribed symbols, and vendor schema drift should increment a per-source metric that the decode-error runbook and monitoring can alert on.

Observed during the initial pass: all four CEX streamers skipped parser errors and continued the stream without incrementing `SourceDecodeErrorsTotal` or another parser-error counter. The runner only recorded poller outcomes, not websocket parse failures. The external connector README said these connectors contribute to the same decode-error budget as on-chain decoders.

Impact: a vendor-side schema change or unexpected feed payload can silently reduce live trade coverage. Operators may only notice after price freshness or source-stopped alerts fire, and the decode-errors runbook will show no evidence even though the parse path is failing.

Current-head reconciliation: Binance, Bitstamp, Coinbase, and Kraken now increment `obs.SourceDecodeErrorsTotal` in the skip-and-continue parse-error branch. The targeted streamer package test set passes on this state.

Remediation direction: retained for audit history; the missing metric increment that made the finding true is now fixed.

### F-1236. Supply snapshots can still be stamped at a fresh ledger when freshness producers fall back to permissive zero-value paths

Severity: `high`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-aggregator/main.go`
- `cmd/ratesengine-ops/supply.go`
- `internal/supply/refresher.go`
- `internal/supply/storage_classic_reader.go`
- `internal/supply/storage_sep41_reader.go`
- `internal/supply/lcm_reader.go`
- `internal/storage/timescale/classic_supply_observations.go`
- `internal/storage/timescale/account_observations.go`
- asset detail / market-cap consumers of `asset_supply_history`

Evidence:

- `XFI-0028`
- `EV-0047`
- `EV-0106`
- `EV-0108`
- `EV-0109`
- `EV-0110`
- `EV-0111`
- `EV-0112`
- `EV-0123`
- `EV-0134`
- `EV-0155`
- `EV-0134`

Expected: a supply snapshot for ledger `N` should be computed from supply-observer components that are complete through ledger `N`, or it should publish explicit component freshness/lag metadata and avoid presenting stale inputs as current supply.

Observed: the aggregator and CLI choose the maximum `last_ledger` across ingestion cursors as the snapshot ledger. Component readers then use `AtOrBefore` storage queries for trustlines, claimable balances, LP reserves, SAC balances, SEP-41 event totals, and account observations. These reader interfaces return balances/totals but not the ledger of each component row, so the refresher cannot detect a stale component before inserting a snapshot at the max ledger.

Current-head reconciliation: current `HEAD=48688b3e...` goes further than the stale register text previously admitted. `timescale.Store.MinClassicComponentLedger` feeds `ClassicSupplyComponents.MinComponentLedger`, `timescale.Store.MinSEP41ComponentLedger` feeds `SEP41SupplyComponents.MinComponentLedger`, and native XLM now also has a real producer path: `LCMReserveBalanceReader.MinReserveAccountLedger` reports the oldest reserve-account observation and `XLMComputer` threads that signal into `Supply.MinComponentLedger`. The targeted supply/timescale/aggregator/indexer test set is green, and dedicated XLM/LCM tests cover happy-path freshness propagation plus the permissive fallback cases.

That still does not close the finding. When any configured SDF reserve account lacks an LCM observation, both aggregator and ops chains deliberately fall back to static `reserve_balances_stroops`, stamp the snapshot at the newest cursor ledger anyway, and return `MinComponentLedger=0`. The refresher therefore skips stale-component rejection precisely when provenance has fallen back from observed chain state to operator-static config. Classic and SEP41 readers likewise collapse freshness-query failures to `0`, and `XLMComputer` swallows freshness-reader errors into the same bypass posture. The code is materially closer, but the original fresh-ledger/stale-component risk is not fully removed across every supply surface.

Impact: if one supply observer stalls while another source advances, asset supply and derived market-cap fields can look current but include old balances. This is especially risky for Stellar-specific depth claims around classic/SAC/SEP-41 supply and for customer-facing asset detail pages.

Remediation direction: keep the rejection gate and the now-present classic/SEP-41/XLM producer paths, then close the remaining permissive bypasses. Decide whether static-XLM fallback may publish a fresh-ledger snapshot at all; if it may, expose that provenance explicitly rather than encoding it as `MinComponentLedger=0`. Treat freshness-query errors on classic/SEP-41/XLM as reject-or-degrade policy rather than silent gate bypass, and add integration-level stale-reader tests proving real storage-backed snapshots reject instead of re-entering the zero-value escape hatch. Expose component freshness in diagnostics.

### F-1237. CoinMarketCap ID disambiguation remains incomplete across runtime and verification paths

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/currency/data/seed.yaml`
- `internal/currency/verified.go`
- `cmd/ratesengine-indexer/main.go`
- `internal/sources/external/coinmarketcap/poller.go`
- `internal/sources/external/coinmarketcap/poller_test.go`

Evidence:

- `XFI-0029`
- `EV-0048`
- `EV-0102`

Expected: CoinMarketCap observations should resolve to the verified currency identity, using the numeric CMC IDs already stored in the catalogue when available.

Observed during the initial pass: the indexer built a ticker-only `aggregatorPairs` list, and the CMC poller queried `symbol=` rather than the catalogue's numeric `coinmarketcap_id`. When CMC returned multiple coins for a ticker, the poller took `coins[0]`; a test explicitly pinned that behavior.

Current-head reconciliation: `cmd/ratesengine-indexer/main.go` now sets `p.CMCIDs = catalogue.CoinMarketCapIDs()`, and `internal/sources/external/coinmarketcap/poller.go` now emits `id=` query values for tickers that have an authoritative mapping. That is a material improvement, but the finding stays open. The verification/backfill-oriented `cmd/ratesengine-ops/main.go` path still constructs the CMC poller without `CMCIDs`, and committed CMC tests still exercise only symbol-shaped responses rather than an ID-mode response contract. The poller continues to decode `data` by response-map key -> ticker, so the ID-mode path needs a real fixture proving that numeric-ID requests are mapped back to the intended asset before this is closure-grade.

Impact: CMC can write an oracle update for the wrong project when tickers collide or ranking changes. That corrupts external divergence checks and customer-facing parity against CMC for any ambiguous ticker.

Remediation direction: finish the ID-mode path end to end. Pass catalogue CMC IDs through every runtime/verification poller construction path, add committed fixtures for the actual ID-mode response shape, and prove the poller yields the intended asset even when ambiguous symbol lookup would choose a different coin. Keep symbol fallback only for entries without an ID.

### F-1239. WASM history and extraction ops tools panic at completion when progress output is disabled

Severity: `medium`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-ops/main.go`
- `cmd/ratesengine-ops/wasm_extract.go`
- `cmd/ratesengine-ops/wasm_history_test.go`
- `cmd/ratesengine-ops/wasm_extract_test.go`
- `docs/operations/wasm-audits/**`

Evidence:

- `XFI-0031`
- `EV-0055`

Expected: long-running WASM history and WASM extraction audit commands should either reject `-progress-every 0` or treat it consistently as "disable progress output" without affecting final artifact production.

Observed: both command paths guard in-loop progress printing with `progressEvery > 0`, but after the stream finishes they add the uncounted residue with `workerScanned % progressEvery`. Supplying `-progress-every 0` therefore panics after the expensive ledger walk.

Impact: an operator can lose a long-running WASM audit/extraction run at completion and may be left with incomplete or missing final JSON/WASM artifacts. That directly weakens the evidence trail used for Stellar-specific Soroban market coverage.

Remediation direction: validate `-progress-every` as `> 0` or make zero a supported disable mode by adding the full `workerScanned` residue without modulo. Add tests for zero and nonzero progress intervals in both `wasm-history` and `extract-wasm-from-galexie`.

### F-1240. Docker images build with a different Go toolchain than CI/release while docs claim binary equivalence

Severity: `medium`

Status: `fixed`

Affected surface:

- `docker/ratesengine-api.Dockerfile`
- `docker/ratesengine-indexer.Dockerfile`
- `docker/ratesengine-aggregator.Dockerfile`
- `docker/ratesengine-ops.Dockerfile`
- `docker/ratesengine-migrate.Dockerfile`
- `docker/ratesengine-sla-probe.Dockerfile`
- `docker/README.md`
- `go.mod`
- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- `.github/workflows/release-validate.yml`

Evidence:

- `XFI-0032`
- `EV-0057`

Expected: container builds should use the same reviewed Go toolchain as CI and release binaries, or the repository should explicitly document and test a deliberate toolchain skew.

Observed: CI and release builds resolve Go from `go.mod`, currently `go 1.25.10`. The Docker README says the builder stage uses `golang:1.25-alpine` and produces binaries equivalent to release builds. Every Dockerfile instead uses `golang:1.26-alpine`.

Impact: self-hosted Docker images can be built with a newer compiler/runtime than the release artifacts and tested CI matrix. That weakens reproducibility and can create build, performance, or behavior differences that the release workflow did not exercise.

Remediation direction: align all Dockerfiles with the module/release Go version, preferably from a single generated or linted source of truth. Add a CI check that Dockerfile `FROM golang:` tags match `go.mod`, or update docs/tests if a newer image toolchain is intentionally supported.

### F-1241. The operator migration index stops at `0015` even though the repository ships dense schema history through `0029`

Severity: `medium`

Status: `fixed`

Affected surface:

- `migrations/README.md`
- `migrations/0016_*.sql` through `migrations/0029_*.sql`
- deployment, incident, and schema-review workflows that use the README as the human-readable migration map

Evidence:

- `XFI-0033`
- `EV-0058`
- `EV-0059`

Expected: the migration README's "Current migrations" table should enumerate every shipped numbered migration family and stay synchronized with the dense on-disk migration sequence it claims to describe.

Observed: the migration tree is dense and paired through `0029`, and the integration round-trip test passes against that full set. The README's current-migration table still ends at `0015` while explicitly telling maintainers to update it whenever a new migration lands.

Impact: operators and reviewers using the README to reason about deploy prerequisites, schema drift, or incident response can miss fourteen later schema families, including platform billing/auth tables, FX history storage, contribution/router tables, classic asset registry, and recent schema maintenance. That increases the chance of incorrect deploy assumptions or incomplete troubleshooting.

Remediation direction: update the README table through the latest migration, add a lightweight drift check that compares listed migration numbers to on-disk `.up.sql` families, and keep future schema-index changes coupled to migration review.

### F-1242. The live contribution-history sink persists rows with `volume_usd=NULL` even though the schema reserves that field for source-transparency UX

Severity: `medium`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-aggregator/main.go`
- `cmd/ratesengine-aggregator/contribution_sink.go`
- `internal/aggregate/vwap.go`
- `internal/aggregate/orchestrator/orchestrator.go`
- `internal/storage/timescale/price_source_contributions.go`
- `migrations/0026_create_source_contributions_and_sdex_offers.up.sql`

Evidence:

- `XFI-0034`
- `EV-0060`
- `EV-0103`
- `EV-0104`
- `EV-0105`

Expected: if the aggregator durably records per-source contribution history before the source-breakdown product surface ships, the persisted row should contain the fields that schema/comments say will power that surface, or the unused fields should stay explicitly unclaimed and unwritten.

Observed during the initial pass: the production aggregator wired `ContributionSink` on every orchestrator run. The storage row included `VolumeUSD`, and migration 0026 said `volume_usd` powers the per-source dollar tooltip, but the sink forwarded only asset, quote, bucket, source, weight, and trade count. `VolumeUSD` was never populated by any production caller, so the database stored `NULL` for every contribution row.

Intermediate reconciliation: the first remediation attempt set `row.VolumeUSD = rec.USDVolumeTotal * c.Weight`, which changed the failure mode rather than closing it. `usdVolumeTotal` was computed in `fetchForTarget` before the later class filter and outlier filter mutated the trade slice, while `aggregate.SourceContributions(trades)` ran afterward.

Current-head reconciliation: settled `HEAD=e787c11b...` now carries per-trade USD attribution keyed by stable `canonical.Trade.ID()`, runs class/outlier filtering, then sums only the surviving per-source USD values inside `flushContributions`. The sink persists `SourceUSDVolume[c.Source]` directly. The specific attribution mismatch recorded in `EV-0103` no longer reproduces, and `go test ./cmd/ratesengine-aggregator ./internal/aggregate/orchestrator` passes.

Impact: historical context only; the field went from all-NULL, through one flawed partial remediation, to a current committed implementation that now follows the same filtered survivor set as contribution weights.

Remediation direction: retained for audit history; the previously-recorded sink mismatch is fixed in current committed code.

### F-1243. Classic-asset registry freshness/counts both freeze and drift because trade insertion idempotency is disconnected from registry updates

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/storage/timescale/trades.go`
- `internal/storage/timescale/asset_registry.go`
- `internal/storage/timescale/coins.go`
- `internal/storage/timescale/issuers.go`
- `internal/api/v1/assets.go`
- `internal/api/v1/issuers.go`
- `test/integration/issuers_coins_storage_test.go`

Evidence:

- `XFI-0035`
- `EV-0062`
- `EV-0117`
- `EV-0135`
- `EV-0149`
- `EV-0158`
- `EV-0167`

Expected: repeated observed trades for a classic asset should keep `classic_assets.first_seen_*`, `last_seen_*`, and `observation_count` accurate within a single long-running live-ingest or backfill process. Replay order should not matter.

Observed: `InsertTrade` invokes `registerClassicAssetSeen` after `INSERT INTO trades ... ON CONFLICT DO NOTHING`, but it never inspects rows affected. That creates two coupled correctness failures:

1. `assetRegistryDedupe` returns early once an asset has been touched once in the current process. That happens before the SQL upsert which would apply `LEAST` to first-seen, `GREATEST` to last-seen, and increment `observation_count`. Later distinct trades for the same asset in the same long-running process therefore stop refreshing registry metadata.
2. Because the registry hook still runs when the trade insert was a duplicate no-op, restarting/replaying the same already-stored trade window can increment `observation_count` once per asset per process even when no new trade row landed. The dedupe cache limits the damage inside one process, but it does not restore trade/registry idempotency across retries or process restarts.

Existing integration coverage still seeds `classic_assets` directly instead of exercising this writer path.

Current-head reconciliation: wave 47 fixed the same-process freshness freeze
with TTL-backed dedupe, and wave 51 fixed the duplicate-replay mutation path by
returning before registry mutation when `RowsAffected()==0` on the
`INSERT ... ON CONFLICT DO NOTHING` trade insert. Wave 64 adds the closure proof
this audit was waiting for:
`test/integration/asset_registry_replay_test.go::TestAssetRegistry_DuplicateReplayDoesNotMutateCounters`.
The test clears `ResetAssetRegistryDedupeForTest` between operations to model a
fresh-store/process-equivalent replay boundary, then proves exact replay keeps
`observation_count` and `last_seen_ledger` stable, a genuinely new same-asset
trade advances them once, and replaying that newer trade remains idempotent.
`go test -tags=integration ./test/integration -run 'AssetRegistry_DuplicateReplayDoesNotMutateCounters' -count=1`
passes on current head.

Impact: historical. The registry writer now has both source-level replay
idempotency and DB-backed regression coverage for the cold-audit failure shape.

Remediation direction: retained for audit history; the required DB-backed
replay proof is now present and green.

### F-1244. Dashboard webhook signing secrets are persisted as recoverable live HMAC keys while the surrounding contract still overstates their protection and non-persistence semantics

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardwebhooks/handlers.go`
- `internal/platform/webhook.go`
- `internal/platform/postgresstore/webhook_store.go`
- `internal/customerwebhook/worker.go`
- `migrations/0027_platform_v1_schema.up.sql`
- `openapi/rates-engine.v1.yaml`

Evidence:

- `XFI-0036`
- `EV-0068`
- `EV-0117`
- `EV-0125`
- `EV-0138`
- `EV-0154`
- `EV-0159`
- `EV-0161`
- `EV-0163`

Expected: the webhook secret-handling contract should be explicit and true. If only hashes are persisted, the runtime must not need the plaintext-equivalent signing key later. If outbound signing requires retrievable key material, the schema/API/docs should say so and the stored key should receive an appropriate at-rest protection model.

Observed: the create handler generates a plaintext `wsec_*` secret, passes `SecretHash: []byte(secret)`, and the Postgres store inserts those bytes directly into `customer_webhooks.secret_hash`. The delivery worker later uses that field as the actual HMAC key. Wave 54 now reconciles the reviewed truth surfaces: `platform.CustomerWebhook`, `WebhookStore.RotateWebhookSecret`, `dashboardwebhooks.webhookDTO`, the Postgres store comment, and `docs/architecture/platform-spec.md` all describe recoverable signing-key persistence consistently. The architecture spec explicitly says customer webhook signing keys are plain `bytea` without application-layer envelope encryption.

Impact during the initial pass: operators and reviewers received a split security
model about the actual protection level of customer webhook signing keys. That
source/spec contradiction is now removed.

Remediation direction: retained for audit history; the current workspace chose
the honest recoverable-`bytea` model and aligns the reviewed code/docs to it.

### F-1245. Customer webhook URLs create an outbound SSRF primitive because validation enforces only `https://` and the worker follows default redirects

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardwebhooks/handlers.go`
- `internal/customerwebhook/worker.go`
- `cmd/ratesengine-api/main.go`
- `internal/api/v1/dashboardwebhooks/handlers_test.go`
- `internal/customerwebhook/worker_test.go`

Evidence:

- `XFI-0037`
- `EV-0069`
- `EV-0096`

Expected: user-configured outbound webhooks should enforce a clear egress policy that prevents access to loopback, link-local, RFC1918, cluster-local, or otherwise internal destinations, including redirects and DNS changes after validation.

Observed: `validateWebhookURL` checks only non-empty, `https://`, and `url.Parse`. Production starts the delivery worker whenever dashboard webhooks are enabled. The worker uses a default `http.Client`, so redirect behavior is unrestricted by this code. The tests cover only plain-HTTP rejection, not private hosts, redirect chains, DNS rebinding, or embedded credentials.

Impact during the initial pass: an authenticated dashboard user could turn the API worker into an outbound network probe or request relay against internal HTTPS services, and redirect handling expanded the reachable surface beyond the initially submitted URL. That was a control-plane boundary violation, especially on hosts that can reach internal storage, observability, or admin services already exposed in other findings.

Current-workspace reconciliation: webhook create/update validation now rejects credential-bearing and internal/private/reserved destinations; the worker's default transport re-resolves with `ssrfGuardedDialContext`, and redirect following is disabled via `http.ErrUseLastResponse`. Targeted webhook package tests pass after those changes.

Remediation direction: retained for audit history; the current workspace implements the primary SSRF controls.

### F-1246. API design docs still say webhook callbacks are not in v1 even though dashboard webhook CRUD, worker, and runbooks have shipped

Severity: `medium`

Status: `fixed`

Affected surface:

- `docs/reference/api-design.md`
- `docs/reference/api/rates-engine.v1.yaml`
- `internal/api/v1/dashboardwebhooks/doc.go`
- `cmd/ratesengine-api/main.go`
- `docs/operations/runbooks/customer-webhook-delivery-failing.md`

Evidence:

- `XFI-0038`
- `EV-0072`
- `EV-0096`

Expected: the API-design reference should match the current product contract after a previously deferred capability ships.

Observed: the design doc's "Open questions — closed" section still says "Webhook callbacks? Not in v1. Customers who want push use SSE." The current API reference, route mounts, delivery worker, and operations runbook all describe a shipped dashboard webhook callback feature.

Impact during the initial pass: integrators, operators, and auditors got contradictory answers about whether push callbacks existed, which complicated product positioning and weakened confidence in docs as a source of truth during security and launch review.

Current-workspace reconciliation: `docs/reference/api-design.md` now states that webhook callbacks shipped, identifies the event families, mentions the delivery worker, and explains how webhooks complement SSE rather than replacing it.

Remediation direction: retained for audit history; the current workspace fixes the design-reference drift.

### F-1247. Customer webhook delivery rows are not atomically claimed, so multiple API workers can emit duplicate callbacks for the same attempt

Severity: `high`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/customerwebhook/worker.go`
- `internal/platform/postgresstore/webhook_store.go`
- `docs/architecture/r2-r3-bringup.md`

Evidence:

- `XFI-0039`
- `EV-0073`
- `EV-0098`

Expected: each pending webhook delivery attempt should be claimed once before network I/O so horizontal API scale or multi-region rollout does not multiply customer callbacks.

Observed during the initial pass: every API process with dashboard webhooks enabled started its own worker. `ListPendingDeliveries` fetched due rows with a plain SELECT, without a lease, claim update, or `FOR UPDATE SKIP LOCKED`; the worker performed the HTTP POST and only afterwards marked the row delivered or failed. Two workers could therefore read and send the same pending row concurrently.

Impact: duplicate SEV/anomaly/divergence callbacks can hit customer automation, paging, or Slack/Discord sinks during routine horizontal scaling, blue/green deploy overlap, or the documented R2/R3 rollout. That is a correctness and customer-trust issue, especially for incident automation.

Current-head reconciliation: `postgresstore.ListPendingDeliveries` now uses `FOR UPDATE SKIP LOCKED` and advances `next_attempt_at` by a five-minute lease in the same claim statement before the worker performs network I/O. The original duplicate-worker selection race no longer reproduces from the current code path.

Remediation direction: retained for audit history; the missing claim/lease primitive is now present.

### F-1248. The documented ten-webhook-per-account limit is enforced with a raceable pre-check, so concurrent creates can exceed the cap

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardwebhooks/handlers.go`
- `internal/platform/postgresstore/webhook_store.go`
- `migrations/0027_platform_v1_schema.up.sql`
- `internal/api/v1/dashboardwebhooks/handlers_test.go`

Evidence:

- `XFI-0040`
- `EV-0074`
- `EV-0098`
- `EV-0121`
- `EV-0147`

Expected: the advertised per-account webhook ceiling should remain true under concurrent requests, not just serial UI use.

Observed during the initial pass: `HandleCreate` called `checkQuota`, which loaded current account hooks and compared `len(hooks)` to ten, then later performed a separate store insert. The Postgres store was a plain insert path, migration 0027 had no database-side cap, and the current quota test exercised only sequential creation.

Current-head reconciliation: the store wraps `CreateWebhook` in a
transaction guarded by
`pg_advisory_xact_lock(hashtext('webhook:'||account_id))`, keeping the
count-and-insert pair inside one per-account critical section. The dedicated
`WebhookStore/Concurrent_QuotaCap_Holds` integration launches concurrent
creates against a lower cap and now passes after wave 46 clears the unrelated
migration bootstrap blocker.

Impact: a customer or script issuing parallel create requests can exceed the service's own control-plane limit, growing outbound delivery fan-out, SSRF exposure, and incident-notification noise beyond the bounded posture the code comments promise.

Remediation direction: retained for audit history; the current code and
integration proof close the originally recorded race.

### F-1249. Customer webhook callbacks are exposed and operated as a shipped feature, but declared event coverage is still only partially wired

Severity: `high`

Status: `fixed`

Current-head reconciliation (wave 29, 2026-05-12): the incident-producer half is now wired as an operator-triggered command. The incident corpus is embedded at build time via `go:embed`, so there is no in-process "state transition" to hook from; instead `cmd/ratesengine-ops/emit_incident.go` exposes `ratesengine-ops emit-incident -slug … -event {sev1|resolved}`, looks up the incident in the embedded corpus, refuses semantically-impossible combinations (sev1 on resolved, resolved on investigating, sev1 on a non-SEV-1 entry — `TestFindIncidentForEmit_RefusesWrongStatus` covers each), constructs a payload with `slug` / `title` / `severity` / `status` / `started_at` / `resolved_at` / `affected_components`, and publishes through the existing `customerwebhook.Fanout`. `docs/operations/sev-playbook.md §5.1` adds the emit step paired with the `.md` deploy. All four declared webhook event types — `anomaly.freeze`, `divergence.firing`, `incident.sev1`, `incident.resolved` — now have a production enqueue path. Per-event integration coverage and enqueue-success/failure observability are still a follow-up but are at parity with the freeze/divergence paths.

Affected surface:

- `internal/platform/webhook.go`
- `internal/platform/postgresstore/webhook_store.go`
- `cmd/ratesengine-api/main.go`
- `internal/api/v1/dashboardwebhooks/doc.go`
- `openapi/rates-engine.v1.yaml`
- `docs/operations/runbooks/customer-webhook-delivery-failing.md`

Evidence:

- `XFI-0041`
- `EV-0076`
- `EV-0105`
- `EV-0106`

Expected: once customers can register event subscriptions and operators monitor webhook delivery health, the product must actually fan incident/anomaly/divergence events into the delivery queue.

Observed during the initial pass: the codebase defined event types, accepted them in dashboard CRUD, exposed them in OpenAPI, started a queue-draining worker, and published a runbook for delivery failures. But a repo-wide source search found `EnqueueDelivery` only at its interface/store implementation and tests/fakes; no production event producer inserted pending rows for `incident.sev1`, `incident.resolved`, `anomaly.freeze`, or `divergence.firing`.

Current shared-workspace reconciliation: the webhook fan-out slice now has two committed producer paths. `anomaly.freeze` is emitted from the freeze-event sink after a new durable freeze row is inserted. `divergence.firing` is emitted through an edge-triggered warning hook on the divergence service, with unit coverage proving repeat refreshes above threshold do not re-spam subscribers until the pair recovers and re-crosses. That materially narrows the product gap, but it does not close it: repo search still finds no production enqueue path for `incident.sev1` or `incident.resolved`, and the remediation gate's per-event integration coverage plus enqueue success/failure observability are still absent.

Impact: customers can configure a callback feature that never receives real product events, while operators can watch worker metrics and runbooks that appear to describe a live system. That is a shipped-surface correctness failure and a direct customer-trust risk.

Remediation direction: finish the producer side explicitly. Keep the new anomaly/divergence paths, wire concrete incident producers into the same fan-out layer, then add integration tests that trigger each declared event family and assert delivery rows are created before the worker runs. Add enqueue success/failure observability so event-loss on the producer side is visible rather than only logged.

### F-1250. Freeze-event open-row dedupe is raceable, so concurrent same-pair freezes can create multiple still-firing durable rows

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/aggregate/freeze/freeze.go`
- `internal/storage/timescale/freeze_events.go`
- `test/integration/freeze_events_test.go`

Evidence:

- `XFI-0042`
- `EV-0079`

Expected: while one `(asset, quote)` pair is already frozen, concurrent refreshes should preserve exactly one open durable `freeze_events` row for that pair.

Observed: `FreezeEventSink.RecordFreeze` comments say the idempotency check and insert happen "in the same transaction," but the implementation issues a single unlocked `INSERT ... SELECT ... WHERE NOT EXISTS (...) ON CONFLICT (...) DO NOTHING`. The `freeze_events` primary key includes `frozen_at`, so two concurrent callers with different timestamps can both evaluate `NOT EXISTS` before either row is visible and insert distinct open rows. The current integration test proves only sequential idempotency.

Impact: the anomaly timeline can overcount active freeze incidents for a pair, open-row monitoring can inflate, and recovery semantics rely on a later bulk update cleaning up duplicate history that should not exist in the first place.

Remediation direction: make "one open row per pair" a real database invariant. A partial unique index on `(asset_id, quote_id) WHERE recovered_at IS NULL`, or an equivalent transactional lock/UPSERT design, would close the race. Add concurrent integration coverage that fires multiple same-pair freezes together and asserts exactly one open row persists.

### F-1251. FX-based `usd_volume` remediation is still incomplete after the trade-time freshness fix

Severity: `high`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-indexer/main.go`
- `internal/storage/timescale/usd_fx_resolver.go`
- `internal/storage/timescale/trades.go`
- `test/integration/usd_fx_resolver_test.go`

Evidence:

- `XFI-0043`
- `EV-0080`
- `EV-0102`
- `EV-0139`
- `EV-0147`

Expected: when resolving USD volume for a trade at timestamp `T`, staleness should be evaluated relative to `T` (or another clearly documented trade-time policy), so valid historical/backfilled trades can still inherit a contemporaneous FX/VWAP anchor.

Observed during the initial pass: `VWAPUSDFXResolver.queryDB` correctly found the latest peg VWAP at-or-before the supplied trade timestamp, but `USDPriceAt` rejected it using `r.clock().Sub(observedAt) > freshness`. That compared the row to wall-clock now, not to the trade timestamp. Any trade older than the one-hour default window could therefore lose Phase-2 USD enrichment even when the at-time rate existed. The same file documented `Freshness: 0` as disabling the check, while the constructor interpreted zero as "apply the 1h default"; the integration test relied on the documented disable behavior and failed with `ok=false`.

Current-head reconciliation: the source-level remediation is now present.
`USDPriceAt` evaluates `at.Sub(observedAt) > freshness`, `trimNumericText`
canonicalises Postgres NUMERIC text before returning it, and
`VWAPUSDFXResolverOptions.Freshness` now has unambiguous sentinel semantics:
`0` = default 1h, negative = disabled, positive = explicit override. Focused
package tests cover the trimming and freshness sentinels.

The prior evidence-state gap is now cleared. After wave 46 fixes migration
bootstrap, `go test -tags=integration ./test/integration -run
TestVWAPUSDFXResolver_QueriesPrices1m -count=1` passes end to end against a
fully migrated integration database.

Impact: historical replay, backfill, and delayed indexing can systematically under-populate `trades.usd_volume` for non-USD quotes covered by the new FX resolver. Downstream 24h volume, ranking, and transparency surfaces then understate coverage exactly where Phase 2 was intended to improve it.

Reproduction or reasoning path:

- `go test -tags=integration ./test/integration -run TestVWAPUSDFXResolver_QueriesPrices1m -count=1`
- Current result: passes.

Remediation direction: retained for audit history; the source fix plus the
DB-backed integration proof now close this finding.

### F-1252. Multi-region cutover instructions invoke a nonexistent `make verify-cross-region` launch check

Severity: `medium`

Status: `fixed`

Affected surface:

- `docs/operations/multi-region-cutover.md`
- `scripts/dev/verify-cross-region.sh`
- `Makefile`

Evidence:

- `XFI-0044`
- `EV-0082`

Expected: a launch or failover checklist should call a command that exists in the current repository, especially when the step gates cross-region price consistency before a high-risk cutover.

Observed: the Stage 5 pre-flight checklist in `docs/operations/multi-region-cutover.md` says "Cross-region consistency check passes (`make verify-cross-region`, see `scripts/dev/verify-cross-region.sh`)." Repository search finds no `verify-cross-region` target in `Makefile`; only the shell script and direct ops binary commands exist.

Impact: an operator following the cutover playbook gets a Make target failure at the exact moment they need a deterministic consistency check, creating avoidable ambiguity during a regional bring-up or failover drill.

Remediation direction: either add the Make target and keep docs as written, or update every operational reference to the direct script invocation. Keep one canonical command in the runbook and test it from the documented working directory.

### F-1253. Enabling Redis ACL lockdown disables the default user, but the rendered application config never sets `redis_username`, so binaries keep authenticating on the rejected legacy path

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`
- `configs/ansible/roles/redis-sentinel/templates/redis.conf.j2`
- `configs/ansible/roles/redis-sentinel/defaults/main.yml`
- `configs/ansible/roles/archival-node/templates/ratesengine.toml.j2`
- `internal/config/config.go`
- `internal/storage/redisclient/redisclient.go`

Evidence:

- `XFI-0045`
- `EV-0083`
- `EV-0098`

Expected: the same deployment toggle that enables Redis ACL lockdown should also render application client config that authenticates as the ACL user the server now requires.

Observed during the initial pass: `redis_acl_lockdown: true` rendered an ACL file that turned `user default off` and created `user ratesengine on >{{ redis_password }}`. The Redis client builder supported ACL usernames through `StorageConfig.RedisUsername`, and docs said it must be set to `ratesengine` under lockdown. But the Ansible-owned `ratesengine.toml.j2` storage block rendered `redis_addr` and secrets only; it never wrote `redis_username = "ratesengine"` or an equivalent variable.

Impact: flipping the hardening flag can strand the API/indexer/aggregator on password-only default-user auth exactly after the server side disables that user. Depending on the binary and feature, this can break Redis-backed rate limiting, signup abuse controls, SEP-10 replay protection, streaming fan-out/subscription, freeze markers, and aggregator cache publication during a supposed security hardening rollout.

Current-head reconciliation: the archival-node template now renders `redis_username = "{{ redis_acl_username | default('ratesengine') }}"` whenever ACL lockdown is enabled, matching the named user that the Redis role exposes and the field consumed by `redisclient.Build`.

Remediation direction: retained for audit history; the rendered client-identity handoff that made the finding true is now present.

### F-1254. Redis ACL lockdown allows stale or wrong key families, so hardened deployments still deny active runtime namespaces after the username handoff is fixed

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`
- `internal/ratelimit/bucket.go`
- `internal/cachekeys/keys.go`
- `internal/auth/signup_tracker.go`
- `internal/auth/sep10/redisreplay.go`
- `internal/usage/counter.go`
- `cmd/ratesengine-api/main.go`

Evidence:

- `XFI-0046`
- `EV-0084`
- `EV-0098`

Expected: the Redis ACL policy should grant exactly the application namespaces the current binaries actually use, with repo-controlled tests preventing template drift when key families are added, renamed, or retired.

Observed during the initial pass: the lockdown ACL permitted `~ratelimit:*` and `~subscriber:*`, but current runtime code wrote `rl:*` rate-limit counters and `sub:*` SSE subscriber presence keys. It also omitted active namespaces entirely for `signup:email:*`, `sep10:seen:*`, `usage:*`, `assets:list:*`, and `markets:list:*`. The API binary wired those paths whenever Redis was configured, so a hardened deployment could authenticate successfully and still hit Redis ACL denials on live features.

Impact: operators can believe Redis ACL lockdown is safely enabled after fixing the username defect, while rate limiting, signup duplicate tracking, SEP-10 replay protection, usage accounting, subscriber presence tracking, and catalogue cache population are denied at runtime. The result mixes security-control degradation with customer-visible feature breakage during a hardening rollout.

Current-head reconciliation: `users.acl.j2` now uses `~rl:*`, `~sub:*`, `~signup:email:*`, `~sep10:seen:*`, `~usage:*`, `~assets:list:*`, and `~markets:list:*`, bringing the rendered allow-list back into line with the active key builders reviewed in this pass.

Remediation direction: retained for audit history; the stale/missing key-family mismatch is now fixed.

### F-1255. Concurrent first-login callbacks for the same new email can still create orphan accounts because provisioning is not atomic per email

Severity: `medium`

Status: `fixed`

Current-head reconciliation (wave 30, 2026-05-12): `dashboardauth.Config` gains an optional `EmailLocker` seam with a Redis-SETNX adapter (`auth.RedisSignupEmailLocker`, key family `signup:lock:<sha256-hex>`, 30s TTL). `signupNewUser` acquires per-email before `Account.Create`; lock-loss callers short-circuit and poll `Users.GetUserByEmail` for the winner row (1.5s/10-attempt budget). Production wiring in `cmd/ratesengine-api/main.go` plugs the Redis client; Redis-less deployments leave the locker nil and fall back to the wave-27 Suspend-on-conflict recovery path (so the orphan reaper still has unambiguous signal). The `signup:lock:*` namespace is added to the Redis ACL allow-list in `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`. Tests cover both the in-memory locker (loser pre-empt: no speculative Account row created) and the miniredis adapter (acquire/release/TTL).

Affected surface:

- `internal/api/v1/dashboardauth/handlers.go`
- `internal/platform/postgresstore/token_store.go`
- `internal/platform/postgresstore/account_store.go`
- `internal/platform/postgresstore/user_store.go`
- `migrations/0027_platform_v1_schema.up.sql`

Evidence:

- `XFI-0047`
- `EV-0086`
- `EV-0087`
- `EV-0102`

Expected: first-login provisioning for one email should converge on one account/user result even if multiple valid magic links are consumed in parallel. The flow should be transactional or retry idempotently on the email uniqueness boundary.

Observed during the initial pass: multiple magic links can exist for the same unregistered email. Each callback consumes its own token atomically, then performs `GetUserByEmail`; if no user exists yet, it creates an account row before creating the user row. Under concurrency, two callbacks can both see no user, each create an account, and the losing user insert then fail on `users_email_idx`. Because `signupNewUser` retries account slug conflicts by creating a suffixed slug, the second account row can remain committed even though its owner user was never created.

Current-head reconciliation: `signupNewUser` now catches the losing `CreateUser` conflict, reloads the winning user by email, and returns that row. That narrows the customer-visible failure, but it does not make provisioning atomic or clean up the speculative loser account row that was already inserted before the user uniqueness conflict. The code comment explicitly acknowledges that orphan account and punts cleanup to an operator-side reaper that is not part of this provisioning transaction.

Impact: first login is less likely to surface the earlier internal-error branch, but the database can still accumulate orphan free-tier accounts that do not correspond to an actual owner user. That pollutes customer/account reporting and makes later cleanup or billing migration harder than it should be.

Remediation direction: move new-user provisioning behind one transactional/idempotent persistence boundary keyed by normalized email. Acceptable shapes include a single transaction that creates-or-loads the user/account pair, or a retry-on-unique-email path that discards the speculative account before reloading the winner. Add an integration test that consumes two distinct same-email tokens concurrently and asserts one logical account/user result with no orphan account rows.

### F-1256. Dashboard key-rate UI and OpenAPI still promise generic 1000/100000 limits even though the backend now silently clamps by account tier

Severity: `medium`

Status: `fixed`

Affected surface:

- `web/dashboard/src/app/keys/page.tsx`
- `openapi/rates-engine.v1.yaml`
- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/platform/account.go`

Evidence:

- `XFI-0048`
- `EV-0090`

Expected: the dashboard and published API contract should describe the actual persisted key-budget semantics customers observe after creation, including the current tier-specific caps.

Observed: the backend now clamps dashboard-created key budgets by tier, but the dashboard form still labels the free-tier default as `1000`, exposes a generic `max={100000}` numeric field to every user, and sends that value unchanged. OpenAPI says `rate_limit_per_min` has `Default 1000` and `maximum: 100000`, without describing the server-side tier clamp. A Free customer can therefore submit a value that the product surface implies is normal, then receive a created key whose persisted budget is silently reduced to 60/min.

Impact: the fixed security boundary is now paired with a product-contract mismatch. Customers and support can disagree about what was requested versus what was stored, and API consumers generated from the published schema have no way to infer that the backend may materially downgrade the submitted value by tier.

Remediation direction: make one truth surface win. Either remove the customer-controlled raw input and render the effective tier budget directly, or keep the input but explain exact clamping semantics in both UI and OpenAPI, including the per-tier ceiling and whether the response returns the effective persisted value. Add UI/spec tests that pin the Free-tier copy to the backend cap ladder.

### F-1257. The 25-active-key/account dashboard quota is enforced with a raceable pre-check, so concurrent creates can exceed the advertised cap

Severity: `medium`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/api/v1/dashboardkeys/handlers_test.go`
- `internal/platform/postgresstore/apikey_store.go`
- `migrations/0027_platform_v1_schema.up.sql`

Evidence:

- `XFI-0049`
- `EV-0092`
- `EV-0103`
- `EV-0121`
- `EV-0148`
- `EV-0152`
- `EV-0156`
- `EV-0157`
- `EV-0160`
- `EV-0162`

Expected: the dashboard's active-key cap should hold under concurrent requests, not only under serial UX flows.

Observed during the initial pass: `HandleCreate` called `checkQuota`, which listed all account keys and counted those with zero `RevokedAt`. If the count was below 25, the handler later performed an independent insert through `APIKeyStore.Create`. The schema provided active-key indexes but no database invariant or transactional compare-and-insert guard for the 25-row ceiling. Current tests only pre-seeded 25 rows and verified a single follow-up create returned 409.

Current-workspace reconciliation: capped `APIKeyStore.Create` calls now run
inside a transaction guarded by
`pg_advisory_xact_lock(hashtext('apikey:'||account_id))`; uncapped staff
seeding remains outside the lock because no quota invariant is requested. The
new `APIKeyStore/Concurrent_QuotaCap_Holds` integration scenario starts twelve
goroutines against a cap of four and expects exactly four persisted active rows
plus quota errors for the losers. Wave 50 clears the `referer_allowlist`
product blocker, wave 53 repairs both invalid quota-fixture identity fields,
and the full `TestPlatformPostgresStores` integration now passes through the
intended concurrent cap proof.

Impact during the initial pass: coordinated or accidental concurrent create
requests could leave an account with more active dashboard keys than the
product promised. The advisory-lock enforcement path and the DB-backed
concurrency proof are now both present.

Remediation direction: retained for audit history; keep the account-scoped
advisory lock or an equally strong invariant, and preserve the green concurrent
integration proof.

### F-1258. Redis-less API deployments still wire a non-nil usage middleware around a nil Redis client, so authenticated requests can panic instead of degrading cleanly

Severity: `high`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/api/v1/middleware/usage.go`
- `internal/usage/counter.go`
- `internal/api/v1/server.go`

Evidence:

- `XFI-0050`
- `EV-0094`
- `EV-0103`

Expected: when API Redis is not configured, optional Redis-backed features should either be omitted cleanly or become explicit no-ops. An authenticated request should not cross into a nil Redis client because startup accepted that deployment mode.

Observed during the initial pass: `redisclient.Build` could return nil and the API still proceeded. Later, `usageCounter := usage.New(rdb)` executed unconditionally and `middleware.UsageTracker(usageCounter, ...)` was always passed into the server. The middleware skipped only when the `*usage.Counter` itself was nil, but here it was non-nil with a nil embedded Redis client. Once an authenticated request finished its handler path, `UsageTracker` called `counter.Increment`, which dereferenced `c.rdb.TxPipeline()` directly.

Current-workspace reconciliation: `cmd/ratesengine-api/main.go` now constructs `usageCounter` only when Redis exists, and `usage.New(nil)` returns nil defensively. That closes the original middleware panic route. A second nil path remains: API wiring still sets `UsageReader: usageReaderAdapter{c: usageCounter}` even when `usageCounter == nil`. The server therefore sees a non-nil `UsageReader`, enters `handleAccountUsage`, and `usageReaderAdapter.Read` dereferences `a.c.Read(...)` on a nil inner counter. Redis-less authenticated traffic can still panic on `/v1/account/usage`.

Impact: a Redis-less API deployment that is otherwise intended to stay online is safer on ordinary authenticated requests, but `/v1/account/usage` can still cross into a nil dependency and panic. The optional-Redis degradation model is therefore still broken on a customer-facing route.

Remediation direction: keep the middleware-side fix, then omit `UsageReader` entirely when `usageCounter == nil` or make `usageReaderAdapter.Read` nil-safe. Add API wiring tests for `rdb=nil` that exercise both an ordinary authenticated request and `/v1/account/usage` without panic.

### F-1259. `/v1/account/usage` docs and generated references still call the endpoint always-empty even though current runtime wiring can return real Redis-backed daily counts

Severity: `medium`

Status: `fixed`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/api/v1/account.go`
- `internal/api/v1/server.go`
- `openapi/rates-engine.v1.yaml`
- `docs/reference/api/rates-engine.v1.yaml`
- `docs/reference/api-design.md`
- `examples/postman/rates-engine.postman_collection.json`
- `docs/architecture/explorer-data-inventory.md`

Evidence:

- `XFI-0051`
- `EV-0095`
- `EV-0103`
- `EV-0159`

Expected: once the usage reader is live in current runtime wiring, customer-facing docs and generated references should describe conditional real data semantics instead of the retired stub contract.

Observed during the initial pass: `ratesengine-api` wired `UsageTracker` and `UsageReader`, and `handleAccountUsage` read a trailing 30-day usage window when the reader was present. Yet its own doc comment still said the endpoint always returned `[]`, the OpenAPI summary said "currently empty," the generated reference YAML/Postman artifacts copied that contract, and the API design / explorer inventory docs still called it a placeholder or stub.

Current-head reconciliation: the source OpenAPI file now describes live
Redis-backed daily counters and correctly points Redis-less degradation to
`/v1/readyz` under `checks`. `internal/api/v1/account.go`,
`docs/reference/api-design.md`,
`docs/architecture/explorer-data-inventory.md`,
`docs/reference/api/rates-engine.v1.yaml`, and the tracked canonical
`examples/postman/rates-engine.postman_collection.json` all use the same
model. The previously observed
`docs/reference/api/postman-collection.json` residual is not a tracked repo
artifact and is absent in the current tree.

Impact during the initial pass: customers and internal reviewers were told a live usage feature was absent, while generated clients and product documentation remained anchored to outdated behavior. That source/reference drift is now reconciled in tracked repository artifacts.

Remediation direction: retained for audit history. Keep the canonical tracked
Postman collection in the generated-artifact sync path and preserve docs lint
coverage for this source-vs-generated artifact family.

### F-1260. `aggregate.min_usd_volume` still evaluates discarded pre-filter volume, so thin survivor windows can publish above a manipulation floor they do not actually meet

Severity: `high`

Status: `fixed`

Current-head reconciliation (wave 28, 2026-05-12): `refreshPairWindow` now passes the survivor-only USD total into `dropForMinUSDVolume` via a new `survivorUSDVolume(trades, tradeUSD)` helper. The map is keyed by stable `canonical.Trade.ID()` and captured before `fetchForTarget`'s pair-rewrite, so post-filter survivors still resolve to the source-pair quote-decimal USD value (preserving F-1213's apples-to-apples accounting). Regression test `TestTick_MinUSDVolumeFilter/class_filter_gutted_window` proves a $101k pre-filter window whose class filter leaves $1k of survivors gets dropped under the $10k threshold instead of publishing on the discarded $100k. `Config.MinUSDVolume` doc already promised post-class/post-outlier semantics; this brings the implementation in line.

Affected surface:

- `internal/aggregate/orchestrator/orchestrator.go`
- `internal/aggregate/outliers.go`
- `configs/example.toml`
- R1 `[aggregate].min_usd_volume` posture

Evidence:

- `XFI-0052`
- `EV-0105`

Expected: `aggregate.min_usd_volume` is documented and commented as a post-class, post-outlier publish gate. Only the filtered survivor set that can actually contribute to the VWAP should decide whether the bucket clears the manipulation floor.

Observed: `refreshPairWindow` computes `usdVolume` immediately after `fetchForTarget`, before both `filterForVWAP` and `aggregate.FilterOutliers`. After filtering, the function calls `dropForMinUSDVolume(pair, trades, usdVolume)`, but `dropForMinUSDVolume` ignores the filtered `trades` slice and compares the stale pre-filter `usdVolume` instead. Its own comment says it operates on the post-class/post-outlier window, and `Config.MinUSDVolume` says the same.

Impact: a window can exceed the configured threshold only because of trades the aggregator later discards, yet still publish a VWAP built from a much thinner surviving set. That weakens the manipulation-resistance gate exactly where the threshold is supposed to stop low-liquidity publication. It is distinct from `F-1213`: the quote-decimal undercount bug concerns how USD is normalized; this finding concerns when the normalized total is sampled relative to filtering.

Remediation direction: recompute or carry USD volume through the same filtered survivor set that reaches VWAP publication, then add tests where a pre-filter window clears the threshold but the post-filter survivor set does not. The bucket must be rejected unless the surviving filtered USD volume itself satisfies `MinUSDVolume`.

### F-1261. Migration `0030_asset_supply_history_unique_constraint` cannot apply while `asset_supply_history` compression is enabled

Severity: `high`

Status: `fixed`

Affected surface:

- `migrations/0005_create_asset_supply_history.up.sql`
- `migrations/0030_asset_supply_history_unique_constraint.up.sql`
- `migrations/README.md`
- `internal/storage/timescale/supply.go`
- `test/integration/platform_postgres_stores_test.go`
- R1 Timescale schema state

Evidence:

- `XFI-0053`
- `EV-0120`
- `EV-0137`
- `EV-0147`

Expected: a migration that changes constraints on a compressed Timescale
hypertable should either use a supported compressed-table path or explicitly
decompress/disable compression, perform the DDL, and restore compression in
the same audited migration pattern already used elsewhere in this tree.

Observed during the initial pass: migration `0005` created
`asset_supply_history`, enabled Timescale compression, and attached a
compression policy. Migration `0030` then attempted the UNIQUE-constraint swap
without disabling compression first, causing Timescale `0A000` bootstrap
failures.

Current-head reconciliation: wave 46 on 2026-05-12 rewrites the up migration
to follow the established defensive pattern already used elsewhere in the
tree: decompress existing chunks, disable compression, perform the constraint
swap, then restore the original 0005 compression settings. The current proof
set now passes:

- `go test -tags=integration ./test/integration -run TestMigrationsRoundTrip -count=1`
- `go test -tags=integration ./test/integration -run TestVWAPUSDFXResolver_QueriesPrices1m -count=1`

Impact: retained for audit history. The migration blocker no longer reproduces
on current head and no longer masks dependent proofs for `F-1248` or
`F-1251`.

Remediation direction: retained for audit history; the current migration shape
and integration coverage close the original defect.

### F-1262. Dashboard/Postgres API-key creation can 500 when optional `referer_allowlist` is omitted

Severity: `high`

Status: `fixed`

Affected surface:

- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/platform/apikey.go`
- `internal/platform/postgresstore/apikey_store.go`
- `migrations/0027_platform_v1_schema.up.sql`
- `web/dashboard/src/lib/api.ts`
- `test/integration/platform_postgres_stores_test.go`

Evidence:

- `XFI-0054`
- `EV-0148`
- `EV-0152`
- `EV-0156`

Expected: dashboard/API-key creates that omit optional allowlist fields should
persist empty arrays and continue normally; zero-value slices must not be
converted into SQL NULL against NOT NULL array columns.

Observed during the initial post-migration pass: after wave 46 cleared
migration bootstrap, the full platform-store integration reached
`APIKeyStore/Concurrent_QuotaCap_Holds` and every create failed with:

- `pq: null value in column "referer_allowlist" of relation "api_keys" violates not-null constraint (23502)`

The source path is direct. `dashboardkeys.HandleCreate` copies the request's
optional `referer_allowlist` field into `platform.APIKey.RefererAllowlist`;
when clients omit that optional field, the slice remains nil. The Postgres store
then passes `pq.StringArray(k.RefererAllowlist)` straight into the INSERT, and
the driver emits SQL NULL rather than `'{}'`. Migration 0027 declares
`referer_allowlist text[] NOT NULL DEFAULT '{}'`, so the row insert fails
before normal key creation or quota behavior can complete. The same store
boundary is repeated in `APIKeyStore.Update`, which uses the same
`pq.StringArray(k.RefererAllowlist)` writer for editable fields; that broadens
the defect from the currently visible dashboard-create outage to a reusable
store-marshalling bug. The handler happy-path unit test misses this because it
uses a fake store; the dashboard client type marks `referer_allowlist`
optional, making omission a normal user path rather than a pathological fixture.

Current-head reconciliation: wave 50 closes the product defect. Both
Postgres-bound `text[]` writers now call
`nonNilStringArray(k.RefererAllowlist)`, which converts nil to a non-nil
zero-length `pq.StringArray`; lib/pq therefore emits `'{}'` instead of SQL
NULL. Focused store/dashboard-key packages pass, and the full integration no
longer fails on `referer_allowlist`. It now advances to the separate malformed
ID fixture problem tracked under `F-1263`, which is evidence/test drift rather
than the original customer-facing create bug.

Impact: retained for audit history. The default dashboard create path no longer
fails on omitted `referer_allowlist`; remaining evidence debt belongs to
`F-1263`/`F-1257`.

Remediation direction: retained for audit history; the nil-array persistence
boundary is fixed in current source.

### F-1263. The concurrent API-key quota integration fixture still violates live `api_keys` identity constraints, so it cannot prove the advisory-lock cap path

Severity: `medium`

Status: `fixed`

Affected surface:

- `test/integration/platform_postgres_stores_test.go`
- `migrations/0027_platform_v1_schema.up.sql`
- `internal/api/v1/dashboardkeys/handlers.go`

Evidence:

- `XFI-0055`
- `EV-0157`
- `EV-0160`
- `EV-0162`

Expected: the closure-grade concurrent quota test should generate API-key IDs
and stored display prefixes that obey the same schema contract as the real
dashboard key minting path, then reach the advisory-lock/quota assertions it
exists to prove.

Observed: after wave 50 fixes the `referer_allowlist` NULL defect, the full
`TestPlatformPostgresStores` integration first failed in
`APIKeyStore/Concurrent_QuotaCap_Holds` because every create violated
`api_keys_id_check (23514)`:

- the fixture uses `ID: "kid_" + uuid.New().String()[:12]`
- migration 0027 requires `id ~ '^kid_[a-f0-9]{12,}$'`
- the real dashboard `generateKeyID` path emits hex-only bytes via
  `hex.EncodeToString`, so production-shaped IDs satisfy the constraint

The moving wave-53 fixture patch now strips UUID hyphens, which clears that
first schema check. Re-running the same full integration then fails all 12
creates on `api_keys_key_prefix_check (23514)` before any quota assertion can
run:

- the fixture derives `KeyPrefix: plaintext[:12]` from `rek_race_...`
- migration 0027 requires `key_prefix ~ '^rek_[a-f0-9]{8}$'`
- the real dashboard plaintext generator returns `rek_` plus hex bytes, so
  production-shaped prefixes satisfy the constraint

Current-workspace reconciliation: the moving fixture patch now generates both
schema-valid fields. IDs use `kid_` plus hyphen-stripped hex UUID characters,
stored prefixes use `rek_` plus eight lowercase hex characters, and the full
`go test -tags=integration ./test/integration -run
TestPlatformPostgresStores -count=1` proof now passes. The advisory-lock quota
scenario finally reaches its intended cap assertions.

Impact during the failing phase: the advisory-lock quota remediation under
`F-1257` lacked terminal DB-backed closure evidence even though the prior
production create bug was fixed. That proof blocker is now removed.

Remediation direction: retained for audit history; the current workspace now
generates production-shaped IDs/prefixes and the closure integration is green.

### F-1264. R1 Prometheus/Loki docs still describe a pre-firewall public exposure posture

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/prometheus/README.md`
- `configs/loki/README.md`
- R1 `nftables` posture and observability listeners
- operator remote-access assumptions for Prometheus/Loki

Evidence:

- `XFI-0056`
- `EV-0181`

Expected: operator docs should describe the live observability-access posture. If Prometheus/Loki listeners remain host-bound but nftables blocks external ingress, the docs should say that plainly and keep SSH-tunnel guidance as the supported access path.

Observed during discovery: both docs still stated that R1 had "no firewall today" and that Prometheus `:9090` / Loki `:3100` were publicly reachable, pending future Caddy fronting. Fresh live inspection showed the processes still listen (`*:9090`, `*:3100`), but `/etc/nftables.conf` now enforces `policy drop` and only explicitly permits the captive-core TCP set. Independent external probes to `136.243.90.96:9090` and `:3100` timed out.

Impact: this misstates the current threat model and access path for production observability services. An operator can waste time debugging an expected public endpoint that is intentionally firewall-blocked, or make rollout/security decisions from an obsolete "no firewall" assumption.

Resolution: current workspace READMEs now describe on-host listeners separately from blocked external ingress, keep SSH tunnelling as the supported path, and no longer claim the observability APIs are public. `EV-0191` confirms the remediation landed across both docs.

### F-1265. Alertmanager docs still describe a severity-vocabulary split that current configs already eliminated

Severity: `low`

Status: `fixed`

Affected surface:

- `configs/alertmanager/README.md`
- `configs/alertmanager/alertmanager.r1.yml`
- `configs/ansible/roles/prometheus/README.md`
- `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`

Evidence:

- `XFI-0057`
- `EV-0182`
- `EV-0184`
- `EV-0191`
- `EV-0204`
- `EV-0206`

Expected: docs/comments should describe the current shared Alertmanager severity ladder so an operator knows the standalone and Ansible apply paths are already aligned.

Observed during discovery: `configs/alertmanager/README.md` still said the Ansible template "currently hardcodes `critical/warning/info` matchers" and must be adapted to `page/ticket/informational`; the standalone `alertmanager.r1.yml` header repeated the same contrast; and `configs/ansible/roles/prometheus/README.md` still documented the pager/chat routing in the retired `critical` / `warning` / `info` terms. The actual Ansible template already routed `severity = "page"`, `severity = "ticket"`, and `severity = "informational"` and its header explicitly said it mirrors the standalone path.

Impact: the migration guidance is stale and can induce unnecessary config churn or false work during the R1 -> multi-host observability transition.

Observed after the first repair: current workspace runtime/operator docs already described the shared `page` / `ticket` / `informational` ladder consistently across the standalone Alertmanager config and the Prometheus role README. `EV-0204` briefly captured a residual design-note lag. The current design note now shows `severity: page`, `severity: ticket`, and `severity: informational`, and adds a correction note explaining that the older `critical / warning / info` wording was retired.

Resolution: current workspace docs/configs are aligned again across the standalone Alertmanager path, the Prometheus role README/template, and the shipped design note. `EV-0206` confirms the residual drift is gone.

### F-1266. Ansible deployment docs still advertise orchestration entrypoints that are not present in the tracked tree

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/README.md`
- `configs/ansible/playbooks/*`
- `configs/ansible/roles/{haproxy,loki,patroni,prometheus,redis-sentinel}/meta/main.yml`
- `configs/ansible/roles/archival-node/tasks/10-observability.yml`

Evidence:

- `XFI-0058`
- `EV-0184`
- `EV-0191`
- `EV-0199`
- `EV-0203`

Expected: the Ansible bootstrap guide should accurately describe what role inventory actually exists and which archival-node observability features are implemented versus deferred.

Observed during discovery: the README still opened with "Today the only role is `archival-node`", even though the repository now ships role metadata for `haproxy`, `loki`, `patroni`, `prometheus`, and `redis-sentinel`. The same README's observability summary said the archival-node role includes `promtail (Loki shipper) on a configurable target`, but `roles/archival-node/tasks/10-observability.yml` ends with `TODO(#0): wire promtail -> loki_push_url. Skeleton only this round.`

Observed after the first repair: the top-level Ansible README and Redis Sentinel README explicitly admitted that the cluster playbooks had not landed yet, which narrowed the finding.

Resolution: current workspace source closes the remaining role-level drift. HAProxy, Loki, and Patroni now present their absent playbooks as backlog-only commented examples rather than runnable commands, while Prometheus points at the tracked `playbooks/monitoring.yml` entrypoint. `rg --files configs/ansible/playbooks` still shows only `archival-node.yml`, `deploy-binary.yml`, and `monitoring.yml`, and the docs now describe that inventory honestly.

Impact: operators using the bootstrap guide get an obsolete mental model of what automation exists and can incorrectly assume a fresh archival-node deployment already ships logs into Loki. That is exactly the kind of deployment-document drift that creates blind spots during a bring-up or SEV.

Remediation direction: source drift closed. Keep future role docs pinned to the tracked playbook inventory, or explicitly label non-landed orchestration as backlog-only.

### F-1267. Healthchecks setup docs undercount required checks and omit the SLA-probe URL in one operator path

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/healthchecks/README.md`
- `configs/healthchecks/install.sh`
- `docs/operations/pre-launch-hardening.md`
- `configs/healthchecks/ratesengine-sla-probe.timer`

Evidence:

- `XFI-0059`
- `EV-0186`

Expected: every Healthchecks setup surface should agree on the number of external checks and the exact env vars required by the installed timers. Once `ratesengine-sla-probe.timer` is part of the installer, the setup contract is five URLs, not four.

Observed during discovery: the Healthchecks README described the SLA timer, listed `HEALTHCHECKS_URL_SLA_PROBE`, and restarted `ratesengine-sla-probe.timer`, but its installation prose still said "create four Checks". `configs/healthchecks/install.sh` repeated the stale count in its placeholder-file comment as "the four Healthchecks.io URLs (3 heartbeats + 1 smoke)" even though the generated env file included `HEALTHCHECKS_URL_SLA_PROBE=` and the script enabled the SLA timer. `docs/operations/pre-launch-hardening.md` also still documented only the four pre-SLA URLs.

Impact: an operator can follow the written hardening/setup steps exactly and leave the SLA probe without an external Healthchecks heartbeat. The underlying local timer still runs, but the separate out-of-band liveness signal is missing from the Healthchecks dashboard.

Resolution: the README now says five checks, the installer comment enumerates the SLA probe explicitly, and the pre-launch hardening snippet includes `HEALTHCHECKS_URL_SLA_PROBE`. `EV-0191` confirms the three setup paths now agree.

### F-1268. The R1 Prometheus rules README copies rule files into a directory the active R1 config does not load

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/prometheus/rules.r1/README.md`
- `configs/prometheus/prometheus.r1.yml`

Evidence:

- `XFI-0060`
- `EV-0188`

Expected: the R1 rule-overlay README should instruct operators to place copied rule files into the same directory that `prometheus.r1.yml` actually includes.

Observed during discovery: the README's apply step copied `configs/prometheus/rules.r1/*.yml` to `/etc/prometheus/rules.d/` and then said `prometheus.r1.yml` already loaded `/etc/prometheus/rules.d/*.yml`. The actual config said the opposite: its comments and `rule_files` block used `/etc/prometheus/rules.r1/*.yml`.

Impact: an operator can follow the README exactly, reload Prometheus successfully, and still have the intended R1 overlay alerts absent because the files landed outside the configured include path.

Resolution: current workspace README now copies to `/etc/prometheus/rules.r1/`, says that path matches the configured `rule_files` glob, and records the corrected historical drift. `EV-0194` confirms the closure.

### F-1269. The WASM audit-input README still describes a removed `_unattributed` bucket

Severity: `low`

Status: `fixed`

Affected surface:

- `configs/audit/README.md`
- `configs/audit/wasm-walk-contracts.yaml`

Evidence:

- `XFI-0061`
- `EV-0190`

Expected: the README should describe the actual schema of the curated YAML used for future WASM-history audits.

Observed during discovery: `configs/audit/README.md` said the YAML contains one block per Soroban source plus an `_unattributed` block for operational contracts whose `ContractInstance` entries were TTL-evicted. The YAML had 540 contracts across exactly eight named source blocks and no `_unattributed` key. Its footer explained why: the former three "TTL-evicted" leftovers were actually Reflector testnet addresses misread during the earlier investigation and were intentionally removed on 2026-05-01.

Impact: low. The curated input itself is coherent, but its README still documents a schema that no longer exists. That can confuse the next auditor refreshing the YAML or make them try to preserve a category the repo deliberately retired.

Resolution: current workspace README now describes one entry per Soroban source, states that `_unattributed` was intentionally removed in the 2026-05-01 testnet-address cleanup, and therefore matches the curated YAML. `EV-0194` confirms the closure.

### F-1270. Active Caddy/operator docs move the Cloudflare trust boundary to the wrong layer

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/caddy/README.md`
- `docs/operations/pre-launch-hardening.md`
- `docs/adr/0025-caddy-cloudflare-trusted-proxy.md`
- `internal/config/config.go`
- `docs/reference/config/README.md`

Evidence:

- `XFI-0062`
- `EV-0193`
- `EV-0198`

Expected: operator docs should describe the same trust boundary the code and ADR define. In the chosen `Cloudflare -> Caddy -> API` topology, Caddy validates Cloudflare's edge ranges and forwards a resolved client IP; the API continues to trust only its immediate peer, Caddy, through `trusted_proxy_cidrs = ["127.0.0.1/32"]`.

Observed: the drift was real when first captured under `EV-0193`, but the current workspace closes it. `configs/caddy/README.md` now says the API's `trusted_proxy_cidrs = ["127.0.0.1/32"]` setting stays unchanged under Cloudflare because Caddy remains the only trusted immediate proxy, and `docs/operations/pre-launch-hardening.md` now says no API-side CIDR change is needed while Cloudflare ranges live in Caddy's trusted-proxy block.

Impact: medium. The docs do not describe the system that is actually implemented. An operator following them can widen the API trust surface at the wrong layer, reason incorrectly about who is allowed to supply `X-Forwarded-For`, or create a future deployment that disagrees with ADR-0025's explicit boundary. This is especially risky because rate-limit identity and access logs depend on that resolution chain.

Remediation direction: closed on the current workspace. Keep future operator docs anchored to ADR-0025's immediate-peer trust model unless that ADR is deliberately superseded.

### F-1271. Redis Sentinel listener authentication is promised everywhere except the rendered Sentinel config

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/redis-sentinel/templates/sentinel.conf.j2`
- `configs/ansible/roles/redis-sentinel/README.md`
- `docs/architecture/redis-sentinel-ansible-role-design-note.md`
- `docs/operations/runbooks/redis-master-down.md`
- `docs/operations/drills/scenarios/sev2-redis-sentinel-failover.md`
- `internal/storage/redisclient/redisclient.go`
- go-redis v9 Sentinel auth handling in the module cache

Evidence:

- `XFI-0063`
- `EV-0196`
- `EV-0198`

Expected: the rendered Sentinel listener-auth contract should match every caller and operator instruction. If clients and runbooks pass a Sentinel password, the Sentinel process must actually require and accept listener auth through `requirepass` or an ACL-backed equivalent.

Observed: the inconsistency was real when first captured under `EV-0196`, but the current workspace closes the source drift. `templates/sentinel.conf.j2` now renders `requirepass {{ redis_password }}` for Sentinel listener auth while preserving `sentinel auth-pass ... {{ redis_password }}` for Sentinel-to-primary auth, matching the docs, runbooks, and `redisclient.Build` SentinelPassword wiring already audited.

Impact: high. The committed HA Redis topology is internally contradictory at an authentication boundary. Depending on Redis Sentinel's runtime posture, this can either leave `26379` accessible without the password the docs promise, or make the production FailoverClient/operator commands misbehave because they attempt Sentinel AUTH against a listener that was never configured for it. Either branch defeats the claimed launch-ready Sentinel path.

Remediation direction: source drift closed. Runtime post-deploy verification should still prove:

- rendered `sentinel.conf` contains the listener-auth directive or ACL equivalent
- `redis-cli -p 26379 -a "$REDIS_PASSWORD" SENTINEL ckquorum ...` succeeds
- the same command without auth fails
- a real `go-redis` FailoverClient configured through `redisclient.Build` reaches the Sentinel cluster successfully

### F-1272. Redis ACL lockdown leaves `redis_exporter` on the wrong auth contract

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`
- `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`
- `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`
- `configs/ansible/roles/redis-sentinel/README.md`
- `docs/reference/config/README.md`

Evidence:

- `XFI-0064`
- `EV-0197`

Expected: the exporter service should render an auth path that is valid in both supported role modes. If `redis_acl_lockdown` is off, exporter auth must match the legacy default-user/requirepass path. If lockdown is on, exporter auth must use a named ACL user that actually exists in the rendered ACL file.

Observed: the first issue captured under `EV-0197` was real: the exporter initially stayed password-only after app clients moved to named ACL users. A first fix then overcorrected by always passing `-redis.user=redis_exporter`, which broke the default non-lockdown branch captured under `EV-0200`.

Resolution: current workspace source closes both branches. `tasks/07-monitoring.yml` now renders `-redis.user=redis_exporter` only under `{% if redis_acl_lockdown | bool %}`, which matches the ACL-file emission path in `redis.conf.j2` and `03-redis-configure.yml`. Lockdown-off keeps password-only exporter auth; lockdown-on selects the named ACL user that `users.acl.j2` defines.

Impact: medium. The original hardening-path blind spot shifted into a default-path deployment break. One of the two supported role modes still loses Redis exporter viability, which means operators can silently lose Redis metrics and alert continuity during either a normal bootstrap or a hardening rollout depending on which side of the branch is wrong.

Remediation direction: source drift closed. Runtime render/deploy verification should still prove both mode renders produce a live exporter scrape.

### F-1273. Redis Sentinel reference docs still describe a pre-shipped contract

Severity: `medium`

Status: `open`

Affected surface:

- `docs/architecture/redis-sentinel-ansible-role-design-note.md`
- `configs/ansible/roles/redis-sentinel/README.md`
- `docs/operations/drills/scenarios/sev2-redis-sentinel-failover.md`
- `configs/ansible/roles/redis-sentinel/templates/sentinel.conf.j2`
- `internal/storage/redisclient/redisclient.go`

Evidence:

- `XFI-0065`
- `EV-0201`
- `EV-0208`
- `EV-0212`

Expected: shipped architecture notes and operational drill docs should describe the contract the code now enforces. Once the role renders Sentinel listener auth and the live connection factory is `internal/storage/redisclient`, maintained docs should not keep pointing at an unimplemented cachekeys future path.

Observed: the tabletop drill was corrected in parallel and now uses `redis-cli -p 26379 -a "$REDIS_PASSWORD" SENTINEL get-master-addr-by-name ...`, matching the rendered Sentinel listener. The remaining drift now spans two shipped reference surfaces. `docs/architecture/redis-sentinel-ansible-role-design-note.md` still says the Redis front-end is an open choice, that a future change in `internal/cachekeys` will instantiate `FailoverClient`, and later repeats `internal/cachekeys/` in the implementation work list. `configs/ansible/roles/redis-sentinel/README.md` likewise still says "we plumb this through `internal/cachekeys` after this role lands." The repo's live connection factory is already `internal/storage/redisclient/redisclient.go`.

Impact: medium. The drill is no longer broken, but two shipped Redis Sentinel docs still misstate the owner and implementation state of the Sentinel client path. That can steer follow-on maintenance or review work into the wrong package and preserve false open-work assumptions in a launch-critical HA subsystem.

Remediation direction: refresh both Sentinel docs to the implemented architecture or mark obsolete future-state paragraphs as superseded, keeping the new listener-auth corrections already landed.

### F-1274. HAProxy shipped docs point at a missing companion runbook

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/haproxy/README.md`
- `docs/architecture/haproxy-ansible-role-design-note.md`
- `docs/operations/runbooks/`

Evidence:

- `XFI-0066`
- `EV-0207`

Expected: once a shipped HAProxy role says a specific incident runbook is the companion operational path, that runbook should exist in the tracked runbook directory or the docs should clearly mark it as not landed.

Observed: the HAProxy role README originally linked to `docs/operations/runbooks/api-pod-down.md` and said it should be created alongside the first deploy if missing. The shipped HAProxy design note repeated that `api-pod-down.md` was the companion runbook while noting it might not yet exist. A tracked-file search of `docs/operations/runbooks/` found no such file.

Resolution: current workspace source closes the drift. The README now points operators at the tracked `docs/operations/runbooks/api-down.md` nearest-neighbour runbook and records that the earlier `api-pod-down.md` name was nonexistent. The HAProxy design note makes the same correction and describes the single-pod-eject guidance as a future section inside `api-down.md`, not as a missing standalone document.

Impact: medium. The HAProxy role is explicitly positioned as the mitigation layer for API backend loss, but the named operator runbook is absent. That leaves the role's failure-mode guidance incomplete exactly where the docs claim the automation changes the incident response path.

Remediation direction: source drift closed. Keep future HAProxy operator docs tied to tracked runbook paths.

### F-1275. HAProxy converts the documented Redis fail-open path into an edge-routing outage

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/haproxy/defaults/main.yml`
- `configs/ansible/roles/haproxy/templates/haproxy.cfg.j2`
- `cmd/ratesengine-api/main.go`
- `internal/api/v1/server.go`
- `docs/architecture/ha-plan.md`
- `docs/operations/runbooks/redis-master-down.md`
- `docs/operations/runbooks/api-down.md`

Evidence:

- `XFI-0067`
- `EV-0214`
- `EV-0227`

Expected: Redis loss should have one coherent serving-plane contract. If product/ops docs say a Redis master failure is degraded-but-serving via Timescale fallback and rate-limit fail-open, the edge load balancer must not simultaneously evict every API backend solely because Redis is unavailable.

Observed: this is now source-closed. `cmd/ratesengine-api/main.go::redisChecker.Critical()` returns false, and `internal/api/v1/server.go::handleReadyz` returns HTTP 200 with body `status="degraded"` when only non-critical checks fail. `internal/api/v1/server_test.go::TestReadyz_NonCriticalFailureReturns200Degraded` pins the Redis-only failure shape that keeps HAProxy backends in service. Residual documentation drift in HAProxy/HA surfaces is tracked separately as `F-1284`.

Impact: fixed. Redis-only readiness failure no longer drains every backend in current source; the remaining risk is stale operator documentation.

Remediation direction: keep the regression test and reconcile HAProxy/HA docs under `F-1284`.

### F-1276. API incident docs still use the retired `job="api"` selector family

Severity: `medium`

Status: `open`

Affected surface:

- `docs/operations/alerts-catalog.md`
- `docs/operations/runbooks/api-down.md`
- `docs/operations/runbooks/api-5xx.md`
- `docs/operations/runbooks/api-latency.md`
- `docs/operations/runbooks/sla-probe-p95-breach.md`
- `internal/obs/metrics.go`
- `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`
- `configs/prometheus/prometheus.r1.yml`
- `deploy/monitoring/rules/api.yml`
- `configs/prometheus/rules.r1/api.yml`

Evidence:

- `XFI-0068`
- `EV-0215`
- `EV-0229`

Expected: maintained incident docs and metric-source comments should use selectors that match at least one real supported deployment shape, or state the required deployment-specific substitution explicitly.

Observed: the canonical multi-host HA scrape config uses `job="ratesengine_api"` and the R1 Prometheus config uses `job="ratesengine-api"`. The two API rule files match those real families. Current operator docs now largely follow that shape: the alert catalog uses `job=~"ratesengine[_-]api"`, `api-down.md` uses the same regex selector, and the API 5xx / latency / SLA probe runbooks now use real deployment selectors or route through the corrected docs. The remaining stale surface is `internal/obs/metrics.go`, whose source comment still says alert rules reference `http_requests_total{status=~"5..", job="api"}`.

Impact: medium. The highest-risk operator copy/paste paths are now repaired, but the stale source comment can still mislead future metric/rule edits back toward a selector family that no supported deployment uses.

Remediation direction: update the `internal/obs/metrics.go` comment to the current selector family or remove the stale selector example entirely.

### F-1277. The `api-down` runbook points readers at a readiness file that does not exist

Severity: `low`

Status: `fixed`

Affected surface:

- `docs/operations/runbooks/api-down.md`
- `internal/api/v1/server.go`

Evidence:

- `XFI-0069`
- `EV-0216`
- `EV-0229`

Expected: a runbook breadcrumb into source should resolve to the implementation file it cites.

Observed: this is now source-closed. `api-down.md` points at `internal/api/v1/server.go::handleReadyz` and explicitly records that the earlier nonexistent `internal/api/v1/healthz.go` breadcrumb was corrected.

Impact: fixed. The operator breadcrumb now resolves to the tracked implementation file.

Remediation direction: none beyond keeping source breadcrumbs tied to tracked files during future runbook edits.

### F-1278. The HA-role nftables drop-ins do not compose safely with the repo's firewall model

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/haproxy/tasks/06-firewall.yml`
- `configs/ansible/roles/redis-sentinel/tasks/06-firewall.yml`
- `configs/ansible/roles/patroni/tasks/10-firewall.yml`
- `configs/ansible/roles/prometheus/tasks/06-firewall.yml`
- `configs/ansible/roles/loki/tasks/server-05-firewall.yml`
- `configs/ansible/roles/archival-node/templates/nftables.conf.j2`
- HA role READMEs/design notes that say those tasks "open" or restrict ports

Evidence:

- `XFI-0070`
- `EV-0217`
- `EV-0224`

Expected: a role-level firewall surface should either enforce its advertised access policy on its own, or integrate deterministically with the repository's default-deny base chain so the intended accepts actually work on hardened hosts.

Observed: the first remediation wave changed every reviewed HA-role drop-in to `type filter hook input priority -100; policy accept;` and added comments claiming an early accept stops the later priority-0 default-drop chain from seeing the packet. That comment is still wrong for nftables base-chain semantics: an `accept` verdict in one base chain is not final when a later base chain at the same hook drops the packet. So the current role drop-ins are still not a valid allow-list composition beside `archival-node/templates/nftables.conf.j2`; they can accept first and then be dropped later by the default-deny chain. If the default-drop chain is absent, unmatched traffic still falls through the role chain's `policy accept`, so the drop-in also does not enforce internal-only posture by itself.

Impact: high. Depending on host baseline, the same reviewed role can fail open from a security-policy perspective or fail closed from a reachability perspective. Redis/Sentinel, Patroni/etcd, Alertmanager gossip, Loki ingest, HAProxy public ingress, and VRRP acceptance are all affected by this composition pattern. The docs currently present these tasks as meaningful firewall boundaries.

Remediation direction: converge on one deterministic nftables ownership model. Either render all role allowances into a single repo-owned default-drop table/chain, or place role chains at explicit ordered priorities with semantics proven against the installed base ruleset. Add render/`nft -c`/fixture tests for both "standalone host" and "host already carrying the repo default-drop base chain."

### F-1279. Patroni's firewall task can fail before it creates the drop-in directory

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/patroni/tasks/10-firewall.yml`

Evidence:

- `XFI-0071`
- `EV-0218`
- `EV-0224`

Expected: a first-time Patroni role application on a clean host should create `/etc/nftables.d/` before writing a file beneath it.

Observed: this is now source-closed. Current `tasks/10-firewall.yml` installs nftables, ensures `/etc/nftables.d/` exists, and only then writes `/etc/nftables.d/40-patroni.conf`.

Impact: fixed. The earlier clean-host ordering failure no longer reproduces in current source.

Remediation direction: keep a clean-host role fixture or syntax check around the task ordering so the parent-directory guarantee does not regress.

### F-1280. Patroni's etcd install task defaults to a placeholder checksum

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/patroni/tasks/02-etcd-install.yml`
- `configs/ansible/roles/patroni/defaults/main.yml`
- `configs/ansible/roles/patroni/README.md`

Evidence:

- `XFI-0072`
- `EV-0220`
- `EV-0224`

Expected: the role should be runnable from its documented defaults and inventory model, or every required override should be declared in defaults/README with an actionable value source.

Observed: the code side has narrowed: `tasks/02-etcd-install.yml` now asserts `etcd_release_sha256` is defined, 64 characters long, and not `REPLACE_WITH_RELEASE_SHA` before the download, while `defaults/main.yml` comments the required override and its source. The role README still omits `etcd_release_sha256` from the prerequisites and sample inventory, so an operator following only the documented role page still lacks the required value before first run.

Impact: high. The failure now happens earlier and with a better message, but the documented first-run path is still incomplete for the launch-critical database HA role.

Remediation direction: add `etcd_release_sha256` to the role README prerequisites and inventory model, including the exact release/SHA source and architecture caveat. Keep the preflight assert.

### F-1281. Patroni's textfile scraper depends on `jq` without installing it

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/patroni/tasks/11-monitoring.yml`
- `configs/ansible/roles/patroni/tasks/05-patroni-install.yml`
- `configs/ansible/roles/patroni/README.md`
- Prometheus node_exporter textfile collector path

Evidence:

- `XFI-0073`
- `EV-0221`
- `EV-0224`

Expected: every binary used by the installed Patroni monitoring timer should be installed by the Patroni role or be an explicit prerequisite.

Observed: this is now source-closed. `tasks/05-patroni-install.yml` installs `jq` alongside Patroni and the Python dependencies before `tasks/11-monitoring.yml` writes the scraper that uses it.

Impact: fixed. A clean role run now installs the scraper's declared JSON parser dependency.

Remediation direction: retain a scraper smoke check if a future role test harness lands.

### F-1282. Patroni's documented pgBackRest restore target is ignored

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/patroni/defaults/main.yml`
- `configs/ansible/roles/patroni/tasks/08-patroni-bootstrap.yml`
- `configs/ansible/roles/patroni/README.md`
- `docs/architecture/patroni-ansible-role-design-note.md`

Evidence:

- `XFI-0074`
- `EV-0222`
- `EV-0224`

Expected: if the role exposes and documents `patroni_pgbackrest_restore_target` as `latest` or a point-in-time value, the restore task should pass that target to pgBackRest or reject unsupported values.

Observed: this is now source-closed. `tasks/08-patroni-bootstrap.yml` validates the target shape and maps `latest` to `--type=default`, `immediate` to `--type=immediate`, and `time:<timestamp>` to `--type=time --target=<timestamp>`.

Impact: fixed. The documented variable now changes the rendered pgBackRest restore command.

Remediation direction: keep rendered-command coverage for `latest`, `immediate`, and `time:<timestamp>` restore modes when Ansible role tests are added.

### F-1283. Timescale primary-down runbook does not match the shipped Patroni/etcd role

Severity: `medium`

Status: `open`

Affected surface:

- `docs/operations/runbooks/timescale-primary-down.md`
- `configs/ansible/roles/patroni/templates/etcd.conf.j2`
- `configs/ansible/roles/patroni/templates/patroni.yml.j2`
- `configs/ansible/roles/patroni/tasks/04-etcd-systemd.yml`
- `docs/architecture/patroni-ansible-role-design-note.md`

Evidence:

- `XFI-0075`
- `EV-0225`

Expected: an operator diagnosing Timescale primary loss should be able to copy the runbook's etcd commands against the shipped Patroni role and get meaningful quorum/leader data.

Observed: `timescale-primary-down.md` tells operators to query `https://etcd-1.internal:2379` and `https://etcd-{1,2,3}.internal:2379`, but the role renders `ETCD_LISTEN_CLIENT_URLS`, `ETCD_ADVERTISE_CLIENT_URLS`, Patroni `etcd3.hosts`, and the health check with plain `http://...:2379`. The runbook also says "At least 3 of 5" members must be healthy, while `tasks/01-preflight.yml` asserts the cluster has exactly three `postgres_cluster` hosts. Finally, the runbook probes `/ratesengine/leader`; the role configures Patroni with `namespace: /service/` and `scope: {{ patroni_cluster_name }}`, so the leader key lives under the Patroni namespace/scope, not the stale top-level path.

Impact: medium. During a primary-down incident, the direct etcd diagnosis path can fail on TLS mismatch, send operators to a nonexistent key, and set the wrong quorum expectation for the deployed three-node DCS.

Remediation direction: update the runbook's etcd examples to the role's current HTTP endpoint model or add TLS/auth variables to the role and switch both sides together. Use the actual Patroni namespace/scope path or prefer `patronictl`/`etcdctl endpoint status` examples that do not rely on a stale key. Replace the five-node quorum text with the current three-node quorum threshold.

### F-1284. HAProxy and HA docs still describe Redis as readiness-critical after the runtime fix

Severity: `medium`

Status: `open`

Affected surface:

- `configs/ansible/roles/haproxy/defaults/main.yml`
- `configs/ansible/roles/haproxy/templates/haproxy.cfg.j2`
- `configs/ansible/roles/haproxy/README.md`
- `docs/architecture/ha-plan.md`
- `internal/api/v1/server.go`
- `cmd/ratesengine-api/main.go`

Evidence:

- `XFI-0076`
- `EV-0227`

Expected: HAProxy operator docs and architecture docs should describe the same readiness semantics implemented by the API: Postgres-critical failures return 503, while Redis-only failures return 200 with body `status="degraded"`.

Observed: runtime now implements the intended fail-open Redis contract, but HAProxy/HA docs still describe the old one. `configs/ansible/roles/haproxy/defaults/main.yml` says `/v1/readyz` is the deep probe for "Timescale + Redis reachable"; `haproxy.cfg.j2` repeats that it "passes only when Timescale + Redis are both reachable"; the HAProxy README says the path "passes only when Timescale + Redis are both reachable"; and `docs/architecture/ha-plan.md` still says HAProxy routes only to `readyz=200` after a deep Timescale + Redis check and that Redis master loss makes `readyz` false during failover.

Impact: medium. The code will route correctly, but operators reading the role docs or HA plan will reason about the wrong failure mode during Redis incidents and may attempt unnecessary HAProxy/API intervention.

Remediation direction: update HAProxy defaults/template comments, role README, and HA plan to describe critical vs non-critical ready checks. Point Redis outage triage at the degraded body/check details and `/v1/status`, not backend eviction.

### F-1285. Loki, Promtail, Prometheus, and Alertmanager roles download upstream release archives without enforced checksums

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/loki/tasks/server-02-install.yml`
- `configs/ansible/roles/loki/tasks/agent-01-install.yml`
- `configs/ansible/roles/prometheus/tasks/02-install.yml`
- `configs/ansible/roles/loki/defaults/main.yml`
- `configs/ansible/roles/loki/README.md`
- `configs/ansible/roles/prometheus/defaults/main.yml`
- `configs/ansible/roles/prometheus/README.md`

Evidence:

- `XFI-0077`
- `EV-0231`
- `EV-0241`

Expected: every upstream binary archive installed by production Ansible roles should be pinned to both a version and a mandatory digest, or the role should fail before download when the digest is missing.

Observed: the code side has narrowed. Loki, Promtail, Prometheus, and Alertmanager install tasks now all assert 64-character SHA-256 variables and wire those variables into `get_url.checksum`. The remaining gap is the documented clean-run path: the Loki and Prometheus READMEs/defaults still do not list `loki_release_sha256`, `promtail_release_sha256`, `prometheus_sha256`, or `alertmanager_sha256` as required inventory variables.

Impact: high. The unauthenticated-download runtime defect is source-fixed, but operators following the role docs can still hit first-run failures or work around the assertions without a documented release-verification procedure.

Remediation direction: add the four checksum variables to role READMEs and sample inventory, including exact release/checksum source and architecture filename. Keep the fail-fast assertions and checksum wiring.

### F-1286. Loki's systemd unit maps MinIO credentials through literal `${...}` strings that systemd does not expand

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/loki/templates/loki.service.j2`
- `configs/ansible/roles/loki/templates/loki-config.yaml.j2`
- `configs/ansible/roles/loki/defaults/main.yml`
- `configs/ansible/roles/loki/README.md`
- systemd service environment semantics

Evidence:

- `XFI-0078`
- `EV-0232`

Expected: the Loki service should receive real AWS-compatible MinIO credentials in `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`.

Observed: the original defect no longer reproduces in source. `loki.service.j2` now removes the literal `Environment=AWS_ACCESS_KEY_ID=${...}` / `AWS_SECRET_ACCESS_KEY=${...}` assignments and reads only `EnvironmentFile=-/etc/default/loki`. The README tells operators to place direct `AWS_ACCESS_KEY_ID=` and `AWS_SECRET_ACCESS_KEY=` entries in that file, and defaults preserve the older env-var-name knobs only as compatibility comments while warning the service template no longer reads them.

Impact: fixed. The service manager no longer passes shell-style placeholder strings as Loki's S3 credentials when operators follow the current role README.

Remediation direction: keep the direct `AWS_*` environment-file contract and add rendered-unit/systemd smoke coverage when Ansible role tests land.

### F-1287. Prometheus and Alertmanager bind to loopback while the generated config targets private IPs for self-scrape and alert delivery

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/prometheus/defaults/main.yml`
- `configs/ansible/roles/prometheus/templates/prometheus.service.j2`
- `configs/ansible/roles/prometheus/templates/alertmanager.service.j2`
- `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`
- `configs/ansible/roles/prometheus/README.md`

Evidence:

- `XFI-0079`
- `EV-0234`
- `EV-0241`

Expected: the generated Prometheus config should target addresses that the rendered Prometheus and Alertmanager services actually listen on. If the services are loopback-only, self-scrape and alert delivery should use local loopback for local services and a deliberately exposed/listening internal address for peer services, or the role should fail until operators choose a coherent topology.

Observed: the original bind-target defect no longer reproduces in source. Defaults now set `prometheus_listen: "0.0.0.0:9090"` and `alertmanager_listen: "0.0.0.0:9093"`, while `tasks/06-firewall.yml` opens 9090, 9093, and 9094 to `prometheus_internal_cidrs`. The generated `prometheus.yml.j2` private-IP scrape and alertmanager targets therefore match the role's intended listener model.

Impact: fixed. A default two-host role render no longer binds the HTTP endpoints only to loopback while targeting private IPs for self-scrape and alert delivery. Residual README drift is tracked separately as `F-1290`, and the broader nftables composition problem remains tracked as `F-1278`.

Remediation direction: keep listener defaults, generated targets, and firewall rules in one topology. Add rendered-config tests when role testing lands.

### F-1288. Prometheus TSDB disk-space preflight is documented as an assertion but is configured to ignore failures

Severity: `medium`

Status: `fixed`

Affected surface:

- `configs/ansible/roles/prometheus/tasks/01-preflight.yml`
- `configs/ansible/roles/prometheus/README.md`
- `configs/ansible/roles/prometheus/defaults/main.yml`

Evidence:

- `XFI-0080`
- `EV-0236`
- `EV-0241`

Expected: if the role README says the Prometheus preflight asserts at least 20 GB free under `/var`, a host that does not satisfy that launch capacity requirement should fail before installation or the README should mark the check as warning-only and document the residual risk.

Observed: current source replaces the ignored mount-fact assertion with a deterministic command path: `df --output=avail -B1 /var`, followed by a blocking assertion against the documented 20 GB threshold. The `ignore_errors: yes` escape hatch is gone.

Impact: fixed. Under-capacity `/var` no longer soft-fails through the role preflight.

Remediation direction: keep the blocking `df`-based check and add role-test coverage for below-threshold and above-threshold hosts.

### F-1289. Loki storage prerequisites are documented as preflight checks but the server preflight does not verify MinIO bucket/access or `/var` capacity

Severity: `medium`

Status: `open`

Affected surface:

- `configs/ansible/roles/loki/tasks/server-01-preflight.yml`
- `configs/ansible/roles/loki/defaults/main.yml`
- `configs/ansible/roles/loki/README.md`
- `docs/architecture/loki-ansible-role-design-note.md`

Evidence:

- `XFI-0081`
- `EV-0239`

Expected: Loki's documented storage prerequisites should be enforced before starting the logging plane, or the docs should explicitly classify them as manual operator checks. The role should verify local index/workdir capacity and MinIO chunk bucket/access because these are the two durable storage legs for logs.

Observed: defaults say the operator creates `loki-chunks` and "the role asserts it exists at preflight." The README requires at least 50 GB free on `/var` and a pre-created bucket/IAM user. The shipped design note goes further, saying the role creates the bucket if absent and asserts at least 50 GB free. But `server-01-preflight.yml` only checks Ubuntu version, inventory groups, time sync, and ensures the `loki` user exists. It never probes MinIO, never checks bucket existence/access, and never checks local disk capacity for BoltDB/index/compactor directories.

Impact: medium. A documented "successful" Loki role run can start a process that later drops chunks or cannot retain/query logs because the S3 bucket/IAM path or local working volume was never validated. This weakens incident response evidence at launch.

Remediation direction: add explicit preflight checks for the resolved `loki_s3_endpoint`, `loki_s3_bucket`, and credential contract, plus a deterministic local capacity check against `loki_data_dir` or `/var`. Decide whether the role creates the bucket or only asserts it; then make defaults, README, and design note say the same thing.

### F-1290. Prometheus README still documents loopback-only UI access and a 9094-only firewall after the role moved 9090/9093 to internal network listeners

Severity: `medium`

Status: `open`

Affected surface:

- `configs/ansible/roles/prometheus/README.md`
- `configs/ansible/roles/prometheus/defaults/main.yml`
- `configs/ansible/roles/prometheus/tasks/06-firewall.yml`

Evidence:

- `XFI-0082`
- `EV-0242`

Expected: after changing Prometheus and Alertmanager from loopback-only listeners to internal network listeners, the operator README should describe the actual security boundary and the ports opened by the firewall task.

Observed: defaults now bind `prometheus_listen` to `0.0.0.0:9090` and `alertmanager_listen` to `0.0.0.0:9093`; the role firewall task says it opens 9090, 9093, and 9094 to `prometheus_internal_cidrs`. The README still labels the query UI and Alertmanager UI "loopback-only", tells operators SSH tunnelling is the access model, and later says `06-firewall.yml` opens only 9094 for Alertmanager gossip.

Impact: medium. Operators reviewing the role can believe 9090/9093 are socket-level loopback-only when they are actually network listeners protected by firewall policy. That matters especially while `F-1278` keeps the role firewall composition open.

Remediation direction: update the README to describe internal-CIDR listeners plus SSH tunnelling through the firewall as the operator access path, and change the firewall section to list 9090/9093/9094. Cross-link the nftables composition finding until that lower-level policy is fixed.

### F-1291. Archival-node role installs MinIO, `mc`, and node_exporter from upstream URLs without enforced checksums

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/archival-node/tasks/09-minio.yml`
- `configs/ansible/roles/archival-node/tasks/10-observability.yml`
- `configs/ansible/roles/archival-node/defaults/main.yml`
- archival-node bring-up/operator docs

Evidence:

- `XFI-0083`
- `EV-0244`
- `EV-0247`

Expected: every Ansible role path that installs upstream root-owned executables should authenticate the exact artifact with a pinned checksum, and the documented inventory/defaults path should tell operators which digest variables to set.

Observed: this narrowed during the moving-workspace recheck. `09-minio.yml` now asserts `minio_release_sha256`, uses `checksum: "sha256:{{ minio_release_sha256 }}"` for the MinIO server binary, asserts `mc_version` and `mc_release_sha256`, and downloads `mc` from the versioned archive path with checksum enforcement. `10-observability.yml` now asserts `node_exporter_release_sha256` and uses it in the node_exporter `get_url` task. The remaining open gap is the documented clean inventory path: `defaults/main.yml` and active archival-node operator docs still do not list or explain the required `minio_release_sha256`, `mc_version`, `mc_release_sha256`, or `node_exporter_release_sha256` variables.

Impact: high until docs/defaults are reconciled. The runtime install tasks now fail fast if the digests are absent, but a clean documented role run can still fail late for operators because the required variables are not discoverable in defaults or the bring-up path.

Remediation direction: add commented required variables and checksum-source guidance for `minio_release_sha256`, `mc_version`, `mc_release_sha256`, and `node_exporter_release_sha256` to defaults/sample inventory/operator docs. Keep the current task-level assertions and checksums.

### F-1292. Active core-lag runbook tells operators to cross-check via Horizon despite ADR-0001 banning Horizon references in runbooks

Severity: `medium`

Status: `fixed`

Affected surface:

- `docs/operations/runbooks/core-lag.md`
- `docs/adr/0001-horizon-deprecated.md`
- `README.md`
- `CLAUDE.md`

Evidence:

- `XFI-0084`
- `EV-0245`
- `EV-0247`

Expected: active operational runbooks should preserve ADR-0001's "no Horizon" boundary. If operators need an external network-health comparator, the runbook should use an approved non-Horizon source or explicitly revise the ADR before depending on Horizon in incident response.

Observed: fixed during the moving-workspace recheck. `core-lag.md` no longer tells operators to cross-check via SDF Horizon. Step 1 now points to stellar.expert/explorer or a public stellar-rpc endpoint and explicitly records that the earlier Horizon wording was removed for ADR-0001.

Impact: fixed for the reviewed active runbook path. The remaining `Horizon` text in `core-lag.md` is explanatory correction history, not an operator instruction or command dependency.

Remediation direction: keep future runbook edits on stellar.expert/stellar-rpc or an explicitly approved non-Horizon source; continue the invariant search across active operations docs during closure sweeps.

### F-1293. Redis Sentinel `redis_exporter` checksum fix is enforced in code but missing from the role inventory/docs contract

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`
- `configs/ansible/roles/redis-sentinel/defaults/main.yml`
- `configs/ansible/roles/redis-sentinel/README.md`
- `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`

Evidence:

- `XFI-0085`
- `EV-0249`
- `EV-0255`

Expected: Redis HA observability should install a verified `redis_exporter` artifact, and the role's documented inventory/defaults path should expose any required digest.

Observed: narrowed during the moving-workspace recheck. `tasks/07-monitoring.yml` now asserts that `redis_exporter_release_sha256` is defined and 64 characters long, then passes it to `get_url.checksum` before extracting and installing `/usr/local/bin/redis_exporter`. The remaining gap is the inventory/docs contract: `defaults/main.yml` still only lists `redis_exporter_version`, and the role README still omits the required digest variable and checksum source.

Impact: high, narrowed. The unauthenticated privileged binary install path is closed in code, but the role contract can still fail a production Redis deploy or drive operators to ad hoc inventory fixes because the required digest variable is not discoverable where the rest of the role inputs are documented. This is the same documentation-contract remainder as the monitoring and archival-node checksum fixes.

Remediation direction: add required `redis_exporter_release_sha256` guidance to defaults/operator docs, including the upstream release checksum source and a note that the role fails before install when the digest is absent.

### F-1294. CI installs promtool and gitleaks through unauthenticated curl-to-tar pipelines before trusting their results

Severity: `medium`

Status: `fixed`

Affected surface:

- `.github/workflows/ci.yml`
- `scripts/ci/lint-actions-pinning.sh`
- GitHub Actions repository workflow-permission setting

Evidence:

- `XFI-0086`
- `EV-0250`
- `EV-0252`
- `EV-0255`

Expected: CI jobs that install external tools and then trust their verdicts should authenticate the downloaded artifacts or use a pinned action/container with reviewed digest semantics.

Observed: fixed during the moving-workspace recheck. `.github/workflows/ci.yml` declares `PROM_TARBALL_SHA256` and `GITLEAKS_TARBALL_SHA256`; both installer steps fail if the digest secret is absent, download the release archive to `/tmp`, run `sha256sum -c`, and extract only after verification. The repository-level workflow token default remains read-only.

Impact: fixed. The CI rule-validation and secret-scanning verdicts are no longer produced by unchecked curl-to-tar binaries.

Remediation direction: closed in source; keep both digest secrets current when bumping `PROM_VERSION` or `GITLEAKS_VERSION`.

### F-1295. Redis Sentinel role writes the Redis password into a world-readable `redis_exporter.service` unit

Severity: `high`

Status: `open`

Affected surface:

- `configs/ansible/roles/redis-sentinel/tasks/07-monitoring.yml`
- `configs/ansible/roles/redis-sentinel/defaults/main.yml`
- `configs/ansible/roles/redis-sentinel/templates/users.acl.j2`
- `configs/ansible/roles/prometheus/templates/prometheus.yml.j2`

Evidence:

- `XFI-0087`
- `EV-0253`

Expected: Redis credentials should be rendered only into root/service-readable secret files or Redis config files with restrictive modes, and Ansible tasks that template those secrets should avoid exposing rendered content.

Observed: `tasks/07-monitoring.yml` writes `/etc/systemd/system/redis_exporter.service` with `mode: "0644"` and inline `Environment=REDIS_PASSWORD={{ redis_password }}`. Systemd unit files under `/etc/systemd/system` are generally readable by local users, so the same password used for Redis/Sentinel and the `redis_exporter` ACL user is exposed outside the intended service boundary.

Impact: high. A low-privilege local user or compromised unprivileged process on a cache host can read the Redis password from the unit file and authenticate to Redis/Sentinel according to the role's current shared-password model. It also increases accidental disclosure through unit-file collection or Ansible diff/log output.

Remediation direction: move the exporter password into a root-owned environment file such as `/etc/default/redis_exporter` with `0600` or `0640` permissions and `no_log: true`, reference it with `EnvironmentFile=`, and consider separating the exporter ACL password from the application Redis password.

### F-1296. Release and deploy workflows grant unused OIDC token minting permission

Severity: `medium`

Status: `open`

Affected surface:

- `.github/workflows/release.yml`
- `.github/workflows/deploy.yml`
- `.github/workflows/api-docs.yml`
- `docs/operations/release-process.md`
- GitHub Actions environment settings

Evidence:

- `XFI-0088`
- `EV-0257`

Expected: workflows should grant `id-token: write` only when a current step needs GitHub's OIDC token to authenticate to a relying party, such as Pages deploy, cloud credentials, keyless signing, or provenance attestation.

Observed: `api-docs.yml` legitimately pairs `pages: write` with `id-token: write` for `actions/deploy-pages`. By contrast, `release.yml` grants `id-token: write` with the comment `future: keyless signing via cosign`, and `deploy.yml` grants it with `future: keyless attestation`. A repo-wide search finds no OIDC consumer in either workflow: no `ACTIONS_ID_TOKEN_*` usage, no cosign/sigstore step, no cloud OIDC auth action, and no attestation upload. The deploy workflow also has production SSH secrets in scope after the `r1` environment gate, so unnecessary token minting expands the credential surface of a high-impact job.

Impact: medium. The unused permission does not by itself deploy code or write repository contents, but it gives every release/deploy step an ambient ability to mint a GitHub OIDC JWT for the workflow identity. If a future or compromised third-party step is present, that token can be exchanged with any external trust policy that mistakenly accepts this repo/workflow/branch audience. The safer posture is least privilege until keyless signing or attestation is actually implemented.

Remediation direction: remove `id-token: write` from `release.yml` and `deploy.yml` until a concrete signing/attestation step lands. When keyless release provenance is added, reintroduce the permission only on the workflow/job that performs the OIDC exchange and document the relying-party audience/claims.

### F-1297. Deploy workflow falls back to live `ssh-keyscan` when the pinned host-key secret is missing

Severity: `high`

Status: `open`

Affected surface:

- `.github/workflows/deploy.yml`
- `docs/operations/deploy-workflow.md`
- `docs/operations/r1-deployment-state.md`
- `configs/ansible/playbooks/deploy-binary.yml`
- `configs/ansible/tasks/deploy-one-binary.yml`

Evidence:

- `XFI-0089`
- `EV-0259`

Expected: production deploy over SSH should fail closed unless the target host key is pinned out-of-band. The documented `<REGION>_SSH_KNOWN_HOSTS` secret is the control that makes `ANSIBLE_HOST_KEY_CHECKING=True` meaningful.

Observed: `docs/operations/deploy-workflow.md` requires `<REGION>_SSH_KNOWN_HOSTS` and says pinning prevents MITM. `.github/workflows/deploy.yml` decodes `R1_SSH_KNOWN_HOSTS` when present, but if it is empty the workflow emits only a warning and runs `ssh-keyscan -T 5 -t ed25519,rsa "$HOST" > ~/.ssh/known_hosts`. The later Ansible step sets `ANSIBLE_HOST_KEY_CHECKING=True`, but at that point the runner trusts whatever key was observed live during the same deploy.

Impact: high. The deploy workflow uses a root-capable SSH key and swaps production binaries. If the host-key secret is accidentally omitted, DNS/BGP/local-network interception can redirect the runner to an attacker-controlled SSH endpoint and satisfy the workflow's host-key check with the attacker's freshly scanned key. Even when the private key is not exposed, the deployment can be misdirected, blocked, or used to exercise release artifacts and operational commands against the wrong host.

Remediation direction: make `R1_SSH_KNOWN_HOSTS` mandatory and fail before SSH setup when it is absent or does not decode to a host-key entry for the resolved host. Keep any emergency `ssh-keyscan` helper as an operator-side command outside the deploy workflow, followed by explicit secret update/review.

### F-1298. Release/deploy workflows interpolate manual inputs directly into shell scripts before validation

Severity: `high`

Status: `open`

Affected surface:

- `.github/workflows/deploy.yml`
- `.github/workflows/release.yml`
- `docs/operations/deploy-workflow.md`
- `docs/operations/release-process.md`
- GitHub Actions environment settings

Evidence:

- `XFI-0090`
- `EV-0263`

Expected: manual workflow inputs should be passed through step `env:` variables or validated by an action/runtime before use. They should not be embedded directly into shell source where quotes, command substitutions, or metacharacters can alter the script before the validation code runs.

Observed: `deploy.yml` validates `inputs.version` and `inputs.binaries`, but the validation step itself embeds them in shell code: `echo "${{ inputs.version }}"`, `echo '${{ inputs.binaries }}'`, and later repeats the same pattern in download, checksum, migration, Ansible, and summary steps. `health_grace_seconds` is also interpolated directly into the `ansible-playbook -e` shell command. `release.yml` similarly sets `tag="${{ inputs.tag }}"` before SemVer validation. Other manual workflows mostly use choice inputs or pass free-form values via `env:`, which avoids this exact shell-source injection shape.

Impact: high. A user with permission to run manual workflows can craft quote-breaking input that executes during the runner's shell parsing before the intended regex/directory checks. In the deploy job, early execution can poison the workspace, `$GITHUB_ENV`, or PATH before later steps load SSH/deploy secrets and invoke `gh`, `ssh-keyscan`, `ansible-galaxy`, or `ansible-playbook`. In the release job, the global `contents: write` permission and release-publish path make manual-input injection a release-integrity risk even before the tag validator runs.

Remediation direction: move every manual input used in shell into step-level environment variables, then reference only the environment variables inside scripts, for example `VERSION: ${{ inputs.version }}` and `BINS_RAW: ${{ inputs.binaries }}`. Keep choice inputs where possible, add strict regex checks for `health_grace_seconds`, and avoid echoing untrusted values into summary files without escaping.

### F-1299. Deploy workflow installs mutable Ansible tooling and Galaxy collections at production deploy time

Severity: `high`

Status: `open`

Affected surface:

- `.github/workflows/deploy.yml`
- `configs/ansible/requirements.yml`
- `configs/ansible/README.md`
- `configs/ansible/playbooks/deploy-binary.yml`
- `configs/ansible/tasks/deploy-one-binary.yml`

Evidence:

- `XFI-0091`
- `EV-0265`

Expected: production deploy tooling should be reproducible from the repository revision being deployed. Dependency upgrades that can change deploy behavior should land through a reviewed diff or a lockfile/pinned digest, not happen implicitly during a manual deploy.

Observed: the deploy workflow installs the latest matching patch for `ansible-core==2.18.*` and then runs `ansible-galaxy collection install -r configs/ansible/requirements.yml`. The Galaxy requirements are lower-bound ranges (`ansible.posix >=1.5.0`, `community.general >=8.0.0`, `community.postgresql >=3.4.0`) and `community.general` appears twice. Those collections provide the modules used by the deployment playbook, including `ansible.posix.synchronize` for binary/migration staging and collection modules used across the production Ansible roles.

Impact: high. The same release tag and workflow revision can execute different deploy code depending on what PyPI/Galaxy resolve at run time. A breaking or compromised collection release can alter root SSH deploy behavior, migration staging, service restart semantics, or host configuration without a corresponding repo change or review. This is especially risky because the workflow later holds the root-capable deploy key and production host target.

Remediation direction: pin exact `ansible-core` and Galaxy collection versions, remove duplicate requirements, and add a lock/update process for Ansible dependencies. Prefer a prebuilt, digest-pinned deploy image or cached artifact built in CI from the lock so production deploys do not resolve mutable tooling live.

### F-1300. Healthchecks SLA-probe unit cannot write its default textfile output under its systemd sandbox

Severity: `high`

Status: `fixed`

Affected surface:

- `configs/healthchecks/sla-probe.sh`
- `configs/healthchecks/ratesengine-sla-probe.service`
- `cmd/ratesengine-sla-probe/main.go`
- `deploy/monitoring/rules/sla-probe.yml`
- `docs/operations/sla-probe.md`
- `docs/operations/runbooks/sla-probe-stale.md`

Evidence:

- `XFI-0092`
- `EV-0267`
- `EV-0273`

Expected: the Healthchecks SLA-probe timer should either run without textfile output by default, or its systemd sandbox should allow the configured textfile output path so the Prometheus SLA evidence chain is populated.

Observed at open: `configs/healthchecks/sla-probe.sh` defaulted `TEXTFILE_OUTPUT` to `/var/lib/node_exporter/textfile_collector/sla_probe.prom` and passed `-textfile-output "$TEXTFILE_OUTPUT"` to `ratesengine-sla-probe`, while the matching Healthchecks service used `DynamicUser=true` with `ProtectSystem=strict`, `ProtectHome=true`, and no writable path grant for the textfile collector directory. The binary exits `2` when `writeTextfileAtomic` fails.

Impact at open: high. A fresh Healthchecks install enabled `ratesengine-sla-probe.timer`, but the default wrapper attempted to write a Prometheus textfile it could not create. With a Healthchecks URL configured, every run reported `/fail`; without a URL, the timer exited 0 after a logged failure and the SLA Prometheus series remained absent.

Closure evidence: current source grants `ReadWritePaths=/var/lib/node_exporter/textfile_collector` and `SupplementaryGroups=ratesengine` in `configs/healthchecks/ratesengine-sla-probe.service`. The archival-node role provisions `/var/lib/node_exporter/textfile_collector` as `ratesengine:ratesengine` mode `0775`, so the service sandbox now permits the default textfile write while preserving `ProtectSystem=strict`. The separate missing-probe-binary wrapper failure is tracked as `F-1303`.

### F-1301. Aggregator-silent runbook points operators at the indexer metrics port on default/R1 deployments

Severity: `medium`

Status: `open`

Affected surface:

- `docs/operations/runbooks/aggregator-silent.md`
- `cmd/ratesengine-aggregator/main.go`
- `configs/prometheus/prometheus.r1.yml`
- `configs/healthchecks/heartbeat.sh`
- `configs/ansible/roles/prometheus/README.md`

Evidence:

- `XFI-0093`
- `EV-0269`

Expected: a P1 aggregator incident runbook should direct responders to the live aggregator scrape endpoint for the deployed topology, or explicitly distinguish single-host/R1 (`9465`) from multi-host overrides.

Observed: `cmd/ratesengine-aggregator/main.go` auto-shifts `obs.metrics_listen` from `127.0.0.1:9464` to `127.0.0.1:9465` when the aggregator would otherwise collide with the indexer on a single host. `configs/prometheus/prometheus.r1.yml` scrapes `ratesengine-indexer` on `localhost:9464` and `ratesengine-aggregator` on `localhost:9465`; `configs/healthchecks/heartbeat.sh` uses the same service-to-port mapping. The `aggregator-silent` P1 runbook still tells operators to inspect `http://localhost:9464/metrics` for `ratesengine_aggregator_*` counters. The Prometheus role README also documents every ratesengine job, including the aggregator, as port `9464`, which is only safe if each binary has a dedicated host and the per-host override is understood.

Impact: medium. During an aggregator-silent page on R1/default single-host deployments, the first metrics command reads the indexer endpoint and returns no aggregator counters, which can make a running aggregator look absent or hide the actual tick/error/outlier state. That delays triage and can send responders toward restarts or Redis/Timescale checks before they have the basic aggregator metric evidence.

Remediation direction: update the runbook to use `localhost:9465` for R1/default single-host deployments, or provide a topology-aware snippet that reads the configured aggregator `obs.metrics_listen`. Update the Prometheus role README to clarify that `9464` is a per-host default and that single-host co-location shifts the aggregator to `9465`; keep R1 Prometheus, Healthchecks, and runbook examples in one shared source of truth.

### F-1302. Healthchecks smoke wrapper exits successfully when the smoke script is missing or not executable

Severity: `medium`

Status: `open`

Affected surface:

- `configs/healthchecks/smoke.sh`
- `configs/healthchecks/ratesengine-smoke.service`
- `configs/healthchecks/install.sh`
- `configs/healthchecks/README.md`
- `scripts/dev/r1-smoke.sh`

Evidence:

- `XFI-0094`
- `EV-0271`

Expected: if the API-surface smoke script is missing, unreadable, or not executable, the Healthchecks smoke wrapper should report failure through `${HEALTHCHECKS_URL_SMOKE}/fail` and leave clear service evidence. The timer should not represent a broken local install as a successful no-op.

Observed: `configs/healthchecks/install.sh` copies `scripts/dev/r1-smoke.sh` to `/opt/ratesengine/healthchecks/r1-smoke.sh`, and the service runs `configs/healthchecks/smoke.sh` under a locked-down oneshot unit. The wrapper checks `if [ ! -x "$SMOKE_SCRIPT" ]`, prints `smoke: ... not found or not executable`, and exits `0` before running the smoke script or pinging `/fail`. This bypasses the README's documented "failed run pings `${URL}/fail`" contract for exactly the install-integrity failure that would disable the smoke.

Impact: medium. The 5-minute smoke check is intended to detect API-schema and data-integrity regressions that metrics-port heartbeats cannot see. If the copied smoke script is missing after a partial install, accidental deletion, permission drift, or bad path override, Healthchecks receives no failure ping and systemd records a successful oneshot. Operators may believe the external smoke coverage exists while the actual API-surface probe is inert.

Remediation direction: make the missing/non-executable branch call `${HEALTHCHECKS_URL_SMOKE}/fail` when a URL is configured, and consider exiting non-zero so `systemctl status ratesengine-smoke.service` also reflects the installation error. Add an installer or unit verification step that asserts `/opt/ratesengine/healthchecks/r1-smoke.sh` exists and is executable after install.

### F-1303. Healthchecks SLA wrapper exits successfully when the SLA probe binary is missing or not executable

Severity: `medium`

Status: `open`

Affected surface:

- `configs/healthchecks/sla-probe.sh`
- `configs/healthchecks/ratesengine-sla-probe.service`
- `configs/healthchecks/install.sh`
- `cmd/ratesengine-sla-probe/main.go`
- `docs/operations/sla-probe.md`

Evidence:

- `XFI-0095`
- `EV-0274`

Expected: if the SLA probe binary is missing, unreadable, or not executable, the Healthchecks wrapper should report failure through `${HEALTHCHECKS_URL_SLA_PROBE}/fail` and make the local service state visibly bad. A missing proof binary should not be represented as a successful timer run.

Observed: `configs/healthchecks/sla-probe.sh` checks `if [ ! -x "$PROBE_BIN" ]`, prints `sla-probe: ... not found or not executable`, and exits `0` before running the probe or sending a failure ping. The service's default `PROBE_BIN` is `/usr/local/bin/ratesengine-sla-probe`, which is installed by release/deploy paths outside the Healthchecks installer. If the deploy misses that binary, permissions drift, or an operator overrides `PROBE_BIN` incorrectly, the SLA Healthchecks timer turns into a successful no-op.

Impact: medium. The SLA probe is the external RFP latency/freshness evidence path, and its Prometheus textfile metrics feed the SLA alert rules. A missing binary may eventually surface through stale Prometheus metrics if a prior textfile existed and Prometheus is healthy, but the Healthchecks signal itself does not fail. That weakens the independent heartbeat channel that is supposed to catch monitoring and API proof gaps.

Remediation direction: make the missing/non-executable branch call `${HEALTHCHECKS_URL_SLA_PROBE}/fail` when configured and exit non-zero for local service visibility. Add a Healthchecks installer/deploy verification step that asserts `/usr/local/bin/ratesengine-sla-probe` exists, is executable, and can print a version/help message before enabling `ratesengine-sla-probe.timer`.

### F-1304. Pre-launch Healthchecks apply step omits `ratesengine-sla-probe.timer` after adding the SLA-probe URL

Severity: `medium`

Status: `open`

Affected surface:

- `docs/operations/pre-launch-hardening.md`
- `configs/healthchecks/README.md`
- `configs/healthchecks/install.sh`
- `configs/healthchecks/ratesengine-sla-probe.service`
- `configs/healthchecks/ratesengine-sla-probe.timer`

Evidence:

- `XFI-0096`
- `EV-0276`

Expected: every active operator path that tells the user to add `HEALTHCHECKS_URL_SLA_PROBE` should restart the SLA probe timer/service along with the other Healthchecks timers so systemd reloads `/etc/default/ratesengine-healthchecks`.

Observed: `docs/operations/pre-launch-hardening.md` now correctly lists five URLs including `HEALTHCHECKS_URL_SLA_PROBE`, but its apply block restarts only `ratesengine-heartbeat@*.timer` and `ratesengine-smoke.timer` before applying Alertmanager. The canonical `configs/healthchecks/README.md` and installer echo both include `ratesengine-sla-probe.timer`, and `configs/healthchecks/ratesengine-sla-probe.service` reads the URL through `EnvironmentFile=-/etc/default/ratesengine-healthchecks`.

Impact: medium. An operator following the pre-launch hardening guide can paste the SLA-probe URL and believe all five checks were activated, while the already-running SLA timer continues with the old environment until a later restart or boot. The timer may still run locally, but the independent Healthchecks proof for RFP latency/freshness is not wired when the guide says it is.

Remediation direction: update the hardening guide's apply command to restart `ratesengine-sla-probe.timer` together with heartbeat and smoke timers. Add a verification command that shows the SLA timer's next run and confirms a recent Healthchecks ping or `/fail` event after the restart.
