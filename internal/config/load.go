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
	f, err := os.Open(path)
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
}
