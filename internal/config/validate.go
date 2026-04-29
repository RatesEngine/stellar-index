package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ErrInvalidConfig is the sentinel error every validation failure
// wraps. Use errors.Is(err, ErrInvalidConfig) in callers that need
// to distinguish config bugs from I/O / decode failures.
var ErrInvalidConfig = errors.New("config: invalid")

// KnownSources is the set of source names the indexer's
// buildSources() switch recognises today. Listed here so
// config.Validate can reject typos at boot rather than at
// runtime — an operator running `-dry-run` with `sorowsap`
// in enabled_sources should get a clear error before storage
// connect + RPC probe burn 5+ seconds.
//
// DO NOT import source packages from config (cycle avoidance, see
// contractIDPattern). When you add a source in cmd/ratesengine-indexer/
// main.go buildSources(), mirror the name here.
var KnownSources = map[string]struct{}{
	"soroswap":      {},
	"aquarius":      {},
	"phoenix":       {},
	"comet":         {},
	"sdex":          {},
	"reflector-dex": {},
	"reflector-cex": {},
	"reflector-fx":  {},
	"redstone":      {},
	"band":          {},
}

// Validate checks the loaded Config against the same constraints
// the operator's runbook assumes. Called by [Load] so a malformed
// config fails at startup, not mid-request.
//
// Returns the first error encountered — callers that want a full
// report should fix the first error and re-run.
func (c Config) Validate() error {
	if err := c.Region.validate(); err != nil {
		return err
	}
	if err := c.Stellar.validate(); err != nil {
		return err
	}
	if err := c.Storage.validate(); err != nil {
		return err
	}
	if err := c.Ingestion.validate(); err != nil {
		return err
	}
	if err := c.Oracle.validate(); err != nil {
		return err
	}
	if err := c.Aggregate.validate(); err != nil {
		return err
	}
	if err := c.Anomaly.validate(); err != nil {
		return err
	}
	if err := c.API.validate(); err != nil {
		return err
	}
	if err := c.Obs.validate(); err != nil {
		return err
	}
	// Cross-section checks: enabled sources must have the config
	// they need. These can't live on the individual sub-structs
	// because they span two sections (ingestion + oracle).
	if err := c.validateCrossSection(); err != nil {
		return err
	}
	return nil
}

// validateCrossSection catches config errors that span multiple
// sections — e.g. "you enabled reflector-dex but didn't set the
// contract address." Runs after per-section validates so we can
// assume each section's internal shape is already sound.
func (c Config) validateCrossSection() error {
	for _, name := range c.Ingestion.EnabledSources {
		key := strings.ToLower(strings.TrimSpace(name))
		switch key {
		case "reflector-dex":
			if c.Oracle.Reflector.DEXContract == "" {
				return fmt.Errorf(
					"%w: ingestion.enabled_sources lists %q but oracle.reflector.dex_contract is empty",
					ErrInvalidConfig, name)
			}
		case "reflector-cex":
			if c.Oracle.Reflector.CEXContract == "" {
				return fmt.Errorf(
					"%w: ingestion.enabled_sources lists %q but oracle.reflector.cex_contract is empty",
					ErrInvalidConfig, name)
			}
		case "reflector-fx":
			if c.Oracle.Reflector.FXContract == "" {
				return fmt.Errorf(
					"%w: ingestion.enabled_sources lists %q but oracle.reflector.fx_contract is empty",
					ErrInvalidConfig, name)
			}
		case "redstone":
			if c.Oracle.Redstone.AdapterContract == "" {
				return fmt.Errorf(
					"%w: ingestion.enabled_sources lists %q but oracle.redstone.adapter_contract is empty",
					ErrInvalidConfig, name)
			}
		case "band":
			if c.Oracle.Band.StandardReferenceContract == "" {
				return fmt.Errorf(
					"%w: ingestion.enabled_sources lists %q but oracle.band.standard_reference_contract is empty",
					ErrInvalidConfig, name)
			}
		}
	}
	return nil
}

func (r RegionConfig) validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: region.id required", ErrInvalidConfig)
	}
	if !regionIDPattern.MatchString(r.ID) {
		return fmt.Errorf("%w: region.id %q must be lowercase alphanumeric (e.g. r1, r2, r3)",
			ErrInvalidConfig, r.ID)
	}
	if r.HomeDomain == "" {
		return fmt.Errorf("%w: region.home_domain required", ErrInvalidConfig)
	}
	if strings.Contains(r.HomeDomain, "/") || strings.Contains(r.HomeDomain, "://") {
		return fmt.Errorf("%w: region.home_domain %q must be a bare DNS name, not a URL",
			ErrInvalidConfig, r.HomeDomain)
	}
	return nil
}

func (s StellarConfig) validate() error {
	switch s.Network {
	case "pubnet", "testnet", "futurenet":
		// ok
	default:
		return fmt.Errorf("%w: stellar.network %q must be pubnet/testnet/futurenet",
			ErrInvalidConfig, s.Network)
	}
	if len(s.RPCEndpoints) == 0 {
		return fmt.Errorf("%w: stellar.rpc_endpoints must have at least one URL",
			ErrInvalidConfig)
	}
	// Reject duplicate endpoints. Failover is the whole point of
	// providing a list — duplicates don't buy redundancy, they just
	// look redundant. Case-fold so "http://X" and "HTTP://X" compare
	// equal (URL schemes are case-insensitive per RFC 3986).
	seen := make(map[string]struct{}, len(s.RPCEndpoints))
	for i, ep := range s.RPCEndpoints {
		if _, err := url.Parse(ep); err != nil || !strings.Contains(ep, "://") {
			return fmt.Errorf("%w: stellar.rpc_endpoints[%d] %q must be a full URL",
				ErrInvalidConfig, i, ep)
		}
		key := strings.ToLower(strings.TrimRight(ep, "/"))
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: stellar.rpc_endpoints has duplicate %q",
				ErrInvalidConfig, ep)
		}
		seen[key] = struct{}{}
	}
	// CoreHTTPEndpoint is optional — empty means "don't probe core".
	// When set it must parse as an absolute URL.
	if s.CoreHTTPEndpoint != "" {
		if _, err := url.Parse(s.CoreHTTPEndpoint); err != nil || !strings.Contains(s.CoreHTTPEndpoint, "://") {
			return fmt.Errorf("%w: stellar.core_http_endpoint %q must be a full URL",
				ErrInvalidConfig, s.CoreHTTPEndpoint)
		}
	}
	if _, err := url.Parse(s.HistoryArchiveURL); err != nil {
		return fmt.Errorf("%w: stellar.history_archive_url %q: %w",
			ErrInvalidConfig, s.HistoryArchiveURL, err)
	}
	return nil
}

func (s StorageConfig) validate() error { //nolint:gocognit // dispatch-heavy; splitting would reduce linearity
	if s.PostgresDSN == "" {
		return fmt.Errorf("%w: storage.postgres_dsn required", ErrInvalidConfig)
	}
	if !strings.HasPrefix(s.PostgresDSN, "postgres://") &&
		!strings.HasPrefix(s.PostgresDSN, "postgresql://") {
		return fmt.Errorf("%w: storage.postgres_dsn %q must start with postgres:// or postgresql://",
			ErrInvalidConfig, s.PostgresDSN)
	}
	if s.RedisAddr != "" {
		if _, _, err := net.SplitHostPort(s.RedisAddr); err != nil {
			return fmt.Errorf("%w: storage.redis_addr %q must be host:port: %w",
				ErrInvalidConfig, s.RedisAddr, err)
		}
	}
	// S3 block is all-or-nothing. If an endpoint is set, the
	// dependent fields must also be set — operators who set only
	// some of them get a silent failure at archive-publish time.
	// Empty endpoint disables object storage (local dev / testing).
	if s.S3Endpoint != "" {
		if _, err := url.Parse(s.S3Endpoint); err != nil || !strings.Contains(s.S3Endpoint, "://") {
			return fmt.Errorf("%w: storage.s3_endpoint %q must be a full URL",
				ErrInvalidConfig, s.S3Endpoint)
		}
		if s.S3BucketArchive == "" {
			return fmt.Errorf("%w: storage.s3_bucket_archive required when s3_endpoint is set",
				ErrInvalidConfig)
		}
		if s.S3BucketLive == "" {
			return fmt.Errorf("%w: storage.s3_bucket_live required when s3_endpoint is set",
				ErrInvalidConfig)
		}
		if s.S3AccessKeyEnv == "" {
			return fmt.Errorf("%w: storage.s3_access_key_env required when s3_endpoint is set",
				ErrInvalidConfig)
		}
		if s.S3SecretKeyEnv == "" {
			return fmt.Errorf("%w: storage.s3_secret_key_env required when s3_endpoint is set",
				ErrInvalidConfig)
		}
		// Bucket names must be DNS-compatible per AWS S3 rules:
		// lowercase, 3–63 chars, alnum + hyphen, can't be an IP.
		// MinIO is more permissive but the AWS rule is a safe
		// super-set for portability.
		for _, b := range []struct{ name, v string }{
			{"s3_bucket_archive", s.S3BucketArchive},
			{"s3_bucket_live", s.S3BucketLive},
		} {
			if !s3BucketPattern.MatchString(b.v) {
				return fmt.Errorf("%w: storage.%s %q must be lowercase alnum + hyphen, 3-63 chars",
					ErrInvalidConfig, b.name, b.v)
			}
		}
	}
	return nil
}

func (i IngestionConfig) validate() error {
	switch i.CursorStoreScheme {
	case "postgres", "redis":
		// ok
	default:
		return fmt.Errorf("%w: ingestion.cursor_store_scheme %q must be postgres/redis",
			ErrInvalidConfig, i.CursorStoreScheme)
	}
	if i.BackfillBatchSize == 0 {
		return fmt.Errorf("%w: ingestion.backfill_batch_size must be > 0",
			ErrInvalidConfig)
	}
	// Duplicate source names would spawn multiple consumers on the
	// same event stream — double-counting metrics and doubling orphan
	// buffer memory. Case-fold so ["soroswap", "Soroswap"] is caught
	// too (buildSources lowercases before dispatch).
	//
	// Unknown names are also rejected here. Without this check a typo
	// like "sorowsap" reaches the indexer's buildSources() switch at
	// startup — by then -dry-run has already paid for storage Open +
	// RPC probe, so the operator waits 5+ seconds for a one-char typo
	// to surface. Cross-checking KnownSources closes that loop.
	seen := make(map[string]struct{}, len(i.EnabledSources))
	for _, name := range i.EnabledSources {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return fmt.Errorf("%w: ingestion.enabled_sources contains empty entry",
				ErrInvalidConfig)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: ingestion.enabled_sources has duplicate %q",
				ErrInvalidConfig, name)
		}
		if _, known := KnownSources[key]; !known {
			return fmt.Errorf("%w: ingestion.enabled_sources has unknown source %q "+
				"(expected one of: see config.KnownSources)",
				ErrInvalidConfig, name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (o OracleConfig) validate() error {
	// Empty is allowed — operator can disable every oracle.
	// When set, addresses must be valid C-strkeys.
	for name, addr := range map[string]string{
		"oracle.reflector.dex_contract":           o.Reflector.DEXContract,
		"oracle.reflector.cex_contract":           o.Reflector.CEXContract,
		"oracle.reflector.fx_contract":            o.Reflector.FXContract,
		"oracle.redstone.adapter_contract":        o.Redstone.AdapterContract,
		"oracle.band.standard_reference_contract": o.Band.StandardReferenceContract,
	} {
		if addr == "" {
			continue
		}
		if !contractIDPattern.MatchString(addr) {
			return fmt.Errorf("%w: %s %q is not a valid C-strkey",
				ErrInvalidConfig, name, addr)
		}
	}
	return nil
}

func (a AggregateConfig) validate() error {
	if a.VWAPWindowSeconds <= 0 {
		return fmt.Errorf("%w: aggregate.vwap_window_seconds must be > 0",
			ErrInvalidConfig)
	}
	if a.TWAPWindowSeconds <= 0 {
		return fmt.Errorf("%w: aggregate.twap_window_seconds must be > 0",
			ErrInvalidConfig)
	}
	if a.MinUSDVolume < 0 {
		return fmt.Errorf("%w: aggregate.min_usd_volume must be >= 0",
			ErrInvalidConfig)
	}
	if a.OutlierSigmaThreshold <= 0 {
		return fmt.Errorf("%w: aggregate.outlier_sigma_threshold must be > 0",
			ErrInvalidConfig)
	}
	for _, raw := range a.Pairs {
		if _, err := parsePairString(raw); err != nil {
			return fmt.Errorf("%w: aggregate.pairs entry %q: %w",
				ErrInvalidConfig, raw, err)
		}
	}
	for _, raw := range a.Windows {
		if _, err := time.ParseDuration(raw); err != nil {
			return fmt.Errorf("%w: aggregate.windows entry %q: %w",
				ErrInvalidConfig, raw, err)
		}
	}
	return nil
}

// validate checks the AnomalyConfig's Phase 2 thresholds. Most of
// AnomalyConfig is loose-typed (Thresholds + Classifications maps
// validated at consumer time); the Phase 2 thresholds have a
// well-defined shape and are checked here.
func (a AnomalyConfig) validate() error {
	return a.Phase2.validate()
}

// validate enforces the per-field bounds documented on
// [Phase2FreezeConfig]. Catches config errors at startup rather
// than letting an out-of-band threshold silently disable the
// freeze gate (e.g. confidence_max_freeze=2.0 means "always freeze").
func (p Phase2FreezeConfig) validate() error {
	if p.ConfidenceMaxFreeze < 0 || p.ConfidenceMaxFreeze > 1 {
		return fmt.Errorf("%w: anomaly.phase2.confidence_max_freeze must be in [0, 1] (got %v)",
			ErrInvalidConfig, p.ConfidenceMaxFreeze)
	}
	if p.ZScoreMinFreeze <= 0 {
		return fmt.Errorf("%w: anomaly.phase2.z_score_min_freeze must be > 0 (got %v)",
			ErrInvalidConfig, p.ZScoreMinFreeze)
	}
	if p.SourceCountMaxFreeze < 0 {
		return fmt.Errorf("%w: anomaly.phase2.source_count_max_freeze must be >= 0 (got %d)",
			ErrInvalidConfig, p.SourceCountMaxFreeze)
	}
	return nil
}

// parsePairString resolves a "<base>/<quote>" string into a
// canonical.Pair via canonical.ParseAsset on each side. Lives here
// (not in canonical/) so the canonical package stays free of
// validation-message specifics — the aggregator binary calls the
// same helper at startup to materialise its [Pair] slice.
func parsePairString(s string) (canonical.Pair, error) {
	slash := strings.LastIndex(s, "/")
	if slash <= 0 || slash == len(s)-1 {
		return canonical.Pair{}, fmt.Errorf("expected \"<base>/<quote>\" with a single slash separator")
	}
	base, err := canonical.ParseAsset(s[:slash])
	if err != nil {
		return canonical.Pair{}, fmt.Errorf("base: %w", err)
	}
	quote, err := canonical.ParseAsset(s[slash+1:])
	if err != nil {
		return canonical.Pair{}, fmt.Errorf("quote: %w", err)
	}
	return canonical.NewPair(base, quote)
}

// AggregatorPairs resolves the operator-supplied pair strings into
// canonical.Pair instances. Returns nil when no pairs are
// configured — callers fall back to their built-in default.
//
// validate() already rejects unparseable entries at startup, so
// this re-parse is infallible in practice; we still return an
// error to keep the seam testable and to surface the regression
// loudly if validation is ever bypassed.
func (a AggregateConfig) AggregatorPairs() ([]canonical.Pair, error) {
	if len(a.Pairs) == 0 {
		return nil, nil
	}
	out := make([]canonical.Pair, 0, len(a.Pairs))
	for _, raw := range a.Pairs {
		p, err := parsePairString(raw)
		if err != nil {
			return nil, fmt.Errorf("aggregate.pairs entry %q: %w", raw, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// AggregatorWindows is the time.Duration twin of AggregatorPairs —
// resolves the configured window strings, returning nil when the
// list is empty so callers fall back to orchestrator.DefaultWindows.
func (a AggregateConfig) AggregatorWindows() ([]time.Duration, error) {
	if len(a.Windows) == 0 {
		return nil, nil
	}
	out := make([]time.Duration, 0, len(a.Windows))
	for _, raw := range a.Windows {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("aggregate.windows entry %q: %w", raw, err)
		}
		out = append(out, d)
	}
	return out, nil
}

// ResolvedTriangulationChain is the parsed representation of a
// TriangulationChainConfig — all pair strings resolved to
// canonical.Pair. Returned by [AggregatorTriangulations]; callers
// pass the slice straight into orchestrator.Config.Triangulations
// after type-converting each row.
type ResolvedTriangulationChain struct {
	Target canonical.Pair
	Legs   []canonical.Pair
}

// AggregatorTriangulations resolves the operator-supplied
// TriangulationChainConfig rows into ResolvedTriangulationChain
// instances. Returns nil when no chains are configured.
//
// Each row's Target + Legs are parsed via parsePairString. The
// per-chain structural validation (legs chainable, endpoints match
// target) lives in orchestrator.ValidateTriangulationChain — this
// helper only does the parse step.
func (a AggregateConfig) AggregatorTriangulations() ([]ResolvedTriangulationChain, error) {
	if len(a.Triangulations) == 0 {
		return nil, nil
	}
	out := make([]ResolvedTriangulationChain, 0, len(a.Triangulations))
	for i, row := range a.Triangulations {
		target, err := parsePairString(row.Target)
		if err != nil {
			return nil, fmt.Errorf("aggregate.triangulations[%d].target %q: %w", i, row.Target, err)
		}
		legs := make([]canonical.Pair, 0, len(row.Legs))
		for j, raw := range row.Legs {
			p, err := parsePairString(raw)
			if err != nil {
				return nil, fmt.Errorf("aggregate.triangulations[%d].legs[%d] %q: %w", i, j, raw, err)
			}
			legs = append(legs, p)
		}
		out = append(out, ResolvedTriangulationChain{Target: target, Legs: legs})
	}
	return out, nil
}

func (a APIConfig) validate() error {
	if a.ListenAddr == "" {
		return fmt.Errorf("%w: api.listen_addr required", ErrInvalidConfig)
	}
	if _, _, err := net.SplitHostPort(a.ListenAddr); err != nil {
		return fmt.Errorf("%w: api.listen_addr %q must be host:port: %w",
			ErrInvalidConfig, a.ListenAddr, err)
	}
	switch a.AuthMode {
	case "none", "apikey", "sep10":
		// ok
	default:
		return fmt.Errorf("%w: api.auth_mode %q must be none/apikey/sep10",
			ErrInvalidConfig, a.AuthMode)
	}
	if a.AnonRateLimitPerMin < 0 {
		return fmt.Errorf("%w: api.anon_rate_limit_per_min must be >= 0",
			ErrInvalidConfig)
	}
	if a.KeyRateLimitPerMin < 0 {
		return fmt.Errorf("%w: api.key_rate_limit_per_min must be >= 0",
			ErrInvalidConfig)
	}
	return nil
}

func (o ObsConfig) validate() error {
	switch o.LogLevel {
	case "debug", "info", "warn", "warning", "error":
		// ok
	default:
		return fmt.Errorf("%w: obs.log_level %q must be debug/info/warn/error",
			ErrInvalidConfig, o.LogLevel)
	}
	switch o.LogFormat {
	case "json", "text", "console":
		// ok
	default:
		return fmt.Errorf("%w: obs.log_format %q must be json/text/console",
			ErrInvalidConfig, o.LogFormat)
	}
	switch o.TraceExporter {
	case "none", "otlp":
		// ok
	default:
		return fmt.Errorf("%w: obs.trace_exporter %q must be none/otlp",
			ErrInvalidConfig, o.TraceExporter)
	}
	if o.TraceSample < 0 || o.TraceSample > 1 {
		return fmt.Errorf("%w: obs.trace_sample %v must be in [0, 1]",
			ErrInvalidConfig, o.TraceSample)
	}
	if o.MetricsListen != "" {
		if _, _, err := net.SplitHostPort(o.MetricsListen); err != nil {
			return fmt.Errorf("%w: obs.metrics_listen %q must be host:port: %w",
				ErrInvalidConfig, o.MetricsListen, err)
		}
	}
	return nil
}

var (
	// regionIDPattern — lowercase alphanumeric, 1-16 chars. Keeps
	// the identifier short + filesystem-safe.
	regionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

	// contractIDPattern matches the Stellar Soroban C-strkey format —
	// identical to canonical.IsContractID, duplicated here so config
	// doesn't depend on canonical (cycle avoidance).
	contractIDPattern = regexp.MustCompile(`^C[A-Z2-7]{55}$`)

	// s3BucketPattern — AWS S3 DNS-compatible bucket naming rules:
	// lowercase, 3–63 chars, alnum + hyphen, must start/end alnum.
	// MinIO is more permissive but we pick the strictest rule so
	// configs stay portable across providers.
	s3BucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
)
