package ledgerstream_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/support/compressxdr"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
)

// TestStream_boundedRange_filesystemDatastore proves the SDK's
// BufferedStorageBackend happy path flows through our wrapper:
// write three ledger files into a temp directory in the Galexie
// layout, call Stream(from=5, to=7), confirm the callback got all
// three LedgerCloseMeta values in order.
//
// Filesystem datastore (no Docker) — unit-test-grade. A separate
// MinIO integration test lands in PR 165d when the indexer wires
// to live MinIO.
func TestStream_boundedRange_filesystemDatastore(t *testing.T) {
	tmp := t.TempDir()

	// The Galexie layout this test simulates: 1 ledger per file, no
	// partition directories (FilesPerPartition=1). Object keys look
	// like `FFFFFFFA--5.xdr.zstd` for ledger 5, etc.
	const ledgersPerFile = 1
	const filesPerPartition = 1

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := datastore.NewFilesystemDataStoreWithPath(tmp)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type: "Filesystem",
		Params: map[string]string{
			"destination_path": tmp,
		},
		Schema: datastore.DataStoreSchema{
			LedgersPerFile:    ledgersPerFile,
			FilesPerPartition: filesPerPartition,
		},
		NetworkPassphrase: "Test SDF Network ; September 2015",
		Compression:       "zstd",
	}

	// Publish the datastore manifest so LoadSchema (called inside
	// ApplyLedgerMetadata) can find it.
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish config: %v", err)
	}

	// Seed three ledger files.
	const fromLedger = 5
	const toLedger = 7
	for seq := uint32(fromLedger); seq <= toLedger; seq++ {
		writeLedgerFixture(t, ctx, store, cfg.Schema, seq)
	}

	// Verify ListFilePaths sees them — canary for the layout.
	files, err := store.ListFilePaths(ctx, datastore.ListFileOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) < 3 {
		t.Fatalf("expected ≥3 files, got %d: %v", len(files), files)
	}

	// Drive our wrapper; collect the LCMs the callback sees.
	got := make([]xdr.LedgerCloseMeta, 0, 3)
	err = ledgerstream.Stream(ctx,
		ledgerstream.Config{DataStore: cfg},
		fromLedger, toLedger,
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("callback invoked %d times, want 3", len(got))
	}
	for i, lcm := range got {
		wantSeq := uint32(fromLedger + i)
		gotSeq := uint32(lcm.LedgerSequence())
		if gotSeq != wantSeq {
			t.Errorf("ledger[%d]: got seq %d, want %d", i, gotSeq, wantSeq)
		}
	}
}

// TestStream_singleLedgerBoundedRange pins the ch-live-catchup
// tip-extend case: Stream(from=N, to=N) must walk exactly one
// ledger. The SDK's ingest.ApplyLedgerMetadata rejects single-ledger
// bounded ranges (`invalid end value for bounded range`), so Stream
// routes them through the in-house hot-only walk — without that,
// every catch-up run that fired exactly one ledger behind the
// galexie tip failed (observed flapping on r1, 2026-06-11).
func TestStream_singleLedgerBoundedRange(t *testing.T) {
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := datastore.NewFilesystemDataStoreWithPath(tmp)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type: "Filesystem",
		Params: map[string]string{
			"destination_path": tmp,
		},
		Schema: datastore.DataStoreSchema{
			LedgersPerFile:    1,
			FilesPerPartition: 1,
		},
		NetworkPassphrase: "Test SDF Network ; September 2015",
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish config: %v", err)
	}

	const seq = uint32(9)
	writeLedgerFixture(t, ctx, store, cfg.Schema, seq)

	got := make([]xdr.LedgerCloseMeta, 0, 1)
	err = ledgerstream.Stream(ctx,
		ledgerstream.Config{DataStore: cfg},
		seq, seq,
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Stream(from=to=%d): %v", seq, err)
	}
	if len(got) != 1 {
		t.Fatalf("callback invoked %d times, want 1", len(got))
	}
	if gotSeq := uint32(got[0].LedgerSequence()); gotSeq != seq {
		t.Errorf("got seq %d, want %d", gotSeq, seq)
	}
}

func TestStream_rejectsNilCallback(t *testing.T) {
	err := ledgerstream.Stream(
		context.Background(),
		ledgerstream.Config{
			DataStore: datastore.DataStoreConfig{Type: "Filesystem"},
		},
		1, 2,
		nil,
	)
	if err == nil {
		t.Fatal("expected error on nil callback")
	}
}

func TestStream_rejectsEmptyDataStoreType(t *testing.T) {
	err := ledgerstream.Stream(
		context.Background(),
		ledgerstream.Config{},
		1, 2,
		func(xdr.LedgerCloseMeta) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error on empty DataStore.Type")
	}
}

func TestStream_callbackErrorAborts(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := datastore.NewFilesystemDataStoreWithPath(tmp)
	if err != nil {
		t.Fatalf("open: %v", err)
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

	sentinel := errors.New("callback-said-stop")
	callCount := 0
	err = ledgerstream.Stream(ctx,
		ledgerstream.Config{DataStore: cfg},
		5, 6,
		func(xdr.LedgerCloseMeta) error {
			callCount++
			return sentinel
		},
	)
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain lost sentinel: %v", err)
	}
	// Stream stops at first callback error — second ledger never
	// invokes the callback.
	if callCount != 1 {
		t.Errorf("callback called %d times, want 1 (stop on first error)", callCount)
	}
}

// ─── fixture helpers ────────────────────────────────────────────

// writeLedgerFixture constructs a minimal xdr.LedgerCloseMeta for
// the given sequence number, wraps it in an xdr.LedgerCloseMetaBatch,
// zstd-compresses to XDR bytes, and writes to the Galexie object-key
// for seq. Mirrors what Galexie itself would produce, but constructed
// in-test so we don't ship fixture binaries in the repo.
func writeLedgerFixture(t *testing.T, ctx context.Context, store datastore.DataStore, schema datastore.DataStoreSchema, seq uint32) {
	t.Helper()
	lcm := minimalLedgerCloseMeta(seq)
	batch := xdr.LedgerCloseMetaBatch{
		StartSequence:    xdr.Uint32(seq),
		EndSequence:      xdr.Uint32(seq),
		LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
	}
	buf := encodeBatch(t, batch)
	key := schema.GetObjectKeyFromSequenceNumber(seq)
	if err := store.PutFile(ctx, key, bufferWriterTo(buf), nil); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	// Sanity: confirm the file is on disk where we expect.
	base, _ := os.Getwd()
	_ = base
	if exists, _ := store.Exists(ctx, key); !exists {
		t.Fatalf("file not found after put: %s", key)
	}
	_ = filepath.Join // silence unused on older paths
}

// minimalLedgerCloseMeta builds a V1 LCM with just the ledger
// sequence populated + an empty GeneralizedTransactionSet arm so
// the XDR encoder doesn't reject the zero-valued union switch.
//
// Our Stream wrapper doesn't inspect any field beyond what the
// SDK's BufferedStorageBackend needs to round-trip the batch;
// downstream decoders that care about tx content will use larger
// fixtures captured from mainnet.
func minimalLedgerCloseMeta(seq uint32) xdr.LedgerCloseMeta {
	return xdr.LedgerCloseMeta{
		V: 1,
		V1: &xdr.LedgerCloseMetaV1{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
				},
			},
			TxSet: xdr.GeneralizedTransactionSet{
				V:       1,
				V1TxSet: &xdr.TransactionSetV1{},
			},
		},
	}
}

// encodeBatch marshals a LedgerCloseMetaBatch to XDR and zstd-
// compresses — the on-wire format Galexie emits.
func encodeBatch(t *testing.T, batch xdr.LedgerCloseMetaBatch) []byte {
	t.Helper()
	encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
	var buf bytes.Buffer
	if _, err := encoder.WriteTo(&buf); err != nil {
		t.Fatalf("encode batch: %v", err)
	}
	return buf.Bytes()
}

// bufferWriterTo adapts a []byte to io.WriterTo — the interface
// PutFile expects.
type bufferWriterTo []byte

func (b bufferWriterTo) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}
