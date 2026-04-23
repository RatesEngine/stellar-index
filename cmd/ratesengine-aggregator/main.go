// Binary ratesengine-aggregator computes VWAP, TWAP, triangulated
// prices, and OHLC continuous aggregates over the ingested
// canonical trade stream.
//
// Phase-1 status: skeleton only. Full wiring lands in Week 4-6 of
// the delivery plan.
//
// See docs/discovery/delivery-plan.md §Week 4-6.
//
// ⚠ CAGG TWAP CAVEAT ⚠
//
// Migration 0002 defines a `twap` column in prices_1m / _15m / _1h /
// _4h / _1d / _1w / _1mo as `avg(quote_amount / base_amount)` — the
// arithmetic mean of observed trade prices, NOT a time-weighted
// average. True TWAP weights each price by the duration it was the
// most-recent observation; that requires inter-trade intervals
// (LAG-based SQL or a window function), which the CAGG definitions
// don't include.
//
// Until migration 0002 is superseded by a corrected CAGG, aggregator
// code reading `SELECT twap FROM prices_*` is returning mean, not
// TWAP. Options:
//
//  1. Compute true TWAP in Go via internal/aggregate/twap.go from raw
//     trades (slower; O(N) per query, but correct).
//  2. Ship a replacement CAGG (new migration) that stores first_price,
//     last_price, per-bucket duration, and integer-summed
//     (price × duration) columns — then derive true TWAP at query
//     time by dividing summed-duration-price by summed-duration.
//  3. Rename the CAGG column to `mean_price` and document the
//     distinction in API responses.
//
// Pre-aggregator landing is the right time to pick one — no consumer
// depends on the mislabeled column yet.
package main

import (
	"fmt"
	"os"

	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	// TODO(#0): wire up the aggregator — rolling-window VWAP/TWAP,
	// outlier filtering, triangulation, continuous-aggregate
	// refresh scheduling.
	//
	// Exit non-zero deliberately: a k8s Deployment that pulled this
	// image would otherwise see the container exit cleanly and mark
	// the pod Completed — looking deployed-and-working while doing
	// nothing. CrashLoopBackOff is the honest signal for "not yet
	// implemented".
	fmt.Fprintf(os.Stderr, "ratesengine-aggregator %s — not yet implemented\n", version.String())
	os.Exit(1)
}
