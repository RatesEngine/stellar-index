package config

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
	Region    RegionConfig    `toml:"region" doc:"Region identity — ID, display name, home domain."`
	Stellar   StellarConfig   `toml:"stellar" doc:"Endpoints for stellar-core and stellar-rpc."`
	Storage   StorageConfig   `toml:"storage" doc:"Postgres/TimescaleDB, Redis, MinIO connection details."`
	Ingestion IngestionConfig `toml:"ingestion" doc:"Source orchestration — which connectors to run, backfill bounds, cursor store."`
	Oracle    OracleConfig    `toml:"oracle" doc:"On-chain oracle contract addresses (Reflector, Redstone, Band)."`
	External  ExternalConfig  `toml:"external" doc:"Off-chain connectors — CEX/FX/aggregator sources that run parallel to the on-chain dispatcher."`
	Aggregate AggregateConfig `toml:"aggregate" doc:"VWAP/TWAP windows + outlier thresholds."`
	API       APIConfig       `toml:"api" doc:"Public API serving plane — port, auth mode, rate limits, CDN."`
	Obs       ObsConfig       `toml:"obs" doc:"Metrics, logs, traces — exporters + sampling."`
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
	PostgresDSN     string `toml:"postgres_dsn" doc:"Postgres DSN; password resolved via env: prefix." env:"RATESENGINE_POSTGRES_DSN" default:"postgres://ratesengine@127.0.0.1:5432/ratesengine?sslmode=disable"`
	RedisAddr       string `toml:"redis_addr" doc:"Redis master address host:port." default:"127.0.0.1:6379"`
	RedisPassword   string `toml:"redis_password_env" doc:"Env var holding the Redis password (reference, not the password itself)." env:"RATESENGINE_REDIS_PASSWORD" default:""`
	S3Endpoint      string `toml:"s3_endpoint" doc:"S3-compatible object-store endpoint (MinIO / AWS S3)." default:"http://127.0.0.1:9000"`
	S3Region        string `toml:"s3_region" doc:"S3 region label (free-form for MinIO; AWS region name otherwise)." default:"r1"`
	S3BucketArchive string `toml:"s3_bucket_archive" doc:"Immutable history-archive bucket name." default:"galexie-archive"`
	S3BucketLive    string `toml:"s3_bucket_live" doc:"Live Galexie export bucket name." default:"galexie-live"`
	S3AccessKeyEnv  string `toml:"s3_access_key_env" doc:"Env var holding S3 access key ID." env:"RATESENGINE_S3_ACCESS_KEY" default:"RATESENGINE_S3_ACCESS_KEY"`
	S3SecretKeyEnv  string `toml:"s3_secret_key_env" doc:"Env var holding S3 secret access key." env:"RATESENGINE_S3_SECRET_KEY" default:"RATESENGINE_S3_SECRET_KEY"`
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

// AggregateConfig controls the aggregator's VWAP/TWAP computation.
type AggregateConfig struct {
	VWAPWindowSeconds         int      `toml:"vwap_window_seconds" doc:"Rolling VWAP window in seconds." default:"300"`
	TWAPWindowSeconds         int      `toml:"twap_window_seconds" doc:"Rolling TWAP window in seconds (fallback when volume below threshold)." default:"300"`
	MinUSDVolume              float64  `toml:"min_usd_volume" doc:"Per-pair minimum USD volume within the window for VWAP eligibility." default:"10000"`
	OutlierSigmaThreshold     float64  `toml:"outlier_sigma_threshold" doc:"Reject trades priced > N sigma from the rolling median before VWAP." default:"4"`
	TriangulationEnabled      bool     `toml:"triangulation_enabled" doc:"Enable cross-pair triangulation through USD/BTC when direct pair below threshold." default:"true"`
	IntervalSeconds           int      `toml:"interval_seconds" doc:"Tick cadence — gap between successive (pair, window) refresh passes. 0 falls back to the library default (30s)." default:"30"`
	MaxTradesPerWindow        int      `toml:"max_trades_per_window" doc:"Per-(pair, window) cap on TradesInRange row count to bound a runaway scan. 0 falls back to the library default (10000)." default:"10000"`
	DisableClassFilter        bool     `toml:"disable_class_filter" doc:"Disable the default ClassExchange-only VWAP filter so every fetched trade contributes regardless of source class. Off by default — see internal/sources/external/registry.go for class semantics." default:"false"`
	EnableStablecoinFiatProxy bool     `toml:"enable_stablecoin_fiat_proxy" doc:"Expand fiat-denominated target pairs to include stablecoin backers (XLM/fiat:USD also pulls XLM/USDT/USDC/DAI/PYUSD/USDP and collapses onto the target). Off by default — N+1 TradesInRange calls per (pair, window)." default:"false"`
	Pairs                     []string `toml:"pairs" doc:"Aggregator coverage set as canonical pair strings (\"crypto:XLM/fiat:USD\", \"native/USDC-G…\"). Empty leaves the binary's built-in default (XLM/BTC/ETH × USD/EUR/GBP). Each entry is parsed via canonical.ParseAsset on both sides; an unparseable entry fails Validate." default:"[]"`
	Windows                   []string `toml:"windows" doc:"Per-window cadences as Go time.Duration strings (\"5m\", \"1h\", \"24h\"). Empty leaves the orchestrator's built-in default ([5m, 1h, 24h])." default:"[]"`
}

// APIConfig controls the public REST+SSE server.
type APIConfig struct {
	ListenAddr          string   `toml:"listen_addr" doc:"Bind address for the HTTP server." default:"0.0.0.0:3000"`
	ExternalBaseURL     string   `toml:"external_base_url" doc:"Public-facing base URL (e.g. https://api.ratesengine.net/v1)." default:"https://api.ratesengine.net/v1"`
	AuthMode            string   `toml:"auth_mode" doc:"Authentication mode — none / apikey (planned) / sep10 (planned). Default 'none' because the auth middleware has not shipped yet; a non-'none' value with the current binary is cosmetic, not enforced." default:"none"`
	AnonRateLimitPerMin int      `toml:"anon_rate_limit_per_min" doc:"Per-IP rate limit for anonymous requests." default:"60"`
	KeyRateLimitPerMin  int      `toml:"key_rate_limit_per_min" doc:"Per-API-key rate limit, default tier." default:"1000"`
	CDNEnabled          bool     `toml:"cdn_enabled" doc:"Emit CDN-friendly Cache-Control headers on long-immutable endpoints." default:"true"`
	AllowedOrigins      []string `toml:"allowed_origins" doc:"CORS allow-list for browser clients." default:"[\"*\"]"`
}

// ObsConfig wires metrics, logs, traces.
type ObsConfig struct {
	MetricsListen string  `toml:"metrics_listen" doc:"Bind address for the /metrics Prometheus endpoint." default:"127.0.0.1:9464"`
	LogLevel      string  `toml:"log_level" doc:"Minimum log level — debug / info / warn / error." default:"info"`
	LogFormat     string  `toml:"log_format" doc:"Log format — json / console." default:"json"`
	TraceExporter string  `toml:"trace_exporter" doc:"OpenTelemetry trace exporter — none / otlp." default:"none"`
	TraceSample   float64 `toml:"trace_sample" doc:"Trace sampling ratio — 0.0 (none) to 1.0 (all)." default:"0.1"`
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
			PostgresDSN:     "postgres://ratesengine@127.0.0.1:5432/ratesengine?sslmode=disable",
			RedisAddr:       "127.0.0.1:6379",
			S3Endpoint:      "http://127.0.0.1:9000",
			S3Region:        "r1",
			S3BucketArchive: "galexie-archive",
			S3BucketLive:    "galexie-live",
			S3AccessKeyEnv:  "RATESENGINE_S3_ACCESS_KEY",
			S3SecretKeyEnv:  "RATESENGINE_S3_SECRET_KEY",
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
		API: APIConfig{
			ListenAddr:          "0.0.0.0:3000",
			ExternalBaseURL:     "https://api.ratesengine.net/v1",
			AuthMode:            "none",
			AnonRateLimitPerMin: 60,
			KeyRateLimitPerMin:  1000,
			CDNEnabled:          true,
			AllowedOrigins:      []string{"*"},
		},
		Obs: ObsConfig{
			MetricsListen: "127.0.0.1:9464",
			LogLevel:      "info",
			LogFormat:     "json",
			TraceExporter: "none",
			TraceSample:   0.1,
		},
	}
}
