package streaming

import (
	"sync"
	"testing"
)

// TestGenerator_NeverDuplicates pins the docstring contract:
// "never returns the same ID twice." Earlier code masked the
// counter to 16 bits and wrapped at 65 536 IDs/ms back to zero,
// re-issuing every prior ID in the millisecond. A burst-publish
// scenario (e.g. fan-out spike, tight test loop, accidental
// hot-loop in operator code) could trip the wrap silently.
//
// 70 000 calls forced into the same millisecond exposes 4 464
// duplicates with the buggy mask; with the fix, every ID is
// unique because the generator advances the synthetic millis
// instead of wrapping the counter.
func TestGenerator_NeverDuplicates(t *testing.T) {
	t.Parallel()

	const N = 70_000
	g := &Generator{}
	// Force every CAS into the "same-millis" branch by pre-seeding
	// state with a future millis — wall-clock now() will be smaller
	// for the lifetime of this test, so every Next() falls into the
	// counter-bump or counter-overflow path.
	const futureMs = uint64(1<<47 - 1) // far enough that time.Now never beats it
	g.state.Store(futureMs << 16)

	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id := g.Next()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at iteration %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerator_StrictlyIncreasing — the wire contract is
// "lexicographic sort = chronological". A regression that
// emitted an ID lower than a previous one would break
// snapshotAfter's `id > lastEventID` filter and the client's
// Last-Event-ID resume logic.
func TestGenerator_StrictlyIncreasing(t *testing.T) {
	t.Parallel()

	const N = 100_000
	g := &Generator{}
	const futureMs = uint64(1<<47 - 1)
	g.state.Store(futureMs << 16)

	prev := ""
	for i := 0; i < N; i++ {
		id := g.Next()
		if i > 0 && id <= prev {
			t.Fatalf("ID at iteration %d (%s) is not strictly greater than previous (%s)", i, id, prev)
		}
		prev = id
	}
}

// TestGenerator_ConcurrentNoDuplicates — under concurrent calls
// from many goroutines, every ID is still unique. The atomic-CAS
// loop is the contract that makes this safe; this test pins it.
func TestGenerator_ConcurrentNoDuplicates(t *testing.T) {
	t.Parallel()

	const goroutines = 50
	const perGoroutine = 2_000
	const total = goroutines * perGoroutine

	g := &Generator{}
	results := make(chan string, total)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				results <- g.Next()
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, total)
	for id := range results {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID under concurrency: %s", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != total {
		t.Errorf("got %d unique IDs, want %d", len(seen), total)
	}
}
