package band

import (
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestUpdateEvent_implementsConsumerEvent(t *testing.T) {
	ue := UpdateEvent{}
	if got := ue.EventKind(); got != "band.update" {
		t.Errorf("EventKind() = %q, want \"band.update\"", got)
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

func TestDecoder_Decode_RoutesToDecodeRelayArgs(t *testing.T) {
	// End-to-end through the adapter: build a relay() call's args,
	// hand them to Decoder.Decode via a ContractCallContext, and
	// verify the resulting UpdateEvent slice carries the expected
	// observations. Effectively the same shape as decode_test.go's
	// TestDecodeRelay_HappyPath but exercises the adapter's
	// out-array packing.
	const resolveSec = uint64(1_745_000_000)
	const btcRateE9 = uint64(500_000_000_000_000)

	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{
			{"BTC", btcRateE9},
		}),
		encodeU64Arg(t, resolveSec),
		encodeU64Arg(t, 42),
	}
	ctx := dispatcher.ContractCallContext{
		Ledger:       52_000_000,
		ClosedAt:     time.Now().UTC(),
		TxHash:       "abcd",
		ContractID:   adapterC,
		FunctionName: FnRelay,
		Args:         args,
	}
	d := NewDecoder(adapterC)
	out, err := d.Decode(ctx)
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
	if ue.Update.Source != SourceName {
		t.Errorf("Update.Source = %q, want %q", ue.Update.Source, SourceName)
	}
}

func TestDecoder_Decode_MalformedArgsReturnsError(t *testing.T) {
	d := NewDecoder(adapterC)
	ctx := dispatcher.ContractCallContext{
		Ledger:       52_000_000,
		ClosedAt:     time.Now().UTC(),
		TxHash:       "abcd",
		ContractID:   adapterC,
		FunctionName: FnRelay,
		Args:         []string{"not-base64"}, // too few args + invalid encoding
	}
	if _, err := d.Decode(ctx); err == nil {
		t.Error("expected decode error on malformed args, got nil")
	}
}
