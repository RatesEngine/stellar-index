# Findings Register

Cold findings only. No prior finding is imported into this register.

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
| F-1201 | critical | R1 exposes internal storage, observability, and admin services publicly while both nftables and UFW are inactive | R1 host firewall; Ansible archival-node firewall; MinIO/Prometheus/Loki/Promtail/node_exporter/Galexie | XFI-0001; R1-0005; R1-0006; R1-0008; EV-0019 | open | ops/security | External TCP connections succeeded to internal-service ports that repo config says should be default-deny or internal-only. |
| F-1202 | high | Source API contract and deployed R1 API disagreed for removed `/v1/coins` and `/v1/currencies` surfaces | API route table; R1 deployed binary; generated API artifacts | XFI-0002; EV-0012; EV-0020; EV-0066; R1-0001 | fixed | api/release | Current R1 now returns 404 for all removed legacy routes, matching source. Keep the historical evidence because it existed earlier in the same audit window; the live mismatch itself is no longer open. |
| F-1203 | high | Generated explorer API types remain stale and local docs verification did not catch it | `web/explorer/src/api/types.ts`; generation/docs CI | XFI-0002; EV-0007; EV-0011; EV-0013; EV-0067 | open | api/web/ci | On current `HEAD`, docs YAML regenerates cleanly, but `pnpm generate:api` still produces a 362-line diff in explorer TS types, including dashboard webhook routes and updated legacy-route wording. |
| F-1204 | medium | Public API audit tooling and machine-facing docs still advertise removed `/v1/coins` and `/v1/currencies` routes | `scripts/dev/audit-public-api.sh`; `web/explorer/public/llms.txt` | XFI-0002; EV-0065; EV-0066 | open | web/api/docs | Live explorer/status and smoke consumers were migrated, but the public audit script still fails 5 checks against current R1 and `llms.txt` still advertises `GET /v1/coins`. |
| F-1205 | high | R1 is missing SLA, archive-integrity, and supply evidence timers that repo monitoring/runbooks expect | R1 systemd; `deploy/systemd/*`; monitoring rules; runbooks | XFI-0003; R1-0002; R1-0003; R1-0004 | open | ops | Only smoke and heartbeat timers from Rates Engine are installed; evidence-producing timers are absent. |
| F-1206 | high | Public launch readiness gate fails despite canonical local verify passing | `scripts/ci/verify-launch-ready`; `Makefile`; launch readiness docs | XFI-0004; EV-0009; EV-0013 | open | release/ops | Cross-region, security-review, failover-chaos, and finalisation blockers remain red. |
| F-1207 | critical | All three public web apps still pin vulnerable `next@15.0.4`, and hosted dependency-update coverage remains incomplete | `web/*/package.json`; `.github/workflows/ci.yml`; `.github/dependabot.yml`; hosted GitHub dependency alerts | XFI-0005; EV-0014; EV-0051; EV-0099 | open | web/security | CI now runs `pnpm audit --audit-level high` in each web job, but the vulnerable Next pins remain, Dependabot still omits npm/pnpm ecosystems, and hosted GitHub vulnerability/Dependabot alerts are still disabled. |
| F-1208 | high | Multiple enabled ingestion sources are stopped or throttled on R1 while API health remains green | R1 indexer/Prometheus/API readiness | XFI-0006; R1-0001; R1-0009; R1-0010 | open | ingestion/ops | Prometheus shows firing source-stopped alerts for ECB/Soroswap/Band/Phoenix and pending alerts for Comet/Blend/Redstone; Coingecko 429s repeat in logs. |
| F-1209 | medium | R1 host capacity is already under memory/swap pressure and MinIO is 78% full | R1 host capacity; infra alerts; storage runbooks | XFI-0006; R1-0007; R1-0010 | open | ops | Memory alert is firing at about 95.41%, swap is full, and MinIO has 4.9T of 6.4T used. |
| F-1210 | medium | API `/healthz` and `/readyz` scope is too narrow for launch/SLA truth | API health endpoints; status semantics; monitoring | XFI-0006; R1-0009; R1-0010 | open | api/ops | Health/ready only report process/postgres/redis ok while material ingest, latency, memory, and timer evidence failures are active. |
| F-1211 | medium | Status-page incident docs and comms templates point to removed Upptime/cstate workflows instead of the shipped Cloudflare Pages app | `web/status`; `deploy/status-page`; operations runbooks; comms templates | XFI-0007; EV-0021 | open | ops/comms/web | During a SEV, the binding runbook tells operators to edit absent `deploy/status-page/cstate/**` files or use Upptime issues, while the repo ships `web/status` as a custom Next.js static export. |
| F-1212 | high | Free dashboard accounts can self-mint API keys with paid-tier rate limits up to 100,000 requests/minute | Dashboard key management; platform API keys; auth validator; rate-limit middleware | XFI-0008; EV-0023; EV-0089 | fixed | dashboard/billing/api | Current `HEAD` now clamps dashboard-minted key budgets by account tier before insert and tests the tier ladder, so the privilege-escalation path no longer reproduces. |
| F-1213 | high | Stablecoin fiat proxy undercounts Stellar USD volume by 10x in the min-volume manipulation gate | Aggregator stablecoin proxy; Stellar DEX quote decimals; `aggregate.min_usd_volume`; R1 aggregator config | XFI-0009; EV-0024; R1-0011 | open | aggregate/market-data | Classic/SAC USDC quotes are 7-decimal, but `windowUSDVolume` always divides quote amounts by 1e8 for fiat-USD windows; R1 currently avoids false drops only by setting `min_usd_volume=0`, disabling the threshold. |
| F-1214 | critical | `main` is unprotected, so required CI, CODEOWNER review, and signed commits are not enforced | GitHub branch protection/rulesets; `CONTRIBUTING.md`; `CODEOWNERS`; release process | XFI-0010; EV-0025; EV-0026 | open | repo-admin/security | GitHub reports `main.protected=false`; branch protection/rulesets are unavailable on the current private repo tier, contradicting local policy docs and removing the merge gate for production code. |
| F-1215 | high | Production deployment environments have no required reviewers despite holding deploy secrets | GitHub environments; `.github/workflows/deploy.yml`; Cloudflare Pages deploy workflows; repo Actions secrets | XFI-0010; EV-0025; EV-0026 | open | repo-admin/ops | `r1`, docs, explorer, status, and GitHub Pages environments have empty protection rules and admin bypass enabled; manual deployment jobs can access production secrets without environment approval. |
| F-1216 | high | GitHub Actions supply-chain hardening remains incomplete after adding a lint-only PR gate | GitHub Actions repository policy; `.github/workflows/*.yml`; CI pinning lint | XFI-0010; EV-0025; EV-0026; EV-0104 | open | repo-admin/security | The new lint script blocks newly added mutable third-party tags in PR diffs, but hosted Actions policy is still permissive and the current workflows still contain 12 tag-pinned third-party actions. |
| F-1217 | high | SEP-10 replay protection is optional and can run guard-free when Redis is absent | SEP-10 validator; API startup wiring; auth token endpoint; bearer auth | XFI-0011; EV-0027; EV-0053; EV-0096; R1-0012 | fixed | api/security | Current workspace now fails API startup when `auth_mode=sep10` is selected without Redis, so the guard-free deployment path no longer reproduces. |
| F-1218 | high | Public signup can mint immediately usable 1000/min API keys from unverified emails, and Redis-less deployments still skip duplicate protection entirely | `/v1/signup`; signup tracker; API key store; signup UI/OpenAPI | XFI-0012; EV-0028; EV-0099 | open | api/security/billing | The same-email race is now closed when Redis tracking is wired, but signup still returns usable keys without email ownership proof and tracker-nil deployments still mint duplicates by design. |
| F-1219 | high | Stripe paid-upgrade webhook still bypasses platform subscription and dashboard-key sources of truth | Stripe webhook; Redis API keys; Postgres platform billing/API keys | XFI-0013; EV-0030; EV-0053; EV-0107 | open | billing/platform/api | The current workspace adds a dormant platform bridge, but production wiring never sets it, tests do not exercise it, and Postgres dashboard-key limits are still not upgraded. |
| F-1220 | high | Tagged deploy migration handling is still not closure-grade after the new staging path | Release/deploy workflow; Ansible binary deploy; migrations; R1 schema state | XFI-0014; EV-0031; EV-0103; R1-0013 | open | release/ops/db | The shared workspace now stages tag-matched migrations and adds a pre-swap migration task, but the deploy job installs only `ansible-core` while the new task uses `ansible.posix.synchronize`, which is a separate collection. The new path is not yet proven runnable end to end. |
| F-1221 | medium | Release/deploy docs still claim GHCR container image publishing that the current release workflow explicitly removed | Release workflow; release/deploy docs; Docker image expectations | XFI-0014; EV-0032 | open | docs/release | Operators and self-hosters are told to expect GHCR artifacts that the workflow intentionally no longer produces. |
| F-1222 | medium | Rollback docs point operators to nonexistent `/opt/ratesengine/release-<tag>` directories instead of actual binary backups | Release process runbook; Ansible deploy backup layout; R1 sidecars | XFI-0014; EV-0032; R1-0013 | open | ops/release | Incident fallback rollback can fail because the documented artifact path is not produced by the current deploy task. |
| F-1223 | high | R1 runs a stale Caddyfile that exposes `/metrics` publicly and collapses Cloudflare client IPs to edge IPs | Caddy reverse proxy; API trusted proxy config; public observability boundary | XFI-0015; EV-0033; R1-0014 | open | ops/security | Source Caddy blocks `/metrics` and forwards `{client_ip}` after Cloudflare CIDR validation; R1 forwards `{remote_host}` and public `/metrics` returns HTTP 200 with internal Prometheus metrics. |
| F-1224 | medium | Dashboard magic-link and session audit IP fields record proxy/loopback IPs instead of real client IPs | Dashboard auth handlers; session middleware; platform token/user stores; Caddy/API proxying | XFI-0016; EV-0034; R1-0014 | open | dashboard/security | Login/security audit fields intended for IP/new-country signals parse `r.RemoteAddr` directly instead of the middleware-resolved remote IP. |
| F-1225 | high | `/v1/history/since-inception` returns empty XLM/USD history while chart and direct USDC history have data | Historical price APIs; stablecoin USD fallback; Timescale CAGG readers | XFI-0017; EV-0035; R1-0015 | open | api/market-data | Since-inception reads only literal `native/fiat:USD`; it lacks the configured USD-pegged stablecoin fallback used by chart/price/VWAP/TWAP/OHLC surfaces. |
| F-1226 | high | Dashboard API-key allowlists, permissions, monthly quotas, and usage fields are accepted but not enforced consistently at runtime | Platform API keys; dashboard key UI/API; auth validator; rate/quota enforcement | XFI-0018; EV-0036; EV-0100 | open | platform/api/security | The current workspace starts wiring IP/referer/permission enforcement, but cache hits still shed the new policy fields and monthly quota plus `TouchUsage` remain unimplemented. |
| F-1227 | medium | The `ratesengine-migrate` container cannot apply bundled migrations out of the box | Docker migrate image; migration binary; self-hosting docs | XFI-0019; EV-0037 | open | docker/db | Runtime image copies only the binary while the binary defaults to a missing `migrations` directory. |
| F-1228 | high | SSE streams are cut off after 30 seconds by the API server write timeout | API HTTP server; SSE stream endpoints; R1 live API | XFI-0020; EV-0038; R1-0016 | open | api/streaming/ops | R1 tip stream closes at elapsed 30s despite 5s events and 15s heartbeats. |
| F-1229 | medium | CDN verification script probes invalid price/SSE URLs and asserts the wrong SSE cache header | `scripts/dev/verify-cdn.sh`; price/tip API; SSE headers | XFI-0021; EV-0039 | open | ops/api | Script uses `base=` where handlers require `asset=` and expects `no-store` while SSE sets `no-cache`. |
| F-1230 | high | R1 `since-inception` history for core XLM/USDC starts on 2026-05-03, not one year or inception | Historical API; backfill; R1 data depth | XFI-0022; EV-0040; R1-0017 | open | data/backfill/api | Direct XLM/Circle-USDC daily history has only nine buckets. |
| F-1231 | high | Canonical CI is PR-only while `main` is unprotected, so direct pushes can bypass full verification | GitHub CI triggers; branch protection; release governance | XFI-0023; EV-0041; EV-0025; EV-0099 | fixed | repo-admin/ci | Current `ci.yml` now runs on pushes to `main`, closing the direct-main verification bypass even though branch protection itself remains open under `F-1214`. |
| F-1232 | high | Circle USDC has `price_usd` on asset detail but 404s or disappears from `/v1/price` and batch price APIs | Price API; batch API; asset detail price enrichment | XFI-0024; EV-0042; R1-0018 | open | api/market-data | Asset detail returns USDC USD price, but single price 404s and batch returns an empty array for the same asset. |
| F-1233 | high | SDEX historical backfill silently drops legacy V0 claim atoms while claiming genesis coverage | SDEX decoder; dispatcher metrics; historical backfill | XFI-0025; EV-0044; EV-0105 | fixed | ingest/backfill/sdex | Current committed code decodes V0 claim atoms into canonical trades by deriving the seller G-strkey, and targeted SDEX tests pass. |
| F-1234 | medium | Oracle decoders silently skip unknown feeds inside mixed batches, hiding upstream coverage drift | Reflector/Redstone/Band decoders; canonical allow-lists; decoder metrics | XFI-0026; EV-0045 | open | oracle/coverage/observability | New oracle-supported assets can be omitted from stored oracle rows without decode-error metrics as long as the same event contains at least one known asset. |
| F-1235 | medium | External CEX stream parser errors are skipped without the decode-error metrics promised by runbooks | Binance/Kraken/Bitstamp/Coinbase streamers; external metrics; decode-error runbook | XFI-0027; EV-0046; EV-0098 | fixed | external/observability | Current `HEAD` increments `SourceDecodeErrorsTotal` in all four streamer parse-error branches, closing the observability gap. |
| F-1236 | high | Supply snapshots can be stamped at a fresh ledger while using stale component observations | Supply refreshers; supply observer storage; asset supply API/market-cap fields | XFI-0028; EV-0047; EV-0106 | open | supply/data-quality | Snapshot ledger is still max-cursor on committed `HEAD`; the current uncommitted min-supply-cursor remediation attempt does not build because it references nonexistent `timescale.Cursor.Name`. |
| F-1237 | medium | CoinMarketCap ID disambiguation remains incomplete across runtime and verification paths | Verified currency catalogue; CMC poller; external aggregator observations; ops verification source wiring | XFI-0029; EV-0048; EV-0102 | open | external/identity | The indexer now passes numeric CMC IDs into the poller, but the ops verification path still omits them and the poller still lacks a committed ID-mode response fixture proving numeric-ID requests map back to the intended asset. |
| F-1238 | medium | Redis-less API deployments fail startup because closed-bucket stream subscriber is gated on Hub, not Redis | API startup; Redis optionality; closed-bucket SSE stream | XFI-0030; EV-0054; EV-0096 | fixed | api/streaming/ops | Current workspace now gates the Redis pub/sub subscriber on `rdb != nil && hub != nil`, so Redis-less API startup no longer aborts on subscriber construction. |
| F-1239 | medium | WASM history and extraction ops tools panic at completion when progress output is disabled | `ratesengine-ops` WASM audit/extraction commands; Soroban coverage evidence | XFI-0031; EV-0055 | open | ops/data-quality | `-progress-every 0` suppresses in-loop progress but completion still computes modulo by zero after the ledger walk. |
| F-1240 | medium | Docker images build with a different Go toolchain than CI/release while docs claim binary equivalence | Dockerfiles; Go module pin; CI/release workflows; self-hosted image builds | XFI-0032; EV-0057 | open | docker/release | All Dockerfiles use `golang:1.26-alpine`; CI/release use `go.mod` `1.25.10`, and Docker docs still claim `golang:1.25-alpine` and release-equivalent binaries. |
| F-1241 | medium | The operator migration index stops at `0015` even though the repository ships dense schema history through `0029` | `migrations/README.md`; migration review/deploy/runbook workflows | XFI-0033; EV-0058; EV-0059 | open | db/docs/ops | The README claims to be the current migration inventory and says to update it on every new migration, but it omits fourteen live migration families that materially change schema and operational expectations. |
| F-1242 | medium | Contribution-history `volume_usd` remediation is still inconsistent with the filtered contribution set | Aggregator contribution sink; contribution schema/storage; future source-breakdown API/UI | XFI-0034; EV-0060; EV-0103; EV-0104; EV-0105 | fixed | aggregate/storage/product | Current committed code carries per-trade USD attribution by stable trade ID and persists only post-filter survivor dollars per source, so the previously-recorded attribution mismatch no longer reproduces. |
| F-1243 | high | Classic-asset registry freshness and observation counts freeze after the first same-process trade for an asset | Trade insert registry hook; `classic_assets`; issuer/asset catalogue ranking and detail metadata | XFI-0035; EV-0062 | open | storage/assets/data-quality | The dedupe cache exits before the upsert that should update first/last seen ledgers and increment observations, so a long-running ingest/backfill process leaves registry metadata stale until restart. |
| F-1244 | high | Dashboard webhook signing secrets are persisted as live HMAC keys while docs and type names claim hash-only / never-persisted semantics | Dashboard webhook create path; Postgres webhook store; outbound worker signing | XFI-0036; EV-0068 | open | security/platform/webhooks | A database read of `customer_webhooks.secret_hash` yields the actual signing key bytes the worker uses, contrary to the published secret-handling contract. |
| F-1245 | high | Customer webhook URLs create an outbound SSRF primitive because validation enforces only `https://` and the worker follows default redirects | Dashboard webhook URL validation; outbound delivery worker; API process egress boundary | XFI-0037; EV-0069; EV-0096 | fixed | security/platform/webhooks | Current workspace now validates internal/private destinations at registration, re-resolves before delivery, and disables redirect following in the worker client. |
| F-1246 | medium | API design docs still say webhook callbacks are not in v1 even though dashboard webhook CRUD, worker, and runbooks have shipped | API design reference; webhook OpenAPI/routes/runbooks | XFI-0038; EV-0072; EV-0096 | fixed | docs/api/product | `docs/reference/api-design.md` now states webhook callbacks shipped and explains how they relate to SSE. |
| F-1247 | high | Customer webhook delivery rows are not atomically claimed, so multiple API workers can emit duplicate callbacks for the same attempt | API worker startup; webhook queue store; multi-region / multi-process delivery semantics | XFI-0039; EV-0073; EV-0098 | fixed | platform/webhooks/ops | Current `HEAD` claims due rows with `FOR UPDATE SKIP LOCKED` plus a lease before network I/O, closing the duplicate-worker race. |
| F-1248 | medium | The documented ten-webhook-per-account limit is enforced with a raceable pre-check, so concurrent creates can exceed the cap | Dashboard webhook quota check; Postgres insert path; schema invariants | XFI-0040; EV-0074; EV-0098 | open | platform/webhooks | The new store-level CTE narrows the old race but still performs an unlocked snapshot count, so concurrent below-cap creates can still both insert. |
| F-1249 | high | Customer webhook callbacks are exposed and operated as a shipped feature, but declared event coverage is still only partially wired | Customer webhook event model; queue writer; dashboard/API docs; operational runbooks | XFI-0041; EV-0076; EV-0105; EV-0106 | open | platform/webhooks/product | Current committed code now produces `anomaly.freeze` and `divergence.firing`, but `incident.sev1` and `incident.resolved` still have no production enqueue path, and closure-grade per-event enqueue coverage/observability is still missing. |
| F-1250 | medium | Freeze-event open-row dedupe is raceable, so concurrent same-pair freezes can create multiple still-firing durable rows | Freeze writer; Timescale freeze-event mirror; anomalies timeline/recovery semantics | XFI-0042; EV-0079 | open | aggregate/storage/anomaly | The SQL comment claims transactional dedupe, but the code uses an unlocked `WHERE NOT EXISTS` insert and the PK includes `frozen_at`, so concurrent callers can both insert distinct open rows. |
| F-1251 | high | FX-based `usd_volume` remediation is still incomplete: historical freshness is fixed, but the integration contract and zero-value freshness semantics remain inconsistent | Indexer USD-volume Phase 2; VWAP FX resolver; historical/backfill enrichment; integration coverage | XFI-0043; EV-0080; EV-0102 | open | storage/indexer/data-quality | The resolver now evaluates staleness at trade time and returns the historical rate, but the targeted integration still fails because runtime returns fixed-scale NUMERIC text while the test/comment expect trimmed text; `Freshness: 0` is still documented as disabled while the constructor treats it as default-one-hour. |
| F-1252 | medium | Multi-region cutover instructions invoke a nonexistent `make verify-cross-region` launch check | Cutover runbook; verification script; Makefile command surface | XFI-0044; EV-0082 | open | docs/ops/release | The pre-flight checklist names a make target that does not exist, so an operator following the launch runbook gets a Make failure exactly where a gating consistency check is expected. |
| F-1253 | high | Enabling Redis ACL lockdown disables the default user, but the rendered application config never sets `redis_username`, so binaries keep authenticating on the rejected legacy path | Redis Sentinel ACL template; application config template; Redis client builder | XFI-0045; EV-0083; EV-0098 | fixed | ops/security/config | Current Ansible rendering emits the named Redis ACL username when lockdown is enabled, matching the server-side auth contract. |
| F-1254 | high | Redis ACL lockdown allows stale or wrong key families, so hardened deployments still deny active runtime namespaces after the username handoff is fixed | Redis Sentinel ACL template; Redis namespace builders; API/auth/cache runtime wiring | XFI-0046; EV-0084; EV-0098 | fixed | ops/security/config | Current ACL rendering now permits the live `rl:*`, `sub:*`, signup, replay, usage, and catalogue namespaces that were previously missing or misnamed. |
| F-1255 | medium | Concurrent first-login callbacks for the same new email can still create orphan accounts because provisioning is not atomic per email | Dashboard magic-link callback; account/user stores; platform schema uniqueness | XFI-0047; EV-0086; EV-0087; EV-0102 | open | platform/auth/data-quality | The losing callback now reloads the winner user instead of surfacing the original conflict response, but token consumption is still per token rather than per email, so two valid links can still persist a speculative loser account row with no owner. |
| F-1256 | medium | Dashboard key-rate UI and OpenAPI still promise generic 1000/100000 limits even though the backend now silently clamps by account tier | Dashboard key form; create-key API schema; tier-cap implementation | XFI-0048; EV-0090 | open | dashboard/docs/product | Free users are told the default is 1000/min and every user can submit 100000/min, but current backend persists Free keys at 60/min and other tiers at smaller caps unless the tier allows more. |
| F-1257 | medium | The attempted 25-active-key/account quota fix still uses a raceable unlocked count CTE | Dashboard key quota check; Postgres insert path; platform schema | XFI-0049; EV-0092; EV-0103 | open | platform/keys | The workspace moved quota handling into `APIKeyStore.Create`, but the new `WITH active_count ... INSERT ... WHERE n < max` has the same MVCC snapshot race already preserved for webhooks under `F-1248`. |
| F-1258 | high | Redis-less API deployments can still panic through the usage-reader path after the middleware-side nil fix | API startup wiring; usage middleware; usage counter; account usage reader | XFI-0050; EV-0094; EV-0103 | open | api/ops/runtime | The workspace now omits the middleware counter when Redis is absent, but still passes `UsageReader: usageReaderAdapter{c:nil}` into the server; `/v1/account/usage` then dereferences the nil inner counter on `Read`. |
| F-1259 | medium | Usage docs are still internally inconsistent after the source OpenAPI rewrite | Account usage handler; OpenAPI/reference docs; product architecture docs | XFI-0051; EV-0095; EV-0103 | open | docs/api/product | The source OpenAPI text now describes live Redis-backed usage, but generated reference YAML, Postman, API-design docs, architecture inventory, and the handler comment remain stale; the new source text also incorrectly says Redis absence is reflected on `/v1/healthz` checks. |
| F-1260 | high | `aggregate.min_usd_volume` still evaluates discarded pre-filter volume, so thin survivor windows can publish above a manipulation floor they do not actually meet | Aggregator stablecoin/USD-volume path; class/outlier filtering; VWAP publish gate | XFI-0052; EV-0105 | open | aggregate/market-data | `refreshPairWindow` computes `usdVolume` before class/outlier filtering, then applies the threshold after filtering with a helper that ignores the survivor slice and compares the stale total. |

## Finding Template

```md
### F-1201. Title

Severity: `high`

Status: `open`

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

### F-1201. R1 exposes internal storage, observability, and admin services publicly

Severity: `critical`

Status: `open`

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

Expected: R1 should run the repo-managed nftables default-deny policy, UFW should be disabled/stopped, and internal services should be loopback-only or restricted to `internal_cidrs`.

Observed: nftables is disabled/inactive with an empty ruleset; UFW is inactive; external TCP connects succeeded to MinIO, MinIO console, Prometheus, Loki, Promtail, node_exporter, Galexie admin, and captive-core port 11726.

Impact: Public exposure of storage, metrics, logs, admin surfaces, and infrastructure fingerprints creates immediate attack surface and data disclosure risk.

Remediation direction: Apply/verify the nftables role, bind internal daemons to loopback/private interfaces where possible, close public ports at host/provider firewall, and add a pre-launch external port probe that fails on unexpected listeners.

### F-1207. Web apps pin vulnerable Next.js and lack pnpm advisory gates

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

Expected: Public Next.js apps should be on patched versions, with automated pnpm updates and CI advisory gates.

Observed during the initial pass: all three apps pinned `next@15.0.4`; `pnpm audit --audit-level moderate` reported 27 advisories per app including 2 critical and 8 high. CI typechecked/linted/built web apps but did not run `pnpm audit`; Dependabot omitted npm/pnpm ecosystems; hosted GitHub vulnerability and Dependabot alerts were disabled.

Impact: Public explorer/status/dashboard surfaces inherit known RCE/auth-bypass/DoS/cache/XSS classes until upgraded. Dashboard risk is higher because it is account-facing.

Current-head reconciliation: Wave 7 added `pnpm audit --audit-level high` to explorer, dashboard, and status CI jobs, so the missing CI advisory gate is no longer part of the open defect. The package pins remain vulnerable, Dependabot still does not cover npm/pnpm, and hosted GitHub vulnerability/Dependabot alerts remain disabled.

Remediation direction: upgrade `next`/`eslint-config-next` across all web apps to a patched compatible release, regenerate lockfiles, add Dependabot npm entries for each web app, and enable GitHub vulnerability/Dependabot alerts.

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

Expected: status-page runbooks and customer-comms templates should describe the shipped incident publication workflow for `status.ratesengine.net`.

Observed: the shipped implementation is a custom `web/status` static-export app on Cloudflare Pages, but the operations runbook requires editing removed `deploy/status-page/cstate/**` paths and other docs/templates point to an Upptime `RatesEngine/ratesengine-status` issue flow.

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

### F-1213. Stablecoin fiat proxy undercounts Stellar USD volume by 10x in the min-volume manipulation gate

Severity: `high`

Status: `open`

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

Expected: when `XLM/fiat:USD` expands to on-chain classic/SAC USD-pegged pairs, the min-volume manipulation gate should compute USD volume using the same decimal convention as the quote asset or the stored `trades.usd_volume` value.

Observed: `ExpandTargetPairWithClassicPegs` appends classic USD-pegged assets from `[trades].usd_pegged_classic_assets`; `fetchForTarget` rewrites those trades to `fiat:USD`; then `windowUSDVolume` divides every `QuoteAmount` by `100_000_000`. The storage path separately documents and implements classic/SAC USD pegs as 7-decimal values. R1 has proxy expansion enabled and Circle USDC configured, but sets `min_usd_volume = 0`.

Impact: if the default `aggregate.min_usd_volume = 10000` is enabled with Stellar USD-pegged proxy expansion, a real $10,000 classic-USDC window is treated as $1,000 and dropped. R1 currently avoids the false negative by disabling the threshold entirely, removing a launch-readiness manipulation defense.

Reproduction or reasoning path: a Stellar XLM/USDC trade with `quote_amount=100_000_000_000` represents $10,000 at 7 decimals. `windowUSDVolume` divides that by `1e8` and returns $1,000. The same quote asset is treated as 7-decimal by `USDVolumeQuoteSpec.QuoteUSDPegInfo` and `tradeUSDVolume`.

Remediation direction: make the min-volume gate operate on normalized USD volume, preferably from `trades.usd_volume` or an aggregation query that already carries quote-decimal metadata. If raw trades remain the input, annotate each proxy-expanded trade with the quote scale before rewriting to `fiat:USD`, and add tests covering classic/SAC USDC plus external `crypto:USDC` in the same fiat-USD window.

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

### F-1218. Public signup can mint immediately usable 1000/min API keys from unverified emails and non-atomic duplicate checks

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

Expected: self-service key minting should prove email ownership or use a stronger anti-abuse gate before issuing a usable 1000/min key; duplicate checks should be atomic if they are the idempotency guarantee.

Observed during the initial pass: `/v1/signup` minted a plaintext Starter API key immediately from a parsed email string. The duplicate tracker was optional and tests pinned that duplicate signup succeeded when it was nil. When Redis was wired, the flow still performed lookup, key create, then SETNX mark, so concurrent same-email requests could mint multiple keys.

Current-head reconciliation: the Redis-backed path now calls `ReserveEmail` before minting, and `RedisSignupTracker.ReserveEmail` uses `SETNX` with a pending placeholder plus a five-minute TTL, so the same-email concurrent race is closed when tracking exists. The route still returns a usable plaintext Starter key without email ownership proof, and the documented/tested `signups == nil` path still disables duplicate protection entirely.

Impact: attackers can still cheaply mint large numbers of free API keys with rotating email strings or Redis-less deployments, bypassing the anonymous 60/min floor and creating capacity/billing abuse.

Remediation direction: route signup through the magic-link/dashboard account flow or require email verification before exposing plaintext keys; make duplicate protection mandatory or remove the Redis-less mint path; add per-email/domain/device abuse controls and alerting.

### F-1219. Stripe paid-upgrade webhook still bypasses platform subscription and dashboard-key sources of truth

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

Expected: Stripe paid-upgrade events should update the same account, subscription, and API-key records that dashboard users and runtime auth consume, with durable idempotency and audit.

Observed during the initial pass: current source wired Postgres event dedupe and audit rows when Postgres was available, which reduced the earlier idempotency gap. The side effect still used `auth.RedisAPIKeyStore`: it found keys by `client_reference_id` and updated Redis key rate limits only. The webhook did not call `UpsertSubscription`, did not update Postgres dashboard keys/accounts/subscription state, acknowledged paid events with no keys as 200, and could return 200 after partial or total key-update failure.

Current-workspace reconciliation: an uncommitted remediation slice adds `StripePlatformBridge` and `applyPlatformSideEffects`, capable of resolving an account by Stripe customer ID, upserting a subscription row, and mutating `Account.Tier`. That does not close the finding. Production API wiring in `cmd/ratesengine-api/main.go` never sets `StripeWebhookConfig.Platform`, repo search finds no other bridge user, and the existing Stripe webhook tests do not exercise the new path. Even if wired, the helper still does not raise Postgres dashboard API-key limits alongside Redis-backed legacy keys, so the runtime/dashboard split remains.

Impact: paid customers using dashboard-created keys can pay and still keep old limits or missing subscription state; customer-facing dashboard/billing truth can disagree with legacy Redis key state; failed upgrades can be acknowledged and then require manual reconciliation.

Remediation direction: finish the platform-side wiring rather than only defining the helper. Wire `StripeWebhookConfig.Platform` in production, test it end to end, update all active account keys in the runtime source of truth including dashboard-created Postgres keys, and return retryable status on unambiguous total failure.

### F-1220. Tagged deploys can restart schema-dependent binaries without shipping or applying matching migrations

Severity: `high`

Status: `open`

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

### F-1221. Release/deploy docs still claim GHCR container image publishing that the current release workflow explicitly removed

Severity: `medium`

Status: `fixed`

Affected surface:

- `.github/workflows/release.yml`
- `docs/operations/deploy-workflow.md`
- `docs/operations/release-process.md`
- Docker/self-hosting expectations

Evidence:

- `XFI-0014`
- `EV-0032`

Expected: release documentation should describe the artifacts the current workflow actually publishes.

Observed: the release workflow explicitly says container images are not built or pushed, but both deploy and release process docs still state that release tags build and push GHCR images.

Impact: operators and self-hosters can wait on or automate against nonexistent container artifacts, delaying recovery or deploys and increasing the chance of manual image builds from the wrong commit.

Remediation direction: update release/deploy docs and any release templates to state binary-only publication, or restore the GHCR job and permissions if container artifacts are now required.

### F-1222. Rollback docs point operators to nonexistent `/opt/ratesengine/release-<tag>` directories instead of actual binary backups

Severity: `medium`

Status: `open`

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

### F-1223. R1 runs a stale Caddyfile that exposes `/metrics` publicly and collapses Cloudflare client IPs to edge IPs

Severity: `high`

Status: `open`

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

Expected: R1 should run the reviewed Caddyfile that resolves real client IPs from Cloudflare trusted ranges and blocks `/metrics` on the public API hostname.

Observed: the live Caddyfile lacks the repo's Cloudflare `trusted_proxies`/`client_ip_headers` block, forwards `{remote_host}` as `X-Forwarded-For`, and does not block `/metrics`. External `/metrics` returned HTTP 200 with Go runtime, route-level, cache, stream, and Rates Engine metric names and values.

Impact: anonymous clients can scrape internal operational metrics and route counters; behind Cloudflare, per-IP logging/rate limiting sees Cloudflare edge IPs rather than customers, so unrelated users on the same edge can collide in anonymous buckets.

Remediation direction: deploy the current source Caddyfile or equivalent immediately; verify `/metrics` returns 404 externally, `{client_ip}` reaches the API from Cloudflare, and direct-origin requests cannot spoof client identity.

### F-1224. Dashboard magic-link and session audit IP fields record proxy/loopback IPs instead of real client IPs

Severity: `medium`

Status: `open`

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

### F-1225. `/v1/history/since-inception` returns empty XLM/USD history while chart and direct USDC history have data

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

Expected: historical USD price surfaces should agree on the declared Stellar USD proxy policy, or return an explicit unsupported/fallback-missing signal.

Observed: `handleHistorySinceInception` queries the literal CAGG pair and returns it directly. It does not apply the stablecoin fallback that chart, price, VWAP, TWAP, and OHLC paths use for `X/fiat:USD`. On R1, `native/fiat:USD` since-inception returned no points while the chart endpoint returned XLM/USD daily points and direct `native/USDC-GA5Z...` history returned populated points.

Impact: clients building long-range price charts from the documented since-inception API see no XLM/USD history even though the system has the data under the configured Stellar USDC market. This is a visible product parity failure against CoinGecko/CMC-style historical chart APIs.

Remediation direction: share the chart stablecoin fallback with since-inception history, preserve `flags.triangulated=true` when it fires, and add tests comparing direct peg history with `fiat:USD` history for the same asset/granularity.

### F-1226. Dashboard API-key allowlists, permissions, monthly quotas, and usage fields are accepted but not enforced at runtime

Severity: `high`

Status: `open`

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

Expected: customer-visible key policy fields should be enforced on every authenticated request, or the UI/API should clearly mark them as not active.

Observed during the initial pass: dashboard key creation stored `monthly_quota`, `permissions`, `ip_allowlist`, `referer_allowlist`, and expiry/revocation fields. Runtime auth validated only key hash, revocation, expiry, and account status, then returned a subject containing tier/key/rate-limit. There was no request-aware check for client IP, referer, permissions, monthly quota, or usage increments; `TouchUsage` had no production caller.

Current-workspace reconciliation: the shared workspace now has a `KeyPolicy` middleware, production API wiring for that middleware, and subject propagation of Postgres-backed IP allowlists, referer allowlists, and permission entries. That narrows the finding, but does not close it. `PostgresAPIKeyValidator.cacheStore` still writes the old `APIKeyRecord` shape, and `cacheLookup` rebuilds a `Subject` from that shape without the new allowlist/permission fields, so a Postgres cache miss can enforce policy while subsequent Redis cache hits silently bypass it. The same workspace still does not enforce `monthly_quota`, and `TouchUsage` still has no production caller.

Impact: customers can still create keys whose request-time policy weakens after cache warm-up, and the advertised monthly quota / last-used surfaces remain non-authoritative. This is still a security and trust issue for dashboard users and a billing-control gap for paid plans.

Remediation direction: finish the policy path end-to-end. Preserve IP/referer/permission fields through the cache schema or bypass the cache for policy-bearing keys, enforce monthly quotas, wire debounced `TouchUsage`, and add tests that prove a cache miss and a cache hit enforce the same restrictions.

### F-1227. The `ratesengine-migrate` container cannot apply bundled migrations out of the box

Severity: `medium`

Status: `open`

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

Status: `open`

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

### F-1228. SSE streams are cut off after 30 seconds by the API server write timeout

Severity: `high`

Status: `fixed`

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

Expected: `/v1/price/stream`, `/v1/price/tip/stream`, and `/v1/observations/stream` should support long-lived SSE clients, with heartbeats preventing idle proxy closure and reconnect/resume handling real network breaks.

Observed: the API `http.Server` sets `WriteTimeout: 30 * time.Second`. Go applies that as a response-write timeout reset when a new request's headers are read, not as a heartbeat-aware per-frame deadline. R1 loopback testing confirmed `/v1/price/tip/stream` emits events through 25 seconds and then closes at elapsed 30 seconds.

Impact: real-time streaming is not actually long-lived. Browser/EventSource and curl clients reconnect every 30 seconds, increasing churn and load; customer demos that instruct a 60-second run fail; CoinGecko/CMC parity for streaming or trade-tape style experiences is weaker than the API contract suggests.

Remediation direction: remove the global write timeout for the API server or route streaming endpoints through a server/listener with no absolute `WriteTimeout`; if write deadlines are required, manage them per write with `http.ResponseController` and tests that keep a stream open beyond the timeout horizon.

### F-1229. CDN verification script probes invalid price/SSE URLs and asserts the wrong SSE cache header

Severity: `medium`

Status: `open`

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

Expected: launch history for core Stellar pairs should meet the Freighter minimum of one year, ideally since inception, or clearly mark the deployment's historical coverage as incomplete.

Observed: R1 direct XLM/Circle-USDC `/v1/history/since-inception?granularity=1d` returned only nine daily points, starting `2026-05-03T00:00:00Z` and ending `2026-05-11T00:00:00Z`. The handler returns available closed buckets without a completeness marker or backfill coverage range.

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

Status: `open`

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

Status: `open`

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

### F-1236. Supply snapshots can be stamped at a fresh ledger while using stale component observations

Severity: `high`

Status: `open`

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

Expected: a supply snapshot for ledger `N` should be computed from supply-observer components that are complete through ledger `N`, or it should publish explicit component freshness/lag metadata and avoid presenting stale inputs as current supply.

Observed: the aggregator and CLI choose the maximum `last_ledger` across ingestion cursors as the snapshot ledger. Component readers then use `AtOrBefore` storage queries for trustlines, claimable balances, LP reserves, SAC balances, SEP-41 event totals, and account observations. These reader interfaces return balances/totals but not the ledger of each component row, so the refresher cannot detect a stale component before inserting a snapshot at the max ledger.

Current-worktree reconciliation: an uncommitted remediation attempt tries to switch both the aggregator path and `ratesengine-ops supply snapshot` path from max-of-all cursors to min-of-`supply.*` cursors. That attempt does not compile: both patches test `strings.HasPrefix(c.Name, "supply.")`, but `timescale.Cursor` exposes `Source` and `Sub`, not `Name`. `go test ./cmd/ratesengine-aggregator` and `go test ./cmd/ratesengine-ops` both fail on the undefined field, so the remediation is not closure-grade and committed `HEAD` still preserves the original max-cursor behavior.

Impact: if one supply observer stalls while another source advances, asset supply and derived market-cap fields can look current but include old balances. This is especially risky for Stellar-specific depth claims around classic/SAC/SEP-41 supply and for customer-facing asset detail pages.

Remediation direction: resolve the snapshot ledger as the minimum complete ledger across all required supply observer cursors for the target asset, or return component ledgers from storage readers and reject/mark snapshots when any component exceeds a freshness lag. Expose component freshness in diagnostics and tests.

### F-1237. CoinMarketCap ID disambiguation remains incomplete across runtime and verification paths

Severity: `medium`

Status: `open`

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

Status: `open`

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

Status: `open`

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

Status: `open`

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

### F-1243. Classic-asset registry freshness and observation counts freeze after the first same-process trade for an asset

Severity: `high`

Status: `open`

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

Expected: repeated observed trades for a classic asset should keep `classic_assets.first_seen_*`, `last_seen_*`, and `observation_count` accurate within a single long-running live-ingest or backfill process. Replay order should not matter.

Observed: `InsertTrade` invokes `registerClassicAssetSeen` after each successful stored trade, but `assetRegistryDedupe` returns early once an asset has been touched once in the current process. That happens before the SQL upsert which would apply `LEAST` to first-seen, `GREATEST` to last-seen, and increment `observation_count`. The conflict-update logic therefore does not run for later same-process observations. Existing integration coverage seeds `classic_assets` directly instead of exercising this writer path.

Impact: asset and issuer catalogue metadata can undercount observations by orders of magnitude, preserve the wrong first-seen ledger during out-of-order replay, and freeze last-seen freshness until the indexer restarts. Those fields drive ranking, trust signals, and customer-facing asset/issuer detail views, so this is a live data-quality issue rather than a cosmetic counter drift.

Remediation direction: remove or narrow the process-lifetime dedupe so correctness updates still occur, or replace it with a bounded coalescing/batching strategy that preserves first/last/count semantics. Add integration coverage that inserts multiple trades for the same classic asset in one process, including out-of-order ledger replay, then asserts `first_seen_*`, `last_seen_*`, and `observation_count`.

### F-1244. Dashboard webhook signing secrets are persisted as live HMAC keys while docs and type names claim hash-only / never-persisted semantics

Severity: `high`

Status: `open`

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

Expected: the webhook secret-handling contract should be explicit and true. If only hashes are persisted, the runtime must not need the plaintext-equivalent signing key later. If outbound signing requires retrievable key material, the schema/API/docs should say so and the stored key should receive an appropriate at-rest protection model.

Observed: the create handler generates a plaintext `wsec_*` secret, passes `SecretHash: []byte(secret)`, and the Postgres store inserts those bytes directly into `customer_webhooks.secret_hash`. The delivery worker later uses that field as the actual HMAC key. Simultaneously, the platform type comments, rotate stub, handler comments, and OpenAPI text say the field is a hash or that plaintext is returned once and never persisted.

Impact: operators, reviewers, and customers are given a false security model. A database compromise or over-broad read path exposes signing keys that let an attacker forge outbound webhook signatures for customers, while the code/docs currently imply those secrets are not recoverable from storage.

Remediation direction: choose one honest design. Either store an encrypted/recoverable signing key under a correctly named field with documented at-rest protections and rotation, or change the delivery protocol so the persisted value can truly be non-reversible. Update schema naming/docs/tests in lockstep.

### F-1245. Customer webhook URLs create an outbound SSRF primitive because validation enforces only `https://` and the worker follows default redirects

Severity: `high`

Status: `open`

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

Status: `open`

Affected surface:

- `internal/api/v1/dashboardwebhooks/handlers.go`
- `internal/platform/postgresstore/webhook_store.go`
- `migrations/0027_platform_v1_schema.up.sql`
- `internal/api/v1/dashboardwebhooks/handlers_test.go`

Evidence:

- `XFI-0040`
- `EV-0074`
- `EV-0098`

Expected: the advertised per-account webhook ceiling should remain true under concurrent requests, not just serial UI use.

Observed during the initial pass: `HandleCreate` called `checkQuota`, which loaded current account hooks and compared `len(hooks)` to ten, then later performed a separate store insert. The Postgres store was a plain insert path, migration 0027 had no database-side cap, and the current quota test exercised only sequential creation.

Current-head reconciliation: the store now attempts to tighten the path with `WITH current_count AS (...) INSERT ... WHERE current_count.n < maxPerAccount`, but that still is not a serialization point under PostgreSQL's normal MVCC semantics. Two statements that begin from the same below-cap snapshot can each see the same count and each insert before either transaction's row becomes visible to the other. There is still no row/advisory lock, exclusion/unique constraint, serializable retry, or concurrent persistence test that proves the ten-row ceiling.

Impact: a customer or script issuing parallel create requests can exceed the service's own control-plane limit, growing outbound delivery fan-out, SSRF exposure, and incident-notification noise beyond the bounded posture the code comments promise.

Remediation direction: move quota enforcement into a real serialization boundary. A transactional account-level advisory lock or account-row lock plus count-and-insert, a durable allocation table, or a serializable retry loop is materially stronger than an unlocked count CTE. Add concurrency tests that launch more than ten creates in parallel and prove the persisted count never exceeds ten.

### F-1249. Customer webhook callbacks are exposed and operated as a shipped feature, but declared event coverage is still only partially wired

Severity: `high`

Status: `open`

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

Status: `open`

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

Status: `open`

Affected surface:

- `cmd/ratesengine-indexer/main.go`
- `internal/storage/timescale/usd_fx_resolver.go`
- `internal/storage/timescale/trades.go`
- `test/integration/usd_fx_resolver_test.go`

Evidence:

- `XFI-0043`
- `EV-0080`
- `EV-0102`

Expected: when resolving USD volume for a trade at timestamp `T`, staleness should be evaluated relative to `T` (or another clearly documented trade-time policy), so valid historical/backfilled trades can still inherit a contemporaneous FX/VWAP anchor.

Observed during the initial pass: `VWAPUSDFXResolver.queryDB` correctly found the latest peg VWAP at-or-before the supplied trade timestamp, but `USDPriceAt` rejected it using `r.clock().Sub(observedAt) > freshness`. That compared the row to wall-clock now, not to the trade timestamp. Any trade older than the one-hour default window could therefore lose Phase-2 USD enrichment even when the at-time rate existed. The same file documented `Freshness: 0` as disabling the check, while the constructor interpreted zero as "apply the 1h default"; the integration test relied on the documented disable behavior and failed with `ok=false`.

Current-head reconciliation: `USDPriceAt` now evaluates `at.Sub(observedAt) > freshness`, which fixes the historical/backfill rejection path itself. The exact same targeted integration now reaches a positive lookup and fails later on contract drift instead: the resolver returns `1.08500000000000000000`, while `test/integration/usd_fx_resolver_test.go` and its comment still expect trimmed text `1.085`. The zero-value `Freshness` documentation/constructor contradiction also remains.

Impact: historical replay, backfill, and delayed indexing can systematically under-populate `trades.usd_volume` for non-USD quotes covered by the new FX resolver. Downstream 24h volume, ranking, and transparency surfaces then understate coverage exactly where Phase 2 was intended to improve it.

Reproduction or reasoning path:

- `go test -tags=integration ./test/integration -run TestVWAPUSDFXResolver_QueriesPrices1m -count=1`
- Current result: fails with `USDPriceAt = "1.08500000000000000000", want "1.085"`.

Remediation direction: retain the trade-time freshness fix, then settle the remaining API/test contract. Either normalize the returned VWAP text before exposing it or adjust the integration contract/comment to accept stable fixed-scale NUMERIC text. Separately split "unset" from "explicitly disabled" freshness semantics if zero is meant to disable checks. Keep the old-wall-clock / valid-at-trade integration and add a second case proving genuinely stale-at-trade anchors are rejected.

### F-1252. Multi-region cutover instructions invoke a nonexistent `make verify-cross-region` launch check

Severity: `medium`

Status: `open`

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

Status: `open`

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

Status: `open`

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

Status: `open`

Affected surface:

- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/api/v1/dashboardkeys/handlers_test.go`
- `internal/platform/postgresstore/apikey_store.go`
- `migrations/0027_platform_v1_schema.up.sql`

Evidence:

- `XFI-0049`
- `EV-0092`
- `EV-0103`

Expected: the dashboard's active-key cap should hold under concurrent requests, not only under serial UX flows.

Observed during the initial pass: `HandleCreate` called `checkQuota`, which listed all account keys and counted those with zero `RevokedAt`. If the count was below 25, the handler later performed an independent insert through `APIKeyStore.Create`. The schema provided active-key indexes but no database invariant or transactional compare-and-insert guard for the 25-row ceiling. Current tests only pre-seeded 25 rows and verified a single follow-up create returned 409.

Current-workspace reconciliation: `APIKeyStore.Create` now accepts `maxActiveKeysPerAccount`, returns `ErrAPIKeyQuotaExceeded`, and uses `WITH active_count AS (...) INSERT ... WHERE active_count.n < $16`. That is still not a serialization boundary under PostgreSQL's normal MVCC semantics. Two create statements that begin from the same below-cap snapshot can each see the same `active_count.n`, both satisfy the predicate, and both insert. The handler's new 409 branch only handles the serial over-cap case; it does not make the boundary atomic. There is still no row lock, advisory lock, unique/exclusion invariant, serializable retry, or concurrent persistence test.

Impact: coordinated or accidental concurrent create requests can leave an account with more active dashboard keys than the product promises. That weakens the anti-sprawl ceiling operators rely on for customer-key hygiene and makes later cleanup/reporting less trustworthy.

Remediation direction: enforce the cap atomically at a real serialization point. Use a transaction with an account-scoped lock/check, a serializable retry loop, or an equivalent database invariant that actually excludes concurrent over-cap inserts. Add a concurrent create test that starts below the threshold and proves persisted active keys never exceed 25.

### F-1258. Redis-less API deployments still wire a non-nil usage middleware around a nil Redis client, so authenticated requests can panic instead of degrading cleanly

Severity: `high`

Status: `open`

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

Status: `open`

Affected surface:

- `cmd/ratesengine-api/main.go`
- `internal/api/v1/account.go`
- `internal/api/v1/server.go`
- `openapi/rates-engine.v1.yaml`
- `docs/reference/api/rates-engine.v1.yaml`
- `docs/reference/api-design.md`
- `docs/reference/api/postman-collection.json`
- `docs/architecture/explorer-data-inventory.md`

Evidence:

- `XFI-0051`
- `EV-0095`
- `EV-0103`

Expected: once the usage reader is live in current runtime wiring, customer-facing docs and generated references should describe conditional real data semantics instead of the retired stub contract.

Observed during the initial pass: `ratesengine-api` wired `UsageTracker` and `UsageReader`, and `handleAccountUsage` read a trailing 30-day usage window when the reader was present. Yet its own doc comment still said the endpoint always returned `[]`, the OpenAPI summary said "currently empty," the generated reference YAML/Postman artifacts copied that contract, and the API design / explorer inventory docs still called it a placeholder or stub.

Current-workspace reconciliation: the source OpenAPI file now describes live Redis-backed daily counters and tier-clamp semantics elsewhere, but the generated reference YAML, Postman collection, API design doc, architecture inventory, and `internal/api/v1/account.go` comment remain stale. The new OpenAPI copy also says Redis-less deployments are reflected on `/v1/healthz` under `checks`, while `healthResponse.Checks` is explicitly absent on `/healthz` and populated only on `/readyz`.

Impact: customers and internal reviewers are told a live usage feature is absent, while generated clients and product documentation remain anchored to outdated behavior. That distorts product readiness judgments and makes future audit/review work easier to misread.

Remediation direction: rewrite the usage contract around current conditional semantics everywhere, correct the `/healthz` versus `/readyz` wording, and regenerate all derived API reference artifacts from the corrected source OpenAPI. Keep docs lint green, but add a targeted drift guard if this class of source-vs-generated/reference mismatch keeps escaping.

### F-1260. `aggregate.min_usd_volume` still evaluates discarded pre-filter volume, so thin survivor windows can publish above a manipulation floor they do not actually meet

Severity: `high`

Status: `open`

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
