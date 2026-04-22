package config_test

import (
	"os"
	"path/filepath"
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

func TestLoad_missingFileErrorsNice(t *testing.T) {
	_, err := cfg.Load("/absolutely/not/a/real/path.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not/a/real") {
		t.Errorf("error should include the path: %v", err)
	}
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
