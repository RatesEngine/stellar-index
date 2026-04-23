package stellarrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Client is a JSON-RPC client for a single stellar-rpc endpoint.
// Safe for concurrent use.
type Client struct {
	endpoint string
	http     *http.Client
	nextID   atomic.Int64
}

// Option configures a [Client] at construction time.
type Option func(*Client)

// WithHTTPClient overrides the default http.Client (useful for
// custom timeouts, TLS configs, transport-level tracing).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout sets an overall request timeout. Ignored if
// [WithHTTPClient] has already been applied.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.http == nil {
			c.http = &http.Client{Timeout: d}
		}
	}
}

// New returns a client pointing at endpoint (e.g.
// "http://localhost:8000"). Default timeout: 30 s.
func New(endpoint string, opts ...Option) *Client {
	c := &Client{endpoint: endpoint}
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	return c
}

// Endpoint returns the URL the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// call is the low-level JSON-RPC round-trip. Callers unmarshal the
// result into their own target. If the remote returned an error
// envelope, call returns it wrapped as a *[JSONRPCError].
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	req := jsonrpcRequest{Version: "2.0", ID: int(id), Method: method, Params: params}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("stellarrpc: marshal %s: %w", method, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("stellarrpc: new request %s: %w", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("stellarrpc: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read full body so callers see JSON-level errors even on !=200.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("stellarrpc: %s: read body: %w", method, err)
	}

	// Upstream proxies sometimes return HTML on 5xx — guard.
	if resp.StatusCode >= 400 && len(respBody) > 0 && respBody[0] != '{' {
		return fmt.Errorf("stellarrpc: %s: HTTP %d: %s", method, resp.StatusCode, truncate(string(respBody), 256))
	}

	var envelope jsonrpcResponse
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("stellarrpc: %s: decode: %w (body: %s)", method, err, truncate(string(respBody), 256))
	}
	if envelope.Error != nil {
		return envelope.Error
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("stellarrpc: %s: decode result: %w", method, err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── Public methods ────────────────────────────────────────────────

// Health calls getHealth. Note: a healthy stale node returns a
// JSON-RPC error envelope rather than a 200 with status=stale —
// callers should handle both paths.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	var h Health
	err := c.call(ctx, "getHealth", nil, &h)
	return &h, err
}

// LatestLedger calls getLatestLedger.
func (c *Client) LatestLedger(ctx context.Context) (*LatestLedger, error) {
	var l LatestLedger
	return &l, c.call(ctx, "getLatestLedger", nil, &l)
}

// LatestLedgerSequence is a convenience wrapper that returns just
// the ledger sequence number. Source implementations seed their
// startLedger from this on first poll — stellar-rpc's getEvents
// rejects startLedger=0, so sources MUST pick a real number before
// their first subscription.
func (c *Client) LatestLedgerSequence(ctx context.Context) (uint32, error) {
	l, err := c.LatestLedger(ctx)
	if err != nil {
		return 0, err
	}
	return l.Sequence, nil
}

// Network calls getNetwork.
func (c *Client) Network(ctx context.Context) (*Network, error) {
	var n Network
	return &n, c.call(ctx, "getNetwork", nil, &n)
}

// VersionInfo calls getVersionInfo.
func (c *Client) VersionInfo(ctx context.Context) (*VersionInfo, error) {
	var v VersionInfo
	return &v, c.call(ctx, "getVersionInfo", nil, &v)
}

// FeeStats calls getFeeStats.
func (c *Client) FeeStats(ctx context.Context) (*FeeStats, error) {
	var f FeeStats
	return &f, c.call(ctx, "getFeeStats", nil, &f)
}

// GetEvents calls getEvents with the given filters + pagination.
// Pass nil for pagination to use server defaults.
func (c *Client) GetEvents(ctx context.Context, startLedger, endLedger uint32, filters []EventFilter, pag *Pagination) (*EventsResponse, error) {
	p := eventsParams{StartLedger: startLedger, EndLedger: endLedger, Filters: filters, Pagination: pag}
	var r EventsResponse
	return &r, c.call(ctx, "getEvents", p, &r)
}

// GetLedgers calls getLedgers.
func (c *Client) GetLedgers(ctx context.Context, startLedger uint32, pag *Pagination) (*LedgersResponse, error) {
	p := ledgersParams{StartLedger: startLedger, Pagination: pag}
	var r LedgersResponse
	return &r, c.call(ctx, "getLedgers", p, &r)
}

// GetTransaction calls getTransaction.
//
// hash is the tx envelope hash as a hex string (no "0x" prefix).
// Returns status=NOT_FOUND when the tx is outside the RPC node's
// retention window — NOT an error. Callers should branch on Status
// rather than relying on error to signal "not found".
func (c *Client) GetTransaction(ctx context.Context, hash string) (*TransactionResponse, error) {
	var r TransactionResponse
	return &r, c.call(ctx, "getTransaction", transactionParams{Hash: hash}, &r)
}

// GetTransactions calls getTransactions (paginated batch lookup).
//
// startLedger=0 uses the pagination cursor only. Cursor format is
// an opaque stellar-rpc value — pass through from a prior response.
func (c *Client) GetTransactions(ctx context.Context, startLedger uint32, pag *Pagination) (*TransactionsResponse, error) {
	p := transactionsParams{StartLedger: startLedger, Pagination: pag}
	var r TransactionsResponse
	return &r, c.call(ctx, "getTransactions", p, &r)
}
