package v1_test

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
)

func TestPairs_MissingBase400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPairs_MissingQuote400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPairs_InvalidAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	for _, q := range []string{
		"/v1/pairs?base=garbage&quote=fiat:USD",
		"/v1/pairs?base=native&quote=garbage",
	} {
		resp := mustGet(t, ts.URL+q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestPairs_IdentityPair400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPairs_EmptyWhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !bytes.Contains([]byte(body), []byte(`"data":[]`)) {
		t.Errorf("expected \"data\":[], got: %s", body)
	}
}

func TestPairs_NotFoundReturnsEmptyArray(t *testing.T) {
	// A pair with no trades returns 200 + empty data — NOT a 404.
	// Spec: PairsEnvelope.data is `type: array`; "no data" is an
	// empty array, not a 404, so clients can distinguish "no such
	// pair" from a malformed request without branching on status.
	reader := &stubMarketsReader{pairFound: false}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !bytes.Contains([]byte(body), []byte(`"data":[]`)) {
		t.Errorf("expected \"data\":[], got: %s", body)
	}
}

func TestPairs_FoundReturnsSingleElement(t *testing.T) {
	ts1 := time.Unix(1_772_000_000, 0).UTC()
	reader := &stubMarketsReader{
		pair:      v1.Market{Base: "native", Quote: "fiat:USD", LastTradeAt: ts1, TradeCount24h: 99},
		pairFound: true,
	}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Market `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d markets, want 1", len(env.Data))
	}
	if env.Data[0].TradeCount24h != 99 {
		t.Errorf("trade_count_24h = %d, want 99", env.Data[0].TradeCount24h)
	}
	if env.Data[0].Base != "native" || env.Data[0].Quote != "fiat:USD" {
		t.Errorf("pair = %s/%s, want native/fiat:USD", env.Data[0].Base, env.Data[0].Quote)
	}
}

func TestPairs_ReaderError500(t *testing.T) {
	reader := &stubMarketsReader{pairErr: errors.New("boom")}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pairs?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}
