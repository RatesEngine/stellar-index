package ledgerstream_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
)

// TestStream_TolerateTrailingMissing_HappyPath asserts that a
// bounded Stream whose range overshoots the materialised content
// returns nil (walk-complete) when TolerateTrailingMissing is set
// and the missing sequence falls within the trailing window.
func TestStream_TolerateTrailingMissing_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, store := tolerateTrailingEdgeStore(t, tmp)
	t.Cleanup(func() { _ = store.Close() })

	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish config: %v", err)
	}

	// Materialise ledgers 5,6,7 but ask for [5, 9]. Ledgers 8 + 9
	// don't exist — the SDK will surface the missing-file error for
	// 8 first. With TolerateTrailingMissing=true and a window that
	// covers the gap (9 - 8 = 1 ≤ 16), Stream should return nil.
	for seq := uint32(5); seq <= 7; seq++ {
		writeLedgerFixture(t, ctx, store, cfg.Schema, seq)
	}

	got := 0
	lsCfg := ledgerstream.Config{
		DataStore:               cfg,
		TolerateTrailingMissing: true,
		TrailingMissingWindow:   16,
	}
	err := ledgerstream.Stream(ctx, lsCfg, 5, 9, func(_ xdr.LedgerCloseMeta) error {
		got++
		return nil
	})
	if err != nil {
		t.Fatalf("Stream returned err=%v; want nil (trailing-edge tolerated)", err)
	}
	// Note: `got` is intentionally NOT asserted to a specific value.
	// When the SDK's BufferedStorageBackend hits a missing file at
	// the trailing edge it cancels its internal context, dropping
	// any pre-fetched ledgers in the buffer that hadn't been
	// delivered to the callback yet — including ledgers that were
	// fully materialised on disk. This is SDK-level behaviour
	// outside our control: with parallel workers the cancel race
	// can drop 0 or all pre-fetched ledgers. Operators relying on
	// full coverage (100%-density backfills) must clamp -to below
	// the live tip in advance. The tolerate flag's role is
	// exclusively graceful exit on trailing-edge races
	// (chain-check, defence in depth).
	_ = got
}

// TestStream_TolerateTrailingMissing_MidRangeStillErrors asserts
// that a missing file FAR from the bounded To still errors even
// when TolerateTrailingMissing is set. This guards against masking
// real corruption: only the trailing window is treated as a tip-
// race; everything earlier is a real gap.
func TestStream_TolerateTrailingMissing_MidRangeStillErrors(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, store := tolerateTrailingEdgeStore(t, tmp)
	t.Cleanup(func() { _ = store.Close() })

	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish config: %v", err)
	}

	// Materialise 5,6,7. Ask for [5, 200] with window 16. Missing
	// seq 8 has gap 200-8=192 > 16 → mid-range, must error.
	for seq := uint32(5); seq <= 7; seq++ {
		writeLedgerFixture(t, ctx, store, cfg.Schema, seq)
	}

	lsCfg := ledgerstream.Config{
		DataStore:               cfg,
		TolerateTrailingMissing: true,
		TrailingMissingWindow:   16,
	}
	err := ledgerstream.Stream(ctx, lsCfg, 5, 200, func(_ xdr.LedgerCloseMeta) error {
		return nil
	})
	if err == nil {
		t.Fatalf("Stream returned nil; expected mid-range gap error")
	}
	if !strings.Contains(err.Error(), "is missing") {
		t.Errorf("err = %v; expected to contain SDK 'is missing' message", err)
	}
}

// TestStream_TolerateTrailingMissing_DisabledStrictMode asserts
// the default (TolerateTrailingMissing=false) preserves pre-fix
// behaviour: any missing file in a bounded range is an error.
func TestStream_TolerateTrailingMissing_DisabledStrictMode(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, store := tolerateTrailingEdgeStore(t, tmp)
	t.Cleanup(func() { _ = store.Close() })

	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish config: %v", err)
	}

	for seq := uint32(5); seq <= 7; seq++ {
		writeLedgerFixture(t, ctx, store, cfg.Schema, seq)
	}

	lsCfg := ledgerstream.Config{
		DataStore: cfg,
		// TolerateTrailingMissing default false
	}
	err := ledgerstream.Stream(ctx, lsCfg, 5, 9, func(_ xdr.LedgerCloseMeta) error {
		return nil
	})
	if err == nil {
		t.Fatalf("Stream returned nil; expected missing-file error in strict mode")
	}
}

// tolerateTrailingEdgeStore creates a fresh filesystem datastore
// shaped like Galexie's default (1 ledger per file, 1 file per
// partition for test compactness). Returns the config + opened
// store; caller must Close.
func tolerateTrailingEdgeStore(t *testing.T, dir string) (datastore.DataStoreConfig, datastore.DataStore) {
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	cfg := datastore.DataStoreConfig{
		Type: "Filesystem",
		Params: map[string]string{
			"destination_path": dir,
		},
		Schema: datastore.DataStoreSchema{
			LedgersPerFile:    1,
			FilesPerPartition: 1,
		},
		NetworkPassphrase: "Test SDF Network ; September 2015",
		Compression:       "zstd",
	}
	return cfg, store
}
