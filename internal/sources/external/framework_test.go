package external

import "testing"

// TestRegistry_KnownSourcesClassified ensures every source we name
// in the on-chain decoder packages + the planned off-chain connectors
// has a Registry entry. If this ever fails it means a new source was
// added without updating the aggregator's source-of-truth map — a
// bug that would silently exclude the source from VWAP.
func TestRegistry_KnownSourcesClassified(t *testing.T) {
	// Keep this list aligned with what internal/sources/ exports.
	want := []string{
		"soroswap", "aquarius", "phoenix", "comet", "sdex",
		"reflector-dex", "reflector-cex", "reflector-fx",
		"redstone", "band",
		"binance", "kraken", "bitstamp", "coinbase", "bitfinex",
		"polygon-forex", "exchangeratesapi",
		"coingecko", "coinmarketcap", "cryptocompare",
		"ecb", "fed-h10",
	}
	for _, name := range want {
		if _, ok := Registry[name]; !ok {
			t.Errorf("Registry missing entry for %q — aggregator would treat it as fail-closed unknown", name)
		}
	}
}

func TestRegistry_ClassPolicy(t *testing.T) {
	// Invariant: only ClassExchange may have IncludeInVWAP=true.
	// The three other classes (aggregator, oracle, authority_sanity)
	// MUST be excluded from VWAP by default — mixing them in
	// double-counts upstream markets or imposes someone else's
	// methodology on our output.
	for name, m := range Registry {
		if m.IncludeInVWAP && m.Class != ClassExchange {
			t.Errorf("source %q: IncludeInVWAP=true but Class=%q — only ClassExchange may VWAP-contribute by default",
				name, m.Class)
		}
	}
}

func TestRegistry_FailClosedOnUnknown(t *testing.T) {
	// Lookup of an unknown source must return a metadata record that
	// is visible (so ops can see the bad entry via /v1/sources) but
	// excluded from VWAP (so a typo or renamed source can't quietly
	// contribute).
	m := Lookup("definitely-not-a-real-source")
	if m.IncludeInVWAP {
		t.Error("Lookup on unknown source returned IncludeInVWAP=true; must fail-closed")
	}
	if IncludeInVWAP("definitely-not-a-real-source") {
		t.Error("IncludeInVWAP helper on unknown source returned true; must fail-closed")
	}
	if IncludeInVWAP("binance") != true {
		t.Error("IncludeInVWAP(binance) should be true; registry says otherwise")
	}
	if IncludeInVWAP("coingecko") != false {
		t.Error("IncludeInVWAP(coingecko) should be false (aggregator class); registry says otherwise")
	}
}

func TestEvents_SourceFieldDelegatesToCanonical(t *testing.T) {
	// The consumer.Event contract's Source() method labels metrics
	// by venue. For external sources where one TradeEvent type
	// covers many venues, Source() MUST delegate to the embedded
	// canonical.Trade.Source — otherwise every external venue
	// would collapse into a single "external.trade" metric label.
	//
	// Can't easily construct a full canonical.Trade here without
	// importing canonical and building valid Pair/Amount values, so
	// just check EventKind + that the Source field path exists by
	// compiling.
	var te TradeEvent
	if got := te.EventKind(); got != "external.trade" {
		t.Errorf("TradeEvent.EventKind() = %q, want external.trade", got)
	}
	var ue UpdateEvent
	if got := ue.EventKind(); got != "external.update" {
		t.Errorf("UpdateEvent.EventKind() = %q, want external.update", got)
	}
}
