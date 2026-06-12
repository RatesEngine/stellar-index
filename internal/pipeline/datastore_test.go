package pipeline

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
)

// TestLedgerstreamConfig_NoColdTier verifies the legacy single-
// source path is unchanged when the operator hasn't opted in to
// the cold tier (ADR-0027). ColdDataStore must be zero-valued so
// ledgerstream.Stream takes the SDK's ApplyLedgerMetadata path.
func TestLedgerstreamConfig_NoColdTier(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Stellar: config.StellarConfig{Network: "pubnet"},
		Storage: config.StorageConfig{
			S3Endpoint:      "http://127.0.0.1:9000",
			S3Region:        "r1",
			S3BucketArchive: "galexie-archive",
			S3BucketLive:    "galexie-live",
			// S3Cold* fields intentionally left zero.
		},
	}
	got := LedgerstreamConfig(cfg, cfg.Storage.S3BucketArchive)
	if got.ColdDataStore.Type != "" {
		t.Errorf("ColdDataStore must be zero when cold tiering disabled; got Type=%q", got.ColdDataStore.Type)
	}
	if got.DataStore.Type != "S3" {
		t.Errorf("hot DataStore.Type = %q, want S3", got.DataStore.Type)
	}
	if got.DataStore.Params["destination_bucket_path"] != "galexie-archive" {
		t.Errorf("hot bucket = %q, want galexie-archive", got.DataStore.Params["destination_bucket_path"])
	}
}

// TestLedgerstreamConfig_ColdTierArchive verifies the cold-tier
// branch fires for the archive bucket when the operator has
// populated the S3Cold* fields.
func TestLedgerstreamConfig_ColdTierArchive(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Stellar: config.StellarConfig{Network: "pubnet"},
		Storage: config.StorageConfig{
			S3Endpoint:          "http://127.0.0.1:9000",
			S3Region:            "r1",
			S3BucketArchive:     "galexie-archive",
			S3BucketLive:        "galexie-live",
			S3ColdEndpoint:      "https://s3.amazonaws.com",
			S3ColdRegion:        "us-east-1",
			S3ColdBucketArchive: "aws-public-blockchain/v1.1/stellar/ledgers/pubnet",
		},
	}
	got := LedgerstreamConfig(cfg, cfg.Storage.S3BucketArchive)
	if got.ColdDataStore.Type != "S3" {
		t.Fatalf("ColdDataStore.Type = %q, want S3 (cold tier should have wired)", got.ColdDataStore.Type)
	}
	if got.ColdDataStore.Params["destination_bucket_path"] != "aws-public-blockchain/v1.1/stellar/ledgers/pubnet" {
		t.Errorf("cold bucket = %q", got.ColdDataStore.Params["destination_bucket_path"])
	}
	if got.ColdDataStore.Params["endpoint_url"] != "https://s3.amazonaws.com" {
		t.Errorf("cold endpoint = %q", got.ColdDataStore.Params["endpoint_url"])
	}
}

// TestLedgerstreamConfig_ColdTierSkippedForLiveBucket verifies
// the cold tier does NOT attach when the caller is reading the
// live bucket — galexie-live is the rolling near-tip working set
// authored locally by galexie, and a cold fallback would point
// at a different source of truth.
func TestLedgerstreamConfig_ColdTierSkippedForLiveBucket(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Stellar: config.StellarConfig{Network: "pubnet"},
		Storage: config.StorageConfig{
			S3Endpoint:          "http://127.0.0.1:9000",
			S3Region:            "r1",
			S3BucketArchive:     "galexie-archive",
			S3BucketLive:        "galexie-live",
			S3ColdEndpoint:      "https://s3.amazonaws.com",
			S3ColdRegion:        "us-east-1",
			S3ColdBucketArchive: "aws-public-blockchain/v1.1/stellar/ledgers/pubnet",
		},
	}
	got := LedgerstreamConfig(cfg, cfg.Storage.S3BucketLive)
	if got.ColdDataStore.Type != "" {
		t.Errorf("ColdDataStore must be zero for the live bucket; got Type=%q", got.ColdDataStore.Type)
	}
}

func TestStorageConfig_ColdTieringEnabled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    config.StorageConfig
		want bool
	}{
		{"all empty", config.StorageConfig{}, false},
		{"endpoint only", config.StorageConfig{S3ColdEndpoint: "https://s3.amazonaws.com"}, false},
		{"bucket only", config.StorageConfig{S3ColdBucketArchive: "aws-public-blockchain/v1.1/stellar/ledgers/pubnet"}, true},
		{"all set", config.StorageConfig{S3ColdEndpoint: "x", S3ColdRegion: "y", S3ColdBucketArchive: "z"}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.s.ColdTieringEnabled(); got != c.want {
				t.Errorf("ColdTieringEnabled() = %v, want %v", got, c.want)
			}
		})
	}
}
