package bitstamp

import (
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// symbolsFor inverts the operator-supplied PairMap so the streamer
// can render a list of canonical pairs as the Bitstamp wire symbols
// it must subscribe to. A pair missing from the map is fatal — we
// don't want to silently subscribe to nothing and starve a market.

func TestSymbolsFor_happyPath(t *testing.T) {
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	xlmusd := pm["xlmusd"]
	btcusd := pm["btcusd"]
	got, err := s.symbolsFor([]canonical.Pair{xlmusd, btcusd})
	if err != nil {
		t.Fatalf("symbolsFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	want := map[string]bool{"xlmusd": true, "btcusd": true}
	for _, sym := range got {
		if !want[sym] {
			t.Errorf("unexpected symbol %q", sym)
		}
	}
}

func TestSymbolsFor_unknownPairRejected(t *testing.T) {
	// A pair with no matching PairMap entry must surface as an
	// error, not a silent omission — the operator's intent is to
	// see ticks for that pair, and skipping it would create a
	// confusing "venue went quiet" appearance downstream.
	pm, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(pm)

	// MATIC: in allow-list, intentionally not in DefaultPairs.
	matic, err := canonical.NewCryptoAsset("MATIC")
	if err != nil {
		t.Fatalf("NewCryptoAsset(MATIC): %v", err)
	}
	usd, err := canonical.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("NewFiatAsset(USD): %v", err)
	}
	missing, err := canonical.NewPair(matic, usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}

	if _, err := s.symbolsFor([]canonical.Pair{missing}); err == nil {
		t.Fatal("expected error for MATIC/USD (not in DefaultPairs), got nil")
	}
}

// jitter scatters reconnect timers ±25% so a fleet doesn't
// thunder-herd a venue after a shared disconnect. Verify the
// envelope is respected and degenerate inputs pass through.

func TestJitter_withinEnvelope(t *testing.T) {
	base := 4 * time.Second
	low := base - base/4
	high := base + base/4
	for i := 0; i < 200; i++ {
		got := jitter(base)
		if got < low || got > high {
			t.Fatalf("jitter(%v) = %v, outside [%v,%v]", base, got, low, high)
		}
	}
}

func TestJitter_zeroOrNegativePassthrough(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	if got := jitter(-1 * time.Second); got != -1*time.Second {
		t.Errorf("jitter(-1s) = %v, want -1s", got)
	}
}
