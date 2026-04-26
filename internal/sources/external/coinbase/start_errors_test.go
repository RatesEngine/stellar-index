package coinbase

import (
	"context"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Streamer.Start has two synchronous reject paths that don't touch
// the network: empty pairs slice, and a pair not in the PairMap.

func TestStreamer_Start_emptyPairsRejected(t *testing.T) {
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	if _, err := s.Start(context.Background(), nil); err == nil {
		t.Error("expected error on empty pairs, got nil")
	}
}

func TestStreamer_Start_unknownPairRejected(t *testing.T) {
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	ada, _ := canonical.NewCryptoAsset("ADA")
	usd, _ := canonical.NewFiatAsset("USD")
	missing, err := canonical.NewPair(ada, usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}

	_, err = s.Start(context.Background(), []canonical.Pair{missing})
	if err == nil {
		t.Fatal("expected error for unknown ADA/USD pair, got nil")
	}
	if !strings.Contains(err.Error(), "ADA") {
		t.Errorf("error %q should cite the offending asset", err.Error())
	}
}
