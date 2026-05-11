package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// stubFXHistoryReader implements v1.FXHistoryReader for chart-fiat tests.
type stubFXHistoryReader struct {
	points []v1.FXQuotePoint
	err    error
}

func (s *stubFXHistoryReader) ListFXHistory(_ context.Context, _ string, _, _ time.Time) ([]v1.FXQuotePoint, error) {
	return s.points, s.err
}

func TestChart_Fiat_USDtoCNY_ReturnsInverseSeries(t *testing.T) {
	// Reader returns USD-base rates: 1 USD = 7.18 CNY etc.
	d1 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	fx := &stubFXHistoryReader{points: []v1.FXQuotePoint{
		{Bucket: d1, RateUSD: 7.18, InverseUSD: 1 / 7.18},
		{Bucket: d2, RateUSD: 7.20, InverseUSD: 1 / 7.20},
	}}
	srv := v1.New(v1.Options{History: &stubHistoryReader{}, FXHistory: fx})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=fiat:USD&quote=fiat:CNY&timeframe=1y&granularity=1d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := len(env.Data.Points); got != 2 {
		t.Fatalf("got %d points, want 2", got)
	}
	// USD→CNY: useInverse=false, P should be RateUSD (~7.18).
	if env.Data.Points[0].P == "" {
		t.Errorf("point[0].P empty")
	}
}

func TestChart_Fiat_CNYtoUSD_UsesInverse(t *testing.T) {
	d1 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	fx := &stubFXHistoryReader{points: []v1.FXQuotePoint{
		{Bucket: d1, RateUSD: 7.18, InverseUSD: 1.0 / 7.18},
	}}
	srv := v1.New(v1.Options{History: &stubHistoryReader{}, FXHistory: fx})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=fiat:CNY&quote=fiat:USD&timeframe=1y&granularity=1d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Points) != 1 {
		t.Fatalf("got %d points, want 1", len(env.Data.Points))
	}
	// Should be ~0.139 (= 1/7.18) — inverse path. Quick "starts with 0." check.
	if env.Data.Points[0].P[:2] != "0." {
		t.Errorf("inverse rate %q not in 0.x form", env.Data.Points[0].P)
	}
}

func TestChart_Fiat_CrossPair_EmptySeries(t *testing.T) {
	// EUR/JPY (neither side USD) is not yet supported.
	fx := &stubFXHistoryReader{points: []v1.FXQuotePoint{
		{Bucket: time.Now(), RateUSD: 1.0, InverseUSD: 1.0},
	}}
	srv := v1.New(v1.Options{History: &stubHistoryReader{}, FXHistory: fx})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=fiat:EUR&quote=fiat:JPY&timeframe=1y&granularity=1d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Data.Points) != 0 {
		t.Errorf("cross-fiat should return empty series, got %d", len(env.Data.Points))
	}
}

func TestChart_Fiat_NoFXHistoryReader_EmptySeries(t *testing.T) {
	// FXHistory nil → empty series, not 500.
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=fiat:CNY&quote=fiat:USD&timeframe=1y&granularity=1d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
}

func TestChart_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", resp.StatusCode)
	}
}

func TestChart_MissingAsset400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

func TestChart_InvalidTimeframe400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&timeframe=2y")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for unknown timeframe", resp.StatusCode)
	}
}

func TestChart_TWAP400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&price_type=twap")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for unsupported price_type=twap", resp.StatusCode)
	}
}

func TestChart_InvalidPriceType400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&price_type=mean")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for unknown price_type", resp.StatusCode)
	}
}

func TestChart_BadGranularity400(t *testing.T) {
	reader := &stubHistoryReader{pointsErr: v1.ErrUnknownGranularity}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&granularity=2h")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for unknown granularity", resp.StatusCode)
	}
}

func TestChart_IdentityPair400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 (asset=quote)", resp.StatusCode)
	}
}

// TestChart_DefaultsTimeframeAndGranularity covers two defaults at
// once: timeframe=24h and granularity=15m (per ADR-0020 table).
func TestChart_DefaultsTimeframeAndGranularity(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	v := "100"
	reader := &stubHistoryReader{
		points: []v1.HistoryPoint{
			{Bucket: t0, VWAP: "0.50", VolumeUSD: &v},
			{Bucket: t0.Add(15 * time.Minute), VWAP: "0.51"},
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Timeframe != "24h" {
		t.Errorf("timeframe default = %q, want 24h", env.Data.Timeframe)
	}
	if env.Data.Granularity != "15m" {
		t.Errorf("granularity default = %q, want 15m (per ADR-0020 table)", env.Data.Granularity)
	}
	if env.Data.PriceType != "vwap" {
		t.Errorf("price_type = %q, want vwap", env.Data.PriceType)
	}
	if len(env.Data.Points) != 2 {
		t.Fatalf("got %d points, want 2", len(env.Data.Points))
	}
	if reader.lastCall.granularity != "15m" {
		t.Errorf("reader saw granularity=%q, want default-resolved 15m", reader.lastCall.granularity)
	}
	// 24h timeframe → from must be ~24h before now (zero would
	// indicate the timeframe→window mapping wasn't applied).
	if reader.lastCall.from.IsZero() {
		t.Error("reader saw zero from — timeframe window not applied")
	}
	delta := time.Since(reader.lastCall.from) - 24*time.Hour
	if delta < -5*time.Second || delta > 5*time.Second {
		t.Errorf("from window = %v from now, want ~24h", time.Since(reader.lastCall.from))
	}
}

// TestChart_TimeframeAllNoLowerBound — `all` means no `from` filter
// (since-inception equivalent). Reader sees zero from-time.
func TestChart_TimeframeAllNoLowerBound(t *testing.T) {
	reader := &stubHistoryReader{points: []v1.HistoryPoint{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&timeframe=all")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !reader.lastCall.from.IsZero() {
		t.Errorf("timeframe=all sent from=%v, want zero (no lower bound)", reader.lastCall.from)
	}
	if reader.lastCall.granularity != "1d" {
		t.Errorf("timeframe=all default granularity = %q, want 1d", reader.lastCall.granularity)
	}
}

// TestChart_PerTimeframeDefaultGranularity walks the ADR-0020 table.
// One assertion per row.
func TestChart_PerTimeframeDefaultGranularity(t *testing.T) {
	cases := map[string]string{
		"1h":  "1m",
		"24h": "15m",
		"1w":  "1h",
		"1mo": "4h",
		"1y":  "1d",
		"all": "1d",
	}
	for tf, wantG := range cases {
		t.Run(tf, func(t *testing.T) {
			reader := &stubHistoryReader{points: []v1.HistoryPoint{}}
			srv := v1.New(v1.Options{History: reader})
			ts := httpTestServer(t, srv)
			resp := mustGet(t, ts.URL+"/v1/chart?asset=native&timeframe="+tf)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("timeframe=%s: status=%d", tf, resp.StatusCode)
			}
			if reader.lastCall.granularity != wantG {
				t.Errorf("timeframe=%s: default granularity=%q want %q",
					tf, reader.lastCall.granularity, wantG)
			}
		})
	}
}

// TestChart_GranularityOverride confirms an explicit granularity
// overrides the timeframe-table default.
func TestChart_GranularityOverride(t *testing.T) {
	reader := &stubHistoryReader{points: []v1.HistoryPoint{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&timeframe=1h&granularity=15m")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if reader.lastCall.granularity != "15m" {
		t.Errorf("granularity=%q, want 15m (explicit override)", reader.lastCall.granularity)
	}
}

// pairKeyedHistoryReader returns different points per pair. Used
// by the stablecoin-fallback test below where the literal pair has
// no data and the proxy retry against X/USDC must succeed.
type pairKeyedHistoryReader struct {
	byPair map[string][]v1.HistoryPoint
	calls  []string // ordered list of pair keys queried
}

func (r *pairKeyedHistoryReader) HistoryPointsInRange(_ context.Context, p canonical.Pair, _ string, _, _ time.Time, _ int) ([]v1.HistoryPoint, error) {
	key := p.Base.String() + "/" + p.Quote.String()
	r.calls = append(r.calls, key)
	return r.byPair[key], nil
}

// Other HistoryReader methods are unused by the chart handler but
// must exist for interface satisfaction.
func (r *pairKeyedHistoryReader) HistoryPoints(_ context.Context, _ canonical.Pair, _ string, _ int) ([]v1.HistoryPoint, error) {
	return nil, nil
}

func (r *pairKeyedHistoryReader) TradesInRange(_ context.Context, _ canonical.Pair, _, _ time.Time, _ int) ([]canonical.Trade, error) {
	return nil, nil
}

func (r *pairKeyedHistoryReader) TradesInRangeAfter(_ context.Context, _ canonical.Pair, _, _, _ time.Time, _ uint32, _, _ string, _ uint32, _ int) ([]canonical.Trade, error) {
	return nil, nil
}

func (r *pairKeyedHistoryReader) LatestTradePerSource(_ context.Context, _ canonical.Pair, _ string) ([]canonical.Trade, error) {
	return nil, nil
}

// TestChart_StablecoinFallback exercises the X/fiat:USD →
// X/<USD-pegged classic> retry. /v1/chart for native/fiat:USD
// with no literal points but USDC trades available should return
// the USDC points and tag the envelope flags.triangulated=true.
func TestChart_StablecoinFallback(t *testing.T) {
	usdc, err := canonical.NewClassicAsset(
		"USDC",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &pairKeyedHistoryReader{
		byPair: map[string][]v1.HistoryPoint{
			"native/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN": {
				{Bucket: t0, VWAP: "0.16"},
				{Bucket: t0.Add(time.Hour), VWAP: "0.161"},
			},
		},
	}
	srv := v1.New(v1.Options{
		History:           reader,
		USDPeggedClassics: []canonical.Asset{usdc},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=fiat:USD&timeframe=24h&granularity=1h")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data  v1.ChartSeries `json:"data"`
		Flags v1.Flags       `json:"flags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}

	if len(env.Data.Points) != 2 {
		t.Fatalf("got %d points, want 2 (from USDC fallback)", len(env.Data.Points))
	}
	if !env.Flags.Triangulated {
		t.Error("flags.triangulated = false, want true on stablecoin-proxy fallback")
	}
	// Reader must have been called twice: literal pair first
	// (returns 0 points), then fallback to USDC.
	if len(reader.calls) < 2 {
		t.Fatalf("reader saw %d calls, want at least 2 (literal + fallback)", len(reader.calls))
	}
	if reader.calls[0] != "native/fiat:USD" {
		t.Errorf("first call = %q, want native/fiat:USD", reader.calls[0])
	}
	if reader.calls[1] != "native/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" {
		t.Errorf("fallback call = %q, want native/USDC-…", reader.calls[1])
	}
}

// TestChart_TruncatedFlagOnRetentionShortfall — when the requested
// timeframe extends before the earliest available data, the
// envelope flips Truncated=true and surfaces both DataStartsAt and
// RequestedFrom so consumers can render a "history begins ..." hint
// instead of guessing whether the deployment is data-thin or the
// asset is genuinely flat. R-013 in `docs/review-2026-05-10.md`.
func TestChart_TruncatedFlagOnRetentionShortfall(t *testing.T) {
	// 7 days of 1d points, but request `timeframe=1y` (=365d window).
	now := time.Now().UTC().Truncate(24 * time.Hour)
	pts := make([]v1.HistoryPoint, 0, 7)
	for i := 6; i >= 0; i-- {
		pts = append(pts, v1.HistoryPoint{Bucket: now.Add(-time.Duration(i) * 24 * time.Hour), VWAP: "0.16"})
	}
	reader := &stubHistoryReader{points: pts}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=fiat:USD&timeframe=1y")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	mustDecode(t, resp, &env)

	if !env.Data.Truncated {
		t.Error("Truncated = false, want true (1y asked, only 7 days returned)")
	}
	if env.Data.DataStartsAt == nil {
		t.Fatal("DataStartsAt = nil on truncated response")
	}
	if env.Data.RequestedFrom == nil {
		t.Fatal("RequestedFrom = nil on truncated response")
	}
	if !env.Data.DataStartsAt.Equal(pts[0].Bucket) {
		t.Errorf("DataStartsAt = %v, want %v", env.Data.DataStartsAt, pts[0].Bucket)
	}
	// RequestedFrom should be ~365d before now.
	delta := time.Since(*env.Data.RequestedFrom) - 365*24*time.Hour
	if delta < -10*time.Second || delta > 10*time.Second {
		t.Errorf("RequestedFrom = %v ago, want ~365d", time.Since(*env.Data.RequestedFrom))
	}
}

// TestChart_NotTruncatedWhenDataReachesWindowStart — when data
// covers the full requested window, Truncated stays false and the
// helper fields stay omitted from the JSON payload entirely.
func TestChart_NotTruncatedWhenDataReachesWindowStart(t *testing.T) {
	// Deeper history than the 24h request — first point is well
	// before `from`. Nothing is truncated.
	now := time.Now().UTC()
	reader := &stubHistoryReader{
		points: []v1.HistoryPoint{
			{Bucket: now.Add(-25 * time.Hour), VWAP: "0.16"},
			{Bucket: now.Add(-1 * time.Hour), VWAP: "0.17"},
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=fiat:USD&timeframe=24h")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Truncated {
		t.Error("Truncated = true, want false (data reaches window start)")
	}
	if env.Data.DataStartsAt != nil {
		t.Errorf("DataStartsAt = %v, want nil when not truncated", env.Data.DataStartsAt)
	}
}

// TestChart_TimeframeAllNeverTruncated — `timeframe=all` means
// "everything you have" by definition, so a short result is the
// full result, never truncated.
func TestChart_TimeframeAllNeverTruncated(t *testing.T) {
	now := time.Now().UTC()
	reader := &stubHistoryReader{
		points: []v1.HistoryPoint{{Bucket: now.Add(-1 * time.Hour), VWAP: "0.16"}},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/chart?asset=native&quote=fiat:USD&timeframe=all")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.ChartSeries `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Truncated {
		t.Error("Truncated = true on timeframe=all; that timeframe means 'everything', never truncated")
	}
}
