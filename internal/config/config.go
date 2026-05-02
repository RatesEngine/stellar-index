package config

import (
	"fmt"
	"time"
)

// Config is the root configuration for every Rates Engine binary.
//
// Fields carry four struct tags:
//
//   - `toml:"…"`       — wire name in the TOML file
//   - `doc:"…"`        — one-line description (required; lint checks)
//   - `env:"…"`        — optional env-var override
//   - `default:"…"`    — default value (for documentation + loader)
//
// Adding a field without `doc:` fails `make docs-config`.
type Config struct {
	Region     RegionConfig     `toml:"region" doc:"Region identity — ID, display name, home domain."`
	Stellar    StellarConfig    `toml:"stellar" doc:"Endpoints for stellar-core and stellar-rpc."`
	Storage    StorageConfig    `toml:"storage" doc:"Postgres/TimescaleDB, Redis, MinIO connection details."`
	Ingestion  IngestionConfig  `toml:"ingestion" doc:"Source orchestration — which connectors to run, backfill bounds, cursor store."`
	Oracle     OracleConfig     `toml:"oracle" doc:"On-chain oracle contract addresses (Reflector, Redstone, Band)."`
	External   ExternalConfig   `toml:"external" doc:"Off-chain connectors — CEX/FX/aggregator sources that run parallel to the on-chain dispatcher."`
	Aggregate  AggregateConfig  `toml:"aggregate" doc:"VWAP/TWAP windows + outlier thresholds."`
	Anomaly    AnomalyConfig    `toml:"anomaly" doc:"Per-asset-class anomaly detection thresholds (ADR-0019 Phase 1 stop-gap)."`
	API        APIConfig        `toml:"api" doc:"Public API serving plane — port, auth mode, rate limits, CDN."`
	Metadata   MetadataConfig   `toml:"metadata" doc:"Asset metadata overlay — SEP-1 issuer→home-domain map, operator overrides."`
	Supply     SupplyConfig     `toml:"supply" doc:"Supply pipeline config — SDF reserve list, operator-managed reserve balances (fallback when the LCM AccountEntry observer hasn't yet covered the watched set), watched classic + SEP-41 asset lists, SAC wrappers, and aggregator-refresh cadence. ADR-0011 (XLM) + ADR-0022 (classic) + ADR-0023 (SEP-41)."`
	Trades     TradesConfig     `toml:"trades" doc:"Trade-insert policy — operator-declared USD-pegged stablecoins so on-chain DEX trades populate trades.usd_volume at insert time (launch-readiness L2.2 phase 1)."`
	Divergence DivergenceConfig `toml:"divergence" doc:"Cross-check references the divergence service consults (CoinGecko + Chainlink). Empty disables; the divergence_warning envelope flag stays unset."`
	Obs        ObsConfig        `toml:"obs" doc:"Metrics, logs, traces — exporters + sampling."`
}

// TradesConfig configures policy that runs at trade-insert time
// (`internal/storage/timescale.Store.InsertTrade`). All fields are
// optional — empty config preserves the off-chain-only `usd_volume`
// behaviour that pre-dates Phase 1 of launch-readiness L2.2.
type TradesConfig struct {
	// USDPeggedClassicAssets is the operator's allow-list of classic
	// credit assets (canonical "CODE-ISSUER" wire form, e.g.
	// "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	// they trust as 1.0-USD-pegged. On-chain trades quoted in any
	// of these assets — or in the SAC contract that wraps any of
	// them, looked up via [SupplyConfig.SACWrappers] — populate
	// `trades.usd_volume` at insert time using `quote_amount /
	// 10^7` (the Stellar classic decimal scale).
	//
	// Empty preserves the pre-Phase-1 default: only off-chain CEX/FX
	// trades populate `usd_volume`, on-chain DEX trades store NULL.
	// See `docs/operations/runbooks/`-adjacent doc + the L2.2 caveat
	// on the volume_24h_usd OpenAPI surface.
	USDPeggedClassicAssets []string `toml:"usd_pegged_classic_assets" doc:"Classic credit asset_keys (CODE-ISSUER) the operator declares as USD-pegged stablecoins. On-chain DEX trades quoted in these (or their SAC wrappers, transitive via [supply.sac_wrappers]) populate trades.usd_volume at insert time. Empty preserves the off-chain-only default." default:"[]"`
}

// validate is the sub-validator hook the top-level Config.Validate
// calls. The deeper "is this a parseable classic asset" check lives
// in `internal/storage/timescale.NewUSDVolumeQuoteSpec` so we don't
// duplicate the parse logic; the indexer's wiring step fails loud
// if any asset_key is unparseable. Here we only catch obvious
// operator-config mistakes (empty strings) so the parse-side error
// names the offending index cleanly.
func (tc TradesConfig) validate() error {
	for i, raw := range tc.USDPeggedClassicAssets {
		if raw == "" {
			return fmt.Errorf("trades: usd_pegged_classic_assets[%d] is empty", i)
		}
	}
	return nil
}

// DivergenceConfig wires the cross-check references the divergence
// service consults. Each enabled reference is constructed in the
// API binary and passed to divergence.NewService; the worker then
// runs them every refresh cycle and the result populates the
// `divergence_warning` envelope flag when the median deviation
// exceeds [Threshold].
//
// Empty config = no references = service runs but writes no
// entries (handler keeps the flag unset). This matches the
// pre-2026-05-01 default; the new behaviour ships CoinGecko
// always-on (no auth required) so divergence detection actually
// fires out of the box.
type DivergenceConfig struct {
	// Threshold is the divergence percentage above which
	// WarningFired is true on the cached result. Forwarded to
	// divergence.ServiceOptions.Threshold. Default 5.0 (5%).
	Threshold float64 `toml:"threshold_pct" doc:"Divergence percentage above which the warning flag fires." default:"5.0"`

	// MinSourcesForWarning is the minimum number of successful
	// references required before WarningFired can be true.
	// Default 2 — a single dissenting source isn't enough.
	MinSourcesForWarning int `toml:"min_sources_for_warning" doc:"Minimum successful references before warning_fired can be true." default:"2"`

	// PerReferenceTimeoutSeconds bounds each reference call.
	// Default 5s.
	PerReferenceTimeoutSeconds int `toml:"per_reference_timeout_seconds" doc:"Bound for each reference call. Default 5." default:"5"`

	// CoinGecko config. Always-on by default (free tier, no
	// auth). Set Enabled=false to skip.
	CoinGecko DivergenceCoinGeckoConfig `toml:"coingecko" doc:"CoinGecko reference (free tier, no auth required)."`

	// Chainlink config. Off by default (operator must provide
	// FeedMap with mainnet feed addresses). When enabled, queries
	// public Ethereum RPC for AggregatorV3 latestAnswer().
	Chainlink DivergenceChainlinkConfig `toml:"chainlink" doc:"Chainlink reference (HTTP cross-check only; not a VWAP contributor)."`
}

// DivergenceCoinGeckoConfig configures the CoinGecko reference.
// IDMap is operator-overridable but defaults to the small built-in
// set covering XLM + the major stablecoins we curate.
type DivergenceCoinGeckoConfig struct {
	Enabled bool              `toml:"enabled" doc:"Whether the CoinGecko reference is wired into the divergence service." default:"true"`
	BaseURL string            `toml:"base_url" doc:"CoinGecko API base URL. Empty defaults to https://api.coingecko.com/api/v3." default:""`
	IDMap   map[string]string `toml:"id_map" doc:"Maps canonical asset_id → CoinGecko slug. Operator-curated; empty falls back to the built-in default covering XLM + major stables." default:"{}"`
}

// DivergenceChainlinkConfig configures the Chainlink reference.
// Off by default — operator opts in by setting RPCURL + FeedMap
// to mainnet AggregatorV3 feed addresses for the pairs they want
// cross-checked.
type DivergenceChainlinkConfig struct {
	Enabled bool   `toml:"enabled" doc:"Whether the Chainlink reference is wired into the divergence service." default:"false"`
	RPCURL  string `toml:"rpc_url" doc:"Ethereum JSON-RPC endpoint. Empty defaults to https://cloudflare-eth.com." default:""`
	// FeedMap maps canonical pair string → mainnet feed address.
	// Pair string format: "<base>/<quote>" e.g. "fiat:EUR/fiat:USD".
	// Decimals defaults to 8 (Chainlink's standard).
	FeedMap map[string]ChainlinkFeedConfig `toml:"feeds" doc:"Maps pair strings to {address, decimals, invert}. Empty disables Chainlink in practice." default:"{}"`
}

// ChainlinkFeedConfig is one entry in the [DivergenceChainlinkConfig.FeedMap].
type ChainlinkFeedConfig struct {
	Address  string `toml:"address" doc:"0x-prefixed mainnet feed contract address." default:""`
	Decimals int    `toml:"decimals" doc:"Power-of-10 divisor for the raw int256. Defaults to 8 (Chainlink standard)." default:"8"`
	Invert   bool   `toml:"invert" doc:"Set true when canonical pair is reciprocal of the feed's natural quote." default:"false"`
}

// defaultDivergenceConfig returns the Default()-shape divergence
// settings: CoinGecko on (free tier, no auth required) so
// divergence_warning fires out of the box; Chainlink off by
// default (operator opts in via FeedMap).
func defaultDivergenceConfig() DivergenceConfig {
	return DivergenceConfig{
		Threshold:                  5.0,
		MinSourcesForWarning:       2,
		PerReferenceTimeoutSeconds: 5,
		CoinGecko: DivergenceCoinGeckoConfig{
			Enabled: true,
			IDMap:   map[string]string{},
		},
		Chainlink: DivergenceChainlinkConfig{
			Enabled: false,
			FeedMap: map[string]ChainlinkFeedConfig{},
		},
	}
}

// MetadataConfig configures the asset-metadata overlay path. Today
// it carries one knob — the curated issuer-account → home-domain
// map — which the API uses to populate AssetDetail.HomeDomain
// before the SEP-1 overlay handler runs.
//
// Why an operator-curated map instead of on-chain derivation:
// AccountEntry.HomeDomain isn't currently indexed in our trades
// hypertable; deriving it would require either a separate
// account-entry observer in the indexer (deferred) or a per-request
// stellar-rpc lookup (latency hit on the hot path). The static map
// is the pragmatic middle ground until that plumbing lands —
// curated entries get the overlay; everything else returns
// sep1_status="not_fetched" cleanly.
type MetadataConfig struct {
	// IssuerHomeDomains maps issuer-account G-strkey → home-domain.
	// E.g. `"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" = "centre.io"`.
	// Empty entries (`""`) are equivalent to the key being absent.
	// TOML representation: `[metadata.issuer_home_domains]` table with
	// one entry per issuer.
	IssuerHomeDomains map[string]string `toml:"issuer_home_domains" doc:"Static curated map of issuer-account G-strkey → home-domain. Populates AssetDetail.HomeDomain so the SEP-1 overlay handler can resolve stellar.toml. Until the on-chain AccountEntry observer ships, this is the only way to enable the overlay for a given issuer." default:"{}"`
}

// HomeDomainFor returns the home-domain registered for the issuer,
// or ("", false) if the issuer isn't curated. Falsy entries (empty
// strings) are treated as "not curated."
func (m MetadataConfig) HomeDomainFor(issuer string) (string, bool) {
	if len(m.IssuerHomeDomains) == 0 {
		return "", false
	}
	h, ok := m.IssuerHomeDomains[issuer]
	if !ok || h == "" {
		return "", false
	}
	return h, true
}

// ExternalConfig controls off-chain connectors that live in
// internal/sources/external/. Each venue toggles via its own
// sub-struct; disabled by default so fresh deployments don't
// attempt network egress until the operator opts in.
//
// Pair lists are hardcoded per venue for v1 (see venue package's
// DefaultPairs). A future PR adds per-venue pair override YAML once
// the fleet stabilises; deferred to keep config surface narrow
// until operators actually ask for it.
type ExternalConfig struct {
	Binance          ExternalVenueConfig         `toml:"binance"          doc:"Binance spot WebSocket aggTrade streamer (XLMUSDT / BTCUSDT / ETHUSDT / XLMBTC)."`
	Kraken           ExternalVenueConfig         `toml:"kraken"           doc:"Kraken v2 WebSocket trade streamer (XLM in 6 fiats: USD/EUR/GBP/AUD/CAD/CHF + BTC/USD, ETH/USD)."`
	Bitstamp         ExternalVenueConfig         `toml:"bitstamp"         doc:"Bitstamp v2 WebSocket live_trades streamer (XLM/USD, XLM/EUR, XLM/GBP, XLM/BTC, BTC/USD, BTC/EUR, ETH/USD)."`
	Coinbase         ExternalVenueConfig         `toml:"coinbase"         doc:"Coinbase Exchange WebSocket matches streamer (XLM-USD, BTC-USD, ETH-USD)."`
	ExchangeRatesApi ExchangeRatesApiVenueConfig `toml:"exchangeratesapi" doc:"ExchangeRatesApi.io REST poller for fiat cross-rates (Professional tier required for USD base + 1-min cadence + redistribution)."`
	PolygonForex     PolygonForexVenueConfig     `toml:"polygon_forex"    doc:"Polygon.io Forex Snapshot poller — institutional-grade FX, Advanced tier ($199/mo+) required."`
	CoinGecko        ExternalVenueConfig         `toml:"coingecko"        doc:"CoinGecko /simple/price poller. Class=aggregator (divergence-only). Free tier works; no auth."`
	CoinMarketCap    CoinMarketCapVenueConfig    `toml:"coinmarketcap"    doc:"CoinMarketCap /v2 quotes poller. Class=aggregator. Paid API key; Standard tier ($79/mo+) for commercial redistribution."`
	CryptoCompare    CryptoCompareVenueConfig    `toml:"cryptocompare"    doc:"CryptoCompare /data/pricemulti poller. Class=aggregator. Paid API key via Authorization header."`
	ECB              ExternalVenueConfig         `toml:"ecb"              doc:"European Central Bank daily FX reference rates. Class=authority_sanity (daily anchor, not VWAP). Free, no auth."`
}

// ExternalVenueConfig is the common per-venue toggle shape for
// credential-less public venues (Binance, Kraken, Bitstamp, Coinbase).
// Paid-tier venues with API keys use their own struct (e.g.
// [ExchangeRatesApiVenueConfig]) that embeds the same Enabled field.
type ExternalVenueConfig struct {
	Enabled bool `toml:"enabled" doc:"Whether this connector runs. Off by default — no network egress until operator opts in." default:"false"`
}

// ExchangeRatesApiVenueConfig extends the common toggle with the
// API-key slot and base-currency override.
//
// APIKey follows the same secret-field convention as
// [StorageConfig.PostgresDSN]: the field holds the actual secret,
// and the `env:` tag names the env var that overrides it at
// [ApplyEnvOverrides] time. Production configs keep APIKey empty
// in the TOML and set the env var at the process level.
type ExchangeRatesApiVenueConfig struct {
	Enabled bool   `toml:"enabled" doc:"Whether this connector runs. Off by default." default:"false"`
	APIKey  string `toml:"api_key" doc:"ExchangeRatesApi access key. Prefer env var; TOML fallback exists for local-dev convenience." env:"EXCHANGERATESAPI_KEY" default:""`
	Base    string `toml:"base" doc:"Base currency (USD, EUR, GBP, …). Defaults to USD. Free tier locked to EUR; paid tier accepts any allow-listed fiat." default:"USD"`
}

// PolygonForexVenueConfig carries the Polygon.io Forex connector
// settings. Advanced tier (~$199/mo) required for the snapshot
// endpoint; lower tiers produce ErrAPIRejected at first poll.
type PolygonForexVenueConfig struct {
	Enabled bool   `toml:"enabled" doc:"Whether this connector runs. Off by default." default:"false"`
	APIKey  string `toml:"api_key" doc:"Polygon.io API key. Prefer env var POLYGON_API_KEY; TOML fallback for local-dev only." env:"POLYGON_API_KEY" default:""`
	Base    string `toml:"base" doc:"Base currency filter. Only tickers matching C:<base><quote> emit. Defaults to USD." default:"USD"`
}

// CoinMarketCapVenueConfig carries the CMC Pro API auth + toggle.
// APIKey follows the same env-override convention as the FX sources.
type CoinMarketCapVenueConfig struct {
	Enabled bool   `toml:"enabled" doc:"Whether this connector runs. Off by default." default:"false"`
	APIKey  string `toml:"api_key" doc:"CMC Pro API key, passed as X-CMC_PRO_API_KEY header. Prefer env var." env:"COINMARKETCAP_API_KEY" default:""`
}

// CryptoCompareVenueConfig carries the CryptoCompare API auth +
// toggle.
type CryptoCompareVenueConfig struct {
	Enabled bool   `toml:"enabled" doc:"Whether this connector runs. Off by default." default:"false"`
	APIKey  string `toml:"api_key" doc:"CryptoCompare API key, passed as 'Authorization: Apikey <KEY>'. Prefer env var." env:"CRYPTOCOMPARE_API_KEY" default:""`
}

// OracleConfig gathers on-chain oracle contract addresses. Each
// provider nests its own sub-struct so the TOML reads naturally:
//
//	[oracle.reflector]
//	dex_contract = "C..."
//	cex_contract = "C..."
//	fx_contract  = "C..."
type OracleConfig struct {
	Reflector ReflectorOracleConfig `toml:"reflector" doc:"Reflector oracle contract addresses per variant (DEX / CEX / FX)."`
	Redstone  RedstoneOracleConfig  `toml:"redstone"  doc:"RedStone Adapter contract address (single adapter owns every feed)."`
	Band      BandOracleConfig      `toml:"band"      doc:"Band Protocol StandardReference contract address (Soroban-native, emits no events — observed via InvokeContract call args)."`
	Soroswap  SoroswapConfig        `toml:"soroswap"  doc:"Soroswap factory contract — used at boot to seed the pair→tokens registry via stellar-rpc view calls. Not required for live ingest, but without it the decoder skips swaps from pairs created before the first processed ledger."`
}

// ReflectorOracleConfig carries the three Reflector contract
// addresses. Leave any variant empty to disable it; the indexer's
// buildSources will reject an enabled source whose address is
// unset rather than silently no-op.
type ReflectorOracleConfig struct {
	DEXContract string `toml:"dex_contract" doc:"Reflector DEX contract (C-prefix) on mainnet."`
	CEXContract string `toml:"cex_contract" doc:"Reflector CEX contract (C-prefix) on mainnet."`
	FXContract  string `toml:"fx_contract"  doc:"Reflector FX contract (C-prefix) on mainnet."`
}

// RedstoneOracleConfig carries the mainnet RedStone Adapter address.
// RedStone's 19 per-feed contracts are thin proxies that don't emit
// events (verified 2026-04-23 via stellar.expert's contract API) —
// all event activity is on the single Adapter, so one address is
// the full configuration surface. See docs/discovery/oracles/redstone.md.
type RedstoneOracleConfig struct {
	AdapterContract string `toml:"adapter_contract" doc:"RedStone Adapter contract (C-prefix) on mainnet — CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG."`
}

// BandOracleConfig carries the mainnet Band StandardReference
// address. Band's Stellar contract emits zero events — we observe
// `relay()` / `force_relay()` InvokeContract calls via the
// dispatcher's ContractCallDecoder interface (PR 168). See
// docs/discovery/oracles/band.md.
type BandOracleConfig struct {
	StandardReferenceContract string `toml:"standard_reference_contract" doc:"Band Protocol StandardReference contract (C-prefix) on mainnet — CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M."`
}

// SoroswapConfig carries the Soroswap factory contract address plus
// an optional stellar-rpc endpoint used to seed the pair→tokens
// registry at boot. Soroswap pair contracts emit swap events that
// carry amounts but NOT token identities; decoding to a canonical
// trade requires the (pair_contract → token0, token1) map that the
// factory maintains. Live dispatch records every new pair on the
// fly via the SoroswapFactory:new_pair event, but pairs created
// before the dispatcher's first ledger are invisible — the seed
// fills that gap.
//
// Leave FactoryContract empty to disable the seed; decoder still
// works for pairs it learns about from live new_pair events.
type SoroswapConfig struct {
	FactoryContract string `toml:"factory_contract" doc:"Soroswap factory contract (C-prefix) on mainnet — CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2."`
	SeedRPCEndpoint string `toml:"seed_rpc_endpoint" doc:"stellar-rpc URL used for the boot-time factory sweep. Any public pubnet endpoint works (e.g. https://mainnet.sorobanrpc.com). Falls back to stellar.rpc_endpoints[0] when empty."`
}

// RegionConfig identifies the region this node belongs to, to tag
// metrics and decide replication direction.
type RegionConfig struct {
	ID         string `toml:"id" doc:"Short region identifier, lowercase (r1/r2/r3)." default:"r1"`
	Name       string `toml:"name" doc:"Human-readable region name (London, Ashburn, …)." default:"London"`
	HomeDomain string `toml:"home_domain" doc:"DNS home domain for this org (used in stellar.toml + SCP quorum sub-quorum)." default:"ratesengine.net"`
}

// StellarConfig points a Rates Engine binary at the stellar-core +
// stellar-rpc endpoints it reads from. Empty values disable the
// corresponding client.
type StellarConfig struct {
	Network           string   `toml:"network" doc:"Network passphrase name — pubnet / testnet / futurenet." default:"pubnet"`
	CoreHTTPEndpoint  string   `toml:"core_http_endpoint" doc:"stellar-core admin HTTP (used for liveness)." default:"http://127.0.0.1:11626"`
	RPCEndpoints      []string `toml:"rpc_endpoints" doc:"stellar-rpc endpoints for getEvents/getLedgers. Tried in order on failover." default:"[\"http://127.0.0.1:8000\"]"`
	HistoryArchiveURL string   `toml:"history_archive_url" doc:"Public history archive (SDF or ours) for backfill catchup." default:"https://history.stellar.org/prd/core-live/core_live_001"`
}

// Well-known Stellar network passphrases, copied from
// github.com/stellar/go-stellar-sdk/network. Kept local so
// internal/config doesn't pull the SDK just for string constants.
const (
	pubnetPassphrase    = "Public Global Stellar Network ; September 2015"
	testnetPassphrase   = "Test SDF Network ; September 2015"
	futurenetPassphrase = "Test SDF Future Network ; October 2022"
)

// Passphrase translates the TOML-friendly short network name
// (pubnet / testnet / futurenet) into the full network passphrase
// string that the Stellar protocol actually uses everywhere —
// stellar-core, go-stellar-sdk datastore manifests, transaction
// signatures. Callers that talk to those subsystems must pass the
// passphrase, not the short name.
//
// Returns "" for unknown values; callers treat that as a config
// error. Validate() rejects unknown names at startup, so a real
// runtime "" here would mean someone bypassed validation.
func (s StellarConfig) Passphrase() string {
	switch s.Network {
	case "pubnet":
		return pubnetPassphrase
	case "testnet":
		return testnetPassphrase
	case "futurenet":
		return futurenetPassphrase
	}
	return ""
}

// StorageConfig captures every persistent-store connection. DSN
// strings NEVER include passwords directly — use the `env:` tag
// pattern to reference a secret store.
type StorageConfig struct {
	PostgresDSN string `toml:"postgres_dsn" doc:"Postgres DSN; password resolved via env: prefix." env:"RATESENGINE_POSTGRES_DSN" default:"postgres://ratesengine@127.0.0.1:5432/ratesengine?sslmode=disable"`
	RedisAddr   string `toml:"redis_addr" doc:"Redis master address host:port. Used when redis_sentinel_addrs is empty (single-node / direct mode). When sentinel addrs are set, this is ignored." default:"127.0.0.1:6379"`
	// Sentinel mode: when redis_sentinel_addrs is non-empty, the
	// client uses go-redis FailoverClient and asks Sentinel for the
	// current primary. Per ADR-0024 (Redis HA via Sentinel) this is
	// the production topology; redis_addr is the fallback for
	// dev/single-node deployments.
	RedisSentinelAddrs []string `toml:"redis_sentinel_addrs" doc:"List of Sentinel host:port addresses. Non-empty enables FailoverClient mode (production HA per ADR-0024); empty falls back to single-node redis_addr." default:"[]"`
	RedisMasterName    string   `toml:"redis_master_name" doc:"Sentinel master name as set in inventory (e.g. ratesengine-r1-cache). Required when redis_sentinel_addrs is non-empty." default:""`
	RedisPassword      string   `toml:"redis_password_env" doc:"Env var holding the Redis password (reference, not the password itself). Used as both requirepass (client auth) and SentinelPassword (sentinel auth) — they're the same secret per the role." env:"RATESENGINE_REDIS_PASSWORD" default:""`
	S3Endpoint         string   `toml:"s3_endpoint" doc:"S3-compatible object-store endpoint (MinIO / AWS S3)." default:"http://127.0.0.1:9000"`
	S3Region           string   `toml:"s3_region" doc:"S3 region label (free-form for MinIO; AWS region name otherwise)." default:"r1"`
	S3BucketArchive    string   `toml:"s3_bucket_archive" doc:"Immutable history-archive bucket name." default:"galexie-archive"`
	S3BucketLive       string   `toml:"s3_bucket_live" doc:"Live Galexie export bucket name." default:"galexie-live"`
	S3AccessKeyEnv     string   `toml:"s3_access_key_env" doc:"Env var holding S3 access key ID." env:"RATESENGINE_S3_ACCESS_KEY" default:"RATESENGINE_S3_ACCESS_KEY"`
	S3SecretKeyEnv     string   `toml:"s3_secret_key_env" doc:"Env var holding S3 secret access key." env:"RATESENGINE_S3_SECRET_KEY" default:"RATESENGINE_S3_SECRET_KEY"`
}

// IngestionConfig controls the indexer's source fleet.
type IngestionConfig struct {
	EnabledSources     []string `toml:"enabled_sources" doc:"List of source connector names to run on this indexer replica. See config.KnownSources for valid values." default:"[\"soroswap\",\"aquarius\",\"phoenix\"]"`
	BackfillFromLedger uint32   `toml:"backfill_from_ledger" doc:"Earliest ledger to backfill from; 0 = continue-from-persisted-cursor." default:"0"`
	BackfillBatchSize  uint32   `toml:"backfill_batch_size" doc:"Ledgers per backfill fetch batch." default:"64"`
	CursorStoreScheme  string   `toml:"cursor_store_scheme" doc:"Where per-source cursors live — postgres / redis." default:"postgres"`

	// LiveSeamLedger is the first ledger written to the live bucket
	// (galexie-live). Ledgers below it live in the historical bucket
	// (galexie-archive); ledgers at or above live in galexie-live.
	// The indexer reads from archive for [from, seam-1] and from live
	// for [seam, ∞), in that order, when from < seam.
	//
	// Set to whatever galexie-append.sh passed as --start when
	// galexie.service first started writing — for r1 today, query
	// the running process args. 0 = no seam configured; indexer
	// reads only galexie-live (the pre-2026-04-26 default).
	LiveSeamLedger uint32 `toml:"live_seam_ledger" doc:"First ledger in the live bucket. Below this, indexer reads from galexie-archive. 0 disables the archive bucket entirely." default:"0"`
}

// AnomalyConfig configures the Phase-1 per-asset-class anomaly
// detection per ADR-0019. The aggregator consults these thresholds
// at bucket-close time to decide whether to publish, warn, or
// freeze the new VWAP.
//
// See `internal/aggregate/anomaly/` for the consumer + the
// algorithm semantics. Phase 2 (statistical baselines) replaces
// these operator-set numbers with per-asset learned thresholds —
// at that point the [Thresholds] table becomes a fallback for
// assets whose baseline isn't yet established.
type AnomalyConfig struct {
	// Enabled gates whether anomaly checks run at all. When false,
	// every bucket is published as-is (no warn / no freeze). Off by
	// default during initial roll-out to avoid surprise 401-with-
	// freeze responses; flip to true once the operator has
	// classified all assets.
	Enabled bool `toml:"enabled" doc:"Master switch. When false, anomaly checks are disabled and every bucket is published as-is. Flip to true after operator has classified the asset set." default:"false"`

	// Thresholds maps asset class → (warn_pct, freeze_pct). Empty
	// or partial maps fall back to the package-default thresholds
	// from `anomaly.DefaultThresholds()`. Each row must satisfy
	// `0 < warn_pct < freeze_pct`. The map MUST contain a `default`
	// entry (the fallback for unclassified assets); the loader
	// fills it from package defaults if the operator omits it.
	//
	// TOML representation:
	//   [anomaly.thresholds.stablecoin]  warn_pct=1.0   freeze_pct=3.0
	//   [anomaly.thresholds.treasury]    warn_pct=1.0   freeze_pct=3.0
	//   [anomaly.thresholds.crypto]      warn_pct=20.0  freeze_pct=50.0
	//   [anomaly.thresholds.governance]  warn_pct=50.0  freeze_pct=100.0
	//   [anomaly.thresholds.default]     warn_pct=30.0  freeze_pct=75.0
	Thresholds map[string]AnomalyThreshold `toml:"thresholds" doc:"Per-class threshold table. Keys are asset class names (stablecoin/treasury/crypto/governance/default). Empty falls back to package defaults; partial maps merge over defaults. The default row is required (loader fills it from package defaults if absent)." default:"{}"`

	// Classifications maps a canonical asset_id (as produced by
	// canonical.Asset.String()) to its asset class. Anything not in
	// the map falls through to ClassDefault.
	//
	// TOML representation:
	//   [anomaly.classifications]
	//   "USDC-GA5Z…" = "stablecoin"
	//   "AQUA-GBN…"  = "governance"
	Classifications map[string]string `toml:"classifications" doc:"Operator-curated map of canonical asset_id → asset class (stablecoin/treasury/crypto/governance). Anything absent falls through to the default class." default:"{}"`

	// Phase2 holds the operator-tunable thresholds for the ADR-0019
	// Phase 2 freeze policy (3-signal AND on confidence + z + source
	// count). Defaults match the package-level hardcoded values; an
	// operator override merges atop those.
	Phase2 Phase2FreezeConfig `toml:"phase2" doc:"Phase 2 (per-asset baseline) freeze thresholds. All three conditions must hold for a freeze. Defaults match the ADR-0019 stop-gap (confidence < 0.10 AND z > 5.0 AND sources <= 1)."`
}

// Phase2FreezeConfig surfaces the ADR-0019 Phase 2 freeze
// thresholds as TOML knobs. All three conditions must hold for a
// freeze; tightening any single threshold makes the gate stricter.
//
// Defaults match the package-level constants in
// `internal/aggregate/orchestrator/phase2_freeze.go`. Operators
// who haven't validated the per-asset baseline against their own
// market data are encouraged to leave these at the defaults until
// they have a sense of false-positive rate.
type Phase2FreezeConfig struct {
	ConfidenceMaxFreeze  float64 `toml:"confidence_max_freeze" doc:"Freeze fires when confidence is strictly less than this. ADR-0019 default 0.10." default:"0.10"`
	ZScoreMinFreeze      float64 `toml:"z_score_min_freeze" doc:"Freeze fires when z-score is strictly greater than this. ADR-0019 default 5.0 (the documented 5σ trigger)." default:"5.0"`
	SourceCountMaxFreeze int     `toml:"source_count_max_freeze" doc:"Freeze fires when source count is at or below this. ADR-0019 default 1 (single-source pattern)." default:"1"`
}

// AnomalyThreshold is one row of the anomaly threshold table.
// Mirrors `anomaly.Thresholds` but uses TOML-friendly types so the
// loader doesn't need a custom unmarshaller.
type AnomalyThreshold struct {
	WarnPct   float64 `toml:"warn_pct" doc:"Deviation above this percentage triggers ActionWarn (publish with divergence_warning flag)." default:"30.0"`
	FreezePct float64 `toml:"freeze_pct" doc:"Deviation above this percentage triggers ActionFreeze when source_count<=1 (don't publish; serve last-known-good)." default:"75.0"`
}

// AggregateConfig controls the aggregator's VWAP/TWAP computation.
type AggregateConfig struct {
	VWAPWindowSeconds         int                        `toml:"vwap_window_seconds" doc:"Rolling VWAP window in seconds." default:"300"`
	TWAPWindowSeconds         int                        `toml:"twap_window_seconds" doc:"Rolling TWAP window in seconds (fallback when volume below threshold)." default:"300"`
	MinUSDVolume              float64                    `toml:"min_usd_volume" doc:"Per-pair minimum USD volume within the window for VWAP eligibility." default:"10000"`
	OutlierSigmaThreshold     float64                    `toml:"outlier_sigma_threshold" doc:"Reject trades priced > N sigma from the rolling median before VWAP." default:"4"`
	TriangulationEnabled      bool                       `toml:"triangulation_enabled" doc:"Master switch for the post-refresh triangulation pass. When true (default), the aggregator runs each aggregate.triangulations chain × window after the per-pair refresh, multiplying the leg VWAPs and writing the implied target price. When false, the pass is skipped entirely regardless of aggregate.triangulations entries — an operator-side kill-switch for the triangulation feature without having to clear the chain table." default:"true"`
	IntervalSeconds           int                        `toml:"interval_seconds" doc:"Tick cadence — gap between successive (pair, window) refresh passes. 0 falls back to the library default (30s)." default:"30"`
	MaxTradesPerWindow        int                        `toml:"max_trades_per_window" doc:"Per-(pair, window) cap on TradesInRange row count to bound a runaway scan. 0 falls back to the library default (10000)." default:"10000"`
	DisableClassFilter        bool                       `toml:"disable_class_filter" doc:"Disable the default ClassExchange-only VWAP filter so every fetched trade contributes regardless of source class. Off by default — see internal/sources/external/registry.go for class semantics." default:"false"`
	EnableStablecoinFiatProxy bool                       `toml:"enable_stablecoin_fiat_proxy" doc:"Expand fiat-denominated target pairs to include stablecoin backers (XLM/fiat:USD also pulls XLM/USDT/USDC/DAI/PYUSD/USDP and collapses onto the target). Off by default — N+1 TradesInRange calls per (pair, window)." default:"false"`
	Pairs                     []string                   `toml:"pairs" doc:"Aggregator coverage set as canonical pair strings (\"crypto:XLM/fiat:USD\", \"native/USDC-G…\"). Empty leaves the binary's built-in default (XLM/BTC/ETH × USD/EUR/GBP). Each entry is parsed via canonical.ParseAsset on both sides; an unparseable entry fails Validate." default:"[]"`
	Windows                   []string                   `toml:"windows" doc:"Per-window cadences as Go time.Duration strings (\"5m\", \"1h\", \"24h\"). Empty leaves the orchestrator's built-in default ([5m, 1h, 24h])." default:"[]"`
	Triangulations            []TriangulationChainConfig `toml:"triangulations" doc:"Operator-configured chain pricing entries — each row defines a target pair plus an ordered chain of leg pairs. After the per-pair refresh runs, the orchestrator multiplies each leg's freshly-cached VWAP via aggregate.TriangulateChain and writes the implied target VWAP to its own cache key. Empty (default) skips triangulation entirely." default:"[]"`
}

// TriangulationChainConfig is one row of the triangulation table.
// Target is the implied pair (e.g. "crypto:XLM/fiat:EUR"); Legs is
// the ordered chain whose product yields the target price (e.g.
// ["crypto:XLM/fiat:USD", "fiat:USD/fiat:EUR"]). Loader validates
// that target = Legs[0].Base / Legs[-1].Quote and that adjacent
// legs share their pivot asset (Legs[i].Quote == Legs[i+1].Base).
type TriangulationChainConfig struct {
	Target string   `toml:"target" doc:"Implied target pair (canonical wire form)."`
	Legs   []string `toml:"legs" doc:"Ordered chain of leg pairs; product yields the target price. Must have at least 2 entries and adjacent legs must share their pivot asset."`
}

// APIConfig controls the public REST+SSE server.
type APIConfig struct {
	ListenAddr          string      `toml:"listen_addr" doc:"Bind address for the HTTP server." default:"0.0.0.0:3000"`
	ExternalBaseURL     string      `toml:"external_base_url" doc:"Public-facing base URL (e.g. https://api.ratesengine.net/v1)." default:"https://api.ratesengine.net/v1"`
	AuthMode            string      `toml:"auth_mode" doc:"Authentication mode — none / apikey / sep10. The API binary wires real validators when the required backing dependencies and secrets are present; a deployment that opts into auth without satisfying those requirements fails loud rather than silently demoting to anonymous." default:"none"`
	AnonRateLimitPerMin int         `toml:"anon_rate_limit_per_min" doc:"Per-IP rate limit for anonymous requests." default:"60"`
	KeyRateLimitPerMin  int         `toml:"key_rate_limit_per_min" doc:"Per-API-key rate limit, default tier." default:"1000"`
	CDNEnabled          bool        `toml:"cdn_enabled" doc:"Emit CDN-friendly Cache-Control headers on long-immutable endpoints." default:"true"`
	AllowedOrigins      []string    `toml:"allowed_origins" doc:"CORS allow-list for browser clients." default:"[\"*\"]"`
	TrustedProxyCIDRs   []string    `toml:"trusted_proxy_cidrs" doc:"Immediate peer CIDR allow-list that is permitted to supply X-Forwarded-For. Empty means the API ignores that header and uses the socket peer address for logging, anonymous identity, and IP-based rate limiting." default:"[]"`
	SEP10               SEP10Config `toml:"sep10" doc:"SEP-10 Web Auth — server signing seed, JWT secret, TTLs. Active when auth_mode=sep10 OR when /v1/auth/sep10/* endpoints are exposed."`
}

// SEP10Config configures the SEP-10 Web Auth validator. Both
// SeedEnv and JWTSecretEnv reference environment variable NAMES,
// not values — the actual secrets stay out of the config file
// and the docs-config output. A deployment with auth_mode=sep10
// AND an unset / empty env var fails loud at startup rather than
// silently 503-ing on every challenge.
type SEP10Config struct {
	SeedEnv       string        `toml:"seed_env" doc:"Environment variable holding the server signing keypair S-strkey. Operators rotate this on a schedule; ansible-vault stores the actual value." default:"RATESENGINE_SEP10_SEED"`
	JWTSecretEnv  string        `toml:"jwt_secret_env" doc:"Environment variable holding the HMAC-SHA256 JWT secret (≥ 32 bytes of entropy required)." default:"RATESENGINE_SEP10_JWT_SECRET"`
	WebAuthDomain string        `toml:"web_auth_domain" doc:"SEP-10 web_auth_domain — the host that serves /v1/auth/sep10/*. Carried inside the challenge tx so clients verify before signing. Typically the API's external host (e.g. api.ratesengine.net)." default:"api.ratesengine.net"`
	HomeDomain    string        `toml:"home_domain" doc:"Issuer home_domain. Carried in the JWT iss claim and in the challenge's first manage_data op. Typically same as the project root domain." default:"ratesengine.net"`
	ChallengeTTL  time.Duration `toml:"challenge_ttl" doc:"How long a SEP-10 challenge is valid for signing. SDK requires ≥ 1s; SEP-10 spec recommends 15m." default:"15m"`
	JWTTTL        time.Duration `toml:"jwt_ttl" doc:"Lifetime of an issued JWT. Clients refresh by repeating the challenge → verify flow." default:"1h"`
}

// SupplyConfig configures the supply-snapshot writer (run via
// `ratesengine-ops supply snapshot` or as the in-aggregator
// goroutine when [SupplyConfig.AggregatorRefreshEnabled] is true).
// Per ADR-0011 we don't fabricate values; for native XLM that means
// the writer needs the configured SDF reserve account list (whose
// balances are excluded from circulating) plus an authoritative
// reading of those balances.
//
// Two reserve-balance sources are supported:
//
//  1. The LCM AccountEntry observer (Task #54, shipped) — when the
//     indexer has the watched reserve accounts in
//     `account_observations`, the writer reads live balances from
//     that table and `ReserveBalancesStroops` is unused.
//  2. The static `ReserveBalancesStroops` map — operators backfill
//     this from SDF announcements when the LCM observer hasn't yet
//     covered the reserve account set (e.g. early bring-up before
//     the AccountEntry hypertable is populated).
//
// Empty `SDFReserveAccounts` is valid and yields
// circulating == total (no reserves excluded). Empty
// `ReserveBalancesStroops` with non-empty `SDFReserveAccounts` is
// only valid when the LCM observer covers every named account; the
// writer falls back to rejecting at start otherwise so an operator
// who configured accounts but forgot balances doesn't silently
// publish an over-stated circulating supply.
type SupplyConfig struct {
	// SDFReserveAccounts is the G-strkey list whose XLM balances
	// are subtracted from the frozen total to yield circulating.
	// Per ADR-0011 these are operator-curated; the algorithm itself
	// is policy-agnostic.
	SDFReserveAccounts []string `toml:"sdf_reserve_accounts" doc:"G-strkey list of SDF-controlled reserve accounts whose XLM balances are excluded from circulating supply per ADR-0011 Algorithm 1." default:"[]"`

	// ReserveBalancesStroops maps account G-strkey → balance in
	// stroops as a decimal string (NUMERIC-safe — no float
	// round-trip per ADR-0003). Operators update these manually
	// when SDF announces a reserve move. Every account in
	// SDFReserveAccounts MUST appear here; missing keys are a
	// configuration error caught at writer-start.
	ReserveBalancesStroops map[string]string `toml:"reserve_balances_stroops" doc:"Operator-managed snapshot of each SDF reserve account's XLM balance in stroops (decimal string). Updated manually on SDF reserve-move announcements. Used as the fallback source when the LCM AccountEntry observer (Task #54) hasn't yet populated account_observations for the watched reserve set; the live observer takes over once those rows land." default:"{}"`

	// AggregatorRefreshEnabled, when true, runs the supply-
	// snapshot writer as a goroutine inside the aggregator on a
	// fixed cadence (see [AggregatorRefreshCadence]). When false
	// (the default), operators are expected to drive the writer
	// via the systemd timer in deploy/systemd/supply-snapshot.timer
	// instead. Once the LCM observer (Task #54 — shipped) covers
	// the live operator set, the goroutine path is preferred —
	// snapshots refresh per-cadence rather than per-day, and the
	// systemd timer becomes redundant.
	AggregatorRefreshEnabled bool `toml:"aggregator_refresh_enabled" doc:"Run the supply-snapshot writer as a goroutine in the aggregator instead of via the systemd timer. Requires the LCM AccountEntry observer to be backfilled across the watched accounts (or the static reserve_balances_stroops fallback to be valid)." default:"false"`

	// AggregatorRefreshCadence is the per-cycle interval for the
	// goroutine path (only relevant when AggregatorRefreshEnabled
	// is true). Defaults to 5 minutes — a balance between freshness
	// (operators want observed_at to track current ledger) and
	// table-write rate (asset_supply_history's ON CONFLICT DO
	// NOTHING dedupes per-(asset, ledger), but unique-ledger rows
	// still accumulate at one-per-tick when the chain advances).
	AggregatorRefreshCadence time.Duration `toml:"aggregator_refresh_cadence" doc:"Per-cycle interval for the in-aggregator supply-snapshot worker (only used when aggregator_refresh_enabled is true)." default:"5m"`

	// WatchedClassicAssets is the operator-curated list of classic
	// credit assets the supply pipeline computes Algorithm 2 for.
	// Per ADR-0022 — drives the four classic-supply observers
	// (trustlines / claimable_balances / liquidity_pools /
	// sac_balances) and the aggregator's classic-supply refresh
	// loop. Each entry is a canonical asset string in CODE-ISSUER
	// form (e.g. "USDC-GA5...").
	//
	// Empty (the default) leaves classic-supply observers + refresh
	// off — the existing XLM-only path stays the operator's only
	// surface.
	WatchedClassicAssets []string `toml:"watched_classic_assets" doc:"Operator-curated classic credit assets (CODE-ISSUER form) to track for Algorithm 2 supply per ADR-0022. Empty leaves the classic-supply pipeline off." default:"[]"`

	// SACWrappers maps the C-strkey of a Stellar-Asset-Contract
	// wrapper to the supply.AssetKey form (CODE:ISSUER) of the
	// classic asset it wraps. The SAC observer uses this map to
	// stamp asset_key on every observation row + filter to the
	// watched set.
	//
	// Each watched classic asset that has a SAC wrapper deployed
	// should have an entry here. Operators that haven't deployed
	// SACs (or aren't tracking them) leave this empty; the
	// SAC-component sum is then zero, which is correct for those
	// assets.
	SACWrappers map[string]string `toml:"sac_wrappers" doc:"SAC wrapper contract C-strkey → supply.AssetKey (CODE:ISSUER) map. Drives the SAC balance observer's watched-contract filter. Pure SEP-41 contracts reuse this map by mapping contract_id → contract_id." default:"{}"`

	// WatchedSEP41Contracts is the operator-curated list of SEP-41
	// Soroban contract ids the supply pipeline computes Algorithm 3
	// for. Per ADR-0023 — drives the SEP-41 supply observer
	// (`internal/sources/sep41_supply/`) and the aggregator's
	// SEP-41 supply refresh loop. Each entry is a C-strkey contract
	// id (e.g. "CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7").
	//
	// Empty (the default) leaves the SEP-41 supply pipeline off.
	WatchedSEP41Contracts []string `toml:"watched_sep41_contracts" doc:"Operator-curated SEP-41 Soroban contract C-strkeys to track for Algorithm 3 supply per ADR-0023. Empty leaves the SEP-41 supply pipeline off." default:"[]"`
}

// Validate reports inconsistencies in the supply block. Currently
// checks:
//
//  1. Every configured SDF reserve account has a balance entry —
//     silently publishing an over-stated circulating supply
//     because an operator forgot a balance is the failure mode
//     worth guarding.
//  2. The aggregator-refresh cadence is at least 30s — tighter
//     than that costs more than it buys (the chain hasn't
//     advanced, the refresh writes a no-op snapshot).
//  3. Each WatchedClassicAssets entry parses cleanly. The actual
//     parse runs at aggregator startup; this method just rejects
//     empty strings to catch mistyped TOML before the parser
//     surfaces a less-obvious error.
//  4. Every SACWrappers asset_key is non-empty.
func (sc SupplyConfig) Validate() error {
	if len(sc.SDFReserveAccounts) != 0 {
		for _, acc := range sc.SDFReserveAccounts {
			if _, ok := sc.ReserveBalancesStroops[acc]; !ok {
				return fmt.Errorf("supply: reserve_balances_stroops missing balance for account %q", acc)
			}
		}
	}
	if sc.AggregatorRefreshEnabled && sc.AggregatorRefreshCadence < 30*time.Second {
		return fmt.Errorf("supply: aggregator_refresh_cadence %v < 30s minimum", sc.AggregatorRefreshCadence)
	}
	for i, raw := range sc.WatchedClassicAssets {
		if raw == "" {
			return fmt.Errorf("supply: watched_classic_assets[%d] is empty", i)
		}
	}
	for cid, ak := range sc.SACWrappers {
		if cid == "" {
			return fmt.Errorf("supply: sac_wrappers has empty contract id (asset %q)", ak)
		}
		if ak == "" {
			return fmt.Errorf("supply: sac_wrappers[%q] has empty asset_key", cid)
		}
	}
	for i, c := range sc.WatchedSEP41Contracts {
		if c == "" {
			return fmt.Errorf("supply: watched_sep41_contracts[%d] is empty", i)
		}
	}
	return nil
}

// ObsConfig wires metrics, logs, and (eventually) traces. Metrics
// exposure varies per-binary: the indexer, the aggregator, and the
// long-lived ops commands (e.g. `cross-region-monitor`,
// `verify-archive --metrics`) each bind a dedicated `/metrics`
// listener at [ObsConfig.MetricsListen]; the API binary serves
// `/metrics` on its public listener (so a CDN-fronted deployment
// doesn't need a sidecar port). Trace fields are reserved for the
// future tracing rollout — see [ObsConfig.TraceExporter].
type ObsConfig struct {
	MetricsListen string  `toml:"metrics_listen" doc:"Bind address for the dedicated /metrics Prometheus endpoint. Read by the indexer, the aggregator, and the long-lived ops binaries (cross-region-monitor, verify-archive --metrics). The API binary serves /metrics on its public listener and ignores this field." default:"127.0.0.1:9464"`
	LogLevel      string  `toml:"log_level" doc:"Minimum log level — debug / info / warn / error." default:"info"`
	LogFormat     string  `toml:"log_format" doc:"Log format — json / console." default:"json"`
	TraceExporter string  `toml:"trace_exporter" doc:"OpenTelemetry trace exporter. Currently only 'none' is wired in this build; the 'otlp' value is reserved for the future tracing rollout and is rejected by Validate() until the exporter is implemented (so an operator setting it doesn't think tracing is on when it isn't)." default:"none"`
	TraceSample   float64 `toml:"trace_sample" doc:"Trace sampling ratio — 0.0 (none) to 1.0 (all). Read by the future tracing rollout; ignored in this build." default:"0.1"`
}

// Default returns a Config pre-populated with every field's default
// value. Used by the docs-config generator to show what operators
// get out of the box, and as the starting point for config loading.
func Default() Config {
	return Config{
		Region: RegionConfig{
			ID:         "r1",
			Name:       "London",
			HomeDomain: "ratesengine.net",
		},
		Stellar: StellarConfig{
			Network:           "pubnet",
			CoreHTTPEndpoint:  "http://127.0.0.1:11626",
			RPCEndpoints:      []string{"http://127.0.0.1:8000"},
			HistoryArchiveURL: "https://history.stellar.org/prd/core-live/core_live_001",
		},
		Storage: StorageConfig{
			PostgresDSN: "postgres://ratesengine@127.0.0.1:5432/ratesengine?sslmode=disable",
			RedisAddr:   "127.0.0.1:6379",
			// RedisSentinelAddrs / RedisMasterName left empty — Default()
			// targets dev / single-node. Production inventories override
			// to enable Sentinel mode (ADR-0024).
			RedisSentinelAddrs: []string{},
			RedisMasterName:    "",
			S3Endpoint:         "http://127.0.0.1:9000",
			S3Region:           "r1",
			S3BucketArchive:    "galexie-archive",
			S3BucketLive:       "galexie-live",
			S3AccessKeyEnv:     "RATESENGINE_S3_ACCESS_KEY",
			S3SecretKeyEnv:     "RATESENGINE_S3_SECRET_KEY",
		},
		Ingestion: IngestionConfig{
			EnabledSources:     []string{"soroswap", "aquarius", "phoenix"},
			BackfillFromLedger: 0,
			BackfillBatchSize:  64,
			CursorStoreScheme:  "postgres",
			LiveSeamLedger:     0,
		},
		Oracle: OracleConfig{
			// Reflector mainnet addresses are operator-supplied
			// (Phase-1 audit left exact values TBD; see
			// docs/discovery/oracles/reflector.md). Empty by
			// default — enabling a reflector-* source without
			// setting its address is a startup error.
			Reflector: ReflectorOracleConfig{},
			Redstone:  RedstoneOracleConfig{},
			Band:      BandOracleConfig{},
			Soroswap:  SoroswapConfig{},
		},
		External: ExternalConfig{
			// All off-chain connectors disabled by default.
			// Operator opts in per-venue once they've confirmed
			// network egress / credentials.
			Binance:          ExternalVenueConfig{Enabled: false},
			Kraken:           ExternalVenueConfig{Enabled: false},
			Bitstamp:         ExternalVenueConfig{Enabled: false},
			Coinbase:         ExternalVenueConfig{Enabled: false},
			ExchangeRatesApi: ExchangeRatesApiVenueConfig{Enabled: false, Base: "USD"},
			PolygonForex:     PolygonForexVenueConfig{Enabled: false, Base: "USD"},
			CoinGecko:        ExternalVenueConfig{Enabled: false},
			CoinMarketCap:    CoinMarketCapVenueConfig{Enabled: false},
			CryptoCompare:    CryptoCompareVenueConfig{Enabled: false},
			ECB:              ExternalVenueConfig{Enabled: false},
		},
		Aggregate: AggregateConfig{
			VWAPWindowSeconds:     300,
			TWAPWindowSeconds:     300,
			MinUSDVolume:          10_000,
			OutlierSigmaThreshold: 4,
			TriangulationEnabled:  true,
		},
		Anomaly: AnomalyConfig{
			// Enabled defaults to false — operator opts in once
			// classifications are set per ADR-0019 stop-gap.
			Phase2: Phase2FreezeConfig{
				ConfidenceMaxFreeze:  0.10,
				ZScoreMinFreeze:      5.0,
				SourceCountMaxFreeze: 1,
			},
		},
		API: APIConfig{
			ListenAddr:          "0.0.0.0:3000",
			ExternalBaseURL:     "https://api.ratesengine.net/v1",
			AuthMode:            "none",
			AnonRateLimitPerMin: 60,
			KeyRateLimitPerMin:  1000,
			CDNEnabled:          true,
			AllowedOrigins:      []string{"*"},
			TrustedProxyCIDRs:   []string{},
			SEP10: SEP10Config{
				SeedEnv:       "RATESENGINE_SEP10_SEED",
				JWTSecretEnv:  "RATESENGINE_SEP10_JWT_SECRET",
				WebAuthDomain: "api.ratesengine.net",
				HomeDomain:    "ratesengine.net",
				ChallengeTTL:  15 * time.Minute,
				JWTTTL:        1 * time.Hour,
			},
		},
		Divergence: defaultDivergenceConfig(),
		Obs: ObsConfig{
			MetricsListen: "127.0.0.1:9464",
			LogLevel:      "info",
			LogFormat:     "json",
			TraceExporter: "none",
			TraceSample:   0.1,
		},
	}
}
