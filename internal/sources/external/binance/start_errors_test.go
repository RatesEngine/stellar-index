package binance

import (
	"context"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Streamer.Start has two pre-network reject paths: empty pairs
// slice (Binance requires explicit subscription, no auto-enum) and
// a pair not in the PairMap. Pin both — silent fallthrough would
// dial Binance with no subscription frame.

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
	usdt, _ := canonical.NewCryptoAsset("USDT")
	missing, err := canonical.NewPair(ada, usdt)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}

	_, err = s.Start(context.Background(), []canonical.Pair{missing})
	if err == nil {
		t.Fatal("expected error for unknown ADA/USDT pair, got nil")
	}
	if !strings.Contains(err.Error(), "ADA") {
		t.Errorf("error %q should cite the offending asset", err.Error())
	}
}
