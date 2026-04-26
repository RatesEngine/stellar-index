package redstone

import (
	"math/big"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder.Decode wraps decodeWritePrices and threads its updates
// through the consumer.Event boundary. The interesting edges:
// happy-path emission, fallback to time.Now() on a malformed
// LedgerClosedAt, and propagation of decode errors.

func TestDecoder_Decode_happyPath(t *testing.T) {
	const pkgTs, wrTs = uint64(1_745_000_000_000), uint64(1_745_000_060_000)
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(oneBTCAt8), big.NewInt(oneETHAt8)},
		pkgTs, wrTs)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC", "ETH"}),
		encodePayloadArg(t),
	}

	ev := events.Event{
		Topic:          []string{TopicSymbolRedstone},
		Value:          body,
		OpArgs:         args,
		ContractID:     adapterC,
		Ledger:         52_000_000,
		TxHash:         "abcd",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}

	out, err := NewDecoder(adapterC).Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d events, want 2", len(out))
	}
	for i, e := range out {
		ue, ok := e.(UpdateEvent)
		if !ok {
			t.Fatalf("out[%d] not UpdateEvent: %T", i, e)
		}
		if ue.Update.Observer != relayerG {
			t.Errorf("out[%d].Observer = %q, want %q", i, ue.Update.Observer, relayerG)
		}
	}
}

func TestDecoder_Decode_malformedLedgerClosedAtFallsBackToNow(t *testing.T) {
	// LedgerClosedAt empty/invalid → EventClosedAt errors → adapter
	// substitutes time.Now() rather than dropping the event.
	// decodeWritePrices then prefers PackageTimestamp from each
	// PriceData anyway, so the substitution is invisible downstream.
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(oneBTCAt8)}, 1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC"}),
		encodePayloadArg(t),
	}
	ev := events.Event{
		Topic:          []string{TopicSymbolRedstone},
		Value:          body,
		OpArgs:         args,
		ContractID:     adapterC,
		LedgerClosedAt: "", // triggers the fallback branch
	}

	out, err := NewDecoder(adapterC).Decode(ev)
	if err != nil {
		t.Fatalf("Decode with empty LedgerClosedAt should not error, got %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
}

func TestDecoder_Decode_propagatesDecodeError(t *testing.T) {
	// 2 prices but 1 feed id — decodeWritePrices returns
	// ErrFeedIDCountMismatch. Decode must surface the error rather
	// than drop the event silently.
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(oneBTCAt8), big.NewInt(oneETHAt8)}, 1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC"}),
		encodePayloadArg(t),
	}
	ev := events.Event{
		Topic:          []string{TopicSymbolRedstone},
		Value:          body,
		OpArgs:         args,
		ContractID:     adapterC,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}

	if _, err := NewDecoder(adapterC).Decode(ev); err == nil {
		t.Error("expected error from feed-id count mismatch, got nil")
	}
}
