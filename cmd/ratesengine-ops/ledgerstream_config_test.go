// Copyright 2026 Rates Engine contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
)

// TestNewBoundedLedgerStreamConfig_TolerateTrailingMissingDefault is the
// critical regression: the helper exists specifically so ops
// subcommands cannot forget to opt into TolerateTrailingMissing. If
// this assertion breaks, the trailing-edge missing-file failure
// (project_62_diagnosis_2026_05_25) is back in scope for
// verify-decoders and scan-soroban-events.
func TestNewBoundedLedgerStreamConfig_TolerateTrailingMissingDefault(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Stellar.Network = "pubnet"
	cfg.Storage.S3Endpoint = "http://127.0.0.1:9000"
	cfg.Storage.S3Region = "r1"
	cfg.Storage.S3BucketArchive = "galexie-archive"
	cfg.Storage.S3BucketLive = "galexie-live"

	got := newBoundedLedgerStreamConfig(cfg, cfg.Storage.S3BucketArchive)
	if !got.TolerateTrailingMissing {
		t.Fatalf("TolerateTrailingMissing = false, want true")
	}
}

// TestNewBoundedLedgerStreamConfig_BucketPassThrough confirms the
// helper threads the caller's bucket into destination_bucket_path
// unmodified — verify-archive uses bucket-by-parameter while
// verify-decoders uses cfg.Storage.S3BucketLive, so the helper
// cannot pick the bucket itself.
func TestNewBoundedLedgerStreamConfig_BucketPassThrough(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Stellar.Network = "pubnet"
	cfg.Storage.S3Endpoint = "http://127.0.0.1:9000"
	cfg.Storage.S3Region = "r1"
	cfg.Storage.S3BucketArchive = "galexie-archive"
	cfg.Storage.S3BucketLive = "galexie-live"

	for _, want := range []string{"galexie-archive", "galexie-live", "custom-override"} {
		got := newBoundedLedgerStreamConfig(cfg, want)
		if got.DataStore.Params["destination_bucket_path"] != want {
			t.Errorf("bucket=%q -> destination_bucket_path=%q, want %q",
				want, got.DataStore.Params["destination_bucket_path"], want)
		}
		if got.DataStore.Params["region"] != cfg.Storage.S3Region {
			t.Errorf("region = %q, want %q",
				got.DataStore.Params["region"], cfg.Storage.S3Region)
		}
		if got.DataStore.Params["endpoint_url"] != cfg.Storage.S3Endpoint {
			t.Errorf("endpoint_url = %q, want %q",
				got.DataStore.Params["endpoint_url"], cfg.Storage.S3Endpoint)
		}
		if got.DataStore.Compression != "zstd" {
			t.Errorf("Compression = %q, want zstd", got.DataStore.Compression)
		}
		if got.DataStore.NetworkPassphrase != cfg.Stellar.Passphrase() {
			t.Errorf("NetworkPassphrase = %q, want %q",
				got.DataStore.NetworkPassphrase, cfg.Stellar.Passphrase())
		}
	}
}
