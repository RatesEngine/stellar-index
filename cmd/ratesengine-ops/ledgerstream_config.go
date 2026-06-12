// Copyright 2026 Rates Engine contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/stellar/go-stellar-sdk/support/datastore"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
)

// newBoundedLedgerStreamConfig returns the ledgerstream.Config that
// ops subcommands should ALWAYS use when their `-to` may equal the
// live galexie-archive tip. Always opts into TolerateTrailingMissing
// per rc.81 (#62 diagnosis); never override that downstream.
//
// Background: the trailing-edge missing-file failure surfaced in the
// 2026-05-25 verify-archive bootstrap (project_62_diagnosis_2026_05_25)
// was patched site-by-site in verify-archive and wasm-history. The
// other ops subcommands that stream LCM (verify-decoders,
// scan-soroban-events) used to construct ledgerstream.Config inline
// without the flag and could hit the same trap when called with
// `-to 0` (live tip). This helper centralises the construction so the
// flag can't be forgotten.
func newBoundedLedgerStreamConfig(cfg config.Config, bucket string) ledgerstream.Config {
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
		TolerateTrailingMissing: true,
	}
}
