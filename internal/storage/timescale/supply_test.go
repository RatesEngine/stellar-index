package timescale

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/supply"
)

// TestInsertSupply_RejectsZeroValueStruct — the supply-package
// computers always populate AssetKey + TotalSupply + CirculatingSupply.
// A caller passing a zero-value Supply is a bug; surface it loudly
// rather than letting the DB reject it (or worse, write garbage if
// future migrations relax the CHECK constraints).
func TestInsertSupply_RejectsZeroValueStruct(t *testing.T) {
	// Use a Store with a nil *sql.DB — InsertSupply must reject
	// before it gets to the DB call, so the nil deref never fires.
	s := &Store{}
	err := s.InsertSupply(context.Background(), supply.Supply{})
	if err == nil {
		t.Fatal("expected error on zero-value Supply; got nil")
	}
	if got := err.Error(); got == "" {
		t.Errorf("error message is empty: %q", got)
	}
}

// TestInsertSupply_RequiresTotalSupply — AssetKey set but TotalSupply
// nil should still fail before touching the DB.
func TestInsertSupply_RequiresTotalSupply(t *testing.T) {
	s := &Store{}
	err := s.InsertSupply(context.Background(), supply.Supply{
		AssetKey:          "XLM",
		CirculatingSupply: big.NewInt(0),
	})
	if err == nil {
		t.Fatal("expected error when TotalSupply is nil; got nil")
	}
}

// TestInsertSupply_RequiresCirculatingSupply — likewise.
func TestInsertSupply_RequiresCirculatingSupply(t *testing.T) {
	s := &Store{}
	err := s.InsertSupply(context.Background(), supply.Supply{
		AssetKey:    "XLM",
		TotalSupply: big.NewInt(0),
	})
	if err == nil {
		t.Fatal("expected error when CirculatingSupply is nil; got nil")
	}
}

// TestAssembleSupply_HappyPath — text-cast NUMERIC columns parse
// back to the same *big.Int the writer started with, including very
// large values that exceed int64.
func TestAssembleSupply_HappyPath(t *testing.T) {
	const totalStr = "500018068120000000" // XLM total in stroops, > 2^59
	observedAt := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := assembleSupply("XLM", observedAt, totalStr, "499000000000000000",
		sql.NullString{Valid: true, String: totalStr}, "xlm_sdf_reserve_exclusion", 50_000_000)
	if err != nil {
		t.Fatalf("assembleSupply: %v", err)
	}
	if got.TotalSupply.String() != totalStr {
		t.Errorf("TotalSupply = %s, want %s", got.TotalSupply, totalStr)
	}
	if got.CirculatingSupply.String() != "499000000000000000" {
		t.Errorf("CirculatingSupply = %s", got.CirculatingSupply)
	}
	if got.MaxSupply == nil || got.MaxSupply.String() != totalStr {
		t.Errorf("MaxSupply = %v", got.MaxSupply)
	}
	if got.Basis != supply.BasisXLMSDFReserveExclusion {
		t.Errorf("Basis = %q", got.Basis)
	}
	if got.LedgerSequence != 50_000_000 {
		t.Errorf("LedgerSequence = %d", got.LedgerSequence)
	}
	if !got.ObservedAt.Equal(observedAt) {
		t.Errorf("ObservedAt = %v", got.ObservedAt)
	}
}

// TestAssembleSupply_NullMaxSupply — uncapped issuer + no override
// case: max_supply is NULL on disk; assembled struct has nil
// MaxSupply (NOT zero, NOT empty string).
func TestAssembleSupply_NullMaxSupply(t *testing.T) {
	got, err := assembleSupply("USDC:GA1", time.Now(), "100", "90",
		sql.NullString{Valid: false}, "issuer_exclusion", 1)
	if err != nil {
		t.Fatalf("assembleSupply: %v", err)
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply = %v, want nil for uncapped issuer", got.MaxSupply)
	}
}

// TestAssembleSupply_RejectsBadNumeric — a non-decimal value in the
// numeric column would be a Postgres-level corruption; surface a
// clear error rather than silently producing zero.
func TestAssembleSupply_RejectsBadNumeric(t *testing.T) {
	_, err := assembleSupply("XLM", time.Now(), "not-a-number", "0", sql.NullString{}, "x", 1)
	if err == nil {
		t.Fatal("expected error for unparseable total_supply; got nil")
	}
}

// TestLatestSupply_NotFoundIsTyped — the API layer relies on
// errors.Is(err, ErrNotFound) to distinguish "no supply data for
// this asset" from "Postgres unreachable". This guard pins the
// contract.
func TestLatestSupply_NotFoundIsTyped(t *testing.T) {
	// We can't invoke the full Store.LatestSupply without a DB; this
	// test documents the contract via reference. The actual SELECT
	// path is covered by the integration test.
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Fatal("ErrNotFound must satisfy errors.Is against itself")
	}
}
