package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cfg "github.com/RatesEngine/rates-engine/internal/config"
)

func TestLoadReader_happyPath(t *testing.T) {
	tomlBody := `
[region]
id = "r2"
name = "Ashburn"

[stellar]
network = "pubnet"

[storage]
postgres_dsn = "postgres://u:p@h/db"
`
	c, err := cfg.LoadReader(strings.NewReader(tomlBody), "test.toml")
	if err != nil {
		t.Fatalf("LoadReader: %v", err)
	}
	if c.Region.ID != "r2" {
		t.Errorf("region.id = %q, want r2", c.Region.ID)
	}
	if c.Region.Name != "Ashburn" {
		t.Errorf("region.name = %q", c.Region.Name)
	}
	// Default home_domain survives when the file omits it.
	if c.Region.HomeDomain != "ratesengine.net" {
		t.Errorf("default home_domain not applied, got %q", c.Region.HomeDomain)
	}
	if c.Storage.PostgresDSN != "postgres://u:p@h/db" {
		t.Errorf("postgres_dsn = %q", c.Storage.PostgresDSN)
	}
	// Default ingestion.enabled_sources should persist through file parse.
	if len(c.Ingestion.EnabledSources) == 0 {
		t.Error("default enabled_sources not preserved")
	}
}

func TestLoadReader_AggregateFlags(t *testing.T) {
	// Verify the new aggregator flags round-trip through TOML.
	body := `
[region]
id = "r1"

[storage]
postgres_dsn = "postgres://u:p@h/db"

[aggregate]
disable_class_filter         = true
enable_stablecoin_fiat_proxy = true
interval_seconds             = 15
max_trades_per_window        = 500
`
	c, err := cfg.LoadReader(strings.NewReader(body), "test.toml")
	if err != nil {
		t.Fatalf("LoadReader: %v", err)
	}
	if !c.Aggregate.DisableClassFilter {
		t.Error("disable_class_filter not parsed")
	}
	if !c.Aggregate.EnableStablecoinFiatProxy {
		t.Error("enable_stablecoin_fiat_proxy not parsed")
	}
	if c.Aggregate.IntervalSeconds != 15 {
		t.Errorf("interval_seconds = %d want 15", c.Aggregate.IntervalSeconds)
	}
	if c.Aggregate.MaxTradesPerWindow != 500 {
		t.Errorf("max_trades_per_window = %d want 500", c.Aggregate.MaxTradesPerWindow)
	}
}

func TestLoadReader_rejectsUnknownKeys(t *testing.T) {
	// Silent typos in config are a classic deployment bug. Unknown
	// keys must be a hard error.
	body := `
[region]
id = "r1"
nonsense_field = "oops"
`
	_, err := cfg.LoadReader(strings.NewReader(body), "test.toml")
	if err == nil {
		t.Fatal("expected unknown-key error, got nil")
	}
	if !strings.Contains(err.Error(), "nonsense_field") {
		t.Errorf("error should name the offending key: %v", err)
	}
}

func TestLoad_readsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	body := `
[region]
id = "r3"
name = "Singapore"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Region.ID != "r3" {
		t.Errorf("got %q", c.Region.ID)
	}
}

func TestLoad_ExampleConfigValid(t *testing.T) {
	// The checked-in configs/example.toml is the reference operators
	// copy for fresh deployments — it MUST load + validate cleanly.
	// Resolve relative to the test file: ../../configs/example.toml.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(wd, "..", "..", "configs", "example.toml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("example.toml not at %s: %v", path, err)
	}
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	// Smoke-check: region + listen came from the file, not defaults.
	if c.Region.ID == "" {
		t.Error("region.id didn't populate from file")
	}
	if c.API.ListenAddr == "" {
		t.Error("api.listen_addr didn't populate from file")
	}
}

func TestLoad_missingFileErrorsNice(t *testing.T) {
	_, err := cfg.Load("/absolutely/not/a/real/path.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not/a/real") {
		t.Errorf("error should include the path: %v", err)
	}
}

func TestApplyEnvOverrides_CoversEveryEnvTag(t *testing.T) {
	// Drift check: every field in the config schema that declares an
	// `env:"…"` tag MUST be honoured by ApplyEnvOverrides. Without
	// this test a new secret-referencing field could ship with the
	// env override silently ignored.
	fields := cfg.Describe()
	var envVars []string
	for _, f := range fields {
		if f.Env != "" {
			envVars = append(envVars, f.Env)
		}
	}
	if len(envVars) == 0 {
		t.Fatal("schema produced zero env-tagged fields — Describe() regression?")
	}

	// Use a sentinel that can't arise from defaults so we can tell
	// whether the override landed.
	const sentinel = "_test_env_override_sentinel_"
	for _, name := range envVars {
		t.Setenv(name, sentinel+name)
	}

	c := cfg.Default()
	c.ApplyEnvOverrides()

	// Serialise the fields via reflect and check that every env-
	// tagged leaf's value starts with the sentinel.
	for _, f := range envVars {
		val := lookupFieldByEnv(&c, f, fields)
		if val == "" {
			t.Errorf("env override %s: field value is empty — ApplyEnvOverrides didn't wire this field",
				f)
			continue
		}
		if !strings.HasPrefix(val, sentinel) {
			t.Errorf("env override %s: field value %q doesn't start with sentinel — ApplyEnvOverrides ignored this env var",
				f, val)
		}
	}
}

// lookupFieldByEnv walks the config via reflect to find the field
// whose `env:` tag matches envName, then returns its stringified
// value. Supports only string leaves (which is what all env-tagged
// fields are today).
func lookupFieldByEnv(c *cfg.Config, envName string, fields []cfg.SchemaField) string {
	v := reflect.ValueOf(c).Elem()
	for _, f := range fields {
		if f.Env != envName {
			continue
		}
		return reflectStringFromPath(v, f.Path)
	}
	return ""
}

// reflectStringFromPath walks a dotted path like
// "storage.postgres_dsn" down the struct via its toml tags.
func reflectStringFromPath(root reflect.Value, path string) string {
	parts := strings.Split(path, ".")
	cur := root
	for _, p := range parts {
		cur = findFieldByTOMLTag(cur, p)
		if !cur.IsValid() {
			return ""
		}
	}
	if cur.Kind() == reflect.String {
		return cur.String()
	}
	return ""
}

func findFieldByTOMLTag(v reflect.Value, tag string) reflect.Value {
	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		if ft.Tag.Get("toml") == tag {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("RATESENGINE_POSTGRES_DSN", "postgres://from-env/db")
	c := cfg.Default()
	c.ApplyEnvOverrides()
	if c.Storage.PostgresDSN != "postgres://from-env/db" {
		t.Errorf("env override didn't land: %q", c.Storage.PostgresDSN)
	}

	// Unset env var → no change.
	t.Setenv("RATESENGINE_POSTGRES_DSN", "")
	c2 := cfg.Default()
	original := c2.Storage.PostgresDSN
	c2.ApplyEnvOverrides()
	if c2.Storage.PostgresDSN != original {
		t.Errorf("empty env should not override: %q", c2.Storage.PostgresDSN)
	}
}

func TestLoadWithEnv_RevalidatesAfterOverride(t *testing.T) {
	// A file that Validate() accepts, then env override with a
	// malformed DSN. Previously (Load + ApplyEnvOverrides) this got
	// past startup and errored at DB dial time. LoadWithEnv must
	// catch it as ErrInvalidConfig.
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	good := `
[region]
id = "r1"
home_domain = "ratesengine.net"

[stellar]
network = "pubnet"
rpc_endpoints = ["http://rpc:8000"]

[storage]
postgres_dsn = "postgres://valid@host/db"
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}

	// Sanity: file validates.
	if _, err := cfg.LoadWithEnv(path); err != nil {
		t.Fatalf("clean env should load: %v", err)
	}

	// Env override with a malformed DSN.
	t.Setenv("RATESENGINE_POSTGRES_DSN", "mysql://not-a-postgres-url")
	_, err := cfg.LoadWithEnv(path)
	if err == nil {
		t.Fatal("bad env-var DSN must be rejected by LoadWithEnv")
	}
	if !strings.Contains(err.Error(), "postgres_dsn") {
		t.Errorf("err should name the offending field: %v", err)
	}
}
