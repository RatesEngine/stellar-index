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
	Aggregate AggregateConfig `toml:"aggregate" doc:"VWAP/TWAP windows + outlier thresholds."`
	API       APIConfig       `toml:"api" doc:"Public API serving plane — port, auth mode, rate limits, CDN."`
	Obs       ObsConfig       `toml:"obs" doc:"Metrics, logs, traces — exporters + sampling."`
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

// StorageConfig captures every persistent-store connection. DSN
// strings NEVER include passwords directly — use the `env:` tag
// pattern to reference a secret store.
type StorageConfig struct {
	PostgresDSN      string `toml:"postgres_dsn" doc:"Postgres DSN; password resolved via env: prefix." env:"RATESENGINE_POSTGRES_DSN" default:"postgres://ratesengine@127.0.0.1:5432/ratesengine?sslmode=disable"`
	RedisAddr        string `toml:"redis_addr" doc:"Redis master address host:port." default:"127.0.0.1:6379"`
	RedisPassword    string `toml:"redis_password_env" doc:"Env var holding the Redis password (reference, not the password itself)." env:"RATESENGINE_REDIS_PASSWORD" default:""`
	S3Endpoint       string `toml:"s3_endpoint" doc:"S3-compatible object-store endpoint (MinIO / AWS S3)." default:"http://127.0.0.1:9000"`
	S3Region         string `toml:"s3_region" doc:"S3 region label (free-form for MinIO; AWS region name otherwise)." default:"r1"`
	S3BucketArchive  string `toml:"s3_bucket_archive" doc:"Immutable history-archive bucket name." default:"galexie-archive"`
	S3BucketLive     string `toml:"s3_bucket_live" doc:"Live Galexie export bucket name." default:"galexie-live"`
	S3AccessKeyEnv   string `toml:"s3_access_key_env" doc:"Env var holding S3 access key ID." env:"RATESENGINE_S3_ACCESS_KEY" default:"RATESENGINE_S3_ACCESS_KEY"`
	S3SecretKeyEnv   string `toml:"s3_secret_key_env" doc:"Env var holding S3 secret access key." env:"RATESENGINE_S3_SECRET_KEY" default:"RATESENGINE_S3_SECRET_KEY"`
}

// IngestionConfig controls the indexer's source fleet.
type IngestionConfig struct {
	EnabledSources      []string `toml:"enabled_sources" doc:"List of source connector names to run on this indexer replica." default:"[\"sdex\",\"soroswap\",\"aquarius\"]"`
	BackfillFromLedger  uint32   `toml:"backfill_from_ledger" doc:"Earliest ledger to backfill from; 0 = continue-from-persisted-cursor." default:"0"`
	BackfillBatchSize   uint32   `toml:"backfill_batch_size" doc:"Ledgers per backfill fetch batch." default:"64"`
	CursorStoreScheme   string   `toml:"cursor_store_scheme" doc:"Where per-source cursors live — postgres / redis." default:"postgres"`
}

// AggregateConfig controls the aggregator's VWAP/TWAP computation.
type AggregateConfig struct {
	VWAPWindowSeconds     int     `toml:"vwap_window_seconds" doc:"Rolling VWAP window in seconds." default:"300"`
	TWAPWindowSeconds     int     `toml:"twap_window_seconds" doc:"Rolling TWAP window in seconds (fallback when volume below threshold)." default:"300"`
	MinUSDVolume          float64 `toml:"min_usd_volume" doc:"Per-pair minimum USD volume within the window for VWAP eligibility." default:"10000"`
	OutlierSigmaThreshold float64 `toml:"outlier_sigma_threshold" doc:"Reject trades priced > N sigma from the rolling median before VWAP." default:"4"`
	TriangulationEnabled  bool    `toml:"triangulation_enabled" doc:"Enable cross-pair triangulation through USD/BTC when direct pair below threshold." default:"true"`
}

// APIConfig controls the public REST+SSE server.
type APIConfig struct {
	ListenAddr         string   `toml:"listen_addr" doc:"Bind address for the HTTP server." default:"0.0.0.0:3000"`
	ExternalBaseURL    string   `toml:"external_base_url" doc:"Public-facing base URL (e.g. https://api.ratesengine.net/v1)." default:"https://api.ratesengine.net/v1"`
	AuthMode           string   `toml:"auth_mode" doc:"Authentication mode — none / apikey / sep10 (future)." default:"apikey"`
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
			EnabledSources:     []string{"sdex", "soroswap", "aquarius"},
			BackfillFromLedger: 0,
			BackfillBatchSize:  64,
			CursorStoreScheme:  "postgres",
		},
		Oracle: OracleConfig{
			// Reflector mainnet addresses are operator-supplied
			// (Phase-1 audit left exact values TBD; see
			// docs/discovery/oracles/reflector.md). Empty by
			// default — enabling a reflector-* source without
			// setting its address is a startup error.
			Reflector: ReflectorOracleConfig{},
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
			AuthMode:            "apikey",
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
