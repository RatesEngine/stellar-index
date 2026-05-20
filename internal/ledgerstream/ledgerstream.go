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
	// (S3/MinIO/GCS) or a filesystem directory for tests. In a
	// tiered deployment this is the **hot** tier (local
	// galexie-archive on r1).
	DataStore datastore.DataStoreConfig

	// ColdDataStore — optional. When non-zero (Type set), Stream
	// constructs a [TieredDataStore] wrapping DataStore (hot) +
	// ColdDataStore (cold) per ADR-0027, so reads of LCMs absent
	// from the local mirror transparently fall back to a cold
	// upstream (typically `aws-public-blockchain` S3 — the AWS
	// Open Data Sponsorship bucket). Writes always target hot.
	// The zero-value disables tiering; the legacy single-source
	// path through ingest.ApplyLedgerMetadata is used instead —
	// behaviour exactly matches pre-#7-step-1.
	ColdDataStore datastore.DataStoreConfig

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
	// When ColdDataStore is also set, the [TieredDataStore]'s
	// tier_read_total + cold_read_duration_seconds metrics
	// register under the same registry.
	Registry          *prometheus.Registry
	RegistryNamespace string
}

// tieringEnabled reports whether Config requests a tiered
// (hot + cold) read path. The zero-value ColdDataStore disables
// tiering; any non-empty Type opts in.
func (c *Config) tieringEnabled() bool {
	return c.ColdDataStore.Type != ""
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

	if cfg.tieringEnabled() {
		return streamTiered(ctx, cfg, ledgerRange, buffered, callback)
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

// streamTiered is the hot+cold branch of [Stream]. It mirrors the
// SDK's ingest.ApplyLedgerMetadata loop (producer.go) but injects
// a [TieredDataStore] as the BufferedStorageBackend's underlying
// store instead of letting the SDK construct one from
// DataStoreConfig. Both hot and cold instances of the SDK's
// concrete DataStore are built from cfg.DataStore + cfg.ColdDataStore
// respectively, then wrapped.
//
// Behavioural parity with ApplyLedgerMetadata: same bounded/unbounded
// validation, same from-clamp (max(2, range.From)), same GetLedger
// loop, same error wrapping.
func streamTiered(
	ctx context.Context,
	cfg Config,
	ledgerRange ledgerbackend.Range,
	buffered ledgerbackend.BufferedStorageBackendConfig,
	callback func(xdr.LedgerCloseMeta) error,
) error {
	hot, err := datastore.NewDataStore(ctx, cfg.DataStore)
	if err != nil {
		return fmt.Errorf("ledgerstream: hot datastore: %w", err)
	}
	cold, err := datastore.NewDataStore(ctx, cfg.ColdDataStore)
	if err != nil {
		// Close hot so the build error doesn't leak its resources;
		// the inner Close error is intentionally swallowed — the
		// caller wants the construction failure surfaced first.
		_ = hot.Close()
		return fmt.Errorf("ledgerstream: cold datastore: %w", err)
	}
	tiered := NewTieredDataStore(hot, cold, cfg.Registry)

	schema, err := datastore.LoadSchema(ctx, tiered, cfg.DataStore)
	if err != nil {
		_ = tiered.Close()
		return fmt.Errorf("ledgerstream: load schema: %w", err)
	}

	var backend ledgerbackend.LedgerBackend
	backend, err = ledgerbackend.NewBufferedStorageBackend(buffered, tiered, schema)
	if err != nil {
		_ = tiered.Close()
		return fmt.Errorf("ledgerstream: new buffered storage backend: %w", err)
	}
	if cfg.Registry != nil {
		backend = ledgerbackend.WithMetrics(backend, cfg.Registry, cfg.RegistryNamespace)
	}
	defer func() { _ = backend.Close() }()

	if ledgerRange.Bounded() && ledgerRange.To() <= ledgerRange.From() {
		return fmt.Errorf("ledgerstream: invalid end value for bounded range, must be greater than start")
	}
	if !ledgerRange.Bounded() && ledgerRange.To() > 0 {
		return fmt.Errorf("ledgerstream: invalid end value for unbounded range, must be zero")
	}

	from := ledgerRange.From()
	if from < 2 {
		from = 2
	}
	if err := backend.PrepareRange(ctx, ledgerRange); err != nil {
		return fmt.Errorf("ledgerstream: prepare range: %w", err)
	}

	for seq := from; seq <= ledgerRange.To() || !ledgerRange.Bounded(); seq++ {
		lcm, err := backend.GetLedger(ctx, seq)
		if err != nil {
			return fmt.Errorf("ledgerstream: get ledger %d: %w", seq, err)
		}
		if err := callback(lcm); err != nil {
			return fmt.Errorf("ledgerstream: callback %d: %w", seq, err)
		}
	}
	return nil
}
