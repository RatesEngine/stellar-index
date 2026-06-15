package pipeline

import (
	"testing"

	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/sources/aquarius"
	"github.com/StellarIndex/stellar-index/internal/sources/band"
	"github.com/StellarIndex/stellar-index/internal/sources/blend"
	blend_backstop "github.com/StellarIndex/stellar-index/internal/sources/blend_backstop"
	"github.com/StellarIndex/stellar-index/internal/sources/cctp"
	"github.com/StellarIndex/stellar-index/internal/sources/comet"
	"github.com/StellarIndex/stellar-index/internal/sources/defindex"
	"github.com/StellarIndex/stellar-index/internal/sources/external"
	"github.com/StellarIndex/stellar-index/internal/sources/phoenix"
	"github.com/StellarIndex/stellar-index/internal/sources/redstone"
	"github.com/StellarIndex/stellar-index/internal/sources/reflector"
	"github.com/StellarIndex/stellar-index/internal/sources/rozo"
	"github.com/StellarIndex/stellar-index/internal/sources/sdex"
	sep41_supply "github.com/StellarIndex/stellar-index/internal/sources/sep41_supply"
	sep41_transfers "github.com/StellarIndex/stellar-index/internal/sources/sep41_transfers"
	"github.com/StellarIndex/stellar-index/internal/sources/soroswap"
)

// TestIsProjectedEvent_TableDriven pins the [IsProjectedEvent]
// dispatch table to the ADR-0032 projector contract. Every event
// emitted by a source `projector/registry.go::buildSource` returns
// must be projected=true; every event from out-of-scope sources
// (sdex, external CEX/FX, band, supply observers) must be
// projected=false.
//
// Drift between this table and `IsProjectedEvent`'s switch is a
// silent double-write bug class (or a silent dropped-event bug
// class, depending on direction). The lint guard at
// scripts/ci/lint-imports.sh already catches "new source added but
// not classify()'d"; this test adds the analogous gate for
// "new consumer.Event added but not classified for the
// projected/non-projected split."
func TestIsProjectedEvent_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		event     consumer.Event
		projected bool
	}{
		// ── Projected (projector writes; dispatcher skips Phase 4+) ──
		{"soroswap.TradeEvent", soroswap.TradeEvent{Trade: canonical.Trade{Source: "soroswap"}}, true},
		{"soroswap.SkimEvent", soroswap.SkimEvent{}, true},
		{"aquarius.TradeEvent", aquarius.TradeEvent{Trade: canonical.Trade{Source: "aquarius"}}, true},
		{"phoenix.TradeEvent", phoenix.TradeEvent{Trade: canonical.Trade{Source: "phoenix"}}, true},
		{"phoenix.LiquidityEvent", phoenix.LiquidityEvent{}, true},
		{"phoenix.StakeEvent", phoenix.StakeEvent{}, true},
		{"comet.TradeEvent", comet.TradeEvent{Trade: canonical.Trade{Source: "comet"}}, true},
		{"comet.LiquidityEvent", comet.LiquidityEvent{}, true},
		{"reflector.UpdateEvent", reflector.UpdateEvent{Update: canonical.OracleUpdate{Source: "reflector-dex"}}, true},
		{"redstone.UpdateEvent", redstone.UpdateEvent{Update: canonical.OracleUpdate{Source: "redstone"}}, true},
		{"blend.NewAuctionEvent", blend.NewAuctionEvent{}, true},
		{"blend.FillAuctionEvent", blend.FillAuctionEvent{}, true},
		{"blend.DeleteAuctionEvent", blend.DeleteAuctionEvent{}, true},
		{"blend.PositionEvent", blend.PositionEvent{}, true},
		{"blend.EmissionEvent", blend.EmissionEvent{}, true},
		{"blend.AdminEvent", blend.AdminEvent{}, true},
		{"blend_backstop.Event", blend_backstop.Event{}, true},
		{"cctp.Event", cctp.Event{}, true},
		{"rozo.Event", rozo.Event{}, true},
		{"defindex.Event", defindex.Event{}, true},
		{"defindex.VaultEvent", defindex.VaultEvent{}, true},
		{"sep41_supply.Event", sep41_supply.Event{}, true},
		{"sep41_transfers.Event", sep41_transfers.Event{}, true},

		// ── Non-projected (dispatcher continues to write Phase 4+) ──
		{"sdex.TradeEvent", sdex.TradeEvent{Trade: canonical.Trade{Source: "sdex"}}, false},
		{"external.TradeEvent", external.TradeEvent{Trade: canonical.Trade{Source: "binance"}}, false},
		{"external.UpdateEvent", external.UpdateEvent{Update: canonical.OracleUpdate{Source: "ecb"}}, false},
		{"band.UpdateEvent", band.UpdateEvent{Update: canonical.OracleUpdate{Source: "band"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsProjectedEvent(tc.event)
			if got != tc.projected {
				t.Errorf("IsProjectedEvent(%T) = %v; want %v", tc.event, got, tc.projected)
			}
		})
	}
}
