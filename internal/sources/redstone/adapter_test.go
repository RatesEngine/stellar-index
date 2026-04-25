package redstone

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestUpdateEvent_implementsConsumerEvent(t *testing.T) {
	ue := UpdateEvent{}
	if got := ue.EventKind(); got != "redstone.update" {
		t.Errorf("EventKind() = %q, want \"redstone.update\"", got)
	}
	if got := ue.Source(); got != SourceName {
		t.Errorf("Source() = %q, want %q", got, SourceName)
	}
	var _ consumer.Event = ue
}

// ─── dispatcher_adapter.go ────────────────────────────────────────

func TestDecoder_Name(t *testing.T) {
	if got := NewDecoder(adapterC).Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestDecoder_Matches(t *testing.T) {
	d := NewDecoder(adapterC)

	good := events.Event{
		Topic:      []string{TopicSymbolRedstone},
		ContractID: adapterC,
	}
	if !d.Matches(good) {
		t.Error("Matches(REDSTONE event from adapter) = false, want true")
	}

	wrongTopic := events.Event{
		Topic:      []string{"AAAACwAAAAhTT1JPU1dBUAAAAAA="},
		ContractID: adapterC,
	}
	if d.Matches(wrongTopic) {
		t.Error("Matches(non-REDSTONE topic) = true, want false")
	}

	wrongContract := events.Event{
		Topic:      []string{TopicSymbolRedstone},
		ContractID: "CWRONGADDRESS3333333333333333333333333333333333333333333",
	}
	if d.Matches(wrongContract) {
		t.Error("Matches(REDSTONE topic but wrong contract) = true, want false")
	}

	emptyTopic := events.Event{Topic: nil, ContractID: adapterC}
	if d.Matches(emptyTopic) {
		t.Error("Matches(empty topic) = true, want false")
	}
}
