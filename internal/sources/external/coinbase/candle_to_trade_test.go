package coinbase

import (
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// coinbaseCandleToTrade has three early-exit error branches the
// existing backfill_test.go's happy-path TestCoinbaseCandleToTrade_LHOC_Ordering
// doesn't reach. They guard against malformed upstream rows
// landing in the trades hypertable as zero-volume or zero-price
// observations.

func makePair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, err := canonical.NewCryptoAsset("XLM")
	if err != nil {
		t.Fatalf("NewCryptoAsset XLM: %v", err)
	}
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset USD: %v", err)
	}
	pair, err := canonical.NewPair(xlm, usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return pair
}

func TestCoinbaseCandleToTrade_missingTimeRejected(t *testing.T) {
	// First slot is the open-time epoch; an empty slice fails
	// openTimeSec.
	row := coinbaseCandle{}
	_, err := coinbaseCandleToTrade(row, "XLM-USD", makePair(t), 3600)
	if err == nil {
		t.Error("expected \"missing time\" error, got nil")
	}
	if !strings.Contains(err.Error(), "time") {
		t.Errorf("error %q missing \"time\" fragment", err.Error())
	}
}

func TestCoinbaseCandleToTrade_zeroVolumeRejected(t *testing.T) {
	// Volume=0 is treated as missing — it would translate into a
	// zero-amount Trade that breaks downstream VWAP weighting.
	row := coinbaseCandle{
		1.7e9,   // time
		0.17500, // low
		0.17600, // high
		0.17582, // open
		0.17582, // close
		0.0,     // volume = 0 → reject
	}
	_, err := coinbaseCandleToTrade(row, "XLM-USD", makePair(t), 3600)
	if err == nil {
		t.Error("expected \"zero volume\" error, got nil")
	}
	if !strings.Contains(err.Error(), "volume") {
		t.Errorf("error %q missing \"volume\" fragment", err.Error())
	}
}

func TestCoinbaseCandleToTrade_missingVolumeRejected(t *testing.T) {
	// volume slot is the wrong type — volumeFloat returns ok=false.
	row := coinbaseCandle{
		1.7e9, 0.17500, 0.17600, 0.17582, 0.17582,
		"100.0", // string, not float64 — volumeFloat rejects
	}
	_, err := coinbaseCandleToTrade(row, "XLM-USD", makePair(t), 3600)
	if err == nil {
		t.Error("expected error for non-float volume, got nil")
	}
}

func TestCoinbaseCandleToTrade_zeroCloseRejected(t *testing.T) {
	// Close=0 is treated as missing — would yield a zero-quote-
	// amount Trade that downstream callers would mistake for a
	// free trade.
	row := coinbaseCandle{
		1.7e9, 0.17500, 0.17600, 0.17582,
		0.0,   // close = 0 → reject
		100.0, // volume
	}
	_, err := coinbaseCandleToTrade(row, "XLM-USD", makePair(t), 3600)
	if err == nil {
		t.Error("expected \"zero close\" error, got nil")
	}
	if !strings.Contains(err.Error(), "close") {
		t.Errorf("error %q missing \"close\" fragment", err.Error())
	}
}

func TestCoinbaseCandleToTrade_missingCloseRejected(t *testing.T) {
	row := coinbaseCandle{
		1.7e9, 0.17500, 0.17600, 0.17582,
		"0.18", // string, not float64
		100.0,
	}
	_, err := coinbaseCandleToTrade(row, "XLM-USD", makePair(t), 3600)
	if err == nil {
		t.Error("expected error for non-float close, got nil")
	}
}
