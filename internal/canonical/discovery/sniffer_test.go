package discovery_test

import (
	"context"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical/discovery"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// validContractID is a real, valid C-strkey reused across tests.
const validContractID = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"

// makeEvent builds an events.Event whose topic[0] is the SCVal
// encoding of `symbol`. Other fields default to populated stubs so
// the sniffer's preconditions pass.
func makeEvent(t *testing.T, symbol, contractID string) events.Event {
	t.Helper()
	topic0 := scval.MustEncodeSymbol(symbol)
	return events.Event{
		Type:           "contract",
		ContractID:     contractID,
		Ledger:         50_000_000,
		LedgerClosedAt: "2026-04-28T12:00:00Z",
		Topic:          []string{topic0, "ignored", "ignored"},
		Value:          "AAAAAA==", // empty body — sniffer doesn't read it
	}
}

// TestSniff_RecognisesAllFourSEP41Events — happy path: each of the
// four SEP-41 event topics maps to its enum value.
func TestSniff_RecognisesAllFourSEP41Events(t *testing.T) {
	tests := []struct {
		symbol string
		want   discovery.SEP41EventType
	}{
		{"transfer", discovery.EventTransfer},
		{"mint", discovery.EventMint},
		{"burn", discovery.EventBurn},
		{"clawback", discovery.EventClawback},
	}
	for _, tc := range tests {
		t.Run(tc.symbol, func(t *testing.T) {
			ev := makeEvent(t, tc.symbol, validContractID)
			hit, ok := discovery.Sniff(ev)
			if !ok {
				t.Fatalf("Sniff returned ok=false for %q", tc.symbol)
			}
			if hit.EventType != tc.want {
				t.Errorf("EventType = %q, want %q", hit.EventType, tc.want)
			}
			if hit.ContractID != validContractID {
				t.Errorf("ContractID = %q, want %q", hit.ContractID, validContractID)
			}
			if hit.Ledger != 50_000_000 {
				t.Errorf("Ledger = %d, want 50_000_000", hit.Ledger)
			}
			if hit.ObservedAtRFC3339 != "2026-04-28T12:00:00Z" {
				t.Errorf("ObservedAtRFC3339 = %q", hit.ObservedAtRFC3339)
			}
		})
	}
}

// TestSniff_RejectsNonSEP41Symbols — DEX-specific symbols (swap,
// sync, deposit, …) and arbitrary unknown symbols MUST NOT trip the
// sniffer. Without this guard discovery would record every
// contract-event-emitting contract on pubnet.
func TestSniff_RejectsNonSEP41Symbols(t *testing.T) {
	for _, sym := range []string{"swap", "sync", "deposit", "withdraw", "rebalance", "REDSTONE", "POOL"} {
		t.Run(sym, func(t *testing.T) {
			ev := makeEvent(t, sym, validContractID)
			if _, ok := discovery.Sniff(ev); ok {
				t.Errorf("Sniff returned ok=true for non-SEP-41 symbol %q", sym)
			}
		})
	}
}

// TestSniff_RejectsNonContractEvents — system + diagnostic events
// have the wrong wire shape; skip them at the type check before
// SCVal parsing.
func TestSniff_RejectsNonContractEvents(t *testing.T) {
	for _, eventType := range []string{"system", "diagnostic", ""} {
		t.Run("type="+eventType, func(t *testing.T) {
			ev := makeEvent(t, "transfer", validContractID)
			ev.Type = eventType
			if _, ok := discovery.Sniff(ev); ok {
				t.Errorf("Sniff accepted Type=%q event", eventType)
			}
		})
	}
}

// TestSniff_RejectsEmptyContractID — defensive: an event without a
// contract id is malformed; surface as ok=false so the recorder
// never sees an empty key.
func TestSniff_RejectsEmptyContractID(t *testing.T) {
	ev := makeEvent(t, "transfer", validContractID)
	ev.ContractID = ""
	if _, ok := discovery.Sniff(ev); ok {
		t.Error("Sniff accepted event with empty ContractID")
	}
}

// TestSniff_RejectsEmptyTopic — no topic = nothing to classify; the
// sniffer must not nil-index Topic[0].
func TestSniff_RejectsEmptyTopic(t *testing.T) {
	ev := makeEvent(t, "transfer", validContractID)
	ev.Topic = nil
	if _, ok := discovery.Sniff(ev); ok {
		t.Error("Sniff accepted event with nil Topic")
	}
	ev.Topic = []string{}
	if _, ok := discovery.Sniff(ev); ok {
		t.Error("Sniff accepted event with empty Topic")
	}
}

// TestSniff_RejectsMalformedTopic0 — a topic[0] that doesn't decode
// as SCVal symbol falls through with ok=false.
func TestSniff_RejectsMalformedTopic0(t *testing.T) {
	ev := makeEvent(t, "transfer", validContractID)
	ev.Topic[0] = "not-base64!!!"
	if _, ok := discovery.Sniff(ev); ok {
		t.Error("Sniff accepted event with unparseable topic[0]")
	}
}

// TestInMemoryRecorder_RoundTrip — Record then IsKnown returns true;
// a never-seen contract returns false.
func TestInMemoryRecorder_RoundTrip(t *testing.T) {
	r := discovery.NewInMemoryRecorder()
	ctx := context.Background()

	// Unknown contract first.
	known, err := r.IsKnown(ctx, validContractID)
	if err != nil {
		t.Fatalf("IsKnown: %v", err)
	}
	if known {
		t.Error("IsKnown=true for never-recorded contract")
	}

	hit := discovery.Hit{
		ContractID:        validContractID,
		EventType:         discovery.EventTransfer,
		Ledger:            50_000_000,
		ObservedAtRFC3339: "2026-04-28T12:00:00Z",
	}
	if err := r.Record(ctx, hit); err != nil {
		t.Fatalf("Record: %v", err)
	}

	known, _ = r.IsKnown(ctx, validContractID)
	if !known {
		t.Error("IsKnown=false after Record")
	}
}

// TestInMemoryRecorder_FirstWriteWinsOnMetadata — the first Record
// for a contract preserves first_seen_event + first_seen_ledger;
// subsequent Records only bump the counter. Mirrors the Postgres
// ON CONFLICT DO UPDATE pattern.
func TestInMemoryRecorder_FirstWriteWinsOnMetadata(t *testing.T) {
	r := discovery.NewInMemoryRecorder()
	ctx := context.Background()

	first := discovery.Hit{
		ContractID:        validContractID,
		EventType:         discovery.EventMint,
		Ledger:            10_000_000,
		ObservedAtRFC3339: "2026-01-01T00:00:00Z",
	}
	second := discovery.Hit{
		ContractID:        validContractID,
		EventType:         discovery.EventTransfer, // different event
		Ledger:            20_000_000,              // later ledger
		ObservedAtRFC3339: "2026-04-28T12:00:00Z",
	}
	if err := r.Record(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, second); err != nil {
		t.Fatal(err)
	}

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot returned %d entries, want 1 (de-duped on contract id)", len(snap))
	}
	if snap[0].EventType != discovery.EventMint {
		t.Errorf("EventType = %q, want %q (first-write-wins)", snap[0].EventType, discovery.EventMint)
	}
	if snap[0].Ledger != 10_000_000 {
		t.Errorf("Ledger = %d, want 10_000_000 (first-write-wins)", snap[0].Ledger)
	}
	if got := r.Count(validContractID); got != 2 {
		t.Errorf("Count = %d, want 2 (both Records counted)", got)
	}
}

// TestInMemoryRecorder_RejectsEmptyContractID — guard against a
// malformed Hit reaching the recorder (would otherwise allocate an
// "" key in the map).
func TestInMemoryRecorder_RejectsEmptyContractID(t *testing.T) {
	r := discovery.NewInMemoryRecorder()
	if err := r.Record(context.Background(), discovery.Hit{}); err == nil {
		t.Error("expected error on empty-ContractID Hit; got nil")
	}
}
