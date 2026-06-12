package bitstamp

import (
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// bitstampCandleToTrade has four error branches before its happy
// path: bad timestamp, bad volume, zero volume, bad close. The
// existing TestBitstampBackfill_HappyPath only exercises the
// success path through Backfill.

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

func TestBitstampCandleToTrade_badTimestampRejected(t *testing.T) {
	c := bitstampCandle{
		Timestamp: "not-a-number",
		Volume:    "100",
		Close:     "0.18",
	}
	_, err := bitstampCandleToTrade(c, "xlmusd", makePair(t), 3600)
	if err == nil {
		t.Error("expected error on bad timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Errorf("error %q missing \"timestamp\" fragment", err.Error())
	}
}

func TestBitstampCandleToTrade_badVolumeRejected(t *testing.T) {
	c := bitstampCandle{
		Timestamp: "1700000000",
		Volume:    "1e3", // scientific notation, decimalString rejects
		Close:     "0.18",
	}
	_, err := bitstampCandleToTrade(c, "xlmusd", makePair(t), 3600)
	if err == nil {
		t.Error("expected error on scientific-notation volume, got nil")
	}
	if !strings.Contains(err.Error(), "volume") {
		t.Errorf("error %q missing \"volume\" fragment", err.Error())
	}
}

func TestBitstampCandleToTrade_zeroVolumeRejected(t *testing.T) {
	c := bitstampCandle{
		Timestamp: "1700000000",
		Volume:    "0",
		Close:     "0.18",
	}
	_, err := bitstampCandleToTrade(c, "xlmusd", makePair(t), 3600)
	if err == nil {
		t.Error("expected error on zero volume, got nil")
	}
	if !strings.Contains(err.Error(), "zero volume") {
		t.Errorf("error %q missing \"zero volume\" fragment", err.Error())
	}
}

func TestBitstampCandleToTrade_badCloseRejected(t *testing.T) {
	c := bitstampCandle{
		Timestamp: "1700000000",
		Volume:    "100",
		Close:     "not-a-number",
	}
	_, err := bitstampCandleToTrade(c, "xlmusd", makePair(t), 3600)
	if err == nil {
		t.Error("expected error on bad close, got nil")
	}
	if !strings.Contains(err.Error(), "close") {
		t.Errorf("error %q missing \"close\" fragment", err.Error())
	}
}

func TestBitstampCandleToTrade_happyPath(t *testing.T) {
	c := bitstampCandle{
		Timestamp: "1700000000",
		Volume:    "100",
		Close:     "0.18",
	}
	trade, err := bitstampCandleToTrade(c, "xlmusd", makePair(t), 3600)
	if err != nil {
		t.Fatalf("bitstampCandleToTrade: %v", err)
	}
	if trade.Source != SourceName {
		t.Errorf("Source = %q, want %q", trade.Source, SourceName)
	}
	if trade.BaseAmount.Sign() == 0 {
		t.Error("BaseAmount is zero")
	}
	if trade.QuoteAmount.Sign() == 0 {
		t.Error("QuoteAmount is zero")
	}
}
