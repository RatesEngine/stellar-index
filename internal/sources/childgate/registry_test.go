package childgate

import (
	"sync"
	"testing"
)

func TestRegistry_HasSeed(t *testing.T) {
	r := New()
	if r.Has("CCHILD") {
		t.Fatal("fresh registry should not contain CCHILD")
	}
	r.Seed("CCHILD", "CFACTORY", 100)
	if !r.Has("CCHILD") {
		t.Fatal("Seed did not register CCHILD")
	}
	if r.Has("COTHER") {
		t.Fatal("unrelated contract should not be registered")
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
	// Idempotent.
	r.Seed("CCHILD", "CFACTORY", 200)
	if got := r.Len(); got != 1 {
		t.Fatalf("Len after re-seed = %d, want 1", got)
	}
}

func TestRegistry_Factories(t *testing.T) {
	r := New(WithFactories([]string{"CFAC1", "CFAC2", ""})) // empty skipped
	if !r.IsFactory("CFAC1") || !r.IsFactory("CFAC2") {
		t.Fatal("both factories should be recognized")
	}
	if r.IsFactory("CFAC3") {
		t.Fatal("non-factory must not be recognized")
	}
	if r.IsFactory("") {
		t.Fatal("empty must not be a factory")
	}
	if len(r.Factories()) != 2 {
		t.Fatalf("Factories() len = %d, want 2 (empty skipped)", len(r.Factories()))
	}
	// A factory is NOT automatically a child (separate sets).
	if r.Has("CFAC1") {
		t.Fatal("factory should not be in the child set")
	}
}

func TestRegistry_WithSeed_doesNotFireHook(t *testing.T) {
	var hookCalls int
	r := New(
		WithSeed([]string{"CA", "CB", ""}), // empty skipped
		WithHook(func(string, string, uint32) { hookCalls++ }),
	)
	if !r.Has("CA") || !r.Has("CB") {
		t.Fatal("WithSeed did not load both contracts")
	}
	if r.Has("") {
		t.Fatal("empty contract id should be skipped")
	}
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (empty skipped)", r.Len())
	}
	if hookCalls != 0 {
		t.Fatalf("WithSeed fired the hook %d times, want 0 (warm IDs are already persisted)", hookCalls)
	}
}

func TestRegistry_Seed_firesHook(t *testing.T) {
	type call struct {
		id      string
		factory string
		ledger  uint32
	}
	var got []call
	r := New(WithHook(func(id, fac string, l uint32) { got = append(got, call{id, fac, l}) }))
	r.Seed("CCHILD", "CFACTORY", 42)
	r.Seed("", "CFACTORY", 7) // empty: no-op, no hook
	if len(got) != 1 || got[0] != (call{"CCHILD", "CFACTORY", 42}) {
		t.Fatalf("hook calls = %+v, want one {CCHILD CFACTORY 42}", got)
	}
}

func TestRegistry_concurrentSeedAndHas(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); r.Seed("CA", "CFAC", 1) }()
		go func() { defer wg.Done(); _ = r.Has("CA") }()
	}
	wg.Wait()
	if !r.Has("CA") {
		t.Fatal("CA should be registered after concurrent seeds")
	}
}
