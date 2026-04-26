package binance

import (
	"testing"
	"time"
)

// jitter scatters Binance reconnect timers ±25% so a fleet
// disconnect doesn't thunder-herd binance.com on the next tick.

func TestJitter_envelopeAndDegenerateInputs(t *testing.T) {
	base := 4 * time.Second
	low, high := base-base/4, base+base/4
	for i := 0; i < 200; i++ {
		got := jitter(base)
		if got < low || got > high {
			t.Fatalf("jitter(%v)=%v outside [%v,%v]", base, got, low, high)
		}
	}
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0)=%v, want 0", got)
	}
	if got := jitter(-time.Second); got != -time.Second {
		t.Errorf("jitter(-1s)=%v, want -1s", got)
	}
}
