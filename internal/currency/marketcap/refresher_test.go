package marketcap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/currency"
)

// fakeCatalogue is a minimal Catalogue with one crypto entry that
// carries a CoinGecko ID, so cgIDsAndSlugs returns a non-empty set.
type fakeCatalogue struct {
	entries []*currency.VerifiedCurrency
}

func (f fakeCatalogue) All() []*currency.VerifiedCurrency { return f.entries }

// TestFetch_APIKeySentAsHeaderNotQueryParam is the F-1337 regression:
// the CoinGecko key MUST travel in the `x-cg-pro-api-key` request
// header, never as a `?x_cg_pro_api_key=` query param. As a query
// param the secret is embedded in the request URL and leaks verbatim
// through *url.Error on any transport failure and into access logs.
func TestFetch_APIKeySentAsHeaderNotQueryParam(t *testing.T) {
	const secret = "cg-pro-secret-DO-NOT-LEAK"

	var gotURL, gotProHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotProHeader = r.Header.Get("x-cg-pro-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bitcoin":{"usd":42000,"usd_market_cap":800000000000}}`))
	}))
	defer srv.Close()

	r := &Refresher{
		Cache:      New(),
		Cat:        fakeCatalogue{entries: []*currency.VerifiedCurrency{{Slug: "btc", CoinGeckoID: "bitcoin", Class: currency.ClassCrypto}}},
		Endpoint:   srv.URL,
		APIKey:     secret,
		HTTPClient: srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.fetch(ctx, []string{"bitcoin"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if gotProHeader != secret {
		t.Errorf("x-cg-pro-api-key header = %q, want the secret", gotProHeader)
	}
	if strings.Contains(gotURL, secret) {
		t.Errorf("secret leaked into request URL %q (F-1337)", gotURL)
	}
	if strings.Contains(gotURL, "x_cg_pro_api_key") {
		t.Errorf("legacy query param present in URL %q (F-1337)", gotURL)
	}
}

// TestFetch_DemoKeySentAsHeader covers the demo-key branch likewise.
func TestFetch_DemoKeySentAsHeader(t *testing.T) {
	const secret = "cg-demo-secret"

	var gotURL, gotDemoHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotDemoHeader = r.Header.Get("x-cg-demo-api-key")
		_, _ = w.Write([]byte(`{"bitcoin":{"usd":1}}`))
	}))
	defer srv.Close()

	r := &Refresher{
		Cache:      New(),
		Cat:        fakeCatalogue{entries: []*currency.VerifiedCurrency{{Slug: "btc", CoinGeckoID: "bitcoin", Class: currency.ClassCrypto}}},
		Endpoint:   srv.URL,
		DemoKey:    secret,
		HTTPClient: srv.Client(),
	}

	if _, err := r.fetch(context.Background(), []string{"bitcoin"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotDemoHeader != secret {
		t.Errorf("x-cg-demo-api-key header = %q, want the secret", gotDemoHeader)
	}
	if strings.Contains(gotURL, secret) {
		t.Errorf("demo secret leaked into request URL %q (F-1337)", gotURL)
	}
}
