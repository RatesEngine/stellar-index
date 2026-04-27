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
func LedgerstreamConfig(cfg config.Config, bucket string) ledgerstream.Config {
	return ledgerstream.Config{
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
}
