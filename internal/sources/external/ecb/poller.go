// Package ecb polls the European Central Bank's daily foreign-
// exchange reference rates. First `ClassAuthoritySanity` connector
// in the fleet — authoritative but daily-cadence sovereign rates,
// used by the aggregator as an end-of-day anchor against which
// intraday VWAP computation drift surfaces as a divergence signal.
//
// Role: NOT primary pricing (cadence too slow — published once per
// TARGET business day ~4pm CET). Not for triangulation either
// (intraday triangulation uses Polygon/ExchangeRatesApi). ECB's
// value is that it's the EU's official reference rate — if our
// computed EUR/USD ever diverges > 50 bps from ECB's daily close,
// we want to know, because one of our upstream feeds is drifting.
//
// Free, no auth, official source. Cadence: one published fix per
// TARGET business day (skips EU bank holidays + weekends). Our
// poller handles "no update since last poll" by re-emitting the
// same rate with a fresh Observer timestamp — harmless idempotent
// insert given the stable tx_hash synthesis.
//
// Wire shape (verified 2026-04-24 against
// https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml):
//
//	<gesmes:Envelope xmlns:gesmes="..." xmlns="...">
//	  <gesmes:subject>Reference rates</gesmes:subject>
//	  <gesmes:Sender>
//	    <gesmes:name>European Central Bank</gesmes:name>
//	  </gesmes:Sender>
//	  <Cube>
//	    <Cube time="2026-04-23">
//	      <Cube currency="USD" rate="1.0825"/>
//	      <Cube currency="JPY" rate="162.45"/>
//	      <Cube currency="GBP" rate="0.8450"/>
//	      ...
//	    </Cube>
//	  </Cube>
//	</gesmes:Envelope>
//
// Each inner <Cube> element's `rate` is "1 EUR = X currency". To fit
// our canonical "price of Asset in Quote" semantics we invert:
//
//	Asset = <currency>, Quote = EUR, Price = 1 / rate
//
// So for USD at rate=1.0825: Asset=USD, Quote=EUR, Price=0.9238.
package ecb

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

const (
	SourceName = "ecb"

	// DefaultEndpoint is the stable URL for the daily XML feed.
	// ECB also publishes 90-day and "all history" variants; the
	// daily file is what we want for a sanity-anchor poller.
	DefaultEndpoint = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

	// DefaultPollInterval — ECB publishes once per EU business
	// day. 6h cadence is comfortable slack vs the ~24h publish
	// window; the poller is idempotent so polling more often is
	// harmless (fresh timestamp, same rate values, dedup via
	// stable tx_hash).
	DefaultPollInterval = 6 * time.Hour

	// DefaultDecimals — 6dp matches ExchangeRatesApi + Polygon
	// Forex. ECB publishes 4dp natively; the extra headroom
	// stays precision-safe under float→integer round-trips.
	DefaultDecimals uint8 = 6
)

var _ = external.ClassAuthoritySanity

var (
	ErrMalformedResponse = errors.New("ecb: malformed XML response")
	ErrNoRates           = errors.New("ecb: response contained no rate cubes")
)

// Poller implements external.Poller.
type Poller struct {
	Endpoint string
	Interval time.Duration
}

func NewPoller() *Poller {
	return &Poller{
		Endpoint: DefaultEndpoint,
		Interval: DefaultPollInterval,
	}
}

func (p *Poller) Name() string          { return SourceName }
func (p *Poller) Class() external.Class { return external.ClassAuthoritySanity }
func (p *Poller) PollInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// gesmesEnvelope matches the ECB's XML shape. The `gesmes` and
// default namespaces come through as prefixed element names; we
// only inspect the Cube hierarchy so the namespace attributes
// themselves don't need unmarshaling.
type gesmesEnvelope struct {
	XMLName xml.Name  `xml:"Envelope"`
	Cube    outerCube `xml:"Cube"`
}

type outerCube struct {
	Inner []dateCube `xml:"Cube"`
}

type dateCube struct {
	Time  string    `xml:"time,attr"`
	Rates []rateRow `xml:"Cube"`
}

type rateRow struct {
	Currency string `xml:"currency,attr"`
	Rate     string `xml:"rate,attr"`
}

// PollOnce implements external.Poller. One GET pulls the day's
// reference rates; we emit one OracleUpdate per (currency, EUR)
// pair that matches the configured fiat list.
//
// Pair filtering: we emit only for currencies that appear as
// *either side* of some configured pair. Unlike the aggregator
// pollers (which filter by exact combo), ECB's natural semantics
// are "EUR vs N other currencies" — operator who configures
// XLM/EUR still wants the USD/EUR rate to triangulate through.
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/xml, text/xml")
	req.Header.Set("User-Agent", "ratesengine/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var env gesmesEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrMalformedResponse, err)
	}
	if len(env.Cube.Inner) == 0 || len(env.Cube.Inner[0].Rates) == 0 {
		return nil, nil, ErrNoRates
	}

	// Take the newest date cube (first in ECB's output — they list
	// newest first in the daily file).
	day := env.Cube.Inner[0]
	ts, err := time.Parse("2006-01-02", day.Time)
	if err != nil {
		// Defensive: if the date doesn't parse, fall back to now().
		// The daily file always has a valid ISO date, so this is
		// belt-and-braces.
		ts = time.Now().UTC()
	}

	// Build the interest set from configured pairs. ECB gives us
	// rates against EUR; we emit for every currency that appears in
	// a pair AND has a rate cube.
	wanted := map[string]canonical.Asset{}
	eurAsset, err := canonical.NewFiatAsset("EUR")
	if err != nil {
		return nil, nil, fmt.Errorf("EUR asset: %w", err)
	}
	for _, pair := range pairs {
		for _, a := range []canonical.Asset{pair.Base, pair.Quote} {
			if a.Type != canonical.AssetFiat {
				continue
			}
			code := strings.ToUpper(a.Code)
			if code == "EUR" {
				continue
			}
			wanted[code] = a
		}
	}
	if len(wanted) == 0 {
		// No fiat cross-rates to cover — e.g. the pair list is all
		// crypto-crypto. Silent no-op.
		return nil, nil, nil
	}

	updates := make([]canonical.OracleUpdate, 0, len(wanted))
	scalePow := pow10(int(DefaultDecimals))

	for _, row := range day.Rates {
		code := strings.ToUpper(row.Currency)
		asset, ok := wanted[code]
		if !ok {
			continue
		}
		// Parse the rate (ECB publishes "1 EUR = X currency").
		rate, err := strconv.ParseFloat(row.Rate, 64)
		if err != nil || rate <= 0 {
			continue
		}
		// Invert + scale: price of currency in EUR = 1 / rate.
		rateScaled, err := floatToScaledInt(rate, int(DefaultDecimals))
		if err != nil || rateScaled.Sign() <= 0 {
			continue
		}
		inverted := new(big.Int).Div(
			new(big.Int).Mul(scalePow, scalePow),
			rateScaled,
		)
		if inverted.Sign() <= 0 {
			continue
		}

		u := canonical.OracleUpdate{
			Source:     SourceName,
			ContractID: "",
			Ledger:     0,
			TxHash:     syntheticTxHash(code, "EUR", ts.Unix()),
			OpIndex:    0,
			Timestamp:  ts.UTC(),
			Asset:      asset,
			Quote:      eurAsset,
			Price:      canonical.NewAmount(inverted),
			Decimals:   DefaultDecimals,
			Observer:   "",
		}
		updates = append(updates, u)
	}
	return nil, updates, nil
}

// floatToScaledInt / decimalStringToScaledInt / syntheticTxHash —
// package-local scaling helpers, parallel to the other pollers.

func floatToScaledInt(v float64, decimals int) (*big.Int, error) {
	if v < 0 || v != v {
		return nil, fmt.Errorf("bad value %v", v)
	}
	return decimalStringToScaledInt(strconv.FormatFloat(v, 'f', decimals+2, 64), decimals)
}

func decimalStringToScaledInt(s string, targetDecimals int) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal string")
	}
	if strings.ContainsAny(s, "eE") {
		return nil, fmt.Errorf("scientific notation %q not supported", s)
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > targetDecimals {
		fracPart = fracPart[:targetDecimals]
	}
	for len(fracPart) < targetDecimals {
		fracPart += "0"
	}
	v, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return nil, fmt.Errorf("not a decimal: %q", s)
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// syntheticTxHash derives a stable 64-char hex from (currency, ts).
// Same-day reruns collide on (currency, unix_day) so the idempotent
// insert path dedupes multiple polls of the same day's fix.
func syntheticTxHash(currency, base string, ts int64) string {
	s := fmt.Sprintf("ECB-%s-%s-%020d", strings.ToUpper(currency), strings.ToUpper(base), ts)
	var hex strings.Builder
	hex.Grow(64)
	for _, b := range []byte(s) {
		fmt.Fprintf(&hex, "%02x", b)
		if hex.Len() >= 64 {
			break
		}
	}
	for hex.Len() < 64 {
		hex.WriteByte('0')
	}
	return hex.String()[:64]
}
