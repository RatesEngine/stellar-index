package v1

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/clickhouse"
)

type fakeTokenSupply struct {
	gotContractID string
	supply        clickhouse.TokenSupply
	nativeCoins   int64
	nativeErr     error
	supplyErr     error
}

func (f *fakeTokenSupply) TokenSupply(_ context.Context, contractID string) (clickhouse.TokenSupply, error) {
	f.gotContractID = contractID
	if f.supplyErr != nil {
		return clickhouse.TokenSupply{}, f.supplyErr
	}
	return f.supply, nil
}

func (f *fakeTokenSupply) NativeTotalCoins(_ context.Context) (int64, uint32, error) {
	if f.nativeErr != nil {
		return 0, 0, f.nativeErr
	}
	return f.nativeCoins, 62944000, nil
}

func serveSupply(t *testing.T, reader TokenSupplyReader, sac map[string]string, assetID string) *httptest.ResponseRecorder {
	t.Helper()
	srv := &Server{tokenSupply: reader, sacWrappers: sac}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/assets/{asset_id}/supply", srv.handleAssetSupply)
	req := httptest.NewRequest(http.MethodGet, "/v1/assets/"+assetID+"/supply", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeSupply(t *testing.T, body []byte) AssetSupply {
	t.Helper()
	var env struct {
		Data AssetSupply `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	return env.Data
}

const supplyContractID = "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP"

func TestAssetSupply_NilReader503(t *testing.T) {
	rec := serveSupply(t, nil, nil, supplyContractID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil reader: got %d, want 503", rec.Code)
	}
}

func TestAssetSupply_ContractIDDirect(t *testing.T) {
	f := &fakeTokenSupply{supply: clickhouse.TokenSupply{
		ContractID: supplyContractID,
		Total:      big.NewInt(900), Mint: big.NewInt(1000), Burn: big.NewInt(80), Clawback: big.NewInt(20),
		FlowCount: 7,
	}}
	rec := serveSupply(t, f, nil, supplyContractID)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotContractID != supplyContractID {
		t.Errorf("resolved contract_id = %q, want %q", f.gotContractID, supplyContractID)
	}
	got := decodeSupply(t, rec.Body.Bytes())
	if got.TotalSupply != "900" || got.Source != "mint_burn_flows" || got.FlowCount != 7 {
		t.Errorf("unexpected body: %+v", got)
	}
	if got.MintTotal == nil || *got.MintTotal != "1000" {
		t.Errorf("mint_total = %v, want 1000", got.MintTotal)
	}
}

func TestAssetSupply_Native(t *testing.T) {
	f := &fakeTokenSupply{nativeCoins: 105443902087730434}
	for _, alias := range []string{"native", "XLM", "crypto:XLM"} {
		rec := serveSupply(t, f, nil, alias)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", alias, rec.Code)
		}
		got := decodeSupply(t, rec.Body.Bytes())
		if got.TotalSupply != "105443902087730434" || got.Source != "ledger_total_coins" {
			t.Errorf("%s: unexpected body %+v", alias, got)
		}
		if got.MintTotal != nil {
			t.Errorf("%s: native should not carry mint_total", alias)
		}
	}
}

func TestAssetSupply_ClassicUnmapped404(t *testing.T) {
	f := &fakeTokenSupply{}
	rec := serveSupply(t, f, nil, "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmapped classic: got %d, want 404", rec.Code)
	}
	if f.gotContractID != "" {
		t.Errorf("reader should not be called for an unmapped classic asset")
	}
}

func TestAssetSupply_ClassicViaSACWrapper(t *testing.T) {
	assetKey := "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	sac := map[string]string{supplyContractID: assetKey}
	f := &fakeTokenSupply{supply: clickhouse.TokenSupply{
		ContractID: supplyContractID,
		Total:      big.NewInt(500), Mint: big.NewInt(500), Burn: big.NewInt(0), Clawback: big.NewInt(0),
		FlowCount: 3,
	}}
	rec := serveSupply(t, f, sac, assetKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotContractID != supplyContractID {
		t.Errorf("classic asset should resolve to its SAC %q, got %q", supplyContractID, f.gotContractID)
	}
	got := decodeSupply(t, rec.Body.Bytes())
	if got.ContractID != supplyContractID || got.TotalSupply != "500" {
		t.Errorf("unexpected body: %+v", got)
	}
}
