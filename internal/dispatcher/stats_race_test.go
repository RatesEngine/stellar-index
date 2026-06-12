package dispatcher

import (
	"sync"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// TestStats_concurrentWithDispatch is the F-1317 regression guard.
// Before the statsMu fix, the dispatch goroutine's `++` mutations of
// eventsSeen / decodeErrors / unmatchedHits raced the statsflush
// goroutine's Stats() snapshot, producing a fatal `concurrent map
// read and map write` panic. Run under `-race` this fails loudly if
// the lock is ever removed.
//
// The matched arm exercises eventsSeen + decodeErrors (the decoder
// returns an error every call); the unmatched arm exercises
// unmatchedHits. A second goroutine hammers Stats() the whole time.
//
// The decoder is stateless (no internal hit counters) so the two
// writer goroutines sharing it don't themselves race — the only
// shared mutable state under test is the dispatcher's counters.
func TestStats_concurrentWithDispatch(t *testing.T) {
	t.Parallel()

	// statelessDecoder matches topic[0]=="A" and always errors on
	// Decode so both eventsSeen AND decodeErrors get bumped on the
	// matched path.
	decoder := statelessRacerDecoder{}
	disp := New(decoder)

	const iterations = 2000
	var wg sync.WaitGroup

	// Writer 1: matched events (eventsSeen + decodeErrors).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_, _ = disp.dispatchOne(events.Event{Topic: []string{"A"}})
		}
	}()

	// Writer 2: unmatched events (unmatchedHits).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_, _ = disp.dispatchOne(events.Event{Topic: []string{"Z"}})
		}
	}()

	// Reader: continuous Stats() snapshots.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = disp.Stats()
		}
	}()

	wg.Wait()

	got := disp.Stats()
	if got.EventsSeen["racer"] != iterations {
		t.Errorf("eventsSeen[racer] = %d, want %d", got.EventsSeen["racer"], iterations)
	}
	if got.DecodeErrors["racer"] != iterations {
		t.Errorf("decodeErrors[racer] = %d, want %d", got.DecodeErrors["racer"], iterations)
	}
	if got.UnmatchedHits != iterations {
		t.Errorf("unmatchedHits = %d, want %d", got.UnmatchedHits, iterations)
	}
}

// statelessRacerDecoder is a Decoder with no mutable state, so two
// goroutines can call it concurrently without racing on anything but
// the dispatcher's own counters (which is the point of the test).
type statelessRacerDecoder struct{}

func (statelessRacerDecoder) Name() string { return "racer" }

func (statelessRacerDecoder) Matches(ev events.Event) bool {
	return len(ev.Topic) > 0 && ev.Topic[0] == "A"
}

func (statelessRacerDecoder) Decode(events.Event) ([]consumer.Event, error) {
	return nil, errSynthetic
}

var errSynthetic = synthErr("synthetic decode error")

type synthErr string

func (e synthErr) Error() string { return string(e) }
