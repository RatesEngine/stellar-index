package timescale

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/accounts"
)

// TestInsertAccountObservation_RejectsEmptyAccountID — the
// observer always populates AccountID. A zero-value AccountID is a
// caller bug; surface it loudly rather than letting the DB reject
// it (or worse, succeed with an empty string PK that no reader
// query will find).
func TestInsertAccountObservation_RejectsEmptyAccountID(t *testing.T) {
	// nil *sql.DB — the validation guard fires before any DB call.
	s := &Store{}
	err := s.InsertAccountObservation(context.Background(), accounts.Observation{
		Balance: big.NewInt(0),
	})
	if err == nil {
		t.Fatal("expected error on empty AccountID; got nil")
	}
	if !strings.Contains(err.Error(), "AccountID") {
		t.Errorf("err=%v should mention AccountID", err)
	}
}

// TestInsertAccountObservation_RejectsNilBalance — silently
// converting nil Balance to zero would publish wrong-but-plausible
// values to readers. Reject loudly.
func TestInsertAccountObservation_RejectsNilBalance(t *testing.T) {
	s := &Store{}
	err := s.InsertAccountObservation(context.Background(), accounts.Observation{
		AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
	})
	if err == nil {
		t.Fatal("expected error on nil Balance; got nil")
	}
	if !strings.Contains(err.Error(), "Balance") {
		t.Errorf("err=%v should mention Balance", err)
	}
	if !strings.Contains(err.Error(), "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF") {
		t.Errorf("err=%v should include the offending AccountID", err)
	}
}
