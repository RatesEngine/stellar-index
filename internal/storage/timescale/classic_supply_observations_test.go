package timescale

import (
	"context"
	"math/big"
	"strings"
	"testing"
)

// These tests cover the Insert*Observation defensive guards —
// the Sum* methods need a real DB and live in test/integration/
// (per the Test conventions in CLAUDE.md, integration tests run
// via testcontainers-go).

func TestInsertTrustlineObservation_RejectsEmptyAccountID(t *testing.T) {
	s := &Store{}
	err := s.InsertTrustlineObservation(context.Background(), TrustlineObservation{
		AssetKey: "USDC:GA5...",
		Balance:  big.NewInt(0),
	})
	if err == nil {
		t.Fatal("expected error on empty AccountID")
	}
	if !strings.Contains(err.Error(), "AccountID") {
		t.Errorf("err=%v should mention AccountID", err)
	}
}

func TestInsertTrustlineObservation_RejectsEmptyAssetKey(t *testing.T) {
	s := &Store{}
	err := s.InsertTrustlineObservation(context.Background(), TrustlineObservation{
		AccountID: "GA1",
		Balance:   big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "AssetKey") {
		t.Errorf("err=%v should mention AssetKey", err)
	}
}

func TestInsertTrustlineObservation_RejectsNilBalance(t *testing.T) {
	s := &Store{}
	err := s.InsertTrustlineObservation(context.Background(), TrustlineObservation{
		AccountID: "GA1",
		AssetKey:  "USDC:GA5...",
	})
	if err == nil || !strings.Contains(err.Error(), "Balance") {
		t.Errorf("err=%v should mention Balance", err)
	}
}

func TestInsertClaimableObservation_RejectsEmptyClaimableID(t *testing.T) {
	s := &Store{}
	err := s.InsertClaimableObservation(context.Background(), ClaimableObservation{
		AssetKey: "USDC:GA5...",
		Balance:  big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "ClaimableID") {
		t.Errorf("err=%v should mention ClaimableID", err)
	}
}

func TestInsertClaimableObservation_RejectsEmptyAssetKey(t *testing.T) {
	s := &Store{}
	err := s.InsertClaimableObservation(context.Background(), ClaimableObservation{
		ClaimableID: "abc",
		Balance:     big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "AssetKey") {
		t.Errorf("err=%v should mention AssetKey", err)
	}
}

func TestInsertClaimableObservation_RejectsNilBalance(t *testing.T) {
	s := &Store{}
	err := s.InsertClaimableObservation(context.Background(), ClaimableObservation{
		ClaimableID: "abc",
		AssetKey:    "USDC:GA5...",
	})
	if err == nil || !strings.Contains(err.Error(), "Balance") {
		t.Errorf("err=%v should mention Balance", err)
	}
}

func TestInsertLPReserveObservation_RejectsEmptyPoolID(t *testing.T) {
	s := &Store{}
	err := s.InsertLPReserveObservation(context.Background(), LPReserveObservation{
		AssetKey: "USDC:GA5...",
		Balance:  big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "PoolID") {
		t.Errorf("err=%v should mention PoolID", err)
	}
}

func TestInsertLPReserveObservation_RejectsEmptyAssetKey(t *testing.T) {
	s := &Store{}
	err := s.InsertLPReserveObservation(context.Background(), LPReserveObservation{
		PoolID:  "deadbeef",
		Balance: big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "AssetKey") {
		t.Errorf("err=%v should mention AssetKey", err)
	}
}

func TestInsertLPReserveObservation_RejectsNilBalance(t *testing.T) {
	s := &Store{}
	err := s.InsertLPReserveObservation(context.Background(), LPReserveObservation{
		PoolID:   "deadbeef",
		AssetKey: "USDC:GA5...",
	})
	if err == nil || !strings.Contains(err.Error(), "Balance") {
		t.Errorf("err=%v should mention Balance", err)
	}
}

func TestInsertSACBalanceObservation_RejectsEmptyContractID(t *testing.T) {
	s := &Store{}
	err := s.InsertSACBalanceObservation(context.Background(), SACBalanceObservation{
		AssetKey: "USDC:GA5...",
		Holder:   "GA1",
		Balance:  big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "ContractID") {
		t.Errorf("err=%v should mention ContractID", err)
	}
}

func TestInsertSACBalanceObservation_RejectsEmptyHolder(t *testing.T) {
	s := &Store{}
	err := s.InsertSACBalanceObservation(context.Background(), SACBalanceObservation{
		ContractID: "CA1",
		AssetKey:   "USDC:GA5...",
		Balance:    big.NewInt(0),
	})
	if err == nil || !strings.Contains(err.Error(), "Holder") {
		t.Errorf("err=%v should mention Holder", err)
	}
}

func TestInsertSACBalanceObservation_RejectsNilBalance(t *testing.T) {
	s := &Store{}
	err := s.InsertSACBalanceObservation(context.Background(), SACBalanceObservation{
		ContractID: "CA1",
		AssetKey:   "USDC:GA5...",
		Holder:     "GA1",
	})
	if err == nil || !strings.Contains(err.Error(), "Balance") {
		t.Errorf("err=%v should mention Balance", err)
	}
}
