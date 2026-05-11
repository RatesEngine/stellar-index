---
title: Launch task list — code-grounded RFP/proposal audit
last_verified: 2026-05-11
status: ratified backlog
related:
  - docs/stellar-rfp.md
  - docs/freighter-rfp.md
  - docs/ctx-proposal.md
  - docs/architecture/coverage-matrix.md
  - docs/architecture/launch-readiness-backlog.md
---

> **Update 2026-05-11:** the site is live on r1 but has no
> consumer traffic yet ("live in-development"). The §G
> *launch-blocking* vs §J *post-launch polish* distinction
> recorded below is no longer load-bearing — operator decision
> 2026-05-11 is to treat the entire list as one polish queue and
> ship items as they're ready. Order-of-execution suggestions in
> §I still hold as guidance for *what to prioritise*, but no row
> blocks the "launch" event today.

# Launch task list — what's actually left

## Why this doc exists

The repo already has two launch-tracking artefacts:

- [`docs/architecture/coverage-matrix.md`](architecture/coverage-matrix.md)
  — RFP × ADR × code traceability per requirement
- [`docs/architecture/launch-readiness-backlog.md`](architecture/launch-readiness-backlog.md)
  — implementation backlog by surface

Both are accurate but optimistic. This doc was written by walking
the *code* — handlers, migrations, registries, binaries, tests —
and cross-referencing it against the verbatim requirement bullets
in the two RFPs and our awarded proposal. Where the existing docs
say "✅" but the code only half-implements the requirement, this
doc records the partial.

**This doc is the launch backlog. Items below either ship before
production cutover or are explicitly deferred with a justification.**

## Methodology

For each requirement:

1. Quote the source bullet (RFP §, proposal §).
2. Cite the code that fulfils it (`file:line` or migration / handler /
   registry / test).
3. Status:
   - ✅ **shipped** — code present, wired in the binary, tests cover it
   - ⚠ **partial** — code exists but a piece is missing; the gap is named
   - 🟡 **designed** — interface/handler/scaffolding shipped, production
     wiring missing
   - ❌ **missing** — no code
   - ⏳ **deferred** — explicit post-launch with justification
4. Where the status is anything other than ✅ or ⏳, the row appears
   in §G ("Remaining work") with a concrete acceptance criterion.

---

## A. Stellar Prices API RFP — `docs/stellar-rfp.md`

### A1. Asset coverage — all classic + SEP-41 Soroban tokens

| RFP § | Code evidence | Status |
|---|---|---|
| Classic asset identity (code + issuer + home_domain) | `internal/canonical/asset.go` Asset type; `internal/sources/accounts/` AccountEntry observer; `internal/metadata/lcm_resolver.go` HomeDomainFor | ✅ |
| SEP-41 Soroban token identity + events | `internal/sources/sep41_supply/` decoder; `internal/canonical/discovery/` Sniffer + AsyncSink (`cmd/ratesengine-indexer/main.go:disp.SetDiscoverySink`) | ✅ |
| SAC-wrapped classic recognised as canonical | `internal/canonical/asset.go` SAC contract handling; `[supply.sac_wrappers]` config in `internal/config/config.go:Supply` | ✅ |
| Auto-discovery of new SEP-41 contracts | `internal/canonical/discovery/Sniffer` + `discovered_assets` hypertable (`migrations/0006_create_discovered_assets.up.sql`) + `ratesengine_discovery_dropped_hits_total` backpressure metric | ✅ |
| `home_domain` → `stellar.toml` SEP-1 resolution | `internal/metadata/sep1.go` Resolver + `cache.go` Redis cache + `cmd/ratesengine-api/main.go:177` wiring | ✅ |

### A2. Oracle coverage — Chainlink, Redstone, Band, Reflector + others

| Oracle | Code evidence | Status |
|---|---|---|
| **Reflector** (DEX / CEX / FX, three contracts) | `internal/sources/reflector/`; registry entries `reflector-{dex,cex,fx}` (`internal/sources/external/registry.go:41-43`); BackfillSafe=true | ✅ |
| **Redstone** (Adapter + 19 per-feed contracts) | `internal/sources/redstone/`; ContractCallDecoder consumes `write_prices(updater, feed_ids, payload)` op args; registry `redstone` BackfillSafe=true | ✅ |
| **Band** (StandardReference contract) | `internal/sources/band/` ContractCallDecoder watching `relay()` / `force_relay()` (no events emitted); registry `band` BackfillSafe=true | ✅ |
| **Chainlink** (HTTP cross-check, no on-chain Stellar) | `internal/divergence/chainlink/` reference; wired in `cmd/ratesengine-api/main.go::buildDivergenceReferences` | ✅ |
| SEP-40 output compatibility (so others can consume our prices) | `/v1/oracle/lastprice` (`internal/api/v1/oracle_sep40.go`); `/v1/oracle/prices`; `/v1/oracle/x_last_price`; `/v1/oracle/latest` (raw observations) | ✅ |
| DIA mainnet | testnet-only at audit time | ⏳ post-launch |

### A3. Price aggregation across Soroswap, Aquarius, SDEX, Blend + others

| Venue | Code evidence | Status |
|---|---|---|
| SDEX | `internal/sources/sdex/` ClaimAtom decoder | ✅ |
| Soroswap | `internal/sources/soroswap/` factory enumeration + Swap+Sync correlator | ✅ |
| Aquarius (volatile / stableswap / concentrated) | `internal/sources/aquarius/` unified-event decoder; 313 pools, 3 unique WASMs audited | ✅ |
| Phoenix | `internal/sources/phoenix/` 8-event-per-swap correlator | ✅ |
| Comet | `internal/sources/comet/` shared `("POOL", ...)` topic decoder | ✅ |
| Blend (auctions as directional signals; not VWAP) | `internal/sources/blend/`; `migrations/0009_create_blend_auctions.up.sql` | ✅ |
| CEX feeds (Binance, Coinbase, Kraken, Bitstamp) | `internal/sources/external/{binance,coinbase,kraken,bitstamp}/`; runner wires via `setSourceEnabled` (`cmd/ratesengine-indexer/main.go:233`) | ✅ |

### A4. VWAP with configurable USD-volume threshold

| Aspect | Code evidence | Status |
|---|---|---|
| VWAP impl | `internal/aggregate/vwap.go::VWAP` | ✅ |
| TWAP fallback | `internal/aggregate/twap.go::TWAP`; orchestrator `evaluateMethod` | ✅ |
| Configurable per-pair USD volume threshold | `Aggregate.MinUSDVolume` (`internal/config/config.go:488`); `dropForMinUSDVolume` (`internal/aggregate/orchestrator/orchestrator.go:710`) | ✅ |
| Class filter (only `ClassExchange` contributes) | `internal/sources/external/framework.go:51` "v1 policy: only ClassExchange contributes to VWAP" + aggregator `external.Lookup` | ✅ |
| Triangulation when direct pair missing | `internal/aggregate/triangulate.go::TriangulateChain`; orchestrator triangulation worker; `flags.triangulated` envelope field | ✅ |

### A5. Real-time price endpoints

| Aspect | Code evidence | Status |
|---|---|---|
| `/v1/price` (closed-bucket, ADR-0015) | `internal/api/v1/price.go::handlePrice` | ✅ |
| `/v1/price/tip` (rolling-window LKG, ADR-0018) | `internal/api/v1/price_tip.go::handlePriceTip` | ✅ |
| `/v1/price/tip/stream` SSE (per-tick) | `internal/api/v1/price_tip_stream.go::handlePriceTipStream` | ✅ |
| `/v1/observations` per-source raw (ADR-0018) | `internal/api/v1/observations.go::handleObservations` | ✅ |
| `/v1/observations/stream` SSE (per-tick) | `internal/api/v1/observations_stream.go::handleObservationsStream` | ✅ |
| `/v1/price/stream` SSE (closed-bucket, Hub-driven) | `internal/api/v1/price_stream.go::handlePriceStream` returns 503 — `s.hub == nil` always true; **aggregator never publishes** to a `streaming.Hub`. The handler is wired but no producer. | 🟡 |
| Degradation flags (`stale`, `triangulated`, `frozen`, `divergence_warning`, `single_source`, `class_diversity_low`) | `internal/api/v1/envelope.go::Flags` + handler-side stamping in `price.go` | ✅ |

### A6. Historical endpoints + OHLC

| Aspect | Code evidence | Status |
|---|---|---|
| `/v1/history` time-bucketed | `internal/api/v1/history.go::handleHistory` | ✅ |
| `/v1/history/since-inception` | `internal/api/v1/history.go::handleHistorySinceInception`; storage at `internal/storage/timescale/aggregates.go:76` | ✅ |
| `/v1/ohlc` candlestick aggregates | `internal/api/v1/ohlc.go::handleOHLC` | ✅ |
| `/v1/chart` opinionated chart contract (ADR-0020) | `internal/api/v1/chart.go::handleChart` | ✅ |
| OHLC continuous aggregates (1m / 15m / 1h / 4h / 1d / 1w / 1mo) | `migrations/0002_create_price_aggregates.up.sql` — 7 CAGGs + `add_continuous_aggregate_policy` auto-refresh | ✅ |
| Retention: 1h+ indefinite, 1m + 15m capped at 30d | `migrations/0002` `add_retention_policy('prices_{1m,15m}', INTERVAL '30 days')`; no retention on 1h+ | ✅ |
| **CAGG `twap` column is NOT real TWAP** (arithmetic mean) | `migrations/0002` notes; `/v1/twap` ignores the column and computes from raw trades | ✅ (caveated; `cmd/ratesengine-aggregator/main.go` carries the warning) |

### A7. Supported timeframes / granularities (1h, 24h, 1w, 1mo, 1yr, all-time)

CAGG migrations cover every required granularity. The chart contract
(ADR-0020) maps RFP timeframes to CAGG buckets in `internal/api/v1/chart.go`.

Status: **✅**

### A8. Base + quote volume in USD

| Aspect | Code evidence | Status |
|---|---|---|
| `usd_volume` column on trades | `migrations/0001_create_trades_hypertable.up.sql` (added by 0004 relaxation) | ✅ |
| Per-trade USD-volume computation | `internal/storage/timescale/trades.go::tradeUSDVolume` + `USDVolumeQuoteSpec` | ⚠ — covers off-chain CEX/FX (uniform 10^8 scale) and on-chain DEX with **operator-declared USD-pegged classic quotes only** (Phase 1). Pure on-chain DEX trades against non-USD quotes (XLM/XLM-LP, XLM/AQUA etc.) leave `usd_volume` NULL. Phase 2 (per-trade FX-anchor multiplication for on-chain trades against any quote) is designed but not shipped. |
| FX anchor for non-USD pairs | `internal/aggregate/orchestrator/triangulate.go::legPrice` X2.5 forex snap; FX sources via `internal/sources/external.FXSources()` | ✅ |
| `volume_24h_usd` on `/v1/assets/{id}` | `internal/api/v1/assets_f2.go::populateVolume24h`; `Volume24hUSDForAsset` reader | ✅ (subject to A8 ⚠ caveat — only counts trades with non-NULL `usd_volume`) |

### A9. Performance SLAs

See §D below — these are shared with Freighter.

### A10. Completely open source

Apache-2.0 license, public-flip strategy ratified
(`docs/operations/public-flip.md`), pre-flip checklist all ☑.
The cutover act (push to public repo) is L6.4. **🟡 designed —
prep complete, cutover pending.**

---

## B. Freighter RFP V1 — `docs/freighter-rfp.md`

### B1. Asset metadata fields

| Field | Code evidence | Status |
|---|---|---|
| Asset/Token Code | `pkg/client/types.go::AssetDetail.Code` + `internal/api/v1/assets.go` | ✅ |
| Current Price (USD) | `AssetDetail.PriceUSD` populated in `assets_f2.go` from aggregator output | ✅ |
| Asset Type (`classic` / `soroban`) | `AssetDetail.Type`; derived in `internal/canonical/asset.go` | ✅ |
| Issuer Address (G…) | `AssetDetail.Issuer` | ✅ |
| Contract Address (C…) | `AssetDetail.Contract` | ✅ |
| Home Domain | `AssetDetail.HomeDomain` populated from `internal/metadata/lcm_resolver.go::HomeDomainFor` (LCM-tracked) and SEP-1 fallback | ✅ |

### B2. Historical price chart (1h / 24h / 1w / 1mo / since-inception)

Same as A6 / A7. **✅**

---

## C. Freighter RFP V2 — market data extension

### C1. Market Cap, FDV, Trading Volume, Supplies

| Field | Code evidence | Status |
|---|---|---|
| Market Cap (`circulating × current_price`) | `internal/api/v1/assets_f2.go::populateMarketCap` (line 154) | ✅ |
| FDV (`max × current_price`) | same handler | ✅ |
| 24h Trading Volume (USD) | `Volume24hUSDForAsset` reader; `internal/storage/timescale/trades_usd_volume.go` | ⚠ — bound to A8 caveat |
| Circulating Supply (XLM Algorithm 1) | `internal/sources/accounts/` reserve observer + `internal/supply/` reader | ✅ |
| Circulating Supply (Classic Algorithm 2) | `internal/sources/{trustlines,claimable_balances,liquidity_pools,sac_balances}/` observers; `internal/supply/StorageClassicSupplyReader` | ✅ |
| Circulating Supply (SEP-41 Algorithm 3) | `internal/sources/sep41_supply/`; `migrations/0015_create_sep41_supply_events.up.sql`; `internal/supply/StorageSEP41SupplyReader` | ✅ |
| Total Supply | same observers, no exclusions | ✅ |
| Max Supply | SEP-1 stellar.toml overlay + operator-config; `internal/metadata/` | ✅ |
| **Indexer wiring of all 6 LCM observers** | `cmd/ratesengine-indexer/main.go::pipeline.{RegisterSupplyEntryDecoders,RegisterSupplyEventDecoders}` driven by `[supply.*]` config | ✅ |

### C2. `change_24h_pct`

| Aspect | Code evidence | Status |
|---|---|---|
| OpenAPI declares the field | `openapi/rates-engine.v1.yaml:1400` | — |
| Go handler / SDK populate it | **No code path** — no `Change24hPct` field in `pkg/client/types.go::AssetDetail`; `assets_f2.go` does not compute it | ❌ — spec/code drift |

The proposal does not commit to this field; the *Freighter RFP API
characteristics* §"Bulk query support" mentions "current price
[and] 24hr % change". OpenAPI declares it but no code path
exists. Either implement (closed-bucket pct delta over a 24h
window) or remove from OpenAPI. **Decision needed before launch.**

---

## D. Performance SLAs (Freighter §"Data Provider Requirements")

| Metric | Target | Code evidence | Status |
|---|---|---|---|
| API latency p95 | ≤ 200 ms | `cmd/ratesengine-sla-probe/`; k6 scenarios under `test/load/scenarios/`; SLO rules `deploy/monitoring/rules/slo.yml` | ⚠ — probe shipped (#283/290/294), k6 scenarios shipped (#L5.1–5.3); **no actual SLA-proof report file under `docs/operations/sla-proof-YYYY-MM-DD.md`** — template at `docs/operations/sla-proof-template.md` waits for a real run |
| API latency p99 | ≤ 500 ms | same | ⚠ |
| Responsiveness | ≥ 99.9 % | HA topology (ADR-0008); Patroni / Sentinel / HAProxy ansible roles | ⚠ — synthetic gate measurable; production-traffic verification post-launch |
| Data freshness | ≤ 30 s | `internal/aggregate/orchestrator/orchestrator.go` Tick cadence; `flags.stale` on envelope | ✅ |
| SEV-1 detect ≤ 15 min / respond ≤ 30 min | RFP F3.5 | `docs/operations/sev-playbook.md` §2; runbooks (62 files); alerts catalogue | ⚠ — playbook + alerts shipped; **no actual drill-output record** — `docs/operations/drills/scenarios/` has 2 templates; nothing in `docs/operations/sev-drill-*.md` |
| SEV-2 detect ≤ 30 min / respond ≤ 60 min | F3.6 | same | ⚠ — same gap |

---

## E. API characteristics (Freighter §)

| Requirement | Code evidence | Status |
|---|---|---|
| RESTful (or GraphQL) | `internal/api/v1/server.go` registers 33 REST handlers (`mux.HandleFunc`); GraphQL not implemented | ✅ (REST canonical; GraphQL deferred per proposal) |
| Rate limits ≥ 1000 req/min | `internal/ratelimit/bucket.go::Bucket` Redis token bucket; `internal/api/v1/middleware/ratelimit.go::RateLimitBySubject` per-tier; `Subject.RateLimitPerMin` per-key override (PR #439) | ✅ |
| Bulk query support | `GET /v1/price/batch` (≤100 ids) + `POST /v1/price/batch` (≤1000 ids); `internal/api/v1/price.go::handlePriceBatch{,Post}` | ✅ |
| Bulk: current price + 24hr % change | Current price ✅; 24h % change → see C2 ❌ | ⚠ |
| API key auth + per-key quotas | `internal/auth/apikey_redis.go::RedisAPIKeyValidator`; `/v1/account/keys` self-service issuance | ✅ |
| SEP-10 Web Auth | `internal/auth/sep10/`; `/v1/auth/sep10/{challenge,token}` | ✅ |
| API reference documentation | `make docs-api` regenerates `docs/reference/api/index.html` from `openapi/rates-engine.v1.yaml`; `.github/workflows/api-docs.yml` deploys to GitHub Pages | ✅ |
| Self-service onboarding | `docs/getting-started.md`; `/v1/account/keys` POST issues a fresh key | ✅ |

---

## F. Proposal commitments beyond the RFPs

These are commitments in `docs/ctx-proposal.md` that the RFP did
not explicitly require. We need to either ship them or document
explicit deferral.

### F1. Streaming via SSE (proposal §Streaming Support)

| Surface | Code evidence | Status |
|---|---|---|
| `/v1/price/tip/stream` per-tick | `price_tip_stream.go` driven off `PriceReader` | ✅ |
| `/v1/observations/stream` per-tick | `observations_stream.go` driven off `HistoryReader` | ✅ |
| `/v1/price/stream` Hub-driven (closed-bucket) | handler returns 503 — Hub field on `Server` is set from `Options.Hub`, but no caller in `cmd/` constructs a `streaming.Hub` and the aggregator never calls `Hub.Publish` | 🟡 — handler exists, producer missing |

### F2. Discord / Slack callback alerts (proposal §Incident Detection and Response)

| Channel | Code evidence | Status |
|---|---|---|
| Slack webhook | `configs/ansible/roles/prometheus/defaults/main.yml:57` `alertmanager_slack_webhook_url` | ✅ |
| Discord webhook | No matches in `configs/` or `deploy/` | ❌ — proposal explicitly mentions "discord/slack" |

Either drop Discord from the proposal in a corrections-register
update, or add it to the Alertmanager config as a route option.

### F3. Public status page (proposal §Incident Detection)

`status.ratesengine.net` — no `deploy/` artefacts, no DNS, no
status worker. **🟡 — L4.11 in launch-readiness-backlog.**

### F4. Self-hosted deployment templates (proposal §Self-Hosted Deployment)

| Asset | Code evidence | Status |
|---|---|---|
| docker-compose dev stack | `deploy/docker-compose/dev.yaml` | ✅ |
| Ansible roles (Patroni, Redis-Sentinel, HAProxy, Prometheus, Loki, archival-node) | `configs/ansible/roles/` | ✅ |
| Kubernetes / Helm | none — `deploy/k8s/` does not exist | ❌ — proposal mentions "huge kubernetes stack with Talos Linux" |
| systemd units | `deploy/systemd/` (indexer, aggregator, api, archive-completeness, verify-archive) | ✅ |

The proposal's Kubernetes / Talos references are infrastructure
narrative for our own deployment, not commitments to ship k8s
manifests. Treat as **non-blocking** — drop from scope or add a
post-launch line in the corrections register.

### F5. Multi-region deployment (proposal §Availability)

`docs/architecture/multi-region-topology.md` + ADR-0016 cover the
R1 (Hetzner) / R2 (AWS) / R3 (Vultr) plan. R1 is operational and
serves as integrity leader; R2/R3 deployment is gated on
post-launch capacity. **⏳ post-launch** for R2/R3; R1 alone meets
the 99.9 % requirement.

---

## G. Remaining launch-blocking work

Ordered roughly by criticality — items deeper down depend on items above.

### G1. Decide + close `change_24h_pct` (§C2) — ✅ **shipped**

Implemented as option (a). `internal/api/v1/assets_f2.go` populates
`detail.Change24hPct` from the trailing-24h closed-bucket pct delta
read by the `Change24hReader` wiring; the field is on
`AssetDetail` (server + `pkg/client`) + the OpenAPI Asset schema;
three regression tests pin happy path, "no comparison bucket"
absence, and "no current price" absence
(`internal/api/v1/assets_f2_test.go::Test{Change24hPct_*}`). Live on
r1 since at least rc.39.

### G2. Wire `streaming.Hub` end-to-end so `/v1/price/stream` actually serves (§A5, §F1) — ✅ **shipped**

`cmd/ratesengine-api/main.go::main` constructs `streaming.NewHub(0)`
(line 400) and passes it as `Options.Hub` (line 563);
`streampublish.New(hub, priceReader, cfg.API.Streaming.PollInterval, …)`
(line 602) drives the publisher goroutine for operator-configured
`api.streaming.pairs`. The 2026-05-10 review section A5 confirmed
live: `GET /v1/price/stream → 3 windows (300/3600/86400) emitted
within 6s ✓`. Live on r1.

### G3. Phase 2 USD-volume coverage (§A8 ⚠)

**The gap:** `tradeUSDVolume` returns NULL for on-chain DEX trades
whose quote is not in the operator's USD-pegged classic allow-list.
A user's "24h DEX volume on AQUA" is therefore zero today.

**Acceptance:** Per-trade FX-anchor multiplication path —
`internal/storage/timescale/trades.go::tradeUSDVolume` consults
the FX-anchor table at trade time, multiplies through XLM/USD or
chain-fiat to land a non-NULL `usd_volume` for any trade where an
FX path is available. Existing Phase 1 path remains the fast lane.

**Effort:** 2–3 days. Designed in the L2.2 scope notes.
**Owner:** `internal/storage/timescale/`, `internal/aggregate/orchestrator/`.

### G4. Public status page (§F3, L4.11)

**The gap:** No `status.ratesengine.net`. SEV playbook references
"status page updates" but there's nowhere to update.

**Acceptance:** `cstate` (or equivalent) deployed; `deploy/`
contains the config; the status page is reachable; DNS points at
it; `docs/operations/sev-playbook.md` references the live URL.

**Effort:** half-day infra + 1 hour DNS/runbook update.
**Owner:** infra; tracked at L4.11.

### G5. SEV-1 / SEV-2 dry-run (§D, L5.7)

**The gap:** Playbook + 62 runbooks + 2 drill scenarios under
`docs/operations/drills/scenarios/`, but no recorded drill
artefact. The Freighter SLA is met by structure today, by
operational verification on launch day.

**Acceptance:** Run one SEV-1 and one SEV-2 scenario to completion
in staging; commit the drill writeups as
`docs/operations/sev-drill-YYYY-MM-DD-<scenario>.md` (per the
template in `docs/operations/drills/_template.md`); time-to-detect
+ time-to-respond meet F3.5 / F3.6.

**Effort:** 1 day (two drills + writeups).
**Owner:** ops.

### G6. SLA proof report (§D, L5.7 sibling)

**The gap:** `docs/operations/sla-proof-template.md` is a
template; no `docs/operations/sla-proof-YYYY-MM-DD.md` file
exists. We claim p95 ≤ 200 ms but have no published evidence.

**Acceptance:** Run `test/load/scenarios/06-mixed-realistic.js`
against staging; produce
`docs/operations/sla-proof-2026-MM-DD.md` from the template;
attach k6 summary JSON; results pass thresholds; commit with
CHANGELOG entry.

**Effort:** half-day.
**Owner:** infra + `test/load/`.

### G7. Discord webhook in Alertmanager (§F2) — ✅ **shipped (code)**

Option (a). The Ansible role at
`configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
emits parallel `slack_configs` + `discord_configs` blocks on the
`chat-default` receiver when the corresponding webhook var is set
(`alertmanager_slack_webhook_url`, `alertmanager_discord_webhook_url`
— both default to empty, both degrade silently to "no fanout" when
unset). The R1 standalone config
(`configs/alertmanager/alertmanager.r1.yml`) is Slack-only by
default; mirroring Discord on R1 is a one-line addition to
`/etc/default/alertmanager-secrets` + a re-apply.

**Operator-side TODO** (not gating): provision a Discord webhook URL
and either set `alertmanager_discord_webhook_url` in the Ansible
inventory or add `DISCORD_WEBHOOK_URL=…` to
`/etc/default/alertmanager-secrets` on R1 + extend
`alertmanager.r1.yml` to fan out.

### G8. External security review (L5.6)

**The gap:** No external/community security review on file.

**Acceptance:** Engagement with a Stellar-ecosystem-aligned auditor
(or community review window) producing a written report; report
filed under `docs/security-reviews/<auditor>-YYYY-MM-DD.md`; any
findings either fixed or carve-outs accepted by the operator.

**Effort:** external; calendar-time 2–4 weeks.
**Owner:** external auditor.

### G9. Documentation sweep (L6.5)

**The gap:** Final pass before public-flip — every runbook
verified executable, every ADR reflects current code, every
config option in `configs/example.toml` documented.

**Acceptance:** A single `docs(launch): final doc sweep` PR that
either updates or annotates each of the 62 runbooks; ADR statuses
reflect current code; `configs/example.toml`'s every key has a
`doc:` tag matching the implementation; `make docs-all` clean.

**Effort:** 1 day.
**Owner:** docs; tracked at L6.5.

### G10. Production cutover (L6.4)

**The gap:** DNS still points at staging; rate-limit tier still
internal; v1.0 not tagged.

**Acceptance:** `git tag v1.0.0` ratified; public-repo flip done
per `docs/operations/public-flip.md`; DNS for `api.ratesengine.net`
+ `status.ratesengine.net` flipped; rate-limit middleware reads
production tier limits; first 24-h post-launch watch (L6.7) on
rotation.

**Effort:** 1 hour cutover + 24 h watch.
**Owner:** infra + on-call rota.

### G11. Customer sign-off demo (L6.6)

**The gap:** No documented demo with the Stellar / Freighter
customer.

**Acceptance:** Demo session held; sign-off email or document
filed; any outstanding feedback either resolved or scheduled
post-launch.

**Effort:** external; 2-hour session.
**Owner:** external.

### G12. [OPERATOR] Populate supply-pipeline watched-sets on r1 (R-006)

**The gap:** F2 supply fields (`circulating_supply`,
`total_supply`, `max_supply`, `market_cap_usd`, `fdv_usd`) are
NULL on every asset in production because r1's
`[supply].watched_classic_assets` and
`[supply].watched_sep41_assets` config lists are empty. The
indexer has nothing to watch, so the supply observer never
populates the per-class hypertables, so the F2 reader returns NULL.

**Surface evidence:** Live curl 2026-05-10 — every row of
`/v1/coins`, `/v1/assets/{id}`, `/v1/changes/coin/{id}` returns
`circulating_supply: null`, `market_cap_usd: null`,
`fdv_usd: null`. R-006 in
[`docs/review-2026-05-10.md`](review-2026-05-10.md).

**Acceptance:** Populate the watched-set arrays in
`/etc/ratesengine.toml` on r1 (minimum: USDC, USDT, EURC, AQUA,
yXLM, SHX, VELO, BTC, ETH, PYUSD, plus operator additions).
Restart the indexer. Re-curl `/v1/coins/USDC` and verify
non-null F2 fields.

**Effort:** 15 min config + restart + re-verify.
**Owner:** operator (Ash).

### G13. [OPERATOR] Configure SEP-10 server signing seed on r1 (R-009)

**The gap:** `/v1/auth/sep10/challenge?account=G…` returns
`503 SEP-10 not configured` on production because r1's
`[auth.sep10]` config is missing `server_signing_seed` +
`server_signing_public`. RFP §"Authentication" lists SEP-10 as
a supported auth mechanism alongside API keys.

**Surface evidence:** R-009 in
[`docs/review-2026-05-10.md`](review-2026-05-10.md).

**Acceptance:** Generate a Stellar keypair dedicated to the
SEP-10 server (NOT a validator key, NOT an issuer key). Set
both fields in r1 config. Restart `ratesengine-api`. Re-curl
`/v1/auth/sep10/challenge?account=G…` and verify 200 + a valid
challenge transaction XDR.

**Effort:** 30 min (keypair generation + config + restart +
SEP-10 verify-script run).
**Owner:** operator (Ash).

### G14. [OPERATOR] Resolve anonymous-tier rate-limit / Chainlink coverage gaps

Two operator-config items captured for visibility:

**Sub-item a — anonymous-tier ceiling (R-019).** Anonymous-tier
rate limit reads `60/min` against an RFP target ≥ 1000/min.
Either raise `[ratelimit].anonymous_rpm` or document that
1000/min is the authenticated-tier target (not anonymous).
Effort: 5 min decision + config.

**Sub-item b — Chainlink coverage (R-119).** r1 Chainlink feeds
are FX-only; no crypto divergence cross-checks happen. Either
configure additional Chainlink feeds for the watched crypto set,
or document that Chainlink contributes FX-only and CG/CMC are
the crypto divergence sources. Effort: 30 min.

**Acceptance:** Both sub-items decided and recorded in
[`docs/operations/r1-deployment-state.md`](operations/r1-deployment-state.md)
with the chosen disposition.

**Owner:** operator (Ash).

### G15. Multi-network assets migration (R-018)

**The gap:** `/v1/coins` and `/v1/assets` are two endpoints
describing the same Stellar-canonical asset with overlapping but
different field sets. Worse, no endpoint represents
"USDC as a cross-chain currency" — the explorer's `/assets/USDC`
returns Stellar USDC only, which conflates two genuinely
different concepts (multi-chain global USDC vs. Stellar-issued
Circle USDC).

**Plan:**
[`docs/architecture/multi-network-assets-migration.md`](architecture/multi-network-assets-migration.md)
captures the agreed design. Five-phase migration:

| Phase | What | Status |
|---|---|---|
| 1.1 | Verified-currency catalogue + unverified-asset warning | shipped 2026-05-11 — `internal/currency` + `internal/currency/data/seed.yaml` embedded + `/v1/assets/{id}.unverified_warning` + `flags.unverified_ticker_collision` (deviated from plan: seed lives embedded in the binary rather than `configs/`, matches `internal/incidents` pattern, no operator setup required) |
| 1.2 | CG + CMC connectors (catalogue-driven) | shipped 2026-05-11 — catalogue-derived CG ticker map + aggregator pair set + `Store.LatestAggregatorPricesForPair` reader. Reuses existing `oracle_updates` hypertable (no new migration). **Deferred to a later phase:** CG catalogue-augmentation worker (top-N market-cap refresh — separate trust surface, hand-curated seed suffices for v1). |
| 1.3 | Per-ticker VWAP worker + three-tier fallback chain (`vwap_native` → `aggregator_avg` → `triangulated`) | **1.3a shipped 2026-05-11** — `internal/aggregate/global.go::ComputeGlobalPrice` walks the three-tier chain over existing per-pair VWAP + Phase 1.2 aggregator reader + existing Redis triangulation. `external.AggregatorSources()` helper exposes the registered aggregator class to handlers. **1.3b deferred** — cross-chain ticker-bucketed VWAP CAGG (normalising Stellar SDEX USDC/XLM against CEX USDC/USD into one VWAP) needs per-trade FX-anchor multiplication; lands when (a) we ingest non-Stellar-chain trades, or (b) Phase 1.4 surfaces a price-quality gap the current per-pair tier-1 can't bridge. |
| 1.4 | `/v1/assets/{slug}` global view + drop `/v1/coins` | **1.4a shipped 2026-05-11** — `/v1/assets/{slug}` global view (catalogue identity + price block from ComputeGlobalPrice + `networks[]` with Stellar deep_links + non-Stellar external_links). `/v1/coins` + `/v1/coins/{slug}` mark themselves deprecated via the `Deprecation: true` + `Link: rel="successor-version"` headers; runtime behaviour unchanged so the explorer keeps working until Phase 1.5 lands. **1.4b deferred** — actual `/v1/coins` deletion waits for Phase 1.5's explorer migration. |
| 1.5 | Explorer migration | not started |

**Acceptance:** Phase 1.1 through 1.5 shipped; `/v1/coins`
removed; explorer renders `/assets/usdc` with multi-network
view + Stellar drill-down; unverified-asset warning fires on
ticker collision.

**Effort:** ~3 weeks of focused work across all five phases.
**Owner:** code; operator decides verified-catalogue
additions.

---

## H. Explicit deferrals (post-launch)

These are tracked at `docs/architecture/launch-readiness-backlog.md`
§"Post-launch" and ratified by operator decision 2026-04-28.

| ID | Item | Justification |
|---|---|---|
| L7.1 | DIA mainnet integration | DIA testnet-only at audit time; conditional on DIA shipping mainnet |
| L7.2 | 99.99 % uptime measurement | Needs ≥ 30 days production traffic; reported 90 days post-launch |
| L7.3 | ADR-0019 Phase 3 cross-oracle confidence factor | Requires `internal/divergence/` to be production-quality first |
| L7.4 | Tier-1 own-validator deployment (ADR-0004) | Multi-week catchup; not RFP-required |
| L7.5 | GraphQL surface alongside REST | Optional per RFP; defer until customer-driven |
| F4-k8s | Kubernetes / Helm manifests | Proposal narrative, not commitment; record in corrections register |
| F5-r2/3 | R2 / R3 multi-region rollout | Capacity / cost gated; R1 alone meets 99.9 % |

---

## I. Recommended order of execution

The remaining items in §G fall into three batches:

**Batch 1 — code (1 dev-week):**
- G1 `change_24h_pct` decision (half-day)
- G2 `streaming.Hub` end-to-end (1 day)
- G3 Phase 2 USD-volume (2–3 days)
- G7 Discord webhook (1 hour, can fold into G9)

**Batch 2 — operational verification (1 dev-week):**
- G4 status page (half-day)
- G5 SEV-1 + SEV-2 dry-runs (1 day)
- G6 SLA-proof report (half-day)
- G9 documentation sweep (1 day)

**Batch 3 — launch (calendar-time):**
- G8 external security review (calendar 2–4 weeks)
- G10 production cutover (1 hour + 24 h watch)
- G11 customer sign-off demo (external)

Batches 1 + 2 can run in parallel. Batch 3 starts when Batch 2
completes and the security review is in flight.

**Realistic launch window:** with the Batch 1 + 2 work done in
parallel over the next 2 weeks and the security review in flight,
production cutover lands mid-to-late May 2026 (vs the original
2026-06-30 plan, comfortably ahead).

The G12-G15 additions extend the original 11-item list:

- G12-G14 are operator-config items surfaced in the 2026-05-10
  production review; ~1 hour total operator time.
- G15 is the multi-network migration; ~3 weeks; has its own
  plan doc.

---

## J. Post-launch polish backlog

Items captured from the in-session task tracker that aren't
launch-blocking but should outlive the session. None of these
gate the public flip; each can ship as time allows.

| ID | Item | Origin |
|---|---|---|
| J1 | [OPERATOR] CF Pages project rename: `ratesengine-showcase` → `ratesengine-explorer` | UI cleanup; aliases work today |
| J2 | Site IA restructure follow-ups (explorer navigation polish) | UX polish |
| J3 | Lending pool detail pages — surface pair-level data instead of contract address only | Feature, post-MVP |
| J4 | DefIndex + Soroswap-router data on `/aggregators` page (in-progress at session close) | Feature |
| J5 | Re-backfill USD volume on Comet trades after the recent SAC-wrapper config addition | Data-quality cleanup |
| J6 | R-015 — `/v1/diagnostics/cursors` filter for `?status=active` or auto-cleanup of completed backfill cursors | Diagnostic polish |
| J7 | R-020 — pure-scam-token suppression beyond the volume-desc default sort on `/v1/markets` | Deferred per operator decision 2026-05-11 (scam warnings are visible; data still useful) |
| J8 | OpenAPI lint: fix 4 pre-existing `oas3-valid-media-example` errors (`/coins`, `/incidents`, `/network/stats`, `/signup`) so CI on every PR doesn't have a permanent red mark | **Shipped 2026-05-11** — spectral lint now 0 errors (84 warnings remain, non-blocking) |
| J9 | Refresh `internal/sources/soroswap/decode_test.go` fixture from a real capture (TODO in code) | Test debt |

These items will not appear in §H "Explicit deferrals" because
they aren't yet ratified post-launch — they're just not on the
critical path. Promote to §G if any becomes launch-blocking.

---

## How this differs from the existing tracking docs

- [`coverage-matrix.md`](architecture/coverage-matrix.md) maps every
  RFP bullet to ADR + delivery week + status. It's correct but
  trusts what the docs say. This doc trusts only the code.
- [`launch-readiness-backlog.md`](architecture/launch-readiness-backlog.md)
  is the active backlog. This doc complements it by surfacing
  three gaps the backlog hadn't fully captured:
  1. **C2 `change_24h_pct`** — OpenAPI/code drift not in the backlog.
  2. **G2 `streaming.Hub` producer wiring** — backlog flagged L3.9
     as ⚠ but didn't name the specific producer-side missing piece.
  3. **G3 Phase 2 USD volume** — backlog L2.2 is ⚠ but doesn't
     spell out the on-chain non-USD-pegged-quote case explicitly.
  4. **G7 Discord webhook** — proposal commitment not previously surfaced.

When the items in §G are done, update this doc by flipping each
header to ✅ and add a one-line PR reference, or supersede the
file with a "shipped" note pointing at the launch tag.

---

_Last code audit: 2026-05-02, against branch `account-self-service`
at HEAD `20fafa2`. Re-walk before production cutover._
