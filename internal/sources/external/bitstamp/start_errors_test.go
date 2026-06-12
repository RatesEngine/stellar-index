package bitstamp

import (
	"context"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Streamer.Start has two synchronous reject paths that don't touch
// the network: empty pairs slice, and a pair not in the PairMap.
// Pin both — the alternative is the goroutine spinning a websocket
// dial against a venue we can't usefully subscribe to.

func TestStreamer_Start_emptyPairsRejected(t *testing.T) {
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	if _, err := s.Start(context.Background(), nil); err == nil {
		t.Error("expected error on empty pairs, got nil")
	}
	if _, err := s.Start(context.Background(), []canonical.Pair{}); err == nil {
		t.Error("expected error on zero-length pairs, got nil")
	}
}

func TestStreamer_Start_unknownPairRejected(t *testing.T) {
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	// MATIC: in allow-list, intentionally not in DefaultPairs.
	matic, _ := canonical.NewCryptoAsset("MATIC")
	usd, _ := canonical.NewFiatAsset("USD")
	missing, err := canonical.NewPair(matic, usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}

	_, err = s.Start(context.Background(), []canonical.Pair{missing})
	if err == nil {
		t.Fatal("expected error for unknown MATIC/USD pair, got nil")
	}
	if !strings.Contains(err.Error(), "MATIC") {
		t.Errorf("error %q should cite the offending asset", err.Error())
	}
}
