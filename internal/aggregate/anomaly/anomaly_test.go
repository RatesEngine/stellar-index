package anomaly_test

import (
	"math/big"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// rat is a small helper so the test cases read like prices.
func rat(x string) *big.Rat {
	r, ok := new(big.Rat).SetString(x)
	if !ok {
		panic("rat: cannot parse " + x)
	}
	return r
}

// usdcIssuer is a realistic G-strkey for tests that need a non-native asset.
const usdcIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func mustNewClassifier(t *testing.T, m map[string]anomaly.AssetClass) *anomaly.Classifier {
	t.Helper()
	return anomaly.NewClassifier(m)
}

func mustNewChecker(t *testing.T, c *anomaly.Classifier) *anomaly.Checker {
	t.Helper()
	chk, err := anomaly.NewChecker(anomaly.DefaultThresholds(), c)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return chk
}

func nativePair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm := canonical.NativeAsset()
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse USD: %v", err)
	}
	return canonical.Pair{Base: xlm, Quote: usd}
}

// TestClassifier_DefaultsToClassDefault — any unclassified asset
// must fall through to ClassDefault. The whole asset universe falls
// here at startup until operator config populates it.
func TestClassifier_DefaultsToClassDefault(t *testing.T) {
	c := mustNewClassifier(t, nil)
	got := c.ClassOf(canonical.NativeAsset())
	if got != anomaly.ClassDefault {
		t.Errorf("unclassified asset: got %q, want %q", got, anomaly.ClassDefault)
	}
}

// TestClassifier_OperatorOverride — explicit classification wins
// over the default.
func TestClassifier_OperatorOverride(t *testing.T) {
	xlm := canonical.NativeAsset()
	c := mustNewClassifier(t, map[string]anomaly.AssetClass{
		xlm.String(): anomaly.ClassCrypto,
	})
	if got := c.ClassOf(xlm); got != anomaly.ClassCrypto {
		t.Errorf("ClassOf(XLM) = %q, want crypto", got)
	}
}

// TestNewChecker_RejectsMissingDefault — Checker construction must
// require ClassDefault. Operator config that omits it would cause
// every unclassified asset to use… nothing, which would silently
// disable anomaly protection.
func TestNewChecker_RejectsMissingDefault(t *testing.T) {
	thresholds := map[anomaly.AssetClass]anomaly.Thresholds{
		anomaly.ClassStablecoin: {WarnPct: 1, FreezePct: 3},
	}
	_, err := anomaly.NewChecker(thresholds, anomaly.NewClassifier(nil))
	if err == nil {
		t.Fatal("NewChecker without ClassDefault: want error, got nil")
	}
}

// TestNewChecker_RejectsInvalidThresholds — FreezePct must be > WarnPct,
// both must be > 0. Operator misconfig that inverts these would mean
// freeze fires before warn (or never).
func TestNewChecker_RejectsInvalidThresholds(t *testing.T) {
	cases := []struct {
		name string
		t    anomaly.Thresholds
	}{
		{"zero warn", anomaly.Thresholds{WarnPct: 0, FreezePct: 5}},
		{"zero freeze", anomaly.Thresholds{WarnPct: 1, FreezePct: 0}},
		{"freeze < warn", anomaly.Thresholds{WarnPct: 5, FreezePct: 1}},
		{"freeze == warn", anomaly.Thresholds{WarnPct: 5, FreezePct: 5}},
		{"negative warn", anomaly.Thresholds{WarnPct: -1, FreezePct: 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			thresholds := map[anomaly.AssetClass]anomaly.Thresholds{
				anomaly.ClassDefault:    {WarnPct: 30, FreezePct: 75},
				anomaly.ClassStablecoin: tc.t,
			}
			_, err := anomaly.NewChecker(thresholds, anomaly.NewClassifier(nil))
			if err == nil {
				t.Errorf("NewChecker with invalid thresholds: want error, got nil")
			}
		})
	}
}

// TestEvaluate_NoPriorBucketAllows — first observation for a pair
// has nothing to compare against; we publish it.
func TestEvaluate_NoPriorBucketAllows(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    nil,
		CurrVWAP:    rat("1.00"),
		SourceCount: 1,
	})
	if d.Action != anomaly.ActionAllow {
		t.Errorf("first observation: action = %q, want allow", d.Action)
	}
}

// TestEvaluate_NilCurrVWAPFreezes — caller bug guard. A nil current
// VWAP shouldn't be passed in, but if it is, fail-safe to freeze so
// the upstream notices instead of silently publishing zero.
func TestEvaluate_NilCurrVWAPFreezes(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    nil,
		SourceCount: 1,
	})
	if d.Action != anomaly.ActionFreeze {
		t.Errorf("nil CurrVWAP: action = %q, want freeze", d.Action)
	}
}

// TestEvaluate_NormalDeviationAllows — small movements within the
// class's WarnPct don't fire any flag.
func TestEvaluate_NormalDeviationAllows(t *testing.T) {
	xlm := canonical.NativeAsset()
	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		xlm.String(): anomaly.ClassCrypto, // 20% warn, 50% freeze
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("1.05"), // 5% deviation, well under 20% warn
		SourceCount: 5,
	})
	if d.Action != anomaly.ActionAllow {
		t.Errorf("5%% crypto move: action = %q, want allow", d.Action)
	}
	if d.DeviationPct < 4.9 || d.DeviationPct > 5.1 {
		t.Errorf("DeviationPct = %g, want ~5", d.DeviationPct)
	}
}

// TestEvaluate_BetweenWarnAndFreezeWarns — deviation past WarnPct
// but under FreezePct triggers warn regardless of source count.
func TestEvaluate_BetweenWarnAndFreezeWarns(t *testing.T) {
	xlm := canonical.NativeAsset()
	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		xlm.String(): anomaly.ClassCrypto,
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("1.30"), // 30% — between 20% warn and 50% freeze
		SourceCount: 5,
	})
	if d.Action != anomaly.ActionWarn {
		t.Errorf("30%% crypto move: action = %q, want warn", d.Action)
	}
}

// TestEvaluate_AboveFreezeMultiSourceWarns — large deviation with
// multi-source corroboration is a real market event, not
// manipulation. Warn rather than freeze.
func TestEvaluate_AboveFreezeMultiSourceWarns(t *testing.T) {
	xlm := canonical.NativeAsset()
	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		xlm.String(): anomaly.ClassCrypto,
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("2.00"), // 100% — way above 50% freeze
		SourceCount: 8,           // multi-source agreement = real move
	})
	if d.Action != anomaly.ActionWarn {
		t.Errorf("100%% crypto move with 8 sources: action = %q, want warn", d.Action)
	}
}

// TestEvaluate_AboveFreezeSingleSourceFreezes — the USTRY scenario.
// Large deviation + single source → freeze.
func TestEvaluate_AboveFreezeSingleSourceFreezes(t *testing.T) {
	ustry, err := canonical.NewClassicAsset("USTRY", usdcIssuer)
	if err != nil {
		t.Fatalf("classic asset: %v", err)
	}
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse USD: %v", err)
	}
	pair := canonical.Pair{Base: ustry, Quote: usd}

	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		ustry.String(): anomaly.ClassTreasury, // 1% warn, 3% freeze
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        pair,
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("50.00"), // 4900% — the manipulation
		SourceCount: 1,
	})
	if d.Action != anomaly.ActionFreeze {
		t.Errorf("USTRY-shape attack: action = %q, want freeze", d.Action)
	}
	if d.Class != anomaly.ClassTreasury {
		t.Errorf("class = %q, want treasury", d.Class)
	}
	if d.DeviationPct < 4800 || d.DeviationPct > 5000 {
		t.Errorf("DeviationPct = %g, want ~4900", d.DeviationPct)
	}
}

// TestEvaluate_StablecoinDepegFreezes — a 5% USDC depeg on a single
// source is freezeable territory (way past stablecoin's 3% freeze).
func TestEvaluate_StablecoinDepegFreezes(t *testing.T) {
	usdc, err := canonical.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("classic asset: %v", err)
	}
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse USD: %v", err)
	}
	pair := canonical.Pair{Base: usdc, Quote: usd}

	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		usdc.String(): anomaly.ClassStablecoin,
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        pair,
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("0.95"), // 5% depeg
		SourceCount: 1,
	})
	if d.Action != anomaly.ActionFreeze {
		t.Errorf("USDC 5%% depeg single-source: action = %q, want freeze", d.Action)
	}
}

// TestEvaluate_StablecoinDepegMultiSourceWarns — same 5% depeg with
// multiple sources → warn (real depeg event, not manipulation).
func TestEvaluate_StablecoinDepegMultiSourceWarns(t *testing.T) {
	usdc, err := canonical.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("classic asset: %v", err)
	}
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair := canonical.Pair{Base: usdc, Quote: usd}

	chk := mustNewChecker(t, mustNewClassifier(t, map[string]anomaly.AssetClass{
		usdc.String(): anomaly.ClassStablecoin,
	}))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        pair,
		PrevVWAP:    rat("1.00"),
		CurrVWAP:    rat("0.95"),
		SourceCount: 4, // CEX + DEX agreement
	})
	if d.Action != anomaly.ActionWarn {
		t.Errorf("USDC 5%% depeg multi-source: action = %q, want warn", d.Action)
	}
}

// TestEvaluate_DownwardMovementSameAsUpward — deviation is absolute.
// A 50% drop is treated identically to a 50% spike.
func TestEvaluate_DownwardMovementSameAsUpward(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil)) // ClassDefault: 30% warn / 75% freeze
	pair := nativePair(t)

	upward := chk.Evaluate(anomaly.Observation{
		Pair: pair, PrevVWAP: rat("1.00"), CurrVWAP: rat("1.50"), SourceCount: 1,
	})
	downward := chk.Evaluate(anomaly.Observation{
		Pair: pair, PrevVWAP: rat("1.00"), CurrVWAP: rat("0.50"), SourceCount: 1,
	})
	if upward.Action != downward.Action {
		t.Errorf("up vs down asymmetry: up=%q down=%q", upward.Action, downward.Action)
	}
}

// TestEvaluate_ZeroPrevVWAPHandled — defensive against the
// theoretical case of a zero prior VWAP. Caller shouldn't pass this
// (CAGGs don't materialise empty buckets) but if they do, treat as
// extreme deviation rather than panic-on-divide-by-zero.
func TestEvaluate_ZeroPrevVWAPHandled(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("0"),
		CurrVWAP:    rat("1.00"),
		SourceCount: 1,
	})
	// With prev=0 and curr>0, deviation is "infinite"; default class
	// freezes at 75%, so we trip freeze.
	if d.Action != anomaly.ActionFreeze {
		t.Errorf("zero prev VWAP with non-zero curr: action = %q, want freeze", d.Action)
	}
}

// TestEvaluate_BothZero — both zero is no movement; allow.
func TestEvaluate_BothZero(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil))
	d := chk.Evaluate(anomaly.Observation{
		Pair:        nativePair(t),
		PrevVWAP:    rat("0"),
		CurrVWAP:    rat("0"),
		SourceCount: 1,
	})
	if d.Action != anomaly.ActionAllow {
		t.Errorf("0 → 0: action = %q, want allow", d.Action)
	}
}

// TestEvaluate_ClassFallsBackToDefault — an asset not in operator
// config gets ClassDefault thresholds (30%/75%). A 50% move on a
// single source is allowed (under freeze), but a 100% move freezes.
func TestEvaluate_ClassFallsBackToDefault(t *testing.T) {
	chk := mustNewChecker(t, mustNewClassifier(t, nil))
	pair := nativePair(t)

	d50 := chk.Evaluate(anomaly.Observation{
		Pair: pair, PrevVWAP: rat("1.00"), CurrVWAP: rat("1.50"), SourceCount: 1,
	})
	if d50.Class != anomaly.ClassDefault {
		t.Errorf("d50 class = %q, want default", d50.Class)
	}
	if d50.Action != anomaly.ActionWarn {
		t.Errorf("default 50%% single-source: action = %q, want warn", d50.Action)
	}

	d100 := chk.Evaluate(anomaly.Observation{
		Pair: pair, PrevVWAP: rat("1.00"), CurrVWAP: rat("2.00"), SourceCount: 1,
	})
	if d100.Action != anomaly.ActionFreeze {
		t.Errorf("default 100%% single-source: action = %q, want freeze", d100.Action)
	}
}

// TestDefaultThresholds_AreValid — the defaults must pass the same
// validation rules operator config does.
func TestDefaultThresholds_AreValid(t *testing.T) {
	for cls, thr := range anomaly.DefaultThresholds() {
		if err := thr.Validate(); err != nil {
			t.Errorf("DefaultThresholds[%s] invalid: %v", cls, err)
		}
	}
	// Must include ClassDefault, the fallback row.
	if _, ok := anomaly.DefaultThresholds()[anomaly.ClassDefault]; !ok {
		t.Error("DefaultThresholds missing ClassDefault row")
	}
}
