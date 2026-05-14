package chainlink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client is a minimal Ethereum JSON-RPC client supporting the three
// methods we need: eth_call (latestRoundData), eth_blockNumber
// (backfill end-of-range probe), and eth_getLogs (backfill walk).
//
// Deliberately tiny — we don't import a full Ethereum library
// because (a) the dep tree is heavy (geth's go-ethereum pulls in
// ~50MB of code) and (b) we use ~3 methods, ABI-decode ~2 shapes,
// and don't sign or send transactions. The cost of vendoring is
// higher than maintaining the ~200 lines of bespoke client here.
//
// Stateless and goroutine-safe: every method takes a context and
// performs one HTTP round-trip. Operators wanting connection
// pooling configure http.Client.Transport accordingly.
type Client struct {
	HTTPClient *http.Client
	Endpoint   string

	// id is a monotonic JSON-RPC request id (atomic counter).
	// Useful for log correlation when an operator pastes a curl
	// trace; not required by JSON-RPC strictly.
	id atomic.Uint64
}

// NewClient constructs a Client. Empty endpoint falls back to
// [DefaultEndpoint] (Cloudflare public). nil http.Client falls
// back to a 30s-timeout client — generous because eth_getLogs over
// 5k blocks can take ~2-5s on Alchemy free tier under load, and
// timing out mid-call is worse than just letting it complete.
func NewClient(endpoint string, httpClient *http.Client) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		HTTPClient: httpClient,
		Endpoint:   strings.TrimRight(endpoint, "/"),
	}
}

// jsonRPCResponse is the lowest-common-denominator success/error
// envelope. Specific RPC methods unmarshal `Result` further.
type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// EthCall performs eth_call against `to` with raw `data` (0x-prefixed
// hex, selector + ABI-encoded args). `block` is a tag ("latest",
// "earliest") or hex-encoded block number. Returns the raw hex
// result (0x-prefixed) for the caller to ABI-decode.
//
// Returns ErrEmptyResult when the contract returns `0x` (typical of
// reverted calls or contract-not-found). Returns ErrMalformedResult
// when the JSON-RPC layer succeeded but the response shape is
// wrong.
func (c *Client) EthCall(ctx context.Context, to, data, block string) (string, error) {
	if block == "" {
		block = "latest"
	}
	params := []any{
		map[string]string{"to": to, "data": data},
		block,
	}
	var resultHex string
	if err := c.do(ctx, "eth_call", params, &resultHex); err != nil {
		return "", err
	}
	if resultHex == "" || resultHex == "0x" {
		return "", fmt.Errorf("%w: to=%s data=%s", ErrEmptyResult, to, data)
	}
	return resultHex, nil
}

// EthBlockNumber returns the current head block as a uint64. Used
// by the backfill harness to bound its eth_getLogs walk at "now".
func (c *Client) EthBlockNumber(ctx context.Context) (uint64, error) {
	var resultHex string
	if err := c.do(ctx, "eth_blockNumber", []any{}, &resultHex); err != nil {
		return 0, err
	}
	n, err := parseHexUint(resultHex)
	if err != nil {
		return 0, fmt.Errorf("%w: eth_blockNumber: %w", ErrMalformedResult, err)
	}
	return n, nil
}

// LogEntry is the eth_getLogs response shape we care about for
// AnswerUpdated decoding. Other fields (logIndex, removed, etc)
// are present in the JSON but ignored by us.
type LogEntry struct {
	Address     string   `json:"address"`     // 0x-prefixed feed contract
	Topics      []string `json:"topics"`      // [topic0, ...indexed]
	Data        string   `json:"data"`        // 0x-prefixed hex of non-indexed args
	BlockNumber string   `json:"blockNumber"` // 0x-prefixed hex
	TxHash      string   `json:"transactionHash"`
	TxIndex     string   `json:"transactionIndex"`
	LogIndex    string   `json:"logIndex"`
}

// EthGetLogs performs eth_getLogs with the given filter. `addresses`
// is the contract list; `topics` is the topic filter array (each
// entry is either a single topic hash or nil for wildcard).
// `fromBlock` / `toBlock` are inclusive uint64.
//
// Provider-specific block-range limits apply: Alchemy free caps
// response size at 10MB / 10k logs. Callers chunk by 5k blocks
// (a Defacto safe default) when walking historical ranges.
func (c *Client) EthGetLogs(
	ctx context.Context,
	addresses []string,
	topics []any, // [topic0_hex, topic1_or_nil, ...]
	fromBlock, toBlock uint64,
) ([]LogEntry, error) {
	filter := map[string]any{
		"fromBlock": fmt.Sprintf("0x%x", fromBlock),
		"toBlock":   fmt.Sprintf("0x%x", toBlock),
	}
	if len(addresses) > 0 {
		filter["address"] = addresses
	}
	if len(topics) > 0 {
		filter["topics"] = topics
	}
	var logs []LogEntry
	if err := c.do(ctx, "eth_getLogs", []any{filter}, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}

// do is the one-shot HTTP wrapper shared by every RPC method.
// Marshals the JSON-RPC request, posts, decodes the response, and
// unmarshals `result` into `out`.
func (c *Client) do(ctx context.Context, method string, params []any, out any) error {
	id := c.id.Add(1)
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("chainlink: marshal %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("chainlink: new request %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("chainlink: %s transport: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("chainlink: %s read body: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		// Bound the snippet so a giant HTML error page doesn't
		// flood logs. 256 bytes is enough to identify "Cloudflare
		// rate-limited us" vs "Alchemy returned 401" without
		// turning a single failed call into 50KB of journal output.
		snippet := string(body)
		if len(snippet) > 256 {
			snippet = snippet[:256] + "…"
		}
		return fmt.Errorf("chainlink: %s status %d: %s", method, resp.StatusCode, snippet)
	}

	var env jsonRPCResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("chainlink: %s decode: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("chainlink: %s rpc error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("%w: %s returned null", ErrEmptyResult, method)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("chainlink: %s unmarshal result: %w", method, err)
	}
	return nil
}
