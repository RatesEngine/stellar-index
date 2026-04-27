package ledgerstream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
)

// TestStreamArchiveThenLive_crossesSeam covers the production
// happy path: ledgers 5..9 in the archive bucket, ledgers 10..15
// in the live bucket, seam=10 marks the handoff. StreamArchiveThenLive
// must read the archive [5,9] bounded then continue into live
// [10, ∞) until the callback signals stop.
func TestStreamArchiveThenLive_crossesSeam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const seam = 10
	const from = 5
	const lastLive = 15

	archiveCfg, _ := newSeededFilesystemDataStore(t, ctx, from, seam-1)
	liveCfg, _ := newSeededFilesystemDataStore(t, ctx, seam, lastLive)

	stop := errors.New("stop-after-last-live")
	got := make([]uint32, 0, lastLive-from+1)
	err := ledgerstream.StreamArchiveThenLive(
		ctx,
		ledgerstream.Config{DataStore: archiveCfg},
		ledgerstream.Config{DataStore: liveCfg},
		from, seam, nil,
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm.LedgerSequence())
			if lcm.LedgerSequence() == lastLive {
				return stop
			}
			return nil
		},
	)
	if !errors.Is(err, stop) {
		t.Fatalf("expected stop sentinel, got %v", err)
	}
	if len(got) != int(lastLive-from+1) {
		t.Fatalf("received %d ledgers, want %d (got=%v)",
			len(got), lastLive-from+1, got)
	}
	for i, seq := range got {
		want := uint32(from) + uint32(i)
		if seq != want {
			t.Errorf("got[%d]=%d, want %d (full=%v)", i, seq, want, got)
		}
	}
}

// TestStreamArchiveThenLive_seamZeroLiveOnly verifies the seam=0
// short-circuit: archive bucket is unused, the call degrades to a
// plain unbounded Stream against the live config. Critical for
// backwards-compat with the pre-2026-04-26 deployment shape.
func TestStreamArchiveThenLive_seamZeroLiveOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const from = 5
	const lastLive = 8

	// Archive cfg points at an empty datastore — if the function
	// reads from it, the test fails (no manifest, no objects).
	archiveCfg := datastore.DataStoreConfig{
		Type:   "Filesystem",
		Params: map[string]string{"destination_path": t.TempDir()},
	}
	liveCfg, _ := newSeededFilesystemDataStore(t, ctx, from, lastLive)

	stop := errors.New("stop")
	got := make([]uint32, 0, lastLive-from+1)
	err := ledgerstream.StreamArchiveThenLive(
		ctx,
		ledgerstream.Config{DataStore: archiveCfg},
		ledgerstream.Config{DataStore: liveCfg},
		from, 0, /*seam=0 → live-only*/
		nil,
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm.LedgerSequence())
			if lcm.LedgerSequence() == lastLive {
				return stop
			}
			return nil
		},
	)
	if !errors.Is(err, stop) {
		t.Fatalf("expected stop sentinel, got %v", err)
	}
	if len(got) != int(lastLive-from+1) {
		t.Fatalf("received %d ledgers, want %d (got=%v)",
			len(got), lastLive-from+1, got)
	}
}

// TestStreamArchiveThenLive_fromAboveSeamLiveOnly covers the
// "resume from a cursor that's already past the seam" case: from=20,
// seam=10. The archive read is skipped entirely (seam-1=9 < from=20
// would be an inverted bounded range and crash the SDK).
func TestStreamArchiveThenLive_fromAboveSeamLiveOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const from = 20
	const seam = 10
	const lastLive = 22

	// Empty archive — same rationale as the seam=0 test.
	archiveCfg := datastore.DataStoreConfig{
		Type:   "Filesystem",
		Params: map[string]string{"destination_path": t.TempDir()},
	}
	liveCfg, _ := newSeededFilesystemDataStore(t, ctx, from, lastLive)

	stop := errors.New("stop")
	got := make([]uint32, 0, lastLive-from+1)
	err := ledgerstream.StreamArchiveThenLive(
		ctx,
		ledgerstream.Config{DataStore: archiveCfg},
		ledgerstream.Config{DataStore: liveCfg},
		from, seam, nil,
		func(lcm xdr.LedgerCloseMeta) error {
			got = append(got, lcm.LedgerSequence())
			if lcm.LedgerSequence() == lastLive {
				return stop
			}
			return nil
		},
	)
	if !errors.Is(err, stop) {
		t.Fatalf("expected stop sentinel, got %v", err)
	}
	if len(got) != int(lastLive-from+1) {
		t.Fatalf("received %d ledgers, want %d (got=%v)",
			len(got), lastLive-from+1, got)
	}
}

// newSeededFilesystemDataStore creates a temp filesystem datastore
// pre-seeded with ledgers [from, to] inclusive. Returns the config
// (suitable for ledgerstream.Config.DataStore) and the dir.
func newSeededFilesystemDataStore(t *testing.T, ctx context.Context, from, to uint32) (datastore.DataStoreConfig, string) {
	t.Helper()
	dir := t.TempDir()

	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:   "Filesystem",
		Params: map[string]string{"destination_path": dir},
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
	for seq := from; seq <= to; seq++ {
		writeLedgerFixture(t, ctx, store, cfg.Schema, seq)
	}
	return cfg, dir
}
