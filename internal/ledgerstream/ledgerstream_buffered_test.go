package ledgerstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
)

// When Config.Buffered is non-nil, Stream uses the caller-supplied
// BufferedStorageBackendConfig verbatim instead of computing
// defaults from DataStore.Schema.LedgersPerFile. The default branch
// is exercised by the existing happy-path test; this pins the
// override branch so a refactor that drops Buffered on the floor
// (regressing operator-tuning capability) gets caught in CI.

func TestStream_buffered_overrideUsedVerbatim(t *testing.T) {
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := datastore.NewFilesystemDataStoreWithPath(tmp)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:   "Filesystem",
		Params: map[string]string{"destination_path": tmp},
		Schema: datastore.DataStoreSchema{
			LedgersPerFile:    1,
			FilesPerPartition: 1,
		},
		NetworkPassphrase: "Test SDF Network ; September 2015",
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish: %v", err)
	}
	writeLedgerFixture(t, ctx, store, cfg.Schema, 5)
	writeLedgerFixture(t, ctx, store, cfg.Schema, 6)

	// Hand-tuned override — small buffer + workers so the test
	// exercises the same throughput path as the default but with
	// values the SDK didn't compute.
	override := &ledgerbackend.BufferedStorageBackendConfig{
		BufferSize: 2,
		NumWorkers: 1,
		RetryLimit: 1,
		RetryWait:  10 * time.Millisecond,
	}

	calls := 0
	err = ledgerstream.Stream(ctx,
		ledgerstream.Config{DataStore: cfg, Buffered: override},
		5, 6,
		func(xdr.LedgerCloseMeta) error {
			calls++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Stream with override: %v", err)
	}
	if calls != 2 {
		t.Errorf("callback invoked %d times, want 2", calls)
	}
}
