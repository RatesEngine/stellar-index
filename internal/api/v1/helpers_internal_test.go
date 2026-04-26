package v1

import (
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// tradeRowFrom: the existing handler-level history_test exercises
// the explicit-decimals path; this pins the default-on-non-positive
// branch (decimals <= 0 → 10 dp) without going through HTTP.

func TestTradeRowFrom_defaultDecimalsOnZero(t *testing.T) {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	tr := canonical.Trade{
		Source:      "soroswap",
		Ledger:      52_000_000,
		TxHash:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		OpIndex:     0,
		Timestamp:   time.Unix(1_770_000_000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(1_000_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(2_000_000)),
	}
	// decimals=0 must trigger the default (10 dp) rather than emit
	// an integer-only price string.
	got := tradeRowFrom(tr, 0)
	if got.Price == "2" || got.Price == "" {
		t.Errorf("Price = %q on decimals=0; expected default 10-dp formatting (got the integer-only path)",
			got.Price)
	}
	// decimals=-3 (also <= 0) must take the same default path.
	gotNeg := tradeRowFrom(tr, -3)
	if gotNeg.Price != got.Price {
		t.Errorf("decimals<0 (%q) and decimals=0 (%q) should both apply the default",
			gotNeg.Price, got.Price)
	}
}
