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
	"unicode/utf8"
)

// MaxResponseBytes caps how much we will read off the wire per call.
// stellar-rpc's getEvents / getTransactions / getLedgers can return
// several MB on a big page; 64 MiB leaves comfortable headroom while
// bounding memory if an upstream proxy ever misbehaves (returns a
// streaming error page, gets man-in-the-middled by a captive portal,
// etc.). Exceeding the cap is an error, not a silent truncation.
const MaxResponseBytes = 64 << 20

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
			c.http = &http.Client{Timeout: d, Transport: newDefaultTransport()}
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
		c.http = &http.Client{
			Timeout:   30 * time.Second,
			Transport: newDefaultTransport(),
		}
	}
	return c
}

// newDefaultTransport returns an http.Transport tuned for the
// indexer's access pattern: one RPC endpoint shared by many source
// goroutines. The stdlib default MaxIdleConnsPerHost=2 forces
// most sources to dial fresh TCP on every call, which burns
// connection setup + TLS handshake cost 100×/s under load. Raising
// it lets keep-alive reuse kick in the way it's supposed to.
func newDefaultTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 200
	t.MaxIdleConnsPerHost = 100
	t.IdleConnTimeout = 90 * time.Second
	return t
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
	// Identifiable User-Agent so stellar-rpc operators can correlate
	// traffic in their logs (mirrors internal/metadata/sep1.go).
	httpReq.Header.Set("User-Agent", "rates-engine/stellarrpc (+https://ratesengine.net)")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("stellarrpc: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read full body so callers see JSON-level errors even on !=200,
	// but cap the read so a runaway upstream can't OOM us. The +1
	// lets us detect overshoot cleanly — if we read MaxResponseBytes+1
	// bytes, the stream wasn't done.
	limited := io.LimitReader(resp.Body, MaxResponseBytes+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("stellarrpc: %s: read body: %w", method, err)
	}
	if int64(len(respBody)) > MaxResponseBytes {
		return fmt.Errorf("stellarrpc: %s: response exceeded %d bytes (upstream misbehaving?)",
			method, MaxResponseBytes)
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
	// Non-2xx status without a JSON-RPC error envelope is still a
	// failure — we must not let the caller treat it as success just
	// because the body happened to be valid JSON. Synthesize.
	if resp.StatusCode >= 400 {
		return fmt.Errorf("stellarrpc: %s: HTTP %d (no JSON-RPC error envelope): %s",
			method, resp.StatusCode, truncate(string(respBody), 256))
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("stellarrpc: %s: decode result: %w", method, err)
		}
	}
	return nil
}

// truncate cuts `s` to at most `n` bytes plus a trailing "…",
// walking back to the nearest UTF-8 rune boundary at or before
// byte n so multi-byte codepoints aren't sliced in half. Used
// for log + error messages where the input is typically an HTTP
// response body — those routinely contain UTF-8 (vendor error
// pages, JSON-with-unicode), and a naive byte slice produced
// invalid UTF-8 in journalctl + Loki output.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
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
//
// The response is sanity-checked (see EventsResponse.sanityCheck)
// before being returned — a node serving inconsistent ledger
// bounds or out-of-order events surfaces as an error here, not as
// a silent ingestion bug downstream.
func (c *Client) GetEvents(ctx context.Context, startLedger, endLedger uint32, filters []EventFilter, pag *Pagination) (*EventsResponse, error) {
	p := eventsParams{StartLedger: startLedger, EndLedger: endLedger, Filters: filters, Pagination: pag}
	var r EventsResponse
	if err := c.call(ctx, "getEvents", p, &r); err != nil {
		return nil, err
	}
	if err := r.sanityCheck(); err != nil {
		return nil, err
	}
	return &r, nil
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

// SimulateTransaction calls simulateTransaction.
//
// txEnvelope is a base64-encoded xdr.TransactionEnvelope. For read-
// only Soroban view calls (all_pairs_length, token_0, etc.) the
// envelope is unsigned — stellar-rpc doesn't validate signatures
// during simulation. Build one with [InvokeContractTxEnvelope].
//
// Returns the SimulationResponse verbatim; callers inspect
// response.Results[0].XDR (base64 SCVal) for the function return
// value. response.Error is non-empty when the contract call itself
// failed (e.g. panicking, out-of-gas); a nil Go error from this
// method only means "the RPC round-trip succeeded."
func (c *Client) SimulateTransaction(ctx context.Context, txEnvelope string) (*SimulationResponse, error) {
	p := simulateParams{Transaction: txEnvelope}
	var r SimulationResponse
	return &r, c.call(ctx, "simulateTransaction", p, &r)
}
