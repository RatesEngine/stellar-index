package chainlink

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// DefaultFeedMap returns the built-in operator-facing default seed —
// the same 6 majors `internal/divergence/chainlink.go` ships, kept
// in sync intentionally so the ingest source and divergence
// cross-check cover the same baseline pairs out of the box.
//
// Operators add more feeds via TOML; this function only fires when
// the operator left [external.chainlink].feed_map empty.
//
// Returns the operator-facing shape — keys are pair strings, values
// are the (address, decimals, invert) triple — so callers can pass
// it straight into [BuildFeedSet]. Decimals=8 throughout (Chainlink's
// standard for every entry here).
//
// Verified against https://docs.chain.link/data-feeds/price-feeds/addresses
// as of 2026-05-14.
func DefaultFeedMap() map[string]FeedSpec {
	return map[string]FeedSpec{
		"crypto:BTC/fiat:USD":  {Address: "0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c", Decimals: 8},
		"crypto:ETH/fiat:USD":  {Address: "0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419", Decimals: 8},
		"crypto:LINK/fiat:USD": {Address: "0x2c1d072e956AFFC0D435Cb7AC38EF18d24d9127c", Decimals: 8},
		"fiat:EUR/fiat:USD":    {Address: "0xb49f677943BC038e9857d61E7d053CaA2C1734C1", Decimals: 8},
		"fiat:GBP/fiat:USD":    {Address: "0x5c0Ab2d9b5a7ed9f470386e82BB36A3613cDd4b5", Decimals: 8},
		"fiat:JPY/fiat:USD":    {Address: "0xBcE206caE7f0ec07b545EddE332A47C2F75bbeb3", Decimals: 8},
	}
}

// BuildFeedSet parses operator-supplied feed-map entries (pair
// string → FeedSpec) into a runtime feed map and the canonical pair
// list the framework's runner expects. Empty input → fall back to
// [DefaultFeedMap] so a `enabled = true` config without a
// feed_map still does useful work.
//
// Returns an error on a pair string that fails canonical parsing —
// silent skips would hide misconfiguration.
//
// Used by both the indexer (live poller) and ratesengine-ops
// (backfill subcommand) so the same operator TOML drives both
// paths.
func BuildFeedSet(operatorMap map[string]FeedSpec) (map[string]FeedSpec, []canonical.Pair, error) {
	source := operatorMap
	if len(source) == 0 {
		source = DefaultFeedMap()
	}
	out := make(map[string]FeedSpec, len(source))
	pairs := make([]canonical.Pair, 0, len(source))
	for pairStr, setting := range source {
		p, err := canonical.ParsePair(pairStr)
		if err != nil {
			return nil, nil, fmt.Errorf("feed_map key %q: %w", pairStr, err)
		}
		dec := setting.Decimals
		if dec == 0 {
			dec = DefaultDecimals
		}
		out[p.String()] = FeedSpec{
			Address:  setting.Address,
			Decimals: dec,
			Invert:   setting.Invert,
		}
		pairs = append(pairs, p)
	}
	return out, pairs, nil
}
