# Changelog

All notable changes to Rates Engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to dual versioning — SemVer for `pkg/*`
and CalVer (`YYYY.MM.DD`) for binary releases. See
[docs/discovery/repo-structure-plan.md §10](docs/discovery/repo-structure-plan.md)
for the rationale.

Every release lists the Stellar protocol version it was tested
against.

---

## [Unreleased]

### Added

- **L3.9 PR 2 of 2: API-side closed-bucket stream subscriber.**
  Closes the L3.9 fan-out end-to-end. New
  `redispub.Subscriber` listens on the same Redis channel the
  aggregator's Publisher writes to (PR 1 of L3.9), decodes each
  `ClosedBucketEvent`, and republishes on the API binary's
  in-process `streaming.Hub` with the canonical
  `closed:<asset>/<quote>` topic key (matches
  `internal/api/v1.PriceStreamTopic`). `cmd/ratesengine-api/main.go`
  constructs a Hub when Redis is available and runs the
  subscriber as a goroutine bound to the root context.
  - New metric
    `ratesengine_api_stream_subscribe_total{outcome="ok"|"decode_error"|"malformed"}`.
  - New tests: nil-input rejection; round-trip via miniredis
    that proves Hub.Publish fires with the correct topic and
    forwarded payload; sentinel test asserts the topic format
    stays in sync with `v1.PriceStreamTopic`.
  - L3.9 in launch-readiness-backlog flipped ⚠ → ✅; the
    documented caveat ("aggregator-side `Hub.Publish` is the
    missing piece") is closed.
- **L3.9 PR 1 of 2: aggregator-side closed-bucket stream
  publisher**. New `orchestrator.StreamPublisher` interface
  declared on `orchestrator.Config`; called once per
  successful (pair, window) VWAP cache write with the freshly-
  computed value + bucket-end timestamp. Best-effort:
  publish errors log + increment
  `ratesengine_aggregator_stream_publish_total{outcome="error"}`
  but never block the tick (the VWAP cache key is the
  source of truth; the stream is enrichment for SSE
  subscribers).
  - Production implementation: new package
    `internal/api/streaming/redispub/` with `Publisher`
    (Redis `PUBLISH` to `ratesengine:closed-bucket:v1`) +
    `ClosedBucketEvent` JSON wire shape.
  - Wired in `cmd/ratesengine-aggregator/main.go` —
    PUBLISH on a no-subscriber channel is a Redis no-op,
    so wiring is safe ahead of the matching API-side
    subscriber.
  - PR 2 of L3.9 will add the API-binary subscriber that
    republishes each event on the in-process
    `streaming.Hub` so `/v1/price/stream` SSE clients
    receive the fan-out.
- **`change_24h_pct` populated on `/v1/assets/{id}`** — the field
  was declared in OpenAPI (Freighter RFP §"Bulk query support"
  mentions a 24h % change alongside current price) but no Go code
  computed it. Closed: `internal/storage/timescale/aggregates.go`
  gains `ClosedVWAP1mAtOrBefore` to anchor the 24h-ago comparison
  price; new `Change24hReader` interface + `populateChange24h`
  helper in `internal/api/v1/assets_f2.go` consult the current
  USD price + 24h-ago anchor and stamp a signed two-decimal
  percentage (e.g. `"+1.27"`, `"-0.05"`, `"0.00"`). The leading
  `+` is suppressed on a sub-cent positive delta that rounds to
  `"0.00"` so the wire signal stays unambiguous. Null when no
  current USD price exists for the asset or the 24h-ago bucket
  is unavailable (asset first traded < 24h ago, or pruned by
  retention). `pkg/client/types.go::AssetDetail` gains the field;
  `cmd/ratesengine-api/main.go` constructs `storeChange24hReader`
  and wires it via `Options.Change24h`.
- **`/v1/price/stream` now serves closed-bucket events end-to-end**
  — the handler returned 503 unconditionally because the API
  binary never constructed a `streaming.Hub`, and no producer
  ever called `Hub.Publish`. Closed: `cmd/ratesengine-api/main.go`
  unconditionally constructs `streaming.NewHub(0)` and passes it
  via `Options.Hub`; new `internal/api/streampublish` package
  hosts a per-pair polling producer that watches the existing
  `PriceReader` (same path `/v1/price` consumes) and fans out to
  the Hub on every `ObservedAt` advance. Operators declare which
  pairs broadcast via the new `[api.streaming]` config block:
  `pairs = [["native","fiat:USD"], …]`. Empty `pairs` leaves the
  producer disabled but still constructs the Hub so subscribers
  connect cleanly (heartbeats only). New
  `ratesengine_stream_publish_total{stream="price_stream"}`
  counter signals fanout activity. The byte-identical-payload
  property required by ADR-0015 is verified by
  `TestPublisher_TwoSubscribersIdenticalPayload`.
- **L2.2 Phase 2 plumbing — `USDVolumeFXResolver` interface +
  `tradeUSDVolume` fallback path** — closes the launch-task-list
  G3 plumbing half. The current Phase 1 path stamps `usd_volume`
  for off-chain CEX/FX trades + on-chain DEX trades whose quote
  is on the operator's USD-pegged classics allow-list, leaving
  every other on-chain trade NULL. New
  `USDVolumeFXResolver.USDPriceAt(ctx, asset, t)` lets a
  deployment supply a USD rate per quote asset; when wired,
  `tradeUSDVolume` falls through to it after Phase 1 declines
  and multiplies through `quote_amount × rate / 10^classicDecimals`
  to land a non-NULL `usd_volume`. `Store.SetUSDVolumeFXResolver`
  installs it; nil (the default) preserves Phase 1 behaviour
  exactly. Production resolver — a goroutine that polls
  `prices_1m` for `<asset>/<USD>` per configured asset and
  caches the latest closed VWAP — ships in a follow-up PR; this
  PR is the contract + test surface so the wiring lands cleanly.
- **`pkg/client.Client.History`** — bounded-range raw-trade lookup
  via the SDK. Distinct from the existing
  `Client.HistorySinceInception` (which returns bucketed VWAP/TWAP
  points); this surface returns the underlying `TradeRow`
  records — useful for trade-level audits, regulatory exports,
  custom aggregations the server doesn't pre-compute. New
  `HistoryRangeQuery` with optional `From`/`To`/`Limit`/`Cursor`;
  `Cursor` walks forward by re-issuing with the previous
  response's `Pagination.Next`. New `TradeRow` type in
  `pkg/client/types.go` mirrors the server's wire shape exactly.
- **`pkg/client.Client.OHLC`** — single-bar OHLC over a window via
  the SDK. Closes another gap from the code-vs-RFP audit:
  Freighter RFP §V1 historical chart requirements explicitly list
  OHLC as a chart-UX path but the SDK only exposed
  `HistorySinceInception`. Both `Base` and `Quote` are required
  on `OHLCQuery` (the server doesn't default Quote to fiat:USD —
  candlestick charts pin a specific pair). `From`/`To` are
  optional with the same closed-bucket-clamp semantics the server
  applies to a defaulted `to` per ADR-0015. Wire shape mirrors
  the server's `OHLCBar` exactly, including the `Truncated` flag
  consumers building chart UIs need to detect when a window has
  more trades than the server's per-request cap.
- **`pkg/client.Client.PriceTip`** — live rolling-window VWAP via
  the SDK. Sibling to `Client.Price` for "freshest possible
  signal" use cases per ADR-0018. Same input shape as `PriceQuery`
  with an additional `WindowSeconds` (server clamps to [1, 60],
  defaults to 5). Caller distinguishes the two in-contract
  response branches via `PriceSnapshot.PriceType`: `"vwap"` for
  the rolling-window VWAP, `"last_trade"` for the empty-window
  fallback. SDK omits `window_seconds=0` from the URL so the
  default-of-5 path stays clean.
- **`pkg/client.Client.PriceBatch`** — bulk price lookup via the
  Go SDK. Closes the most impactful gap from a code-vs-RFP audit
  of the SDK surface: Freighter RFP §"Bulk query support
  preferred (batch asset lookups)" was implemented server-side
  (`GET`/`POST /v1/price/batch`) but the SDK only exposed the
  single-asset `Client.Price`. SDK now routes ≤100 ids via GET
  and >100 via POST automatically (the threshold below which the
  query string fits within typical 8 KiB header limits), validates
  ≤1000 client-side to match the server cap, and returns the
  same `Envelope[[]PriceSnapshot]` shape with OR-over-rows flags.
  Splitting beyond 1000 is deliberately the caller's choice —
  silently chunking would mask `flags.stale` semantics on
  subsets the caller wouldn't see.
- **`runbooks/dr-activation.md` — disaster-recovery activation
  procedure** — closes the missing runbook the SEV playbook §8.3
  (annual DR exercise), `timescale-primary-down.md` §D
  ("complete cluster loss"), and ADR-0008 / ADR-0016 all
  referenced. Previously the only pointer was `TODO(#0)` in
  `timescale-primary-down.md`. Covers when to activate (decision
  tree distinguishing it from per-component HA failover),
  pre-flight checks (DR storage freshness, MinIO archive
  integrity, host reachability), the Cloudflare-LB and manual-
  DNS flip procedures, post-flip monitoring (SLA + ingest +
  flag rates), failback to primary, escalation, and quarterly
  drift signals operators run between drills. SEV playbook §8.3
  + the timescale runbook updated to link the new file.
- **Two new SEV drill scenarios** — `sev2-redis-sentinel-failover`
  exercises ADR-0024's Sentinel HA path end-to-end across every
  Redis-dependent surface (`/v1/price` cache + freeze markers +
  confidence + triangulation + API-key validator + SEP-1 cache);
  pinned validation criteria include "did oncall correctly
  classify SEV-2 (degraded) not SEV-1 (down)" and "did anyone
  fail back contrary to ADR-0024's fail-forward rule" — both
  common simulation mis-steps. `sev1-anomaly-freeze-stuck`
  exercises the ADR-0019 anomaly chain (Phase 1 thresholds →
  Phase 2 baseline → freeze.Writer → /v1/price's flags.frozen);
  drills the operator-driven-clear contract that ADR-0019
  Phase 1 explicitly chose over auto-clear, plus the verify-
  before-clearing discipline that prevents re-freeze loops.
  Drills README updated to list all four scenarios with their
  category coverage (storage / cache / ingest / aggregator).
  Closes G5 in `docs/launch-task-list.md` for the script-
  authoring half; actual drill execution + writeups remain
  operator work against staging.
- **Status-page scaffold + `sev-status-page-update` runbook** —
  `status.ratesengine.net` was committed to in the proposal §IDR
  and required by Freighter F3.5 / F3.6, but nothing in `deploy/`
  pointed at the page or specified what an update should look
  like. New `deploy/status-page/cstate/` ships the cstate
  (Hugo-based) site config, the public component list (12
  customer-facing service surfaces matching the API + ingest +
  backend layers), and the per-incident front-matter template.
  New `docs/operations/runbooks/sev-status-page-update.md`
  binds the update cadence (hourly during SEV-1, daily during
  SEV-2 — matches the SEV-playbook + Freighter SLA), the
  safe-to-publish detail level, and the workstation-down
  fallback path. `docs/operations/sev-playbook.md` §5.1 now
  references both rather than dangling a TBD. Hosting target
  (Cloudflare Pages recommended) + DNS cutover remain operator
  work — see [`deploy/status-page/README.md`](../deploy/status-page/README.md).
  Closes G4 in `docs/launch-task-list.md`.
- **AlertManager Discord webhook (parallel fanout with Slack)** —
  the proposal commits to alerts being "integrated into
  discord/slack" but the Prometheus ansible role only wired
  Slack. New `alertmanager_discord_webhook_url` vault var; the
  warning + info routes now point at a unified `chat-fanout`
  receiver that emits to BOTH Slack and Discord when their
  respective webhook URLs are set, either alone, or neither
  (alerts accumulate in the AM UI in the last case). Preflight
  warns when both URLs are empty rather than silently letting
  alerts fall on the floor. Closes G7 in
  `docs/launch-task-list.md`.
### Documentation

- **Public-flip 24-hour pre-cutover dry-run (closes L6.3 / Task #78)** —
  `docs/operations/public-flip.md` gains a §"Final 24-hour
  pre-cutover dry-run" capturing the gates that must re-run in
  the 24 h immediately before tagging v1.0: gitleaks rerun,
  file-level scrub recheck, `make test && make test-integration`
  on the v1.0 SHA, doc-rot spot-check on `last_verified` dates,
  CI-green-within-24h check, and external-asset readiness
  (SECURITY mailbox monitored, CODEOWNERS bandwidth, GitHub
  repo name still un-claimed). The pre-flip checklist itself is
  already `☑` × 16 — this addition closes the "what about the
  PRs that landed between standing-checklist verification and
  launch day" gap. L6.3 status flipped 🟢 → ✅.

- **SLA proof procedure (Task #77 operator-recipe)** — new
  `docs/operations/sla-proof-procedure.md` documents the
  end-to-end recipe that turns a `make test-load-mixed` run into
  the checked-in `docs/operations/sla-proof-<YYYY-MM-DD>.md`
  proof artefact: pre-flight checklist, run command, Grafana
  snapshot capture, Promql baseline reads against the soak
  window, monthly cadence, and the documented-acceptance
  fallback if staging access is delayed. The existing template
  at `sla-proof-template.md` is the report skeleton; this
  procedure is the operator's how-to. Closes the "no operator
  recipe to produce the proof report" gap that left Task #77
  without a clear path-to-done even though all upstream
  scenarios (L5.1-L5.3) had already shipped.

- **SEV-1 / SEV-2 dry-run records (closes L5.7 / Task #76)** —
  Two new tabletop drill writeups under `docs/operations/drills/`
  exercise the SEV playbook end-to-end against the existing
  scripted scenarios:
  - `2026-04-sev1-timescale-failover.md` — Timescale primary
    out-of-disk simulation; chose fix-in-place via
    `drop_chunks('prices_1m', '30 days')` plus restart;
    validated all 8 scenario criteria, 7 pass + 1 partial.
  - `2026-04-sev2-soroswap-decode-regression.md` — protocol-25
    SCVal type-tag enum extension breaks soroswap decoder;
    forward-fix path via `internal/scval` + golden fixture
    + ordinary deploy + `ratesengine-ops backfill -source`;
    validated all 8 scenario criteria, all pass.
  - Promoted two action items into runbook updates in the same
    PR: `timescale-primary-down.md` Quick-diagnosis now leads
    with `/v1/readyz` (shaves ~1 min off detection); `decode-errors.md`
    Mitigation gains a customer-comms note for the
    `class_drop_spike` ↔ `flags.divergence_warning` correlation.
  - Solo-drill caveats called out explicitly — a 3-person tabletop
    is queued for post-launch with the next on-call hire.

- **WASM-audit v2 fill-in across all eight Soroban sources** —
  every per-source audit doc under `docs/operations/wasm-audits/`
  now folds in the 2026-04-30 r1 wide-net walk's per-instance
  evidence (540 contracts / 52 unique WASMs SHA-256-verified +
  bytes-preserved on r1). Notable changes:
  - **Comet's v2 audit folded into Blend's** — the only mainnet
    Comet pool is Blend's Backstop V2 (`CAQQR5SW…` →
    `c1f4502a…`). `comet.md` now redirects to `blend.md` for the
    per-instance hash inventory; `blend.md` documents both source
    rows symmetrically. Comet (the protocol) is a Balancer-v1-style
    AMM library used by Blend's backstop module — not an actively-
    maintained standalone DEX.
  - **Aquarius gained Cohort A / Cohort B sections** — 168 never-
    upgraded pools (3 WASMs) plus 145 upgraded pools across a
    5-WASM upgrade chain (`b54ba37b → 2d770946 → 7cecf23b →
    a1629dcd → 4f080d24`). Closes the "doc incomplete, not wrong"
    gap flagged in the 2026-05-01 cross-source review.
  - **Soroswap gained per-instance Phase 2 results** — 196
    contracts (1 factory + 1 router + 194 pair instances), three
    unique WASMs total, zero mid-life upgrades observed.
  - **Phoenix gained per-instance Phase 2 results** — 13
    contracts on 22 WASMs (5 factory + 3 multihop + 14 pool); the
    most-iterated source. All 14 pool WASMs binary-confirmed to
    contain the eight swap-field strings (`actual received amount`
    spelling preserved across the chain).
  - **Reflector / Redstone / Band** confirmation notes added
    pinning the v2 walk's findings; no decoder-relevant changes.
  - All `last_verified` dates bumped to 2026-05-03.

### Fixed

- **`internal/canonical/discovery/doc.go` "Future work" list has
  shipped** — the package's `# Future work (separate PRs):` block
  named three items, all of which have landed:
  - Dispatcher integration → `internal/dispatcher/dispatcher.go`
    calls `discovery.Sniff` on every event after decoder
    dispatch.
  - Postgres-backed Recorder → `internal/storage/timescale/
    discovery.go` implements `Recorder` against the
    `discovered_assets` hypertable.
  - Ops command + alert metric → `ratesengine-ops discovery`
    subcommand exists; `ratesengine_ingestion_discovery_drops`
    alert lives in `deploy/monitoring/rules/ingestion.yml`.
  Section renamed to "Wired today" with concrete file pointers.
  Same drift family as #477 / #483 / #484. Continuation of the
  L6.5 doc-sweep.
- **`internal/api/v1/doc.go` no longer says auth is "future"** —
  the package-level "What this package doesn't do" list said
  `No auth logic — [middleware.APIKey] (future) handles that.`
  Two stalenesses: (a) the auth middleware ships today at
  `internal/api/v1/middleware/auth.go` (Auth, not APIKey), and
  (b) it's a unified middleware that handles both API-key and
  SEP-10 modes via the validator interfaces in `internal/auth`.
  Rewritten to point at the live middleware + concrete
  validators (`auth.RedisAPIKeyValidator`, `sep10.Validator`).
  Same drift family as #477 / #482. Continuation of the L6.5
  doc-sweep.
- **`sep1-resolution.md` operator-override section described a
  fictional schema** — the §"Adding a curated home-domain
  override" subsection showed a `config/asset_metadata_overrides.yaml`
  file with per-asset `name` / `desc` / `image` / `max_supply`
  overrides plus a `sep1_status: operator_override` wire status.
  None of that exists. The actual override is much narrower:
  `[metadata.issuer_home_domains]` in `/etc/ratesengine.toml`
  maps issuer G-strkey → home-domain so the SEP-1 resolver can
  fetch the issuer's stellar.toml; per-field metadata comes from
  that toml, not an override. The `operator_override` status
  string is also fictional (no such status code in the codebase).
  Section rewritten to describe the real `MetadataConfig` shape
  + the real reload story (config is parsed at boot, not hot-
  reloaded). Continuation of the L6.5 doc-sweep.
- **`sep1-resolution.md` no longer hand-waves a `sep1-trace`
  subcommand as "Phase 5 deliverable"** — same drift as #481
  (UsageRow). The doc said `ratesengine-ops sep1-trace -domain
  <home_domain> (Phase 5 deliverable; not yet implemented)
  would dump the full resolution path…`. We don't track
  follow-up work as "Phase 5" anymore; the comment now
  describes the gap concretely (`not in
  cmd/ratesengine-ops/main.go's switch today`) and points the
  operator at the manual playbook. Continuation of the L6.5
  doc-sweep.
- **`oracle-manipulation-defense.md` red-team-tests no longer
  hand-waves divergence as `(when shipped)`** — §"Validation
  exercises" red-team-test 1 said `Divergence monitoring (when
  shipped) flags it`. Divergence monitoring HAS shipped (per
  `internal/divergence/{compare,worker,coingecko,chainlink}.go`
  + the orchestrator's `DivergenceRefresher` Tick wiring).
  Updated to describe the live behaviour: `flags.divergence_warning`
  flips on the affected pair via the `div:<asset>` Redis key
  the divergence service writes, and the `/v1/price` handler
  surfaces it. Same drift family as #483 / #484. Continuation
  of the L6.5 doc-sweep.
- **`api-design.md` no longer reads as a Week-4-pending design
  doc** — frontmatter said `status: draft — ratified at Week 4
  design review` and the body had `**Ratification target:** end
  of Week 4.` We're well past Week 4; the OpenAPI file ships as
  the binding contract today and 32+ handlers are wired against
  it. Frontmatter flipped to `ratified` with the right pointer
  ("openapi/rates-engine.v1.yaml is the binding contract; this
  doc records design intent"). §15 "Open questions (close by
  Week 4)" rewritten as a closure list — GraphQL→L7.5,
  SSE-not-WebSocket (shipped), proxy-not-rehost issuer images
  (the metadata package does this), no Webhook callbacks, no
  gRPC. Also fixed a stale `lint-docs.sh §11` citation in §16
  (the OpenAPI ↔ handler invariant is §2). Same drift family as
  #466 / #467 (Week-N frontmatter de-staling). Continuation of
  the L6.5 doc-sweep.
- **`internal/divergence/doc.go` describes the wired-today
  scope, not the original `PR A` slice** — the package's `# Scope`
  section was framed as `PR A (this package as initially shipped)
  ...` plus a `Subsequent PRs add more references` list naming
  CoinMarketCap, Reflector, Band, Redstone, Chainlink. Reality:
  Chainlink shipped (`ChainlinkReference`); the others either
  (a) don't belong in this package because they ingest as on-
  chain *sources* not divergence references (Reflector, Band,
  Redstone — they contribute to the underlying VWAP itself),
  or (b) are deferred behind operator demand (CoinMarketCap).
  The "PR B/C will add confidence-weighted aggregation" line
  also stale — `[ServiceOptions.MinSourcesForWarning]` does the
  trust-floor job today via the `[divergence].min_sources_for_warning`
  config knob. Section rewritten around what's wired now (Compare,
  Service, CoinGecko, Chainlink) and a one-paragraph note on
  why on-chain oracles aren't here. Continuation of the L6.5
  doc-sweep.
- **`internal/aggregate/doc.go` no longer claims triangulation
  is deferred** — the package's "What this package deliberately
  doesn't do" section listed `No multi-venue weighting /
  triangulation. Those are deferred items captured in
  docs/architecture/aggregation-plan.md.` But triangulation
  ships in this package — `triangulate.go` defines `Triangulate`
  and `TriangulateChain` (X2.5 forex-snap rule for chained-fiat,
  per F-0014), and the aggregator orchestrator wires it via the
  `Triangulations` field. New `# Triangulation` heading
  documents what's there; the "deliberately doesn't do" list
  retains the still-deferred multi-venue weighting (per-source
  weight overrides). Continuation of the L6.5 doc-sweep.
- **`auth.ErrNotImplemented` doc comment no longer claims the
  sentinel goes away once the validator body lands** — said
  `Removed once the body implementation lands`, but the SEP-10
  body has shipped at `internal/auth/sep10/` and the apikey body
  shipped via `RedisAPIKeyValidator` (#196). The sentinel
  **stays** because it serves the `NoopAPIKeyValidator` /
  `NoopSEP10Validator` fallbacks — the deliberate disabled-state
  the middleware lands on when an auth-mode is configured but
  no real validator is wired (e.g. `auth_mode=apikey` selected
  but Redis unavailable). Comment rewritten to describe the
  actual role: fail-loud 503 from the disabled-state fallback,
  not a placeholder awaiting replacement. Same drift family as
  #477 / #481. Continuation of the L6.5 doc-sweep.
- **`UsageRow` godoc no longer hand-waves "Phase 5 follow-up"** —
  the wire-shape comment said the `/v1/account/usage` counter
  store does not yet exist as a "Phase 5 follow-up." More accurate:
  the rate-limit middleware records per-key request counts in
  Redis today; the missing piece is a rollup writer that
  aggregates those into daily UsageRows. Comment now describes
  what's there and what's missing in concrete terms (rather than
  pointing at a phase label that's not how follow-up work is
  tracked anymore). Continuation of the L6.5 doc-sweep.
- **`aggregation-plan.md` API-surface table is internally
  consistent** — the `GET /v1/twap` row claimed `Backed by:
  Redis cache` while the same row's parenthetical said
  `TWAP-via-orchestrator path is TBD`. Both can't be true; the
  handler at `internal/api/v1/twap.go` runs `aggregate.TWAP`
  against the trades hypertable on every request — there is no
  TWAP cache. Row updated to `Trades hypertable (on-query)` and
  the Deferred section grew an explicit `TWAP-via-orchestrator
  pre-compute` entry so the parenthetical "see Deferred" cites
  something real. Continuation of the L6.5 doc-sweep.
- **ADR-0019 Phase 2 godocs no longer claim the phase is
  unbuilt** — `internal/aggregate/anomaly/doc.go` framed Phase 2
  as "planned per ADR-0019 §Phase 2" and Phase 1 as "the
  safety-net we ship before Phase 2 lands so the API has SOME
  anomaly protection during the gap." Phase 2 has shipped:
  `internal/aggregate/baseline/` (per-asset MAD baselines +
  z-score), `internal/aggregate/confidence/` (six-factor
  weighted-geomean confidence). Both layers run in parallel; the
  orchestrator's AND-of-three-signals rule
  ([Phase2FreezeConfig]) only fires ActionFreeze when Phase 1
  flags a class-level breach **and** Phase 2 confirms statistical
  anomaly + low confidence + low corroboration.
  `internal/config/config.go`'s `AnomalyConfig` description and
  the field-level `Anomaly` doc-tag carried the same "Phase 2
  will replace this" framing — both rewritten to describe the
  actual parallel scheme. Continuation of the L6.5 doc-sweep.
- **`internal/auth/sep10.go` and `internal/aggregate/orchestrator`
  godocs match shipped reality** — the SEP-10 interface declared
  `Production implementation lands in Phase 5; current
  [NoopSEP10Validator] returns [ErrNotImplemented] from every
  method`. The real implementation has shipped at
  `internal/auth/sep10/` (Validator, Challenge, Verify,
  VerifyJWT) and is wired in `cmd/ratesengine-api/main.go`'s
  `buildSEP10Validator`; Noop is now correctly described as the
  fallback for non-`auth_mode=sep10` deployments. The aggregator
  orchestrator's "Deliberately out of scope for v1" list claimed
  stablecoin→fiat proxy, triangulation, divergence, and outlier
  filtering were all still pending — every one has shipped (the
  ratesengine-aggregator binary wires each one through
  `orchestrator.Config` fields). Both godocs rewritten to
  describe what's actually wired today, with pointers at the
  packages doing the work. Same drift family as #475 / #476.
  Continuation of the L6.5 doc-sweep.
- **`ratesengine-api` and `ratesengine-aggregator` package
  docstrings match what each binary actually wires today** —
  the api binary's godoc said "Today: /v1/healthz, /v1/readyz,
  /v1/version — the infra-facing surface. The full endpoint
  catalogue ... lands in follow-up PRs." Reality: 32+ handlers
  registered (full pricing, historical, catalogue, oracle, account
  self-service, SEP-10, SSE streams). The aggregator binary's
  godoc had a "Deferred to follow-up PRs" list that already
  shipped: triangulation worker (X2.5 forex-snap, F-0014),
  divergence detector (Tick-driven RefreshPair), outlier filter
  (`OutlierSigmaThreshold`), and the multi-factor confidence +
  ADR-0019 anomaly-response pipeline. Both godocs now describe
  the actual wired surface and point at the canonical source
  block (`server.go` HandleFunc list, `orchestrator.Config`
  fields). Same drift family as #475 (ops binary). Continuation
  of the L6.5 doc-sweep.
- **`ratesengine-ops` package docstring matches the actual
  subcommand set** — the binary's `// Binary ratesengine-ops`
  godoc said "admin CLI: backfill, gap-detect, cache-prime,
  docs-config" with the closing line "Today only `docs-config`
  is wired; the rest land with the corresponding implementation
  PRs." Reality (per `cmd/ratesengine-ops/main.go`'s
  `switch args[0]` block): 18+ subcommands wired across ingest /
  archive integrity / Soroban discovery / supply / diagnostics /
  doc generation. The docstring also called the gap-detection
  subcommand `gap-detect` but the actual name is `detect-gaps`,
  and `cache-prime` was never built. Rewritten to enumerate the
  real buckets with the canonical names; closing line now points
  readers at the switch block + `--help` as the source of truth.
  Continuation of the L6.5 doc-sweep.
- **`CONTRIBUTING.md` and `repo-hygiene-plan.md` source-connector
  five-file convention now matches reality** — both docs listed
  the fourth canonical file as `factory.go (on-chain) or
  consumer.go (off-chain)`, but no `factory.go` exists anywhere
  in `internal/sources/` (verified with `find internal/sources
  -name factory.go`). The on-chain shape uses `consumer.go` plus
  source-specific extras like `dispatcher_adapter.go` and
  `factory_seed.go` (Soroswap / Aquarius factory-deploys-pair
  contracts). The CEX shape sometimes splits `consumer.go` into
  `streamer.go` + `backfill.go` (binance). Both docs now name
  `consumer.go` as the canonical fourth file (matching CLAUDE.md
  §"Add a new CEX connector") and mention the per-shape extras.
  Continuation of the L6.5 doc-sweep.
- **`README.md` no longer claims a non-existent Stellar
  protocol** — the `**Tested against:** Stellar protocol 25.x`
  line at the top of the README pointed at a network protocol
  that doesn't exist (the only "protocol 25" in the repo is in a
  hypothetical SEV-2 drill scenario explicitly marked
  `(hypothetical)`). Real protocol per CLAUDE.md +
  contract-schema-evolution.md + semver-policy.md is **23**
  (Whisk, mainnet 2025-09-03, CAP-67 unified events). README
  now matches. Also fixed README's repo-layout block: `cmd/`
  list missing `sla-probe`; `deploy/` description had stale
  "k8s / baremetal" instead of the actual
  docker-compose/systemd/monitoring/status-page subdirs;
  `configs/` description tightened to call out the ansible
  shape. Same drift family as #470 (CLAUDE.md tree). L6.5
  doc-sweep continuation.
- **`lint-docs.sh` no longer exempts `/v1/price/stream` from the
  "spec ↔ handler" check** — the planned_regex allow-list was
  scoped to "documented but not yet shipped" routes; the only
  entry was `/price/stream`, but the handler has been registered
  in `internal/api/v1/server.go:354` since before launch
  readiness began. Cross-checked: every OpenAPI path has a
  handler and every handler is in OpenAPI today, so the
  allow-list is empty. Tightened to `'^$'` (matches nothing)
  with a comment on what to do if a future doc-but-stub endpoint
  lands. Closes a small drift in CI strictness. L6.5 doc-sweep
  continuation.
- **`AGENTS.md` and `CLAUDE.md` quick-reference make-targets are
  accurate** — `AGENTS.md` claimed `make lint` runs "gofumpt +
  golangci-lint + archlint"; the actual `lint` target only runs
  golangci-lint (gofumpt is a golangci formatter), and the
  architectural import-boundary check is the separate
  `lint-imports` target. `make verify` was missing from
  `CLAUDE.md`'s build-and-test quick-reference even though
  `verify.sh` is the canonical pre-push gate (fmt+vet+lint+
  docs+test); operators reading just the top quick-reference
  would miss it. Both files now describe `make verify` with the
  same definition the Makefile uses, and the docs-all line on
  both files mentions metric Name: regen alongside OpenAPI +
  struct tags. Continuation of the L6.5 doc-sweep.
- **`CLAUDE.md` repo-tree is now accurate** — the orientation
  file every AI agent reads cold claimed `cmd/ binary entry
  points (four in total)` while listing 5 entries; reality is 6
  (the `ratesengine-sla-probe` binary that ships the SLA-evidence
  harness was missing). The `internal/` enumeration was missing
  five packages: `archivecompleteness` (the dual-archive
  daemon — ADR-0017), `events` (transport-neutral Soroban event
  types), `hashdb` (drift-detector against upstream LCM
  rewrites), `pipeline` (shared ingest glue between indexer +
  `ratesengine-ops backfill`), and `scval` (SCVal primitives
  wrapper). The `deploy/` description claimed "k8s / baremetal
  kits" but the actual subdirs are
  `docker-compose / monitoring / status-page / systemd` (no
  `deploy/k8s/`, per ADR-0008's bare-metal commitment). `configs/`
  description tightened to call out the ansible
  `roles/inventory/playbooks/` shape; `test/` description
  expanded to mention the `load` (k6) and `chaos` trees;
  `docs/audit-2026-04-29/` added to the tree (it's the
  post-Phase-1 cross-cutting findings register that several
  open PRs reference). Continuation of the L6.5 doc-sweep.
- **`repo-hygiene-plan.md` §15 IaC discipline now describes our
  actual stack** — the section listed Kubernetes manifests in
  `deploy/k8s/`, Helm charts, and "no inline shell heredocs in
  manifests" as the IaC discipline, but ADR-0008 ratifies bare
  metal + systemd + Ansible (no Kubernetes anywhere). Section
  rewritten around `configs/ansible/roles/<name>/`, the actual
  systemd units in `deploy/systemd/` (api / indexer / aggregator
  + the four timer/oneshot pairs for archive-completeness,
  sla-probe, supply-snapshot, verify-archive-tier-a), and
  `deploy/docker-compose/` as the dev-only reference stack.
  Continuation of the L6.5 doc-sweep.
- **`coverage-matrix.md` and `repo-hygiene-plan.md` no longer
  point at Week-N plan items that have either landed elsewhere
  or were never built** — the coverage matrix's "deferred to
  Week 9" / "planned (Weeks 8–9)" lines now cite the actual k6
  suite at `test/load/`, the operator-driven backfill via
  `ratesengine-ops backfill`, and the bare-metal+systemd+ansible
  deployment kit (the matrix had been promising `deploy/k8s`
  which doesn't exist and isn't our deployment shape). The
  hygiene plan's `scripts/ci/check-adr-numbering.sh` and
  `scripts/ci/lint-layout.sh` references are now accurate:
  ADR status integrity is enforced by `lint-docs.sh §8`,
  numbering-gap is reviewer-policed (no dedicated script yet);
  the architectural import-boundary check is in
  `lint-imports.sh`. The protocol-boundary fixtures section now
  describes the actual `test/fixtures/<source>/` layout instead
  of the original `test/fixtures/protocol-boundary/{pre,post}-pNN/`
  tree that never landed. Continuation of the L6.5 doc-sweep.
- **Architecture-doc frontmatter no longer pretends the launch
  plan is mid-flight** — `ha-plan.md`, `multi-region-topology.md`,
  `archival-node-spec.md`, `hosting-options.md`, and
  `validator-rollout.md` each declared themselves `draft —
  ratified at Week 2 …` or `decision at Week 1 procurement
  call`. We are well past those weeks; the plan executed (ADR-0008
  ratifies HA, ADR-0016 ratifies per-region storage, the
  `archival-node` ansible role embodies the per-host spec, r1 is
  live on Hetzner FSN1). Frontmatter on each now reflects current
  state with a pointer to the ratifying ADR or role. The
  `ha-plan.md §11` and `multi-region-topology.md §15` "Open
  questions to close before Week 2 design review" sections are
  now "closed" lists, citing where each answer landed (ADR / role
  path / runbook). Removes a recurring source of confusion when
  agents and operators read these docs cold and assume the plan
  is still in flight. Continuation of the L6.5 doc-sweep.
- **Eight more runbooks plus the runbook template are
  bare-metal-native** — final batch of single-mention kubectl /
  k8s drift in the L6.5 doc-sweep. `redis-replication.md`,
  `redis-memory.md`, `price-stale.md`, `rpc-lag.md`,
  `core-lag.md`, `archive-publish.md`, `archive-divergence.md`,
  `backup-failed.md`, and `_template.md` each had a single
  kubectl line referencing pods/StatefulSets/Daemonsets/Jobs
  that don't exist in our deployment. Each line replaced with
  the systemd / journalctl / ansible-role equivalent that
  matches what the repo actually deploys. The `_template`
  example block now nudges new runbook authors toward
  `systemctl status` / `journalctl -u` rather than `kubectl ...`.
  All 25+ kubectl-bearing runbooks have now been converted across
  PRs #460/#461/#462/#463/#464 and this PR.
- **Four host-level runbooks are bare-metal-native** —
  `host-cpu-high`, `host-memory-high`, `host-down`, `nvme-smart`
  each had a single `kubectl` line that doesn't apply to our
  fleet. Per-process / per-cgroup breakdown now uses
  `systemd-cgtop` (it's already installed on every Ubuntu host
  via systemd; no extra deps). Host-drain steps now route via
  HAProxy admin (`disable server <pool>/<host>` on each LB)
  instead of `kubectl cordon` — Patroni / Sentinel handle DB and
  cache primary failover automatically. Continuation of the L6.5
  doc-sweep (#460/#461/#462/#463).
- **Five indexer-side runbooks are bare-metal-native** —
  `source-stopped`, `cursor-stuck`, `orphan-events`,
  `discovery-drops`, and `decode-errors` each had a single stale
  `kubectl rollout restart deploy/ratesengine-indexer` /
  `kubectl logs deploy/ratesengine-indexer` invocation that
  doesn't run on r1. The indexer ships as
  `ratesengine-indexer.service` per the `archival-node` ansible
  role (ADR-0008). Restart commands now use `ssh root@indexer-01
  "systemctl restart ratesengine-indexer"`; log commands use
  `journalctl -u ratesengine-indexer`. Continuation of the L6.5
  doc-sweep started in #460/#461/#462.
- **Four more runbooks are bare-metal-native** — same drift as
  api-down and api-5xx: kubectl-flavoured diagnosis steps that
  wouldn't run on production. `redis-master-down.md` now talks
  to `cache-01..03` running `redis-server.service` +
  `redis-sentinel.service` (per the `redis-sentinel` role) instead
  of `kubectl get pods -l app=redis` and `redis-0..2` StatefulSet
  pod names. `scrape-failing.md` swaps `kubectl exec -it
  prometheus-0` for `ssh root@mon-01` running `prometheus.service`
  and rewrites the SD-misconfig section from ServiceMonitor /
  PodMonitor to the prometheus role's static-config drift.
  `alertmanager-bad-config.md` swaps `kubectl get cm
  alertmanager-config -o jsonpath` for `cat
  /etc/alertmanager/alertmanager.yml` on `mon-01..02` (the cited
  `deploy/monitoring/alertmanager.yml` was a fictional file — the
  role-rendered template is the source of truth). `core-peers.md`
  swaps `kubectl describe cm` / `kubectl logs ds/stellar-core`
  and a fictional `deploy/k8s/network-policy.yaml` for the
  `archival-node` role's per-validator-host shape (still inert on
  r1 since stellar-core was removed 2026-04-23, but ready for the
  Phase-3 Tier-1 rollout). Closes another batch of the L6.5
  doc-sweep.
- **`api-5xx` runbook is bare-metal-native** — the runbook still
  walked operators through `kubectl rollout undo`, an Istio
  `VirtualService` JSON-patch (we don't run Istio), and
  `kubectl scale --replicas=6` for "load mitigation." None of
  those map to production: ADR-0008 ratifies systemd-managed
  binaries on three fixed `api-01..03` hosts behind two HAProxy
  load balancers — no autoscaler, no Istio, no kubectl.
  Diagnosis now uses the per-host `/v1/version` probe +
  `systemctl show -p ActiveEnterTimestamp` to time-correlate
  releases against the error-rate lift; §A revert defers to the
  Rollback procedure in `release-process.md`; §B endpoint-block
  offers the HAProxy `http-request return 503 if path_beg`
  rule + the binary feature-flag option; §D rewrites "scale up"
  guidance — bare metal doesn't autoscale, so the real
  mitigations are edge rate-limiting + path shedding + (last
  resort) DR promotion. Closes another L6.5 doc-sweep item.
- **`api-down` runbook + `release-process.md` rollback path are
  now bare-metal-native** — both still spoke kubectl
  (`kubectl rollout undo`, `kubectl logs`, `kubectl get pods`,
  …) from a pre-ADR-0008 cloud-sketch era. ADR-0008 ratifies
  colocated bare metal as the primary deployment shape; production
  runs `ratesengine-api.service` on three hosts behind two
  HAProxy + keepalived load balancers — no Kubernetes anywhere.
  An operator paged at 3 AM following kubectl commands on this
  fleet would land on errors, not diagnosis. `api-down.md`
  rewritten end-to-end against `systemctl` / `journalctl` /
  HAProxy admin socket; `release-process.md` grew a full
  "Rollback" section documenting the per-host binary-swap
  procedure (rolling for the API tier via the
  `disable server api_pool/api-XX` admin command). The post-flight
  thin "Rollback path" bullet now points at the new section
  instead of inlining a stub. Closes a documentation drift
  surfaced during the L6.5 doc-sweep.
- **`pkg/client/doc.go` — accurate auth + coverage** — the
  package-level godoc that ships to pkg.go.dev had two stale
  sections: the "Authentication" SEP-10 bullet still said
  "pending; will be added when the server's SEP-10 verifier ships
  (Phase 5)" — but the verifier ships at
  `/v1/auth/sep10/{challenge,token}` (PR landed weeks ago) and
  the SDK accepts SEP-10 JWTs verbatim via Options.APIKey today.
  And the "Roadmap" section claimed "PR A (this PR) ships the
  skeleton" — language that's been stale since the skeleton
  landed. Replaced both: SEP-10 bullet documents the live
  challenge → sign → verify flow + that `Authorization: Bearer`
  carries either `rek_*` keys or SEP-10 JWTs; the new "Coverage"
  section enumerates the eight methods on main today, the seven
  queued in PRs #446–#450, and the four surfaces deliberately
  not-in-SDK (SSE / VWAP-TWAP-derivable / SEP-40 oracle /
  operator endpoints).
- **`launch-readiness-backlog.md` — six 🟢 / 🟡 items flipped to ✅
  to match shipped reality** (L6.5 doc-sweep): L3.11 (API
  reference workflow), L3.14 (CDN cache-control middleware),
  L3.15 (getting-started doc), L3.16 (URL-discipline OpenAPI
  lint), L5.5 (chaos suite Wave 1), L6.1 (CHANGELOG hygiene +
  SemVer policy), L6.2 (release notes template +
  release-process), L6.3 (public-flip prep). Each row now points
  at the file path that exists on main today and notes any
  per-item operator follow-up that's deliberately deferred (e.g.
  L3.14's CloudFront-side config, L6.3's actual cutover at
  L6.4). Status emoji legend at line 34 unchanged.
- **`docs/getting-started.md` SDK example now compiles** — the
  customer-facing onboarding doc showed
  `c.GetPrice(ctx, "native", "fiat:USD")` for the SDK quickstart,
  but no such method exists on `*client.Client`. Customers
  copy-pasting the example would hit a Go build error on the
  first line. Replaced with the actual `c.Price(ctx,
  client.PriceQuery{Asset, Quote})` shape returning
  `*Envelope[PriceSnapshot]`. Also fixed the API-key example
  prefix (`rate_` → `rek_`, matching the actual issuance path
  at `internal/auth/store.go:142`'s
  `generateID(s.randRead, "rek_", 32)`) and added a "what methods
  exist today" note so the doc doesn't imply a method that
  lives on an unmerged PR.
- **Three runbooks no longer reference fictional commands /
  paths** (L6.5 doc-sweep continued):
  `runbooks/all-ingestion-down.md` §D referenced `make rollback
  INDEXER_VERSION=<previous>` (`TODO(#0)`); the make target
  doesn't exist and the deployment shape doesn't fit the local-
  build convention. Replaced with the actual systemd-binary
  rollback procedure that `release-process.md` §4.4 prescribes:
  stop the unit, copy the previous-release binary into place
  (kept by goreleaser packaging convention at
  `/opt/ratesengine/release-<tag>/`), restart.
  `runbooks/ingestion-lag.md` step 4 carried `TODO(#0)` for the
  backfill subcommand — except the subcommand exists and has
  for some time (`ratesengine-ops backfill -from N -to N
  -source S`). Replaced the placeholder with the concrete
  two-step `detect-gaps` → `backfill` procedure operators run
  during incidents.
  `runbooks/insert-errors.md` step 2 had the same stale
  `TODO(#0)` PLUS a fictional `deploy/k8s/` PVC reference. The
  production deployment is bare-metal NVMe + ZFS per ADR-0008,
  not Kubernetes. Updated to point at zpool / Hetzner volume-
  resize and the same backfill commands.
- **Six broken markdown links across docs** (L6.5 doc-sweep) —
  surfaced via a Python sweep across every relative `(./...md)`
  link in `docs/`. Closed:
  `docs/adr/0023-sep41-supply-observer.md` `0003-i128-no-truncate.md`
  → `0003-i128-no-truncation.md`.
  `docs/architecture/supply-pipeline.md` two links: same ADR-0003
  fix + `0006-timescale-storage.md` →
  `0006-timescaledb-for-price-time-series.md`.
  `docs/operations/r1-deployment-state.md`: extra `..` in
  `../../discovery/data-sources/archival-nodes.md` → fixed to
  `../discovery/...`.
  `docs/operations/wasm-audits/evidence/blend/phase2-2026-05-02/README.md`:
  off-by-one relative path `../../blend.md` → `../../../blend.md`.
  `docs/architecture/infrastructure/archival-node-spec.md`: three
  fictional runbook refs (`archive-publish-fail.md`,
  `galexie-lag.md`, `rpc-sqlite-growth.md`); first replaced with
  the real `archive-publish.md`, the other two converted to
  italicised "_runbook tbd_" notes citing the existing ad-hoc
  coverage path (no creation of stub runbooks — the alerts they
  reference are post-launch / Phase-3 anyway).
  `docs/architecture/ha-plan.md` §3.10 ratesengine-ops: fictional
  `ops-cli.md` doc replaced with a description of the binary's
  actual top-level subcommands, citing `--help` and the source
  at `cmd/ratesengine-ops/main.go`.
  Verification: re-ran the link sweep; zero broken links remain.

- **SSE event-ID generator no longer wraps to duplicates after
  65 536 same-millisecond IDs** — `streaming.Generator.Next`'s
  docstring promised "never returns the same ID twice" but the
  counter was masked to 16 bits, so 65 536 IDs in a single
  millisecond wrapped back to 0 and re-issued every prior ID
  for that millisecond. A reproducer pinned the bug at 4 464
  duplicates across 70 000 calls in one ms (e.g. publish-burst
  during a fan-out spike, tight test loop, or hot-loop in
  operator code). Fix advances the synthetic millis by 1 when
  the counter saturates instead of wrapping; subsequent
  wall-clock ms catch back up via the existing `now > oldMillis`
  branch. Three new tests: NeverDuplicates (70 k same-ms calls),
  StrictlyIncreasing (lex-sort = chronological invariant),
  ConcurrentNoDuplicates (50×2 000 goroutines).

- **`divergence.Compare` recovers panics from references** — the
  function's docstring promised "panic recovered, etc. are
  recorded in Failures", but the per-reference goroutine had no
  `recover()` deferred. A misbehaving reference (network panic,
  malformed-JSON parser blow-up, operator-supplied custom
  reference with a bug) would take the whole comparison run
  down + crash the worker. Now the goroutine recovers and
  records the panic with a stable `panicked: <text>` failure
  label so operators see which reference is broken without
  reading goroutine traces. New `safeName` helper guards
  `Reference.Name()` itself in case it's what panics — the
  failure surfaces under `_unknown` in that path.

- **Rate-limit middleware now honours `Subject.RateLimitPerMin`** —
  the field was plumbed end-to-end (storage record → validator →
  Subject → `/v1/account/me`) but `RateLimitBySubject` only
  consulted the bucket's static `Max()`, so a paid customer with a
  per-key override of e.g. 5000/min got throttled at the deployment
  default (typically 1000). `Bucket.TakeN(ctx, key, max)` accepts
  a per-call override (≤0 falls back to `b.max`); the middleware
  passes `subject.RateLimitPerMin` through and surfaces the
  effective limit in the `X-RateLimit-Limit` response header.
  Anonymous callers continue to use the bucket default (no per-IP
  override path). Closes another exposed-but-never-driven gap from
  the account self-service work.

- **`/v1/account/me` now returns the credential's `label`** —
  `APIKeyRecord.Label` was set at creation time and the OpenAPI
  `Account` schema declared the field, but the path
  `RedisAPIKeyValidator.Lookup` → `auth.Subject` → `handleAccountMe`
  dropped it on the floor (no `Label` field on `Subject`). Customers
  who created keys via `POST /v1/account/keys` saw their chosen label
  recorded, then got an empty string back from `/me`. Subject now
  carries `Label`, the validator copies it from the record, and the
  handler surfaces it. Anonymous callers continue to get an empty
  label (omitempty hides it from the wire).

### Added

- **`/v1/sources` exposes `subclass` and `backfill_safe`** — the
  endpoint already projected `external.Registry` to the wire, but
  two operationally-useful fields stayed internal-only. `subclass`
  (`dex` / `cex` / `fx`, omitted for non-exchange classes) lets UI
  consumers group exchange venues without reverse-engineering the
  name prefix. `backfill_safe` surfaces the per-WASM-hash audit
  state that gates `ratesengine-ops backfill` (CLAUDE.md "Soroban
  DeFi contracts upgrade in place"): operators can now read it
  off the API instead of grepping
  `internal/sources/external/registry.go`. Additive — no existing
  field changed shape.

- **`pkg/client` godoc examples** — three `Example*` functions
  (`ExampleNew`, `ExampleClient_Price`, `ExampleClient_Asset`,
  `ExampleAPIError`) that show up in pkg.go.dev and verify
  themselves at build time via `// Output:` assertions.
  Self-contained against `httptest`-backed servers so they don't
  need a live API. Walks integrators through the canonical SDK
  surface: construct + call + handle errors.

- **API binary wires the freeze.Looker so `flags.frozen` is no
  longer permanently false (closes another half-shipped audit
  finding)**: `freeze.Looker` reads the `freeze:<asset>:<quote>`
  markers the aggregator's `freeze.Writer` publishes (Phase 1 + 2
  anomaly response, ADR-0019), but the API binary's
  `v1.New(Options{...})` never set `Freeze:`. The handler-side
  `FrozenLooker` interface was declared and `/v1/price`'s
  `lookupFrozen` consulted it, but with no looker installed the
  call always returned (false, nil) — operators relying on
  `flags.frozen` to detect frozen-LKG responses got permanent
  `false`. Now `cmd/ratesengine-api/main.go` constructs
  `freeze.NewLooker(rdb)` when Redis is configured (mirrors the
  existing pattern for confidence + triangulated lookers) and
  passes it through `Options.Freeze`. L3.13 in the launch-readiness
  backlog flips from 🟢 to ✅.

- **Aggregator now drives the divergence-cache refresh (closes
  another half-shipped audit finding)**:
  `divergence.Service.RefreshPair` was exposed but had zero
  production callers — the API's `flags.divergence_warning` reads
  from `div:<asset>` Redis cache, but nothing populated the cache,
  so the flag was permanently `false` across the public surface.
  Wired the orchestrator's Tick to call `RefreshPair` once per
  configured pair after VWAPs are written, using the
  shortest-window VWAP as "our price". Best-effort per-pair: errors
  log + count via the new `ratesengine_divergence_refresh_total{outcome}`
  counter (ok / no_vwap / parse_error / refresh_error) but never
  abort the Tick. New `orchestrator.DivergenceRefresher` interface
  is the seam (nil = pre-Phase no-op preserved); aggregator's
  `main.go` builds the same `divergence.Service` shape the API
  binary already builds, mirroring the helper for now (a shared
  builder is one CHANGELOG fixme away when a third caller appears).

- **`ratesengine_trade_inserts_total{source, usd_volume_populated}`
  counter for L2.2 phase 1 coverage**: per-source counter labelled
  by whether the trade's `usd_volume` column was populated at
  insert time. Operators flipping on
  `[trades].usd_pegged_classic_assets` use this to verify their
  allow-list actually covers what the indexer is seeing — a
  configured deployment with steady-state
  `usd_volume_populated="no"` on a USDC-quoting venue means the
  operator's classic asset_key doesn't match the decoder's stamp
  (typically an issuer mismatch or a missing entry).
  `Store.WouldPopulateUSDVolume(t)` exposes the predicate as a
  package-public method so the pipeline sink can label the metric
  without re-implementing the populated-ness decision.

- **SEP-1 issuance declarations now surfaced on `/v1/assets/{id}` +
  `/v1/assets/{id}/metadata`**: `conditions`, `fixed_number`,
  `max_number`, and `is_unlimited` from the issuer's
  `[[CURRENCIES]]` entry populate when `sep1_status="verified"`.
  These are issuer-declared (separate from the F2 fields, which
  observe live ledger state) — useful for asset-detail UIs that
  want to show "Circle has committed to a fixed total of X
  tokens" alongside the live `total_supply`. The metadata package
  already parsed these fields; the gap was in the API projection.
  OpenAPI spec was already promising them on
  `/v1/assets/{id}/metadata` (under different field names,
  including the wrong `image_url` for `image`); this PR realigns
  the spec to the handler's actual shape AND adds the four
  issuance fields to the surface for real. SDK
  `pkg/client.AssetMetadata` updated to match (replaces the
  invented `sep1`/`fetched_at` fields that didn't exist on the
  wire).

- **On-chain DEX trades populate `trades.usd_volume` (launch-
  readiness L2.2 phase 1)**: previously only off-chain CEX/FX
  trades populated `usd_volume` at insert time — on-chain trades
  (Stellar SDEX, Soroswap, Aquarius, Phoenix, Comet) stored NULL,
  biasing the `volume_24h_usd` field on `/v1/assets/{id}` toward
  off-chain venues. New `[trades].usd_pegged_classic_assets`
  config — operators list classic credits they trust as
  USD-pegged stablecoins (e.g. Circle's USDC-GA5...). On-chain
  trades quoted in any of those classics, OR in their SAC wrapper
  (transitive via `[supply.sac_wrappers]`), now populate
  `usd_volume = quote_amount / 10^7` at insert time. Empty
  allow-list preserves the pre-Phase-1 default. Phase 2 (FX-anchor
  multiplication for non-USD on-chain quotes — XLM/AQUA, XLM/BTC)
  is post-launch. The OpenAPI / storage / handler doc caveats
  on `volume_24h_usd` updated to reflect the operator-opt-in
  surface; the field stays forward-compatible (Phase 2 lands
  additively).

  New surface: `internal/storage/timescale.USDVolumeQuoteSpec` +
  `Store.SetUSDVolumeQuoteSpec`. Wired into both the indexer's
  live ingest path and the ops-binary backfill path so an
  operator-driven historical replay matches live behaviour.

### Fixed

- **`pkg/client.AssetDetail` was missing 15 documented wire
  fields**: the SDK consumer using `client.Asset()` deserialized
  into a struct that omitted `decimals`, `sep1_status`, all six
  SEP-1 overlay fields (`name`, `description`, `image`,
  `org_name`, `anchor_asset`, `anchor_asset_type`), all seven F2
  fields (`circulating_supply`, `total_supply`, `max_supply`,
  `market_cap_usd`, `fdv_usd`, `supply_basis`,
  `volume_24h_usd`), and the four SEP-1 issuance declarations
  (`conditions`, `fixed_number`, `max_number`, `is_unlimited`).
  Go's `encoding/json` silently ignores unknown fields by
  default, so consumers got zero-valued structs without warning
  — the only way to access the missing fields was dropping to
  raw HTTP. This was a real wallet-integrator gap (the F2 + SEP-1
  fields are exactly what asset-detail UIs need). Adding the
  fields is purely additive under SemVer (`pkg/client` is `v0.x`
  pre-release; the SDK contract pins backwards-compat from
  `v1.0.0`). Two new tests pin the JSON-decode contract and the
  `omitempty`-on-nil round-trip shape so a future regression
  fires before shipping.

- **`ratesengine-aggregator` log-level + log-format now match the
  other binaries**: the aggregator's bespoke logger factory was
  case-sensitive on the `[obs] log_level` value (so `LogLevel =
  "DEBUG"` silently fell back to info), missed the `"warning"`
  alias the indexer + api accept, and the LogFormat switch only
  recognised `"console"` (not `"text"`). Extracted the shared
  factory to `internal/obs.NewLogger(cfg, binaryName)` and pointed
  all three binaries at it. Side-effect: aggregator logs now also
  carry the `binary=ratesengine-aggregator` slog attribute, so
  Loki dashboards can filter per-binary without grepping path
  prefixes (the indexer + api already had this stamp).

### Added

- **Supply cross-check gauge wired into the aggregator's
  refresh loop (closes a half-shipped audit finding)**: the
  `ratesengine_supply_cross_check_divergence_stroops` gauge and
  the `ratesengine_supply_cross_check_total{outcome=…}` counter
  were declared in `internal/obs/metrics.go` and the supply alert
  in `deploy/monitoring/rules/supply.yml` referenced them, but no
  production code path emitted either — the alert was inert.
  Added `internal/supply.CrossCheckRefresher` (loads the latest
  classic + SAC snapshots per pair, runs `supply.CrossCheck`,
  emits the gauge + counter via a small `CrossCheckEmitter`
  interface) and wired it into `ratesengine-aggregator` alongside
  the per-asset supply refreshers. Pairs are derived from the ∩ of
  `[supply].sac_wrappers`, `watched_classic_assets`, and
  `watched_sep41_contracts` — no new config knob. Runbook
  `docs/operations/runbooks/supply-cross-check-divergence.md`
  flipped from `draft` to `living` and the manual-cron caveat is
  gone; metric doc comments lose the "not yet emitted" note. New
  outcome labels (`missing_snapshot`, `read_error`) surface the
  bootstrap state and transient-storage failures separately from
  genuine within/over divergence.

- **Blend WASM audit complete; `BackfillSafe` flipped → `true`
  (Task #53)**: the 5h4m wide-net wasm-history walk on r1
  finished 2026-05-02 and covered all 11 Blend contracts (9 pools
  + 1 backstop + 1 pool factory) over the verified-clean ledger
  range [50,457,424, 62,249,727]. Result: 3 unique WASM hashes
  observed across all 11 contracts, zero mid-life upgrades. The
  three hashes (pool `a41fc53d…`, backstop `c1f4502a…`, factory
  `31328050…`) match Phase 1's Soroban-RPC current-state query
  and have all been disassembled in Phase 3 with the decoder-
  expected event topics + AuctionData field names confirmed
  present. `internal/sources/external/registry.go` flips
  `blend.BackfillSafe` from `false` to `true`; `framework_test.go`
  moves `blend` from `wantUnsafe` to `wantSafe`. Audit doc
  `docs/operations/wasm-audits/blend.md` adopts the canonical
  per-contract findings table; filtered evidence preserved at
  `docs/operations/wasm-audits/evidence/blend/phase2-2026-05-02/`.

- **Per-trade `usd_volume` column populated at insert (partially
  closes launch-readiness L2.2)**: previously `InsertTrade` set
  `usd_volume = NULL` with a "filled by aggregator" comment that
  never got actioned, which silently zeroed
  `/v1/assets/{id}.volume_24h_usd` (the CAGG `prices_1m.volume_usd`
  is `sum(coalesce(usd_volume, 0))`). New
  `internal/storage/timescale/trades.go::tradeUSDVolume` populates
  the column when the source is off-chain (Subclass=CEX or FX, so
  amount is at the uniform 10⁸ decimal convention) AND the quote is
  fiat:USD or a USD-pegged stablecoin per `aggregate.FiatProxy`
  (USDC/USDT/DAI/PYUSD/USDP). For those trades the value is exact:
  `quote_amount / 1e8`, rendered as a fixed-precision NUMERIC
  string. Out-of-scope cases (on-chain DEX trades, EUR-quoted pairs,
  unknown sources, oracle-class sources) keep the column NULL — the
  CAGG's `coalesce(0)` makes that the right safe default. Tests
  cover both the populated path (binance + fiat:USD, polygon-forex
  + fiat:USD, kraken + USDC) and the NULL path (soroswap, EUR
  quote, unknown source, reflector, coingecko, zero amount). The
  remaining on-chain coverage (XLM/USDC trades from soroswap /
  aquarius / phoenix at per-source decimals) is a separate
  follow-up — same L2.2 row stays ⚠ because the
  on-chain path needs per-source decimal awareness that's its own
  design conversation.

- **Per-pair `aggregate.min_usd_volume` filter wired through the
  orchestrator (closes launch-readiness L2.1 caveat)**: the config
  knob existed in `internal/config/config.go` (`MinUSDVolume`,
  default 10_000) but no production code path consumed it. The
  re-baseline of `docs/architecture/launch-readiness-backlog.md`
  surfaced this as the L2.1 ⚠ caveat. This commit threads it through
  to `Config.MinUSDVolume` and adds a window-level filter step in
  `refreshPairWindow` between the per-trade outlier filter and the
  VWAP compute. When set > 0 AND the pair's quote is `fiat:USD`,
  the orchestrator sums each contributing trade's `quote_amount` /
  10⁸ (the uniform off-chain CEX/FX scale per
  `internal/sources/external/<venue>::externalAmountDecimals`) and
  drops the window if the sum is below threshold. Non-USD-quoted
  pairs are exempt because cross-decimal arithmetic across mixed
  on-chain/off-chain sources doesn't reduce to a clean single-USD
  figure; the dominant launch case (XLM/USD) is in scope. Skip path
  emits new
  `ratesengine_aggregator_dropped_windows_total{reason="min_usd_volume"}`
  + bumps the existing `empty_windows_total` so freshness alerts
  see consistent state. Filter is OFF when `MinUSDVolume == 0` —
  preserves pre-filter behaviour for deployments that haven't
  tuned the threshold yet. Tested: thin window rejected; fat
  window published; non-USD pair exempt; filter-off bypass.

- **`ratesengine-ops wasm-history-merge-jsonl` — recover from a
  crashed walk**: the existing `wasm-history -checkpoint-dir` flag
  has been writing per-worker JSONL transition logs since #185, but
  the matching merge tool that reconstructs the canonical JSON from
  those files was tracked in a comment as "(planned) or hand-stitch".
  This subcommand fills that gap. After the wide-net walk on r1 died
  at 5 h on 2026-05-01 (failed `-to` past the archive's frozen tip,
  see PR #368), we lost the in-memory state — the JSON only writes
  at end-of-run. Going forward, every multi-hour walk should pass
  `-checkpoint-dir`; if it crashes, recover with
  `ratesengine-ops wasm-history-merge-jsonl -checkpoint-dir <dir> -to N`.
  The merge logic mirrors the walker's end-of-run merge: per-contract
  sort by ledger, collapse adjacent same-hash transitions across
  worker boundaries, close the last range at `-to`. Half-written
  trailing lines (a crashed worker's last partial flush) are
  tolerated. Smoke-tested against the in-flight wide-net checkpoint
  dir on r1 — reconstructed 144 contracts from 273 transitions across
  8 worker JSONL files. Documented in
  `docs/operations/wasm-audits/README.md` §2.

- **Chaos suite Wave 1 (Task #75)**: ships `test/chaos/` with three
  failure-mode scenarios against the docker-compose dev stack —
  Redis container stop, Timescale container stop, Redis network
  partition. Pass criteria assert the API either degrades-with-flag
  or fails loudly with a structured envelope; never silently serves
  bad data. Bash-based to keep symmetry with the k6 load suite's
  external-tool harness shape (separate `test/load/` already uses
  k6 .js files); Go was considered but `exec.Command` boilerplate
  around `docker stop` / `pumba pause` would be longer than the
  bash equivalent. Production-safety guard duplicated at runner +
  scenario-prologue level: every script refuses to run against a
  target whose host matches `*production*` /
  `*api.ratesengine.net*` / `*prod.*`. Wave 2 (HA-shaped scenarios
  — Patroni replica promotion, Redis Sentinel failover, HAProxy +
  keepalived VIP flip) is gated on staging baremetal deploys and
  is deferred post-launch. Companion design note at
  `docs/architecture/chaos-suite-design-note.md`. Closes
  launch-readiness L5.5.

- **X2.5 forex-factor snap rule (Task #71)**: implements
  ADR-0018 §"Forex factor handling" so chained-fiat triangulation
  (e.g. XLM/EUR via XLM/USD × USD/EUR) preserves across-region
  consistency. For every fiat-vs-fiat leg of a configured
  triangulation chain, the orchestrator queries the most recent
  FX-source quote at-or-before the bucket-end timestamp instead of
  reading the leg's cached VWAP — every region serving the same
  closed bucket queries the same trades hypertable and gets the
  same row. Pre-snap behaviour was *almost* equivalent to the rule
  in steady state (region observation timing skew + multi-publish-
  per-bucket FX sources were the strict-compliance gap); the new
  path closes that gap and is the path ADR-0018 mandated.
  Storage primitive: `timescale.Store.FXQuoteAtOrBefore(pair,
  cutoff, fxSources)`. FX-source enumeration:
  `external.FXSources()` (deterministic lex order) +
  `external.IsFXSource(name)`. Orchestrator:
  `internal/aggregate/orchestrator/triangulate.go::legPrice`
  routes FX legs (both sides `AssetFiat`) to the snap path when
  `Config.FXStore` is wired. Snap misses
  (`timescale.ErrNoFXQuote`) fall back to the cached-VWAP path so
  chains stay published; new metric
  `ratesengine_aggregator_fx_snap_fallback_total{leg=…}` counts
  these. Alert
  `ratesengine_aggregator_fx_snap_fallback_dominant` fires at >50%
  fallback rate sustained for 30 m. Hard DB errors from the FX
  store skip publish for that tick (no chained-fiat output if we
  can't trust the FX leg). Wired by default in
  `cmd/ratesengine-aggregator/main.go` (passes the existing
  `*timescale.Store` as the `FXStore`); deployments without FX
  ingestion configured see no behavioural change because legs
  fall back uniformly. Companion runbook:
  `docs/operations/runbooks/aggregator-fx-snap-fallback-dominant.md`.

- **Loki + Promtail ansible role — CLOSES Task #72**: ships
  the fifth and final sub-role of #72 after Patroni (#344),
  Redis Sentinel (#350), HAProxy (#362), and Prometheus (#363).
  Single-host Loki running in single-binary mode per ha-plan §7
  ("Logs: Loki + Tempo" — singular, not paired). Chunks land in
  MinIO via S3 backend (reusing the galexie S3 deployment); index
  is local BoltDB. Promtail agents ship the systemd journal from
  every host in `log_shippers` (the union of every other
  inventory group: prometheus_pair / ratesengine_api / aggregator
  / indexer / haproxy_lb / redis_cluster / postgres_cluster).
  Single role file with two task surfaces — server tasks
  (`server-{01..05}.yml`) run on hosts in `log_aggregator`, agent
  tasks (`agent-{01..03}.yml`) on `log_shippers`. Versions
  pinned to upstream `v3.2.0` for both Loki and Promtail.
  Promtail labels every entry with `job=systemd` + `instance` +
  `unit` + `hostname` + `severity` for downstream filtering;
  drops a few low-signal units (`systemd-tmpfiles-clean`,
  `cron`, `systemd-logind`) as noise. 30d retention via Loki's
  compactor; reject-old-samples set to 7d to catch broken
  Promtail position files. Loki query API + Promtail HTTP
  endpoint both bound to internal addresses, with the firewall
  drop-in opening 3100 only on the internal CIDR. Companion
  design note at
  `docs/architecture/loki-ansible-role-design-note.md` covers
  the 1-host design choice (logs are forensic, not real-time-
  decision; HA scale-up path documented), the BoltDB-vs-TSDB
  index trade-off at this scale, and the failure-mode table
  (Promtail buffers up to 10k entries during Loki outage; new
  chunks fail with 429 if MinIO is down; etc.). After this PR,
  **Task #72 is fully closed** — all five sub-roles landed
  this session.

- **Prometheus + AlertManager ansible role (Task #72 sub-role)**:
  closes the fourth sub-role of #72 after Patroni (#344), Redis
  Sentinel (#350), and HAProxy (#362). 2-host Prometheus pair per
  `docs/architecture/ha-plan.md §7`; each host independently
  scrapes all targets (data duplication is the HA mechanism), and
  AlertManagers cluster via gossip on port 9094 to dedupe alerts
  before fanout. Seven task files (preflight with disk-space +
  time-sync + vault-warning checks, install via upstream tarballs
  pinned to `v2.54.1` / `v0.27.0`, prometheus-configure with
  inventory-driven scrape config + rule-file sync from
  `deploy/monitoring/rules/`, alertmanager-configure with
  PagerDuty/Slack routing, systemd, firewall, self-scrape
  monitoring), four templates (`prometheus.yml.j2` walks the
  inventory groups to build scrape configs, `alertmanager.yml.j2`
  with severity-based routing + inhibit rules, both systemd
  units). Ships with all 17 existing rule files
  (`aggregator/anomaly/api/archive-completeness/cache/divergence/
  infra/ingestion/meta/sla-probe/slo/stellar/storage/supply*/
  verify-archive`, ~1721 LoC) loaded via the rule-files-sync
  pass that also handles deletions (drops files no longer in
  repo). Three validation gates (`promtool check config`,
  `promtool check rules`, `amtool check-config`) run BEFORE any
  reload, so a malformed render never lands. Loopback-only
  bindings (`127.0.0.1:9090` + `:9093`); operators SSH-tunnel.
  Companion design note at
  `docs/architecture/prometheus-ansible-role-design-note.md`. After
  this PR, only Loki remains of Task #72's five sub-roles.

- **HAProxy ansible role + keepalived VRRP (Task #72 sub-role)**:
  closes the third launch-critical sub-role of #72 after Patroni
  (#344) and Redis Sentinel (#350). Two LB hosts share a
  floating VIP via keepalived VRRP, fronting the
  `ratesengine-api` pool with `/v1/readyz`-based health checks
  per `docs/architecture/ha-plan.md §3.1`. TLS terminates at the
  edge (HSTS on every response, Mozilla intermediate cipher
  suite); HAProxy's built-in Prometheus exporter is enabled on
  the loopback stats endpoint (`127.0.0.1:8404/metrics` — never
  exposed publicly). Seven task files (preflight with
  `net.ipv4.ip_nonlocal_bind=1` for VIP binding + a vrrp-
  password-length warning, install, haproxy-configure with
  `haproxy -c -f` validation, keepalived-configure, systemd
  hardening drop-in, firewall allowing 80/443 + VRRP from peer
  CIDRs, monitoring), three Jinja templates (haproxy.cfg,
  keepalived.conf, systemd-override). Health-check semantics:
  5s interval, 3 fails before drain, 2 successes before re-add
  (15s detection latency), 10s slowstart prevents cold pods
  from getting hammered after recovery. Failover RTOs:
  ≤3s for HAProxy host failure (keepalived VRRP), 1-4s for
  HAProxy process death (`chk_haproxy` track-script). Companion
  design note at
  `docs/architecture/haproxy-ansible-role-design-note.md` covers
  cloud VRRP gotchas (Hetzner multicast OK; AWS needs unicast
  peers), VIP-as-secondary-IP requirements, and the rolling
  vrrp-password rotation procedure. After this PR, two of five
  Task #72 sub-roles remain (Prometheus + Loki); the
  launch-critical HA path is complete (Patroni-driven Postgres
  failover + Sentinel-driven Redis failover + keepalived-driven
  api-tier failover + HAProxy-driven api-pod redirection).

- **`ratesengine-ops wasm-history` Tier 2 enhancements: storage-
  rotation + ContractCode-upload tracking**: opt-in observers
  that ride alongside the existing executable-hash transition
  walker. Closes the "wide-net" goal called out in
  `walker-investigation-2026-05-01.md`. Two new flags:
  - `-storage-rotations-out=PATH` — when set, every
    Created/Updated/Restored `ContractData` entry whose key is
    NOT `LedgerKeyContractInstance` (i.e. custom storage rows)
    is recorded for any watched contract. Catches admin
    storage flips like Soroswap factory's `set_pair_wasm`
    rotation that the wasm-history-only walker doesn't see.
    Output: `[{contract, changes: [{ledger, change_type,
    key_xdr_b64, key_hint, durability}]}]`. The `key_hint`
    field renders common SCVal key shapes (Symbol, Vec\[Symbol,
    ...\], U32, Bytes) as one-line summaries so an operator
    skimming the JSON can recognise patterns without round-
    tripping the base64-encoded XDR through a decoder.
  - `-code-uploads-out=PATH` — when set, every `ContractCode`
    Created/Restored event observed in the walked range is
    captured globally (not per-watched-contract; the upload is
    independent of which contract may later reference the
    hash). Output: `[{ledger, wasm_hash, size_bytes, change_type}]`.
    Updated changes are deliberately excluded — Soroban's
    ContractCode bytes are immutable, only TTL changes via
    Updated, so they're not real upload events.

  Both features are off by default; the existing wasm-history
  stdout shape is unchanged. Tests cover the positive paths +
  the inverse-filter on Instance keys + the entry-type
  short-circuit.

  Operational use: re-run `wasm-history` against the curated
  `configs/audit/wasm-walk-contracts.yaml` list with both flags
  set to capture the full picture in one pass — the wide-net
  walk plan from PR #359.

- **Redis Sentinel ansible role + go-redis FailoverClient
  migration (Task #72 sub-role)**: closes the second
  launch-critical sub-role of #72 (after Patroni #344).
  Implements the topology pinned by ADR-0024: 1 primary + 2
  replicas across 3 cache hosts, 3 co-located Sentinels with
  quorum=2, AOF every-second + RDB nightly persistence,
  failover RTO 15–30 s. Seven task files (preflight with
  THP/overcommit kernel tuning, install, redis-configure,
  sentinel-configure, systemd hardening drop-ins, firewall
  internal-only on 6379+26379, monitoring via redis_exporter
  +  textfile sentinel-state scraper), three Jinja templates
  (redis.conf, sentinel.conf, systemd-override), idempotent
  re-runs (consults Sentinel for the current primary; refuses
  to overwrite post-failover state when
  `redis_first_run_only=true`).
  New `internal/storage/redisclient` package centralises
  client construction: `Build(StorageConfig)` picks
  `redis.NewFailoverClient` when `redis_sentinel_addrs` is
  non-empty, falls back to `redis.NewClient` against
  `redis_addr` for dev / single-node, returns nil when both
  unset. Both `cmd/ratesengine-api` and
  `cmd/ratesengine-aggregator` now route through this builder
  and log `redis configured mode={sentinel|single|disabled}`
  at startup. New `redis_sentinel_addrs` + `redis_master_name`
  config fields with validate-time assertion that
  master_name is set when sentinel_addrs is non-empty.
  Companion docs: ha-plan §3.4 amended to remove the
  Cluster-vs-Sentinel contradiction (per the original tension
  ADR-0024 ratifies); `redis-master-down.md` runbook split
  into §A automatic-Sentinel-failover (now the default,
  15–30 s RTO) and §B manual-failover (the
  `redis-cli SENTINEL failover` escalation path), with
  Sentinel-aware diagnosis commands. The
  `ratesengine_redis_sentinel_primary` gauge — emitted every
  30 s by the role's textfile scraper — sums to 1 across hosts
  in steady-state and is the durable signal for split-brain
  detection.

- **k6 load test suite — Wave 4 (Task #74; weekly schedule —
  CLOSES Task #74)**: ships
  `.github/workflows/k6-weekly.yml` running the canonical
  `06-mixed-realistic.js` against staging every Sunday 02:00 UTC
  (off-peak so a legitimate latency regression isn't masked by
  routine staging traffic). Workflow dispatch supports running any
  single scenario by name for ad-hoc regression investigation. Run
  output flows to the existing Prometheus/Grafana stack via
  `--out experimental-prometheus-rw`; tagged with `run_id` +
  `run_attempt` so the run window is queryable from Grafana
  without guessing timestamps. Secrets required (configured in
  repo settings):
  - `K6_TARGET_STAGING` — staging API base URL (e.g. `https://api.staging.ratesengine.net/v1`)
  - `RATESENGINE_LOAD_API_KEY` — vault-minted load-test API key
  - `K6_PROMETHEUS_RW_SERVER_URL` — Prometheus remote-write endpoint
  After this PR, **Task #74 is closed** end-to-end (scaffold +
  every scenario + AlertManager silence + weekly schedule);
  Task #77 remains the operator action to publish the first
  monthly `sla-proof-YYYY-MM-DD.md` once the staging environment
  has the secrets configured.

- **k6 load test suite — Wave 3 (Task #74; spike + AlertManager
  silence)**: closes the scenario surface for Task #74 by adding
  `99-spike.js` — a 10× burst absorption test (100 → 1000 rps for
  30s, ramp-down, 2 min recovery observation). Pass criteria are
  intentionally permissive on latency mid-spike (the hand-wave
  explicit in the design note §Spike) but tight on error rate
  (< 0.5 %) and recovery (baseline p95 within 2 min of spike end).
  New `scenarios/lib/alertmanager.js` posts a silence to
  `${ALERTMANAGER_URL}/api/v2/silences` matching `APIHighLatencyP95`
  + `APIHighErrorRate` for a 10-min window covering the spike,
  removed in scenario teardown so a real post-run regression
  still pages. Helpers are no-ops when `ALERTMANAGER_URL` is
  unset (Make target prints a 10-second warning so the operator
  can manually silence). Adds `make test-load-spike`. After this
  PR, the only remaining Task #74 work is Wave 4 (GitHub Actions
  weekly schedule) — the actual SLA proof artefact (Task #77) is
  unblocked and ready for the operator's first staging run.

- **k6 load test suite — Wave 2 (Task #74; unblocks #77)**: lands
  the four scenarios that complete the canonical SLA proof.
  `03-history.js` (windowed + since-inception, 80/20 mix per
  customer telemetry), `04-batch.js` (batch-size-100 fan-out at
  50 rps), `05-streaming.js` (constant 200 SSE clients with
  first-event latency tracked via `sse_first_event_ms` Trend),
  and `06-mixed-realistic.js` — the canonical proof scenario
  running the design-note traffic blend (60% price / 15% batch /
  10% tip / 6% vwap / 4% history / 3% twap / 1% stream / 1%
  oracle) at 300 rps over a 10 min soak. Pass criteria align
  with Freighter SLA (p95 < 200 ms; p99 < 500 ms; 99.9 % success
  rate).
  Companion `docs/operations/sla-proof-template.md` is the
  canonical artefact shape for Task #77 — operator copies to
  `sla-proof-YYYY-MM-DD.md` after each canonical run, fills in
  the per-endpoint p95 / p99 / error-rate table from Prometheus,
  attaches Grafana snapshot links, and commits alongside the
  release. The most recent passing report is the proof Task #77
  closes against. Wave 3 (spike + AlertManager-silence) and
  Wave 4 (weekly schedule) follow as separate PRs.

- **k6 load test suite — Wave 1 scaffold (Task #74)**: lays the
  foundation for the Freighter SLA proof (Task #77). New
  `test/load/` tree with `scenarios/lib/{env,pairs,thresholds,warmup}.js`
  shared helpers, the first two scenarios (`01-price-hot-path.js`,
  `02-vwap-twap.js`), `docker-compose.k6.yaml` runner, package
  doc.go for `go doc` visibility, and `reports/` (gitignored) for
  per-run artefacts. Makefile gains `test-load`, `test-load-mixed`,
  `test-load-price`, `test-load-vwap`, and `test-load-check`
  (compile-check without running) — every target is gated by a
  production-target guard that refuses to run if `K6_TARGET`
  resolves to `api.ratesengine.{net,io}` or `rates.stellar.org`.
  The same guard fires inside `scenarios/lib/env.js` so a direct
  `k6 run` cannot bypass it. Companion design note at
  `docs/architecture/k6-load-tests-design-note.md` (lays out the
  remaining waves: 03/04/05 scenarios, mixed-realistic proof,
  spike + AlertManager-silence integration, weekly schedule).
  Wave 1 unblocks ad-hoc operator runs against staging today;
  Task #77 closes once Wave 2's `06-mixed-realistic.js` passes
  end-to-end.

- **Patroni ansible role (#344)**: closes the launch-critical
  sub-role of Task #72. Implements the topology pinned in
  `ha-plan.md §3.3` — 1 primary + 2 synchronous replicas across
  3 hosts, 3-node etcd quorum (DCS), `synchronous_commit=remote_apply`,
  `synchronous_standby_names='ANY 1 (db-02, db-03)'`. Eleven
  task files (preflight, etcd install/configure/systemd, Patroni
  install/configure/systemd, bootstrap, replica join, firewall,
  monitoring), four templates (etcd.conf, etcd.service,
  patroni.yml, patroni.service), idempotent re-runs (detects
  existing cluster via Patroni REST `/cluster` endpoint, refuses
  to overwrite live config when `patroni_first_run_only=true`),
  pgBackRest restore-from-backup path for DR rebuilds. Companion
  design note at `docs/architecture/patroni-ansible-role-design-note.md`.
  **Effect on the launch-readiness picture:**
  `timescale-primary-down.md` Mitigation §A ("Automatic Patroni
  failover — the happy path") is now the actual default rather
  than aspirational; SEV-1 failover RTO drops from ~15 min
  (manual) to ~60 s (Patroni-driven). The drill scenario's
  Validation criterion #6 ("Did anyone reference Patroni hasn't
  landed?") becomes obsolete.

- **ADR-0024 — Redis HA via Sentinel (#343)**: ratifies the
  Redis HA mode choice. `ha-plan.md §3.4` had a contradictory
  description ("3 masters + 3 replicas, Redis-Cluster mode...
  3 sentinels for failover vote") — Cluster and Sentinel are
  different HA modes; the original phrasing combined them. ADR
  pins **Sentinel** for our scale (small hot-set, simpler ops,
  uniform `go-redis/v9 FailoverClient` integration without an
  HAProxy in front of Redis). Notes that ha-plan.md §3.4 should
  be amended for terminological consistency in the same PR
  that ships the Redis Sentinel ansible role (Task #72 sub-role).

- **`status-page-hosting-comparison.md` tracked (#343)**:
  decision-support doc surveying 6 status-page options against
  `sev-playbook.md §5.1`'s requirements. Recommends **Instatus**
  (free tier covers launch volume; modern UI; bring-your-own
  incident-management since we have PagerDuty). Fallback:
  Cachet self-hosted. Closes the design gap on Task #73 — the
  remaining work is half-a-day of vendor wiring once a vendor
  is picked.

### Added

- **`internal/sources/sep41_supply` (event-stream Algorithm 3
  decoder) now registers with the indexer dispatcher — closes
  L2.12a 6/6.** New `dispatcher.AddDecoder` method (mirroring the
  existing `Add{Op,ContractCall,Entry}Decoder` siblings) and a
  new `pipeline.RegisterSupplyEventDecoders` helper that attaches
  the sep41_supply decoder when `[supply] watched_sep41_contracts`
  is non-empty. The Algorithm 3 mint/burn/clawback running sums
  start landing in `sep41_supply_events` per ledger close. Indexer
  main.go calls both supply-registration helpers (entry +  event)
  and merges the registered observer list for the boot log.
  Closes the wiring gap flagged in #410: the supply pipeline
  (Algorithms 1 + 2 + 3) is now fully end-to-end live in
  production for opted-in deployments.

- **Classic-asset supply observers (trustlines / claimable_balances /
  liquidity_pools / sac_balances) now register with the indexer
  dispatcher.** Second slice of the L2.12a six-observer wiring
  sweep — closes Algorithm 2 (classic credit-asset supply) for
  every component except the SEP-41 event stream. Builds on
  `pipeline.RegisterSupplyEntryDecoders` from the previous PR;
  three new conditional registrations:
    - `[supply] watched_classic_assets` non-empty → trustlines,
      claimable_balances, AND liquidity_pools all attach (an
      operator who watches an asset MUST get every component or
      Algorithm 2's sum is wrong);
    - `[supply.sac_wrappers]` non-empty → sac_balances attaches
      independently (cross-check-only deployments don't need the
      classic trio).
  Boot log now reports the three watched-set sizes alongside the
  registered observer list. Empty per-observer watched-set leaves
  that observer unregistered — no behaviour change for
  deployments that haven't opted in. New
  `internal/pipeline/dispatcher_test.go::TestRegisterSupplyEntryDecoders_*`
  sub-tests pin the classic-trio attachment, the SAC-only path,
  and the all-five full-config path.

- **`internal/sources/accounts` (LCM AccountEntry observer) is now
  registered with the indexer dispatcher.** First slice of the
  L2.12a six-observer wiring sweep (the supply observers compiled
  and had unit tests but no production code path called
  `disp.AddEntryDecoder` for any of them; the supply pipeline
  consequently read empty hypertables in production despite the
  algorithms being correct). New
  `pipeline.RegisterSupplyEntryDecoders(disp, cfg.Supply)`
  attaches each opt-in observer based on the corresponding
  watched-set:
    - `accounts` ← `[supply] sdf_reserve_accounts` (this PR);
    - trustlines / claimable_balances / liquidity_pools / sac_balances
      / sep41_supply — follow-up PRs.
  The watched-set itself is the on/off switch — empty list leaves
  the observer unregistered, no behaviour change for deployments
  that haven't opted in. Empty G-strkey inside a non-empty list
  fails-loud at startup so an operator sees the misconfiguration
  before processing begins. Boot log emits the registered set so
  operators see which observers are live without consulting
  config. New
  `internal/pipeline/dispatcher_test.go::TestRegisterSupplyEntryDecoders_*`
  pins the no-op-when-empty / registers-when-watched / rejects-
  empty-strkey transitions. The persistence side
  (`internal/pipeline/sink.go`) was already wired for this
  observer's Observation type, so once it registers,
  `account_observations` rows start landing on every matching
  ledger close.

### Fixed

- **`ratesengine_oracle_resolution_seconds` is now actually
  emitted.** The metric was registered in `internal/obs` and the
  `ratesengine_oracle_stale` alert
  (`deploy/monitoring/rules/divergence.yml`) depends on it — the
  expression is
  `(time() - oracle_last_update_unix) > 10 * oracle_resolution_seconds`.
  The denominator was never set in production, so Prometheus's
  missing-metric semantics meant the alert either evaluated `>0`
  (always fired once a single update landed) or stayed
  unevaluatable depending on operator scrape config — neither was
  the intended behaviour. `pipeline.BuildDispatcher` now sets the
  gauge per oracle source at registration time, using each
  source's published `DefaultResolutionSeconds` constant:
  reflector-{dex,cex,fx} = 300 s (5 min), redstone = 86400 s
  (24 h), band = 60 s (1 min). The metric label is `source`, so
  each reflector variant gets its own gauge entry. Same
  audit-finding shape as the supply cross-check / trace_exporter
  / cdn_enabled gaps — alert + metric defined but never emitted
  by production code.

- **CORS default AllowedMethods includes POST** — the default
  was set when v1 was a read-only API and never updated as POST
  endpoints landed (`/v1/account/keys`, `/v1/auth/sep10/token`,
  `/v1/price/batch`). The API binary's `CORS(CORSOptions{
  AllowedOrigins: cfg.API.AllowedOrigins})` shorthand was
  silently failing browser cross-origin POST preflights;
  operators had to override `AllowedMethods` explicitly to make
  a wallet-side POST work. New default:
  `{GET, HEAD, OPTIONS, POST}`. Operators who want a stricter
  cross-origin posture set the field explicitly. The doc tag
  on `CORSOptions.AllowedMethods` is updated to match the v1
  surface. New `TestCORS_DefaultAllowedMethodsIncludePOST` pins
  the default-preflight-allows-POST behaviour.

- **Aggregator binary now exposes `/metrics`** — closes a known
  gap surfaced by the half-shipped-config audit. The aggregator's
  Prometheus counters (`ratesengine_aggregator_ticks_total`,
  `_vwap_writes_total`, `_empty_windows_total`,
  `_dropped_trades_total{reason}`, `_triangulations_total{outcome}`)
  registered into `internal/obs` at package init but no HTTP
  listener was mounted, so Prometheus scrapes returned 404 and
  the alert rules in `deploy/monitoring/rules/aggregator.yml`
  (`aggregator_silent`, `aggregator_outlier_storm`,
  `aggregator_class_drop_spike`) could never fire.
  `cmd/ratesengine-aggregator/main.go` now mirrors the indexer's
  `startMetricsServer` pattern: bind `cfg.Obs.MetricsListen`,
  expose `GET /metrics` (Prometheus) + `GET /healthz`, and run
  graceful shutdown after `orch.Run` returns. Empty
  `MetricsListen` logs a warning calling out which alerts won't
  fire — same shape as the indexer warning. The `ObsConfig`
  package doc is updated to drop the "known gap" caveat.

- **`obs.trace_exporter = "otlp"` now fails-loud instead of
  silently no-op'ing** — the fourth half-shipped config field
  caught by the audit-finding wire-up pattern (after F-0008
  `key_rate_limit_per_min` in #384, F-0009 `trusted_proxy_cidrs`,
  and `api.cdn_enabled` in the previous commit). The struct
  field, default (`"none"`), TOML example (`# none | otlp`), and
  validation (`switch o.TraceExporter { case "none", "otlp": }`)
  all advertised OTLP as a working option, but no production code
  imports the OpenTelemetry SDK or sets up a `TracerProvider` —
  any operator who set `trace_exporter = "otlp"` got zero traces
  with no error or warning. Validate() now rejects `"otlp"` with
  a message pointing operators to the truth ("reserved for the
  future tracing rollout and is not yet wired in this build; set
  to \"none\""). When the OTel exporter is wired in
  `cmd/ratesengine-{api,indexer,aggregator}/main.go`, the
  validation case is restored. The doc tag on `Obs.TraceExporter`
  + `Obs.TraceSample` and the `[obs]` block in
  `configs/example.toml` now state the ship truth so the
  auto-generated reference at `docs/reference/config/README.md`
  matches reality. New `validate_test.go` row exercises the
  reject path. No operator code change required for the default
  config (`trace_exporter = "none"` is unchanged).

- **`api.cdn_enabled` now actually gates `s-maxage`** — the third
  half-shipped config field caught by the audit-finding wire-up
  pattern (after F-0008 `key_rate_limit_per_min` in #384 and F-0009
  `trusted_proxy_cidrs` review). `internal/config/config.go` has
  exposed `cfg.API.CDNEnabled` (default `true`) since the early API
  surface design, but `internal/api/v1/server.go` mounted the
  middleware as bare `middleware.CacheControl` — the operator-facing
  knob compiled, defaulted, and was logged, but had no runtime
  effect. New `middleware.CacheControlWithCDN(cdnEnabled bool)`
  constructor: when `false`, drops the `s-maxage` half from cacheable
  routes (`public, max-age=30, s-maxage=60` → `public, max-age=30`
  for current-price / asset-detail; `public, max-age=60, s-maxage=300`
  → `public, max-age=60` for closed-bucket historical). Non-cacheable
  directives (`no-store`, `private, no-cache, must-revalidate`) are
  unchanged because they were never CDN-cacheable. The legacy
  `middleware.CacheControl` symbol is kept as a backwards-compat
  shim that forwards to `CacheControlWithCDN(true)` so test sites
  and any external caller that imported the function don't break.
  `v1.Options.CDNEnabled` plumbs the config into the server;
  `cmd/ratesengine-api/main.go` passes `cfg.API.CDNEnabled` at
  construction. New tests: `TestPolicyForPath_CDNDisabled` (18-row
  matrix verifying the s-maxage drop on every cacheable route) and
  `TestCacheControlWithCDN_FalseDropsSMaxAge` (handler-side
  end-to-end). Operators without a CDN in front of the API set
  `cdn_enabled = false` and the API stops emitting a directive a
  CDN they don't run could later honour.

- **Divergence service is now wired with references by default**:
  the API binary (`cmd/ratesengine-api`) was constructing
  `divergence.NewService` with an empty `References` list, leaving
  the `divergence_warning` envelope flag inert in production —
  surfaced by a 2026-05-01 review pointing at S9.4 of the coverage
  matrix. New `[divergence]` config block in `internal/config/config.go`
  + `cmd/ratesengine-api/main.go` `buildDivergenceReferences()`
  helper. CoinGecko reference is on by default (free tier, no
  auth required) so divergence detection fires out of the box.
  Chainlink reference is opt-in via
  `[divergence.chainlink].enabled = true` plus a non-empty
  `feeds` table mapping pair strings to mainnet AggregatorV3
  feed addresses. `Threshold`, `MinSourcesForWarning`, and
  `PerReferenceTimeoutSeconds` are also surfaced for operator
  control. New tests
  (`cmd/ratesengine-api/main_test.go::TestBuildDivergenceReferences_*`)
  cover the four wiring permutations: defaults / both-enabled /
  Chainlink-enabled-but-empty-FeedMap-skip / all-disabled.
  Boot log now emits
  `divergence service wired reference_count=N references=[...] threshold_pct=...`
  so operators can confirm the active set at startup.

- **Public-flip checklist is 16/16 verified (#342)**: the two
  rows in `docs/operations/public-flip.md` that required
  human-in-the-loop review (the `CLAUDE.md` private-archive
  check and the `docs/discovery/` sensitive-content check) are
  now ☑ with citations. CLAUDE.md got a pattern scan + manual
  spot-checks — 0 private references, 2 non-blocking editorial
  recs noted; `docs/discovery/` got a 9-pattern sensitivity scan
  across all 48 files — 0 hits in credential/PII categories,
  6 benign hits across qualitative categories. Task #78 moves
  from "checklist incomplete" to "execution-ready" — what's
  left is the operator-side cut-over mechanics in
  `public-flip.md §"Cut-over mechanics"` (gh repo create, DNS,
  branch protection, secrets re-create).

- **`api-docs` workflow disabled until public-flip (#262)**: the
  `api-docs` workflow's final step `actions/deploy-pages` requires
  GitHub Pages enabled on the repo, which only happens at
  public-flip time. Until then every push to `main` ran this
  workflow, which always failed at the deploy step (verified
  across 5 consecutive main pushes 2026-04-29 / 2026-04-30) —
  pure CI waste. Switched the trigger to `workflow_dispatch:`
  only with an inline comment naming Task #78 as the
  re-enablement cutover. Re-enable the push trigger as part of
  the public-flip per `docs/operations/public-flip.md §Post-flip`.

- **Coverage matrix: re-baseline the Open list (#340)**: Task #50
  re-baselined the upper per-section rows today, but the
  *Open — implementation pending* summary table at the bottom of
  `docs/architecture/coverage-matrix.md` still listed twenty-one
  items as pending that had actually shipped (S4.1-4 VWAP/TWAP,
  S8.1-2 USD volume + FX, F2.4 circulating-supply, S3.7 CEX
  connectors, S2.4 Chainlink HTTP, S1.4 asset enumeration, X2.2
  /v1/price/tip, X2.3 /v1/observations, X2.6 streaming ×4, X3.1-7
  anomaly + baseline + freeze, F5.3 batch endpoint, #2 SEP-10,
  #9 pkg/client SDK, #10 docs-api pipeline, #24 internal/divergence,
  X1.5 archive-completeness daemon, X1.7 verify-archive
  -fail-on-missed, #21 CHANGELOG + SemVer, #23 release-notes
  template, #26 envelope flag retrofit). All twenty-one verified
  against the current `internal/`, `cmd/`, and `pkg/` tree, then
  moved to *Closed since Phase 1* with the file paths cited as
  evidence. The Open list now contains the **eight items that
  are genuinely outstanding before launch** (X2.5 forex snap;
  Patroni/Redis/HAProxy/Prom/Loki ansible roles; public status
  page; k6 load tests; chaos suite; SEV dry-run record; p95
  proof; public-flip checklist) plus the in-flight Task #53
  Blend Phase 2. Massive accuracy improvement for understanding
  what's actually left before the 2026-06-30 launch.

### Added

- **SEV drill scenarios + framework (#341)**: Coverage matrix
  Validation #20 ("SEV-1/SEV-2 dry-run") needs scripted scenarios
  to exercise the playbook. The sev-playbook.md §8 already
  promised a `docs/operations/drills/` directory holding writeups
  per drill; that directory didn't exist. New:
  `docs/operations/drills/README.md` describing the three-tier
  cadence (monthly tabletop / quarterly chaos / annual DR), the
  drill protocol, and a writeup template; `scenarios/`
  subdirectory holding two canonical tabletop scripts —
  `sev1-timescale-primary-failover.md` (primary disk-full →
  failover decision; exercises `timescale-primary-down.md`) and
  `sev2-source-decoder-regression.md` (post-protocol-upgrade
  soroswap decode errors; exercises `decode-errors.md`). Each
  scenario carries Initial conditions, Trigger event, Injection
  timeline (per-minute beats), Expected response per the
  playbook, Validation criteria (pass/partial/fail per row), and
  Common gaps surfaced from prior runs. Operator-executable: the
  next monthly tabletop has scripts to run.

- **Blend WASM audit — Phase 1 + partial Phase 3 (#339)**: Task
  #53 advances substantively without r1 access. Pool Factory's
  9 lifetime `Symbol("deploy")` events fetched via
  stellar.expert API, bodies decoded with `scripts/dev/decode-scval`,
  yielding the canonical list of all 9 Blend pool addresses with
  deploy timestamps (2025-04-14 → 2025-11-25). Current WASM hash
  for every pool fetched via `/explorer/public/contract` API:
  all nine share `a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e`.
  WASM bytes downloaded with `stellar contract fetch --network
  mainnet` (57 KiB) and archived as audit evidence at
  `docs/operations/wasm-audits/evidence/blend/`. Decoder-
  compatibility verified: `strings` finds all three event topics
  (`new_auction`, `fill_auction`, `delete_auction`) and all three
  AuctionData field names (`bid`, `lot`, `block`) the
  `internal/sources/blend` decoder switches on; `stellar contract
  info interface` confirms canonical Blend pool surface. **Phase
  2 (per-pool `wasm-history` walk on r1) still required** before
  the `BackfillSafe: false → true` flip — current Phase-3 read
  shows only the *current* WASM, not whether any pool was
  upgraded mid-life. Audit doc updated with both phases'
  results; status moves from "Pending" to "Phase 1 complete;
  Phase 3 partial".

- **CI promtool validation (#319)**: `make monitoring-check`
  (Prometheus rule-file validation via `promtool check rules`)
  is now wired into both `bash scripts/dev/verify.sh` (graceful-
  skip when promtool isn't installed locally) and a dedicated
  `monitoring-rules` job in `.github/workflows/ci.yml` (installs
  promtool from the Prometheus GH release). The alert rules
  shipped in #294 / #295 / #313 had been validated by visual
  review only — broken PromQL or undefined recording-rule
  references would have surfaced at Prometheus reload time on
  a production node. Closes the gap.

- **`CLAUDE.md` "Add a new supply observer" recipe (#323)**: the
  six supply observers shipped through Tasks #54–#56 follow a
  pattern (`doc.go` + `dispatcher_adapter.go`, three possible
  dispatcher hooks per the LCM/Op/Event split) that has no entry
  in the task-recipe section of the agent orientation file. A
  future agent adding a 7th observer would have nowhere to look.
  Recipe links to `docs/architecture/supply-pipeline.md` for the
  full shape and names the three hook variants explicitly.

### Fixed

- **Supply-refresh runbooks acknowledge `asset_key` label (#320)**:
  PR #314 added the `asset_key` dimension to
  `ratesengine_aggregator_supply_refresh_total`, but the
  `supply-refresh-error-dominant.md` runbook still claimed
  *"Logs carry asset key; metric doesn't"* — operators following
  the runbook would skip a useful per-asset diagnosis path and go
  straight to journald. Both supply-refresh runbooks now show how
  to split by `asset_key` from `/metrics` (and the equivalent
  PromQL for dashboards).

- **Stellar runbooks acknowledge inert metrics (#321)**: four
  alerts in `deploy/monitoring/rules/stellar.yml` (`core-lag`,
  `core-peers`, `rpc-lag`, `archive-publish`) reference metrics
  produced by stellar-core / stellar-rpc / the
  stellar-core-prometheus-exporter — all three were removed from
  r1 on 2026-04-23. Each runbook now opens with a *Deployment
  posture* callout explaining the alert is inert on r1 today and
  retained for Phase-3 (Tier-1 validator rollout per ADR-0004).
  Alerts catalog gets a matching note above the Stellar / node
  alerts section.

- **Ingestion runbooks point `rpc-probe` at an external endpoint
  (#322)**: six runbooks (`all-ingestion-down`, `decode-errors`,
  `insert-errors`, `oracle-stale`, `source-stopped`,
  `orphan-events`) instructed on-call to run probes against
  `http://stellar-rpc:8000` — but stellar-rpc was removed from r1
  on 2026-04-23, so the URL no longer resolves on-box. Each now
  points the probe at a public stellar-rpc endpoint
  (`https://mainnet.sorobanrpc.com`) with a one-line note
  explaining the architectural shift. The `all-ingestion-down`
  runbook additionally rewrites quick-diagnosis + Mitigation A
  around Galexie + MinIO (the actual r1 upstream).

- **API runbooks now cover SLO burn-rate alerts (#324)**: PR #313
  shipped six SLO burn-rate alerts (`slo_latency_burn_*`,
  `slo_availability_burn_*`) per ADR-0009, all routing to
  `api-latency.md` / `api-5xx.md` — but neither runbook
  acknowledged the new alerts or explained the burn-rate-vs-
  direct-threshold distinction. An on-call operator paging from
  `slo_availability_burn_fast` would land in `api-5xx.md`, see
  only the direct-threshold alerts, and potentially dismiss the
  page as "p95 just nudged a line" rather than "we're burning
  the 99.99 % SLO budget at 14.4× rate." Both runbooks now list
  the burn-rate variants and explain the multi-window pattern.

- **`supply-snapshot-stale` runbook acknowledges the
  aggregator-resident path (#325)**: PR #318 established that
  `asset_supply_history` has two producers (systemd timer +
  aggregator-resident goroutine), but the
  `ratesengine_supply_snapshot_stale` alert tracks only the
  timer-path's `last_success_timestamp` gauge. Deployments
  running exclusively the goroutine path would have this alert
  firing forever despite fresh snapshots landing. New top-of-
  file callout names both paths and points operators at the
  silence-vs-investigate decision.

- **Two more supply-snapshot runbooks acknowledge the
  aggregator-resident path (#326)**: companion to #325. Both
  `supply-snapshot-circulating-zero.md` and
  `supply-snapshot-unit-failed.md` track metrics emitted
  exclusively by the systemd-timer path, so on a goroutine-only
  deployment these alerts silently never fire — a worse failure
  mode than the noisy false-positive in #325. Each carries a
  *Coverage caveat* callout naming the two-path architecture and
  the goroutine-path equivalent signal.

- **`supply-cross-check-divergence` runbook cross-links sibling
  supply alerts (#327)**: the runbook only referenced
  `aggregator-silent.md` and `internal/supply/crosscheck.go`
  under Related, missing four cross-references that an operator
  triaging a divergence routinely needs.

- **`cursor-stuck` runbook upstream is Galexie + MinIO, not
  stellar-rpc (#328)**: the runbook's Mitigation step 1 told
  on-call to *"fix the upstream (stellar-rpc) first"* — but
  stellar-rpc was removed from r1 and the indexer reads ledger
  metadata from Galexie's MinIO output. Mitigation step now
  points at Galexie / MinIO checks.

- **`archive-divergence` runbook deployment-posture callout
  (#329)**: the runbook treated us as an active archive publisher
  ("stop advertising the affected checkpoints", "core-binary bug
  producing a different bucket") — but r1's
  `/srv/history-archive/` is a one-shot stellar-archivist mirror
  with no running publisher since 2026-04-23. Top-of-file callout
  scopes the runbook to "what r1 can actually do today" with
  Phase-3 framing for the publishing path.

- **`host-cpu-high` runbook captive-core context is galexie, not
  stellar-rpc / stellar-core (#330)**: root cause #2 named
  "stellar-rpc or stellar-core host" as the captive-core sites,
  but on r1 today only galexie embeds a captive-core. Galexie
  also doesn't expose `/info`, so the end-state signal changes
  accordingly.

- **`CONTRIBUTING.md` recommends the canonical pre-push gate
  (#331)**: contributors were told to run `make lint && make
  test` before pushing, but the canonical pre-push gate is `make
  verify` — which additionally runs doc-lint, import-lint,
  openapi-url-lint, the integration-build smoke check, and the
  Prometheus rule-file validation wired in #319. First-time
  setup, the workflow's pre-push step, and the Definition of
  Done now all reference `make verify`.

- **`deploy/monitoring/README.md` lists every rule file (#332)**:
  the layout list named 9 rule files but the directory holds 17.
  The `component` label list (used for dashboard grouping) was
  similarly missing four values that alert rules already use
  today. Pure documentation change; alert rules untouched.

- **`configs/ansible/README.md` reflects current default tag set
  (#333)**: the README opened with *"a fully configured Stellar
  archival node running stellar-core, Galexie, stellar-rpc, and
  Postgres 15"* — but `defaults/main.yml` has
  `run_stellar_core: false` and `run_stellar_rpc: false` since
  2026-04-23. A fresh-host bring-up gets Galexie + Postgres +
  MinIO and not the two daemons. Opening summary, first-run-
  bootstrap tag list, the role-overview section, and the running-
  a-subset examples all updated.

- **`deploy/docker-compose/README.md` migration version reference
  (#334)**: README told first-run contributors to expect
  *"migrated to version 8 (dirty=false)"* but the latest
  migration is `0015_create_sep41_supply_events`. Updated to 15
  with a self-correcting hint pointing at
  `ls migrations/*.up.sql | sort | tail -1`.

- **`migrations/README.md` lists every migration (#335)**: the
  Current-migrations table only listed 0001-0004 — eleven
  migrations had shipped without README updates. Each new row
  cites its ADR (0011 / 0021 / 0022 / 0023 / 0019) and the role
  the table plays in the algorithm map.

- **Source READMEs name the dispatcher seam, not the legacy
  `consumer.Source` (#336)**: three source READMEs (`soroswap`,
  `phoenix`, `reflector`) described their `consumer.go` as
  *"implements `consumer.Source`"* — but production routing has
  been via `dispatcher.Decoder` for a while; the only remaining
  consumer of `consumer.Source` is the legacy orchestrator's own
  test file. Future agents reading these would be told to follow
  the legacy pattern that CLAUDE.md invariant #6 explicitly
  forbids.

- **`configs/example.toml` `[stellar]` section explains the
  localhost defaults (#337)**: `core_http_endpoint` and
  `rpc_endpoints` defaulted to `http://127.0.0.1:...` — the right
  values for a Phase-3 archival host running stellar-core +
  stellar-rpc locally, but neither daemon runs on r1. An r1
  operator copying the example would have a config that silently
  drives every diagnostic into a refused-connection error. New
  top-of-section comment names the two postures and points r1
  operators at `https://mainnet.sorobanrpc.com`.

### Added

- **`docs/architecture/supply-pipeline.md` (#318)**: architecture-
  level overview tying together the three-algorithm supply
  derivation, the six observers, the chained-fallback reader
  pattern, the two refresh paths (systemd timer + aggregator
  goroutine), the per-class storage tables, and the failure-mode
  catalog. Mirrors the existing `ingest-pipeline.md` for the
  ingest side. ADRs 0011 / 0021 / 0022 / 0023 each cover one
  slice; the coverage matrix lists rows; this doc is the
  single-source orientation for someone arriving cold.

- **Classic-supply storage integration tests (#317)**: companion
  to #316 covering the four classic-supply hypertables shipped in
  #303 (`trustline_observations`, `claimable_observations`,
  `lp_reserve_observations`, `sac_balance_observations`). Each
  Sum*AtOrBefore method uses the same `DISTINCT ON (...) ORDER BY
  ledger DESC` + `WHERE NOT is_removal` pattern; a SQL regression
  in the DISTINCT ON ordering or the is_removal filter silently
  mis-reports Algorithm 2 components. Four sub-tests walk a
  realistic per-component lifecycle (insert → upsert →
  later-ledger advance → removal) and verify (1) at-or-before
  ledger filter, (2) last-writer-wins semantics, (3) removed-row
  exclusion from sums, (4) `asset_key` WHERE-filter isolation
  across watched assets, (5) per-account / per-contract latest-
  balance lookups for `LockedSet` / issuer-balance use cases.

- **SEP-41 supply storage integration tests (#316)**: covers the
  `Insert → SEP41NetMintAtOrBefore → SEP41KindTotalsAtOrBefore`
  paths through real TimescaleDB via testcontainers-go. The
  Algorithm 3 running sum's SQL (CASE-WHEN sign-flip for
  burn/clawback, FILTER (WHERE event_kind=...) per-kind
  aggregations, contract_id isolation) ships untested at the SQL
  level until this PR — Go-layer defensive guards in #309 catch
  invalid inputs but can't detect a SQL regression that silently
  corrupts the running sum. Two test scenarios: (1) round-trip
  with mint + burn + clawback at different ledgers, verifying
  the sign-flip is correct, the at-or-before filter respects the
  ledger bound, kind-totals split cleanly, and contract_id
  isolation works; (2) i128/NUMERIC round-trip preserves
  precision for values exceeding int64.

- **Coverage-matrix re-baseline (#315)**: walks rows that had drifted
  to "designed / pending" but actually shipped this session.
  Concrete row updates:

  | Row | Was | Now |
  |-----|-----|-----|
  | S2.4 Chainlink | ❌ gap | ✅ verified (#282) |
  | S5.2 ≤30s freshness | 🧪 designed | ✅ verified (#283/#290/#294) |
  | S9.2 p95/p99 latency | 🧪 designed | ✅ verified (synthetic via SLA probe) |
  | S9.1 ≥99.99% uptime | 🧪 designed | ⚠ caveat (probe shipped; production traffic needed for full verification) |
  | F2.1/F2.2 Market Cap / FDV | ⚠ writer pending | ✅ verified (writer end-to-end across XLM + classic + SEP-41) |
  | F2.4 Circulating Supply | ⚠ writer pending | ✅ verified (all three algorithms live) |
  | F2.5 Total Supply | ⚠ writer pending | ✅ verified (mint − burn − clawback live for SEP-41; classic via component sum) |
  | F2.6 Max Supply | ⚠ caveat | ✅ verified (overlay policy + null-for-uncapped per ADR-0011) |
  | F3.1 API p95 | 🧪 designed | ✅ verified (probe + alert) |
  | F3.2 API p99 | 🧪 designed | ✅ verified (umbrella alert) |
  | F3.3 Responsiveness ≥99.9% | 🧪 designed | ⚠ caveat (synthetic + HA topology backs it) |
  | F3.4 Freshness | 🧪 designed | ✅ verified (probe + alert) |

  S3.6 Blend stays ⚠ caveat (audit pending Task #53 — blocked on r1).

- **`asset_key` label on the supply-refresh metric (#314)**:
  extends `ratesengine_aggregator_supply_refresh_total` from
  `(outcome)` to `(asset_key, outcome)` so operators with
  multiple watched assets can chart per-asset bootstrap progress
  + isolate failure modes per asset. Existing alerts in
  `deploy/monitoring/rules/supply-refresh.yml` (#313) are
  forward-compatible — `sum(rate(...))` and `max(timestamp(...))`
  both sum/max over the new label naturally. New
  `supplyRefresherBinding` struct in
  `cmd/ratesengine-aggregator/main.go` pairs the `Refresher`
  with its asset_key at goroutine-construction time;
  `runSupplyRefresh` labels the metric per-tick. Metrics
  reference doc updated.

- **Aggregator supply-refresh alert rules + two runbooks
  (#313)**: closes the operator-visibility gap on the goroutine
  path of the supply refresher (#301 / #307 / #312). Pairs with
  the systemd-timer-path alerts in #295. Two rules in
  `deploy/monitoring/rules/supply-refresh.yml`:
  `_stalled` (P2 page when no `outcome="ok"` increments in
  30 min — wedged goroutine or every-tick-failing) and
  `_error_dominant` (P3 ticket when > 50% of ticks have
  non-ok outcomes for 30 min — split-by-outcome runbook
  identifies the root cause). Two new runbooks under
  `docs/operations/runbooks/` cross-link to the systemd-timer
  equivalents (`supply-snapshot-stale.md`,
  `supply-snapshot-unit-failed.md`) so operators on either
  deployment path land on the right diagnostic flow.

- **SEP-41 aggregator wiring — closes Task #56 (#312)**:
  extends `buildSupplyRefreshers` in
  `cmd/ratesengine-aggregator/main.go` with a third per-asset
  loop alongside XLM (Algorithm 1) + classic
  (Algorithm 2): one `supply.Refresher` goroutine per entry in
  `[supply] watched_sep41_contracts` (Algorithm 3). New
  `supplyAggregatorSEP41Store` adapter projects
  `timescale.SEP41KindTotals` ↔ `supply.SEP41KindTotals` (the
  duplication is necessary to avoid a cyclic import — timescale
  already imports supply for `InsertSupply`). Closes Task #56
  across PRs #309 → #310 → #311 → #312, completing
  ADR-0011's three-domain supply coverage end-to-end. The
  per-tick outcome counter
  (`ratesengine_aggregator_supply_refresh_total{outcome}`) now
  labels for all three algorithms identically. Updated
  `docs/operations/supply-snapshot.md` to reflect shipped
  status across the three asset classes.

- **`StorageSEP41SupplyReader` + `watched_sep41_contracts` config
  (#311 — Task #56 PR 3/4)**: composes the per-kind running sums
  (`Store.SEP41KindTotalsAtOrBefore`, new in this PR — single
  round-trip via SQL `SUM(...) FILTER (WHERE event_kind=...)`)
  with the SAC-balance per-contract lookups for locked-set
  subtraction into a single `SEP41SupplyReader` satisfying the
  existing interface from #199. `AssetBoundSEP41Computer`
  adapts the contract-parameterised `SEP41Computer` to the
  per-asset `SnapshotComputer` shape (mirrors
  `AssetBoundClassicComputer` from #307). New
  `[supply] watched_sep41_contracts` config (C-strkey list) +
  validation. AdminBalance is intentionally `0` in the v1
  reader — operators put admin addresses in `LockedSet.Accounts`
  alongside other locked addresses; the algorithm subtracts them
  equivalently. Pure SEP-41 contracts share the SAC-observer
  storage path by adding their `(contract_id, contract_id)`
  entry to `[supply.sac_wrappers]`.

- **`internal/sources/sep41_supply/` observer + sink wiring
  (#310 — Task #56 PR 2/4)**: SEP-41 supply event observer per
  ADR-0023, plugging into the existing events-based
  `dispatcher.Decoder` hook (NOT `LedgerEntryChangeDecoder` —
  events are not ledger-entry deltas). Operator-watched-contract
  driven via `NewDecoder([]string)` (PR 3/4 wires the operator
  TOML). Match fast-path is `(contract_id ∈ watched_set) AND
  (topic[0] symbol ∈ {mint, burn, clawback})` — `transfer` is
  intentionally NOT matched (transfers move ownership, not
  supply). Decode parses topic-position counterparty (mint/clawback
  → topic[2]; burn → topic[1]) and the i128 amount via
  `scval.AsAmountFromI128`. Sink type-switches on
  `sep41_supply.Event` and routes through
  `Store.InsertSEP41SupplyEvent` (#309). 9 new unit tests cover
  match/skip semantics + decode for all three kinds + i128-safe
  amount handling for values exceeding int64.

- **`sep41_supply_events` hypertable + storage methods (#309 —
  Task #56 PR 1/4)**: migration 0015 creates the
  `sep41_supply_events` hypertable bounded by ADR-0023
  (PK `(contract_id, ledger, tx_hash, op_index, observed_at)`;
  `event_kind` CHECK in `(mint, burn, clawback)`;
  `amount NUMERIC` non-negative). `Store.InsertSEP41SupplyEvent`
  is idempotent on PK conflict (re-running the indexer over the
  same range is a no-op for the running sum).
  `Store.SEP41NetMintAtOrBefore` returns `Σ mint − Σ(burn +
  clawback)` for one contract — the running supply per ADR-0011
  Algorithm 3. Defensive guards reject empty PK columns, invalid
  event kinds, nil/negative amounts before touching the DB. New
  `SEP41EventKind` typed-string enum mirrors the migration's
  CHECK constraint and the discovery sniffer's symbol names.

- **ADR-0023 — SEP-41 supply observer (#308)**: bounds the
  implementation work for Task #56 before code lands. Defines an
  event-stream observer (`internal/sources/sep41_supply/`)
  consuming the existing dispatcher `Decoder` hook with a
  per-contract watched-set filter; aggregates mint/burn/clawback
  amounts into `Σ mint − Σ(burn + clawback)` per ADR-0011
  Algorithm 3. New `sep41_supply_events` hypertable + `Insert*` /
  `SEP41NetMintAtOrBefore` storage primitives. New
  `[supply] watched_sep41_contracts` config + reader composition
  follow the Task #54 / #55 sliced pattern. The 4-PR plan
  (Tasks #67-#70) closes Task #56 and completes ADR-0011's
  three-domain supply coverage.

- **Classic-supply reader composition + aggregator wiring — closes
  Task #55 (#307)**: ships the final piece of ADR-0022.
  `supply.StorageClassicSupplyReader` composes the four
  `Sum*AtOrBefore` primitives from #303 plus the new per-account
  `TrustlineBalanceForAccountAtOrBefore` and per-contract
  `SACBalanceForContractAtOrBefore` lookups into a single
  `ClassicSupplyReader` satisfying the existing interface from
  #199. `supply.AssetBoundClassicComputer` adapts the
  asset-parameterised `ClassicComputer` to the per-asset
  `SnapshotComputer` shape that `Refresher.Tick` expects. New
  `[supply] watched_classic_assets` (CODE-ISSUER list) +
  `[supply.sac_wrappers]` (C-strkey → asset_key map) drive the
  aggregator's classic-supply refresh: `buildSupplyRefreshers`
  spawns one goroutine per watched asset alongside the existing
  XLM-only refresher; the per-tick outcome counter from #301
  (`ratesengine_aggregator_supply_refresh_total`) labels by
  outcome regardless of asset. Closes Task #55 across PRs
  #303 → #304 → #305 → #306 → #307.

- **`internal/sources/{liquidity_pools,sac_balances}/` observers
  (#306 — Task #55 PR 4/5)**: bundles the LP-reserve and
  SAC-wrapped-balance observers per ADR-0022. The LP observer
  emits up to two Observations per pool change (one per asset
  side that's in the watched set); ConstantProduct only at v1
  (the only LP variant Stellar runs today). The SAC observer is
  watched-contract driven via a `map[contract_id]asset_key`
  (PR 5/5 wires the operator TOML), matches the SEP-41
  `Vec(Symbol("Balance"), Address)` key shape, and extracts the
  amount from either i128 or the native SAC's BalanceValue map
  (`amount` field). Unlike the prior two observers, SAC handles
  Removed-variant changes — the operator's contract→asset map
  carries the asset_key independently of the entry body, so
  removed entries emit `IsRemoval=true` rows the reader treats
  as zero balance.

- **`internal/sources/claimable_balances/` observer (#305 — Task #55
  PR 3/5)**: ClaimableBalanceEntry observer following the
  trustlines pattern from #304. Same operator-watched-asset
  config (`[supply] watched_classic_assets`); same dispatcher
  hook (#297). Identity is per-claimable-balance-id (hex of
  `BalanceId.V0`), not per-account, since claimable balances
  aren't tied to an account post-creation. **Removed-variant
  changes are filtered out at v1**: the LedgerKey for a removed
  claimable carries only the BalanceId, not the asset, so we
  can't determine watched-set membership at the observer level.
  Sum query overcount is bounded by the cumulative claimed-but-
  not-recorded volume per watched asset; for circulating-supply
  derivation this is a CONSERVATIVE error (we under-report
  circulating). A writer-side lookup follow-up is documented in
  the package doc if measurable in production.

- **`internal/sources/trustlines/` observer (#304 — Task #55 PR 2/5)**:
  TrustlineEntry observer mirroring the AccountEntry pattern from
  #298. Operator-watched-asset driven via the existing
  `[supply] watched_classic_assets` config (PR 5/5 wires the
  validation in). Match fast-path is type discriminator + asset
  variant + asset_key map lookup — non-classic-credit Trustline
  variants (native XLM, pool-share) are skipped before any decode
  work. Native XLM trustlines route through the AccountEntry
  observer (Algorithm 1); pool-share trustlines route through the
  LP observer in PR 4/5. Indexer-side sink type-switches on
  `trustlines.Observation` and writes to `trustline_observations`
  via #303's `InsertTrustlineObservation`. The observer plugs
  into the existing dispatcher hook (#297) — no `ProcessLedger`
  changes needed.

- **Classic-supply hypertables 0011-0014 + Insert*/Sum* storage
  methods (#303 — Task #55 PR 1/5)**: ships the four migrations
  bounded by ADR-0022 (`trustline_observations`,
  `claimable_observations`, `lp_reserve_observations`,
  `sac_balance_observations`) plus 4 `Insert*Observation` writers
  (last-writer-wins on conflict, mirroring the
  `account_observations` pattern from #299) and 4 `Sum*AtOrBefore`
  read-side primitives (DISTINCT-ON the most-recent row per
  identity-tuple, sum where !is_removal). The Sum* methods are
  what the future `StorageClassicSupplyReader` (PR 5/5) consumes
  to satisfy `ClassicSupplyReader`. Defensive guards at every
  Insert call reject empty PK columns + nil Balance before
  touching the DB. SAC table denormalises asset_key into the row
  so the reader sums by asset without joining a side table — the
  contract → asset mapping is operator-curated and stable
  post-deploy.

- **ADR-0022 — Classic-supply observers (#302)**: bounds the
  implementation work for Task #55 before code lands. Defines
  four observer + storage + reader stacks under
  `internal/sources/{trustlines,claimable_balances,liquidity_pools,sac_balances}/`,
  each mirroring the AccountEntry pattern from ADR-0021 — the
  dispatcher hook from #297 already routes per-tx ledger-entry
  changes through every registered entry decoder, so adding the
  four packages is purely additive. Operator-watched-set driven
  via new `[supply] watched_classic_assets` config; switching to
  "watch every classic asset" is a separate ADR (table-size
  implications). The sliced 5-PR implementation plan ships each
  hypertable populated independently of the reader, so operators
  can audit components via SQL while subsequent PRs land. Once
  shipped, Task #57's aggregator refresher iterates the watched-
  asset list naturally — the existing single-asset path becomes
  the multi-asset case.

- **Periodic supply-snapshot worker in the aggregator — closes
  Task #57 (#301)**: runs the supply-snapshot writer as a
  goroutine inside the aggregator on a configurable cadence,
  replacing the systemd-timer-driven path (#288) for operators
  that have backfilled the LCM observer. New
  `internal/supply/refresher.go` composes ledger lookup +
  computer + inserter into a `Tick`-able unit; the aggregator
  drives it via `runSupplyRefresh` mirroring the baseline-
  refresher pattern. Operator-opted-in via
  `[supply] aggregator_refresh_enabled = true`; cadence is
  `[supply] aggregator_refresh_cadence` (default 5m, validated
  ≥ 30s). Per-cycle outcomes emit as
  `ratesengine_aggregator_supply_refresh_total{outcome}` —
  outcomes are `ok` / `no_ledger` / `no_observation` /
  `compute_error` / `write_error`. The systemd timer (#288)
  remains the path for operators that haven't enabled the
  goroutine; the two paths are mutually exclusive on conflict-
  safe writes (idempotent ON CONFLICT DO NOTHING) but operators
  should disable one when flipping to the other to avoid
  redundant work.

- **LCM-derived readers — closes Task #54 (#300)**: ships
  `supply.LCMReserveBalanceReader` and
  `metadata.LCMHomeDomainResolver`, the two readers that consume
  the `account_observations` hypertable. Wired into both call
  sites with a chained-fallback pattern: live wins when the
  observer has backfilled the watched account; falls through to
  the operator-static config (`[supply.reserve_balances_stroops]`
  / `[metadata.issuer_home_domains]`) when no observation exists
  or a transient storage error fires. The static config blocks
  stay in tree as bootstrap fallbacks; once the observer covers
  the live operator set, balance / home-domain changes flow
  automatically through to the next snapshot / next request
  without operator-edit-and-redeploy. Closes ADR-0021's full
  implementation across PRs #297 / #298 / #299 / #300.

- **`account_observations` hypertable + storage writer + sink wiring
  (#299 — ADR-0021 / Task #54 PR 3/3)**: closes the storage gap left
  by #298. Migration 0010 creates the `account_observations`
  hypertable (7-day chunks; PK `(account_id, ledger, observed_at)`;
  GIN-friendly indexes on `(account_id, observed_at DESC)` and
  `(ledger DESC)` for the two main reader query shapes).
  `Store.InsertAccountObservation` is last-writer-wins on conflict
  (the AccountEntry post-state is monotonic within a ledger so the
  final write is the authoritative state).
  `Store.LatestAccountObservationAtOrBefore` is the read-side
  primitive the next PR's `LCMReserveBalanceReader` /
  `LCMHomeDomainResolver` will consume. The pipeline sink now type-
  switches on `accounts.Observation` and routes to the writer with
  the same panic-recover + per-source-error-counter contract as the
  other event types. Closes the producer half of Task #54; readers
  follow in Task #61 to fully replace the operator-static config maps.

- **AccountEntry observer + `ProcessLedger` integration (#298 —
  ADR-0021 / Task #54 PR 2/3)**: lands `internal/sources/accounts/`
  — the canonical observer implementing the
  `LedgerEntryChangeDecoder` hook from #297. Operator-watched-set
  driven (`NewObserver([]string)`); G-strkeys not in the watched
  list are skipped at `Matches` time before any decode work.
  Emits one `Observation` per matched change (account_id, ledger,
  observed_at, balance_stroops, home_domain, flags, seq_num,
  is_removal). `Dispatcher.ProcessLedger` now walks per-tx
  LedgerEntryChange rows from `tx.UnsafeMeta` (V3 + V4 supported;
  V1/V2 skipped — pre-Soroban metadata doesn't carry the same
  shape) plus the tx-level fee/before/after change blocks.
  Routing path is symmetric with the existing event/op/contract-
  call hooks. Storage writer + `account_observations` migration
  ship in PR 3/3 (Task #60); the readers replacing the static
  config maps follow that.

- **Dispatcher hook for `LedgerEntryChange` deltas (#297)**: starts
  Task #54 / ADR-0021 implementation. Adds the fourth dispatcher
  hook (`LedgerEntryChangeDecoder`) alongside the existing three —
  same first-match-wins / non-fatal-error / per-source-decode-error-
  counted contract. Per ADR-0021 entry changes are high-volume so
  unmatched changes are silently dropped (no `UnmatchedHits` bump,
  unlike `Decoder` events). New `RouteEntryChange` test-harness
  helper symmetric with `Route` / `RouteOp` / `RouteContractCall`.
  Six unit tests cover routing-by-type, first-match-wins, decode-
  error accounting, no-decoder-registered, output flow-through.
  ProcessLedger integration is the next PR (lands alongside the
  first decoder using the hook — `internal/sources/accounts/`'s
  AccountEntryObserver).

- **Supply-snapshot textfile-collector + four alerts + three
  runbooks (#295)**: closes the operator-visibility gap on the
  daily supply-snapshot writer (#288). Mirrors the SLA-probe
  pattern from #293/#294. New `internal/supply/textfile.go` emits
  per-asset gauges (`total_xlm`, `circulating_xlm`, `max_xlm`,
  `ledger`, `observed_at_seconds`) plus a `unit_failed` /
  `last_success_timestamp` pair the alerts key on. Failure path
  emits a fail-marker textfile (no `last_success_timestamp`) so the
  staleness alert keys on the previous-scrape value. Alerts:
  `_unit_failed_alert` (P3 ticket), `_stale` (P3 at 36 h) /
  `_critical_stale` (P2 page at 72 h), and
  `_circulating_zero` (P2 page — ADR-0011 invariant violation
  signal). Three new runbooks. Operator-toggled via
  `TEXTFILE_OUTPUT` env-var; empty default behaves exactly like
  #288.

- **SLA-probe alert rules + four runbooks (#294)**: closes the
  alert-rules-tracked-as-follow-up note in #293. Ships
  `deploy/monitoring/rules/sla-probe.yml` with four rules —
  `_p95_breach` (page on > 200 ms sustained 30 min),
  `_freshness_breach` (page on > 30 s sustained 30 min),
  `_unit_failed_alert` (umbrella ticket for any breach kind), and
  `_stale` (page when no successful run in 90 min — 6× the
  15-min cadence). Each alert has a per-runbook entry under
  `docs/operations/runbooks/sla-probe-*.md` and a row in
  `docs/operations/alerts-catalog.md`.

- **`-textfile-output` flag on `ratesengine-sla-probe` (#293)**:
  follow-up to #283 / #290. Writes the per-run latency / availability
  / freshness / sample-count / verdict values as a Prometheus
  textfile (atomic `<path>.tmp`-then-rename) so node_exporter can
  scrape them via the textfile_collector. Metric set:
  `ratesengine_sla_probe_latency_ms{endpoint,quantile}`,
  `ratesengine_sla_probe_availability_pct{endpoint}`,
  `ratesengine_sla_probe_freshness_sec{endpoint}`,
  `ratesengine_sla_probe_samples{endpoint}`,
  `ratesengine_sla_probe_run_duration_seconds`,
  `ratesengine_sla_probe_unit_failed`,
  `ratesengine_sla_probe_last_pass_timestamp` (only on pass — the
  staleness alert keys on previous-scrape value when current run
  fails). Systemd service updated with optional `TEXTFILE_OUTPUT`
  env-var; `ReadWritePaths` allows writes to the standard
  textfile_collector dir. Alert rules tracked as a separate
  follow-up — the metric set is shipped.

- **ADR-0021 — AccountEntry observer for live home-domain +
  reserve-balance tracking (#292)**: bounds the implementation work
  for Task #54 before code lands. Defines a fourth dispatcher hook
  (`LedgerEntryChangeDecoder`), a canonical observer in
  `internal/sources/accounts/` driven by operator-watched-set config,
  a new `account_observations` hypertable, and two readers
  (`metadata.LCMHomeDomainResolver` + `supply.LCMReserveBalanceReader`)
  that replace the operator-static `[metadata.issuer_home_domains]`
  and `[supply.reserve_balances_stroops]` config blocks once the
  observer has backfilled. The two operator-static maps stay in tree
  as fallbacks while the live data catches up. Once shipped, Task
  #57 (periodic supply-snapshot worker) becomes implementable — the
  aggregator can refresh snapshots per tick rather than per cron-
  fire, and the systemd timer (#288) becomes redundant.

- **systemd timer + service + runbook for the SLA probe (#290)**:
  closes the operator-side gap left by #283. Ships
  `deploy/systemd/sla-probe.{service,timer}` (every 15 min + 2 min
  jitter — strikes the balance between SEV-2 detection requirement
  ≤ 30 min and the anonymous-tier rate budget) plus
  `docs/operations/sla-probe.md`. Exit-1-on-SLA-breach surfaces via
  `systemctl is-failed`; node_exporter's `--collector.systemd`
  picks the failure up so the existing systemd-unit-failed alert
  pattern covers it. Today the probe writes to journald only — the
  textfile-collector + alerting integration is the additive
  follow-up.

- **systemd timer + service + runbook for the supply-snapshot writer
  (#288)**: closes the operator-side gap left by #285. Ships
  `deploy/systemd/supply-snapshot.{service,timer}` (daily 04:42 UTC
  + jitter, spaced after the existing archive-completeness 02:17 and
  verify-archive-tier-a 03:23 timers) plus
  `docs/operations/supply-snapshot.md` covering the `[supply]` config
  block, the SDF-reserve-move update procedure, the dry-run
  pre-flight, and the v1 asset-class scope. Daily cadence is correct
  for now — values change only when operator config changes (a few
  times per year). When Task #54's LCM-derived reader ships and the
  writer becomes goroutine-resident in the aggregator, this systemd
  unit becomes redundant.

- **XLM supply-snapshot writer via `ratesengine-ops supply snapshot`
  (#285)**: closes the write half of the supply pipeline. Read half
  shipped in #277 left `/v1/assets/{id}` F2 fields null because no
  producer was populating `asset_supply_history`; this PR plugs the
  gap for native XLM (Algorithm 1 per ADR-0011). New
  `ConfigReserveBalanceReader` satisfies `supply.ReserveBalanceReader`
  from operator-supplied balances; new `[supply]` config block carries
  `sdf_reserve_accounts` + `reserve_balances_stroops`. Writer-start
  validates every configured account has a balance entry — silently
  treating an unknown account as zero would publish an over-stated
  circulating supply, the exact failure mode ADR-0011 prohibits.
  Reserve balances are operator-managed for now (a few SDF moves per
  year); the LCM-AccountEntry-observer follow-up replaces the static
  map with a live reader. Drive-by: extended
  `internal/config/schema.go` to recurse into `[]struct` fields so
  `docs-config` emits per-element rows for slices of structs.

- **`/v1/chart` endpoint per Freighter RFP (#284)**: adds
  `GET /v1/chart` matching the Freighter RFP V1 chart contract
  exactly: `(timeframe, granularity, price_type) → points[]`. ADR-0020
  documents the decision. New storage method `HistoryPointsInRange`
  adds a `[from, to)` bucket bound on top of the existing closed-
  bucket guard — no CAGG / migration changes. Default-granularity
  table follows the RFP: 1h→1m, 24h→15m, 1w→1h, 1mo→4h, 1y→1d, all→1d;
  operators can override granularity explicitly. `price_type=twap` is
  reserved and returns 400 today — flipping to 200 is gated on
  shipping a TWAP CAGG. Coverage matrix row F1.3 (Historical Price
  Chart) moves from partial to served.

- **Executable SLA-evidence CLI `cmd/ratesengine-sla-probe` (#283)**:
  drives load against a deployed Rates Engine API and reports per-
  endpoint p50 / p95 / p99 latency, freshness against the price's
  `observed_at`, and availability — with a pass/fail verdict against
  the RFP-stated SLA targets (p95 ≤ 200ms, p99 ≤ 500ms, freshness
  ≤ 30s, availability ≥ 99.9%). JSON or text output; exit code 1 on
  any SLA violation so it slots into CI / scheduled-job pipelines and
  trends over time. Closes Codex medium-7 / coverage-matrix rows
  S5.2, S9.1, S9.2, F3.1-F3.4 — the executable evidence the RFPs /
  proposal asked for. Remaining rows (HA posture, SEV detection time)
  need a production deployment to measure, not a pre-launch CLI.

- **Chainlink HTTP divergence reference (#282)**: closes Codex
  high-3 (Chainlink-named-but-not-implemented). Adds a
  `divergence.Reference` backed by Chainlink Data Feeds via
  off-chain Ethereum JSON-RPC reads — Stellar joined Chainlink
  Scale in 2025/2026 but no Soroban Data Feeds contracts are live
  on mainnet at audit time, so the bytes live on Ethereum + L2s.
  Reference does `eth_call` against the AggregatorV3 contract's
  `latestAnswer()` view function (selector `0x50d25bcd`), decodes
  the int256 (two's-complement aware), applies per-feed decimals,
  and optionally inverts. Role: divergence cross-check ONLY —
  Chainlink does NOT contribute to VWAP/TWAP; its values surface
  as `flags.divergence_warning` on `/v1/price` when our aggregated
  price diverges beyond threshold. `FeedMap` operator-curated;
  empty yields `ErrAssetUnsupported` per pair.

- **Coverage-matrix re-baseline (#281)**: closes Codex medium-1 +
  Task #50. The matrix had drifted in both directions — rows
  marked "designed / impl pending" had shipped (triangulation, SSE
  streams, batch price, OHLC CAGGs, SEP-40 endpoints, supply
  read-path, volume_24h_usd), and rows marked "verified" had
  quietly become operational gaps (Chainlink, Blend prior to #275,
  supply writer). Rewrites every materially-stale row to the
  as-of-2026-04-30 reality. Net 13 row-state corrections.

- **Triangulation API reader, `flags.triangulated` (#280)**: reader
  half of the F-0014 / Codex medium-3 fix; pairs with #279.
  When `/v1/price`'s Timescale lookup returns `ErrPriceNotFound`
  (the steady-state for triangulated-only pairs like XLM/EUR via
  XLM/USD × USD/EUR), the handler now consults a
  `TriangulatedPriceLooker` fallback that reads the Redis VWAP
  value AND the provenance marker that #279 added on the writer
  side. Marker present → synthesised `PriceSnapshot` with
  `flags.triangulated=true`; marker absent → falls through to the
  original 404. Direct-VWAP cache reads are still gated to
  Timescale; the fallback only activates for the triangulated
  case so the source-of-truth contract is preserved.

- **Triangulation provenance marker, writer half (#279)**: writes
  `cachekeys.VWAPProvenance(base, quote, window) = "triangulated"`
  alongside the value key when the orchestrator's triangulator
  produces an implied VWAP. Per-pair direct refresh does NOT write
  the marker — absence == direct (or unknown), which the read side
  treats as `flags.triangulated=false`. Marker-write failure logs
  WARN but does not roll back the value write; the implied VWAP is
  correct either way.

- **`volume_24h_usd` on `/v1/assets/{id}` (#278)**: closes Codex
  Freighter-V2 high-1 trailing item. Adds the field end-to-end:
  new `Volume24hUSDForAsset` storage method sums
  `prices_1m.volume_usd` over pairs where the asset appears as
  base OR quote in the trailing 24h window (CAGG-served, 1440
  buckets max — cheap); new `VolumeReader` interface populates
  `AssetDetail.VolumeUSD24h`. Independent of the Supply path so
  volume serves even when supply isn't yet wired (and vice versa).

- **Supply snapshot reader wired (#277)**: closes audit F-0020 +
  Codex Freighter-V2 high-1. The API binary was leaving
  `v1.Options.Supply` nil, dead-coding the F2-fields path entirely.
  This change populates `total_supply` / `circulating_supply` /
  `max_supply` / `market_cap_usd` / `fdv_usd` / `supply_basis` on
  `/v1/assets/{id}` whenever the asset has a snapshot in
  `asset_supply_history`. No-snapshot keeps the F2 fields null
  and the asset-detail body still serves cleanly per ADR-0011
  ("we don't fabricate"). Read half only — the write half landed
  separately in #285.

- **Blend WASM-audit doc scaffold (#276)**: sets up the per-source
  audit log under `docs/operations/wasm-audits/blend.md` with the
  mainnet contract list (Pool Factory + Backstop) cross-referenced
  against the blend-contracts-v2 deploy manifest, the decoder-
  expectations table mirroring `internal/sources/blend/`, the
  4-phase audit plan (enumerate → walk → review → flip), and the
  failure-mode checklist (topic[0] rename, AuctionData field
  rename, i128 type drift, new auction_type discriminant). Status
  stays `pending`; `BackfillSafe` stays `false`. The follow-up PR
  completes Phases 1-4 and flips the flag.

- **Blend wired into dispatcher + registry + indexer sink (#275)**:
  final wiring step for the Blend integration. After this PR an
  operator who lists `blend` in `ingestion.enabled_sources` gets
  full live ingest of Blend auction events. Adds a new
  `ClassLending` taxonomy entry to `internal/sources/external`
  alongside `ClassExchange` / `ClassAggregator` / `ClassOracle` /
  `ClassAuthoritySanity` — Blend doesn't fit any existing class
  (not exchange, not aggregator, not oracle, not authority-sanity).
  `BackfillSafe: false` until #53 audit completes.

- **Blend auction storage layer (#274)**: migration 0009 creates
  the `blend_auctions` hypertable (1-day chunks; same shape as
  trades + oracle_updates) keyed on
  `(ledger, tx_hash, op_index, ts)`. `auction_type` SMALLINT with
  `CHECK 0..2`, `event_kind` TEXT with `CHECK ('new','fill','delete')`,
  per-variant fields nullable per lifecycle event. `bid` / `lot`
  JSONB arrays of `{asset, amount}` with stringified i128 amounts
  preserving full precision through the JSON boundary per
  ADR-0003. Three insert methods on `*timescale.Store` —
  `InsertAuctionNew` / `InsertAuctionFill` / `InsertAuctionDelete`.

- **Blend auction-event decoder skeleton (#273)**: first step of
  the Blend integration committed in the Stellar RFP + ctx-
  proposal price-aggregation scope. Per
  `docs/discovery/dexes-amms/blend.md` Blend is **not** a spot
  trading venue — we index for directional / state-change signals,
  not VWAP. Ships the package skeleton and the auction-event
  decoder surface (new_auction / fill_auction / delete_auction);
  follow-up PRs added storage (#274) + dispatcher wiring (#275) +
  the audit doc (#276).

- **Audit remediation wave for the 2026-04-29 cold adversarial
  audit (#272)**: closes 20 of the 31 findings raised in the audit
  workspace (`docs/audit-2026-04-29/`). Mix of correctness fixes,
  monitoring truth, public-contract repair, and docs-truth
  alignment. Highlights: F-0008 wired
  `api.key_rate_limit_per_min` into a subject-aware authenticated
  bucket (anonymous and authed tiers now use distinct buckets);
  F-0028 + F-0031 closed via complementary correctness fixes.
  See `docs/audit-2026-04-29/findings/` for the per-finding ledger.

- **`extract-wasm-from-galexie` ratesengine-ops subcommand (#271)**:
  extracts raw WASM bytes for one or more contract-code hashes by
  walking the local galexie LCM archive — the truer source than
  RPC `getLedgerEntry` because it (1) works for evicted WASMs
  (TTL-expired bytes are no longer in active ledger state but ARE
  preserved in galexie LCM), (2) doesn't depend on public-RPC
  retention, (3) runs offline against r1's full archive. Companion
  to `wasm-history`: walk first to enumerate every hash that ever
  ran on each contract, then run extract to pull the bytes for the
  older (likely-evicted) versions. Parallel range partitioning;
  per-LCM scan picks `LedgerEntryChange` of type Created or
  Updated. Also adds the v2-audit template doc.

- **systemd units for `ratesengine-{indexer,aggregator,api}` (L4.13)**:
  long-running `Type=simple` service files for the three runtime
  binaries. Hardened (`ProtectSystem=full`, `PrivateTmp`, etc.),
  restart-on-failure with backoff, after-graph respects the
  postgres + redis + indexer dependency chain. Doesn't include
  Postgres/Redis/binary deploy — that's still operator-side. The
  bringup doc already forward-referenced these by name; this PR
  ships the actual files. Slot under `deploy/systemd/` alongside
  the L4.12 verify-archive timer + the existing
  `archive-completeness.{timer,service}`.

- **verify-archive systemd timer (L4.12)**: nightly Tier A
  chain-link integrity check on R1 per the ADR-0016 per-region
  trust model + the `archival-node-bringup.md` schedule
  (`R1: Tier A nightly`). Ships
  `deploy/systemd/verify-archive-tier-a.{timer,service}` —
  fires at 03:23 UTC + 10m jitter (placed AFTER the daily
  archive-completeness verify at 02:17, so missing-file gaps
  surface there first). 8h max-runtime cap based on the
  parallel-chunk run profile observed today (5h47m for the full
  archive on 8 workers). Two new Prometheus alerts:
  `ratesengine_verify_archive_unit_failed` (P3, ticket — last
  run failed) and `ratesengine_verify_archive_run_stale` (P2,
  page — no clean run in 36h+); both source from
  node_exporter's `--collector.systemd` so no application-side
  metrics work was needed. Two runbooks shipped. Backlog row
  L4.12 added.

- **`external.Metadata.Subclass` for CEX/DEX/FX diversity (L2.6
  follow-up)**: closes the gap noted in #259 — the existing `Class`
  enum lumps CEX + DEX both under `ClassExchange`, which under-
  counted diversity per the ADR-0019 worked example. New `Subclass`
  field partitions ClassExchange into `cex` / `dex` / `fx`. The
  orchestrator's `distinctSourceClassCount` now keys on the
  `Class:Subclass` composite, so:
  - two CEXes (binance + coinbase) → 1 bucket
  - CEX + DEX (binance + soroswap) → 2 buckets ✅ matches ADR
  - CEX + DEX + FX → 3 buckets
  - DEX + Oracle → 2 buckets (cross-parent-class)
  Sources outside ClassExchange leave Subclass blank — their
  parent Class already captures the economic distinction.

- **Source-class registry lookup for confidence diversity factor
  (L2.6 follow-up)**: the orchestrator's `distinctSourceClassCount`
  now consults `external.Lookup(source).Class` instead of using
  the source name as a proxy. The diversity factor reads "two
  CEXes = 1 class" (correct) and "CEX + Oracle = 2 classes"
  (correct) where before it would have read both as equally
  diverse. CEX-vs-DEX is still collapsed under `ClassExchange` —
  the existing taxonomy doesn't split them; a follow-up that adds
  a `Subclass` field to `external.Metadata` would close the gap.

- **Operator-tunable Phase 2 freeze thresholds (L2.7 follow-up)**:
  the ADR-0019 Phase 2 freeze condition's three thresholds —
  `confidence_max_freeze` (0.10), `z_score_min_freeze` (5.0),
  `source_count_max_freeze` (1) — are now surfaced as
  `[anomaly.phase2]` TOML knobs. Defaults match the package-level
  values from #256 so unset operators see no behaviour change.
  Partial overrides merge with defaults (`Phase2Thresholds.withDefaults`)
  so an operator who only wants to tighten one signal doesn't have
  to restate the others. Validation runs at startup —
  out-of-range values surface clear errors instead of silently
  disabling the gate. New `DefaultPhase2*` package constants
  document the canonical values; tests cover boundary cases plus
  partial-override merging.

- **Bootstrap confidence cap (L2.9)**: per ADR-0019 §"Bootstrap
  policy", assets with fewer than 30 days of history have their
  confidence score hard-capped at 0.5 regardless of how healthy
  every other factor reads. Implemented as a post-combiner clamp
  in `confidence.Compute`: when `BaselineAgeDays < 30` (or the
  `-1` "no baseline yet" sentinel), the cap fires. The cap is a
  ceiling, not a floor — naturally-low confidence (single-source,
  low liquidity) still reads through. New constants
  `BootstrapDays = 30` and `BootstrapConfidenceCap = 0.5` document
  the threshold. The class-average baseline + auto-classify
  pieces of L2.9 are deferred to a follow-up.

- **Phase 2 freeze policy — 3-signal AND (L2.7 closes)**: per
  ADR-0019 §"Freeze policy", the orchestrator now runs a second
  freeze layer alongside Phase 1: `confidence < 0.10 AND z_score >
  5.0 AND source_count <= 1`. All three signals must agree —
  catches the USTRY-shape attack pattern (single source, large
  deviation, confidence-killing combination) without firing on
  legitimate market events (those have multi-source corroboration).
  Refactored `refreshPairWindow`: confidence now computes BEFORE
  the VWAP cache write, so a Phase 2 freeze leaves the prior
  bucket's value intact in cache (same LKG-preserving semantic
  as Phase 1). The freeze marker carries
  `Reason="phase2:3_signal_AND confidence=… z=… sources=…"` so
  log lines + Redis marker JSON make the source legible without a
  new wire field. Class label on
  `ratesengine_anomaly_freeze_engaged_total` consistent with
  Phase 1 (uses the same Checker's classifier when wired). New
  exported `Checker.ClassOf` for that consistency.

- **Confidence score on `/v1/price` envelope (L2.6 closes)**: API
  reads the cached `confidence:<base>:<quote>:<window>` Redis key
  written by the aggregator and surfaces both the score
  (`confidence` ∈ [0, 1]) and its decomposition (`confidence_factors`)
  on the response data object per ADR-0019. New `ConfidenceLooker`
  interface; production wiring is `redisConfidenceLooker` in the
  API binary that JSON-decodes the cached `confidence.Score`.
  Cache misses + read errors leave the fields off the wire
  (`omitempty`) — clients that gate on confidence treat absence as
  "unknown", not "low". Closes L2.6 across 4 PRs: math primitive
  (#252), orchestrator compute + cache write (#253), cross-oracle
  divergence wiring (#254), API surface (this PR).

- **Cross-oracle divergence wired into confidence (L2.6 slice 3)**:
  the orchestrator's confidence step now reads `div:<asset>` from
  Redis (the cache the divergence worker writes via
  `Service.RefreshPair`) and feeds the cached `DivergencePct` into
  `confidence.Inputs.CrossOracleDivergencePct` when
  `SuccessCount >= 2`. Single-source cached results are ignored
  (pass the "no data" sentinel — guards against scoring one
  reference's hiccup as a multi-source signal). Best-effort:
  `divergence_read_error` / `divergence_decode_error` outcomes
  surface on the existing
  `ratesengine_aggregator_confidence_compute_total` counter and
  the confidence step continues with the neutral sentinel rather
  than blocking on a Redis blip. Two new tests confirm wiring
  (within-1% cached → CrossOracle factor 1.0, no cache → 0.7
  neutral) and the SuccessCount<2 ignore policy.

- **Confidence score wired into the orchestrator (L2.6 wire-up
  slice)**: per-tick confidence-score compute alongside VWAP
  publishing. New `BaselineSource` interface on `orchestrator.Config`
  reads the cached `MultiBaseline` for z-score lookup. After each
  successful VWAP cache write, the orchestrator computes a return %
  vs the prior tick's VWAP, runs `MultiBaseline.MaxZScore`, gathers
  source count + class count + USD-quote volume + baseline age, and
  writes the JSON-encoded `confidence.Score` to Redis at
  `confidence:<base>:<quote>:<window>`. Confidence is enrichment,
  not a publish gate — baseline-source errors / Redis blips on the
  confidence path are logged + counted but never block the VWAP
  publish itself. New cache key `cachekeys.Confidence` /
  `ConfidenceTTL` (matches VWAP TTL). New Prometheus counter
  `ratesengine_aggregator_confidence_compute_total` labelled by
  `{ok, skipped, baseline_missing, marshal_error, write_error}`.
  Cross-oracle divergence input still passes the "no data" sentinel
  pending the next slice (which wires the `div:<asset>` Redis key
  read). API hot-path read of the confidence cache key follows
  separately.

- **Multi-factor confidence score primitive (L2.6 math slice)**:
  pure-Go `internal/aggregate/confidence` package implementing the
  ADR-0019 §"Multi-factor confidence score" combiner. Six factors
  per the ADR shape: `ZScoreFactor` (sigmoid 1.0 at z=0, ~0.5 at
  z=5, ~0 at z=10), `SourceCountFactor` (logistic; n=3 → 0.5;
  n≥6 → ~1.0), `DiversityFactor` (step: 0/0.5/1.0), `LiquidityFactor`
  (log-saturating; $1K → 0, $100K → 1.0), `CrossOracleFactor`
  (piecewise: 1.0 within 1%, exponential decay beyond; negative
  input is the "no cross-oracle data" sentinel returning the ADR's
  0.7 neutral), `BaselineQualityFactor` (linear 0.5 → 1.0 over
  30d). Combined via weighted geometric mean with `1/sum(weights)`
  normalisation so weight magnitude doesn't change scale. Compute
  is numerically stable (sums log-factors, exp at the end) so
  near-zero factors don't underflow. 21 tests pin the per-factor
  shapes, the dominating-factor behaviour, and edge cases (all-
  zero weights, full bootstrap, extreme inputs). Orchestrator
  wire-up follows in the next slice.

- **Multi-window baseline storage + refresh integration (L2.8
  closes L2.8)**: migration 0008 adds `median_1d/mad_1d/n_1d` and
  `median_7d/mad_7d/n_7d` to `volatility_baseline_1m` (the existing
  median/mad/sample_count columns hold the 30d baseline; the new
  pairs are nullable for the bootstrap-on-this-scale case).
  `Store.UpsertBaseline` and `LatestBaseline` now carry a
  `baseline.MultiBaseline` end-to-end; pre-flight checks include
  Day30 non-nil. `Store.TimedVWAPsForPair1m` returns time-stamped
  VWAPs so the refresher can apply `SplitByLookback` to derive the
  three sub-windows from one read. `baseline.Sink` updated to take
  a MultiBaseline; aggregator binary's adapters track. The 30d
  bootstrap (Day30 nil) outcome surfaces as
  `OutcomeNotEnoughSamples` (no row written); per-window bootstrap
  (Day1/Day7 nil while Day30 valid) is OK and persists with NULL
  columns. Closes L2.8 across 2 PRs — the anomaly-evaluator
  consumer of `MultiBaseline.MaxZScore` lands with L2.7.

- **Multi-window baseline safeguard (L2.8 math slice)**: per
  ADR-0019 §"Multi-window safeguard against frog-boiling" — a
  coordinated attacker who slowly drifts an asset over weeks would
  defeat the 1d window (median tracks the drift) but the 30d
  window (still includes pre-attack data) flags the drifted price
  as anomalous. New `baseline.MultiBaseline` holds three
  independent baselines at 1d/7d/30d lookbacks; `MaxZScore`
  returns the largest z across all valid windows so "any window
  flags" maps to a single threshold check. `SplitByLookback`
  helper partitions a time-stamped VWAP series into three sub-
  windows in one pass. 7 new tests including the headline
  frog-boiling-defense scenario (sustained 0.5%/day drift over
  14d → 30d window dominates). Storage + orchestrator wire-up
  follow as separate slices.

- **Baseline refresh wired into the aggregator binary (L2.5 final
  slice — closes L2.5)**: `cmd/ratesengine-aggregator` now runs a
  hourly baseline refresh loop alongside the orchestrator's
  per-tick VWAP cycle. Adapters wrap `*timescale.Store` to satisfy
  `baseline.VWAPSource` + `baseline.Sink`. The first refresh fires
  immediately on startup so a fresh deployment populates
  `volatility_baseline_1m` without waiting a full hour. Outcomes
  emit through `ratesengine_aggregator_baseline_refresh_total`
  labelled by `{ok, not_enough_samples, read_error, write_error}`.
  Cadence (1h) and concurrency (4) are hardcoded for now —
  surfaceable as TOML knobs only if production usage shows we need
  them. Closes L2.5 across 4 slices: math primitive, storage layer,
  refresh worker, binary wire-up.

- **Baseline refresh worker (L2.5 slice)**: `baseline.Refresher`
  reads bucket-aligned 1m VWAPs over a 30d window via the new
  `Source.VWAPSource` interface, runs `ReturnsFromVWAPs` →
  `FromReturns` to compute the baseline, and persists via the
  `Sink` interface. Storage layer adds `Store.VWAPsForPair1m`.
  `RefreshPair` returns a structured `RefreshOutcome` (ok,
  not_enough_samples, read_error, write_error) so callers can
  emit per-outcome metrics; `RefreshAll` runs across a pair list
  with bounded concurrency, aggregates a `RefreshSummary`, and
  honours ctx cancellation cleanly. The bootstrap branch is
  not_enough_samples — caller skips the upsert and applies
  ADR-0019 §"Bootstrap policy" instead. The aggregator binary's
  wire-up (running this on an hourly ticker against the
  configured pair list) lands in the next L2.5 slice.

- **`volatility_baseline_1m` table + storage layer (L2.5 slice)**:
  per-pair baseline persistence per ADR-0019 Phase 2. Migration 0007
  adds the table — plain Postgres, NOT a CAGG (Median + MAD are only
  expressible via percentile_cont, which is non-parallel and
  non-incremental, so a CAGG would re-scan the whole 30-day window
  on every refresh anyway with no benefit). Current-state semantics:
  one row per pair, refreshes UPSERT and overwrite. Storage layer
  ships `StoredBaseline` wire shape, `Store.UpsertBaseline` (with
  pre-flight N >= MinSamples + window-validity checks),
  `Store.LatestBaseline` (returns `ErrBaselineNotFound` for the
  bootstrap branch), and `Store.CountBaselines` for ops metrics.
  Integration test rounds the API trip including overwrite semantics
  and per-pair isolation. The aggregator-side compute + write
  pipeline lands in the next L2.5 slice.

- **`internal/aggregate/baseline/` MAD math (L2.5 slice)**:
  pure-Go primitives implementing the per-asset volatility baseline
  per [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  Phase 2. `Median`, `MAD` (1.4826-scaled to σ-equivalent), `Baseline`
  struct with `ZScore` method (handles zero-MAD edge case: exact-match
  returns 0, any deviation returns +Inf), and `ReturnsFromVWAPs`
  helper for bucket-to-bucket percent-change conversion. Skips
  buckets with `prev == 0` to avoid Inf-poisoning downstream stats.
  17 tests cover odd/even median, MAD outlier-robustness vs σ,
  z-score symmetry, zero-MAD edge cases, and a stablecoin-class
  end-to-end roundtrip. The `volatility_baseline_1m` CAGG migration
  and the orchestrator wiring (the two larger pieces of L2.5) ship
  in follow-up PRs — this slice is the math primitive everything
  else builds on.

- **`/v1/price/stream` SSE endpoint (L3.9)**: closed-bucket SSE
  surface per ADR-0015 + ADR-0018. Hub-driven (unlike the per-tick
  tip/observations streams) — the aggregator publishes one event per
  closed bucket on the topic `closed:<asset>/<quote>`, and every
  subscriber on the same pair receives byte-identical payloads.
  Returns 503 until the deployment wires a `streaming.Hub` into
  `v1.Options.Hub`; the API handler + topic helper ship now so
  consumers can integrate against the wire contract before the
  aggregator's publish path lands. URL discipline: `?granularity=`
  returns 400 (closed-bucket stream is fixed at 1m).

- **`/v1/observations/stream` SSE endpoint (L3.8)**: streaming
  counterpart to `/v1/observations` per ADR-0018. Same compute,
  pushed on a per-connection tick. Cadence knob is `interval_seconds`
  (default 5, clamp 1–60) — deliberately a different name from
  tip's `window_seconds` because observations doesn't aggregate.
  First event always emits synchronously (may be empty array;
  observations returns 200/empty not 404, the stream mirrors that).
  Same `?source=`, `?aggregate=latest` knobs as the request
  endpoint. URL discipline: `?granularity=` and `?window_seconds=`
  return 400. Refactored the request handler's compute path into a
  shared `Server.computeObservations`.

- **`/v1/price/tip/stream` SSE endpoint (L3.7)**: streaming
  counterpart to `/v1/price/tip` per ADR-0018. Same compute logic
  pushed on a per-connection tick (default cadence = window_seconds,
  clamp 1–60). First event emits synchronously on connect — no
  waiting a full window for the first datum. Pre-flight 404 when
  the pair has no observations (SSE can't change status mid-stream).
  Heartbeats every 15s; Last-Event-ID resume via header or
  `?last_event_id=` fallback. Refactored the request handler's
  rolling-window-then-fallback core into a shared `Server.computeTip`
  used by both endpoints.

- **`internal/api/streaming/` SSE infrastructure (L3.6)**: shared
  pub/sub primitive backing the upcoming streaming endpoints
  (L3.7 `/v1/price/tip/stream`, L3.8 `/v1/observations/stream`,
  L3.9 `/v1/price/stream`). `Hub` is goroutine-safe per-topic
  fanout with a per-topic ring buffer (default 256 events) for
  Last-Event-ID resume. `Stream` HTTP handler sets the SSE wire
  contract: `text/event-stream` headers, `X-Accel-Buffering: no`,
  comment-only heartbeats every 15 s (configurable), parses
  `Last-Event-ID` header (with `?last_event_id=` fallback), and
  forwards live events as SSE frames until the request context
  cancels. Slow subscribers are dropped (32-deep per-sub queue)
  rather than blocking the publish path — the dropped client sees
  the connection close and reconnects with `Last-Event-ID` for
  buffered replay. ULID-shaped 16-char hex IDs, monotonic and
  lexicographically sortable. No external dependencies.

- **`/v1/observations` raw per-source surface (L3.3)**: implements
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) Surface 3 —
  the lowest-level, no-aggregation surface. Returns the most-recent
  trade per source for the (asset, quote) pair as an array.
  `?source=X` narrows to one venue; `?aggregate=latest` collapses to
  the single newest trade across sources. `flags.stale` is always
  false; freeze + divergence flags intentionally not consulted (this
  is the rawest surface, no aggregation contract). Empty pair returns
  200 with `data: []`, not 404 — divergence-detection callers polling
  for source coverage benefit from the 200/empty distinction.
  URL discipline: `?granularity=` and `?window_seconds=` return 400.
  New storage primitive `Store.LatestTradePerSource` does the work in
  SQL via `DISTINCT ON (source)`.

- **`/v1/price/tip` rolling-window tip surface (L3.2)**: implements
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) Surface 2.
  VWAP over a configurable rolling window (default 5 s, clamp 1–60 s)
  with last-good-price fallback when the window is empty. Both
  branches are in-contract — `flags.stale` stays `false` on this
  surface (the closed-bucket "below-baseline" semantic doesn't
  apply). Freeze flag is intentionally NOT consulted (freeze is a
  closed-bucket concept; tip explicitly has no cross-region
  consistency contract). Divergence flag still applies (asset-level).
  URL discipline enforced: `?granularity=` returns 400.
  Hypertable hiccups on the window path silently drop to the
  fallback so a transient TimescaleDB error doesn't take down the
  whole tip surface when the LatestPrice path is healthy.

- **`pkg/client/` Go SDK skeleton (#201)**: first public-package
  surface under [ADR-0005](docs/adr/0005-monorepo.md)'s SemVer
  promise. v0.1.0 pre-release. Generic `Envelope[T]` for type-
  safe data fields; covered endpoints: `Price`, `HistorySinceInception`,
  `Assets`, `Asset`, `AssetMetadata`, `Me`, `Usage`, `CreateKey`.
  `*APIError` wraps RFC 9457 problem+json with convenience
  predicates (`IsNotFound`, `IsRateLimited`, …); falls back to
  status-only on text/plain bodies (reverse-proxy 502s). Auth via
  `Options.APIKey` → `Authorization: Bearer …` header (omitted
  when empty so anonymous callers don't trigger malformed-credential
  rejections).

- **`internal/divergence/` package (#204, #205)**: cross-reference
  divergence layer per [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  §"Layer 5". `Reference` interface + parallel `Compare()` with
  robust median + per-source breakdown. `CoinGeckoReference`
  implementation as the working concrete example. `Service` writes
  `div:<asset>` Redis keys per [ADR-0007](docs/adr/0007-redis-cache-schema.md);
  `LookupCached` is the API hot-path read. `flags.divergence_warning`
  now fires for real on `/v1/price` when the cached result says
  warning is fired (5% deviation × 2 min sources defaults).
  Best-effort: lookup errors log at WARN, flag stays default false.

- **`internal/aggregate/anomaly/` Phase 1 (#199)**: ADR-0019
  Phase 1 stop-gap. `Classifier` + `Thresholds` + `Checker.Evaluate`
  with the 3-signal AND freeze condition (deviation > class
  threshold AND source_count <= 1). Per-class defaults:
  stablecoin/treasury 1%/3%, crypto 20%/50%, governance 50%/100%,
  default 30%/75%. New envelope flags `Frozen` and `SingleSource`
  on the wire. Config schema describer recurses into
  `map[string]<struct>` value types so per-row sub-fields appear
  in the generated config reference.

- **`internal/archivecompleteness/` daemon (#200, #202, #203)**:
  three-PR trilogy implementing [ADR-0017](docs/adr/0017-archive-completeness-invariants.md).
  `ratesengine-ops archive-completeness check` (PR A) — read-only
  scan + JSON Report. `… fix` (PR B) — multi-source fallback
  fetcher with shuffled source order, atomic placement, gzip
  validation, zip-bomb guards. `… verify` (PR C) — daily-cron
  shape with Prometheus textfile output, systemd timer
  (`02:17 UTC` + 5min jitter, `Persistent=true`), 4 alert rules
  (`files_missing`, `stale`, `critical_stale`, `repair_source_degraded`).
  Wires into node_exporter's textfile_collector; alerts fire from
  `deploy/monitoring/rules/archive-completeness.yml`.

- **`auth.RedisAPIKeyValidator` (#196)**: fills the [`internal/auth`](internal/auth/)
  scaffolding from PR #190 with a Redis-backed validator. Storage
  shape `apikey:<sha256-hex>` → JSON record (identifier, tier,
  scopes, expires_at, revoked_at). Plaintext keys never enter
  Redis. Sentinel mapping: missing/revoked → `ErrUnauthorized`;
  `expires_at` past → `ErrTokenExpired` (middleware sets
  WWW-Authenticate with refresh hint). Wired in `cmd/ratesengine-api`:
  `auth_mode=apikey` + Redis reachable → real validator; without
  Redis → Noop fallback so every request 503s (correct fail-loud).

- **`/v1/account/{me,usage,keys}` self-service (#197)**: three
  account endpoints from the OpenAPI spec. `/me` echoes the
  authenticated `Subject`; `/usage` returns empty array (counter
  store ships separately, wire shape locked); POST `/keys` issues
  a fresh key inheriting the caller's identifier+tier verbatim.
  New `auth.APIKeyStore` interface + `RedisAPIKeyStore`. Plaintext
  generated as `rek_<64-hex>` from `crypto/rand`; KeyID as
  `kid_<16-hex>`.

- **`/v1/history/since-inception` (#195)**: CAGG-served full
  historical series at the requested granularity. `1m / 15m / 1h /
  4h / 1d / 1w / 1mo` granularities; default `1d`; capped at 50K
  points. New `Store.HistoryPoints` against `prices_<granularity>`
  tables with the closed-bucket guard scaling per granularity.

- **`/v1/oracle/prices` (#193)**: SEP-40 `prices(asset, records)`
  passthrough. Returns the last N closed 1m VWAP buckets. Capped
  at 200 records per the SEP-40 contract.

- **`/v1/assets/{id}/metadata` + SEP-1 overlay (#192)**: new
  endpoint plus overlay handler that resolves home-domain →
  stellar.toml. Operator-curated issuer→home-domain map in
  `cfg.Metadata.IssuerHomeDomains`; on-chain AccountEntry
  observation deferred until indexer pipework lands.

- **SLO multi-window burn-rate alerts (#194)**: per
  [ADR-0009](docs/adr/0009-latency-budget.md). Three sensitivity
  tiers per SLO (fast/medium/slow burns) with both-windows-must-
  agree to suppress single-spike noise. Wired in
  `deploy/monitoring/rules/slo.yml`.

### Changed

- **`comet` source flipped `BackfillSafe: false → true`** —
  pool-identification audit landed
  ([docs/operations/wasm-audits/comet.md](docs/operations/wasm-audits/comet.md)).
  The only known mainnet Comet deployment is Blend's backstop
  pool `CAS3FL6T...` (per `docs/discovery/dexes-amms/comet.md`
  open-item resolution and the L55,261,759 mainnet snapshot in
  `blend-contracts/test-suites/`). Pool's WASM hash
  `8abc28913035c074...` fetched via `stellar contract fetch --id`
  and verified — all 5 SwapEvent body field names (`caller`,
  `token_in`, `token_out`, `token_amount_in`, `token_amount_out`)
  preserved in the binary; no upgrade since L51,499,546. The
  topic-based decoder design is robust to any future canonical
  Comet pool using the same audited WASM. **All 8 Soroban
  on-chain sources are now BackfillSafe=true.**

- **`aquarius` source flipped `BackfillSafe: false → true`** —
  pool-enumeration audit landed
  ([docs/operations/wasm-audits/aquarius.md](docs/operations/wasm-audits/aquarius.md)).
  All 313 mainnet pool contracts enumerated via router
  `get_pools_for_tokens_range()`; their current WASMs fetched via
  `stellar contract fetch`. Three unique pool-WASM hashes total
  (one volatile, one stableswap, one rewards-enhanced; 267/40/6
  pool distribution), all three containing the 4 expected
  event-name strings (`trade`, `update_reserves`,
  `deposit_liquidity`, `withdraw_liquidity`). Source-import
  topology confirmed across all three aquarius pool-type crates
  (`liquidity_pool`, `liquidity_pool_stableswap`,
  `liquidity_pool_concentrated`) — all `use
  liquidity_pool_events::Events` and dispatch to the shared
  `LiquidityPoolEvents::trade()` emitter, structurally preventing
  wire-format drift across pool types. The 6 router hashes from
  the original walk are informational only (decoder targets
  per-pool trade events, not router swap events).

- **`phoenix` source flipped `BackfillSafe: false → true`** —
  pool-enumeration audit landed
  ([docs/operations/wasm-audits/phoenix.md](docs/operations/wasm-audits/phoenix.md)).
  All 11 mainnet pool contracts enumerated via factory
  `query_pools()`; their current WASMs fetched via
  `stellar contract fetch` and analyzed. Two unique pool-WASM
  hashes total, both containing all 8 required swap-field string
  literals (`sender`, `sell_token`, `offer_amount`, `actual
  received amount`, `buy_token`, `return_amount`, `spread_amount`,
  `referral_fee_amount`) and identical contract interfaces — both
  decoder-compatible. The 5 factory + 3 multihop hashes from the
  walk are informational only (decoder targets per-pool swap
  events, not factory/multihop events).

- **`reflector-dex` and `reflector-cex` flipped `BackfillSafe:
  false → true`** — v2-era WASM (`4a64c8c8…`) fetched via
  `stellar contract fetch` and disassembled against the v3
  production hash (`df88820e…`). Contract-interface diff is
  cosmetic (one removed governance function, struct ordering);
  data-section field names identical; SDK 20.x family preserves
  `#[contractevent]` macro behavior, so v2 and v3 events have the
  same wire format. The decoder works for both. Audit evidence
  appended to
  [docs/operations/wasm-audits/reflector.md](docs/operations/wasm-audits/reflector.md);
  status flipped partial → ratified. All three Reflector variants
  now flip-completed.

- **`reflector-fx` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/reflector.md](docs/operations/wasm-audits/reflector.md)).
  All three Reflector variants share one decoder; the walk shows two
  unique hashes total: a v2-era `4a64c8c8…` (DEX+CEX only, Feb–Apr
  2024) and the current production `df88820e…`. FX was deployed
  fresh on `df88820e…` and has never run any other hash, so the
  audit covers it deterministically. DEX + CEX stay
  `BackfillSafe: false` pending v2-era WASM disassembly to confirm
  the pre-v3 event shape matches the current decoder; that's
  documented as the next follow-up.

- **`redstone` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/redstone.md](docs/operations/wasm-audits/redstone.md)).
  Adapter contract `CA526Y2N…` shows two WASM hashes: a 420-ledger
  (~35 min) first-deploy hotfix `b400f7a8…` (L58,758,722 →
  L58,759,141) and the current production `5e93d22c…`
  (L58,759,142 → scan-end, ~36 days stable). Per-hash review
  confirms the production hash matches the live decoder; the
  hotfix-window analysis (zero redstone trades in that 420-ledger
  range, deploy-then-hotfix pattern) supports flipping the flag with
  a documented caveat that the b400f7a8 bytes were not disassembled
  inline. Backfill against historical Redstone ranges is now
  permitted via `ratesengine-ops backfill`.

- **`band` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/band.md](docs/operations/wasm-audits/band.md)).
  StandardReference contract `CCQXWMZV…` shows one stable WASM hash
  `6cdb9a3c…` since launch (L50,842,736 / 2024-03-19); no
  `update_contract` events through scan-end. Per-hash review
  confirms `relay` / `force_relay` function signatures + `(Symbol,
  u64)` Vec tuple order match the positional op-args reader. Backfill
  against historical Band ranges is now permitted via
  `ratesengine-ops backfill`.

- **`soroswap` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/soroswap.md](docs/operations/wasm-audits/soroswap.md)).
  Factory + router each show one stable WASM hash across the entire
  post-Soroban window (L50,746,266 → L59,301,651, ~2024-03 → 2026-04);
  no `update_contract` events observed. Per-hash review against the
  live decoder confirms no schema divergence. Backfill against
  historical ranges is now permitted for `soroswap` via
  `ratesengine-ops backfill`. Per-instance pair-WASM enumeration is
  documented as a v2 audit follow-up. The remaining 6 on-chain Soroban
  sources (aquarius, phoenix, comet, reflector-{dex,cex,fx}, redstone,
  band) stay `BackfillSafe: false` until each source's audit lands.

- **`verify-archive -fail-on-missed` (#206)**: per
  [ADR-0017](docs/adr/0017-archive-completeness-invariants.md) X1.7.
  Off by default (preserves pre-bootstrap workflow that tolerated
  scattered missed checkpoints). On after running the
  archive-completeness bootstrap so a regression surfaces as a
  P2 ticket within 24 h instead of being hidden in info logs.

- **API consistency surfaces** per [ADR-0018](docs/adr/0018-api-consistency-surfaces.md):
  established the three-URL model — `/v1/price` (closed-bucket,
  cross-region consistent), `/v1/price/tip` (rolling window with
  last-good-price fallback, not consistent), `/v1/observations`
  (raw per-source). URL discipline as the contract enforcer; query
  parameters MUST NOT change consistency tier. Forex factor snap
  rule for chained-fiat preserves cross-region consistency on
  `/v1/price`. Implementation of `tip` + `observations` follows.

- **`flags.stale` semantic clarified** (ADR-0018): means "below
  this surface's documented baseline contract." Fires on `/v1/price`
  for closed-bucket degradation; never on `/v1/price/tip` (the
  last-good-price fallback is in-contract there); never on
  `/v1/observations` (no aggregation contract).

### Documentation

- **3 new ADRs (#198)**:
  [ADR-0017](docs/adr/0017-archive-completeness-invariants.md)
  archive completeness invariants (4 hard contracts; per-region
  asymmetric trust model — R1 leader, R2/R3 delegate via metric
  scrape with 26h staleness budget);
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) three API
  consistency surfaces;
  [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  anomaly response with per-asset MAD-based statistical baselines
  (not fixed thresholds), 3-signal AND freeze on closed-bucket only.

- **`docs/architecture/oracle-manipulation-defense.md` (#198)**:
  attack catalogue (Reflector/USTRY, Mango, Cream, Inverse,
  Polter, Harvest, bZx) + worked USTRY scenario walkthrough
  showing per-surface response under each ADR-0019 phase.

- **`docs/operations/archive-completeness.md` (#198)**: daily-cron
  design, multi-source fallback chain, Prometheus surface,
  status-page integration. Per-region behaviour details
  (R1 enforces / R2/R3 delegate).

- **`docs/architecture/launch-readiness-backlog.md` (#198)**:
  canonical 47-item launch-blocking backlog with dependency
  graph + critical path. Operator decision 2026-04-28: every
  non-deferred item ships before launch.

- **4 new operator runbooks (#198)**: `anomaly-freeze-engaged`,
  `archive-files-missing`, `archive-completeness-stale`,
  `archive-repair-source-degraded`. Wired into `alerts-catalog.md`.

- **`coverage-matrix.md` refreshed (#198)**: 22 new cross-cutting
  integrity invariant rows (X1.* archive, X2.* API surfaces,
  X3.* anomaly). Gap-triage reflects every outstanding item as
  launch-blocking.

- **SemVer policy formalised**: see
  [`docs/architecture/semver-policy.md`](docs/architecture/semver-policy.md)
  for the binding rules on `pkg/*` API stability and binary
  CalVer release tagging.

- **`GET /v1/price/batch?asset_ids=A,B,C&quote=`**: batch
  price lookup for up to 100 assets in one round-trip. Promised
  by the OpenAPI spec but previously unmounted. Missing assets
  are omitted from the response (not 404'd) so callers asking
  for 5 assets and getting 3 rows know exactly which 2 we don't
  have data for. Server-side dedupe collapses repeats; the
  envelope's `flags.stale` is the OR of per-row staleness, and
  `sources` is the union across all returned rows. Reuses the
  existing `PriceReader` interface — no storage-layer changes.

- **`GET /v1/oracle/lastprice?asset=` and
  `GET /v1/oracle/x_last_price?base=&quote=`**: SEP-40
  passthrough surface promised by the OpenAPI spec but
  previously unmounted. Returns the SEP-40 `(asset, price,
  timestamp)` shape using the same VWAP / last-trade pipeline
  that backs `/v1/price`. `lastprice` is fixed at `fiat:USD`
  quote (matches the SEP-40 contract semantic — the on-chain
  oracle has one configured quote per contract);
  `x_last_price` takes explicit base + quote. The richer
  per-source readings remain on `/v1/oracle/latest`.
  `/v1/oracle/prices` (N historical records) deferred —
  needs a CAGG read path that the aggregator's continuous-
  aggregates surface hasn't grown yet.

- **`POST /v1/price/batch`**: JSON-body variant accepting up to
  1000 `asset_ids`. Same semantics as GET; the body shape exists
  precisely to raise the GET ceiling without bloating query
  strings (a 1000-id query would blow past most reverse-proxy
  default 8 KiB header limits). Body capped at 1 MiB,
  `DisallowUnknownFields()` rejects unrecognised keys. Shared
  core (`runPriceBatch`) under both GET and POST so behaviour
  stays in lockstep.

- **`GET /v1/pairs?base=&quote=`**: single-pair activity summary
  promised by the OpenAPI spec but previously unimplemented.
  Returns the same `MarketRow` shape as `/v1/markets`, filtered
  to one pair: zero or one element. Empty array (200 OK), not
  404, when the pair has no trades — matches the
  `PairsEnvelope.data: array` contract so clients can
  distinguish "no data" from "bad request" without branching on
  status code. Backed by a new `Store.PairMarket(base, quote)`
  method on the timescale store.

- **PRs 41–73 — As-built audit + galexie tuning playbook**
  (2026-04-25): an autonomous-loop session focused on bringing
  the docs flush with the shipped code and capturing live-run
  findings. Mostly housekeeping, two small bugfixes, one
  substantive operational discovery.

  Code-side fixes:

  - **PR 66 — orchestrator `lastTickAt` UTC**: was recording in
    host local timezone while the rest of the tick used UTC;
    `Stats()` now returns consistent UTC throughout.
  - **PR 67 — orchestrator `Stats` doc**: corrected the
    "zero-copy" claim to the accurate "value-type return,
    independent snapshot."

  Galexie + archival-node operational findings:

  - **PR 57 — `docs/operations/galexie-backfill.md § Tuning`**:
    the 2026-04-25 r1 backfill ran phase 3 at ~58 ledgers/sec —
    10–25× under galexie's claimed ceiling. Bottleneck is the
    single-goroutine S3 PUT loop (verified against
    `stellar/stellar-galexie@6dec23e2:internal/uploader.go`).
    Highest-impact lever without forking is parallel
    `scan-and-fill` processes on disjoint ranges (idempotent
    via the per-object `IfNoneMatch: "*"` precondition);
    8 workers ≈ 1.5 days vs ~12 days serial. Recipe in the
    section.
  - **PR 58 — `archival-node-spec.md § 3.3.4`**: galexie
    backfill is the actually-long pole when bringing up a new
    archival node, not stellar-core catchup. Cite the live
    numbers.
  - **PR 71 — bootstrap-runbook galexie pointer**: §7
    "Catchup Timeline Expectations" now warns operators that
    the table only covers stellar-core, not galexie.
  - **PR 73 — AWS public-bucket mirror alternative**: AWS
    hosts a public Stellar dataset at
    `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`.
    For new-node bootstrap or DR, mirroring it is much faster
    than running scan-and-fill at all. OBSRVR's `nebu`
    archive mode reads directly from there. Documented
    trade-offs (retention floor, egress cost, loss of
    cross-validation).

  As-built doc audit (the mass of small fixes, none individually
  load-bearing — listed for the audit trail):

  - PRs 31–36 (per-source READMEs) and 32 (aggregation-plan)
    were already covered in the PRs 30–40 rollup above.
  - **PRs 38, 47, 109** dropped stale ADR-TBD / planned-package
    notes now that ADR-0010 + ADR-0014 are accepted and
    stellar-rpc is removed from r1 ingest.
  - **PRs 41, 50, 112, 130** brought the CHANGELOG, aggregate
    package doc, and canonical package doc current with the
    fiat / crypto / aggregation-plan additions.
  - **PRs 51, 53, 113, 115** captured the live-run backfill
    phase-shape + TUI status pointer in the operations
    playbook.
  - **PRs 44, 45, 106, 107** fixed `migrations/0004` collisions
    in storage-package comments and added the migrations
    manifest table.
  - **PRs 48, 55, 105, 110, 117** re-aligned OpenAPI / api-design
    with what `/v1` actually serves (`/v1/sources` listed,
    `/v1/version` enriched fields, missing meta tag, sigling
    `/v1/prices` → `/v1/price` typo).
  - **PRs 54, 111, 116, 121, 124, 125** corrected stale facts
    in r1-deployment-state, makefile, monitoring README, and
    one stray ecosystem-review entry.
  - **PRs 60, 65, 114, 122, 126, 127, 132** brought the
    operations runbook + alerts-catalog into compliance with
    the `_template.md` shape and made the "CI enforces this"
    claims honest.
  - **PRs 61, 68, 134** pulled the public Reflector v3 mainnet
    addresses into example.toml + the source-package READMEs
    (Phase-1 audit had left them as TBD).
  - **PRs 99, 131** dropped truly-stale references — PR 99
    switched canonical strkey from regex format-only validation
    to SDK-backed CRC verification (caught real bugs:
    CRC-mismatched and wrong-version-byte strkeys were being
    accepted); PR 131 dropped `withObsrvr/stellar-extract` from
    VERSIONS.md's active-deps list since it never landed in
    `go.mod`.

- **PRs 30–40 — Aggregator stack documentation, refactors, and
  Tier E** (2026-04-25): rounds out the aggregator build-out
  with as-built docs, a couple of code refactors, and the final
  verify-archive tier.

  - **PR 30 — CHANGELOG rollup** for PRs 21–29 (the entry above
    this one).
  - **PR 31–35 — Per-source READMEs**: Comet, Redstone, Band,
    SDEX, plus a single consolidated catalogue for the 10
    external connectors. Every `internal/sources/*` package now
    has a README following the same shape (what this ingests,
    topic shape, events table, quirks, files).
  - **PR 32 — `docs/architecture/aggregation-plan.md`**: the
    single anchor for the aggregator-layer design. Data flow,
    policy chain ordering, configuration surface, observability,
    API surface, boundaries, and deferred items in one place.
  - **PR 37 — strkey CRC validation via go-stellar-sdk**:
    replaces the regex-only `IsAccountID` / `IsContractID` with
    the SDK's `strkey.Decode(VersionByte*, str)`. Now rejects
    CRC-mismatched and wrong-version-byte strkeys (silently
    accepted under the regex). Resolves the standing TODO.
  - **PR 38 — drop stale ADR-TBD comment in oracle.go**:
    points the pair-vs-single-asset note at accepted ADR-0010
    instead of "TBD".
  - **PR 39 — verify-archive Tier E**: wraps `stellar-archivist
    scan` (or `rs-stellar-archivist scan`) for a full
    bucket-by-bucket sha256 audit of an archive — the fifth and
    final tier of the verification playbook. Defaults to
    scanning the local mirror at `file://<archive-root>`; any
    peer's `https://` archive URL also works.
  - **PR 40 — `/v1/sources?class=` filter**: optional class
    query parameter on the source catalogue endpoint. Useful
    for dashboards that split sources by role
    (exchange / aggregator / oracle / authority_sanity).

  Net effect: the verification playbook is fully implemented
  (Tiers A/B/D/E; Tier C deferred pending GCS public-read
  confirmation), the aggregator's design + ops surface is
  documented end-to-end, and one stable-named code path
  (canonical strkey) became stricter without API churn.

- **PRs 21–29 — Aggregator policy + observability layer**
  (2026-04-25): builds out the orchestrator from PR 182's
  passthrough VWAP into a configurable, observable, alerting-
  ready computation:

  - **PR 21 (class filter)**: orchestrator drops non-`ClassExchange`
    trades from the VWAP input set by default. Aggregator-class
    sources (CoinGecko / CMC / CryptoCompare) and oracle-class
    sources (Reflector / Redstone / Band) stay visible in
    `/v1/sources` for transparency but no longer skew the
    computed price. Inverted `DisableClassFilter` flag —
    zero value is the safer default.
  - **PR 22 (stablecoin helper)**: `internal/aggregate/stablecoin.go`
    with `FiatProxy` / `ProxyPair` / `ProxyTrade`. Maps quote-
    side stablecoins (USDT/USDC/DAI/PYUSD/USDP → USD,
    EURC/EUROC/EUROB → EUR, MXNe → MXN). Aggregator policy
    only — decoders still record the raw pair so a depeg event
    stays visible in the trade feed.
  - **PR 23 (orchestrator stablecoin wire-up)**:
    `Config.EnableStablecoinFiatProxy`. When on, a fiat-
    denominated target pair fans out to direct + stablecoin
    backers and collapses onto the target via `ProxyPair`
    before VWAP. Single-backer fetch failure logs and skips
    rather than aborting the window.
  - **PR 24 (TOML plumbing for filter flags)**: exposes
    `disable_class_filter`, `enable_stablecoin_fiat_proxy`,
    `interval_seconds`, `max_trades_per_window` in
    `[aggregate]`.
  - **PR 25 (outlier filter wire-up)**: orchestrator's
    `OutlierSigmaThreshold` (driven by `aggregate.outlier_sigma_threshold`,
    default 4.0) drops trades > σ from the window mean before
    VWAP. Applied after class + stablecoin steps so the σ
    arithmetic runs over comparable price values.
  - **PR 26 (Prometheus metrics)**: `ratesengine_aggregator_*`
    counters — ticks (by outcome), VWAP writes, empty windows,
    dropped trades (by reason: `class` / `outlier`).
  - **PR 27 (alerts + runbooks)**: three Prometheus rules
    (`aggregator_silent` P1, `aggregator_outlier_storm` P3,
    `aggregator_class_drop_spike` P3) with full runbooks.
    Baseline-comparator alerts use `offset 1h` to auto-tune to
    operator traffic.
  - **PR 28 (`GET /v1/sources`)**: surfaces `external.Registry`
    on the API so consumers can confirm a venue's class +
    `include_in_vwap` without internal access. Same metadata
    the class filter consults — they agree by construction.
  - **PR 29 (configurable pairs + windows)**: `aggregate.pairs`
    and `aggregate.windows` accept operator overrides as
    canonical pair strings (`"crypto:XLM/fiat:USD"`) and Go
    `time.Duration` strings (`"5m"`). Empty falls back to the
    binary's built-in defaults.

  Together: the aggregator can now be deployed with operator-
  chosen coverage, the class/stablecoin/outlier policy chain
  applied in order, observable via Prometheus + paged via
  Alertmanager when it goes silent or throws an unusually high
  drop rate.

- **PR 182 — Aggregator orchestrator v1** (2026-04-24): turns
  `cmd/ratesengine-aggregator` from a deliberate `os.Exit(1)`
  stub into a running binary. Rolling-window VWAP pre-computed
  on a ticker, written to Redis, consumed by the API's `/v1/price`
  — unblocks the path from "last trade, stale-flagged" degraded
  mode to fresh cached pricing.

  - `internal/aggregate/orchestrator/` (new): `Orchestrator`
    with `New(Store, Cache, Config)` + `Run(ctx)` + `Tick(ctx)`.
    On each tick, for every (pair, window) combination: fetch
    trades via `TradesInRange`, compute VWAP via existing
    `internal/aggregate/vwap.go`, write to Redis key
    `vwap:<base>:<quote>:<window-seconds>` with TTL = window.
    First tick fires immediately on startup so a fresh
    aggregator has warm keys before the API's first query.
  - **`Store` and `Cache` are interfaces**: tests substitute a
    mock Store + miniredis instead of pulling up Testcontainers
    for unit-level coverage.
  - **Built-in windows**: 5m / 1h / 24h. Operator override via
    `Config.Windows`; empty list defaults.
  - **Tick cadence**: 30s default, matches the Redis
    `price:` TTL of 60s with headroom.
  - **Built-in pair set**: XLM/BTC/ETH × USD/EUR/GBP 3×3.
  - **`formatRatFixed`** handles big.Rat → decimal-string
    conversion with truncate-toward-zero semantics (not Go's
    stdlib banker's rounding). Float encoding prohibited on
    this path (ADR-0003).
  - Binary: config load → Timescale open → Redis open (with
    dry-run ping) → orchestrator build → `Run(ctx)` until
    SIGINT/SIGTERM.
  - 7 unit tests: happy-path Redis write, empty-window skip,
    store-error recovery, multi-window writes, no-op on empty
    pair list, immediate-first-tick behaviour, `formatRatFixed`
    rounding semantics.

  **v1 policy deliberately out of scope** (each is a clean
  follow-up the Config shape already accepts):
  - Class-based filtering (only `ClassExchange` contributes).
  - Stablecoin → fiat proxy (USDT→USD, USDC→USD …).
  - Cross-pair triangulation.
  - Divergence detector against aggregator-class sources.
  - Outlier filtering before VWAP computes.

  Satisfies the "two-phase aggregator landing" plan agreed
  earlier: Phase 1 = plumbing + passthrough aggregation (no
  policy commitments); Phase 2 = class filtering + fiat proxy
  + triangulation once the CEX fleet's live data reveals real
  failure modes.

- **PR 181 — External-fleet end-to-end integration test + 0004
  migration** (2026-04-24): Phase-2 ingestion closing ceremony.
  Ties every external-source class together in a single test
  hitting a live Timescale, proving the framework + all
  interfaces + wire-up to storage work end-to-end under realistic
  shapes.

  - `test/integration/external_fleet_test.go` (new):
    `TestExternalFleet_EndToEnd` spins up **5 mock venues in
    parallel** — Binance WS (Streamer / exchange), Bitstamp WS
    (Streamer / exchange — proves multi-streamer fan-out),
    ExchangeRatesApi REST (Poller / exchange FX),
    CoinGecko REST (Poller / aggregator),
    ECB XML (Poller / authority_sanity). Each is a scripted
    `httptest` server with venue-specific fixture responses.
    Calls `external.Run`, drains events through
    `store.Insert*`, asserts trades and oracle_updates rows
    land in Timescale via `LatestTradesForPair` and
    `LatestOracleUpdateForAsset`.
  - **What it caught**:
    1. `canonical.Trade.Validate()` was rejecting `Ledger=0`.
       Off-chain sources stamp 0 deliberately (no ledger
       concept). Fixed: relaxed the Validate check; TxHash +
       Source + OpIndex already enforce uniqueness. `trade_test.go`
       updated to match.
    2. The `trades.ledger` column had a `CHECK (ledger > 0)`
       constraint at the DB level. See migration 0004.
    3. Integration test context-propagation bug: using the
       cancelled fleet context for post-drain SELECT queries
       surfaced as "context canceled". Fixed: separate
       `assertCtx` for post-drain assertions.
  - **Migration 0004** (`0004_relax_trades_ledger_for_offchain`):
    relaxes the `trades.ledger` CHECK from `> 0` to `>= 0`.
    Up path does a decompress → ALTER → re-compress dance
    because TimescaleDB blocks constraint changes on
    compressed hypertables. Down path uses `ADD CONSTRAINT ...
    NOT VALID` so the stricter constraint restores
    schema-level but doesn't block rollback against a DB with
    existing off-chain rows — operator can `VALIDATE
    CONSTRAINT` explicitly if they know it's safe.
  - **migrations_test** update: the "zero ledger" CHECK-
    rejection case flipped to an `assertInsertAccepted` call
    — `ledger=0` is now the positive invariant. Sample values
    use `binance` source + `crypto:XLM`/`crypto:USDT` pair to
    mirror real off-chain traffic.
  - Runs in ~4 seconds against a shared Timescale container.
    In a typical run: 2 trades + 120 updates inserted (120 =
    3 pollers × ~40 ticks over 2 seconds with 100ms interval
    override).

  **Phase-2 ingestion close-out**: every source class now has
  at least one reference implementation shipped + integration-
  tested. 10 off-chain venues + 10 on-chain sources + 20+ unit
  test suites (116 external-package tests alone). The framework
  proves itself; future venues drop into the established Streamer
  / Poller / Backfiller / ContractCallDecoder shapes.

- **PR 180 — ECB daily FX reference rates** (2026-04-24): first
  `ClassAuthoritySanity` connector. European Central Bank's
  official daily fix emitted as `canonical.OracleUpdate` rows
  with `source = "ecb"` — the aggregator's end-of-day
  divergence anchor against intraday VWAP drift.

  - `internal/sources/external/ecb/` (new): REST Poller against
    `https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml`.
    XML parsing (first non-JSON source in the fleet — ECB
    publishes via gesmes Envelope). Free, no auth.
  - **Role**: explicitly NOT primary pricing (cadence is one
    fix per TARGET business day). The aggregator uses ECB as
    a sanity anchor: if our computed EUR/USD ever diverges
    > 50 bps from ECB's daily close, one of the upstream feeds
    is drifting. Sovereign-authority class guarantees the
    reference is trustworthy.
  - **Inversion semantics**: ECB publishes "1 EUR = X currency"
    (e.g. USD rate 1.0825 = 1.0825 USD per 1 EUR). We invert
    to canonical "price of Asset in Quote" form (1 USD = 0.9238
    EUR → Asset=USD, Quote=EUR). Same pattern as
    ExchangeRatesApi / Polygon Forex; aggregator math stays
    uniform across FX sources.
  - **Cadence**: 6-hour poll interval default — ECB publishes
    once per EU business day ~4pm CET; 6h gives comfortable
    slack. Poller is idempotent (stable `(currency, ts)`-
    derived tx_hash); extra polls on the same day's fix
    dedup harmlessly.
  - **Pair filtering**: emits for any fiat appearing in a
    configured pair (either side), excluding EUR (the base).
    Operator configuring `XLM/USD` gets USD/EUR rate; operator
    configuring `XLM/GBP` also gets GBP/EUR.
  - 8 unit tests: happy-path inversion + fiat filter, malformed
    XML, empty cube, crypto-only pair no-op, HTTP 5xx error,
    negative-rate entry skip, PollInterval default, direct
    inversion math sanity.

  **Fed H.10 deferred** to a follow-up PR: Federal Reserve
  datadownload URLs are series-specific (different URL per
  currency pair, mixed direction conventions across series) —
  meaningful complexity over ECB's single-file shape. Captured
  as a TODO; ECB alone covers the authoritative-sovereign-
  anchor requirement for EUR-based reference while Phase 2
  closes.

- **PR 179 — CoinGecko / CoinMarketCap / CryptoCompare aggregator
  pollers** (2026-04-24): three `ClassAggregator` pollers in one
  PR. All three emit `canonical.OracleUpdate` rows —
  **divergence signal only, excluded from VWAP** per the
  class-based policy shipped in PR 169. The future divergence
  detector consumes these to flag when our computed VWAP drifts
  beyond threshold against the aggregator consensus.

  - `internal/sources/external/coingecko/` (new): free-tier
    friendly (no auth), `/api/v3/simple/price` batch endpoint.
    `tickerToID` map (XLM→stellar, BTC→bitcoin, …) because
    CoinGecko uses slug IDs not tickers — the only aggregator
    with this quirk. 7 unit tests.
  - `internal/sources/external/coinmarketcap/` (new): paid Pro
    API key via `X-CMC_PRO_API_KEY` header, `/v2/cryptocurrency/quotes/latest`.
    CMC wraps each symbol's payload in an array because
    multiple coins can share a ticker; we take the first entry
    (canonical project by CMC rank) — pinned by
    `TestPollOnce_MultipleCoinsWithSameTicker_TakesFirst`. 6
    unit tests.
  - `internal/sources/external/cryptocompare/` (new): paid API
    key via `Authorization: Apikey <KEY>`, `/data/pricemulti`.
    Simplest aggregator shape — flat `{asset: {quote: price}}`
    map. CryptoCompare returns a 200-OK error envelope
    (`{"Response":"Error",...}`) for auth failures; probe
    detection before decoding the price map. 6 unit tests.

  **Exact-combo filtering** (applied to all three): filters the
  venue's N×M response matrix down to just the (crypto, fiat)
  pairs the operator configured. Prevents cross-product noise
  in `oracle_updates`. Each pair lookup keyed on
  `"<TICKER>/<CURRENCY>"`.

  **Config**: `CoinGecko` uses shared `ExternalVenueConfig`
  (no auth). `CoinMarketCap` and `CryptoCompare` get their
  own structs with API-key fields following the
  `PolygonForex` env-override convention (env vars
  `COINMARKETCAP_API_KEY` / `CRYPTOCOMPARE_API_KEY`). All
  default-off.

  **Indexer wiring**: `defaultAggregatorPairs()` returns the
  XLM/BTC/ETH × USD/EUR/GBP 3×3 crypto-fiat matrix as the
  baseline set aggregators poll.

- **PR 178 — `backfill-external` operator CLI** (2026-04-24):
  turns the Backfiller interface from infrastructure into an
  operator tool. Historical-data ingestion is now a single
  command away; no custom scripts or direct DB writes required.

  - `cmd/ratesengine-ops/main.go`: new `backfill-external`
    subcommand. Flags: `-config`, `-source`, `-pair`, `-from`,
    `-to`, `-granularity`, `-dry-run`, `-progress-every`.
    Dispatches on `-source` to build the right venue's Streamer,
    resolves the venue-native symbol via its DefaultPairs, calls
    Backfill, inserts results into Timescale. 30-minute
    operation-wide context timeout.
  - **Venue-native symbols** on the command line, not invented
    cross-venue normalisation: `XLMUSDT` for Binance,
    `XLM/USD` for Kraken, `xlmusd` for Bitstamp, `XLM-USD` for
    Coinbase. Operators who know the venue don't relearn our
    conventions; unknown symbols surface the venue's configured
    set sorted in the error message.
  - **Dry-run mode**: fetches + synthesises trades but writes
    nothing. Prints a summary table (trade count, first/last
    timestamps, total base/quote volume, computed VWAP) so the
    operator can sanity-check a range before committing a large
    insert.
  - **Progress output**: emits one status line every
    `-progress-every` inserts (default 1000) so large backfills
    are visible without tail-f-ing logs.
  - **Example workflow** (in the binary's help text):
      ```
      ratesengine-ops backfill-external \
        -config configs/prod.toml \
        -source binance -pair XLMUSDT \
        -from 2024-01-01T00:00:00Z \
        -to   2024-12-31T00:00:00Z \
        -granularity 1h
      ```
    With stable per-candle `tx_hash` synthesis (see PRs 174 +
    177), repeated runs of the same command are idempotent —
    Timescale's `ON CONFLICT DO NOTHING` dedups.

  Imports the four external venue packages; unlocks the
  ratesengine-ops binary as the operator surface for every
  Backfiller we've shipped.

- **PR 177 — Kraken / Bitstamp / Coinbase historical backfill**
  (2026-04-24): three `Backfiller` implementations in one PR —
  the three CEX venues that had live streams but no historical
  data now cover the full range. Every CEX in our fleet is
  Streamer+Backfiller.

  Each follows the Binance pattern (one `Backfill` method on the
  existing `Streamer` type, synthesised `canonical.Trade` per
  candle at close-time) but with venue-specific quirks:

  - **Kraken** (`kraken/backfill.go`): `/0/public/OHLC`, interval
    in MINUTES, **hard cap 720 candles per response** (~30 days
    at 1h — documented as depth caveat on the Registry entry).
    Uses Kraken's own VWAP field (not close) × base volume for
    quote. Pagination via `since` param + response's `last`
    cursor. Granularity set: 1m/5m/15m/30m/1h/4h/1d/1w/15d.

  - **Bitstamp** (`bitstamp/backfill.go`): `/api/v2/ohlc/{pair}/`,
    step in SECONDS (60/180/300/…/86400/259200), limit 1000 per
    response. Deeper historical retention than Kraken — back to
    pair listing. Derives quote as close × volume (Bitstamp
    doesn't publish VWAP or quote-volume). Granularity set:
    1m/3m/5m/15m/30m/1h/2h/4h/6h/12h/1d/3d.

  - **Coinbase** (`coinbase/backfill.go`): `/products/{id}/candles`,
    granularity in SECONDS, **300 candles per response** (the
    tightest cap). **Critical trap**: Coinbase's candle array is
    **LHOC-ordered** — `[time, low, high, open, close, volume]` —
    NOT OHLC like every other venue. Parsing by index with the
    wrong assumption silently reports low as close. We read by
    index with comments documenting each slot, and
    `TestCoinbaseCandleToTrade_LHOC_Ordering` pins the correct
    behaviour. Response is newest-first; we iterate in reverse
    to emit chronologically. Granularity set: 1m/5m/15m/1h/6h/1d.

  All three require **User-Agent** for Coinbase (it rejects empty
  UA with 400); set in the HTTP client. Tx hashes are
  deterministic from (symbol, close_time_sec) across all three —
  same pattern as Binance, so repeated backfill runs hit the
  same primary key and our idempotent-insert path (ON CONFLICT
  DO NOTHING) handles dedup.

  **Registry update**: `external.Registry` flips
  `BackfillAvailable=true` for kraken / bitstamp / coinbase.
  Kraken's entry carries a comment flagging the 30-day cap so
  operators reading the map know the depth limit without having
  to read venue docs.

  13 new tests across the three packages:
  - Kraken: happy-path (5-candle single-page), invalid-range
    rejection, unsupported-granularity rejection, granularity
    map exhaustive, API error array surface (4 tests).
  - Bitstamp: happy-path, unsupported granularity, granularity
    map (3 tests).
  - Coinbase: happy-path (with reverse-order chronological
    emission verified), unsupported granularity, granularity
    map, **LHOC ordering guard** (catches the positional-field
    trap — asserts quote = close × vol, not low × vol) (4 tests).

  **Not in this PR**:
  - `ratesengine-ops backfill-external` CLI wrapper around the
    Backfiller interface. Next loop iteration.
  - ExchangeRatesApi / Polygon.io backfill — FX providers have
    different historical shapes (timeseries endpoints); deferred
    until aggregator actually needs historical FX for triangulation
    charts.

- **PR 176 — Polygon.io Forex poller** (2026-04-24): top-tier
  authoritative FX source, pre-approved by Ash as the "authority
  that will not make mistakes" entry in the external fleet. Second
  FX connector (alongside ExchangeRatesApi which is now the
  secondary/redundancy layer).

  - `internal/sources/external/polygonforex/` (new): REST Poller
    against the snapshot endpoint
    `/v2/snapshot/locale/global/markets/forex/tickers`. One call
    returns every forex ticker globally — fits the Poller
    interface cleanly, avoids the per-pair /v1/conversion/ call
    amplification that would otherwise burn rate-limit budget.
  - **Tier requirement documented**: Advanced tier ($199/mo+) for
    the snapshot endpoint. Lower tiers (Starter $29/mo, Developer
    $99/mo) produce ErrAPIRejected at first poll. The pluralised
    "pay the good tier" expectation is baked into events.go's
    package doc so future operators don't accidentally pick a
    tier that silently fails.
  - **Ticker parser**: `C:USDEUR` → (base=USD, quote=EUR).
    Case-insensitive input, strict 6-char length check, 7 unit
    tests (`TestParseCurrencyTicker`).
  - **Mid-price from ask/bid**: `(a + b) / 2` when both sides
    present, single-side fallback when one is missing, skip when
    both zero. Matches institutional FX convention where the
    spread is tight enough that mid is the authoritative
    reference rate.
  - **Rate inversion**: venue returns "1 base = X quote" quotes
    (e.g. USD=EUR 0.9235 meaning 1 USD = 0.9235 EUR). We invert
    to "1 EUR = 1/0.9235 USD = 1.0828" before stamping the
    OracleUpdate. Same asset/quote semantics as ExchangeRatesApi
    so aggregator math across both FX sources is uniform.
  - **Base-filter + pair-filter**: snapshot is global, we filter
    by `p.Base` (only tickers with that base) AND by the
    configured pair list's fiat quote set (don't emit for
    currencies no one queries). Cuts snapshot size ~10× for
    G10-only deployments.
  - **Config**: `PolygonForexVenueConfig{Enabled, APIKey, Base}`.
    APIKey via env override `POLYGON_API_KEY` at
    `config.ApplyEnvOverrides()` time (same secret-field pattern
    as ExchangeRatesApi + Postgres DSN).
  - 10 unit tests: empty-key rejection, happy-path with
    inversion + filter (EUR/GBP land, JPY filtered out),
    `status: "ERROR"` API rejection, 401 unauthorized, 429 rate
    limit, malformed ticker per-entry skip, ticker parser
    exhaustive, mid-price edge cases (both/ask-only/bid-only/
    both-zero), wrong-base ticker skip, PollInterval default.

  **Operator action required to enable**:
   1. Subscribe to Polygon.io Advanced tier.
   2. Set `POLYGON_API_KEY` in the indexer's env.
   3. Flip `[external.polygon_forex].enabled = true` in config.
   Connector emits OracleUpdates into `oracle_updates` table
   with `source = "polygon-forex"` — aggregator consumes
   alongside ExchangeRatesApi for FX triangulation.

- **PR 175 — ExchangeRatesApi FX poller + Poller runtime**
  (2026-04-24): first `external.Poller` implementation; FX side
  of the external fleet comes online.

  - `internal/sources/external/runner.go`: Poller support added
    — per-poller goroutine with a ticker at `PollInterval()`,
    fans `PollOnce` outputs (`[]canonical.Trade` + `[]canonical.OracleUpdate`)
    into the shared sink wrapping them as `TradeEvent` /
    `UpdateEvent`. First poll fires immediately on startup (not
    after the first interval elapses) so fresh data is visible
    within seconds of indexer launch. Transient `PollOnce` errors
    are logged + counted but don't stop the ticker — expected
    behaviour for REST sources hitting rate limits or network
    blips.
  - `internal/sources/external/exchangeratesapi/` (new): REST
    Poller against `https://api.exchangeratesapi.io/v1/latest`.
    - **Emits OracleUpdates, not Trades** — an FX reference rate
      is a computed benchmark, not an executed trade. Consumed
      by the future triangulation layer as the authoritative
      `<fiat>/<base>` cross rate.
    - **Rate inversion**: venue returns `base → symbol` rates
      (e.g. USD base, EUR=0.9235 meaning 1 USD = 0.9235 EUR).
      We invert to canonical "price of <asset> in <quote>"
      form (EUR = 1.0828 USD) before stamping the OracleUpdate.
    - **Tier awareness**: paid-tier requirement documented
      inline — free tier's EUR-only base is rejected at poll
      time via base-mismatch detection. Targets Professional
      tier minimum ($29.99/mo) for USD base + 1-min cadence +
      redistribution rights.
    - **API key via env override**: `APIKey` field follows the
      same secret-field convention as `StorageConfig.PostgresDSN`
      — env var `EXCHANGERATESAPI_KEY` overrides the TOML value
      at `config.ApplyEnvOverrides()` time. Production configs
      keep the TOML value empty.
    - **Pair resolution**: poller scans the configured pair list,
      extracts unique fiat symbols, and requests them in one
      batch call. Crypto-base pairs (XLM/USD, BTC/USD) are
      silently skipped — FX poller doesn't speak crypto, so a
      mixed-pair config is normal.
    - **Unknown currency skip**: venue occasionally returns
      exotic codes (ZZZ test currency, newly added EM symbols);
      skipped per-entry rather than aborting the whole poll.
  - Config: `ExchangeRatesApiVenueConfig{Enabled, APIKey, Base}`
    added to `ExternalConfig`. Default Base is USD.
  - Indexer wiring: `defaultFXPairs(base)` helper returns a
    G10-ish fiat set (EUR, GBP, JPY, CAD, AUD, CHF, NZD, SEK,
    NOK, MXN) as `canonical.Pair` values against the configured
    base. Operator overrides via `p.Symbols` when needed.
  - Tests: 11 total — 2 new runner tests (Poller immediate-fire
    + non-positive-interval rejection), 9 ExchangeRatesApi tests
    (happy-path with inversion, API rejection, base mismatch
    rejection, unknown-currency skip, crypto-pairs silent no-op,
    HTTP 5xx error, PollInterval default, symbol resolution
    excludes base, empty-key rejection).

- **PR 174 — Binance historical backfill** (2026-04-24): first
  `external.Backfiller` implementation. Completes Binance's triple
  capability (live stream + historical candles); every subsequent
  venue's backfill mirrors this shape.

  - `internal/sources/external/binance/backfill.go` (new):
    `Streamer.Backfill(ctx, pair, from, to, granularity)` hits
    `GET /api/v3/klines`, synthesises one `canonical.Trade` per
    candle bucket.
  - **Candle → Trade synthesis**: `Timestamp = close-time`,
    `BaseAmount` = base-asset volume (field 5), `QuoteAmount` =
    quote-asset volume (field 7), scaled at 10^8 integer (same
    `externalAmountDecimals` convention as live stream).
    Open/high/low dropped — consumers who need full OHLC candles
    read from the Timescale continuous aggregates (1m/15m/1h/4h
    /1d/1w/1mo) instead.
  - **Stable tx_hash** across reruns: `backfillTxHash(symbol,
    close_time_ms)` yields a 64-char hex deterministic from the
    bucket's close time. Repeated backfill runs hit the same
    primary key → idempotent insert, no duplicate rows.
  - **Pagination**: Binance caps 1000 candles per request; we
    serially advance `startTime` after each full-page response.
    ~9 requests for 1 year of hourly data. Serial, not parallel
    — respects the per-minute 6000-weight rate-limit budget (each
    klines call costs weight 2).
  - **Granularity support**: 1m / 3m / 5m / 15m / 30m / 1h / 2h
    / 4h / 6h / 12h / 1d / 1w — covers the RFP's listed
    timeframes (1 min, 15 min, 1h, 4h, 1d, 1w) plus common
    intermediates. Unsupported Durations return an error before
    any HTTP call.
  - **Zero-volume candles skipped**: buckets with base=0 or
    quote=0 provide no price signal and would divide-by-zero in
    downstream VWAP math.
  - 8 unit tests: single-page, pagination across 1000-candle
    boundary (1800-candle total), invalid-range rejection,
    unsupported-granularity rejection, granularity map
    exhaustive, empty-response (0 trades), zero-volume skip,
    HTTP-429 surfaces as error.

  **Not in this PR**:
  - `ratesengine-ops backfill-external --source binance --pair
    XLM/USDT --from ... --to ... --granularity 1h` CLI wiring —
    exposes Backfill via an operator command. Deferred to a
    follow-up once the ops binary grows the subcommand shape.
  - Kraken / Bitstamp / Coinbase backfill implementations —
    each reuses the same pattern, different REST endpoints:
    Kraken's `/0/public/OHLC` (capped at 720 intervals),
    Bitstamp's `/api/v2/ohlc/{pair}/`, Coinbase's
    `/products/{id}/candles`. Next loop iterations.

- **PR 172 + 173 — Bitstamp + Coinbase streamers** (2026-04-24):
  two CEX venues shipped in a single loop iteration — both reuse
  the Streamer + DefaultPairs + indexer-wiring pattern
  established by Binance and Kraken.

  **PR 172 — Bitstamp** (`internal/sources/external/bitstamp/`):
  - EUR/GBP XLM depth (XLM/USD, XLM/EUR, XLM/GBP, XLM/BTC) +
    BTC/USD, BTC/EUR, ETH/USD.
  - One subscribe frame per channel — Bitstamp doesn't accept a
    symbol array like Kraken/Coinbase. We send N sequential
    `bts:subscribe` messages on connect.
  - Uses the `amount_str` / `price_str` string fields
    (authoritative) rather than the float64 siblings — i128
    invariant.
  - Honours `bts:request_reconnect` (Bitstamp's ~hourly
    rebalance signal) by closing + reconnecting via the normal
    backoff path. Logged at info rather than warn since it's
    expected behaviour.
  - Microtimestamp parsing (string μs since epoch) with a
    seconds-timestamp fallback for defensive frame variation.
  - 8 unit tests: happy-path trade, request-reconnect surface,
    subscription-succeeded ignore, unknown-event ignore,
    unknown-channel skip, malformed JSON, missing `*_str`
    fields, microsecond fallback.

  **PR 173 — Coinbase Exchange** (`internal/sources/external/coinbase/`):
  - US price discovery — the net-new venue vs `~/code/rates`
    (Coinbase wasn't in the reference system).
  - Targets **Coinbase Exchange** (ex-Pro API, public WS, no
    auth needed for `matches` channel) — NOT Coinbase Advanced
    Trade (retail OAuth, different URLs, heavier rate limits).
    Distinction documented in events.go.
  - Single subscribe with product_ids array covers every pair
    on one connection.
  - Numbers arrive as strings natively — no json.Number dance.
  - Handles both `match` (live) and `last_match` (one-per-
    product on subscribe — carries a real historical trade,
    emitted same as match).
  - `type:"error"` frames surface as ErrSubscriptionRejected
    so the streamer logs loudly on bad product_id config
    instead of tight-looping.
  - 9 unit tests: match happy-path, last_match emission,
    subscriptions ack ignore, error-frame → rejection,
    unknown-product skip, malformed JSON, unknown-type ignore,
    tx-hash dash-normalisation, precision round-trip.

  Both wired into `cmd/ratesengine-indexer` with their
  `External.<venue>.Enabled` toggles (default false — no
  network egress on fresh deployments).

- **PR 171 — Kraken WS v2 streamer** (2026-04-24): second CEX
  connector, widest XLM-fiat coverage of any venue we integrate.
  Native pairs for XLM in USD, EUR, GBP, AUD, CAD, CHF (6 fiats
  directly quoted — no stablecoin proxy needed).

  - `internal/sources/external/kraken/` (new): 4 files following
    the same shape as binance. Subscribes to v2 `trade` channel
    via a JSON method call (vs Binance's URL-based
    subscription); decodes snapshot + update frames; ignores
    heartbeat / status / subscribe-ack frames inline.
  - **Precision handling**: Kraken's v2 API sends qty / price as
    JSON *numbers* (not strings). We decode via `json.Number`
    (via `dec.UseNumber()`) to preserve the original decimal
    representation — float64 is precision-safe at Kraken's 8-dp
    precision but the i128 invariant (ADR-0003) says no floats
    on the price path.
  - **Default pair set**: XLM across all 6 Kraken fiats + BTC/USD
    + ETH/USD. Covers the RFP's "major pairs" requirement for
    XLM without any per-operator tuning. Operator enables via
    `external.kraken.enabled = true` in config.
  - Indexer wiring mirrors Binance: `cfg.External.Kraken.Enabled`
    gates the connector; `startExternalConnectors` appends to
    the same `StreamerSpec` list fed to `external.Run`; shutdown
    path unchanged.
  - Tests: 13 total — 10 parse-layer (happy-path trade,
    snapshot-multi-entry, heartbeat / status / subscribe-ack
    ignored, unknown-symbol skip, malformed-JSON, precision
    cross-check against Binance scaling, symbol-normalised
    hashes) + 3 streamer-level (end-to-end with scripted
    httptest WS server that captures the subscribe request,
    reject empty/unconfigured pairs).

  **Behaviour note**: Kraken delivers a ~50-trade snapshot on
  subscribe. We emit every entry to Timescale with its real
  historical timestamp — small backfill effect on first connect
  that dedupes against future `ratesengine-ops backfill` runs
  via the synthesised tx_hash (symbol + trade_id).

- **PR 170 — Indexer wiring for external connectors** (2026-04-24):
  external streamers now launch from the same `ratesengine-indexer`
  process, share the same event sink, and feed the same Timescale
  trades hypertable as on-chain decoders. End-to-end off-chain
  ingestion is operational (pending config opt-in).

  - `internal/sources/external/runner.go` (new): `Run(ctx,
    streamers, pollers, sink, logger)` fans N streamer channels
    into the shared `consumer.Event` sink, wrapping each
    `canonical.Trade` in `external.TradeEvent`. Returns a
    `wait()` function the indexer's shutdown path calls before
    closing the sink — guarantees no in-flight writes on a
    closed channel. 4 unit tests cover empty-runner behaviour,
    fan-out + TradeEvent wrapping, synchronous Start-error
    propagation, and ctx-cancel cleanup.
  - `internal/sources/external/binance/pairs.go` (new):
    `DefaultPairs()` / `DefaultPairList()` — hardcoded common
    set (XLMUSDT, XLMBTC, BTCUSDT, ETHUSDT). Operator enables
    Binance in config, gets those pairs streaming with zero
    further configuration. Per-venue pair override YAML is a
    follow-up PR once the fleet stabilises.
  - `internal/config/config.go`: new `ExternalConfig` +
    `ExternalVenueConfig{Enabled bool}`. All external venues
    default to `enabled: false` — no network egress until
    operator opts in, eliminating a "fresh deployment
    accidentally streams from Binance" failure mode.
  - `cmd/ratesengine-indexer/main.go`: new
    `startExternalConnectors(ctx, cfg, events, logger)` helper
    builds enabled venues, calls `external.Run`, returns the
    wait func. Threaded into the shutdown sequence between
    ledgerstream stop and events-channel close so drain is
    ordered. Sink type-switch gains `case external.TradeEvent`
    + `case external.UpdateEvent` → existing `persistTrade` /
    `persistOracle` helpers.

  **Behaviour**: with `external.binance.enabled=true` in config
  and no firewall blocking `stream.binance.com:9443`, the indexer
  starts Binance alongside the Galexie dispatcher loop and
  writes XLMUSDT / BTCUSDT / ETHUSDT / XLMBTC trades into the
  `trades` hypertable with `source="binance"`. Stablecoin →
  fiat mapping remains aggregator-side policy (not baked into
  ingest); these rows store the actual pair, not a normalised
  XLM/USD.

  **Not in this PR** (immediate follow-ups):
  - Kraken + Bitstamp + Coinbase streamers (each ~100-150 lines,
    reuse the Streamer + DefaultPairs pattern).
  - Binance historical backfill (`Backfiller.Backfill` body
    against `/api/v3/klines`).
  - Polygon.io Forex poller + ExchangeRatesApi poller (first
    paid-license sources; waiting on operator to provision keys).
  - Aggregator connector pollers (CoinGecko / CoinMarketCap /
    CryptoCompare, class=aggregator → divergence-only).
  - Sovereign anchors (ECB + Fed H.10 daily polls).
  - Integration test that spins up an `httptest` WS server, runs
    the full indexer with Binance enabled, asserts trades land
    in Timescale via `LatestTradesForPair`.

- **PR 169 — External-connector framework + Binance streamer**
  (2026-04-24): first off-chain ingest subsystem. Parallel to the
  dispatcher path — runs its own goroutines speaking HTTPS /
  WebSocket to vendor APIs, but converges on the same canonical
  types + Timescale hypertables.

  - `internal/sources/external/framework.go` (new): three
    orthogonal interfaces — `Streamer` (live WS), `Poller` (REST
    tick), `Backfiller` (historical OHLC). A venue implements
    whichever subset it supports; most CEXes will be
    `Streamer+Backfiller`, aggregators + FX REST feeds are
    `Poller+Backfiller`, sovereign sanity anchors are `Poller`-only.
    Generic `TradeEvent` / `UpdateEvent` wrappers so the indexer
    sink's type-switch gains one case per event kind, not per
    venue.
  - `internal/sources/external/registry.go` (new): single source-
    of-truth map of every venue's `Class` (`exchange` | `aggregator`
    | `oracle` | `authority_sanity`), default weight, VWAP inclusion,
    paid-license flag, backfill availability. Aggregator queries
    this at VWAP compute time to decide contribution. Covers every
    existing on-chain source (soroswap, aquarius, phoenix, comet,
    sdex, reflector×3, redstone, band) + planned off-chain venues
    (binance, kraken, bitstamp, coinbase, bitfinex, polygon-forex,
    exchangeratesapi, coingecko, coinmarketcap, cryptocompare,
    ecb, fed-h10). Unknown sources fail closed: visible in
    `/v1/sources` as `included_in_vwap=false` so ops can see the
    bad entry, but don't silently contribute to aggregation.
  - `internal/sources/external/binance/` (new): first reference
    implementation. Streamer connects to Binance's public combined
    `@aggTrade` WebSocket, parses frames per the verified wire
    spec, emits `canonical.Trade` values. Reconnects with bounded
    exponential backoff + ±25% jitter to avoid thundering-herd on
    shared venue outages. Pair map is explicit (no blind
    auto-subscribe) — operator configures which symbols to
    stream; unknown symbols on the wire are counted + dropped,
    stream stays up.
  - **External-source amount scaling convention**: every off-chain
    source normalises `canonical.Trade.BaseAmount` /
    `QuoteAmount` to a fixed **10^8** integer scale
    (`externalAmountDecimals = 8`). Matches most crypto-native
    venue precision + Redstone's on-chain scale. Aggregator
    queries `external.Lookup(trade.Source).Class` to know which
    side of the on/off-chain boundary a trade came from (on-chain
    uses per-asset decimals). Documented in
    `parse.go:externalAmountDecimals`.
  - **Stablecoin fiat-proxy policy**: ingest stores the actual
    pair (e.g. `XLM/USDT`). The aggregator applies a fiat-proxy
    table (`USDT→USD`, `USDC→USD`, `PYUSD→USD`, `EUROC→EUR`,
    `EUROB→EUR`, `MXNe→MXN`) at VWAP compute time. Keeps the
    stored data honest; depeg failure mode surfaces cleanly
    rather than hiding behind eager normalisation. Per Ash's
    guidance (memory: feedback_production_artifacts).
  - Dep: `github.com/coder/websocket v1.8.14` — pure-Go,
    context-aware, minimal transitive footprint.
  - Tests: 11 unit tests cover the parser, decimal-string scaling,
    tx-hash synthesis, URL build, and end-to-end WebSocket
    streaming against an `httptest` mock server (2-frame scenario,
    verifies trade emission order + stamped fields).

  **Not in this PR** (immediate follow-ups):
  - Backfill implementation for Binance (GET /api/v3/klines →
    synthesised `canonical.Trade` per candle; the interface is
    wired but the body is pending).
  - Wiring into `cmd/ratesengine-indexer` — external connectors
    launched alongside the dispatcher goroutine, sink type-switch
    gains `case external.TradeEvent` / `case external.UpdateEvent`.
  - Additional venues: Kraken, Bitstamp, Coinbase (reuse the
    Streamer interface).
  - Polygon.io Forex + ExchangeRatesApi Pollers.
  - CoinGecko / CoinMarketCap / CryptoCompare aggregators
    (divergence-only, not VWAP).
  - ECB + Fed H.10 daily sanity anchors.

- **PR 168 — Band decoder + ContractCallDecoder interface** (2026-04-24):
  Third oracle integration, and first source that doesn't emit
  events. Band's Soroban StandardReference contract publishes zero
  events on `relay()` / `force_relay()` (verified against pinned
  `bandprotocol/band-std-reference-contracts-soroban` source). A
  conventional event-path Decoder would never fire on a Band update.

  - `internal/dispatcher/dispatcher.go`: new `ContractCallDecoder`
    interface (`Name`, `Matches(contractID, functionName)`,
    `Decode(ContractCallContext)`) + `AddContractCallDecoder`
    registration method + `dispatchContractCall` loop that runs
    per successful InvokeContract op regardless of whether the
    op emitted events. `extractInvokeContractArgs` generalized to
    `extractInvokeContractCalls` — now returns per-op
    `(contractID, functionName, args)` snapshots feeding both
    `events.Event.OpArgs` (Redstone-style event path) and the
    new call-path routing.
  - `internal/sources/band/` (new package): four files in the
    house convention. Decoder matches on `(StandardReference
    contract, {relay | force_relay})`. Decodes `(from, symbol_rates,
    resolve_time, request_id)` for `relay` and the 3-arg subset
    for `force_relay` (no `from` — admin-only path; observer
    falls back to op/tx source). Emits one `OracleUpdate` per
    `(Symbol, u64)` entry at 9 decimals (E9 per
    `band-soroban/src/constant.rs`), USD-quoted. `USD` symbol
    skipped per contract special-case. Timestamp sourced from
    `resolve_time` (UNIX seconds, verified against
    `band-soroban/src/storage/ref_data.rs:56`).
  - `internal/config/`: new `BandOracleConfig{StandardReferenceContract}`,
    `"band"` in `KnownSources`, cross-section + strkey validation.
  - `cmd/ratesengine-indexer/main.go`: `buildDispatcher` gains
    `case band.SourceName: callDecoders = append(...)`; new
    `AddContractCallDecoder` loop at the end of the builder;
    sink type-switch adds `case band.UpdateEvent`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with band relay (no events) lands
    OracleUpdates`. Builds a Soroban envelope whose
    InvokeHostFunction op is `StandardReference.relay(from,
    [("BTC", e9), ("XLM", e9)], resolve_time, request_id)` with
    **SorobanMeta.Events explicitly empty** — proves the
    call-path runs independently of the event-path. Asserts both
    rows land in `oracle_updates` via `LatestOracleUpdateForAsset`.
  - Unit tests cover: happy-path `relay`, happy-path `force_relay`
    (3-arg), USD-symbol skip, unknown-symbol per-entry skip,
    empty rates rejection, too-few-args malformed, decoder
    Matches predicate (accepts relay/force_relay only).

  **Architectural significance:** this is the first decoder that
  bypasses events entirely. The ContractCallDecoder interface
  generalizes — any future Soroban source whose contract reads/
  writes storage without emitting events (Orbit supply, custom
  adapter contracts, future admin-only oracle paths) plugs into
  the same hook. See `docs/discovery/oracles/band.md` for full
  analysis.

- **PR 167 — Comet decoder** (2026-04-23): third on-chain DEX after
  Soroswap + Aquarius + Phoenix. Balancer-v1-style weighted AMM; the
  Blend backstop pool runs on Comet, so this picks up BLND/USDC
  pricing even before broader Comet adoption on pubnet.
  - `internal/sources/comet/` (new package): four files in the
    house convention. Topic = `(Symbol("POOL"), Symbol("swap"))`;
    body = `Map{caller, token_in, token_out, token_amount_in,
    token_amount_out}`. Unlike Soroswap (pair registry) or Phoenix
    (8-event correlation), Comet's swap event is fully
    self-contained — token identities live in the body by field
    name, so the decoder has zero state and no cross-event
    correlation. Matches the Aquarius shape most closely: one
    event → one trade, base = token_in, quote = token_out.
  - `cmd/ratesengine-indexer/main.go`: buildDispatcher gains
    `case comet.SourceName: ...`; sink type-switch gains
    `case comet.TradeEvent`. `config.KnownSources` adds `"comet"`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with comet POOL.swap lands Trade` pairs
    the now-generic `seedSorobanLedger` with a purpose-built
    POOL.swap ContractEvent, runs through the full pipeline, and
    asserts `LatestTradesForPair` returns the row with correct
    source / base amount / quote amount / taker / ledger.
    Removed the reflector-specific `sanityCheckReflectorTopics`
    from `seedSorobanLedger` — the helper is now source-agnostic.
  - Unit tests cover: classify (POOL,swap match, order-swapped
    topic rejection), happy-path decode, non-positive amounts
    rejection, wrong-topic rejection, missing body field
    malformed.

  **Not in this PR** (follow-ups):
  - `join_pool` / `exit_pool` / `deposit` / `withdraw` decoding —
    needed once the aggregator wants live pool-state tracking
    for the spot-price formula (requires reserves + weights).
  - Blend backstop pool address pinning — for targeted BLND/USDC
    pricing without subscribing to every POOL.swap on pubnet.
  - Real mainnet fixture capture.

- **PR 166 — RedStone decoder + OpArgs plumbing** (2026-04-23):
  Second on-chain oracle shipped after Reflector. Closes the long
  path from `Galexie → dispatcher → redstone.Decoder →
  timescale.oracle_updates` for the 4 mainnet feeds currently
  mappable to canonical assets (BTC, ETH, USDC, XLM).
  - `internal/events/event.go`: new `OpArgs []string` field on
    `events.Event`. Carries the base64 SCVal arguments of the
    InvokeHostFunction op that produced the event, populated by
    the dispatcher when the op is an InvokeContract call.
    Optional/omitempty — existing RPC fixture JSON round-trips
    unchanged. Decoders that don't need args (reflector, soroswap,
    aquarius, phoenix) continue to ignore it.
  - `internal/dispatcher/dispatcher.go`: `extractInvokeContractArgs`
    walks the tx envelope's operations once per tx and returns a
    parallel `[][]string`. Events inherit the args of their
    producing op. Marshaling failures degrade gracefully to an
    empty slot (decoders that require args surface the gap
    themselves).
  - `internal/sources/redstone/` (new package): four files following
    the house convention. Topic = `Symbol("REDSTONE")`; body =
    `Map{updater: Address, updated_feeds: Vec<PriceData>}` where
    `PriceData = {price: U256, package_timestamp: u64,
    write_timestamp: u64}`. Feed IDs live in the InvokeContract
    op args (`write_prices(updater, feed_ids, payload)`), NOT in
    the event body — the decoder zips them one-to-one with a
    strict length guard (`ErrFeedIDCountMismatch`) so a
    freshness-verifier rejection can't mis-attribute prices.
    Timestamp is taken from the per-feed `package_timestamp` (the
    oracle's signing time), matching Reflector's pattern of
    preferring oracle-declared time over ledger close time.
  - `internal/scval/scval.go`: new `AsAmountFromU256` accessor.
    RedStone's price field is 256-bit — most other Soroban
    numerics stop at i128/u128 per ADR-0003, so this is the first
    u256 decoder path in the codebase. Backed by
    `canonical.FromUInt256Parts` which assembles the four 64-bit
    words big-endian.
  - `internal/canonical/amount.go`: new `FromUInt256Parts`
    constructor. Composes HiHi/HiLo/LoHi/LoLo → `*big.Int` with
    left-shift chaining, preserving the full u256 range in our
    existing Amount wrapper.
  - `internal/config/`: new `RedstoneOracleConfig` with a single
    `adapter_contract` field (the 19 per-feed proxies emit no
    events — all activity is on the single Adapter).
    `KnownSources` gains `"redstone"`; cross-section validation
    requires the contract address when the source is enabled.
  - `cmd/ratesengine-indexer/main.go`: `buildDispatcher` registers
    `redstone.NewDecoder(cfg.Oracle.Redstone.AdapterContract)`
    when the source is enabled; event-sink type-switch gains
    `case redstone.UpdateEvent: persistOracle(…)`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with redstone write_prices lands
    OracleUpdates`. Constructs a full Soroban envelope whose
    InvokeHostFunction op calls `write_prices(updater,
    ["BTC","ETH"], payload)`, pairs it with a WritePrices event
    body carrying two U256 prices, and asserts both OracleUpdate
    rows land in Timescale via `LatestOracleUpdateForAsset`.
    Proves the full OpArgs → zip → canonical attribution chain
    works under realistic bytes.
  - Unit tests cover: classify, happy-path two-feed, feed-id
    count mismatch, missing op args, unknown-feed per-entry skip,
    all-unknown empty updates, non-REDSTONE topic rejection.

  **Not in this PR** (follow-ups tracked against
  docs/discovery/oracles/redstone.md):
  - RWA feed mappings (BENJI, GILTS, CETES, TESOURO, USTRY, etc.)
    — needs a canonical asset variant for tokenized real-world
    assets.
  - EUROC/EUR, MXNe, PYUSD — stablecoin-to-fiat mapping decisions.
  - Real mainnet fixture capture (`scripts/dev/capture-redstone-
    fixtures.sh`).

- **ADR-0013 accepted** (2026-04-23): adopt
  `github.com/stellar/go-stellar-sdk/xdr` for SCVal decoding in
  Soroban source connectors.
- `internal/scval/` — narrow SCVal helper wrapping the SDK's xdr
  package. Primitives: `Parse`, `EncodeSymbol` / `MustEncodeSymbol`,
  `AsSymbol` / `AsU64` / `AsAmountFromI128` / `AsAmountFromU128` /
  `AsAddressStrkey` / `AsVec` / `AsMap` / `AsTupleN` /
  `MapField` / `MustMapField` / `DecodeAddressOrSymbol`. Re-exports
  `ScVal` + `ScMapEntry` so connectors never import `xdr` directly.
  Golden regression pins the base64 wire bytes for two canonical
  symbols so an SDK upgrade that changes encoding trips a test.
- Reflector decoder ported off stubs. Real `TopicSymbol*` SCVal
  constants computed at init via `scval.MustEncodeSymbol`.
  `decodeUpdate` now pulls the timestamp from `topic[2]` (per the
  real `#[contractevent]` declaration in
  `reflector-contract/oracle/src/events.rs:4-10`), handles both
  `Asset::Stellar(Address)` and `Asset::Other(Symbol)` union arms,
  and surfaces `ErrUnknownFiatSymbol` when an unlisted symbol is
  seen. End-to-end decoder tests in `decode_test.go` use SDK-encoded
  fixtures; `test/fixtures/reflector/README.md` documents the
  real-mainnet capture workflow (pending operator capture).
- `scripts/dev/capture-reflector-fixtures.sh` — capture real
  Reflector update events from a live stellar-rpc into fixture
  JSON per WASM hash.
- 10 real mainnet Reflector fixtures captured under
  `test/fixtures/reflector/v6-2026-04-23/` (4 DEX, 3 CEX, 3 FX).
  `real_fixture_test.go` regression-replays every fixture through
  the decoder. CEX fixtures are currently `t.Skip`ped pending
  crypto-ticker modeling (tracked as PR 164e).
- ADR-0010 fiat allow-list extended with ARS, CLP, COP, IDR, ILS,
  MYR, NOK, PHP, PLN, SEK, THB, UAH, VND — observed in Reflector's
  FX oracle payload during 164a capture.
- **PR 164b**: Soroswap decoder ported off stubs. Real `TopicPrefix*`
  / `TopicSymbol*` constants (String for prefix, Symbol for event
  name), `decodeSwap` + `decodeNewPair` against SDK XDR, factory
  `new_pair` registry wired into the consumer.
- `scval.EncodeString` / `MustEncodeString` / `AsString` — needed
  because Soroswap's topic[0] is `ScvString`, not `ScvSymbol` like
  Reflector's.
- `scripts/dev/encode-topics` — tiny Go CLI for printing base64-
  encoded SCVal::Symbol / SCVal::String wire bytes. Used when
  hardcoding topic blobs into shell capture scripts.
- `scripts/dev/capture-soroswap-fixtures.sh` + `test/fixtures/soroswap/`
  — capture + pin-per-WASM-hash layout matching the Reflector one.
  8 real mainnet swap+sync fixtures land under
  `v1-2026-04-23/`; `real_fixture_test.go` decodes them
  end-to-end. No `new_pair` captures yet (infrequent on mainnet).
- **PR 164c**: Aquarius trade decoder ported off stubs. Real topic
  classification (`TopicSymbolTrade` via scval init), `decodeTrade`
  with assets pulled directly from topics (`token_in` / `token_out`
  / `user` in slots 1–3), body decoded as positional 3-tuple
  (sold_amount, bought_amount, fee) via `scval.AsTupleN`.
  Server-side filter subscribes with `[TopicSymbolTrade, "*",
  "*", "*"]`.
- `scripts/dev/capture-aquarius-fixtures.sh` + `test/fixtures/aquarius/`
  — 10 real mainnet trade captures under `v2-2026-04-23/` (6
  unique tx_hashes), decoded end-to-end by
  `real_fixture_test.go`.
- **PR 164d**: Phoenix swap decoder ported off stubs. Real
  `TopicSymbol*` constants (all `ScvString`, since both topic slots
  are string literals in the pool contract), real `sdkDecodeAddress`
  / `sdkDecodeAsset` / `sdkDecodeI128` for the three body-SCVal
  shapes Phoenix emits. Server-side filter subscribes with
  `[TopicSymbolSwap, "*"]` — a single filter catches all 8
  per-field events.
- `scripts/dev/capture-phoenix-fixtures.sh` + `test/fixtures/phoenix/`
  — 5 complete 8-event swap fixtures (40 field events) under
  `v1-2026-04-23/`. Real-fixture test replays each through the
  `RawSwap` collator + `decodeSwap()`, the same path
  `processPage` drives at runtime.
- **PR 164e**: **ADR-0014 accepted** — `AssetCrypto` variant added
  as sibling to `AssetFiat`. Wire form `crypto:<TICKER>`; initial
  allow-list of 22 tickers (BTC, ETH, USDT, USDC, SOL, XRP, ADA,
  AVAX, DOT, LINK, TON, BNB, DOGE, MATIC, SHIB, NEAR, ATOM, TRX,
  UNI, BCH, LTC, XLM). Threaded through `canonical.Asset.String`,
  `Validate`, `ParseAsset`, JSON round-trip. Parallel test file
  `asset_crypto_test.go`.
- Reflector decoder now dispatches `Asset::Other(Symbol)` through
  fiat → crypto → skip, instead of fiat-only → skip. **All 10 real
  mainnet fixtures** (4 DEX + 3 CEX + 3 FX) now decode end-to-end
  — the `t.Skip` branch from PR 164a/164d for CEX is gone. The
  real-fixture test also asserts the expected `Asset.Type` per
  variant (DEX→Soroban, CEX→Crypto, FX→Fiat), so a future
  mis-classification fails the harness loudly.
- `docs/architecture/contract-schema-evolution.md` — living doc
  covering per-contract WASM-upgrade handling for Soroban sources
  (Soroswap / Phoenix / Aquarius / Reflector). Why backfill must
  be WASM-version-aware, what's known per source, handling
  strategy (Map-field-by-name, topic-dispatch, WASM-hash column
  on ingest rows, gated backfill).
- CLAUDE.md "Things that will surprise you" entry linking to the
  new architecture doc.

- Repository foundation: `LICENSE` (Apache-2.0), `README.md`,
  `CLAUDE.md`, `CHANGELOG.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CODEOWNERS`.
- ADRs 0001–0007 + 0010: Horizon deprecated, MinIO S3-compat,
  i128 no-truncation, Tier-1 validator aspiration, monorepo,
  TimescaleDB for price time-series, Redis cache schema, and
  off-chain fiat representation.
- Root-level `VERSIONS.md` — pinned SHAs of all audited
  upstream deps.
- Makefile targets `dev`, `dev-teardown`, `dev-seed`, `lint`,
  `test`, `test-integration`, `build`, `docs-all`, `verify`.
- `.golangci.yml` strict lint config per
  [engineering-standards.md §8](docs/discovery/engineering-standards.md).
- GitHub Actions `ci.yml`, PR template, CODEOWNERS,
  `dependabot.yml`.
- Phase-1 discovery artefacts under `docs/discovery/`, closure
  doc at `docs/discovery/phase1-closure.md`, RFP × proposal ×
  delivery coverage matrix at `docs/architecture/coverage-matrix.md`.
- HA + multi-region design: `docs/architecture/ha-plan.md`,
  `docs/architecture/infrastructure/{archival-node-spec,
  multi-region-topology, validator-rollout, hosting-options}.md`.
- API design: `docs/reference/api-design.md` + OpenAPI spec at
  `openapi/rates-engine.v1.yaml` (shared error responses,
  pagination, asset / price / history / OHLC / VWAP / TWAP /
  markets / oracle schemas — source of truth for the wire
  contract).
- Repo hygiene + tech-debt prevention plan at
  `docs/architecture/repo-hygiene-plan.md`.
- `internal/canonical/`: `Amount` (i128-safe big.Int wrapper with
  JSON-as-string, SQL Scanner/Valuer, KALIEN regression test,
  `MaxAmountStringLen` DoS cap), `Asset` (tagged union —
  native/classic/soroban/fiat), `Pair` (directional base/quote
  with Flip / EqualEitherWay helpers), `Trade` (stable ID via
  source/ledger/tx_hash/op_index), `Price`, `OracleUpdate`,
  `FiatRate`, and `strkey.go` format validators for G/C addresses.
- `internal/config/`: root `Config` + seven substructs (Region,
  Stellar, Storage, Ingestion, Aggregate, API, Obs) with struct-
  tag–driven doc generator. `Load` + `ApplyEnvOverrides` +
  `Validate` pipeline so env overrides are always validated.
  Startup error-log when `auth_mode != "none"` (auth middleware
  not yet wired). S3 config validated all-or-nothing.
  `docs-config` subcommand on `ratesengine-ops` emits
  `docs/reference/config/README.md` with the mandatory
  generated-file banner.
- `internal/stellarrpc/`: JSON-RPC client wrapping `getHealth`,
  `getLatestLedger`, `getNetwork`, `getVersionInfo`, `getEvents`,
  `getLedgers`, `getFeeStats`. Context-aware, concurrent-safe,
  mockable; identifiable `User-Agent`; post-decode sanity checks
  on GetEvents response (ledger bounds, event order). Tested
  against httptest.Server. `rpc-probe` subcommand on
  `ratesengine-ops`.
- `internal/consumer/`: stable `Source` interface (StreamLive /
  BackfillRange) that every on-chain, oracle, and CEX/FX source
  implements.
- `internal/sources/{soroswap,aquarius,phoenix,reflector}`:
  five-file per-source packages (doc/events/decode/consumer/tests)
  decoding canonical trades from Soroban events with compile-time
  `consumer.Source` assertions. Handles Soroswap Swap+Sync
  correlation, Phoenix 8-event-per-swap fanout, Aquarius
  multi-op-per-tx flat-counter fanout, and Reflector
  three-contract (DEX/CEX/FX) price-vector decoding.
  `sweepStale` uses event `ClosedAt` (not wall-clock) so backfill
  does not synthesise false orphans.
- `internal/storage/timescale/`: typed adapters for trades
  (InsertTrade idempotent, TradesInRange[After] cursor-paged),
  oracle updates, ingestion cursors (DB-level monotonic-advance
  guard), distinct assets + distinct pairs (cursor-paged,
  `hasMore` flag). Pool tuned for Patroni failover windows.
- `internal/api/v1/`: REST server with envelope-wrapped responses
  (`data` / `as_of` / `sources` / `flags` / `pagination`),
  RFC 9457 problem+json errors, handlers for `/healthz`,
  `/readyz` (parallel dependency pings under shared deadline),
  `/version`, `/assets`, `/assets/{asset_id}`, `/price`,
  `/history`, `/ohlc`, `/vwap`, `/twap`, `/markets`,
  `/oracle/latest`, and `/metrics` (unversioned, operator-facing).
- `internal/api/v1/middleware/`: RequestID → HTTPMetrics →
  Logger (slog access + remote_ip context) → Recoverer →
  SecurityHeaders → CORS (allow-list) → RateLimit (per-IP, Redis
  token bucket, skips health + /metrics). Stack order
  audited for preflight-free CORS and ratelimit-after-remote-ip
  invariants.
- `internal/ratelimit/`: Redis-backed atomic Lua token bucket
  with window-remaining Retry-After semantics,
  `url.QueryEscape` key-sanitisation, and bounded key length.
- `internal/metadata/`: SEP-1 / stellar.toml resolver with
  SSRF guard (loopback + RFC 1918 + link-local + metadata-IP
  deny), singleflight fan-in, and a Redis-backed cache that
  tolerates a nil client.
- `internal/obs/`: Prometheus non-default registry, HTTP
  metrics middleware (`http_requests_total`,
  `http_request_duration_seconds`), shared slog factory.
- `migrations/0001_create_trades_hypertable.{up,down}.sql` —
  `trades` hypertable (1-day chunks, compression policy after 7
  days, retention 90 days), four secondary indexes, and
  `ingestion_cursors` table.
- `migrations/0002_create_price_aggregates.{up,down}.sql` — the
  seven RFP-grain continuous aggregates (1m/15m/1h/4h/1d/1w/1mo)
  with VWAP + TWAP + OHLC tuple + per-CAGG refresh & retention
  policies.
- `migrations/0003_create_oracle_updates_hypertable.{up,down}.sql`
  — `oracle_updates` hypertable with compression + retention +
  `(asset_id, source, ts DESC)` index for "latest per source".
- `cmd/ratesengine-migrate`: golang-migrate wrapper with
  subcommands `up`, `down [N]`, `status`, `version`, `force`,
  `help`. DSN via `-dsn` flag or `RATESENGINE_POSTGRES_DSN` env.
- `cmd/ratesengine-indexer`: orchestration binary for the source
  pipeline with graceful shutdown, per-source supervisor +
  restart policy, and an embedded Prometheus scrape server on
  `obs.MetricsListen` so ingestion alerts actually have a target.
- `cmd/ratesengine-api`: REST server binary with `-dry-run` (now
  pings Postgres + Redis for real), signal-driven graceful
  shutdown (30 s drain), SEP-1 cache wiring, optional CORS, and
  optional rate-limit middleware.
- `cmd/ratesengine-aggregator`: scaffold for the VWAP/TWAP +
  continuous-aggregate refresh orchestrator.
- `cmd/ratesengine-ops`: admin CLI with `docs-config`,
  `rpc-probe`, backfill, and gap-detect subcommands.
- `deploy/docker-compose/dev.yaml`: local TimescaleDB (pg15) +
  Redis 7 + MinIO with a one-shot bucket initialiser. Driven by
  `.env.example`. `make dev` end-to-end works.
- `test/integration/`: testcontainers-go round-trip proofs for
  migrations, API (readyz, oracle/latest), trades (multi-op
  fanout, cursor regressions), CHECK-constraint enforcement,
  CAGG policy attachment, DistinctPairs pagination. Guarded by
  `//go:build integration`.
- `configs/ansible/roles/archival-node/`: full Ubuntu-22.04
  bootstrap role (ZFS raidz2, Postgres 15, stellar-core,
  Galexie, stellar-rpc, MinIO, nftables, node_exporter,
  SSH hardening). Hardware-agnostic via inventory.
- `docs/operations/runbooks/`: 38 runbooks covering every
  currently-defined Prometheus alert (ingestion-lag,
  decode-errors, cursor-stuck, rpc-lag, source-stopped,
  orphan-events, cagg-stale, compression-lag, insert-errors,
  price-divergence, price-stale, oracle-stale, api-down,
  api-5xx, api-latency, redis-*, timescale-primary-down,
  archive-*, replica-lag, scrape-failing, deadmansswitch,
  backup-failed, db-disk-full, host-*, nvme-*, pg-conns-saturated,
  zfs-degraded, alertmanager-bad-config, core-lag, core-peers,
  bootstrap-archival-node). CI enforces alert ↔ runbook
  bijection via `scripts/ci/lint-docs.sh`.
- `scripts/ci/lint-docs.sh`: BSD-sed-compatible pre-merge doc
  linter — config drift, OpenAPI routes ↔ handlers, metrics
  catalogue, stale refs, TODOs, frontmatter, banners, ADR
  index, runbook URLs, alerts-catalog drift.

### Fixed

- `internal/sources/reflector/events.go:61` had an incorrect
  schema comment (claimed body was
  `Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}`) — real
  wire shape (verified against mainnet 2026-04-23) is
  `Map{"update_data": Vec<(Val, i128)>}` with `timestamp` in
  topic[2]. `decodeUpdateBody` signature changed from
  `([]PriceEntry, uint64, error)` to `([]PriceEntry, error)`.
- Reflector event timestamp unit is **u64 milliseconds**, not
  seconds. Previous code's `time.Unix(int64(ts), 0)` gave year
  58277; now uses `time.UnixMilli(int64(ts))`.
- Reflector consumer's server-side topic filter had 2 slots but
  real events have 3 (REFLECTOR, update, timestamp). Added the
  `"*"` WildCardExactOne at position 2 so stellar-rpc's
  length-aware matcher doesn't drop every event.
- Soroswap's Phase-1 `TopicSymbolSwap` / `classify` stub assumed
  topic[0] was `Symbol("swap")`. Actual wire format is
  `topic[0]=String("SoroswapPair"), topic[1]=Symbol("swap")` —
  rewritten. A server-side filter built from the stubs would
  have returned zero events.
- Aquarius Phase-1 stub assumed a `Vec<i128>` body with N×N
  in/out fanout driven by a pool-info cache. Real contract emits a
  3-tuple body (sold, bought, fee) with tokens carried in topics —
  zero decoder paths matched reality. Rewritten; dead
  `poolCache` / `SeedPool` / `WithSeededPools` / `PoolInfo` /
  `lookupPool` surface removed.
- Phoenix Phase-1 stub had placeholder topic blobs that never
  matched real events, and three stub body decoders
  (`decodeAddress` / `decodeAsset` / `decodeI128`) that returned
  errors. Real format (verified 2026-04-23): both topic slots are
  `ScvString`, bodies are raw single-value SCVals (no Vec or Map
  wrapper). Decoders real now.
- Renamed reflector's `ErrUnknownFiatSymbol` →
  `ErrUnknownSymbol` now that the decoder tries both fiat and
  crypto allow-lists. Kept the rename note inline at the error
  declaration for discoverability via `git blame`.
- **`InsertOracleUpdate`** used `NULLIF($11, 0)` which typed the
  confidence parameter as integer. Passing a float64 `Confidence`
  crashed the driver with `invalid input syntax for type integer:
  "0.95"`. Fixed to `NULLIF($11, 0.0)`. Would have misfired the
  first time an oracle emitted a non-zero confidence score. Caught
  by the new TestDecoderOutputFitsStorageSchema integration test.
- Pre-existing integration-test fixture bugs surfaced while wiring
  the schema round-trip test:
  - `TestAssetsReaderPagination` used 55-char hand-written
    `CA001JYLG…` strings that failed canonical's 56-char C-strkey
    check. Replaced with `strkey.Encode`-generated seeds
    (`sorobanFromSeed`).
  - `TestStoreRoundTrip` used `Observer: "GRELAYER_FAKE"` (13
    chars); replaced with `gAccountFromSeed`.
  - `TestTradesInRangeAndMarkets`'s `mkIntegrationTrade` embedded
    the literal source string (`"sdex"`) into the tx_hash,
    producing non-hex chars. Now hex-encodes each source byte so
    the hash stays parseable.

### Added — architecture / guardrails

- **PR 165d**: `cmd/ratesengine-indexer/main.go` rewritten against
  the Galexie → ledgerstream → dispatcher flow. No stellar-rpc
  client, no per-source orchestrator, no poll loops.
  - One goroutine drives `ledgerstream.Stream` with an
    unbounded-live-tail range; the callback invokes
    `dispatcher.ProcessLedger` per LCM, forwards emitted
    `consumer.Event`s to the sink goroutine, and upserts the
    pipeline cursor atomically.
  - `buildDispatcher` maps `cfg.Ingestion.EnabledSources` to
    `Decoder` / `OpDecoder` registrations (reflector×3 +
    soroswap + aquarius + phoenix + sdex). Unknown source names
    are fatal at startup.
  - `resolveStartLedger` prefers a persisted pipeline cursor;
    falls back to `cfg.Ingestion.BackfillFromLedger`; refuses
    to silently pick zero (which would re-ingest genesis).
  - Sink goroutine retains panic-recovery + per-source metric
    stamping. Type-switch expanded to include `sdex.TradeEvent`.
  - Cursor table: one `source="ledgerstream"` entry per
    indexer replica; replaces the pre-165 per-source cursors.
- **Source packages cleaned:** each of the four
  `internal/sources/{soroswap,aquarius,phoenix,reflector}/consumer.go`
  shrunk from ~300 LOC of RPC-orchestrator scaffolding to just
  the `TradeEvent` / `UpdateEvent` wrapper + (for Soroswap /
  Phoenix) the correlation buffer. Total deletion:
  `Source` struct, `New`, `Option`, `BackfillRange`,
  `StreamLive`, `processPage`, `filters`, `setError`, `setOK`,
  `recordNewPair`, `setPair`, `lookupPair`, `Health`, `SeedPair`
  (moved to Decoder), `Option` / `WithPollInterval` /
  `WithSeededPairTokens` / `WithDecimals` / `NewDEX` / `NewCEX`
  / `NewFX` / `newVariant`. Per-source `source_test.go`
  migrated off the deleted API; legacy `TestSource_*` renamed
  to `TestDecoder_*` and reshaped to exercise the new Decoder
  seams (pair-registry concurrency, name lookup).
- **lint-imports.baseline empty.** All 5 grandfathered legacy
  violations removed as the refactors landed. The baseline
  header documents that re-adding an entry requires a PR note
  citing why the exception is temporary. `lint-imports.sh`
  allowlist updated to include `cmd/ratesengine-indexer/` in
  rule B (the indexer passes `xdr.LedgerCloseMeta` through as
  legitimate binding glue).

- **PR 165c**: `internal/sources/sdex/` — classic DEX decoder.
  First non-Soroban source. Walks classic op results for
  ManageSellOffer / ManageBuyOffer / CreatePassiveSellOffer /
  PathPaymentStrictReceive / PathPaymentStrictSend. Decodes the
  three `ClaimAtom` variants: OrderBook (modern G-address
  counterparty), LiquidityPool (classic-AMM pool ID as hex Maker),
  and V0 (pre-P18 legacy — skipped with `ErrUnknownClaimAtomType`
  so backfills surface it rather than silently drop).
- `dispatcher.OpDecoder` interface + `Dispatcher.AddOpDecoder` /
  `RouteOp` — sibling to the Soroban `Decoder` interface. Classic
  ops need access to `xdr.Operation` + `xdr.OperationResult`
  which contract events don't carry; `OpContext` bundles both
  along with tx-level metadata (ledger, close time, tx hash, tx
  source). One `ProcessLedger` call now walks both contract
  events and classic ops per transaction. Test coverage: SDEX
  package (7 unit tests, ClaimAtom happy path + multi-claim
  OpIndex-uniqueness fanout + failed-op zero-output + V0 legacy
  skip + negative-amount rejection), dispatcher package
  (`TestRouteOp_*` cross-cutting routing + error accounting).
- **PR 165b**: `internal/events/` + `internal/dispatcher/` + per-
  source Decoder adapters. The one-pipeline pivot from the RPC-
  based per-source orchestrator to the Galexie → dispatcher →
  decoder flow described in
  `docs/architecture/ingest-pipeline.md`.
  - `internal/events/Event` — transport-neutral contract-event
    type (moved from `internal/stellarrpc`). Decoders import
    `events` instead of `stellarrpc`. `stellarrpc.Event` is now a
    deprecated type alias pointing at `events.Event`; callers that
    still build events via the JSON-RPC client keep working
    unchanged.
  - `internal/dispatcher/` — owns the single production ingest
    codepath. `Dispatcher.ProcessLedger` walks a
    `xdr.LedgerCloseMeta` via
    `ingest.NewLedgerTransactionReaderFromLedgerCloseMeta`,
    extracts Soroban contract events per transaction, and routes
    each via `Decoder.Matches` (first-match-wins, byte-equality on
    topic[0]). `Dispatcher.Route` is exposed for test harnesses +
    fixture replay.
  - `internal/sources/{reflector,aquarius,soroswap,phoenix}/dispatcher_adapter.go`
    — each source exports a `NewDecoder(...)` that implements the
    dispatcher's `Decoder` interface. Correlation state (Soroswap
    swap+sync buffer, Phoenix 8-field assembly) moved inside the
    Decoder; no goroutines, no RPC clients, no polling loops.
    Reflector variants take the contract-address scope as an
    explicit constructor arg so the dispatcher can co-register
    all three oracles.
  - `TestEndToEndRouting_withRealFixtures` — feeds every captured
    mainnet fixture through one Dispatcher wired with all 6
    Decoders (4 sources + 3 Reflector variants). Validates that
    72 real events produce 173 canonical outputs with zero
    unmatched hits; per-source ratios (1:1 aquarius, 1:2 soroswap,
    1:8 phoenix, 1:many reflector) are asserted so a future
    routing regression trips loudly.
- **PR 165a**: `internal/ledgerstream/` — thin wrapper around the
  SDK's `ingest.ApplyLedgerMetadata` that reads Galexie's
  MinIO/S3/Filesystem output and yields `xdr.LedgerCloseMeta` per
  ledger to a caller callback. Config binds
  `datastore.DataStoreConfig` + `ledgerbackend.BufferedStorageBackendConfig`
  + optional Prometheus registry into one unit; auto-derives
  sensible buffered-backend defaults. Supports bounded + unbounded
  ranges (backfill + live tail use the same code). Unit tests use
  the filesystem datastore + the SDK's `compressxdr` helpers to
  construct Galexie-shaped fixtures in-test (no binary fixtures
  in the repo).
- `docs/architecture/ingest-pipeline.md` — binding doc for the one
  canonical ingest path (Galexie → ledgerstream → dispatcher →
  decoder). Replaces the earlier "RPC-based source
  `BackfillRange`/`StreamLive`" pattern; documents that
  stellar-rpc was removed from r1 on 2026-04-23.
- CLAUDE.md **Invariant #6** — no stellar-rpc in production
  ingest. Pointer to the ingest-pipeline doc.
- **`scripts/ci/lint-imports.sh`** + `lint-imports.baseline` —
  build-time enforcement of three architectural boundaries:
  - A/no-rpc-in-ingest: `internal/stellarrpc` blocked outside the
    package itself, `cmd/ratesengine-ops/`, `scripts/dev/`,
    source `decode.go` files (transitional), and test files.
  - B/xdr-scoped-to-scval: `go-stellar-sdk/xdr` scoped to
    `internal/scval/`, `internal/ledgerstream/`,
    `internal/dispatcher/` (planned 165b),
    `internal/sources/sdex/` (planned 165c), and test files.
  - C/no-horizon: all Horizon imports banned everywhere
    (ADR-0001).
  Baseline grandfathers 5 known legacy violations (the 4 source
  `consumer.go` files + indexer main, all slated for rewrite in
  PR 165b/d). Lint FAILS on new violations OR stale baseline
  entries — baseline shrinks monotonically. Hooked into
  `make lint-imports`, `make verify`, and a dedicated
  `import-checks` GitHub Actions job.

### Added — integration

- **PR 165e**:
  `test/integration/ledgerstream_to_storage_test.go` —
  `TestEndToEnd_LedgerstreamToTimescale`. First end-to-end
  integration test of the full production ingest path:
  Galexie-shaped `.xdr.zst` on disk → `ledgerstream` → full
  `dispatcher` (all 6 decoders registered: reflector×3 +
  soroswap + aquarius + phoenix + sdex) → `consumer.Event` type
  switch → `timescale.Insert*` → cursor upsert → query back.
  Uses the SDK's filesystem datastore + compressxdr helpers to
  construct valid Galexie batches in-test; no binary fixtures.
  Two subtests:

  1. **bounded range of empty ledgers** — 3 ledgers flow
     through, pipeline persists zero events, cursor advances to
     the last sequence.
  2. **soroban LCM with reflector FX update lands OracleUpdate**
     — constructs a Soroban-flagged `TransactionEnvelope`
     (Ext.V=1 + SorobanData) whose `TransactionMetaV3.SorobanMeta.Events`
     carries a real Reflector FX `xdr.ContractEvent`
     (topic[0]=Symbol("REFLECTOR"), topic[1]=Symbol("update"),
     topic[2]=U64 ms, body=Map{"update_data": Vec<(Symbol,i128)>}),
     signs the envelope hash into `TxProcessing[i].Result`, ships
     through the pipeline, and asserts the row in
     `oracle_updates` carries the expected source / contract /
     ledger / asset / price / decimals / timestamp / observer.
     Proves the hash-matched envelope-lookup + SorobanMeta.Events
     extraction + topic-byte-equality routing all work together
     under realistic bytes. Runs in <1 s.

- `test/integration/decoders_to_storage_test.go` —
  **`TestDecoderOutputFitsStorageSchema`** proves canonical.Trade
  / canonical.OracleUpdate produced by the four Soroban decoders
  satisfy the trades / oracle_updates hypertable schemas. 7
  subtests under one shared Timescale container: soroswap trade,
  aquarius trade, phoenix trade, phoenix large_i128 (ADR-0003
  boundary), reflector fiat_oracle, reflector crypto_oracle (PR
  164e AssetCrypto SQL round-trip), reflector dex_oracle. Runs in
  ~14 s.

### Tested against

- Stellar protocol 25.x (mainnet passphrase
  `"Public Global Stellar Network ; September 2015"`).
- stellar-core `v26.0.1`, stellar-rpc `v26.0.0`,
  stellar-galexie `v26.0.0`.
- `go-stellar-sdk v0.5.0`, `withObsrvr/stellar-extract v0.1.2`.
- `timescale/timescaledb:2.17.2-pg15`, `redis:7.4-alpine`,
  `minio:RELEASE.2024-11-07`.
- `golang-migrate v4.19.1`, `testcontainers-go v0.38+`.

---

<!--
Release sections will be added here as versions ship. Keep the
[Unreleased] block at the top; the release workflow moves it
under the new version header on tag push.

Example of a future release entry:

## [2026.06.30.1] — 2026-06-30 — Initial public release

### Added
- Full SDEX / Soroswap / Aquarius / Phoenix / Comet / Blend indexing.
- Reflector / Redstone / Band oracle integration.
- Since-inception OHLC for top-20 pairs.
- REST + SSE API v1.

### Tested against
- Stellar protocol 25.x.
- stellar-core v26.0.1, stellar-rpc v26.0.0.
-->
