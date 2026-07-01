---
title: Capability inventory — "does this already exist? where?"
status: canonical artifact — check this BEFORE writing any new utility code
---

# Capability inventory

**Before writing any utility (hash, HMAC, SSRF guard, rate limit, cache key, SCVal
decode, VWAP, email, XDR decode) — it almost certainly exists. Check here first.**
Intent-keyed: *Need to X → use `package.Symbol`*. Every symbol verified present (D4).

## Prices / aggregation math — pure funcs, `internal/aggregate`
- VWAP over a trade slice → `aggregate.VWAP(trades)` (`*big.Rat`, never float)
- TWAP over a window → `aggregate.TWAP(trades, windowEnd)`
- OHLC bar → `aggregate.ComputeOHLC(trades)`
- Drop fat-tail outliers → `aggregate.FilterOutliers(trades, sigma)`
- Triangulate cross-pair → `aggregate.Triangulate` / `TriangulateChain`
- Full tiered global price (VWAP→aggregator fallback) → `aggregate.ComputeGlobalPrice` + `DefaultGlobalPriceOptions()`
- Stablecoin→fiat / expand target pair → `aggregate.FiatProxy`, `ProxyPair`, `ProxyTrade`, `ExpandTargetPair`, `FiatBackers`

## Serving prices — reads, `internal/storage/timescale/aggregates.go`
- Latest closed 1m VWAP → `(*Store).LatestClosedVWAP1mForPair` (closed-bucket contract, ADR-0015; sargable WHERE)
- Recent/point-in-time/ranged → `RecentClosedVWAP1mForPair`, `ClosedVWAP1mAtOrBefore`, `TimedVWAPsForPair1m`, `VWAPsForPair1m`
- USD price via FX pegs → `timescale.NewVWAPUSDFXResolver(...).USDPriceAt(...)` (has its own bucket cache — don't add another)

## Canonical domain types — `internal/canonical`
- Build/parse Asset → `NewCryptoAsset`/`NewFiatAsset`/`NewRWAAsset`/`NewClassicAsset`/`NewSorobanAsset`/`ParseAsset`
- Build/parse Pair → `NewPair`, `ParsePair`
- i128/u128 amount → `canonical.Amount` + `NewAmount(*big.Int)` (ADR-0003, never int64)
- Core rows → `canonical.Trade`, `Price`, `OracleUpdate`

## SCVal / i128 decoding — `internal/scval`
- Parse XDR SCVal → `scval.Parse(b64)`, `ParseBytes(raw)`
- i128/u128/u256 → amount → `AsAmountFromI128`/`AsAmountFromU128`/`AsAmountFromU256` (never `int64(parts.Lo)`)
- map/vec/tuple/address/symbol/… → `AsMap`, `MapField`, `MustMapField` (decode-by-NAME, schema-safe), `AsVec`, `AsTupleN`, `AsAddressStrkey`, `AsSymbol`, `AsString`, `AsU32/U64`, `AsBytes`, `AsBool`
- Encode call args → `EncodeArgsAsScVec`, `DecodeScVecToArgs`

## XDR ledger/op JSON — `internal/xdrjson`  ⚠ (was absent from CLAUDE.md)
- Decode classic op body → `xdrjson.DecodeOperationBody(bodyB64)`
- Participant accounts → `xdrjson.ParticipantAccounts(bodyB64)`
- SAC contract id for a classic asset → `xdrjson.SACContractID(assetID, passphrase)`
- Human names → `OpTypeName`, `MemoTypeName`, `AssetID`, `TrustLineAssetID`

## SEP-1 / stellar.toml + verified currency
- Fetch+parse toml (SSRF-guarded, Redis-cached, coalesced) → `metadata.NewResolver(opts)` + `metadata.NewCache(resolver, rdb).Resolve(ctx, domain)`
- Issuer home_domain as-of ledger → `metadata.NewLCMHomeDomainResolver`, `ChainedHomeDomainLookup`
- Verified currency → `currency.LoadEmbedded().LookupBySlug/LookupByTicker/LookupByStellarAssetID`, `.Browseable`, `.ByClass`, `.CoinGeckoIDs` (never auto-populate seed.yaml)

## SSRF-guarded outbound fetch — ⚠ **DUPLICATED, needs extraction (D4 M0-2)**
- Today: two private impls — `metadata/sep1.go` (`ssrfDialer`, `isBlocked`) AND `customerwebhook/ssrf.go` (`ssrfGuardedDialContext`, `isInternalIP`, exported `IsReservedTLD`). **Target:** a shared `internal/safehttp.GuardedTransport()`; until then reuse `customerwebhook.IsReservedTLD` — do NOT write a third copy.

## Webhooks — `internal/customerwebhook`
- Fan a domain event to customer webhooks → `NewFanout(store, logger).Publish(...)`, `MarshalPayload`
- Drain + HMAC-sign + POST queue → `New(store, opts).Run(ctx)` (signs via unexported `signHMACSHA256` — extract, don't rewrite, if you need to sign elsewhere)

## Rate limiting / usage — Redis
- Token-bucket → `ratelimit.New(rdb, limit, window, opts...).Take(ctx, key)` / `TakeN` (atomic Lua, fail-open w/ dwell)
- Per-subject daily / MTD counter → `usage.New(rdb, opts...).Increment/MonthToDate/Read` ⚠ (not in CLAUDE.md)
- **A full throttle family already exists** (`auth.NewRedisLoginThrottle`, `NewRedisSignupIPThrottle`, …) — don't hand-roll a Redis throttle

## Cache keys — `internal/cachekeys` (the ONLY canonical key builder, ADR-0007)
- `cachekeys.VWAP/OHLC/Price/Confidence/Freeze/RateLimitKey/TOML/Metadata/Divergence/APIKey/AssetsList/MarketsList/…` — never hand-format a Redis key; prewarm must call readers with byte-identical args

## Prometheus metrics — `internal/obs` (convention, no factory)
- Emit → declare a `prometheus.New*Vec` var in `obs/metrics.go` + register in `init()`; paired pattern `FooTotal{outcome}` + `FooDurationSeconds{outcome}` (copy `DivergenceRefreshTotal`)
- HTTP metrics/route → `obs.HTTPMetrics`, `obs.CaptureRoute` (already wired)
- Logger → `obs.NewLogger(cfg, binary)`; assert metric children in tests → `obstest.HistogramSampleCount`

## Storage connectors
- Served tier (Timescale/PG) → `timescale.Open(ctx, dsn)` → `*Store`
- ClickHouse lake → `clickhouse.NewExplorerReader`, `NewSupplyReader`, `clickhouse.Open` (sink), `NewLiveSink`
- Redis client from config → `redisclient.Build(cfg)`, `redisclient.Mode(cfg)` ⚠ (not in CLAUDE.md; handles sentinel vs single)

## Ingest / decode — `ledgerstream`, `dispatcher`, `pipeline`, `projector`
- Stream archive/live LCM → `ledgerstream.Stream`, `StreamArchiveThenLive`, `NewTieredDataStore` (Galexie→MinIO only, never rpc)
- Route a ledger → `dispatcher.New(decoders...)`; implement `Decoder`/`OpDecoder`/`ContractCallDecoder`/`LedgerEntryChangeDecoder` (pick by what the source emits)
- Add a projected Soroban source → a case in `projector/registry.go::buildSource` + an arm in `pipeline/sink.go::IsProjectedEvent` (projector is the ONLY writer; catch-up = `projector-replay`)
- Add a CEX/FX venue → implement `external.Connector`/`Streamer`/`Poller`/`Backfiller` + register `Metadata` in `external/registry.go` (copy binance/kraken; NOT `consumer.Source`)

## Auth / sessions / API keys — `internal/auth`, `internal/platform`
- Hash an API key → `auth.HashAPIKey(key)` (single source of truth)
- Validate → `auth.NewRedisAPIKeyValidator` / `NewPostgresAPIKeyValidator` (split-brain-prone — don't query the table directly)
- Store/mint → `auth.NewRedisAPIKeyStore(...).Create` / `platform/postgresstore.NewAPIKeyStore(s).Create`
- SEP-10 → `sep10.NewValidator(opts)`, `sep10.NewRedisReplayGuard(rdb)`
- Throttles/verifiers → `NewRedisLoginThrottle`, `NewRedisSignupIPThrottle`, `NewRedisSignupVerifier`, `NewRedisSignupEmailLocker`, `NewRedisSignupTracker`, `NewRedisTouchDebouncer`
- Store contracts → `platform.{AccountStore,APIKeyStore,UsageStore,WebhookStore,TokenStore,BillingStore,AuditStore}` (impls in `postgresstore`)

## Email — `internal/notify`
- Send → `notify.Sender`; `NewResendSender(apiKey)` (prod) / `NoopSender` (dev)
- Magic-link render → `notify.MagicLinkMessage(...)`

## Divergence / completeness / supply / incidents
- Cross-check vs reference → `divergence.Compare`; `NewCoinGeckoReference`, `NewChainlinkReference`; `divergence.NewService(opts)`
- Completeness verdict (ADR-0033) → `completeness.ComputeWatermark`, `AuditRecognition`, `ReconcileCounts`, `SumKinds` (authoritative = `completeness_snapshots`)
- Supply → `supply.NewClassicComputer`, `NewSEP41Computer`, `NewRefresher`, `NewCrossCheckRefresher`, reserve readers
- Incident post-mortems → `incidents.Load(logger)`

## Config + SDK
- Config → `config.Load`, `LoadWithEnv`, `LoadReader`, `Default`
- Call our own API from Go → `pkg/client.New(opts)` (wire types in `pkg/client/types.go`)

---
_Maintenance: this file must stay current — D4 recommends a CI check that every non-source
leaf package has a `doc.go`, and a Definition-of-Done line requiring "checked
CAPABILITY-INVENTORY.md before writing new utility code."_
