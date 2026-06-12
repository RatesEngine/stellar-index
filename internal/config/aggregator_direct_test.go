package config_test

import (
	"strings"
	"testing"

	cfg "github.com/StellarAtlas/stellar-atlas/internal/config"
)

// AggregatorPairs and AggregatorWindows are documented as
// infallible in practice (validate() rejects bad entries at
// startup). They still return errors to keep the seam testable
// and to surface a regression loudly if validation is bypassed.
// Pin those error branches by calling the methods directly on a
// struct that skips validation — covers the seam, catches a
// refactor that swallows the underlying parse error.

func TestAggregatorPairs_emptyReturnsNil(t *testing.T) {
	a := cfg.AggregateConfig{}
	pairs, err := a.AggregatorPairs()
	if err != nil {
		t.Fatalf("empty pairs: %v", err)
	}
	if pairs != nil {
		t.Errorf("got %v, want nil for empty Pairs", pairs)
	}
}

func TestAggregatorPairs_directBadEntryErrors(t *testing.T) {
	// Bypass validate() by constructing AggregateConfig directly with
	// a pair string that won't parse. The method's error branch must
	// surface the offending entry in the error message.
	a := cfg.AggregateConfig{Pairs: []string{"bogus-not-a-pair"}}
	_, err := a.AggregatorPairs()
	if err == nil {
		t.Fatal("expected error from malformed pair, got nil")
	}
	if !strings.Contains(err.Error(), "bogus-not-a-pair") {
		t.Errorf("error %q should cite the offending entry", err.Error())
	}
}

func TestAggregatorWindows_emptyReturnsNil(t *testing.T) {
	a := cfg.AggregateConfig{}
	wins, err := a.AggregatorWindows()
	if err != nil {
		t.Fatalf("empty windows: %v", err)
	}
	if wins != nil {
		t.Errorf("got %v, want nil for empty Windows", wins)
	}
}

func TestAggregatorWindows_directBadEntryErrors(t *testing.T) {
	a := cfg.AggregateConfig{Windows: []string{"7 fortnights"}}
	_, err := a.AggregatorWindows()
	if err == nil {
		t.Fatal("expected error from malformed window, got nil")
	}
	if !strings.Contains(err.Error(), "7 fortnights") {
		t.Errorf("error %q should cite the offending entry", err.Error())
	}
}
