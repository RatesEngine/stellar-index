package soroswap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/scval"
	"github.com/StellarAtlas/stellar-atlas/internal/stellarrpc"
)

// mockSorobanRPC stages a JSON-RPC server that responds to
// simulateTransaction with the provided result XDR. Each call gets
// the next entry from `results`; subsequent calls reuse the last
// entry. Lets us script multi-step factory sweeps.
func mockSorobanRPC(t *testing.T, results []string, errMsg string) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "simulateTransaction" {
			http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
			return
		}
		var xdrResult string
		if idx < len(results) {
			xdrResult = results[idx]
		} else if len(results) > 0 {
			xdrResult = results[len(results)-1]
		}
		idx++

		simResult := map[string]any{
			"latestLedger": 100,
		}
		if errMsg != "" {
			simResult["error"] = errMsg
		} else {
			simResult["results"] = []map[string]any{{"xdr": xdrResult}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": simResult,
		})
	}))
}

// b64ScVal serialises an SCVal to the base64 form stellar-rpc returns.
func b64ScVal(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func u32SV(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func contractAddrSV(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	return contractAddrFromStrkey(t, strk)
}

// ─── callView ─────────────────────────────────────────────────

func TestCallView_happyPath(t *testing.T) {
	want := u32SV(42)
	srv := mockSorobanRPC(t, []string{b64ScVal(t, want)}, "")
	defer srv.Close()

	c := stellarrpc.New(srv.URL)
	contract := makeContractStrkey(t, 0x10)
	got, err := callView(context.Background(), c, contract, "noop", nil)
	if err != nil {
		t.Fatalf("callView: %v", err)
	}
	if got.Type != xdr.ScValTypeScvU32 || uint32(*got.U32) != 42 {
		t.Errorf("got %+v, want u32(42)", got)
	}
}

func TestCallView_simulateRejected(t *testing.T) {
	srv := mockSorobanRPC(t, nil, "host trap: contract panicked")
	defer srv.Close()

	c := stellarrpc.New(srv.URL)
	contract := makeContractStrkey(t, 0x10)
	_, err := callView(context.Background(), c, contract, "noop", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "simulate rejected") {
		t.Errorf("error %q missing \"simulate rejected\" fragment", err.Error())
	}
}

func TestCallView_invalidContractIDRejectedAtEnvelopeBuild(t *testing.T) {
	c := stellarrpc.New("http://does-not-matter")
	_, err := callView(context.Background(), c, "not-a-c-strkey", "noop", nil)
	if err == nil {
		t.Fatal("expected envelope-build error, got nil")
	}
	if !strings.Contains(err.Error(), "build envelope") {
		t.Errorf("error %q missing \"build envelope\" fragment", err.Error())
	}
}

// ─── callU32 ──────────────────────────────────────────────────

func TestCallU32_happyPath(t *testing.T) {
	srv := mockSorobanRPC(t, []string{b64ScVal(t, u32SV(7))}, "")
	defer srv.Close()

	c := stellarrpc.New(srv.URL)
	contract := makeContractStrkey(t, 0x10)
	got, err := callU32(context.Background(), c, contract, "all_pairs_length", nil)
	if err != nil {
		t.Fatalf("callU32: %v", err)
	}
	if got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestCallU32_wrongTypeReturnsScValTypeError(t *testing.T) {
	// SCVal::Symbol cannot decode as u32 → AsU32 should error.
	sym := xdr.ScSymbol("not-a-number")
	srv := mockSorobanRPC(t, []string{b64ScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym})}, "")
	defer srv.Close()

	c := stellarrpc.New(srv.URL)
	contract := makeContractStrkey(t, 0x10)
	_, err := callU32(context.Background(), c, contract, "all_pairs_length", nil)
	if err == nil {
		t.Fatal("expected error on wrong-type result, got nil")
	}
}

// ─── callAddressStrkey ────────────────────────────────────────

func TestCallAddressStrkey_happyPath(t *testing.T) {
	target := makeContractStrkey(t, 0x42)
	srv := mockSorobanRPC(t, []string{b64ScVal(t, contractAddrSV(t, target))}, "")
	defer srv.Close()

	c := stellarrpc.New(srv.URL)
	caller := makeContractStrkey(t, 0x10)
	got, err := callAddressStrkey(context.Background(), c, caller, "all_pairs", []scval.ScVal{u32SV(0)})
	if err != nil {
		t.Fatalf("callAddressStrkey: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}
