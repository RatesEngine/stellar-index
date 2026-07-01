package ledgerstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stellar/go-stellar-sdk/support/datastore"
)

// TieredDataStore wraps a hot + cold [datastore.DataStore] in a
// fallback chain. Reads try the hot store first; on a not-found
// error (and only that — not transient errors) they fall through
// to the cold store. Writes always target the hot store; the cold
// store is treated as read-only.
//
// Per ADR-0027 the production hot tier is local galexie-archive
// (MinIO on r1) and the cold tier is `aws-public-blockchain` S3
// (the Open Data Sponsorship bucket — the same source R2 reads
// per ADR-0016). The cold path is read-only; PutFile +
// PutFileIfNotExists always target hot.
//
// Fail-loud-not-silent: transient errors from the hot store
// propagate immediately. A misconfigured hot endpoint surfaces
// as the operator's actual problem rather than being masked by
// a slow cold fallback that succeeds for every read.
//
// Metrics (registered when Registry is non-nil):
//
//   - stellarindex_ledgerstream_tier_read_total
//     {outcome="hot"|"cold"|"both_missing"}
//   - stellarindex_ledgerstream_cold_read_duration_seconds
//     {outcome="ok"|"miss"|"error"}
//
// Operators chart `cold` rate as a proxy for "is the trim window
// correctly sized, or am I paying cross-Atlantic latency for
// ranges that should be hot?". A `cold` rate spike on live ingest
// = trim window too tight; cold rate on backfill is expected.
type TieredDataStore struct {
	hot  datastore.DataStore
	cold datastore.DataStore

	readTotal       *prometheus.CounterVec
	coldDurationSec *prometheus.HistogramVec
}

// NewTieredDataStore builds a TieredDataStore wrapping hot + cold.
// registry is optional — pass nil for no metrics; pass the same
// registry used by [Stream] in production.
func NewTieredDataStore(hot, cold datastore.DataStore, registry prometheus.Registerer) *TieredDataStore {
	t := &TieredDataStore{hot: hot, cold: cold}
	if registry != nil {
		t.readTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "stellarindex",
				Subsystem: "ledgerstream",
				Name:      "tier_read_total",
				Help:      "Tiered datastore reads partitioned by which tier served the request. hot=local MinIO; cold=AWS public bucket fallback; both_missing=neither tier has the object.",
			},
			[]string{"outcome"},
		)
		t.coldDurationSec = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "stellarindex",
				Subsystem: "ledgerstream",
				Name:      "cold_read_duration_seconds",
				Help:      "Latency of cold-tier (AWS public bucket) reads. Includes hot miss → cold attempt; does not include hot-tier reads.",
				// Wider buckets than the default — cold reads are
				// cross-Atlantic + spread across whole-partition
				// fetches. Range covers 5ms (cache-warm CDN hit) to
				// 30s (transient AWS slowdown).
				Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"outcome"},
		)
		registry.MustRegister(t.readTotal, t.coldDurationSec)
	}
	return t
}

// IsNotFound returns true when err looks like a missing-key error
// from either the filesystem datastore (os.IsNotExist), the S3
// datastore (AWS `NoSuchKey`, surfaced via the SDK as a wrapped
// error containing the string), or the SDK's manifest-empty
// errors ([datastore.ErrNoLedgerFiles] etc.). Best-effort;
// transient errors (network timeouts, auth failures, throttling)
// DO NOT match and so propagate up rather than falsely triggering
// a cold fallback.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if errors.Is(err, datastore.ErrNoLedgerFiles) || errors.Is(err, datastore.ErrNoValidLedgerFiles) {
		return true
	}
	// Best-effort string match. The AWS SDK wraps NoSuchKey errors
	// as `smithy.GenericAPIError` with code="NoSuchKey"; checking
	// that requires importing `github.com/aws/smithy-go` directly
	// which we'd rather avoid as a transitive-version-pin risk.
	// The SDK's S3DataStore.GetFile is documented to surface
	// "NoSuchKey" in the wrapped error string, and the MinIO
	// client's err format includes the same code. Refine if/when
	// the SDK exports a typed IsNotFound helper.
	s := err.Error()
	return strings.Contains(s, "NoSuchKey") ||
		strings.Contains(s, "key not found") ||
		strings.Contains(s, "no such file or directory")
}

// GetFile reads from hot; on IsNotFound, reads from cold.
// Transient errors from hot propagate without trying cold.
//
// The int64 is the object size in bytes (go-stellar-sdk v0.6 added it to the
// datastore.DataStore.GetFile contract); we thread the underlying store's
// value through unchanged.
func (t *TieredDataStore) GetFile(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	rc, size, err := t.hot.GetFile(ctx, path)
	if err == nil {
		t.observeHot()
		return rc, size, nil
	}
	if !IsNotFound(err) {
		// Transient hot-side error — fail loud rather than mask.
		return nil, 0, fmt.Errorf("tiered: hot GetFile %q: %w", path, err)
	}
	return t.coldGetFile(ctx, path)
}

func (t *TieredDataStore) coldGetFile(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	start := time.Now()
	rc, size, cerr := t.cold.GetFile(ctx, path)
	elapsed := time.Since(start).Seconds()
	switch {
	case cerr == nil:
		t.observeCold("ok", elapsed)
		t.bumpTotal("cold")
		return rc, size, nil
	case IsNotFound(cerr):
		t.observeCold("miss", elapsed)
		t.bumpTotal("both_missing")
		return nil, 0, cerr
	default:
		t.observeCold("error", elapsed)
		return nil, 0, fmt.Errorf("tiered: cold GetFile %q: %w", path, cerr)
	}
}

// GetFileMetadata: hot first, cold on not-found.
func (t *TieredDataStore) GetFileMetadata(ctx context.Context, path string) (map[string]string, error) {
	md, err := t.hot.GetFileMetadata(ctx, path)
	if err == nil {
		return md, nil
	}
	if !IsNotFound(err) {
		return nil, err
	}
	return t.cold.GetFileMetadata(ctx, path)
}

// GetFileLastModified: hot first, cold on not-found.
func (t *TieredDataStore) GetFileLastModified(ctx context.Context, filePath string) (time.Time, error) {
	tm, err := t.hot.GetFileLastModified(ctx, filePath)
	if err == nil {
		return tm, nil
	}
	if !IsNotFound(err) {
		return time.Time{}, err
	}
	return t.cold.GetFileLastModified(ctx, filePath)
}

// Exists returns true if either tier has the object. Hot is
// preferred (sub-ms intra-host); cold is consulted only when hot
// returns false.
func (t *TieredDataStore) Exists(ctx context.Context, path string) (bool, error) {
	ok, err := t.hot.Exists(ctx, path)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	return t.cold.Exists(ctx, path)
}

// Size: hot first, cold on not-found.
func (t *TieredDataStore) Size(ctx context.Context, path string) (int64, error) {
	n, err := t.hot.Size(ctx, path)
	if err == nil {
		return n, nil
	}
	if !IsNotFound(err) {
		return 0, err
	}
	return t.cold.Size(ctx, path)
}

// ListFilePaths returns the union of hot + cold listings. Cold
// paths that are also in hot are deduplicated — hot wins (the
// fresh path). This is the right shape for backfills that span
// the hot/cold boundary: a single call returns every available
// partition.
//
// Order is hot-first then cold-only; callers that need a sorted
// view sort downstream.
func (t *TieredDataStore) ListFilePaths(ctx context.Context, options datastore.ListFileOptions) ([]string, error) {
	hot, err := t.hot.ListFilePaths(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("tiered: hot ListFilePaths: %w", err)
	}
	cold, err := t.cold.ListFilePaths(ctx, options)
	if err != nil {
		// Cold-side list failures are non-fatal for ranges already
		// in hot; return the hot list and a context-wrapped error
		// so callers can decide whether to retry.
		return hot, fmt.Errorf("tiered: cold ListFilePaths (returning hot-only list): %w", err)
	}
	seen := make(map[string]struct{}, len(hot))
	out := make([]string, 0, len(hot)+len(cold))
	for _, p := range hot {
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range cold {
		if _, dup := seen[p]; dup {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// PutFile always targets the hot tier. The cold tier (AWS public
// bucket) is treated as read-only.
func (t *TieredDataStore) PutFile(ctx context.Context, path string, in io.WriterTo, metaData map[string]string) error {
	return t.hot.PutFile(ctx, path, in, metaData)
}

// PutFileIfNotExists always targets the hot tier.
func (t *TieredDataStore) PutFileIfNotExists(ctx context.Context, path string, in io.WriterTo, metaData map[string]string) (bool, error) {
	return t.hot.PutFileIfNotExists(ctx, path, in, metaData)
}

// Close closes both tiers. A hot-side Close error masks any
// cold-side Close error; callers that need both should call them
// individually before wrapping in a TieredDataStore.
func (t *TieredDataStore) Close() error {
	hotErr := t.hot.Close()
	coldErr := t.cold.Close()
	if hotErr != nil {
		return hotErr
	}
	return coldErr
}

func (t *TieredDataStore) observeHot() {
	if t.readTotal != nil {
		t.readTotal.WithLabelValues("hot").Inc()
	}
}

func (t *TieredDataStore) bumpTotal(outcome string) {
	if t.readTotal != nil {
		t.readTotal.WithLabelValues(outcome).Inc()
	}
}

func (t *TieredDataStore) observeCold(outcome string, seconds float64) {
	if t.coldDurationSec != nil {
		t.coldDurationSec.WithLabelValues(outcome).Observe(seconds)
	}
}

// Compile-time assertion that TieredDataStore satisfies the SDK
// interface. If the SDK adds a method, this build break is the
// signal to extend the wrapper.
var _ datastore.DataStore = (*TieredDataStore)(nil)
