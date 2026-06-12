package divergence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/divergence"
)

// newTestService wires a Service against an in-memory miniredis +
// the supplied references. Returns the service, the redis client
// (for direct assertions), and the miniredis handle.
func newTestService(t *testing.T, refs []divergence.Reference, opts divergence.ServiceOptions) (*divergence.Service, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	opts.References = refs
	opts.Cache = rdb
	svc, err := divergence.NewService(opts)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, rdb, mr
}

// TestNewService_RequiresCache — operator misconfig that omits the
// cache should fail loudly at construction, not silently skip writes.
func TestNewService_RequiresCache(t *testing.T) {
	_, err := divergence.NewService(divergence.ServiceOptions{
		References: []divergence.Reference{&stubReference{name: "a", price: 1}},
	})
	if err == nil {
		t.Fatal("expected error when Cache is nil")
	}
}

// TestRefreshPair_NoReferencesIsNoop — empty References list yields
// no Redis writes and no error.
func TestRefreshPair_NoReferencesIsNoop(t *testing.T) {
	svc, _, mr := newTestService(t, nil, divergence.ServiceOptions{})
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.00, time.Now()); err != nil {
		t.Errorf("RefreshPair on empty refs: %v", err)
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("no-op should not write redis; got keys %v", keys)
	}
}

// TestRefreshPair_HappyPath — references agree with our value;
// CachedResult writes to Redis, WarningFired=false.
func TestRefreshPair_HappyPath(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.00},
	}
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{})

	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.00, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}

	body, err := rdb.Get(context.Background(), cachekeys.Divergence(xlmUSD(t))).Bytes()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	var cached divergence.CachedResult
	if err := json.Unmarshal(body, &cached); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cached.WarningFired {
		t.Errorf("WarningFired = true on consensus, want false")
	}
	if cached.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", cached.SuccessCount)
	}
	if cached.DivergencePct > 0.001 {
		t.Errorf("DivergencePct = %g, want ~0", cached.DivergencePct)
	}
}

// TestRefreshPair_FiresWarning — references agree on a price that
// disagrees with our value by > threshold; WarningFired=true.
// TestRefreshPair_OnWarningFiredEdgeOnly pins F-1249 (codex
// audit-2026-05-12): the OnWarningFired hook fires only on the
// `below threshold → above threshold` edge, not on every refresh
// while a divergence stays elevated. Multiple consecutive
// above-threshold refreshes must produce one hook call; a return
// to below-threshold + re-cross re-arms the latch.
func TestRefreshPair_OnWarningFiredEdgeOnly(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.00},
	}
	var fired int
	svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            5.0,
		MinSourcesForWarning: 2,
		OnWarningFired: func(_ context.Context, _ canonical.Pair, _ divergence.CachedResult) {
			fired++
		},
	})
	ctx := context.Background()
	// 1) Below threshold (1%) → no fire.
	_ = svc.RefreshPair(ctx, xlmUSD(t), 1.01, time.Now())
	if fired != 0 {
		t.Fatalf("fired=%d after below-threshold refresh, want 0", fired)
	}
	// 2) Above threshold (10%) → first fire.
	_ = svc.RefreshPair(ctx, xlmUSD(t), 1.10, time.Now())
	if fired != 1 {
		t.Fatalf("fired=%d after first above-threshold, want 1", fired)
	}
	// 3) Still above threshold → no second fire (latch held).
	_ = svc.RefreshPair(ctx, xlmUSD(t), 1.12, time.Now())
	if fired != 1 {
		t.Fatalf("fired=%d on still-elevated refresh, want 1 (latch should hold)", fired)
	}
	// 4) Drop below threshold → latch resets (no fire on the way down).
	_ = svc.RefreshPair(ctx, xlmUSD(t), 1.02, time.Now())
	if fired != 1 {
		t.Fatalf("fired=%d on recovery refresh, want 1", fired)
	}
	// 5) Re-cross threshold → second fire.
	_ = svc.RefreshPair(ctx, xlmUSD(t), 1.10, time.Now())
	if fired != 2 {
		t.Fatalf("fired=%d after re-cross, want 2 (latch should re-arm)", fired)
	}
}

func TestRefreshPair_FiresWarning(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.00},
	}
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            5.0, // 5% threshold
		MinSourcesForWarning: 2,
	})

	// Our price is 10% above the consensus.
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.10, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}

	body, err := rdb.Get(context.Background(), cachekeys.Divergence(xlmUSD(t))).Bytes()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	var cached divergence.CachedResult
	_ = json.Unmarshal(body, &cached)
	if !cached.WarningFired {
		t.Errorf("WarningFired = false on 10%% deviation, want true")
	}
}

// TestRefreshPair_BelowMinSourcesNoWarning — even when divergence
// is huge, fewer than MinSourcesForWarning successful references
// suppresses the warning. Single-source disagreement shouldn't fire.
func TestRefreshPair_BelowMinSourcesNoWarning(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "only", price: 1.00},
	}
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            5.0,
		MinSourcesForWarning: 2, // require 2+ agreeing sources
	})
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.50, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	body, _ := rdb.Get(context.Background(), cachekeys.Divergence(xlmUSD(t))).Bytes()
	var cached divergence.CachedResult
	_ = json.Unmarshal(body, &cached)
	if cached.WarningFired {
		t.Errorf("WarningFired = true with single source; should require ≥ 2")
	}
	// But the comparator's data should still be cached so operators
	// can see what one source thinks.
	if cached.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", cached.SuccessCount)
	}
}

// TestRefreshPair_TTLApplied — Redis TTL on the cache entry matches
// cachekeys.DivergenceTTL.
func TestRefreshPair_TTLApplied(t *testing.T) {
	refs := []divergence.Reference{&stubReference{name: "a", price: 1.00}}
	svc, _, mr := newTestService(t, refs, divergence.ServiceOptions{})

	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.00, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	ttl := mr.TTL(cachekeys.Divergence(xlmUSD(t)))
	if ttl == 0 || ttl > cachekeys.DivergenceTTL {
		t.Errorf("TTL = %v, want ≤ %v and > 0", ttl, cachekeys.DivergenceTTL)
	}
}

// TestLookupCached_PresentEntry — RefreshPair → LookupCached round
// trips the entry preserving every field.
func TestLookupCached_PresentEntry(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.05},
		&stubReference{name: "b", price: 1.05},
	}
	svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{Threshold: 1.0, MinSourcesForWarning: 2})

	pair := xlmUSD(t)
	if err := svc.RefreshPair(context.Background(), pair, 1.00, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}

	cached, found, err := svc.LookupCached(context.Background(), pair.Base)
	if err != nil {
		t.Fatalf("LookupCached: %v", err)
	}
	if !found {
		t.Fatal("LookupCached returned found=false on a freshly-cached entry")
	}
	if cached.PairID != pair.String() {
		t.Errorf("PairID = %q, want %q", cached.PairID, pair.String())
	}
	if cached.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", cached.SuccessCount)
	}
	// 1.05 vs 1.00 = ~4.76% deviation. Threshold 1.0% → warning fires.
	if !cached.WarningFired {
		t.Errorf("WarningFired = false, expected true (4.76%% > 1%% threshold)")
	}
}

// TestLookupCached_AbsentEntry — querying an asset with no cached
// result returns (zero, false, nil) — not an error.
func TestLookupCached_AbsentEntry(t *testing.T) {
	svc, _, _ := newTestService(t, nil, divergence.ServiceOptions{})
	_, found, err := svc.LookupCached(context.Background(), canonical.NativeAsset())
	if err != nil {
		t.Errorf("LookupCached on absent entry: %v", err)
	}
	if found {
		t.Errorf("found = true on absent entry")
	}
}

// xlmPair builds native/<quote-fiat> for the per-base-asset
// aggregation tests.
func xlmPair(t *testing.T, quoteFiat string) canonical.Pair {
	t.Helper()
	q, err := canonical.ParseAsset("fiat:" + quoteFiat)
	if err != nil {
		t.Fatalf("parse %s: %v", quoteFiat, err)
	}
	return canonical.Pair{Base: canonical.NativeAsset(), Quote: q}
}

// TestLookupCached_PerPairOR_OrderIndependent pins F-1344 (G16-03):
// the by-asset reader must report "firing if ANY quote diverges"
// regardless of the order the worker refreshes the base's pairs. The
// pre-fix per-base key let the LAST pair refreshed clobber the
// asset's verdict — so XLM/USD diverging but XLM/GBP not would clear
// the warning if GBP refreshed last. With per-pair keys + the OR
// across the base index, the verdict is stable.
func TestLookupCached_PerPairOR_OrderIndependent(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
	}
	xlm := canonical.NativeAsset()
	usdPair := xlmPair(t, "USD")
	gbpPair := xlmPair(t, "GBP")
	ctx := context.Background()

	// Two refresh orderings; both must yield firing=true for the base.
	// Ordering 1: USD (diverging) first, GBP (in-tolerance) last —
	// this is the regression case (GBP would have cleared the base key
	// under the pre-fix per-base layout).
	// Ordering 2: GBP first, USD last.
	type step struct {
		pair  canonical.Pair
		price float64
	}
	eurPair := xlmPair(t, "EUR")
	for _, tc := range []struct {
		name       string
		order      []step
		firingPair canonical.Pair // expected representative detail row
	}{
		{
			name:       "diverging_first_clean_last",
			order:      []step{{usdPair, 1.50}, {gbpPair, 1.00}},
			firingPair: usdPair,
		},
		{
			name:       "clean_first_diverging_last",
			order:      []step{{gbpPair, 1.00}, {usdPair, 1.50}},
			firingPair: usdPair,
		},
		{
			// Robustness against Redis set-iteration order: the FIRING
			// quote (EUR) sorts lexicographically BEFORE the clean
			// quote (USD), so miniredis's sorted SMembers returns the
			// clean pair LAST. A naive "last value wins" aggregation
			// (the pre-fix per-base clobber) would read the base
			// verdict as false here — only a true OR keeps it firing.
			name:       "firing_quote_sorts_first",
			order:      []step{{eurPair, 1.50}, {usdPair, 1.00}},
			firingPair: eurPair,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{
				Threshold:            5.0,
				MinSourcesForWarning: 2,
			})
			for _, s := range tc.order {
				if err := svc.RefreshPair(ctx, s.pair, s.price, time.Now()); err != nil {
					t.Fatalf("RefreshPair %s: %v", s.pair, err)
				}
			}

			cached, found, err := svc.LookupCached(ctx, xlm)
			if err != nil {
				t.Fatalf("LookupCached: %v", err)
			}
			if !found {
				t.Fatal("found=false after refreshing two pairs for the base")
			}
			if !cached.WarningFired {
				t.Errorf("WarningFired=false; want true — a quote diverges so the "+
					"base verdict must fire regardless of refresh / iteration order "+
					"(got pair_id=%q)", cached.PairID)
			}
			// The representative detail row should be the FIRING pair,
			// not the clean one — independent of Redis set order.
			if cached.PairID != tc.firingPair.String() {
				t.Errorf("representative PairID = %q, want the firing pair %q",
					cached.PairID, tc.firingPair.String())
			}
		})
	}
}

// TestLookupCached_PerPairOR_AllClean — when every quote is within
// tolerance the base verdict must be false (no false positives from
// the OR aggregation).
func TestLookupCached_PerPairOR_AllClean(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
	}
	svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            5.0,
		MinSourcesForWarning: 2,
	})
	ctx := context.Background()
	for _, p := range []canonical.Pair{xlmPair(t, "USD"), xlmPair(t, "GBP")} {
		if err := svc.RefreshPair(ctx, p, 1.00, time.Now()); err != nil {
			t.Fatalf("RefreshPair %s: %v", p, err)
		}
	}
	cached, found, err := svc.LookupCached(ctx, canonical.NativeAsset())
	if err != nil {
		t.Fatalf("LookupCached: %v", err)
	}
	if !found {
		t.Fatal("found=false after refreshing two clean pairs")
	}
	if cached.WarningFired {
		t.Errorf("WarningFired=true with every quote in-tolerance; want false")
	}
}

// TestRefreshPair_DefaultsApplied — zero-value options use sensible
// defaults: 5% threshold, 2 min-sources, 5s timeout.
func TestRefreshPair_DefaultsApplied(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
		&stubReference{name: "b", price: 1.00},
		&stubReference{name: "c", price: 1.00},
	}
	// Zero-value options: defaults should kick in (5% threshold,
	// 2 min sources). 4% deviation → no warning.
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{})
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.04, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	body, _ := rdb.Get(context.Background(), cachekeys.Divergence(xlmUSD(t))).Bytes()
	var cached divergence.CachedResult
	_ = json.Unmarshal(body, &cached)
	if cached.WarningFired {
		t.Errorf("4%% deviation should not fire under default 5%% threshold")
	}

	// 6% deviation → warning fires.
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.06, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	body, _ = rdb.Get(context.Background(), cachekeys.Divergence(xlmUSD(t))).Bytes()
	_ = json.Unmarshal(body, &cached)
	if !cached.WarningFired {
		t.Errorf("6%% deviation should fire under default 5%% threshold")
	}
}

// recordingObservationSink captures every RecordObservation call so
// tests can pin the worker fires per-reference rows.
type recordingObservationSink struct {
	records []divergence.ObservationRecord
}

func (r *recordingObservationSink) RecordObservation(_ context.Context, obs divergence.ObservationRecord) error {
	r.records = append(r.records, obs)
	return nil
}

// TestRefreshPair_FiresObservationSink — when a sink is wired, the
// worker must call it once per (pair, reference) tuple per refresh
// with the right deltas + firing flag.
func TestRefreshPair_FiresObservationSink(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "chainlink", price: 1.00},
		&stubReference{name: "coingecko", price: 1.00},
	}
	sink := &recordingObservationSink{}
	svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            5.0,
		MinSourcesForWarning: 1,
		ObservationSink:      sink,
	})

	// Our price is 10% above both refs — both observations should
	// be recorded with status=firing.
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.10, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}

	if len(sink.records) != 2 {
		t.Fatalf("sink got %d records, want 2 (one per reference)", len(sink.records))
	}
	for _, r := range sink.records {
		if !r.Firing {
			t.Errorf("ref %s: Firing=false, want true (10%% delta exceeds 5%% threshold)", r.Reference)
		}
		if r.OurPrice != 1.10 {
			t.Errorf("ref %s: OurPrice = %g, want 1.10", r.Reference, r.OurPrice)
		}
		if r.RefPrice != 1.00 {
			t.Errorf("ref %s: RefPrice = %g, want 1.00", r.Reference, r.RefPrice)
		}
		// (1.10 - 1.00) / 1.00 * 100 = 10
		if r.DeltaPct < 9.99 || r.DeltaPct > 10.01 {
			t.Errorf("ref %s: DeltaPct = %g, want ~10", r.Reference, r.DeltaPct)
		}
	}
}

// TestRefreshPair_NoSinkIsLegacyBehaviour — the pre-Phase-2 default
// (no sink) keeps the legacy Redis-only path working unchanged.
func TestRefreshPair_NoSinkIsLegacyBehaviour(t *testing.T) {
	refs := []divergence.Reference{
		&stubReference{name: "a", price: 1.00},
	}
	svc, _, _ := newTestService(t, refs, divergence.ServiceOptions{
		// ObservationSink: nil (default)
	})
	if err := svc.RefreshPair(context.Background(), xlmUSD(t), 1.00, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	// No sink, no records — but no panic, no error. Legacy
	// behaviour preserved.
}
