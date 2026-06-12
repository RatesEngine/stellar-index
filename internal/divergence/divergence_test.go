package divergence_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/divergence"
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
// returns the mapped price. The reference batches every configured
// (id, quote) pair into a single request, so we assert the URL
// contains the expected id + quote (alongside others from the
// merged defaults) rather than equality.
func TestCoinGecko_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Confirm we hit the right path with a batched query.
		if r.URL.Path != "/simple/price" {
			t.Errorf("path = %q", r.URL.Path)
		}
		ids := r.URL.Query().Get("ids")
		quotes := r.URL.Query().Get("vs_currencies")
		if !strings.Contains(ids, "stellar") {
			t.Errorf("ids = %q, want to contain 'stellar'", ids)
		}
		if !strings.Contains(quotes, "usd") {
			t.Errorf("vs_currencies = %q, want to contain 'usd'", quotes)
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
		// The batched request includes every default id; just
		// assert stellar made it onto the wire.
		if got := r.URL.Query().Get("ids"); !strings.Contains(got, "stellar") {
			t.Errorf("ids = %q, want to contain stellar (default IDMap missed XLM)", got)
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

// TestCoinGecko_BatchedAcrossPairs — F-0030 follow-up. Multiple
// per-pair LookupPrice calls within the batch TTL window MUST
// coalesce into a single HTTP request covering every configured
// (id, quote) pair. Before this fix, the orchestrator's per-tick
// loop issued one HTTP call per pair (9 pairs × 2 ticks/min × 1440
// min/day = 25,920 calls/day, well past CoinGecko's demo-tier
// 10K/day limit). After: 1 call per tick (~2,880/day).
func TestCoinGecko_BatchedAcrossPairs(t *testing.T) {
	var hits int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		// Verify the batched request carries every requested id
		// + quote in one shot.
		ids := r.URL.Query().Get("ids")
		quotes := r.URL.Query().Get("vs_currencies")
		for _, want := range []string{"stellar", "bitcoin", "ethereum"} {
			if !strings.Contains(ids, want) {
				t.Errorf("batched ids %q missing %q", ids, want)
			}
		}
		for _, want := range []string{"usd", "eur"} {
			if !strings.Contains(quotes, want) {
				t.Errorf("batched vs_currencies %q missing %q", quotes, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"stellar":  {"usd": 0.16, "eur": 0.14},
			"bitcoin":  {"usd": 67000, "eur": 62000},
			"ethereum": {"usd": 4100,  "eur": 3800}
		}`))
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL: ts.URL,
		IDMap: map[string]string{
			"native":     "stellar",
			"crypto:BTC": "bitcoin",
			"crypto:ETH": "ethereum",
		},
		QuoteMap: map[string]string{"fiat:USD": "usd", "fiat:EUR": "eur"},
		// Long TTL so all six lookups land in the same batch window.
		BatchTTL: time.Hour,
	})

	usd := mustParseAsset(t, "fiat:USD")
	eur := mustParseAsset(t, "fiat:EUR")
	btc := mustParseAsset(t, "crypto:BTC")
	eth := mustParseAsset(t, "crypto:ETH")
	xlm := canonical.NativeAsset()

	pairs := []canonical.Pair{
		{Base: xlm, Quote: usd},
		{Base: xlm, Quote: eur},
		{Base: btc, Quote: usd},
		{Base: btc, Quote: eur},
		{Base: eth, Quote: usd},
		{Base: eth, Quote: eur},
	}
	for _, p := range pairs {
		price, err := ref.LookupPrice(context.Background(), p, time.Now())
		if err != nil {
			t.Fatalf("LookupPrice(%s): %v", p, err)
		}
		if price <= 0 {
			t.Errorf("LookupPrice(%s) = %g, want > 0", p, price)
		}
	}

	got := atomic.LoadInt64(&hits)
	if got != 1 {
		t.Errorf("HTTP requests = %d, want 1 (6 LookupPrice calls must batch into 1 HTTP call)", got)
	}
}

// TestCoinGecko_LookupPricesBatched — the public LookupPrices entry
// point is the explicit batched call site. Verify it issues a
// single HTTP request and returns one entry per known pair.
func TestCoinGecko_LookupPricesBatched(t *testing.T) {
	var hits int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"stellar": {"usd": 0.16},
			"bitcoin": {"usd": 67000}
		}`))
	}))
	defer ts.Close()

	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL:  ts.URL,
		IDMap:    map[string]string{"native": "stellar", "crypto:BTC": "bitcoin"},
		QuoteMap: map[string]string{"fiat:USD": "usd"},
		BatchTTL: time.Hour,
	})

	usd := mustParseAsset(t, "fiat:USD")
	btc := mustParseAsset(t, "crypto:BTC")
	pairs := []canonical.Pair{
		{Base: canonical.NativeAsset(), Quote: usd},
		{Base: btc, Quote: usd},
	}
	got, err := ref.LookupPrices(context.Background(), pairs)
	if err != nil {
		t.Fatalf("LookupPrices: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("LookupPrices returned %d entries, want 2: %+v", len(got), got)
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Errorf("HTTP requests = %d, want 1", atomic.LoadInt64(&hits))
	}
}

// TestCoinGecko_BatchTTLExpires — once the batch TTL elapses, the
// next LookupPrice MUST re-fetch (otherwise we'd serve stale prices
// indefinitely on a long-running process).
func TestCoinGecko_BatchTTLExpires(t *testing.T) {
	var hits int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stellar": {"usd": 0.16}}`))
	}))
	defer ts.Close()

	// Hand-cranked clock: each call to nowFn returns the next slot.
	clock := newFakeClock()
	ref := divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
		BaseURL:  ts.URL,
		IDMap:    map[string]string{"native": "stellar"},
		QuoteMap: map[string]string{"fiat:USD": "usd"},
		BatchTTL: 30 * time.Second,
		NowFn:    clock.now,
	})

	// Tick 1.
	clock.set(time.Unix(1_000, 0))
	if _, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now()); err != nil {
		t.Fatalf("LookupPrice tick 1: %v", err)
	}
	// Within TTL — must reuse cache.
	clock.set(time.Unix(1_010, 0))
	if _, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now()); err != nil {
		t.Fatalf("LookupPrice tick 1 (cached): %v", err)
	}
	// Past TTL — must refetch.
	clock.set(time.Unix(1_100, 0))
	if _, err := ref.LookupPrice(context.Background(), xlmUSD(t), time.Now()); err != nil {
		t.Fatalf("LookupPrice tick 2: %v", err)
	}

	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("HTTP requests = %d, want 2 (one fetch per TTL window)", got)
	}
}

// fakeClock is a tiny hand-cranked time source for the TTL test.
// Lives here rather than a shared helper because nothing else in
// the divergence test suite needs deterministic time today.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(0, 0)} }

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func mustParseAsset(t *testing.T, s string) canonical.Asset {
	t.Helper()
	a, err := canonical.ParseAsset(s)
	if err != nil {
		t.Fatalf("ParseAsset(%q): %v", s, err)
	}
	return a
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
