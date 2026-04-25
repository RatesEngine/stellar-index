package v1_test

import (
	"bytes"
	"io"
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

func TestSources_FilterByClass(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	cases := []struct {
		class string
		want  map[string]bool // expected names
	}{
		{
			class: "aggregator",
			want:  map[string]bool{"coingecko": true, "coinmarketcap": true, "cryptocompare": true},
		},
		{
			class: "oracle",
			want:  map[string]bool{"reflector-dex": true, "reflector-cex": true, "reflector-fx": true, "redstone": true, "band": true},
		},
		{
			class: "authority_sanity",
			want:  map[string]bool{"ecb": true, "fed-h10": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.class, func(t *testing.T) {
			resp := mustGet(t, ts.URL+"/v1/sources?class="+tc.class)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var env struct {
				Data []v1.Source `json:"data"`
			}
			mustDecode(t, resp, &env)

			got := map[string]bool{}
			for _, s := range env.Data {
				if s.Class != tc.class {
					t.Errorf("class filter leaked: got %q in class=%q result", s.Class, tc.class)
				}
				got[s.Name] = true
			}
			for name := range tc.want {
				if !got[name] {
					t.Errorf("expected %s in class=%q result, got %v", name, tc.class, got)
				}
			}
		})
	}
}

func TestSources_FilterByClass_Unknown(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/sources?class=nonsense")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown class", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("invalid-class")) {
		t.Errorf("expected invalid-class error type in body: %s", body)
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
