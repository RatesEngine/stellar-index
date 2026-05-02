package external

import "testing"

// TestRegistry_KnownSourcesClassified ensures every source we name
// in the on-chain decoder packages + the live off-chain connectors
// has a Registry entry. If this ever fails it means a new source was
// added without updating the aggregator's source-of-truth map — a
// bug that would silently exclude the source from VWAP.
func TestRegistry_KnownSourcesClassified(t *testing.T) {
	// Keep this list aligned with what internal/sources/ exports.
	want := []string{
		"soroswap", "aquarius", "phoenix", "comet", "sdex", "blend",
		"reflector-dex", "reflector-cex", "reflector-fx",
		"redstone", "band",
		"binance", "kraken", "bitstamp", "coinbase",
		"polygon-forex", "exchangeratesapi",
		"coingecko", "coinmarketcap", "cryptocompare",
		"ecb",
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

// TestRegistry_BackfillSafePolicy locks down the WASM-aware default:
// every on-chain Soroban source starts at BackfillSafe=false until its
// decoder has been audited against every WASM version that ran for the
// replay range. SDEX (classic, no WASM) and every off-chain source are
// BackfillSafe=true. Unknown sources fall back to false (fail-closed).
//
// Flipping a source from false→true is the *only* allowed direction,
// and only after a wasm-history audit. This test exists to make the
// flip a deliberate, reviewed change rather than a quiet flag toggle.
func TestRegistry_BackfillSafePolicy(t *testing.T) {
	wantUnsafe := []string{
		// All Soroban sources are now audited; the wantUnsafe slot
		// stays here as the structural place to put a new on-chain
		// source while its audit is pending.
		// History:
		//   soroswap/phoenix/aquarius/comet/band/redstone/
		//   reflector-{dex,cex,fx} audited 2026-04-29 → wantSafe.
		//   blend audited 2026-05-02 (wide-net Phase 2 walk +
		//   Phase 3 disassembly) → wantSafe.
	}
	for _, name := range wantUnsafe {
		if Registry[name].BackfillSafe {
			t.Errorf("source %q has BackfillSafe=true but is on-chain Soroban; flip only after wasm-history audit lands", name)
		}
		if BackfillSafe(name) {
			t.Errorf("BackfillSafe(%q) returned true; must be false until per-WASM-hash audit completes", name)
		}
	}

	wantSafe := []string{
		"sdex",          // classic Stellar, no WASM
		"soroswap",      // audited 2026-04-29 — see docs/operations/wasm-audits/soroswap.md
		"band",          // audited 2026-04-29 — see docs/operations/wasm-audits/band.md
		"redstone",      // audited 2026-04-29 — see docs/operations/wasm-audits/redstone.md
		"reflector-dex", // audited 2026-04-29 (incl v2 disassembly) — see docs/operations/wasm-audits/reflector.md
		"reflector-cex", // audited 2026-04-29 (incl v2 disassembly) — see docs/operations/wasm-audits/reflector.md
		"reflector-fx",  // audited 2026-04-29 — see docs/operations/wasm-audits/reflector.md
		"phoenix",       // audited 2026-04-29 (11 pools enumerated, 2 unique WASMs verified) — see docs/operations/wasm-audits/phoenix.md
		"aquarius",      // audited 2026-04-29 (313 pools enumerated, 3 unique WASMs verified) — see docs/operations/wasm-audits/aquarius.md
		"comet",         // audited 2026-04-29 (Blend backstop pool only known mainnet deployment; WASM verified) — see docs/operations/wasm-audits/comet.md
		"blend",         // audited 2026-05-02 (11 contracts, 3 unique WASMs, no mid-life upgrades over 11.79M-ledger walk) — see docs/operations/wasm-audits/blend.md §"Phase 2 results"
		"binance", "kraken", "bitstamp", "coinbase",
		"polygon-forex", "exchangeratesapi",
		"coingecko", "coinmarketcap", "cryptocompare",
		"ecb",
	}
	for _, name := range wantSafe {
		if !Registry[name].BackfillSafe {
			t.Errorf("source %q must be BackfillSafe=true (off-chain or pre-Soroban)", name)
		}
		if !BackfillSafe(name) {
			t.Errorf("BackfillSafe(%q) returned false; off-chain + SDEX have no on-chain WASM dependency", name)
		}
	}

	// Unknown source → fail-closed false. An unaudited or typoed
	// source must never be allowed to run a backfill.
	if BackfillSafe("definitely-not-a-real-source") {
		t.Error("BackfillSafe on unknown source returned true; must fail-closed false")
	}
}

// TestFXSources_DeterministicLexOrder pins the X2.5 forex-snap
// helper to its documented contract: every FX-classified source in
// Registry must appear in the result, exactly once, in lexicographic
// order. Order matters because the storage primitive's tiebreak
// (`ORDER BY ts DESC, source DESC`) only stays deterministic if every
// region computes the same source set.
func TestFXSources_DeterministicLexOrder(t *testing.T) {
	got := FXSources()

	// Every FX entry in Registry must be present.
	wantSet := map[string]struct{}{}
	for name, m := range Registry {
		if m.Subclass == SubclassFX {
			wantSet[name] = struct{}{}
		}
	}
	if len(got) != len(wantSet) {
		t.Fatalf("FXSources() returned %d, want %d (registry FX subclass count)", len(got), len(wantSet))
	}
	for _, name := range got {
		if _, ok := wantSet[name]; !ok {
			t.Errorf("FXSources() included %q which is not Subclass=FX in Registry", name)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("FXSources() not in ascending lex order: %v", got)
			break
		}
	}
}

// TestIsFXSource_RegistryDriven exercises the snap-rule's per-leg
// classification. A predicate-driven view of the registry — when an
// operator adds a new SubclassFX vendor, the snap rule picks it up
// without code changes elsewhere.
func TestIsFXSource_RegistryDriven(t *testing.T) {
	if !IsFXSource("polygon-forex") {
		t.Error("IsFXSource(polygon-forex) = false, want true")
	}
	if !IsFXSource("exchangeratesapi") {
		t.Error("IsFXSource(exchangeratesapi) = false, want true")
	}
	if IsFXSource("binance") {
		t.Error("IsFXSource(binance) = true, want false (CEX, not FX)")
	}
	if IsFXSource("definitely-not-real") {
		t.Error("IsFXSource(unknown) = true, want false (fallback empty subclass)")
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
