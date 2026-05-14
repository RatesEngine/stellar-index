package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads a TOML config file from path and returns a fully-
// populated [Config] with defaults applied for any field the file
// omits. File precedence beats the built-in defaults; env-var
// overrides (see [ApplyEnvOverrides]) beat the file.
//
// Returns a wrapped error identifying the path + offending line on
// parse failure.
func Load(path string) (Config, error) {
	// G304 false positive: operator-supplied config path is the
	// whole point of the flag. No user-controlled input reaches
	// here — the indexer's -config flag is parsed from argv.
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return Config{}, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return LoadReader(f, path)
}

// LoadReader is [Load] with a supplied io.Reader. Useful for tests
// that don't want to touch the filesystem.
func LoadReader(r io.Reader, origin string) (Config, error) {
	c := Default()
	meta, err := toml.NewDecoder(r).Decode(&c)
	if err != nil {
		return Config{}, fmt.Errorf("config: decode %s: %w", origin, err)
	}
	if undec := meta.Undecoded(); len(undec) > 0 {
		// Unknown keys are a hard error — silent typos in config
		// are one of the most common deployment bugs.
		keys := make([]string, 0, len(undec))
		for _, k := range undec {
			keys = append(keys, k.String())
		}
		return Config{}, fmt.Errorf("config: unknown keys in %s: %s",
			origin, strings.Join(keys, ", "))
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", origin, err)
	}
	return c, nil
}

// ApplyEnvOverrides mutates c in place, replacing any field that has
// an `env:` tag with the env-var's value if that var is set.
//
// Secret fields follow the `env: "NAME"` convention where NAME is
// the var holding the actual secret — see
// [StorageConfig.PostgresDSN] for the canonical example.
//
// Unknown / empty env vars are ignored; no field is overwritten with
// an empty string.
//
// Does NOT re-validate. Callers that want env-driven values held to
// the same invariants as file-driven values should use [LoadWithEnv]
// or call [Config.Validate] after this.
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("RATESENGINE_POSTGRES_DSN"); v != "" {
		c.Storage.PostgresDSN = v
	}
	if v := os.Getenv("RATESENGINE_REDIS_PASSWORD"); v != "" {
		c.Storage.RedisPassword = v
	}
	if v := os.Getenv("RATESENGINE_S3_ACCESS_KEY"); v != "" {
		c.Storage.S3AccessKeyEnv = v
	}
	if v := os.Getenv("RATESENGINE_S3_SECRET_KEY"); v != "" {
		c.Storage.S3SecretKeyEnv = v
	}
	if v := os.Getenv("EXCHANGERATESAPI_KEY"); v != "" {
		c.External.ExchangeRatesApi.APIKey = v
	}
	if v := os.Getenv("POLYGON_API_KEY"); v != "" {
		c.External.PolygonForex.APIKey = v
	}
	if v := os.Getenv("COINMARKETCAP_API_KEY"); v != "" {
		c.External.CoinMarketCap.APIKey = v
	}
	if v := os.Getenv("CRYPTOCOMPARE_API_KEY"); v != "" {
		c.External.CryptoCompare.APIKey = v
	}
	if v := os.Getenv("CHAINLINK_RPC_URL"); v != "" {
		c.External.Chainlink.RPCUrl = v
	}
	if v := os.Getenv("RATESENGINE_STRIPE_WEBHOOK_SECRET"); v != "" {
		c.API.Stripe.SigningSecret = v
	}
}

// LoadWithEnv is [Load] + [ApplyEnvOverrides] + a second [Validate].
// Use this in binaries so a bad env-var value (e.g., malformed
// RATESENGINE_POSTGRES_DSN overriding a known-good DSN from the file)
// fails fast with the same ErrInvalidConfig error as a bad file,
// instead of opening the pool and getting a confusing DB error
// at connect time.
func LoadWithEnv(path string) (Config, error) {
	c, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	c.ApplyEnvOverrides()
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s (with env overrides): %w", path, err)
	}
	return c, nil
}
