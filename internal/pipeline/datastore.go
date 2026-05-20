package pipeline

import (
	"github.com/stellar/go-stellar-sdk/support/datastore"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
)

// LedgerstreamConfig builds a ledgerstream.Config pointing at one
// galexie bucket. Pass cfg.Storage.S3BucketArchive for historical
// reads (ledger < seam) or S3BucketLive for the live tail.
//
// Only S3/MinIO is wired today; Filesystem is reserved for tests,
// GCS for a hypothetical cloud deploy.
//
// When cfg.Storage.ColdTieringEnabled() (ADR-0027 — the cold-tier
// fields populated in TOML), the returned Config also carries a
// ColdDataStore pointing at the cold-tier bucket. ledgerstream's
// TieredDataStore then transparently falls back to cold on
// hot-side NoSuchKey. Only the **archive** bucket gets the
// tiering treatment — galexie-live is the rolling near-tip
// working set and never needs a cold fallback. Caller passes the
// archive bucket as `bucket` to opt the cold path in; passing
// the live bucket leaves ColdDataStore zero (single-source).
func LedgerstreamConfig(cfg config.Config, bucket string) ledgerstream.Config {
	out := ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": bucket,
				"region":                  cfg.Storage.S3Region,
				"endpoint_url":            cfg.Storage.S3Endpoint,
			},
			NetworkPassphrase: cfg.Stellar.Passphrase(),
			Compression:       "zstd",
		},
	}

	// Tiered-read opt-in: only attach a ColdDataStore when the
	// operator has populated the cold-tier fields AND the caller
	// is reading the archive bucket (not the live tail). The live
	// tail's writer is galexie itself — it's authoritative
	// locally — so a cold fallback would be wrong.
	if cfg.Storage.ColdTieringEnabled() && bucket == cfg.Storage.S3BucketArchive {
		out.ColdDataStore = datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": cfg.Storage.S3ColdBucketArchive,
				"region":                  cfg.Storage.S3ColdRegion,
				"endpoint_url":            cfg.Storage.S3ColdEndpoint,
			},
			NetworkPassphrase: cfg.Stellar.Passphrase(),
			Compression:       "zstd",
		}
	}

	return out
}
