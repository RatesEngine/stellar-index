package canonical_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
)

const (
	goodTxHash = "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe"
)

func validTrade() c.Trade {
	return c.Trade{
		Source:      "sdex",
		Ledger:      52_430_001,
		TxHash:      goodTxHash,
		OpIndex:     0,
		Timestamp:   time.Unix(1745000000, 0).UTC(),
		Pair:        mustPair(c.NativeAsset(), mustClassic("USDC", usdcIssuer)),
		BaseAmount:  c.NewAmount(big.NewInt(100_000_0000)), // 100 XLM in stroops
		QuoteAmount: c.NewAmount(big.NewInt(12_420_000)),   // 12.42 USDC (7 decimals)
	}
}

func TestTrade_ID(t *testing.T) {
	tr := validTrade()
	want := "sdex:52430001:" + goodTxHash + ":0"
	if got := tr.ID(); got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestTrade_Validate_happy(t *testing.T) {
	if err := validTrade().Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestTrade_Validate_errors(t *testing.T) {
	cases := map[string]func(*c.Trade){
		"empty source": func(t *c.Trade) { t.Source = "" },
		// "zero ledger" is intentionally NOT an error case —
		// off-chain sources (Binance/Kraken/Bitstamp/Coinbase)
		// stamp Ledger=0 deliberately. Uniqueness comes from
		// Source + TxHash + OpIndex. Documented in Validate().
		"short tx hash":   func(t *c.Trade) { t.TxHash = "cafe" },
		"non-hex tx hash": func(t *c.Trade) { t.TxHash = "z" + goodTxHash[1:] },
		// Uppercase hex decodes but isn't canonical — Postgres would
		// treat upper and lower hex as distinct primary keys, so the
		// same on-chain tx from different sources could duplicate.
		"uppercase tx hash":  func(t *c.Trade) { t.TxHash = "CAFEBABECAFEBABECAFEBABECAFEBABECAFEBABECAFEBABECAFEBABECAFEBABE" },
		"mixed case tx hash": func(t *c.Trade) { t.TxHash = "CafeBabe" + goodTxHash[8:] },
		"zero timestamp":     func(t *c.Trade) { t.Timestamp = time.Time{} },
		"zero base amount":   func(t *c.Trade) { t.BaseAmount = c.NewAmount(big.NewInt(0)) },
		"neg quote amount":   func(t *c.Trade) { t.QuoteAmount = c.NewAmount(big.NewInt(-1)) },
		"self-pair":          func(t *c.Trade) { t.Pair = c.Pair{Base: c.NativeAsset(), Quote: c.NativeAsset()} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tr := validTrade()
			mutate(&tr)
			err := tr.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			// Must wrap the ErrInvalidTrade sentinel so callers can
			// errors.Is classify; mirrors TestOracle_Validate_errors.
			if !errors.Is(err, c.ErrInvalidTrade) {
				t.Errorf("err %v does not wrap ErrInvalidTrade", err)
			}
		})
	}
}

func TestTrade_PriceRatio(t *testing.T) {
	tr := validTrade()
	num, den := tr.PriceRatio()
	if num.Cmp(big.NewInt(12_420_000)) != 0 {
		t.Fatalf("num = %s", num)
	}
	if den.Cmp(big.NewInt(100_000_0000)) != 0 {
		t.Fatalf("den = %s", den)
	}

	// Mutating the returned bigints must not affect the trade.
	num.Add(num, big.NewInt(1))
	num2, _ := tr.PriceRatio()
	if num2.Cmp(big.NewInt(12_420_000)) != 0 {
		t.Fatalf("trade was mutated by caller: num2 = %s", num2)
	}
}

func TestTrade_Equal_identityOnly(t *testing.T) {
	a := validTrade()
	b := validTrade()
	if !a.Equal(b) {
		t.Fatal("identical trades should be equal")
	}
	b.Maker = "G..."
	if !a.Equal(b) {
		t.Fatal("Maker differs but identity same → Equal should still be true")
	}
	b.OpIndex = 1
	if a.Equal(b) {
		t.Fatal("identity should differ on OpIndex")
	}
}

func TestTrade_JSON(t *testing.T) {
	tr := validTrade()
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got c.Trade
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(tr) {
		t.Fatalf("round-trip identity: got %+v", got)
	}
	if got.BaseAmount.Cmp(tr.BaseAmount) != 0 {
		t.Fatalf("base_amount lost: got %s, want %s", got.BaseAmount, tr.BaseAmount)
	}
	if got.QuoteAmount.Cmp(tr.QuoteAmount) != 0 {
		t.Fatalf("quote_amount lost: got %s, want %s", got.QuoteAmount, tr.QuoteAmount)
	}
}

func TestTrade_LargeAmounts(t *testing.T) {
	// i128 invariant — make sure a Trade with Soroban-scale amounts
	// round-trips through JSON without losing precision. Uses the
	// KALIEN-incident-sized number from amount_test.go.
	tr := validTrade()
	kalien, ok := new(big.Int).SetString("40000005972900000000", 10)
	if !ok {
		t.Fatal("bad test data")
	}
	tr.BaseAmount = c.NewAmount(kalien)

	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got c.Trade
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.BaseAmount.String() != "40000005972900000000" {
		t.Fatalf("i128 round-trip lost precision: got %s", got.BaseAmount.String())
	}
}
