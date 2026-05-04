package timescale

import (
	"context"
	"strings"
	"testing"
)

// Defensive-guard coverage. The full INSERT/SELECT round-trip lives
// in test/integration/ per the established testcontainers-go pattern
// (see PR #316 / #317).

func TestUpsertSoroswapPair_rejectsEmptyPair(t *testing.T) {
	s := &Store{}
	err := s.UpsertSoroswapPair(context.Background(), "", "C0", "C1")
	if err == nil || !strings.Contains(err.Error(), "pair_strkey") {
		t.Errorf("err=%v should mention pair_strkey", err)
	}
}

func TestUpsertSoroswapPair_rejectsEmptyToken0(t *testing.T) {
	s := &Store{}
	err := s.UpsertSoroswapPair(context.Background(), "Cpair", "", "C1")
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("err=%v should mention token", err)
	}
}

func TestUpsertSoroswapPair_rejectsEmptyToken1(t *testing.T) {
	s := &Store{}
	err := s.UpsertSoroswapPair(context.Background(), "Cpair", "C0", "")
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("err=%v should mention token", err)
	}
}
