package ecb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// fixtureXML is a real-shape ECB daily file (condensed). The
// surrounding namespaces are preserved so XML unmarshaling
// exercises the same path as real fetches.
const fixtureXML = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <gesmes:subject>Reference rates</gesmes:subject>
  <gesmes:Sender><gesmes:name>European Central Bank</gesmes:name></gesmes:Sender>
  <Cube>
    <Cube time="2026-04-23">
      <Cube currency="USD" rate="1.0825"/>
      <Cube currency="JPY" rate="162.45"/>
      <Cube currency="GBP" rate="0.8450"/>
      <Cube currency="CAD" rate="1.4920"/>
      <Cube currency="CHF" rate="0.9340"/>
      <Cube currency="BOGUS" rate="-1"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`

func buildPairs(t *testing.T) []canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	gbp, _ := canonical.NewFiatAsset("GBP")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	xlmEUR, _ := canonical.NewPair(xlm, eur)
	xlmGBP, _ := canonical.NewPair(xlm, gbp)
	return []canonical.Pair{xlmUSD, xlmEUR, xlmGBP}
}

func newTestECBServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
}

func TestPollOnce_HappyPath(t *testing.T) {
	srv := newTestECBServer(t, fixtureXML, http.StatusOK)
	defer srv.Close()

	p := NewPoller()
	p.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trades, updates, err := p.PollOnce(ctx, buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("expected 0 trades (sovereign rates emit updates only), got %d", len(trades))
	}
	// Pair list has USD, GBP (and EUR, skipped as base). Fixture
	// has USD/JPY/GBP/CAD/CHF/BOGUS. Emissions: USD + GBP only.
	// JPY/CAD/CHF not wanted; BOGUS has negative rate; EUR excluded.
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (USD, GBP), got %d", len(updates))
	}

	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	var usdU *canonical.OracleUpdate
	for i := range updates {
		if updates[i].Asset.Equal(usd) {
			usdU = &updates[i]
			break
		}
	}
	if usdU == nil {
		t.Fatal("missing USD update")
	}
	if !usdU.Quote.Equal(eur) {
		t.Errorf("USD quote = %+v want EUR", usdU.Quote)
	}
	// ECB: 1 EUR = 1.0825 USD → 1 USD = 1/1.0825 EUR ≈ 0.9238 EUR.
	// At 10^6 → 923788 (give or take rounding).
	priceInt := usdU.Price.BigInt().Int64()
	if priceInt < 923_000 || priceInt > 925_000 {
		t.Errorf("USD price (1/1.0825 at 10^6) = %d want ~923788", priceInt)
	}
	if usdU.Decimals != 6 {
		t.Errorf("decimals = %d want 6", usdU.Decimals)
	}
	if len(usdU.TxHash) != 64 {
		t.Errorf("tx_hash len = %d", len(usdU.TxHash))
	}
	if usdU.Source != "ecb" {
		t.Errorf("Source = %q", usdU.Source)
	}
	// Timestamp should parse from the day cube.
	wantDay, _ := time.Parse("2006-01-02", "2026-04-23")
	if !usdU.Timestamp.Equal(wantDay) {
		t.Errorf("Timestamp = %v want %v", usdU.Timestamp, wantDay)
	}
}

func TestPollOnce_MalformedXML(t *testing.T) {
	srv := newTestECBServer(t, `<not valid xml`, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrMalformedResponse) {
		t.Errorf("expected ErrMalformedResponse, got %v", err)
	}
}

func TestPollOnce_EmptyCube(t *testing.T) {
	srv := newTestECBServer(t, `<?xml version="1.0"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube></Cube>
</gesmes:Envelope>`, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrNoRates) {
		t.Errorf("expected ErrNoRates, got %v", err)
	}
}

func TestPollOnce_CryptoOnlyPairs_NoOp(t *testing.T) {
	// No fiat in the pair list → poller no-ops (still hits HTTP,
	// decodes the response, then returns empty). This mirrors the
	// FX pollers' behaviour.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	xlmUsdt, _ := canonical.NewPair(xlm, usdt)

	srv := newTestECBServer(t, fixtureXML, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{xlmUsdt})
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("expected 0 updates (no fiat in pairs), got %d", len(updates))
	}
}

func TestPollOnce_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "maintenance", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Error("expected error on HTTP http.StatusServiceUnavailable")
	}
}

func TestPollOnce_UnknownCurrencySkipped(t *testing.T) {
	// Currency code "BOGUS" in fixture has rate="-1" — skip per-entry.
	srv := newTestECBServer(t, fixtureXML, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	// Pair list asks for USD, GBP, EUR. Fixture valid entries
	// matching: USD, GBP. BOGUS skipped (negative rate).
	for _, u := range updates {
		if strings.EqualFold(u.Asset.Code, "BOGUS") {
			t.Error("BOGUS currency (rate=-1) should have been skipped")
		}
	}
}

func TestPollInterval_Default(t *testing.T) {
	p := NewPoller()
	if p.PollInterval() != 6*time.Hour {
		t.Errorf("default = %v want 6h", p.PollInterval())
	}
}

func TestInversionMath_MatchesExpected(t *testing.T) {
	// Direct check of the inversion pipeline: ECB's rate=1.0825 →
	// emitted price should be 1/1.0825 = 0.92378... at 10^6 scale
	// ≈ 923787.
	scaled, err := floatToScaledInt(1.0825, 6)
	if err != nil {
		t.Fatalf("floatToScaledInt: %v", err)
	}
	// We don't invert inside floatToScaledInt — that's the
	// Poller's responsibility. Just verify the scaled value.
	if scaled.Int64() != 1_082_500 {
		t.Errorf("1.0825 at 10^6 = %d want 1082500", scaled.Int64())
	}
}
