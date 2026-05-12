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
| F-1202 | high | Source API contract and deployed R1 API disagree for removed `/v1/coins` and `/v1/currencies` surfaces | API route table; R1 deployed binary; generated API artifacts | XFI-0002; EV-0012; EV-0020; R1-0001 | open | api/release | Local source removed legacy HTTP surfaces, but R1 and public smoke/audit still expect them; audit of source is not representative of production behavior until deployment state is reconciled. |
| F-1203 | high | Generated OpenAPI reference and explorer API types were stale and local docs verification did not catch it | `docs/reference/api/rates-engine.v1.yaml`; `web/explorer/src/api/types.ts`; docs/scripts CI | XFI-0002; EV-0007; EV-0010; EV-0011; EV-0013 | open | api/web/ci | `docs-api.sh` and `pnpm generate:api` both produced diffs; `make verify` and `lint-docs` still passed locally. |
| F-1204 | high | Active frontend, status, examples, and smoke scripts still call removed `/v1/coins` and `/v1/currencies` endpoints | `web/explorer/**`; `web/status/**`; `examples/**`; `scripts/dev/*` | XFI-0002; EV-0012; EV-0020 | open | web/api | Current source deployment would break or degrade HomeCurrencies, converters, embed currency pages, status probes, examples, and public API audit/smoke checks. |
| F-1205 | high | R1 is missing SLA, archive-integrity, and supply evidence timers that repo monitoring/runbooks expect | R1 systemd; `deploy/systemd/*`; monitoring rules; runbooks | XFI-0003; R1-0002; R1-0003; R1-0004 | open | ops | Only smoke and heartbeat timers from Rates Engine are installed; evidence-producing timers are absent. |
| F-1206 | high | Public launch readiness gate fails despite canonical local verify passing | `scripts/ci/verify-launch-ready`; `Makefile`; launch readiness docs | XFI-0004; EV-0009; EV-0013 | open | release/ops | Cross-region, security-review, failover-chaos, and finalisation blockers remain red. |
| F-1207 | critical | All three public web apps pin vulnerable `next@15.0.4` and CI/Dependabot do not cover pnpm advisories | `web/*/package.json`; `.github/workflows/ci.yml`; `.github/dependabot.yml`; hosted GitHub dependency alerts | XFI-0005; EV-0014; EV-0051 | open | web/security | `pnpm audit` reports 2 critical and 8 high advisories per app; Dependabot has gomod/actions/docker only, CI has no pnpm audit step, and hosted GitHub vulnerability/Dependabot alerts are disabled. |
| F-1208 | high | Multiple enabled ingestion sources are stopped or throttled on R1 while API health remains green | R1 indexer/Prometheus/API readiness | XFI-0006; R1-0001; R1-0009; R1-0010 | open | ingestion/ops | Prometheus shows firing source-stopped alerts for ECB/Soroswap/Band/Phoenix and pending alerts for Comet/Blend/Redstone; Coingecko 429s repeat in logs. |
| F-1209 | medium | R1 host capacity is already under memory/swap pressure and MinIO is 78% full | R1 host capacity; infra alerts; storage runbooks | XFI-0006; R1-0007; R1-0010 | open | ops | Memory alert is firing at about 95.41%, swap is full, and MinIO has 4.9T of 6.4T used. |
| F-1210 | medium | API `/healthz` and `/readyz` scope is too narrow for launch/SLA truth | API health endpoints; status semantics; monitoring | XFI-0006; R1-0009; R1-0010 | open | api/ops | Health/ready only report process/postgres/redis ok while material ingest, latency, memory, and timer evidence failures are active. |
| F-1211 | medium | Status-page incident docs and comms templates point to removed Upptime/cstate workflows instead of the shipped Cloudflare Pages app | `web/status`; `deploy/status-page`; operations runbooks; comms templates | XFI-0007; EV-0021 | open | ops/comms/web | During a SEV, the binding runbook tells operators to edit absent `deploy/status-page/cstate/**` files or use Upptime issues, while the repo ships `web/status` as a custom Next.js static export. |
| F-1212 | high | Free dashboard accounts can self-mint API keys with paid-tier rate limits up to 100,000 requests/minute | Dashboard key management; platform API keys; auth validator; rate-limit middleware | XFI-0008; EV-0023 | open | dashboard/billing/api | The customer-controlled key create request sets `rate_limit_per_min`, and the runtime honors that value without deriving or capping it from account tier, subscription, or billing status. |
| F-1213 | high | Stablecoin fiat proxy undercounts Stellar USD volume by 10x in the min-volume manipulation gate | Aggregator stablecoin proxy; Stellar DEX quote decimals; `aggregate.min_usd_volume`; R1 aggregator config | XFI-0009; EV-0024; R1-0011 | open | aggregate/market-data | Classic/SAC USDC quotes are 7-decimal, but `windowUSDVolume` always divides quote amounts by 1e8 for fiat-USD windows; R1 currently avoids false drops only by setting `min_usd_volume=0`, disabling the threshold. |
| F-1214 | critical | `main` is unprotected, so required CI, CODEOWNER review, and signed commits are not enforced | GitHub branch protection/rulesets; `CONTRIBUTING.md`; `CODEOWNERS`; release process | XFI-0010; EV-0025; EV-0026 | open | repo-admin/security | GitHub reports `main.protected=false`; branch protection/rulesets are unavailable on the current private repo tier, contradicting local policy docs and removing the merge gate for production code. |
| F-1215 | high | Production deployment environments have no required reviewers despite holding deploy secrets | GitHub environments; `.github/workflows/deploy.yml`; Cloudflare Pages deploy workflows; repo Actions secrets | XFI-0010; EV-0025; EV-0026 | open | repo-admin/ops | `r1`, docs, explorer, status, and GitHub Pages environments have empty protection rules and admin bypass enabled; manual deployment jobs can access production secrets without environment approval. |
| F-1216 | high | GitHub Actions allows all third-party actions without SHA pinning while workflows use tag-pinned actions | GitHub Actions repository policy; `.github/workflows/*.yml` | XFI-0010; EV-0025; EV-0026 | open | repo-admin/security | Hosted Actions policy has `allowed_actions=all` and `sha_pinning_required=false`; workflows invoke many external actions by mutable version tags, including deployment and release paths. |
| F-1217 | high | SEP-10 replay protection is optional and can run guard-free when Redis is absent | SEP-10 validator; API startup wiring; auth token endpoint; bearer auth | XFI-0011; EV-0027; EV-0053; R1-0012 | open | api/security | Current source has a Redis replay guard, but `auth_mode=sep10` does not require Redis or a guard; guardless deployments preserve replayable signed challenges. R1 currently has SEP-10 disabled. |
| F-1218 | high | Public signup can mint immediately usable 1000/min API keys from unverified emails and non-atomic duplicate checks | `/v1/signup`; signup tracker; API key store; signup UI/OpenAPI | XFI-0012; EV-0028 | open | api/security/billing | Attackers can script unique emails, missing tracker deployments, or concurrent same-email races to mint many Starter keys without email ownership proof or billing gate. |
| F-1219 | high | Stripe paid-upgrade webhook still bypasses platform subscription and dashboard-key sources of truth | Stripe webhook; Redis API keys; Postgres platform billing/API keys | XFI-0013; EV-0030; EV-0053 | open | billing/platform/api | Current source has Postgres Stripe event dedupe/audit wiring, but the webhook still mutates only Redis keys by `client_reference_id`; subscriptions, accounts, and Postgres dashboard keys are not upgraded. |
| F-1220 | high | Tagged deploys can restart schema-dependent binaries without shipping or applying matching migrations | Release/deploy workflow; Ansible binary deploy; migrations; R1 schema state | XFI-0014; EV-0031; R1-0013 | open | release/ops/db | The production deploy path downloads and swaps binaries only; migration sync/apply is an initial bring-up/manual step, and there is no schema-compatibility readiness gate. |
| F-1221 | medium | Release/deploy docs still claim GHCR container image publishing that the current release workflow explicitly removed | Release workflow; release/deploy docs; Docker image expectations | XFI-0014; EV-0032 | open | docs/release | Operators and self-hosters are told to expect GHCR artifacts that the workflow intentionally no longer produces. |
| F-1222 | medium | Rollback docs point operators to nonexistent `/opt/ratesengine/release-<tag>` directories instead of actual binary backups | Release process runbook; Ansible deploy backup layout; R1 sidecars | XFI-0014; EV-0032; R1-0013 | open | ops/release | Incident fallback rollback can fail because the documented artifact path is not produced by the current deploy task. |
| F-1223 | high | R1 runs a stale Caddyfile that exposes `/metrics` publicly and collapses Cloudflare client IPs to edge IPs | Caddy reverse proxy; API trusted proxy config; public observability boundary | XFI-0015; EV-0033; R1-0014 | open | ops/security | Source Caddy blocks `/metrics` and forwards `{client_ip}` after Cloudflare CIDR validation; R1 forwards `{remote_host}` and public `/metrics` returns HTTP 200 with internal Prometheus metrics. |
| F-1224 | medium | Dashboard magic-link and session audit IP fields record proxy/loopback IPs instead of real client IPs | Dashboard auth handlers; session middleware; platform token/user stores; Caddy/API proxying | XFI-0016; EV-0034; R1-0014 | open | dashboard/security | Login/security audit fields intended for IP/new-country signals parse `r.RemoteAddr` directly instead of the middleware-resolved remote IP. |
| F-1225 | high | `/v1/history/since-inception` returns empty XLM/USD history while chart and direct USDC history have data | Historical price APIs; stablecoin USD fallback; Timescale CAGG readers | XFI-0017; EV-0035; R1-0015 | open | api/market-data | Since-inception reads only literal `native/fiat:USD`; it lacks the configured USD-pegged stablecoin fallback used by chart/price/VWAP/TWAP/OHLC surfaces. |
| F-1226 | high | Dashboard API-key allowlists, permissions, monthly quotas, and usage fields are accepted but not enforced at runtime | Platform API keys; dashboard key UI/API; auth validator; rate/quota enforcement | XFI-0018; EV-0036 | open | platform/api/security | Key records carry policy fields, but the authenticated request path propagates only identity/tier/rate-limit and never checks allowlists, referer, permissions, monthly quotas, or last-used updates. |
| F-1227 | medium | The `ratesengine-migrate` container cannot apply bundled migrations out of the box | Docker migrate image; migration binary; self-hosting docs | XFI-0019; EV-0037 | open | docker/db | Runtime image copies only the binary while the binary defaults to a missing `migrations` directory. |
| F-1228 | high | SSE streams are cut off after 30 seconds by the API server write timeout | API HTTP server; SSE stream endpoints; R1 live API | XFI-0020; EV-0038; R1-0016 | open | api/streaming/ops | R1 tip stream closes at elapsed 30s despite 5s events and 15s heartbeats. |
| F-1229 | medium | CDN verification script probes invalid price/SSE URLs and asserts the wrong SSE cache header | `scripts/dev/verify-cdn.sh`; price/tip API; SSE headers | XFI-0021; EV-0039 | open | ops/api | Script uses `base=` where handlers require `asset=` and expects `no-store` while SSE sets `no-cache`. |
| F-1230 | high | R1 `since-inception` history for core XLM/USDC starts on 2026-05-03, not one year or inception | Historical API; backfill; R1 data depth | XFI-0022; EV-0040; R1-0017 | open | data/backfill/api | Direct XLM/Circle-USDC daily history has only nine buckets. |
| F-1231 | high | Canonical CI is PR-only while `main` is unprotected, so direct pushes can bypass full verification | GitHub CI triggers; branch protection; release governance | XFI-0023; EV-0041; EV-0025 | open | repo-admin/ci | Full `ci` does not run on main pushes, but hosted branch protection does not enforce the PR-only assumption. |
| F-1232 | high | Circle USDC has `price_usd` on asset detail but 404s or disappears from `/v1/price` and batch price APIs | Price API; batch API; asset detail price enrichment | XFI-0024; EV-0042; R1-0018 | open | api/market-data | Asset detail returns USDC USD price, but single price 404s and batch returns an empty array for the same asset. |
| F-1233 | high | SDEX historical backfill silently drops legacy V0 claim atoms while claiming genesis coverage | SDEX decoder; dispatcher metrics; historical backfill | XFI-0025; EV-0044 | open | ingest/backfill/sdex | Legacy V0 claim atoms are rejected then suppressed inside the source adapter, so old SDEX fills disappear without decode-error metrics. |
| F-1234 | medium | Oracle decoders silently skip unknown feeds inside mixed batches, hiding upstream coverage drift | Reflector/Redstone/Band decoders; canonical allow-lists; decoder metrics | XFI-0026; EV-0045 | open | oracle/coverage/observability | New oracle-supported assets can be omitted from stored oracle rows without decode-error metrics as long as the same event contains at least one known asset. |
| F-1235 | medium | External CEX stream parser errors are skipped without the decode-error metrics promised by runbooks | Binance/Kraken/Bitstamp/Coinbase streamers; external metrics; decode-error runbook | XFI-0027; EV-0046 | open | external/observability | Vendor frame/schema drift can drop live trades while `SourceDecodeErrorsTotal` stays clean. |
| F-1236 | high | Supply snapshots can be stamped at a fresh ledger while using stale component observations | Supply refreshers; supply observer storage; asset supply API/market-cap fields | XFI-0028; EV-0047 | open | supply/data-quality | Snapshot ledger is the max ingestion cursor, but component readers use latest-at-or-before rows without freshness checks. |
| F-1237 | medium | CoinMarketCap polling ignores verified CMC IDs and can bind ambiguous tickers to the wrong asset | Verified currency catalogue; CMC poller; external aggregator observations | XFI-0029; EV-0048 | open | external/identity | The poller queries CMC by ticker and takes the first duplicate-ticker result even though the catalogue stores numeric CMC IDs. |

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

Expected: Public Next.js apps should be on patched versions, with automated pnpm updates and CI advisory gates.

Observed: all three apps pin `next@15.0.4`; `pnpm audit --audit-level moderate` reports 27 advisories per app including 2 critical and 8 high. CI typechecks/lints/builds web apps but does not run `pnpm audit`; Dependabot does not include npm/pnpm ecosystems; hosted GitHub vulnerability and Dependabot alerts are disabled.

Impact: Public explorer/status/dashboard surfaces inherit known RCE/auth-bypass/DoS/cache/XSS classes until upgraded. Dashboard risk is higher because it is account-facing.

Remediation direction: upgrade `next`/`eslint-config-next` across all web apps to a patched compatible release, regenerate lockfiles, add Dependabot npm entries for each web app, enable GitHub vulnerability/Dependabot alerts, and add `pnpm audit --prod --audit-level high` or equivalent to CI.

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

Status: `open`

Affected surface:

- `web/dashboard/src/app/keys/page.tsx`
- `internal/api/v1/dashboardkeys/handlers.go`
- `internal/platform/postgresstore/apikey_store.go`
- `internal/auth/apikey_postgres.go`
- `internal/api/v1/middleware/ratelimit.go`

Evidence:

- `XFI-0008`
- `EV-0023`

Expected: key rate limits should be derived from account tier/subscription or an operator-approved override, not from a free-form customer dashboard input.

Observed: the dashboard UI submits `rate_limit_per_min`; the handler accepts any positive value up to 100,000 for owner/admin/member sessions; the Postgres auth validator copies that value into the authenticated subject; the rate-limit middleware uses it as the effective per-key budget.

Impact: a free or unbilled account can mint a key with 100x the default public paid-key budget, bypassing planned billing gates and creating an abuse/capacity risk.

Remediation direction: remove customer control over raw rate-limit values, derive limits server-side from account tier and verified subscription state, require staff/operator permission for explicit overrides, and add tests proving free accounts cannot mint elevated-budget keys.

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

Observed: Actions policy is `allowed_actions=all` and `sha_pinning_required=false`; workflow files call many external actions by mutable version tags, including `cloudflare/wrangler-action@v3`, `stoplightio/spectral-action@v0.8.13`, `grafana/setup-k6-action@v1`, `pnpm/action-setup@v6`, and standard `actions/*` tags.

Impact: a compromised upstream action tag or newly introduced unreviewed action can execute in CI with repository or deployment secrets, including release/deploy paths.

Remediation direction: set Actions policy to selected trusted actions, enable SHA pinning or pin non-GitHub-owned actions to full commit SHAs, review transitive deployment actions, and add workflow linting for unpinned `uses:` entries.

### F-1217. SEP-10 replay protection is optional and can run guard-free when Redis is absent

Severity: `high`

Status: `open`

Affected surface:

- `internal/auth/sep10/validator.go`
- `internal/api/v1/auth_sep10.go`
- `internal/auth/sep10/validator_test.go`
- R1 SEP-10 runtime configuration

Evidence:

- `XFI-0011`
- `EV-0027`
- `EV-0053`
- `R1-0012`

Expected: when SEP-10 authentication is enabled, replay protection should be mandatory and fail closed; a signed challenge should be accepted once for its time-bound window.

Observed: current source implements a Redis-backed `ReplayGuard` and wires it when Redis is configured. The same startup path still permits a configured SEP-10 validator without Redis and explicitly leaves it guard-free when Redis is absent; `auth_mode=sep10` does not require a guard. R1 currently returns 503 for SEP-10, so this is a latent source/configuration flaw until SEP-10 is enabled.

Impact: an operator can enable SEP-10 in a Redis-less or mis-scoped deployment and unknowingly preserve replayable signed challenges. If a signed challenge XDR is captured, it can be reused within the challenge window on that guardless deployment to mint additional valid JWTs.

Remediation direction: require a replay guard whenever `auth_mode=sep10` or public SEP-10 token issuance is enabled. Treat missing Redis/guard as a startup error or add a Postgres-backed guard fallback; add startup tests for `auth_mode=sep10` with no Redis and token-flow tests proving guardless operation is rejected.

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

Expected: self-service key minting should prove email ownership or use a stronger anti-abuse gate before issuing a usable 1000/min key; duplicate checks should be atomic if they are the idempotency guarantee.

Observed: `/v1/signup` mints a plaintext Starter API key immediately from a parsed email string. The duplicate tracker is optional and tests pin that duplicate signup succeeds when it is nil. When Redis is wired, the flow still performs lookup, key create, then SETNX mark, so concurrent same-email requests can mint multiple keys.

Impact: attackers can cheaply mint large numbers of free API keys with rotating email strings or races, bypassing the anonymous 60/min floor and creating capacity/billing abuse.

Remediation direction: route signup through the magic-link/dashboard account flow or require email verification before exposing plaintext keys; make idempotency atomic by reserving the email hash before key creation; add per-email/domain/device abuse controls and alerting.

### F-1219. Stripe paid-upgrade webhook still bypasses platform subscription and dashboard-key sources of truth

Severity: `high`

Status: `open`

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

Expected: Stripe paid-upgrade events should update the same account, subscription, and API-key records that dashboard users and runtime auth consume, with durable idempotency and audit.

Observed: current source wires Postgres event dedupe and audit rows when Postgres is available, which reduces the earlier idempotency gap. The side effect still uses `auth.RedisAPIKeyStore`: it finds keys by `client_reference_id` and updates Redis key rate limits only. The webhook does not call `UpsertSubscription`, does not update Postgres dashboard keys/accounts/subscription state, acknowledges paid events with no keys as 200, and can return 200 after partial or total key-update failure.

Impact: paid customers using dashboard-created keys can pay and still keep old limits or missing subscription state; customer-facing dashboard/billing truth can disagree with legacy Redis key state; failed upgrades can be acknowledged and then require manual reconciliation.

Remediation direction: move Stripe side effects onto platform Postgres account/subscription/API-key stores, call subscription upserts for relevant Stripe event types, update all active account keys in the runtime source of truth, and return retryable status on unambiguous total failure.

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
- `R1-0013`

Expected: a tagged production deploy that can introduce code depending on new tables, columns, CAGGs, or indexes should ship and apply the matching migration set before restarting binaries, or fail readiness when the binary and database schema diverge.

Observed: the current deploy workflow downloads release binaries, verifies checksums, and runs an Ansible binary-swap playbook. That playbook restarts/probes services but does not copy `migrations/`, run `ratesengine-migrate up`, or check `schema_migrations`. Migration sync/apply exists in the initial archival-node role and manual runbook instructions instead. R1 currently reports schema `28|f`, but the deploy mechanism itself would not prevent a future binary/schema mismatch.

Impact: a release can pass checksum and `/v1/healthz` probes while code paths depending on a new migration fail at runtime, creating partial outages or silent feature breakage after deploy.

Remediation direction: make migrations a first-class deploy artifact and pre-binary step, or embed an expected schema version in each binary and gate `/readyz`/deploy on matching `schema_migrations`. Add an integration test or CI check proving migrations are included in the release/deploy contract.

### F-1221. Release/deploy docs still claim GHCR container image publishing that the current release workflow explicitly removed

Severity: `medium`

Status: `open`

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

Expected: customer-visible key policy fields should be enforced on every authenticated request, or the UI/API should clearly mark them as not active.

Observed: dashboard key creation stores `monthly_quota`, `permissions`, `ip_allowlist`, `referer_allowlist`, and expiry/revocation fields. Runtime auth validates only key hash, revocation, expiry, and account status, then returns a subject containing tier/key/rate-limit. There is no request-aware check for client IP, referer, permissions, monthly quota, or usage increments; `TouchUsage` has no production caller.

Impact: customers can create keys that appear IP-bound, referer-bound, scoped, or quota-limited, but those controls do not protect the API. This is a security and trust issue for dashboard users and a billing-control gap for paid plans.

Remediation direction: add a request-aware key policy middleware after auth that enforces IP/referer/permission/monthly quota, updates debounced usage, and returns 403/429 with clear problem bodies. Hide or label fields as pending until enforcement is live.

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

### F-1228. SSE streams are cut off after 30 seconds by the API server write timeout

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

Status: `open`

Affected surface:

- `.github/workflows/ci.yml`
- `.github/workflows/api-audit.yml`
- `CONTRIBUTING.md`
- hosted GitHub branch protection

Evidence:

- `XFI-0023`
- `EV-0041`
- `EV-0025`

Expected: every change reaching `main` should either have passed the canonical CI workflow through an enforced PR path, or the same canonical gate should run on main pushes.

Observed: `ci.yml` triggers only on `pull_request` and explicitly disables push-to-main CI. Hosted GitHub reports `main.protected=false`, so the PR-only assumption is not enforced. The path-limited `api-audit` workflow can run on some main pushes, but it only smokes public API examples and is not equivalent to lint, tests, builds, import rules, govulncheck, gitleaks, OpenAPI generation, and web app checks.

Impact: a direct push to `main` can land broken code or vulnerable dependencies without the full suite running. This compounds the existing branch-protection finding and weakens release confidence for a project preparing public market-data launch.

Remediation direction: either enable enforceable branch protection that requires the `ci` jobs before merge, or restore the canonical `ci` push trigger for `main` until protection is actually active.

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

Status: `open`

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

Expected: SDEX backfill either decodes every claim-atom shape required by the requested historical range, including legacy V0, or rejects/marks unsupported ranges with visible errors and coverage metadata.

Observed: `decodeClaimAtom` returns `ErrUnknownClaimAtomType` for `ClaimAtomTypeV0`, the legacy raw-Ed25519 claim atom shape. The SDEX dispatcher adapter catches each per-claim error and continues, returning a successful `Decode` result with fewer outputs. Because the dispatcher only increments `DecodeErrors` when `OpDecoder.Decode` itself returns an error, replaying old ledgers drops V0 fills without an error metric. The same package README says SDEX backfill is supported to genesis, while the discovery notes say historical backfill must handle V0.

Impact: since-inception and one-year-plus SDEX history is materially incomplete for old protocol ranges, but operators and clients get no direct signal that data was skipped. This weakens market-history depth claims and any CoinGecko/CMC-style charting, volume, or OHLC computation over pre-modern Stellar DEX history.

Remediation direction: implement V0 decoding by deriving the seller G-address from raw Ed25519 bytes, add fixture/unit coverage for V0 claim atoms, and add backfill-range coverage metadata or explicit unsupported-range rejection for any protocol-era gaps.

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

Status: `open`

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

Expected: malformed external websocket frames, unknown subscribed symbols, and vendor schema drift should increment a per-source metric that the decode-error runbook and monitoring can alert on.

Observed: all four CEX streamers skip parser errors and continue the stream without incrementing `SourceDecodeErrorsTotal` or another parser-error counter. The runner only records poller outcomes, not websocket parse failures. The external connector README says these connectors contribute to the same decode-error budget as on-chain decoders.

Impact: a vendor-side schema change or unexpected feed payload can silently reduce live trade coverage. Operators may only notice after price freshness or source-stopped alerts fire, and the decode-errors runbook will show no evidence even though the parse path is failing.

Remediation direction: increment a source-labelled parse/decode counter on every skipped streamer parse error, include reason labels with bounded cardinality, and update tests/runbooks so injected malformed frames produce observable metrics without killing the stream.

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

Expected: a supply snapshot for ledger `N` should be computed from supply-observer components that are complete through ledger `N`, or it should publish explicit component freshness/lag metadata and avoid presenting stale inputs as current supply.

Observed: the aggregator and CLI choose the maximum `last_ledger` across ingestion cursors as the snapshot ledger. Component readers then use `AtOrBefore` storage queries for trustlines, claimable balances, LP reserves, SAC balances, SEP-41 event totals, and account observations. These reader interfaces return balances/totals but not the ledger of each component row, so the refresher cannot detect a stale component before inserting a snapshot at the max ledger.

Impact: if one supply observer stalls while another source advances, asset supply and derived market-cap fields can look current but include old balances. This is especially risky for Stellar-specific depth claims around classic/SAC/SEP-41 supply and for customer-facing asset detail pages.

Remediation direction: resolve the snapshot ledger as the minimum complete ledger across all required supply observer cursors for the target asset, or return component ledgers from storage readers and reject/mark snapshots when any component exceeds a freshness lag. Expose component freshness in diagnostics and tests.

### F-1237. CoinMarketCap polling ignores verified CMC IDs and can bind ambiguous tickers to the wrong asset

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

Expected: CoinMarketCap observations should resolve to the verified currency identity, using the numeric CMC IDs already stored in the catalogue when available.

Observed: the indexer builds a ticker-only `aggregatorPairs` list, and the CMC poller queries `symbol=` rather than the catalogue's numeric `coinmarketcap_id`. When CMC returns multiple coins for a ticker, the poller takes `coins[0]`; a test explicitly pins that behavior.

Impact: CMC can write an oracle update for the wrong project when tickers collide or ranking changes. That corrupts external divergence checks and customer-facing parity against CMC for any ambiguous ticker.

Remediation direction: thread `Catalogue.CoinMarketCapIDs()` into the CMC poller and query/filter by CMC ID when available. Keep symbol fallback only for entries without an ID, and add tests where the correct verified ID is not the first duplicate ticker.
