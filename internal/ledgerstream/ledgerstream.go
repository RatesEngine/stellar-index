// Package ledgerstream reads Galexie-exported ledger-meta from an
// S3-compatible datastore (MinIO in production, Filesystem in
// tests) and yields one xdr.LedgerCloseMeta per ledger to a caller-
// supplied callback.
//
// This package is the **only** production path into the ingest
// pipeline. Per docs/architecture/ingest-pipeline.md, every source
// decoder receives its events via this package's output, never
// via stellar-rpc. The scripts/ci/lint-imports.sh rule
// A/no-rpc-in-ingest blocks stellarrpc imports from the ingest
// codepath as a structural guardrail.
//
// Design: this is a **thin wrapper** around the SDK's
// ingest.ApplyLedgerMetadata. The SDK already implements the
// buffered, parallel-fetch, retry-on-error reader; we don't
// reimplement it. This package exists to:
//
//  1. Give us a stable seam for testing (inject a Filesystem
//     datastore in tests, MinIO in integration, S3 in prod).
//  2. Centralize logger + Prometheus registry wiring.
//  3. Provide a single place for any future customization
//     (bounded-vs-unbounded, cursor persistence, etc.).
//
// If the wrapper turns out to be pure delegation, that's still
// the correct value — one import boundary, one test seam.
package ledgerstream

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stellar/go-stellar-sdk/ingest"
	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	sdklog "github.com/stellar/go-stellar-sdk/support/log"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Config binds the SDK's datastore configuration + BufferedStorageBackend
// tuning + optional observability into one unit. Typical production
// values come from our config.Stellar section; unit tests use the
// filesystem datastore pointed at a tempdir.
//
// The zero-value DataStore is invalid — [Stream] returns an error
// rather than silently skipping.
type Config struct {
	// DataStore — required. Describes the Galexie output bucket
	// (S3/MinIO/GCS) or a filesystem directory for tests.
	DataStore datastore.DataStoreConfig

	// Buffered — optional. If nil, Stream derives sensible defaults
	// from DataStore.Schema.LedgersPerFile via
	// ingest.DefaultBufferedStorageBackendConfig. Override only when
	// profiling has shown the defaults are wrong for your workload.
	Buffered *ledgerbackend.BufferedStorageBackendConfig

	// Logger — optional. nil uses the SDK's package logger at info
	// level. Pass a configured logger to route the SDK's output
	// through our slog setup.
	Logger *sdklog.Entry

	// Registry — optional. When non-nil, the backend registers
	// Prometheus metrics (buffer_fetch_latency_seconds, etc.) under
	// RegistryNamespace. Use our main obs registry in production.
	Registry          *prometheus.Registry
	RegistryNamespace string
}

// Stream reads ledgers in [from, to] from the datastore and invokes
// callback once per xdr.LedgerCloseMeta.
//
//   - to == 0 → unbounded live tail. Stream returns only on ctx
//     cancellation, a datastore error, or a callback error.
//   - to >= from → bounded range. Stream returns nil on successful
//     completion of the range.
//
// from is clamped upward to the Stellar genesis ledger (2), per
// the SDK's ApplyLedgerMetadata contract. Callers passing 0 or 1
// get data from ledger 2 onward — that's an SDK behavior, not ours.
//
// The callback blocks Stream's goroutine; expensive work inside
// callback directly affects ingest throughput. For multi-consumer
// fanout, have callback send onto a channel and let consumers read
// off it.
//
// Blocking: yes. Call Stream in its own goroutine if the caller
// needs concurrent work.
func Stream(
	ctx context.Context,
	cfg Config,
	from, to uint32,
	callback func(xdr.LedgerCloseMeta) error,
) error {
	if callback == nil {
		return fmt.Errorf("ledgerstream: callback is nil")
	}
	if cfg.DataStore.Type == "" {
		return fmt.Errorf("ledgerstream: DataStore.Type is empty — config missing")
	}

	var buffered ledgerbackend.BufferedStorageBackendConfig
	if cfg.Buffered != nil {
		buffered = *cfg.Buffered
	} else {
		lpf := cfg.DataStore.Schema.LedgersPerFile
		if lpf == 0 {
			// Galexie's default at the time of writing is 1 ledger per
			// file; the SDK's schema discovery will override this if
			// the datastore's manifest says otherwise, but we still
			// need a value to seed the default config.
			lpf = 1
		}
		buffered = ingest.DefaultBufferedStorageBackendConfig(lpf)
	}

	var ledgerRange ledgerbackend.Range
	if to == 0 {
		ledgerRange = ledgerbackend.UnboundedRange(from)
	} else {
		ledgerRange = ledgerbackend.BoundedRange(from, to)
	}

	return ingest.ApplyLedgerMetadata(
		ledgerRange,
		ingest.PublisherConfig{
			Registry:              cfg.Registry,
			RegistryNamespace:     cfg.RegistryNamespace,
			BufferedStorageConfig: buffered,
			DataStoreConfig:       cfg.DataStore,
			Log:                   cfg.Logger,
		},
		ctx,
		callback,
	)
}
