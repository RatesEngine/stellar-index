//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/support/compressxdr"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
)

// testPassphrase is the SDF testnet passphrase. Doesn't matter for
// hash-only tests but must be SOMETHING the SDK accepts.
const testPassphrase = "Test SDF Network ; September 2015"

// TestRunVerifyChunks_FilesystemBackend_ParallelWalk drives the
// chunked verify path end-to-end against a real filesystem-backed
// datastore (no MinIO testcontainer needed). Verifies:
//
//   - 8 chunks each see their slice of the ledger range
//   - per-chunk Verified counters sum to the input range
//   - boundary stitch passes (FirstPrevHash / LastHash continuity)
//   - chain check internal to each chunk reports zero mismatches
//
// Empty ledgers all carry zero hashes; PreviousLedgerHash on each
// is the zero hash too — so the chain check holds trivially. This
// test exercises the parallel control flow + stitch math without
// needing realistic XDR to be hand-rolled.
func TestRunVerifyChunks_FilesystemBackend_ParallelWalk(t *testing.T) {
	const (
		from    = uint32(100)
		to      = uint32(163) // 64 ledgers — a full checkpoint range
		workers = 8
	)

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	seqs := make([]uint32, 0, to-from+1)
	for s := from; s <= to; s++ {
		seqs = append(seqs, s)
	}
	seedEmptyLedgers(t, ctx, dir, seqs)

	chunks := splitRange(from, to, workers)
	if len(chunks) != workers {
		t.Fatalf("splitRange returned %d chunks, want %d", len(chunks), workers)
	}

	lsCfg := filesystemLedgerstreamConfig(dir)

	results, err := runVerifyChunks(
		ctx, lsCfg, chunks,
		true,  // doChain
		false, // doCheckpoint — no Tier B fixtures wired in this test
		filepath.Join(dir, "no-archive-root"),
		time.Now(),
		1*time.Hour, // suppress per-chunk progress prints
		chunkOrchestratorOpts{},
	)
	if err != nil {
		t.Fatalf("runVerifyChunks: %v", err)
	}
	if len(results) != workers {
		t.Fatalf("got %d results, want %d", len(results), workers)
	}

	totalVerified := 0
	for i, r := range results {
		if r.Idx != i {
			t.Errorf("results[%d].Idx = %d, want %d", i, r.Idx, i)
		}
		if r.Mismatches != 0 {
			t.Errorf("results[%d] reports %d mismatches; chain check should pass on empty ledgers", i, r.Mismatches)
		}
		expectedSpan := chunks[i].to - chunks[i].from + 1
		if uint32(r.Verified) != expectedSpan {
			t.Errorf("results[%d] verified %d ledgers, want %d for chunk [%d, %d]",
				i, r.Verified, expectedSpan, chunks[i].from, chunks[i].to)
		}
		totalVerified += r.Verified
	}

	expectedTotal := int(to - from + 1)
	if totalVerified != expectedTotal {
		t.Errorf("total verified = %d, want %d", totalVerified, expectedTotal)
	}

	if err := stitchChunks(results); err != nil {
		t.Errorf("stitchChunks: %v — empty-ledger boundaries should match (zero hash on both sides)", err)
	}
}

// TestRunVerifyChunks_FilesystemBackend_SerialPath confirms the
// same fixture works on the workers=1 path. Regression guard: a
// future change to splitRange that broke the single-chunk degenerate
// case would surface here, not just via the parallel test.
func TestRunVerifyChunks_FilesystemBackend_SerialPath(t *testing.T) {
	const (
		from = uint32(200)
		to   = uint32(231)
	)

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	seqs := make([]uint32, 0, to-from+1)
	for s := from; s <= to; s++ {
		seqs = append(seqs, s)
	}
	seedEmptyLedgers(t, ctx, dir, seqs)

	chunks := splitRange(from, to, 1)
	results, err := runVerifyChunks(
		ctx, filesystemLedgerstreamConfig(dir), chunks,
		true, false, "",
		time.Now(), 1*time.Hour,
		chunkOrchestratorOpts{},
	)
	if err != nil {
		t.Fatalf("runVerifyChunks: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Verified != int(to-from+1) {
		t.Errorf("Verified = %d, want %d", results[0].Verified, int(to-from+1))
	}
	if err := stitchChunks(results); err != nil {
		t.Errorf("single-chunk stitch should pass: %v", err)
	}
}

// ─── fixture helpers ────────────────────────────────────────────────

// filesystemLedgerstreamConfig is a copy of the helper from
// test/integration/ledgerstream_to_storage_test.go — duplicated so
// the cmd/ratesengine-ops integration test doesn't have to import
// across package boundaries.
func filesystemLedgerstreamConfig(dir string) ledgerstream.Config {
	return ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type:              "Filesystem",
			Params:            map[string]string{"destination_path": dir},
			Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
			NetworkPassphrase: testPassphrase,
			Compression:       "zstd",
		},
	}
}

// seedEmptyLedgers writes valid-but-empty xdr.LedgerCloseMeta values
// to the filesystem datastore at `dir`. Each ledger has the right
// LedgerSeq + an empty TxSet; PreviousLedgerHash + Hash default to
// zero — the chain check passes trivially because zero == zero.
//
// Mirrored from test/integration/ledgerstream_to_storage_test.go for
// the same reason filesystemLedgerstreamConfig is duplicated: keeps
// this test file self-contained inside cmd/ratesengine-ops.
func seedEmptyLedgers(t *testing.T, ctx context.Context, dir string, seqs []uint32) { //nolint:revive // ctx-second matches the test/integration helper signature
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:              "Filesystem",
		Params:            map[string]string{"destination_path": dir},
		Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
		NetworkPassphrase: testPassphrase,
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}

	for _, seq := range seqs {
		lcm := xdr.LedgerCloseMeta{
			V: 1,
			V1: &xdr.LedgerCloseMetaV1{
				LedgerHeader: xdr.LedgerHeaderHistoryEntry{
					Header: xdr.LedgerHeader{LedgerSeq: xdr.Uint32(seq)},
				},
				TxSet: xdr.GeneralizedTransactionSet{
					V:       1,
					V1TxSet: &xdr.TransactionSetV1{},
				},
			},
		}
		batch := xdr.LedgerCloseMetaBatch{
			StartSequence:    xdr.Uint32(seq),
			EndSequence:      xdr.Uint32(seq),
			LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
		}
		encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
		var buf bytes.Buffer
		if _, err := encoder.WriteTo(&buf); err != nil {
			t.Fatalf("encode batch seq=%d: %v", seq, err)
		}
		key := cfg.Schema.GetObjectKeyFromSequenceNumber(seq)
		if err := store.PutFile(ctx, key, byteSliceWriterTo(buf.Bytes()), nil); err != nil {
			t.Fatalf("put seq=%d: %v", seq, err)
		}
	}
}

type byteSliceWriterTo []byte

func (b byteSliceWriterTo) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}
