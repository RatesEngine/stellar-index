package stellarrpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	rpc "github.com/StellarAtlas/stellar-atlas/internal/stellarrpc"
)

// ─── JSONRPCError.Error() ─────────────────────────────────────

func TestJSONRPCError_Error_formatString(t *testing.T) {
	e := &rpc.JSONRPCError{Code: -32603, Message: "internal error"}
	got := e.Error()
	if !strings.Contains(got, "-32603") {
		t.Errorf("Error() = %q, want code in message", got)
	}
	if !strings.Contains(got, "internal error") {
		t.Errorf("Error() = %q, want message in message", got)
	}
	if !strings.Contains(got, "stellar-rpc error") {
		t.Errorf("Error() = %q, want \"stellar-rpc error\" prefix", got)
	}
}

// ─── GetLedgers ───────────────────────────────────────────────

func TestGetLedgers_decodesResponse(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getLedgers": map[string]any{
			"ledgers": []map[string]any{
				{
					"hash":            "abcd",
					"sequence":        100,
					"closeTimestamp":  1772000000,
					"protocolVersion": 23,
				},
			},
			"latestLedger": 200,
			"oldestLedger": 1,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.GetLedgers(context.Background(), 100, nil)
	if err != nil {
		t.Fatalf("GetLedgers: %v", err)
	}
	if got.LatestLedger != 200 {
		t.Errorf("LatestLedger = %d, want 200", got.LatestLedger)
	}
	if got.OldestLedger != 1 {
		t.Errorf("OldestLedger = %d, want 1", got.OldestLedger)
	}
	if len(got.Ledgers) != 1 {
		t.Fatalf("got %d ledgers, want 1", len(got.Ledgers))
	}
}

// ─── SimulateTransaction ──────────────────────────────────────

func TestSimulateTransaction_decodesSuccess(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"simulateTransaction": map[string]any{
			"latestLedger": 100,
			"results": []map[string]any{
				{"xdr": "AAAACQAA"},
			},
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.SimulateTransaction(context.Background(), "envelope-base64-blob")
	if err != nil {
		t.Fatalf("SimulateTransaction: %v", err)
	}
	if got.LatestLedger != 100 {
		t.Errorf("LatestLedger = %d, want 100", got.LatestLedger)
	}
	if len(got.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(got.Results))
	}
}

func TestSimulateTransaction_passesThroughContractError(t *testing.T) {
	// stellar-rpc returns a 200 with .error populated when the
	// contract call itself failed (panic, out-of-gas, etc.). The
	// Go method must NOT translate that to a Go error — callers
	// inspect SimulationResponse.Error directly.
	s := mockRPC(t, map[string]any{
		"simulateTransaction": map[string]any{
			"latestLedger": 100,
			"error":        "host trap: out of gas",
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.SimulateTransaction(context.Background(), "envelope")
	if err != nil {
		t.Fatalf("SimulateTransaction returned Go error %v; should pass contract failure via .Error", err)
	}
	if got.Error == "" {
		t.Error("expected non-empty SimulationResponse.Error")
	}
}

// ─── LatestLedgerSequence error path ─────────────────────────

func TestLatestLedgerSequence_propagatesError(t *testing.T) {
	// Mock returns a JSON-RPC error envelope; the convenience
	// wrapper must propagate the wrapped *JSONRPCError unchanged.
	s := mockRPC(t, map[string]any{}) // no method registered → -32601 method not found
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.LatestLedgerSequence(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var jerr *rpc.JSONRPCError
	if !errors.As(err, &jerr) {
		t.Errorf("expected *JSONRPCError chain, got %T", err)
	}
}
