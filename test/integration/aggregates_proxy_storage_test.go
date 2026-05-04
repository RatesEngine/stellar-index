//go:build integration

// Integration tests for the prices_1m_proxy storage methods shipped
// alongside ADR-0025 phase 1. The migration adds an empty hypertable;
// these tests verify the store methods round-trip and honour the
// closed-bucket guard.

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestUpsertProxyVWAP_RoundTrips — write one row, read it back via
// LatestClosedVWAP1mForPairProxy. Pair the operator-friendly
// happy-path test with the closed-bucket guard so a row written
// for the future doesn't surface to a present-time read.
func TestUpsertProxyVWAP_RoundTrips(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Aligned to the 1-minute grid AND in the past so the
	// `bucket + 1 min <= now()` guard accepts it.
	bucket := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Minute)
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)

	row := timescale.Vwap1mProxyRow{
		Bucket:     bucket,
		BaseAsset:  "native",
		QuoteAsset: "fiat:USD",
		VWAP:       "0.157352774926",
		TWAP:       "0.157321890000",
		Volume:     "12345.6789",
		VolumeUSD:  "2954.526",
		TradeCount: 42,
		Sources:    []string{"soroswap", "sdex"},
	}
	if err := store.UpsertProxyVWAP(ctx, row); err != nil {
		t.Fatalf("UpsertProxyVWAP: %v", err)
	}

	got, err := store.LatestClosedVWAP1mForPairProxy(ctx, pair)
	if err != nil {
		t.Fatalf("LatestClosedVWAP1mForPairProxy: %v", err)
	}
	if got.BaseAsset != "native" || got.QuoteAsset != "fiat:USD" {
		t.Errorf("base/quote = %s/%s, want native/fiat:USD", got.BaseAsset, got.QuoteAsset)
	}
	if got.VWAP != "0.157352774926" {
		t.Errorf("VWAP = %q, want %q", got.VWAP, "0.157352774926")
	}
	if got.TradeCount != 42 {
		t.Errorf("TradeCount = %d, want 42", got.TradeCount)
	}
	if len(got.Sources) != 2 {
		t.Errorf("Sources = %v, want [soroswap sdex]", got.Sources)
	}

	// Idempotent — re-upsert with a new VWAP, the read sees the new
	// value (matches the orchestrator's restart-replay behaviour).
	row.VWAP = "0.158000000000"
	row.TradeCount = 50
	if err := store.UpsertProxyVWAP(ctx, row); err != nil {
		t.Fatalf("UpsertProxyVWAP (idempotent re-write): %v", err)
	}
	got, err = store.LatestClosedVWAP1mForPairProxy(ctx, pair)
	if err != nil {
		t.Fatalf("LatestClosedVWAP1mForPairProxy after re-upsert: %v", err)
	}
	if got.VWAP != "0.158000000000" || got.TradeCount != 50 {
		t.Errorf("re-upsert didn't take: VWAP=%q tradeCount=%d", got.VWAP, got.TradeCount)
	}

	// Empty-pair read returns sql.ErrNoRows. Same shape as the
	// literal-pair LatestClosedVWAP1mForPair API; the price-reader
	// fallback chain treats this as "no rewritten data, try Redis next."
	xlm2, _ := canonical.ParseAsset("crypto:XLM")
	eur, _ := canonical.ParseAsset("fiat:EUR")
	pair2, _ := canonical.NewPair(xlm2, eur)
	if _, err := store.LatestClosedVWAP1mForPairProxy(ctx, pair2); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("LatestClosedVWAP1mForPairProxy on empty pair: err = %v, want sql.ErrNoRows", err)
	}
}

// TestLatestClosedVWAP1mForPairProxy_OpenBucketExcluded — a row
// written for the current 1-minute bucket (bucket + 1 min > now)
// must NOT be returned. ADR-0015's "we serve only closed buckets"
// invariant applies to the proxy table just as it does to
// prices_1m.
func TestLatestClosedVWAP1mForPairProxy_OpenBucketExcluded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	openBucket := now.Truncate(time.Minute) // current 1-min bucket
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)

	if err := store.UpsertProxyVWAP(ctx, timescale.Vwap1mProxyRow{
		Bucket:     openBucket,
		BaseAsset:  "native",
		QuoteAsset: "fiat:USD",
		VWAP:       "0.157",
		Volume:     "1",
		TradeCount: 1,
		Sources:    []string{"soroswap"},
	}); err != nil {
		t.Fatalf("UpsertProxyVWAP: %v", err)
	}

	if _, err := store.LatestClosedVWAP1mForPairProxy(ctx, pair); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows (open-bucket row must be excluded)", err)
	}
}

// TestTimedVWAPs1mForChangeSummaryProxy_OldestFirst — the
// change-summary worker requires oldest-first ordering so its
// d30/d7/h24/h1 cutoff scan walks forward. Pin this for the
// rewritten table; the literal-pair version already has its own
// test elsewhere.
func TestTimedVWAPs1mForChangeSummaryProxy_OldestFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	now := time.Now().UTC()

	// Insert three rows out of order to confirm the SQL ORDER BY.
	for _, off := range []int{-30, -10, -20} {
		bucket := now.Add(time.Duration(off) * time.Minute).Truncate(time.Minute)
		if err := store.UpsertProxyVWAP(ctx, timescale.Vwap1mProxyRow{
			Bucket:     bucket,
			BaseAsset:  "native",
			QuoteAsset: "fiat:USD",
			VWAP:       "0.157",
			Volume:     "1",
			TradeCount: 1,
			Sources:    []string{"soroswap"},
		}); err != nil {
			t.Fatalf("UpsertProxyVWAP off=%d: %v", off, err)
		}
	}

	got, err := store.TimedVWAPs1mForChangeSummaryProxy(ctx, pair, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatalf("TimedVWAPs1mForChangeSummaryProxy: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].At.After(got[i-1].At) {
			t.Errorf("series not oldest-first at index %d: %v then %v",
				i, got[i-1].At, got[i].At)
		}
	}
}
