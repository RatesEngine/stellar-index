package v1_test

import (
	"net/http"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestMethodology_BaselineShape pins the wire shape's required
// keys + version. Adding optional fields is fine; flipping the
// version string or removing required fields is a breaking
// change and must be coordinated with pkg/client + the explorer.
//
// R-023 in `docs/review-2026-05-10.md`.
func TestMethodology_BaselineShape(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/methodology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var env struct {
		Data v1.Methodology `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Version == "" {
		t.Error("version is empty")
	}
	if env.Data.Aggregation.PriceMethod != "vwap" {
		t.Errorf("price_method = %q, want vwap", env.Data.Aggregation.PriceMethod)
	}
	if env.Data.Aggregation.ClosedBucketWindowSeconds <= 0 {
		t.Errorf("closed_bucket_window_seconds = %d, want > 0", env.Data.Aggregation.ClosedBucketWindowSeconds)
	}
	// All four registry classes must appear, exactly one of which
	// (exchange) contributes_to_vwap=true.
	wantClasses := map[string]bool{
		"exchange":         true,
		"aggregator":       false,
		"oracle":           false,
		"authority_sanity": false,
	}
	if len(env.Data.SourceClasses) != 4 {
		t.Fatalf("source_classes = %d entries, want 4", len(env.Data.SourceClasses))
	}
	for _, sc := range env.Data.SourceClasses {
		want, ok := wantClasses[sc.Name]
		if !ok {
			t.Errorf("unexpected class %q", sc.Name)
			continue
		}
		if sc.ContributesToVWAP != want {
			t.Errorf("class %q contributes_to_vwap = %v, want %v", sc.Name, sc.ContributesToVWAP, want)
		}
		if sc.Description == "" {
			t.Errorf("class %q has empty description", sc.Name)
		}
	}

	// Sources must come from external.Registry — the live ingest
	// venues. Pin the must-have rows; the exact length grows as
	// new sources land, so we don't pin it.
	gotSources := map[string]v1.MethodologySource{}
	for _, s := range env.Data.Sources {
		gotSources[s.Name] = s
	}
	for _, name := range []string{"sdex", "soroswap", "binance", "coinbase", "reflector-dex"} {
		if _, ok := gotSources[name]; !ok {
			t.Errorf("expected source %q in Methodology.Sources", name)
		}
	}

	// References must include the four ADRs that govern the
	// served-price contract.
	gotADRs := map[string]bool{}
	for _, ref := range env.Data.References {
		gotADRs[ref.ID] = true
	}
	for _, want := range []string{"ADR-0007", "ADR-0015", "ADR-0019"} {
		if !gotADRs[want] {
			t.Errorf("expected reference %q in Methodology.References", want)
		}
	}
}

// TestMethodology_SurfacesStablecoinPegConfig confirms operator-
// declared USD pegs round-trip through the response. Empty when
// the operator hasn't declared any.
func TestMethodology_SurfacesStablecoinPegConfig(t *testing.T) {
	usdc, err := canonical.NewClassicAsset(
		"USDC",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := v1.New(v1.Options{USDPeggedClassics: []canonical.Asset{usdc}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/methodology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.Methodology `json:"data"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data.Aggregation.StablecoinFiatProxy) != 1 {
		t.Fatalf("StablecoinFiatProxy = %d entries, want 1", len(env.Data.Aggregation.StablecoinFiatProxy))
	}
	got := env.Data.Aggregation.StablecoinFiatProxy[0]
	if got.AssetID != usdc.String() {
		t.Errorf("AssetID = %q, want %q", got.AssetID, usdc.String())
	}
	if got.PegsTo != "fiat:USD" {
		t.Errorf("PegsTo = %q, want fiat:USD", got.PegsTo)
	}
}

// TestMethodology_EmptyStablecoinList is the no-pegs deployment
// case — the field is present (so consumers don't crash on
// missing key lookups) but empty.
func TestMethodology_EmptyStablecoinList(t *testing.T) {
	srv := v1.New(v1.Options{}) // no USDPeggedClassics
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/methodology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.Methodology `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Aggregation.StablecoinFiatProxy == nil {
		t.Error("StablecoinFiatProxy = nil, want empty slice (consumers index safely)")
	}
	if len(env.Data.Aggregation.StablecoinFiatProxy) != 0 {
		t.Errorf("StablecoinFiatProxy = %d entries, want 0", len(env.Data.Aggregation.StablecoinFiatProxy))
	}
}
