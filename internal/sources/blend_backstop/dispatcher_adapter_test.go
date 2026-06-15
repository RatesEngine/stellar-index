package blend_backstop

import (
	"testing"

	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/events"
)

func TestDecoder_Name(t *testing.T) {
	t.Parallel()
	if got := (&Decoder{}).Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestIsBackstopContract(t *testing.T) {
	t.Parallel()
	for _, id := range []string{MainnetBackstopV2, MainnetBackstopV1} {
		if !IsBackstopContract(id) {
			t.Errorf("IsBackstopContract(%q) = false, want true", id)
		}
	}
	if IsBackstopContract(contractStrkey(t, 0xEE)) {
		t.Error("IsBackstopContract on a foreign contract = true, want false")
	}
}

func TestDecoder_Matches(t *testing.T) {
	t.Parallel()
	d := NewDecoder()

	t.Run("backstop symbol from backstop contract", func(t *testing.T) {
		t.Parallel()
		ev := goldenEvent(t, "deposit")
		ev.ContractID = MainnetBackstopV2
		if !d.Matches(*ev) {
			t.Error("want Matches=true for a deposit from the V2 backstop")
		}
	})

	t.Run("V1 backstop also matches", func(t *testing.T) {
		t.Parallel()
		ev := goldenEvent(t, "claim")
		ev.ContractID = MainnetBackstopV1
		if !d.Matches(*ev) {
			t.Error("want Matches=true for a claim from the V1 backstop")
		}
	})

	t.Run("backstop symbol from NON-backstop contract", func(t *testing.T) {
		t.Parallel()
		// `claim` overlaps with Blend pool symbols — a foreign emitter
		// must be rejected by the contract gate, never minted.
		ev := goldenEvent(t, "claim")
		ev.ContractID = contractStrkey(t, 0xEE)
		if d.Matches(*ev) {
			t.Error("want Matches=false for a backstop symbol from a non-backstop contract")
		}
	})

	t.Run("non-backstop symbol from backstop contract", func(t *testing.T) {
		t.Parallel()
		ev := events.Event{
			ContractID: MainnetBackstopV2,
			Topic:      []string{b64SV(t, symbolSV("transfer"))},
		}
		if d.Matches(ev) {
			t.Error("want Matches=false for an unrecognised topic")
		}
	})
}

func TestDecoder_Decode_Deposit(t *testing.T) {
	t.Parallel()
	ev := goldenEvent(t, "deposit")
	out, err := NewDecoder().Decode(*ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	got, ok := out[0].(Event)
	if !ok {
		t.Fatalf("emitted event is %T, want blend_backstop.Event", out[0])
	}
	if got.EventType != EventDeposit {
		t.Errorf("EventType = %q, want %q", got.EventType, EventDeposit)
	}
	if got.Amount != "179414602" || got.Amount2 != "131015685" {
		t.Errorf("Amount/Amount2 = %q/%q, want 179414602/131015685", got.Amount, got.Amount2)
	}
	if got.Pool == "" || got.UserAddress == "" {
		t.Error("deposit should carry pool + user")
	}
	var _ consumer.Event = got
	if got.Source() != SourceName {
		t.Errorf("Source() = %q, want %q", got.Source(), SourceName)
	}
}

func TestDecoder_Decode_NonBackstopContract(t *testing.T) {
	t.Parallel()
	// A genuine backstop-shaped event from a foreign contract: Decode
	// returns nothing rather than minting a row.
	ev := goldenEvent(t, "deposit")
	ev.ContractID = contractStrkey(t, 0xEE)
	out, err := NewDecoder().Decode(*ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Decode emitted %d events for a foreign contract, want 0", len(out))
	}
}

func TestDecoder_Decode_EmptyClosedAt(t *testing.T) {
	t.Parallel()
	ev := goldenEvent(t, "deposit")
	ev.LedgerClosedAt = "" // EventClosedAt fails closed
	if _, err := NewDecoder().Decode(*ev); err == nil {
		t.Fatal("want an error when LedgerClosedAt is empty")
	}
}
