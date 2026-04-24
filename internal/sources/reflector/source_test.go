package reflector

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

const (
	reflectorTxHash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	dexContractID   = "CAFF00000000000000000000000000000000000000000000000000000"
	cexContractID   = "CBAR00000000000000000000000000000000000000000000000000000"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		topics []string
		want   bool
	}{
		{"match", []string{TopicSymbolReflector, TopicSymbolUpdate}, true},
		{"wrong topic0", []string{"elsewhere", TopicSymbolUpdate}, false},
		{"wrong topic1", []string{TopicSymbolReflector, "create"}, false},
		{"missing topic1", []string{TopicSymbolReflector}, false},
		{"empty topics", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(&stellarrpc.Event{Topic: tc.topics})
			if got != tc.want {
				t.Errorf("classify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVariantSourceName(t *testing.T) {
	cases := map[Variant]string{
		VariantDEX:  SourceDEX,
		VariantCEX:  SourceCEX,
		VariantFX:   SourceFX,
		Variant(99): "reflector-unknown",
	}
	for v, want := range cases {
		if got := v.SourceName(); got != want {
			t.Errorf("%d.SourceName() = %q, want %q", v, got, want)
		}
	}
}

func TestQuoteForVariant(t *testing.T) {
	// DEX → XLM native. CEX + FX → fiat:USD (ADR-0010).
	if q := quoteForVariant(VariantDEX); q.Type != canonical.AssetNative {
		t.Errorf("DEX quote = %+v, want native", q)
	}
	usd, _ := canonical.NewFiatAsset("USD")
	for _, v := range []Variant{VariantCEX, VariantFX} {
		if q := quoteForVariant(v); !q.Equal(usd) {
			t.Errorf("%s quote = %+v, want fiat:USD", v.SourceName(), q)
		}
	}
}

func TestDecodeUpdate_fanout(t *testing.T) {
	// Fake decoder returns three (asset, price) pairs — expect 3
	// canonical.OracleUpdate records back.
	prev, prevTS := decodeUpdateBody, decodeUpdateTimestamp
	defer func() { decodeUpdateBody, decodeUpdateTimestamp = prev, prevTS }()

	xlm := canonical.NativeAsset()
	usdcAsset, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")

	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		v := func(s string) canonical.Amount {
			n, _ := new(big.Int).SetString(s, 10)
			return canonical.NewAmount(n)
		}
		return []PriceEntry{
			{Asset: xlm, Price: v("1242000000000000")},      // 0.1242 USD (at 14 decimals) — in DEX it's XLM/XLM which doesn't make sense but fine for the test
			{Asset: usdcAsset, Price: v("100000000000000")}, // 1 USDC
			{Asset: xlm, Price: v("1243000000000000")},      // duplicate asset — different OpIndex
		}, nil
	}
	// Reflector event timestamps are u64 milliseconds.
	// 1745000000000 ms = 2025-04-18T17:33:20Z.
	decodeUpdateTimestamp = func(_ string) (uint64, error) { return 1745000000000, nil }

	e := &stellarrpc.Event{
		Topic:          []string{TopicSymbolReflector, TopicSymbolUpdate, "ts-placeholder"},
		ContractID:     cexContractID,
		Ledger:         52_430_001,
		TxHash:         reflectorTxHash,
		OperationIndex: 0,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)

	updates, err := decodeUpdate(e, VariantCEX, DefaultDecimals, "GRELAYER", closedAt)
	if err != nil {
		t.Fatalf("decodeUpdate: %v", err)
	}
	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}

	// Source name stamped per variant.
	for _, u := range updates {
		if u.Source != SourceCEX {
			t.Errorf("Source = %q, want %q", u.Source, SourceCEX)
		}
		if u.Observer != "GRELAYER" {
			t.Errorf("Observer = %q", u.Observer)
		}
		if u.Decimals != DefaultDecimals {
			t.Errorf("Decimals = %d", u.Decimals)
		}
		if u.ContractID != cexContractID {
			t.Errorf("ContractID = %q", u.ContractID)
		}
	}

	// Timestamp sourced from the oracle's own timestamp (not ledger close).
	// Event carries 1745000000000 ms → 1745000000 seconds unix.
	if updates[0].Timestamp.UnixMilli() != 1745000000000 {
		t.Errorf("timestamp wrong: %v (%d ms)", updates[0].Timestamp, updates[0].Timestamp.UnixMilli())
	}

	// Each update has a distinct OpIndex to keep identity unique.
	seenOps := map[uint32]bool{}
	for _, u := range updates {
		if seenOps[u.OpIndex] {
			t.Errorf("duplicate OpIndex %d", u.OpIndex)
		}
		seenOps[u.OpIndex] = true
	}

	// Quote is fiat:USD for CEX.
	usd, _ := canonical.NewFiatAsset("USD")
	for _, u := range updates {
		if !u.Quote.Equal(usd) {
			t.Errorf("Quote = %+v, want fiat:USD", u.Quote)
		}
	}
}

func TestDecodeUpdate_OpIndexStrideIsFixed(t *testing.T) {
	// Regression: previously OpIndex used `OperationIndex × len(prices)
	// + i`, which could collide across events in the same tx with
	// different vector sizes. With a fixed stride (opIndexFanoutStride),
	// the op_index ranges never overlap.
	prev, prevTS := decodeUpdateBody, decodeUpdateTimestamp
	defer func() { decodeUpdateBody, decodeUpdateTimestamp = prev, prevTS }()
	decodeUpdateTimestamp = func(_ string) (uint64, error) { return 0, nil }

	xlm := canonical.NativeAsset()
	usdc, _ := canonical.NewFiatAsset("USD")
	v := func(s string) canonical.Amount {
		n, _ := new(big.Int).SetString(s, 10)
		return canonical.NewAmount(n)
	}

	// Event A: op_index=0 with 3 prices.
	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		return []PriceEntry{
			{Asset: xlm, Price: v("100")},
			{Asset: usdc, Price: v("100")},
			{Asset: xlm, Price: v("100")},
		}, nil
	}
	eA := &stellarrpc.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "ts"},
		ContractID: dexContractID,
		Ledger:     1, TxHash: reflectorTxHash, OperationIndex: 0,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
	}
	closedAt, _ := time.Parse(time.RFC3339, eA.LedgerClosedAt)
	updatesA, err := decodeUpdate(eA, VariantDEX, DefaultDecimals, "", closedAt)
	if err != nil {
		t.Fatal(err)
	}

	// Event B: op_index=1 with 2 prices — if the old formula was
	// still in place, B's first slot would be OpIndex=2 which
	// collides with A's third slot at OpIndex=2 (0×3+2).
	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		return []PriceEntry{
			{Asset: xlm, Price: v("100")},
			{Asset: usdc, Price: v("100")},
		}, nil
	}
	eB := &stellarrpc.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "ts"},
		ContractID: dexContractID,
		Ledger:     1, TxHash: reflectorTxHash, OperationIndex: 1,
		LedgerClosedAt: eA.LedgerClosedAt,
	}
	updatesB, err := decodeUpdate(eB, VariantDEX, DefaultDecimals, "", closedAt)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[uint32]bool{}
	for _, u := range updatesA {
		if seen[u.OpIndex] {
			t.Errorf("duplicate OpIndex in A: %d", u.OpIndex)
		}
		seen[u.OpIndex] = true
	}
	for _, u := range updatesB {
		if seen[u.OpIndex] {
			t.Errorf("cross-event OpIndex collision: %d in both A and B", u.OpIndex)
		}
		seen[u.OpIndex] = true
	}
	if len(seen) != len(updatesA)+len(updatesB) {
		t.Errorf("op_index uniqueness violated: %d unique across %d total",
			len(seen), len(updatesA)+len(updatesB))
	}
}

func TestDecodeUpdate_skipsZeroPrices(t *testing.T) {
	prev, prevTS := decodeUpdateBody, decodeUpdateTimestamp
	defer func() { decodeUpdateBody, decodeUpdateTimestamp = prev, prevTS }()
	decodeUpdateTimestamp = func(_ string) (uint64, error) { return 0, nil }

	xlm := canonical.NativeAsset()
	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		return []PriceEntry{
			{Asset: xlm, Price: canonical.NewAmount(big.NewInt(0))},
			{Asset: xlm, Price: canonical.NewAmount(big.NewInt(100))},
		}, nil
	}

	e := &stellarrpc.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "ts"},
		ContractID: dexContractID,
		Ledger:     1, TxHash: reflectorTxHash, OperationIndex: 0,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)
	updates, err := decodeUpdate(e, VariantDEX, DefaultDecimals, "", closedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 (zero-price skipped), got %d", len(updates))
	}
}

func TestDecodeUpdate_refusesWrongTopic(t *testing.T) {
	e := &stellarrpc.Event{Topic: []string{"wrong", "update"}}
	_, err := decodeUpdate(e, VariantDEX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrNotReflectorEvent) {
		t.Errorf("expected ErrNotReflectorEvent, got %v", err)
	}
}

func TestDecodeUpdate_emptyPricesError(t *testing.T) {
	prev, prevTS := decodeUpdateBody, decodeUpdateTimestamp
	defer func() { decodeUpdateBody, decodeUpdateTimestamp = prev, prevTS }()
	decodeUpdateTimestamp = func(_ string) (uint64, error) { return 0, nil }
	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		return []PriceEntry{}, nil
	}

	e := &stellarrpc.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "ts"},
		ContractID: dexContractID,
	}
	_, err := decodeUpdate(e, VariantDEX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrEmptyPrices) {
		t.Errorf("expected ErrEmptyPrices, got %v", err)
	}
}

func TestDecodeUpdate_rejectsPriceVectorLargerThanStride(t *testing.T) {
	// Safety: a prices vector bigger than opIndexFanoutStride would
	// overflow the fanned-out OpIndex range into the next operation's
	// synthetic slot and cause PK collisions in oracle_updates.
	// Refuse loudly with ErrPriceVectorOverflow instead.
	prev, prevTS := decodeUpdateBody, decodeUpdateTimestamp
	defer func() { decodeUpdateBody, decodeUpdateTimestamp = prev, prevTS }()
	decodeUpdateTimestamp = func(_ string) (uint64, error) { return 0, nil }

	xlm := canonical.NativeAsset()
	oversized := make([]PriceEntry, opIndexFanoutStride+1)
	price := canonical.NewAmount(big.NewInt(1))
	for i := range oversized {
		oversized[i] = PriceEntry{Asset: xlm, Price: price}
	}
	decodeUpdateBody = func(_ string) ([]PriceEntry, error) {
		return oversized, nil
	}

	e := &stellarrpc.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, "ts"},
		ContractID: dexContractID,
	}
	_, err := decodeUpdate(e, VariantDEX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrPriceVectorOverflow) {
		t.Errorf("expected ErrPriceVectorOverflow, got %v", err)
	}
}

func TestDecoder_NamesPerVariant(t *testing.T) {
	cases := []struct {
		variant  Variant
		wantName string
	}{
		{VariantDEX, SourceDEX},
		{VariantCEX, SourceCEX},
		{VariantFX, SourceFX},
	}
	for _, tc := range cases {
		t.Run(tc.wantName, func(t *testing.T) {
			d := NewDecoder(tc.variant, "Ccontract")
			if d.Name() != tc.wantName {
				t.Errorf("Name() = %q, want %q", d.Name(), tc.wantName)
			}
		})
	}

	// WithDecoderDecimals option round-trips through the decoder's
	// internal scaling field — wire-level coverage is the
	// real-fixture test; this just checks the plumbing.
	d := NewDecoder(VariantDEX, "Ccontract", WithDecoderDecimals(10))
	if d.decimals != 10 {
		t.Errorf("WithDecoderDecimals not applied: got %d", d.decimals)
	}
}

func TestUpdateEvent_sourceMatchesUpdate(t *testing.T) {
	// The UpdateEvent.Source() must return the same thing as the
	// contained OracleUpdate's Source field — otherwise metric
	// labels drift between what the event reports and what the
	// row persisted to oracle_updates carries.
	u := canonical.OracleUpdate{Source: SourceCEX}
	e := UpdateEvent{Update: u}
	if e.Source() != SourceCEX {
		t.Errorf("UpdateEvent.Source() = %q, want %q", e.Source(), SourceCEX)
	}
}
