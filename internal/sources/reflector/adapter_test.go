package reflector

import (
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────

func TestUpdateEvent_implementsConsumerEvent(t *testing.T) {
	ue := UpdateEvent{Update: canonical.OracleUpdate{Source: "reflector-dex"}}
	if got := ue.EventKind(); got != "reflector.update" {
		t.Errorf("EventKind() = %q, want \"reflector.update\"", got)
	}
	// Source is delegated to the contained Update.Source — exercises
	// the per-variant metric routing path (reflector-dex /
	// reflector-cex / reflector-fx).
	if got := ue.Source(); got != "reflector-dex" {
		t.Errorf("Source() = %q, want \"reflector-dex\"", got)
	}
	var _ consumer.Event = ue
}

// ─── dispatcher_adapter.go ────────────────────────────────────

const adapterContract = "CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN"

func TestDecoder_Name_perVariant(t *testing.T) {
	cases := []struct {
		variant Variant
		want    string
	}{
		{VariantDEX, "reflector-dex"},
		{VariantCEX, "reflector-cex"},
		{VariantFX, "reflector-fx"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			d := NewDecoder(tc.variant, adapterContract)
			if got := d.Name(); got != tc.want {
				t.Errorf("Name() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDecoder_Matches(t *testing.T) {
	d := NewDecoder(VariantDEX, adapterContract)

	good := events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "anything"},
		ContractID: adapterContract,
	}
	if !d.Matches(good) {
		t.Error("Matches(REFLECTOR:update from configured contract) = false, want true")
	}

	// Wrong contract — keeps DEX from picking up CEX events.
	wrongContract := events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "x"},
		ContractID: "CWRONG",
	}
	if d.Matches(wrongContract) {
		t.Error("Matches(wrong ContractID) = true, want false")
	}

	// Right contract but wrong topic.
	wrongTopic := events.Event{
		Topic:      []string{"AAAACwAAAAhTT1JPU1dBUAAAAAA="},
		ContractID: adapterContract,
	}
	if d.Matches(wrongTopic) {
		t.Error("Matches(wrong topic) = true, want false")
	}
}

func TestWithDecoderObserver_setsObserver(t *testing.T) {
	d := NewDecoder(VariantCEX, adapterContract,
		WithDecoderObserver("GRELAYER0000000000000000000000000000000000000000000000000"))
	if d.observer == "" {
		t.Error("WithDecoderObserver did not set observer field")
	}
}

func TestDecoder_Decode_emitsUpdatesForKnownSymbol(t *testing.T) {
	// Build a fixture with one fiat:USD entry — decodes via the
	// CEX/FX symbol path and surfaces as a single UpdateEvent.
	usd := xdr.ScSymbol("USD")
	symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &usd}
	body := encodeUpdateBody(t, []xdr.ScVal{symSv}, []*big.Int{big.NewInt(100_000_000_000_000)})
	tsB64 := encodeTimestampTopic(t, 1_745_000_000_000)

	d := NewDecoder(VariantCEX, adapterContract,
		WithDecoderObserver("GRELAYER0000000000000000000000000000000000000000000000000"))
	out, err := d.Decode(events.Event{
		Topic:          []string{TopicSymbolReflector, TopicSymbolUpdate, tsB64},
		Value:          body,
		ContractID:     adapterContract,
		Ledger:         52_000_000,
		TxHash:         "abc",
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	ue, ok := out[0].(UpdateEvent)
	if !ok {
		t.Fatalf("expected UpdateEvent, got %T", out[0])
	}
	if ue.Update.Source != "reflector-cex" {
		t.Errorf("Update.Source = %q, want \"reflector-cex\"", ue.Update.Source)
	}
}

func TestDecoder_Decode_malformedClosedAtFailsClosed(t *testing.T) {
	// LedgerClosedAt empty — decoder FAILS CLOSED (returns the error)
	// rather than substituting time.Now(). closedAt is the fallback
	// decodeUpdate uses when topic[2] is missing / out of its sanity
	// window, so a wall-clock value here would mis-timestamp the row
	// during a backfill replay. Matches the comet/blend/phoenix
	// siblings.
	usd := xdr.ScSymbol("USD")
	symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &usd}
	body := encodeUpdateBody(t, []xdr.ScVal{symSv}, []*big.Int{big.NewInt(1_000_000_000_000)})
	tsB64 := encodeTimestampTopic(t, 1_745_000_000_000)

	d := NewDecoder(VariantCEX, adapterContract)
	out, err := d.Decode(events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, tsB64},
		Value:      body,
		ContractID: adapterContract,
		// LedgerClosedAt deliberately empty.
	})
	if err == nil {
		t.Fatalf("Decode with empty LedgerClosedAt should error, got nil (out=%v)", out)
	}
	if out != nil {
		t.Errorf("expected nil events on error, got %v", out)
	}
}

func TestDecoder_Decode_topicTimestampWinsOverClosedAt(t *testing.T) {
	// With a present LedgerClosedAt, the in-window topic[2] oracle
	// timestamp is what lands on the OracleUpdate (the ledger close
	// time is only a fallback when topic[2] is missing / out of the
	// sanity window).
	usd := xdr.ScSymbol("USD")
	symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &usd}
	body := encodeUpdateBody(t, []xdr.ScVal{symSv}, []*big.Int{big.NewInt(1_000_000_000_000)})
	const tsMs = 1_745_000_000_000
	tsB64 := encodeTimestampTopic(t, tsMs)

	d := NewDecoder(VariantCEX, adapterContract)
	out, err := d.Decode(events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, tsB64},
		Value:      body,
		ContractID: adapterContract,
		// Ledger close a few seconds after the oracle stamp — within
		// the sanity window so topic[2] is accepted.
		LedgerClosedAt: time.UnixMilli(tsMs + 3000).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	ue := out[0].(UpdateEvent)
	if ue.Update.Timestamp.UnixMilli() != tsMs {
		t.Errorf("Timestamp = %v, want topic[2]'s ms value", ue.Update.Timestamp)
	}
}

func TestDecoder_Decode_propagatesDecodeError(t *testing.T) {
	// Body is non-base64 — sdkDecodeUpdateBody returns an error
	// that decodeUpdate wraps; Decode must surface it.
	d := NewDecoder(VariantDEX, adapterContract)
	_, err := d.Decode(events.Event{
		Topic:          []string{TopicSymbolReflector, TopicSymbolUpdate, "garbagets"},
		Value:          "not-base64",
		ContractID:     adapterContract,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	})
	if err == nil {
		t.Error("expected decode error, got nil")
	}
}
