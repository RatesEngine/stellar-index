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
