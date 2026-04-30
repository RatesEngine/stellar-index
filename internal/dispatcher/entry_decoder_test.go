package dispatcher

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// fakeEntryDecoder is the test-only [LedgerEntryChangeDecoder].
// Filters by entry-data type so we can route AccountEntry vs
// ContractCode separately in the routing tests.
type fakeEntryDecoder struct {
	name        string
	matchType   xdr.LedgerEntryType
	decodeFn    func(LedgerEntryChangeContext) ([]consumer.Event, error)
	matchCount  int
	decodeCount int
}

func (f *fakeEntryDecoder) Name() string { return f.name }

func (f *fakeEntryDecoder) Matches(change xdr.LedgerEntryChange) bool {
	f.matchCount++
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return change.Created != nil && change.Created.Data.Type == f.matchType
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return change.Updated != nil && change.Updated.Data.Type == f.matchType
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return change.Restored != nil && change.Restored.Data.Type == f.matchType
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return change.Removed != nil && change.Removed.Type == f.matchType
	}
	return false
}

func (f *fakeEntryDecoder) Decode(ctx LedgerEntryChangeContext) ([]consumer.Event, error) {
	f.decodeCount++
	if f.decodeFn == nil {
		return nil, nil
	}
	return f.decodeFn(ctx)
}

// makeAccountChange builds a minimal Created LedgerEntryChange for
// an AccountEntry with the given balance — enough surface for the
// routing tests to verify type-discriminator filtering.
func makeAccountChange(balance int64) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeAccount,
				Account: &xdr.AccountEntry{
					Balance: xdr.Int64(balance),
				},
			},
		},
	}
}

// makeContractCodeChange builds a minimal Created LedgerEntryChange
// for a ContractCode entry — used to verify decoders that match on
// AccountEntry don't accidentally fire on contract-code changes.
func makeContractCodeChange() xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type:         xdr.LedgerEntryTypeContractCode,
				ContractCode: &xdr.ContractCodeEntry{},
			},
		},
	}
}

// TestEntryDispatch_RoutesByType — a decoder watching AccountEntry
// fires on AccountEntry changes and skips ContractCode.
func TestEntryDispatch_RoutesByType(t *testing.T) {
	called := 0
	dec := &fakeEntryDecoder{
		name:      "accounts",
		matchType: xdr.LedgerEntryTypeAccount,
		decodeFn: func(_ LedgerEntryChangeContext) ([]consumer.Event, error) {
			called++
			return nil, nil
		},
	}
	disp := New()
	disp.AddEntryDecoder(dec)

	// AccountEntry change — should fire.
	if _, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Ledger:   1,
		ClosedAt: time.Now().UTC(),
		TxHash:   "abc",
		OpIndex:  0,
		Change:   makeAccountChange(1_000_000),
	}); err != nil {
		t.Fatalf("RouteEntryChange (AccountEntry): %v", err)
	}
	if called != 1 {
		t.Errorf("AccountEntry: decoder called %d times, want 1", called)
	}

	// ContractCode change — should NOT fire.
	if _, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Ledger:   1,
		ClosedAt: time.Now().UTC(),
		TxHash:   "abc",
		OpIndex:  1,
		Change:   makeContractCodeChange(),
	}); err != nil {
		t.Fatalf("RouteEntryChange (ContractCode): %v", err)
	}
	if called != 1 {
		t.Errorf("ContractCode: decoder fired but shouldn't have (called %d times)", called)
	}
}

// TestEntryDispatch_FirstMatchWins — when two decoders both match,
// only the first one's Decode runs. Same first-match-wins contract
// as the other three hooks.
func TestEntryDispatch_FirstMatchWins(t *testing.T) {
	first := &fakeEntryDecoder{
		name:      "first",
		matchType: xdr.LedgerEntryTypeAccount,
		decodeFn: func(_ LedgerEntryChangeContext) ([]consumer.Event, error) {
			return nil, nil
		},
	}
	second := &fakeEntryDecoder{
		name:      "second",
		matchType: xdr.LedgerEntryTypeAccount,
	}
	disp := New()
	disp.AddEntryDecoder(first)
	disp.AddEntryDecoder(second)

	if _, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Change: makeAccountChange(100),
	}); err != nil {
		t.Fatal(err)
	}
	if first.decodeCount != 1 {
		t.Errorf("first decoder: decodeCount=%d, want 1", first.decodeCount)
	}
	if second.decodeCount != 0 {
		t.Errorf("second decoder: decodeCount=%d, want 0 (first matched)", second.decodeCount)
	}
}

// TestEntryDispatch_DecodeErrorCountedPerSource — non-fatal-error
// contract: a decoder error is a "skip + count" signal. The
// dispatcher's decodeErrors counter increments by source.
func TestEntryDispatch_DecodeErrorCountedPerSource(t *testing.T) {
	boom := errors.New("entry decoder explosion")
	dec := &fakeEntryDecoder{
		name:      "boom",
		matchType: xdr.LedgerEntryTypeAccount,
		decodeFn: func(_ LedgerEntryChangeContext) ([]consumer.Event, error) {
			return nil, boom
		},
	}
	disp := New()
	disp.AddEntryDecoder(dec)

	_, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Change: makeAccountChange(100),
	})
	if !errors.Is(err, boom) {
		t.Errorf("got err=%v, want %v", err, boom)
	}
	if got := disp.Stats().DecodeErrors["boom"]; got != 1 {
		t.Errorf("DecodeErrors[boom]=%d, want 1", got)
	}
}

// TestEntryDispatch_NoDecoderRegistered — a dispatcher with no
// entry decoders silently drops every change. No error, no
// outputs, no counter bump (per ADR-0021 entry changes are
// high-volume so we don't count unmatched).
func TestEntryDispatch_NoDecoderRegistered(t *testing.T) {
	disp := New()
	outs, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Change: makeAccountChange(100),
	})
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs, want 0", len(outs))
	}
	if disp.Stats().UnmatchedHits != 0 {
		t.Errorf("UnmatchedHits bumped (%d) — entry changes shouldn't count toward unmatched",
			disp.Stats().UnmatchedHits)
	}
}

// TestEntryDispatch_OutputsReturned — Decode's emitted events flow
// back through RouteEntryChange to the caller.
func TestEntryDispatch_OutputsReturned(t *testing.T) {
	dec := &fakeEntryDecoder{
		name:      "emits",
		matchType: xdr.LedgerEntryTypeAccount,
		decodeFn: func(_ LedgerEntryChangeContext) ([]consumer.Event, error) {
			return []consumer.Event{fakeEvent{source: "emits", kind: "balance_obs"}}, nil
		},
	}
	disp := New()
	disp.AddEntryDecoder(dec)

	outs, err := disp.RouteEntryChange(LedgerEntryChangeContext{
		Change: makeAccountChange(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	if outs[0].Source() != "emits" {
		t.Errorf("output source=%q, want emits", outs[0].Source())
	}
}
