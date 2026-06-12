package dispatcher

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// TestRecognize verifies Recognize reports a match (with decoder name)
// for handled topics, false for unhandled, and — importantly — has no
// side effects on the dispatcher's stats counters (so a recognition
// audit doesn't pollute the live unmatched/seen tallies).
func TestRecognize(t *testing.T) {
	disp := New(
		&fakeDecoder{name: "alpha", topic0: "swap"},
		&fakeDecoder{name: "beta", topic0: "sync"},
	)

	if name, ok := disp.Recognize(events.Event{Topic: []string{"swap"}}); !ok || name != "alpha" {
		t.Errorf("Recognize(swap) = (%q,%v), want (alpha,true)", name, ok)
	}
	if name, ok := disp.Recognize(events.Event{Topic: []string{"sync"}}); !ok || name != "beta" {
		t.Errorf("Recognize(sync) = (%q,%v), want (beta,true)", name, ok)
	}

	before := disp.unmatchedHits
	if _, ok := disp.Recognize(events.Event{Topic: []string{"mystery_topic"}}); ok {
		t.Error("Recognize(mystery_topic) = true, want false (recognition gap)")
	}
	if disp.unmatchedHits != before {
		t.Errorf("Recognize mutated unmatchedHits (%d → %d); must be side-effect-free", before, disp.unmatchedHits)
	}
}
