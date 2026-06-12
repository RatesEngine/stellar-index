package ledgerstream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
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

// TestStream_liveRetryWait_picksUpLateLedger pins Config.LiveRetryWait.
// On an unbounded (live-tail) stream, a fetch worker that misses the
// next ledger object must re-check within LiveRetryWait, not the
// SDK's 30s default. The test writes ledger 7 ~400ms AFTER the stream
// has already caught up to ledger 6 — so the worker fetching 7 has
// missed and entered its retry-wait loop — then asserts the stream
// delivers 7 well inside an 8s window. That is impossible if the 30s
// default leaked through, so the test genuinely exercises the
// override and guards the live-tail ingest-lag fix from regression.
func TestStream_liveRetryWait_picksUpLateLedger(t *testing.T) {
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
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

	// Pre-encode ledger 7 on the test goroutine — the fixture helpers
	// call t.Fatalf, which is illegal off the test goroutine.
	buf7 := encodeBatch(t, xdr.LedgerCloseMetaBatch{
		StartSequence:    xdr.Uint32(7),
		EndSequence:      xdr.Uint32(7),
		LedgerCloseMetas: []xdr.LedgerCloseMeta{minimalLedgerCloseMeta(7)},
	})
	key7 := cfg.Schema.GetObjectKeyFromSequenceNumber(7)

	// Publish ledger 7 only AFTER the stream has caught up. With a
	// short LiveRetryWait the worker waiting on 7 must surface it
	// promptly; with the 30s default it would miss the 8s window.
	putErr := make(chan error, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		putErr <- store.PutFile(ctx, key7, bufferWriterTo(buf7), nil)
	}()

	stop := errors.New("stop")
	got := make([]uint32, 0, 3)
	err = ledgerstream.Stream(ctx,
		ledgerstream.Config{
			DataStore:     cfg,
			LiveRetryWait: 50 * time.Millisecond,
		},
		5, 0, /*unbounded — live tail*/
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm.LedgerSequence())
			if lcm.LedgerSequence() == 7 {
				return stop
			}
			return nil
		},
	)
	if !errors.Is(err, stop) {
		t.Fatalf("stream did not deliver ledger 7 within 8s — "+
			"LiveRetryWait override not honored? err=%v got=%v", err, got)
	}
	if perr := <-putErr; perr != nil {
		t.Fatalf("put ledger 7: %v", perr)
	}
	if len(got) != 3 || got[0] != 5 || got[1] != 6 || got[2] != 7 {
		t.Errorf("got %v, want [5 6 7]", got)
	}
}
