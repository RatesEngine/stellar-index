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
	"regexp"
	"strconv"
	"time"

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

	// LiveRetryWait overrides the BufferedStorageBackend's RetryWait
	// for an **unbounded (live-tail)** stream only. The SDK default
	// is 30s: when a fetch worker requests the next ledger object and
	// it isn't in the datastore yet, the worker sleeps RetryWait
	// before re-checking. Once the indexer has caught up to galexie's
	// tip every next-ledger fetch misses, so a 30s default makes the
	// end-to-end ingest lag sawtooth 0→30s even though galexie
	// uploads the next LCM within ~5s. Setting this to a few seconds
	// lets a caught-up worker re-check promptly. Zero leaves the
	// SDK/derived default untouched. Ignored for bounded ranges (a
	// missing object there is a hard error, not a wait-for-tip).
	LiveRetryWait time.Duration

	// TolerateTrailingMissing — when true, a bounded Stream that
	// fails with the SDK's "ledger object containing sequence X is
	// missing" error is converted to a clean walk-complete (returns
	// nil with a WARN log) provided X is within TrailingMissingWindow
	// of the bounded To. Use for backfills that may race the live
	// tip (Galexie writes partition files lazily) or for archive-
	// integrity walks where a trailing-edge gap is "the tip isn't
	// here yet" rather than corruption. False (default) preserves
	// strict bounded semantics: any missing file is an error,
	// matching pre-2026-05-26 behaviour.
	//
	// Mid-range gaps still error regardless of this flag — the
	// window check guards against masking real corruption. The
	// 2026-05-26 audit walks against the same archive confirmed
	// the chain is intact up to the live tip; the failure mode
	// this targets is exclusively the trailing 1-2 partitions
	// that Galexie hasn't finished uploading yet.
	//
	// Delivery caveat: when the SDK's BufferedStorageBackend hits a
	// missing file it cancels its internal context, dropping any
	// pre-fetched ledgers in the buffer that hadn't been delivered
	// to the callback yet. This is SDK-level behaviour. Result: the
	// last delivered ledger can be up to BufferSize ledgers behind
	// the missing-file's seq. Operators relying on full coverage
	// (e.g. 100%-density backfills) must clamp -to below the live
	// tip in advance; the tolerate flag's role is graceful exit on
	// trailing-edge races (chain-check, defence in depth), not a
	// substitute for tip-aware -to selection.
	TolerateTrailingMissing bool

	// TrailingMissingWindow — how close to the bounded range's To
	// the missing-file's sequence must be to qualify as
	// trailing-edge. Default 65536 (one full Galexie 64k-ledger
	// partition plus slack — covers any "Galexie hasn't written
	// the next partition yet" race plus operator-set To values
	// that overshoot the tip by hours). Mid-range gaps farther
	// from To than the window error regardless of the tolerate
	// flag. Ignored when TolerateTrailingMissing is false.
	TrailingMissingWindow uint32
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

	// Live-tail RetryWait override — see Config.LiveRetryWait. Only
	// an unbounded range (to == 0) waits for the tip; on a bounded
	// range a missing object is a hard error, so the override is
	// meaningless there and deliberately not applied.
	if to == 0 && cfg.LiveRetryWait > 0 {
		buffered.RetryWait = cfg.LiveRetryWait
	}

	var ledgerRange ledgerbackend.Range
	if to == 0 {
		ledgerRange = ledgerbackend.UnboundedRange(from)
	} else {
		ledgerRange = ledgerbackend.BoundedRange(from, to)
	}

	var err error
	switch {
	case cfg.tieringEnabled():
		err = streamTiered(ctx, cfg, ledgerRange, buffered, callback)
	case ledgerRange.Bounded() && ledgerRange.To() == ledgerRange.From():
		// The SDK's ingest.ApplyLedgerMetadata rejects a bounded
		// range of exactly one ledger (producer.go: `To() <=
		// From()`) even though the SDK exports SingleLedgerRange.
		// Walk it with our own backend loop instead — this is
		// ch-live-catchup's tip-extend case whenever the timer
		// fires exactly one ledger behind the galexie tip.
		err = streamHot(ctx, cfg, ledgerRange, buffered, callback)
	default:
		err = ingest.ApplyLedgerMetadata(
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
	return maybeTolerateTrailingMissing(cfg, to, err)
}

// validateRange rejects malformed ranges before PrepareRange. A
// bounded range of exactly one ledger (To == From) is VALID — the
// SDK models it as a first-class concept
// (ledgerbackend.SingleLedgerRange) and the walk loop handles it as
// a single iteration. The previous `To() <= From()` check rejected
// it, which made ch-live-catchup's tip-extend fail every time the
// timer fired exactly one ledger behind the galexie tip (an
// intermittent ~flap whenever the 10-min cadence landed on a
// 1-ledger delta; observed on r1 2026-06-11).
func validateRange(r ledgerbackend.Range) error {
	if r.Bounded() && r.To() < r.From() {
		return fmt.Errorf("ledgerstream: invalid end value for bounded range, must not be less than start")
	}
	if !r.Bounded() && r.To() > 0 {
		return fmt.Errorf("ledgerstream: invalid end value for unbounded range, must be zero")
	}
	return nil
}

// maybeTolerateTrailingMissing converts a bounded-stream missing-
// file error into a clean walk-complete (nil) when the operator
// opted in via Config.TolerateTrailingMissing AND the missing
// sequence is within the trailing window of the bounded To. All
// other error shapes pass through unchanged. Always returns nil
// for nil err.
func maybeTolerateTrailingMissing(cfg Config, to uint32, err error) error {
	if err == nil {
		return nil
	}
	if !cfg.TolerateTrailingMissing || to == 0 {
		return err
	}
	seq, ok := parseTrailingMissingSeq(err)
	if !ok {
		return err
	}
	window := cfg.TrailingMissingWindow
	if window == 0 {
		window = defaultTrailingMissingWindow
	}
	if seq > to || to-seq > window {
		return err
	}
	if cfg.Logger != nil {
		cfg.Logger.WithFields(map[string]interface{}{
			"missing_ledger": seq,
			"range_to":       to,
			"gap_to_tip":     to - seq,
			"window":         window,
		}).Warn("ledgerstream: bounded walk hit trailing-edge missing file — treating as walk-complete (TolerateTrailingMissing=true)")
	}
	return nil
}

// defaultTrailingMissingWindow is one Galexie 64k-ledger partition
// plus 1536 ledgers of slack, covering any tip-race between the
// operator's chosen -to and the partition file Galexie hasn't
// finished writing yet.
const defaultTrailingMissingWindow uint32 = 65536

// trailingMissingRE matches the SDK's
// `ledger object containing sequence X is missing` error wrap. The
// SDK uses `pkg/errors.Wrapf`, which produces a colon-joined chain
// — the sequence appears verbatim in the message regardless of how
// many layers deep the wrap is. Capturing group 1 is the sequence
// as a decimal integer.
var trailingMissingRE = regexp.MustCompile(`ledger object containing sequence (\d+) is missing`)

// parseTrailingMissingSeq extracts the sequence number from the
// SDK's trailing-edge missing-file error. Returns (0, false) when
// the error does not match the SDK's known wrap shape.
//
// We match on the error string because the SDK
// (github.com/stellar/go-stellar-sdk/ingest/ledgerbackend.ledger_buffer)
// wraps with pkg/errors.Wrapf and exposes no typed sentinel.
func parseTrailingMissingSeq(err error) (uint32, bool) {
	if err == nil {
		return 0, false
	}
	m := trailingMissingRE.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return 0, false
	}
	n, perr := strconv.ParseUint(m[1], 10, 32)
	if perr != nil {
		return 0, false
	}
	return uint32(n), true
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
		// Cold tier is OPTIONAL by design (ADR-0027) — it's a
		// fallback for ledger ranges trimmed from local
		// galexie-archive. If cold init fails (wrong region,
		// network issue, anonymous auth rejected by the upstream
		// bucket, etc.) we should NOT abort — local galexie-archive
		// is still authoritative for everything the system was
		// reading pre-tier-enable. Hot-only path via the legacy
		// ApplyLedgerMetadata is byte-equivalent to pre-#7-step-1b
		// behaviour.
		//
		// Fail-loud-but-degrade: log a Warn (operator-visible) and
		// fall back; don't propagate the cold-side error as a
		// blocking failure. The pre-fix behaviour cascaded a
		// cold-misconfig (region mismatch in r1's 2026-05-20 §3
		// enable) into a backfill abort — opposite of the cold
		// tier being optional.
		if cfg.Logger != nil {
			cfg.Logger.WithField("err", err).Warn("ledgerstream: cold datastore init failed; falling back to hot-only single-source path")
		}
		if ledgerRange.Bounded() && ledgerRange.To() == ledgerRange.From() {
			// ApplyLedgerMetadata rejects single-ledger bounded
			// ranges (see the [Stream] dispatch) — reuse the
			// already-open hot store via our own walk.
			return walkDataStore(ctx, cfg, hot, ledgerRange, buffered, callback)
		}
		_ = hot.Close()
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
	tiered := NewTieredDataStore(hot, cold, cfg.Registry)
	return walkDataStore(ctx, cfg, tiered, ledgerRange, buffered, callback)
}

// streamHot is the hot-only counterpart of [streamTiered]: same
// backend construction + walk loop, but over cfg.DataStore alone
// with no tiering wrapper. It exists because the SDK's
// ingest.ApplyLedgerMetadata rejects a bounded range of exactly one
// ledger (`To() <= From()` in producer.go) even though the SDK
// itself exports ledgerbackend.SingleLedgerRange — so [Stream]
// routes single-ledger non-tiered requests here instead.
func streamHot(
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
	return walkDataStore(ctx, cfg, hot, ledgerRange, buffered, callback)
}

// walkDataStore builds the buffered storage backend over `store`
// and runs the GetLedger walk — the shared tail of [streamTiered]
// and [streamHot]. Closes the backend (and thereby the store) on
// return. Behavioural parity with the SDK's
// ingest.ApplyLedgerMetadata loop: same from-clamp (max(2, From)),
// same GetLedger loop, same error wrapping — except single-ledger
// bounded ranges are accepted (see [validateRange]).
func walkDataStore(
	ctx context.Context,
	cfg Config,
	store datastore.DataStore,
	ledgerRange ledgerbackend.Range,
	buffered ledgerbackend.BufferedStorageBackendConfig,
	callback func(xdr.LedgerCloseMeta) error,
) error {
	schema, err := datastore.LoadSchema(ctx, store, cfg.DataStore)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("ledgerstream: load schema: %w", err)
	}

	var backend ledgerbackend.LedgerBackend
	backend, err = ledgerbackend.NewBufferedStorageBackend(buffered, store, schema)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("ledgerstream: new buffered storage backend: %w", err)
	}
	if cfg.Registry != nil {
		backend = ledgerbackend.WithMetrics(backend, cfg.Registry, cfg.RegistryNamespace)
	}
	defer func() { _ = backend.Close() }()

	if err := validateRange(ledgerRange); err != nil {
		return err
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
