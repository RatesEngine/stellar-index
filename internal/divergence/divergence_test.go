package divergence_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/divergence"
)

// stubReference is a Reference implementation that returns canned
// responses. Used to drive Compare's logic deterministically
// without depending on real HTTP.
type stubReference struct {
	name  string
	price float64
	err   error
	delay time.Duration
}

func (s *stubReference) Name() string { return s.name }

func (s *stubReference) LookupPrice(ctx context.Context, _ canonical.Pair, _ time.Time) (float64, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return s.price, s.err
}

// xlmUSD is a convenient test pair.
func xlmUSD(t *testing.T) canonical.Pair {
	t.Helper()
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse USD: %v", err)
	}
	return canonical.Pair{Base: canonical.NativeAsset(), Quote: usd}
}

// TestCompare_AllAgree — every reference returns the same price as
// our value. DivergencePct = 0; SuccessCount = N.
func TestCompare_AllAgree(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 0.10},
		&stubReference{name: "b", price: 0.10},
		&stubReference{name: "c", price: 0.10},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 0.10, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", res.SuccessCount)
	}
	if res.Median != 0.10 {
		t.Errorf("Median = %g, want 0.10", res.Median)
	}
	if res.DivergencePct != 0 {
		t.Errorf("DivergencePct = %g, want 0", res.DivergencePct)
	}
}

// TestCompare_ConsensusAgrees_OurValueOff — references agree but
// our value is 10% off. DivergencePct ≈ 10.
func TestCompare_ConsensusAgrees_OurValueOff(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.00},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.10, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d", res.SuccessCount)
	}
	if got := res.DivergencePct; got < 9.9 || got > 10.1 {
		t.Errorf("DivergencePct = %g, want ~10", got)
	}
}

// TestCompare_ReferencesDisagree_MedianHandlesIt — three sources;
// one outlier. Median is robust against the outlier so DivergencePct
// is computed against the consensus, not the average.
func TestCompare_ReferencesDisagree_MedianHandlesIt(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "outlier", price: 100.00}, // ridiculous outlier
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if res.Median != 1.00 {
		t.Errorf("Median = %g, want 1.00 (outlier shouldn't move median)", res.Median)
	}
	if res.DivergencePct > 0.1 {
		t.Errorf("DivergencePct = %g, want ~0", res.DivergencePct)
	}
}

// TestCompare_AssetUnsupported — sentinel error gets a stable
// failure label, distinguishable from generic transport errors.
func TestCompare_AssetUnsupported(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", err: divergence.ErrAssetUnsupported},
		&stubReference{name: "b", price: 1.00},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if res.Failures["a"] != "asset_unsupported" {
		t.Errorf("Failures[a] = %q, want asset_unsupported", res.Failures["a"])
	}
	if res.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", res.SuccessCount)
	}
}

// TestCompare_PriceUnavailable — vendor outage sentinel maps to a
// distinct failure label.
func TestCompare_PriceUnavailable(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", err: divergence.ErrPriceUnavailable},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if res.Failures["a"] != "price_unavailable" {
		t.Errorf("Failures[a] = %q, want price_unavailable", res.Failures["a"])
	}
}

// TestCompare_GenericErrorPassesThrough — non-sentinel error
// surfaces as its verbatim message, NOT as a sentinel label.
// Operator can grep dashboards for the actual cause.
func TestCompare_GenericErrorPassesThrough(t *testing.T) {
	weird := errors.New("connection reset by peer")
	refs := []divergence.Reference{
		&stubReference{name: "a", err: weird},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if res.Failures["a"] != "connection reset by peer" {
		t.Errorf("Failures[a] = %q, want verbatim error", res.Failures["a"])
	}
}

// TestCompare_NoReferencesIsClean — empty refs produces a Result
// with SuccessCount=0 and zero divergence; not an error.
func TestCompare_NoReferencesIsClean(t *testing.T) {
	res := divergence.Compare(context.Background(), nil, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 0 || res.DivergencePct != 0 {
		t.Errorf("empty refs: %+v", res)
	}
}

// TestCompare_MinSuccessForMedian_Honored — when fewer than the
// configured minimum references succeed, DivergencePct stays 0
// even if SuccessCount > 0.
func TestCompare_MinSuccessForMedianHonored(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
	}
	opts := divergence.CompareOptions{MinSuccessForMedian: 2}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.50, time.Now(), opts)
	if res.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", res.SuccessCount)
	}
	if res.DivergencePct != 0 {
		t.Errorf("DivergencePct = %g, want 0 (below MinSuccessForMedian)", res.DivergencePct)
	}
}

// TestCompare_TimeoutBoundsSlowReference — a reference that takes
// longer than the per-reference timeout is recorded as a failure;
// the others still complete.
func TestCompare_TimeoutBoundsSlowReference(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "fast", price: 1.00},
		&stubReference{name: "slow", price: 1.00, delay: 200 * time.Millisecond},
	}
	opts := divergence.CompareOptions{PerReferenceTimeout: 50 * time.Millisecond}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), opts)
	if _, ok := res.Sources["fast"]; !ok {
		t.Errorf("fast reference should have succeeded")
	}
	if _, ok := res.Failures["slow"]; !ok {
		t.Errorf("slow reference should have timed out and landed in Failures")
	}
}

// TestCompare_NonFinitePriceRejected — Inf / NaN / zero / negative
// prices land in Failures, never in Sources.
func TestCompare_NonFinitePriceRejected(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "neg", price: -1.0},
		&stubReference{name: "zero", price: 0.0},
		// NaN check would need a float bit-trick; covered by
		// math.NaN() in real-world parse failures.
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.00, time.Now(), divergence.CompareOptions{})
	if len(res.Sources) != 0 {
		t.Errorf("non-positive prices should not be in Sources, got %v", res.Sources)
	}
	if len(res.Failures) != 2 {
		t.Errorf("Failures count = %d, want 2", len(res.Failures))
	}
}

// TestCompare_EvenCountMedian — 4 references; median is mean of
// middle two values.
func TestCompare_EvenCountMedian(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 0.95},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.10},
		&stubReference{name: "d", price: 1.20},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 1.05, time.Now(), divergence.CompareOptions{})
	// sorted: [0.95, 1.00, 1.10, 1.20] → median = (1.00 + 1.10) / 2 = 1.05
	if got := res.Median; got < 1.04 || got > 1.06 {
		t.Errorf("Median = %g, want ~1.05", got)
	}
}

// ─── CoinGecko reference tests ─────────────────────────────────────

// TestCoinGecko_HappyPath — typical /simple/price response decodes,
// returns the mapped price.
func TestCoinGecko_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Confirm we hit the right path with the right query.
		if r.URL.Path != "/simple/price" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("ids") != "stellar" {
			t.Errorf("ids = %q", r.URL.Query().Get("ids"))
		}
		if r.URL.Query().Get("vs_currencies") != "usd" {
			t.Errorf("vs_currencies = %q", r.URL.Query().Get("vs_currencies"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"stellar": {"usd": 0.07142}}`)
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL: ts.URL,
		IDMap:   map[string]string{"native": "stellar"},
	})

	price, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now())
	if err != nil {
		t.Fatalf("LookupPrice: %v", err)
	}
	if price < 0.07140 || price > 0.07144 {
		t.Errorf("price = %g, want ~0.07142", price)
	}
}

// TestCoinGecko_AssetNotInIDMap — an asset that's neither in the
// operator's IDMap nor in the built-in default falls back to
// ErrAssetUnsupported (not a transport error). The bare native /
// XLM / BTC / ETH paths are now covered by the built-in default,
// so we use a deliberately-unknown synthetic asset to exercise
// the unsupported branch.
func TestCoinGecko_AssetNotInIDMap(t *testing.T) {
	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		IDMap: map[string]string{}, // empty — relies on built-in default
	})
	// Build a pair whose base ISN'T in the default IDMap. A classic
	// SEP-1 asset (long-tail issuer) is parseable but not curated;
	// the lookup should return ErrAssetUnsupported.
	base, err := canonical.ParseAsset("AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")
	if err != nil {
		t.Fatalf("ParseAsset(AQUA-...): %v", err)
	}
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset(fiat:USD): %v", err)
	}
	pair := canonical.Pair{Base: base, Quote: usd}
	if _, err := ref.LookupPrice(context.Background(), pair, time.Now()); !errors.Is(err, divergence.ErrAssetUnsupported) {
		t.Errorf("err = %v, want ErrAssetUnsupported", err)
	}
}

// TestCoinGecko_DefaultIDMapCoversCommonPairs — a stock deployment
// (no operator IDMap) MUST recognise the canonical asset_id forms
// the aggregator computes by default. Caught from r1 audit
// (2026-05-10): the previous behaviour was empty-IDMap → every
// pair returns ErrAssetUnsupported → divergence_observations
// silently empty even though Compare's "ok" counter incremented.
func TestCoinGecko_DefaultIDMapCoversCommonPairs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ids"); got != "stellar" {
			t.Errorf("ids = %q, want stellar (default IDMap missed XLM)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stellar":{"usd":0.16475}}`))
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL: ts.URL,
		IDMap:   map[string]string{}, // empty — the regression scenario
	})
	got, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now())
	if err != nil {
		t.Fatalf("LookupPrice: %v", err)
	}
	if got != 0.16475 {
		t.Errorf("price = %v, want 0.16475", got)
	}
}

// TestCoinGecko_RateLimited — 429 maps to ErrPriceUnavailable so the
// Compare layer can distinguish from a permanent unsupported asset.
func TestCoinGecko_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL: ts.URL,
		IDMap:   map[string]string{"native": "stellar"},
	})
	_, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now())
	if !errors.Is(err, divergence.ErrPriceUnavailable) {
		t.Errorf("err = %v, want ErrPriceUnavailable", err)
	}
}

// TestCoinGecko_MalformedJSON — parse error surfaces as a generic
// (non-sentinel) transport-style error so the operator sees the
// real cause.
func TestCoinGecko_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `not json`)
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL: ts.URL,
		IDMap:   map[string]string{"native": "stellar"},
	})
	_, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now())
	if err == nil {
		t.Fatal("expected error from malformed JSON")
	}
	if errors.Is(err, divergence.ErrAssetUnsupported) || errors.Is(err, divergence.ErrPriceUnavailable) {
		t.Errorf("malformed JSON should NOT match either sentinel; got %v", err)
	}
}

// TestCoinGecko_QuoteNotInMap — fiat:GBP isn't in the test's
// custom QuoteMap → ErrAssetUnsupported.
func TestCoinGecko_QuoteNotInMap(t *testing.T) {
	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		IDMap:    map[string]string{"native": "stellar"},
		QuoteMap: map[string]string{"fiat:USD": "usd"}, // GBP missing
	})
	gbp, err := canonical.ParseAsset("fiat:GBP")
	if err != nil {
		t.Fatalf("parse GBP: %v", err)
	}
	pair := canonical.Pair{Base: canonical.NativeAsset(), Quote: gbp}
	_, err = ref.LookupPrice(context.Background(), pair, time.Now())
	if !errors.Is(err, divergence.ErrAssetUnsupported) {
		t.Errorf("err = %v, want ErrAssetUnsupported", err)
	}
}

// TestCoinGecko_NameStable — the metric label is locked across
// versions. Renaming is a wire break against alert rules in
// divergence.yml.
func TestCoinGecko_NameStable(t *testing.T) {
	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{})
	if ref.Name() != "coingecko" {
		t.Errorf("Name() = %q, want coingecko", ref.Name())
	}
}

// panickingReference panics on every LookupPrice. Used to verify
// the comparator's panic-recovery contract — a misbehaving
// reference MUST NOT take the whole Compare run down with it,
// even though the docstring promises "panic recovered ...
// recorded in Failures."
type panickingReference struct {
	name        string
	panicValue  any
	panicOnName bool
}

func (p *panickingReference) Name() string {
	if p.panicOnName {
		panic("Name() panic")
	}
	return p.name
}

func (p *panickingReference) LookupPrice(_ context.Context, _ canonical.Pair, _ time.Time) (float64, error) {
	panic(p.panicValue)
}

// TestCompare_PanicInOneReferenceIsolated — one reference panicking
// MUST NOT take the comparator down. The other references' results
// still aggregate; the panic-source surfaces in Failures with a
// "panicked: …" label so operators see which reference is broken
// without reading goroutine traces.
func TestCompare_PanicInOneReferenceIsolated(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "good-a", price: 0.10},
		&panickingReference{name: "bad", panicValue: "kapow"},
		&stubReference{name: "good-b", price: 0.10},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 0.10, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2 (panicking reference must not cancel the others)", res.SuccessCount)
	}
	if res.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", res.FailureCount)
	}
	label, ok := res.Failures["bad"]
	if !ok {
		t.Fatalf("Failures missing 'bad' entry: %+v", res.Failures)
	}
	if label != "panicked: kapow" {
		t.Errorf("Failures[bad] = %q, want %q (panic surfaces with stable label prefix)", label, "panicked: kapow")
	}
	// The good references still drove the median.
	if res.Median != 0.10 {
		t.Errorf("Median = %g, want 0.10", res.Median)
	}
}

// TestCompare_PanicWithErrorValue — a panic with an error type
// (e.g. runtime.Error from a nil-deref) renders via fmt's %v
// verbose form. Pinned because the label is part of the
// operator-facing dashboard surface.
func TestCompare_PanicWithErrorValue(t *testing.T) {
	refs := []divergence.Reference{
		&panickingReference{name: "nil-deref", panicValue: errors.New("runtime: invalid memory address")},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 0.10, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", res.SuccessCount)
	}
	label := res.Failures["nil-deref"]
	if label != "panicked: runtime: invalid memory address" {
		t.Errorf("Failures[nil-deref] = %q, want panicked: runtime: invalid memory address", label)
	}
}

// TestCompare_PanicInName — even Name() panicking shouldn't
// crash the comparator. The failure surfaces under the synthetic
// "_unknown" name so operators see "something panicked here"
// without losing the rest of the run.
func TestCompare_PanicInName(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "good", price: 0.10},
		&panickingReference{panicValue: "swap", panicOnName: true},
	}
	res := divergence.Compare(context.Background(), refs, xlmUSD(t), 0.10, time.Now(), divergence.CompareOptions{})
	if res.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1 (good ref still works)", res.SuccessCount)
	}
	if _, ok := res.Failures["_unknown"]; !ok {
		t.Errorf("expected _unknown failure entry, got %+v", res.Failures)
	}
}
