package stellarrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	rpc "github.com/StellarAtlas/stellar-atlas/internal/stellarrpc"
)

// mockRPC returns a test server that responds with the given result
// for each method name.
func mockRPC(t *testing.T, responses map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("mockRPC: bad request body: %v", err)
		}
		result, ok := responses[req.Method]
		if !ok {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": result,
		})
	}))
}

func TestLatestLedger(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getLatestLedger": map[string]any{
			"id":              "abc123",
			"protocolVersion": 23,
			"sequence":        52000000,
			"closeTime":       "1772000000",
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := c.LatestLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != 52_000_000 {
		t.Errorf("sequence = %d, want 52000000", got.Sequence)
	}
	if got.ProtocolVersion != 23 {
		t.Errorf("protocol = %d, want 23", got.ProtocolVersion)
	}
}

func TestHealthError(t *testing.T) {
	// Real stellar-rpc returns JSON-RPC error envelope when stale.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"error": map[string]any{
				"code":    -32603,
				"message": "[-32603] latency (1146h39m6s) since last known ledger closed is too high (>30s)",
			},
		})
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for stale rpc")
	}
	var jerr *rpc.JSONRPCError
	if !errors.As(err, &jerr) {
		t.Fatalf("error was %T, want *JSONRPCError", err)
	}
	if jerr.Code != -32603 {
		t.Errorf("code = %d, want -32603", jerr.Code)
	}
	if !strings.Contains(jerr.Message, "latency") {
		t.Errorf("message = %q, want 'latency' substring", jerr.Message)
	}
}

func TestGetEventsRoundTrip(t *testing.T) {
	// Use the shape we observed from the real crypto-stellar/stellar-rpc
	// probe: 2-topic "fee" event from the XLM SAC.
	s := mockRPC(t, map[string]any{
		"getEvents": map[string]any{
			"events": []map[string]any{
				{
					"type":                     "contract",
					"ledger":                   61521000,
					"ledgerClosedAt":           "2026-03-06T02:32:13Z",
					"contractId":               "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA",
					"id":                       "0264230683017216000-0000000000",
					"operationIndex":           0,
					"transactionIndex":         0,
					"txHash":                   "f95e8788b9dfe1f94813f50b90c2a77413eda5c32ba179b8a7867d50ec4f7aa8",
					"inSuccessfulContractCall": true,
					"topic":                    []string{"AAAADwAAAANmZWUA", "AAAAEgAAAAAAAAAAgqX242/gYO75aFiwQIDrNyzZZa3Qrsl6gX4omLblaig="},
					"value":                    "AAAACgAAAAAAAAAAAAAAAAAAAMg=",
				},
			},
			"cursor":                "0264230683017216000-0000000000",
			"latestLedger":          61521295,
			"oldestLedger":          61400336,
			"latestLedgerCloseTime": "1772766017",
			"oldestLedgerCloseTime": "1772067154",
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.GetEvents(context.Background(), 61_521_000, 0, nil, &rpc.Pagination{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got.Events))
	}
	e := got.Events[0]
	if e.ContractID != "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA" {
		t.Errorf("contractId = %q", e.ContractID)
	}
	if len(e.Topic) != 2 {
		t.Errorf("topic count = %d, want 2", len(e.Topic))
	}
	// 7-day-ish retention window → oldest and latest differ
	if got.LatestLedger-got.OldestLedger < 1000 {
		t.Errorf("retention window too small: %d", got.LatestLedger-got.OldestLedger)
	}
}

func TestNextIDIncrements(t *testing.T) {
	// Every request should get a unique JSON-RPC id.
	seenIDs := make(map[int]bool)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID int `json:"id"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if seenIDs[req.ID] {
			t.Errorf("duplicate id %d", req.ID)
		}
		seenIDs[req.ID] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"passphrase": "x", "protocolVersion": 23},
		})
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	for i := 0; i < 5; i++ {
		if _, err := c.Network(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(seenIDs) != 5 {
		t.Fatalf("got %d unique ids, want 5", len(seenIDs))
	}
}

func TestGetTransactionSuccess(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getTransaction": map[string]any{
			"status":                "SUCCESS",
			"latestLedger":          61521295,
			"latestLedgerCloseTime": "1772766017",
			"oldestLedger":          61400336,
			"oldestLedgerCloseTime": "1772067154",
			"ledger":                61521000,
			"createdAt":             "2026-03-06T02:32:13Z",
			"applicationOrder":      4,
			"feeBump":               false,
			"envelopeXdr":           "AAAAAgAAAAA=",
			"resultXdr":             "AAAAAAAAAMgAAAA=",
			"resultMetaXdr":         "AAAAAwAAAAA=",
			"diagnosticEventsXdr":   []string{"AAAACg==", "AAAADw=="},
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.GetTransaction(context.Background(),
		"f95e8788b9dfe1f94813f50b90c2a77413eda5c32ba179b8a7867d50ec4f7aa8")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != rpc.TxStatusSuccess {
		t.Errorf("status = %q, want SUCCESS", got.Status)
	}
	if got.Ledger != 61_521_000 {
		t.Errorf("ledger = %d, want 61521000", got.Ledger)
	}
	if got.ApplicationOrder != 4 {
		t.Errorf("applicationOrder = %d, want 4", got.ApplicationOrder)
	}
	if len(got.DiagnosticEventsXdr) != 2 {
		t.Errorf("diagnosticEventsXdr count = %d, want 2 (v23+ field)", len(got.DiagnosticEventsXdr))
	}
}

func TestGetTransactionNotFound(t *testing.T) {
	// NOT_FOUND is a Status value, not an error envelope — callers
	// must branch on Status, not err.
	s := mockRPC(t, map[string]any{
		"getTransaction": map[string]any{
			"status":       "NOT_FOUND",
			"latestLedger": 61521295,
			"oldestLedger": 61400336,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.GetTransaction(context.Background(), "deadbeef")
	if err != nil {
		t.Fatalf("NOT_FOUND must not bubble as err: %v", err)
	}
	if got.Status != rpc.TxStatusNotFound {
		t.Errorf("status = %q, want NOT_FOUND", got.Status)
	}
	if got.Ledger != 0 {
		t.Errorf("ledger = %d, want 0 for NOT_FOUND", got.Ledger)
	}
}

func TestGetTransactionsBatch(t *testing.T) {
	s := mockRPC(t, map[string]any{
		"getTransactions": map[string]any{
			"transactions": []map[string]any{
				{"status": "SUCCESS", "ledger": 100, "applicationOrder": 1},
				{"status": "FAILED", "ledger": 100, "applicationOrder": 2},
				{"status": "SUCCESS", "ledger": 101, "applicationOrder": 1},
			},
			"cursor":       "opaque-cursor",
			"latestLedger": 200,
			"oldestLedger": 50,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	got, err := c.GetTransactions(context.Background(), 100, &rpc.Pagination{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Transactions) != 3 {
		t.Fatalf("got %d tx, want 3", len(got.Transactions))
	}
	if got.Transactions[1].Status != rpc.TxStatusFailed {
		t.Errorf("tx[1].status = %q, want FAILED", got.Transactions[1].Status)
	}
	if got.Cursor != "opaque-cursor" {
		t.Errorf("cursor = %q", got.Cursor)
	}
}

func TestNonEnvelopeHTTPErrorSurfaces(t *testing.T) {
	// Servers / reverse proxies sometimes return HTTP 5xx with a JSON
	// body that ISN'T a JSON-RPC error envelope — e.g. AWS ALB's
	// {"message":"Internal server error"} or Cloudflare's
	// {"errors":[{...}]}. The client must NOT treat those as success
	// just because the body parses as JSON.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Internal server error"}`))
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.LatestLedger(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestNonEnvelopeHTTP4xxSurfaces(t *testing.T) {
	// Same class of bug at the 4xx boundary — 429 from a rate limiter
	// with a non-envelope body.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited","retry_after":60}`))
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.LatestLedger(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 429, got nil")
	}
}

func TestResponseSizeCap(t *testing.T) {
	// Upstream returns a body larger than MaxResponseBytes. The client
	// must refuse with a clear error rather than swallow the whole
	// thing into memory. We write just over the cap — enough to
	// trigger the check, small enough to keep the test cheap.
	const overcap = rpc.MaxResponseBytes + 1024
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Pad a valid-looking JSON envelope out to > MaxResponseBytes.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"id":"`)
		pad := strings.Repeat("a", overcap)
		_, _ = io.WriteString(w, pad)
		_, _ = io.WriteString(w, `"}}`)
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.LatestLedger(context.Background())
	if err == nil {
		t.Fatal("expected size-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error should mention cap: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	// Slow server — make sure ctx cancel kills the call.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer s.Close()

	c := rpc.New(s.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.LatestLedger(ctx)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestEventClosedAtParse(t *testing.T) {
	// Well-formed RFC 3339 round-trips to UTC time.
	e := &events.Event{ID: "ok", LedgerClosedAt: "2026-04-23T12:34:56Z"}
	got, err := e.EventClosedAt()
	if err != nil {
		t.Fatalf("valid timestamp errored: %v", err)
	}
	want := time.Date(2026, 4, 23, 12, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEventClosedAtEmptyIsError(t *testing.T) {
	// Empty ledgerClosedAt must error — the previous behaviour of
	// returning time.Time{} silently is the bug we fixed on
	// 2026-04-23. Zero-time events were sneaking through into
	// trades with observed_at = 0.
	e := &events.Event{ID: "bad-1"}
	if _, err := e.EventClosedAt(); err == nil {
		t.Fatal("empty LedgerClosedAt must error, got nil")
	}
}

func TestGetEventsRejectsInconsistentBounds(t *testing.T) {
	// RPC node mid-catchup / forked / buggy: response claims
	// OldestLedger > LatestLedger. The client must reject rather
	// than hand the caller a corrupt envelope.
	s := mockRPC(t, map[string]any{
		"getEvents": map[string]any{
			"events":       []any{},
			"latestLedger": 1_000_000,
			"oldestLedger": 2_000_000, // inverted on purpose
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.GetEvents(context.Background(), 1_000_500, 0, nil, nil)
	if err == nil {
		t.Fatal("inverted bounds must error")
	}
	if !strings.Contains(err.Error(), "OldestLedger") {
		t.Errorf("err = %v; want OldestLedger substring", err)
	}
}

func TestGetEventsRejectsOutOfBoundEvent(t *testing.T) {
	// Event with Ledger > LatestLedger. Either the envelope or the
	// event is wrong — don't guess which, just reject.
	s := mockRPC(t, map[string]any{
		"getEvents": map[string]any{
			"events": []map[string]any{
				{
					"type":       "contract",
					"ledger":     999_999_999, // past the claimed tip
					"id":         "evt1",
					"contractId": "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					"topic":      []string{},
				},
			},
			"latestLedger": 1_000_000,
			"oldestLedger": 900_000,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.GetEvents(context.Background(), 900_500, 0, nil, nil)
	if err == nil {
		t.Fatal("out-of-bounds event must error")
	}
}

func TestGetEventsRejectsOutOfOrder(t *testing.T) {
	// Events must arrive in ascending ledger order — source
	// correlators (Soroswap swap+sync) depend on it.
	s := mockRPC(t, map[string]any{
		"getEvents": map[string]any{
			"events": []map[string]any{
				{"type": "contract", "ledger": 950_500, "id": "e1", "contractId": "C", "topic": []string{}},
				{"type": "contract", "ledger": 950_400, "id": "e2", "contractId": "C", "topic": []string{}}, // earlier
			},
			"latestLedger": 1_000_000,
			"oldestLedger": 900_000,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.GetEvents(context.Background(), 900_500, 0, nil, nil)
	if err == nil {
		t.Fatal("out-of-order events must error")
	}
	if !strings.Contains(err.Error(), "out of order") {
		t.Errorf("err = %v; want 'out of order' substring", err)
	}
}

func TestGetEventsRejectsZeroLedger(t *testing.T) {
	// Stellar genesis is ledger 1 — Ledger=0 on an event means the
	// JSON was malformed or the field was absent from the payload.
	// Accepting it would let group-keys collide on (0, tx, opIdx)
	// in the phoenix/soroswap fanout buffers.
	s := mockRPC(t, map[string]any{
		"getEvents": map[string]any{
			"events": []map[string]any{
				{"type": "contract", "ledger": 0, "id": "genesis?", "contractId": "C", "topic": []string{}},
			},
			"latestLedger": 1_000_000,
			"oldestLedger": 900_000,
		},
	})
	defer s.Close()

	c := rpc.New(s.URL)
	_, err := c.GetEvents(context.Background(), 900_500, 0, nil, nil)
	if err == nil {
		t.Fatal("zero-ledger event must error")
	}
	if !strings.Contains(err.Error(), "zero ledger") {
		t.Errorf("err = %v; want 'zero ledger' substring", err)
	}
}

func TestEventClosedAtMalformedIsError(t *testing.T) {
	// Unparseable timestamp must error, not silently coerce to
	// zero. Wrong format (missing T) and wrong offset shape both
	// caught here.
	cases := []string{
		"not-a-date",
		"2026-04-23 12:34:56",       // space, not T
		"2026-13-23T12:34:56Z",      // month 13
		"2026-04-23T12:34:56+25:00", // bad offset
	}
	for _, ts := range cases {
		e := &events.Event{ID: "bad-2", LedgerClosedAt: ts}
		if _, err := e.EventClosedAt(); err == nil {
			t.Errorf("malformed %q did not error", ts)
		}
	}
}
