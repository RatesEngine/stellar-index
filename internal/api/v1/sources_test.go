package v1_test

import (
	"net/http"
	"sort"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

func TestSources_ReturnsRegistry(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/sources")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Source `json:"data"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data) == 0 {
		t.Fatal("expected at least one source in /v1/sources")
	}

	// Spot-check the canonical entries: binance is exchange-class
	// and contributes; coingecko is aggregator-class and doesn't.
	want := map[string]struct {
		class  string
		inVWAP bool
	}{
		"binance":       {class: "exchange", inVWAP: true},
		"soroswap":      {class: "exchange", inVWAP: true},
		"coingecko":     {class: "aggregator", inVWAP: false},
		"reflector-dex": {class: "oracle", inVWAP: false},
		"ecb":           {class: "authority_sanity", inVWAP: false},
	}
	got := map[string]v1.Source{}
	for _, s := range env.Data {
		got[s.Name] = s
	}
	for name, exp := range want {
		s, ok := got[name]
		if !ok {
			t.Errorf("source %q missing from /v1/sources", name)
			continue
		}
		if s.Class != exp.class {
			t.Errorf("%s.class = %q want %q", name, s.Class, exp.class)
		}
		if s.IncludeInVWAP != exp.inVWAP {
			t.Errorf("%s.include_in_vwap = %v want %v", name, s.IncludeInVWAP, exp.inVWAP)
		}
	}
}

func TestSources_SortedByName(t *testing.T) {
	// Stable ordering matters: CDN cache hit ratio + smoother diffs
	// in operator dashboards.
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/sources")
	var env struct {
		Data []v1.Source `json:"data"`
	}
	mustDecode(t, resp, &env)

	names := make([]string, len(env.Data))
	for i, s := range env.Data {
		names[i] = s.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("sources not sorted: %v", names)
	}
}
