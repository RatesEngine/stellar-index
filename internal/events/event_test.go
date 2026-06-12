package events_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// TestEventClosedAt_RFC3339Parsing — the happy path: a well-formed
// RFC 3339 LedgerClosedAt round-trips to time.Time.
func TestEventClosedAt_RFC3339Parsing(t *testing.T) {
	ev := events.Event{
		ID:             "0001-001",
		LedgerClosedAt: "2026-05-12T14:30:00Z",
	}
	got, err := ev.EventClosedAt()
	if err != nil {
		t.Fatalf("EventClosedAt: %v", err)
	}
	want := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("EventClosedAt = %v, want %v", got, want)
	}
}

// TestEventClosedAt_EmptyStringErrors — an event with no
// LedgerClosedAt MUST surface an error rather than the zero time.
// Per the docstring: the zero time would break VWAP windows + time-
// ordered queries downstream.
func TestEventClosedAt_EmptyStringErrors(t *testing.T) {
	ev := events.Event{ID: "0002-002", LedgerClosedAt: ""}
	_, err := ev.EventClosedAt()
	if err == nil {
		t.Fatal("expected error on empty LedgerClosedAt")
	}
	if !strings.Contains(err.Error(), "empty ledgerClosedAt") {
		t.Errorf("error %q should mention 'empty ledgerClosedAt'", err.Error())
	}
	if !strings.Contains(err.Error(), "0002-002") {
		t.Errorf("error %q should carry the event ID", err.Error())
	}
}

// TestEventClosedAt_BadFormatErrors — a non-RFC-3339 string should
// fail loudly, not silently coerce.
func TestEventClosedAt_BadFormatErrors(t *testing.T) {
	ev := events.Event{ID: "0003-003", LedgerClosedAt: "12 May 2026"}
	_, err := ev.EventClosedAt()
	if err == nil {
		t.Fatal("expected error on non-RFC-3339 LedgerClosedAt")
	}
	if !strings.Contains(err.Error(), "12 May 2026") {
		t.Errorf("error %q should echo the bad input", err.Error())
	}
}

// TestEvent_JSONRoundTrip — the JSON tags are the wire contract
// for stellar-rpc fixture replays. Verify a typical event marshals
// and unmarshals losslessly, especially the omitempty on OpArgs
// (events from RPC don't carry op args; events from the dispatcher
// do).
func TestEvent_JSONRoundTrip(t *testing.T) {
	original := events.Event{
		Type:                     "contract",
		Ledger:                   52_000_000,
		LedgerClosedAt:           "2026-05-12T14:30:00Z",
		ContractID:               "CAW...",
		ID:                       "0000000000000000-0000000001",
		OperationIndex:           1,
		TransactionIndex:         2,
		TxHash:                   "deadbeef",
		InSuccessfulContractCall: true,
		Topic:                    []string{"AAA=", "BBB="},
		Value:                    "CCCC==",
		OpArgs:                   []string{"DDD=", "EEE="},
	}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got events.Event
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != original.ID || got.Ledger != original.Ledger {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if len(got.Topic) != 2 || got.Topic[0] != "AAA=" {
		t.Errorf("Topic round-trip: %v", got.Topic)
	}
	if len(got.OpArgs) != 2 || got.OpArgs[1] != "EEE=" {
		t.Errorf("OpArgs round-trip: %v", got.OpArgs)
	}
}

// TestEvent_OpArgsOmittedWhenEmpty — events from stellar-rpc
// don't carry OpArgs. The omitempty tag must keep the JSON shape
// stable for fixture replays.
func TestEvent_OpArgsOmittedWhenEmpty(t *testing.T) {
	ev := events.Event{
		Type:           "contract",
		Ledger:         42,
		LedgerClosedAt: "2026-05-12T14:30:00Z",
		ContractID:     "CABC",
		// OpArgs intentionally nil
	}
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(body), "opArgs") {
		t.Errorf("body contains opArgs when OpArgs is empty: %s", body)
	}
}

// TestEvent_OpArgsPresentWhenSet — symmetric to the omitempty
// test: when OpArgs is populated, it MUST appear in the JSON
// (Redstone + similar decoders need it).
func TestEvent_OpArgsPresentWhenSet(t *testing.T) {
	ev := events.Event{
		Type:           "contract",
		LedgerClosedAt: "2026-05-12T14:30:00Z",
		OpArgs:         []string{"feed_ids_blob_base64"},
	}
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(body), "opArgs") {
		t.Errorf("body must contain opArgs when populated: %s", body)
	}
}
