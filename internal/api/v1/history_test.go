package v1_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// stubHistoryReader implements v1.HistoryReader with a static slice.
type stubHistoryReader struct {
	trades []canonical.Trade
	// observations is the per-source fixture returned by
	// LatestTradePerSource. Distinct from `trades` so observations
	// tests don't have to share state with TradesInRange tests.
	observations []canonical.Trade
	points       []v1.HistoryPoint
	// pointsByPair, when non-nil, overrides `points` based on the
	// query pair. Used by the F-1225 stablecoin-fallback regression
	// test where the literal XLM/fiat:USD pair returns empty and a
	// proxied XLM/USDC-G… pair carries the fixture.
	pointsByPair map[string][]v1.HistoryPoint
	lastCall     struct {
		from, to     time.Time
		limit        int
		afterTs      time.Time
		afterLedger  uint32
		afterTxHash  string
		afterSource  string
		afterOpIndex uint32
		granularity  string
		sourceFilter string
	}
	// pointsErr is set by tests that want to drive the
	// since-inception handler to a specific error code (e.g.
	// ErrUnknownGranularity).
	pointsErr error
	err       error
}

func (r *stubHistoryReader) TradesInRange(_ context.Context, _ canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	r.lastCall.from = from
	r.lastCall.to = to
	r.lastCall.limit = limit
	if r.err != nil {
		return nil, r.err
	}
	return r.trades, nil
}

// TradesInRangeAfter: the stub ignores the cursor (tests construct
// their own trade slices per-assertion) but records it so cursor
// tests can verify the handler forwarded it.
func (r *stubHistoryReader) TradesInRangeAfter(_ context.Context, _ canonical.Pair, from, to, afterTs time.Time, afterLedger uint32, afterTxHash, afterSource string, afterOpIndex uint32, limit int) ([]canonical.Trade, error) {
	r.lastCall.from = from
	r.lastCall.to = to
	r.lastCall.limit = limit
	r.lastCall.afterTs = afterTs
	r.lastCall.afterLedger = afterLedger
	r.lastCall.afterTxHash = afterTxHash
	r.lastCall.afterSource = afterSource
	r.lastCall.afterOpIndex = afterOpIndex
	if r.err != nil {
		return nil, r.err
	}
	return r.trades, nil
}

// HistoryPoints stub records the granularity + returns the
// pre-set fixture (or pointsErr). Honours pointsByPair when set,
// otherwise falls back to the global `points` slice.
func (r *stubHistoryReader) HistoryPoints(_ context.Context, pair canonical.Pair, granularity string, _ int) ([]v1.HistoryPoint, error) {
	r.lastCall.granularity = granularity
	if r.pointsErr != nil {
		return nil, r.pointsErr
	}
	if r.pointsByPair != nil {
		return r.pointsByPair[pair.String()], nil
	}
	return r.points, nil
}

// HistoryPointsInRange stub mirrors HistoryPoints — same fixture,
// same error path. Records the from/to so chart tests can assert
// the timeframe→window mapping.
func (r *stubHistoryReader) HistoryPointsInRange(_ context.Context, _ canonical.Pair, granularity string, from, to time.Time, _ int) ([]v1.HistoryPoint, error) {
	r.lastCall.granularity = granularity
	r.lastCall.from = from
	r.lastCall.to = to
	if r.pointsErr != nil {
		return nil, r.pointsErr
	}
	return r.points, nil
}

// LatestTradePerSource stub: returns r.observations (per-source
// fixture distinct from the full r.trades slice) so observations
// tests can drive the handler without polluting other history-test
// fixtures. Honors sourceFilter — restricts to the matching entry.
func (r *stubHistoryReader) LatestTradePerSource(_ context.Context, _ canonical.Pair, sourceFilter string) ([]canonical.Trade, error) {
	r.lastCall.sourceFilter = sourceFilter
	if r.err != nil {
		return nil, r.err
	}
	if sourceFilter == "" {
		return r.observations, nil
	}
	out := make([]canonical.Trade, 0, 1)
	for _, t := range r.observations {
		if t.Source == sourceFilter {
			out = append(out, t)
		}
	}
	return out, nil
}

func mkHistTrade(price int64) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source: "soroswap", Ledger: 1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   time.Unix(1_772_000_000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(1)),
		QuoteAmount: canonical.NewAmount(big.NewInt(price)),
	}
}

func TestHistory_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no reader wired)", resp.StatusCode)
	}
}

func TestHistory_MissingBase400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// When `asset` is supplied (the /v1/price/chart param convention)
// and `base` is missing, the error detail nudges callers to the
// right param name. Pin the hint shape so a refactor can't silently
// drop it — that hint is the difference between a confused 400 and
// a self-explanatory one.
func TestHistory_MissingBaseWithAssetHint(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "base/quote") || !strings.Contains(string(body), "/v1/price") {
		t.Errorf("body should mention base/quote and /v1/price; got %q", string(body))
	}
}

// parseBaseQuote has four branches; TestHistory_MissingBase400
// covers one. The remaining three (bad base, missing quote, bad
// quote) all share the same shape — quick coverage round-trip.

func TestHistory_BadBase400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=garbage&quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid base id)", resp.StatusCode)
	}
}

func TestHistory_MissingQuote400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHistory_BadQuote400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=garbage")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid quote id)", resp.StatusCode)
	}
}

func TestHistory_InvalidTime400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&from=yesterday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHistory_FromAfterTo400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	q := url.Values{}
	q.Set("base", "native")
	q.Set("quote", "fiat:USD")
	q.Set("from", "2026-04-23T12:00:00Z")
	q.Set("to", "2026-04-23T11:00:00Z")
	resp := mustGet(t, ts.URL+"/v1/history?"+q.Encode())
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHistory_InvalidLimit400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	for _, bad := range []string{"0", "10001", "-5", "abc"} {
		resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&limit="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestHistory_ReturnsTrades(t *testing.T) {
	reader := &stubHistoryReader{
		trades: []canonical.Trade{mkHistTrade(100), mkHistTrade(101)},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data []v1.TradeRow `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 2 {
		t.Fatalf("got %d rows, want 2", len(env.Data))
	}
	if env.Data[0].Source != "soroswap" {
		t.Errorf("source = %q", env.Data[0].Source)
	}
	if env.Data[0].BaseAsset != "native" || env.Data[0].QuoteAsset != "fiat:USD" {
		t.Errorf("pair fields wrong: %+v", env.Data[0])
	}
	if env.Data[0].Price == "" {
		t.Error("price missing")
	}
	if reader.lastCall.limit != 50 {
		t.Errorf("limit threaded to reader = %d, want 50", reader.lastCall.limit)
	}
}

func TestHistory_DefaultWindowIs1Hour(t *testing.T) {
	// When neither from nor to is set, the handler should compute a
	// 1-hour window ending ~now. Check the window duration rather
	// than absolute times (to minimize test-clock flakiness).
	reader := &stubHistoryReader{trades: nil}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	_ = mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD")

	if reader.lastCall.from.IsZero() || reader.lastCall.to.IsZero() {
		t.Fatal("handler didn't pass from/to to reader")
	}
	dur := reader.lastCall.to.Sub(reader.lastCall.from)
	if dur != time.Hour {
		t.Errorf("default window = %v, want 1h", dur)
	}
}

func TestHistory_EmitsNextCursorWhenPageFull(t *testing.T) {
	// With limit=2 and reader returning exactly 2 rows, the handler
	// treats the page as full and emits a next cursor. Clients then
	// re-issue with ?cursor=<that> to get subsequent pages.
	trades := []canonical.Trade{mkHistTrade(100), mkHistTrade(101)}
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: trades}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&limit=2")
	var env struct {
		Data       []v1.TradeRow `json:"data"`
		Pagination *struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)
	if env.Pagination == nil || env.Pagination.Next == "" {
		t.Fatalf("page full → expected next cursor, got: %+v", env.Pagination)
	}
}

func TestHistory_NoCursorWhenPageNotFull(t *testing.T) {
	// Reader returns fewer rows than limit → window exhausted →
	// no next cursor.
	trades := []canonical.Trade{mkHistTrade(100)}
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: trades}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&limit=50")
	var env struct {
		Data       []v1.TradeRow `json:"data"`
		Pagination *struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)
	if env.Pagination != nil && env.Pagination.Next != "" {
		t.Errorf("short page → no next cursor, got %q", env.Pagination.Next)
	}
}

func TestHistory_CursorForwardedToReader(t *testing.T) {
	// A valid cursor decodes to the full PK tuple (ts, ledger,
	// tx_hash, op_index, source) and gets forwarded to the reader.
	// Widening the cursor to full PK (see history.go) means we must
	// also verify tx_hash and source round-trip.
	reader := &stubHistoryReader{trades: []canonical.Trade{mkHistTrade(100), mkHistTrade(101)}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	// First request → get a cursor back.
	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&limit=2")
	var env struct {
		Pagination *struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)
	if env.Pagination == nil {
		t.Fatal("first request should have produced a cursor")
	}
	next := env.Pagination.Next

	// Second request with that cursor — reader sees every full-PK
	// component populated.
	reader.lastCall.afterTs = time.Time{}
	reader.lastCall.afterLedger = 0
	reader.lastCall.afterTxHash = ""
	reader.lastCall.afterSource = ""
	reader.lastCall.afterOpIndex = 0
	_ = mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&cursor="+next)

	last := reader.trades[len(reader.trades)-1]
	if reader.lastCall.afterTs.IsZero() {
		t.Error("cursor not decoded into afterTs")
	}
	if reader.lastCall.afterLedger != last.Ledger {
		t.Errorf("afterLedger = %d, want %d", reader.lastCall.afterLedger, last.Ledger)
	}
	if reader.lastCall.afterTxHash != last.TxHash {
		t.Errorf("afterTxHash = %q, want %q", reader.lastCall.afterTxHash, last.TxHash)
	}
	if reader.lastCall.afterSource != last.Source {
		t.Errorf("afterSource = %q, want %q", reader.lastCall.afterSource, last.Source)
	}
	if reader.lastCall.afterOpIndex != last.OpIndex {
		t.Errorf("afterOpIndex = %d, want %d", reader.lastCall.afterOpIndex, last.OpIndex)
	}
}

func TestHistory_InvalidCursor400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	// base64-encode each "raw" cursor shape below.
	b64 := func(s string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(s))
	}
	lowerHex64 := "fadefadefadefadefadefadefadefadefadefadefadefadefadefadefadefade"
	uppercaseHex := "FADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADEFADE"

	for _, bad := range []string{
		"not-base64!!!",
		"dGVzdA", // base64 of "test" — no colon separator
		// Empty source — would degenerate the full-PK cursor back
		// into the (ts, ledger)-only shape that loses rows sharing a
		// ledger.
		b64("100:1::" + lowerHex64 + ":0"),
		// Bad tx_hash format (63 chars, missing one).
		b64("100:1:soroswap:" + lowerHex64[:63] + ":0"),
		// Uppercase hex tx_hash (canonical form is lowercase).
		b64("100:1:soroswap:" + uppercaseHex + ":0"),
	} {
		resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD&cursor="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("cursor=%q: status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestHistory_EmptyListReturnsEmptyArray(t *testing.T) {
	// No trades in the window → empty array, not null.
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: nil}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	var parsed struct {
		Data []v1.TradeRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, body)
	}
	if parsed.Data == nil {
		t.Error("empty result should be [] not null")
	}
}

// ─── /v1/history/since-inception ────────────────────────────────

func TestHistorySinceInception_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception?asset=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHistorySinceInception_MissingAsset400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestHistorySinceInception_BadGranularity400 confirms unknown
// granularity surfaces as 400 — the reader returns
// ErrUnknownGranularity, the handler maps to 400 problem+json.
func TestHistorySinceInception_BadGranularity400(t *testing.T) {
	reader := &stubHistoryReader{pointsErr: v1.ErrUnknownGranularity}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception?asset=native&granularity=2h")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown granularity", resp.StatusCode)
	}
}

// TestHistorySinceInception_HappyPath verifies the wire shape:
// envelope wraps a HistorySeries; points are an array of
// {t, p, v_usd?}; the granularity defaults to 1d when omitted.
func TestHistorySinceInception_HappyPath(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	v := "1234.56"
	reader := &stubHistoryReader{
		points: []v1.HistoryPoint{
			{Bucket: t0, VWAP: "0.123", VolumeUSD: &v},
			{Bucket: t0.Add(24 * time.Hour), VWAP: "0.124"},
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.HistorySeries `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.AssetID != "native" {
		t.Errorf("asset_id = %q, want native", env.Data.AssetID)
	}
	if env.Data.Quote != "fiat:USD" {
		t.Errorf("quote = %q, want fiat:USD", env.Data.Quote)
	}
	if env.Data.Granularity != "1d" {
		t.Errorf("granularity default = %q, want 1d", env.Data.Granularity)
	}
	if env.Data.PriceType != "vwap" {
		t.Errorf("price_type = %q, want vwap", env.Data.PriceType)
	}
	if len(env.Data.Points) != 2 {
		t.Fatalf("got %d points, want 2", len(env.Data.Points))
	}
	if env.Data.Points[0].P != "0.123" || env.Data.Points[1].P != "0.124" {
		t.Errorf("points prices: %+v", env.Data.Points)
	}
	// VolumeUSD is *string so empty is null in JSON; second point omits it.
	if env.Data.Points[0].VUSD == nil || *env.Data.Points[0].VUSD != "1234.56" {
		t.Errorf("point[0].v_usd = %+v, want pointer to '1234.56'", env.Data.Points[0].VUSD)
	}
	if env.Data.Points[1].VUSD != nil {
		t.Errorf("point[1].v_usd = %+v, want nil (omitted)", env.Data.Points[1].VUSD)
	}

	if reader.lastCall.granularity != "1d" {
		t.Errorf("reader saw granularity=%q, want default-resolved 1d", reader.lastCall.granularity)
	}
}

// TestHistorySinceInception_StablecoinFallback pins F-1225 (codex
// audit-2026-05-12): when the literal X/fiat:USD CAGG read returns
// empty (because no on-chain trades quote in fiat:USD), the handler
// retries against each operator-declared classic USD-peg and serves
// the first non-empty result. Without this, since-inception
// XLM/fiat:USD returns empty even when XLM/<USDC-classic> has data.
func TestHistorySinceInception_StablecoinFallback(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	usdcG := "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	reader := &stubHistoryReader{
		pointsByPair: map[string][]v1.HistoryPoint{
			// Literal pair: empty (the case where the bug bit).
			"native/fiat:USD": nil,
			// Proxied pair: has data.
			"native/" + usdcG: {
				{Bucket: t0, VWAP: "0.123"},
				{Bucket: t0.Add(24 * time.Hour), VWAP: "0.124"},
			},
		},
	}
	classicUSDC, _ := canonical.ParseAsset(usdcG)
	srv := v1.New(v1.Options{
		History:           reader,
		USDPeggedClassics: []canonical.Asset{classicUSDC},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.HistorySeries `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data.Points) != 2 {
		t.Fatalf("got %d points, want 2 (fallback should have served XLM/<USDC> data)", len(env.Data.Points))
	}
	// Wire shape still reports the requested quote, not the proxy peg —
	// the user asked for fiat:USD and the snapshot is interpreted as
	// USD via the implicit peg ≈ $1 assumption.
	if env.Data.Quote != "fiat:USD" {
		t.Errorf("quote = %q, want fiat:USD (unchanged by fallback)", env.Data.Quote)
	}
}

// TestHistorySinceInception_GranularityForwarded confirms a
// non-default granularity reaches the reader unchanged. Catches a
// regression where the handler accidentally rewrites the param.
func TestHistorySinceInception_GranularityForwarded(t *testing.T) {
	reader := &stubHistoryReader{points: []v1.HistoryPoint{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/history/since-inception?asset=native&granularity=15m")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if reader.lastCall.granularity != "15m" {
		t.Errorf("reader saw granularity=%q, want 15m", reader.lastCall.granularity)
	}
}
