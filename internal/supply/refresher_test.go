package supply

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"testing"
	"time"
)

type stubLedgers struct {
	ledger     uint32
	observedAt time.Time
	err        error
}

func (s stubLedgers) LatestKnownLedger(_ context.Context) (uint32, time.Time, error) {
	return s.ledger, s.observedAt, s.err
}

type stubComputer struct {
	out Supply
	err error
}

func (s stubComputer) Compute(_ context.Context, ledger uint32, observedAt time.Time) (Supply, error) {
	if s.err != nil {
		return Supply{}, s.err
	}
	out := s.out
	out.LedgerSequence = ledger
	out.ObservedAt = observedAt
	return out, nil
}

type stubInserter struct {
	calls int
	err   error
}

func (s *stubInserter) InsertSupply(_ context.Context, _ Supply) error {
	s.calls++
	return s.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRefresher_HappyPath(t *testing.T) {
	inserter := &stubInserter{}
	r := NewRefresher(
		stubLedgers{ledger: 50_000_000, observedAt: time.Unix(1_770_000_000, 0).UTC()},
		stubComputer{out: Supply{
			AssetKey:          "XLM",
			TotalSupply:       big.NewInt(1_000_000),
			CirculatingSupply: big.NewInt(900_000),
			Basis:             BasisXLMSDFReserveExclusion,
		}},
		inserter,
		discardLogger(),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindOK {
		t.Fatalf("kind=%s, want ok; err=%v", out.Kind, out.Err)
	}
	if inserter.calls != 1 {
		t.Errorf("inserter.calls=%d want 1", inserter.calls)
	}
	if out.Snapshot.LedgerSequence != 50_000_000 {
		t.Errorf("snapshot ledger=%d want 50000000", out.Snapshot.LedgerSequence)
	}
}

// TestRefresher_StaleComponentRejected pins F-1236 (codex
// audit-2026-05-12): a snapshot whose MinComponentLedger lags
// the snapshot ledger by more than the threshold is rejected
// with OutcomeKindStaleComponent. The inserter is NOT called.
func TestRefresher_StaleComponentRejected(t *testing.T) {
	inserter := &stubInserter{}
	r := NewRefresher(
		stubLedgers{ledger: 50_001_500, observedAt: time.Unix(1_770_000_000, 0).UTC()},
		stubComputer{out: Supply{
			AssetKey:           "XLM",
			TotalSupply:        big.NewInt(1_000_000),
			CirculatingSupply:  big.NewInt(900_000),
			Basis:              BasisXLMSDFReserveExclusion,
			LedgerSequence:     50_001_500,
			MinComponentLedger: 50_000_000, // 1500 ledgers behind
		}},
		inserter,
		discardLogger(),
		// threshold 1000 — gap 1500 > 1000, must reject.
		WithStaleComponentLedgers(1000),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindStaleComponent {
		t.Fatalf("kind=%s, want %s (err=%v)", out.Kind, OutcomeKindStaleComponent, out.Err)
	}
	if inserter.calls != 0 {
		t.Errorf("inserter called on stale-component snapshot (want 0, got %d)", inserter.calls)
	}
}

// TestRefresher_StaleComponentBelowThresholdAccepted pins the
// happy-path branch: a snapshot whose component lag is within
// the threshold inserts cleanly.
func TestRefresher_StaleComponentBelowThresholdAccepted(t *testing.T) {
	inserter := &stubInserter{}
	r := NewRefresher(
		stubLedgers{ledger: 50_000_500, observedAt: time.Unix(1_770_000_000, 0).UTC()},
		stubComputer{out: Supply{
			AssetKey:           "XLM",
			TotalSupply:        big.NewInt(1_000_000),
			CirculatingSupply:  big.NewInt(900_000),
			Basis:              BasisXLMSDFReserveExclusion,
			LedgerSequence:     50_000_500,
			MinComponentLedger: 50_000_000, // 500 ledgers behind — within threshold
		}},
		inserter,
		discardLogger(),
		WithStaleComponentLedgers(1000),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindOK {
		t.Fatalf("kind=%s, want ok (err=%v)", out.Kind, out.Err)
	}
	if inserter.calls != 1 {
		t.Errorf("inserter.calls=%d want 1", inserter.calls)
	}
}

// TestRefresher_StaleComponentZeroDisablesGate pins the
// legacy-compat branch: when the computer doesn't populate
// MinComponentLedger (legacy / non-storage-backed paths) the
// gate is skipped and snapshots insert as before.
func TestRefresher_StaleComponentZeroDisablesGate(t *testing.T) {
	inserter := &stubInserter{}
	r := NewRefresher(
		stubLedgers{ledger: 50_000_500, observedAt: time.Unix(1_770_000_000, 0).UTC()},
		stubComputer{out: Supply{
			AssetKey:          "XLM",
			TotalSupply:       big.NewInt(1_000_000),
			CirculatingSupply: big.NewInt(900_000),
			Basis:             BasisXLMSDFReserveExclusion,
			LedgerSequence:    50_000_500,
			// MinComponentLedger left zero — legacy computer.
		}},
		inserter,
		discardLogger(),
		WithStaleComponentLedgers(1000),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindOK {
		t.Fatalf("kind=%s, want ok (err=%v)", out.Kind, out.Err)
	}
	if inserter.calls != 1 {
		t.Errorf("inserter.calls=%d want 1", inserter.calls)
	}
}

func TestRefresher_NoLedger(t *testing.T) {
	inserter := &stubInserter{}
	r := NewRefresher(
		stubLedgers{err: errors.New("no cursors yet")},
		stubComputer{},
		inserter,
		discardLogger(),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindNoLedger {
		t.Errorf("kind=%s want %s", out.Kind, OutcomeKindNoLedger)
	}
	if inserter.calls != 0 {
		t.Errorf("inserter called on no-ledger outcome")
	}
}

// TestRefresher_NoObservation — ErrNoObservation surfaces as the
// dedicated outcome so the bootstrap-progress signal is chartable.
func TestRefresher_NoObservation(t *testing.T) {
	r := NewRefresher(
		stubLedgers{ledger: 1, observedAt: time.Now()},
		stubComputer{err: ErrNoObservation},
		&stubInserter{},
		discardLogger(),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindNoObservation {
		t.Errorf("kind=%s want %s", out.Kind, OutcomeKindNoObservation)
	}
}

// TestRefresher_GenericComputeError — non-observation errors map
// to compute_error.
func TestRefresher_GenericComputeError(t *testing.T) {
	r := NewRefresher(
		stubLedgers{ledger: 1, observedAt: time.Now()},
		stubComputer{err: errors.New("computer is broken")},
		&stubInserter{},
		discardLogger(),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindComputeError {
		t.Errorf("kind=%s want %s", out.Kind, OutcomeKindComputeError)
	}
}

func TestRefresher_WriteError(t *testing.T) {
	inserter := &stubInserter{err: errors.New("DB unreachable")}
	r := NewRefresher(
		stubLedgers{ledger: 1, observedAt: time.Now()},
		stubComputer{out: Supply{
			AssetKey:          "XLM",
			TotalSupply:       big.NewInt(1),
			CirculatingSupply: big.NewInt(1),
		}},
		inserter,
		discardLogger(),
	)
	out := r.Tick(context.Background())
	if out.Kind != OutcomeKindWriteError {
		t.Errorf("kind=%s want %s", out.Kind, OutcomeKindWriteError)
	}
	if inserter.calls != 1 {
		t.Errorf("inserter should have been called once before failing")
	}
}
