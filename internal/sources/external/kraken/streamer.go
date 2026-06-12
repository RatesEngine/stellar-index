package kraken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// healthyConnectionThreshold — if a connection survived at least this
// long before disconnecting, treat the next reconnect as a fresh start
// and reset backoff to InitialBackoff. Without this, an indefinite
// stream of healthy multi-minute Kraken cycles eventually pins backoff
// at MaxBackoff forever — losing up to MaxBackoff of data per cycle
// instead of the expected InitialBackoff (F-0029, ported G10-03).
const healthyConnectionThreshold = 5 * time.Minute

// Streamer implements external.Streamer for Kraken's v2 WebSocket
// trade channel. Single connection per process, reconnects with
// bounded exponential backoff + jitter — same lifecycle as Binance.
type Streamer struct {
	// PairMap: Kraken symbol ("XLM/USD") → canonical.Pair. See
	// pairs.go:DefaultPairs.
	PairMap map[string]canonical.Pair

	Logger   *slog.Logger
	Endpoint string

	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// NewStreamer constructs a Streamer with sensible defaults.
//
// Backoff defaults (F-0029, ported G10-03): InitialBackoff 5 s,
// MaxBackoff 60 s. Combined with the healthy-connection reset in run()
// (a connection that stays alive ≥ healthyConnectionThreshold rewinds
// backoff to InitialBackoff on its next failure), the effect is bounded
// 5-60 s reconnect windows instead of a 60 s blanket.
func NewStreamer(pairMap map[string]canonical.Pair) *Streamer {
	return &Streamer{
		PairMap:        pairMap,
		Endpoint:       WSEndpoint,
		InitialBackoff: 5 * time.Second,
		MaxBackoff:     60 * time.Second,
	}
}

// Name implements external.Connector.
func (s *Streamer) Name() string { return SourceName }

// Class implements external.Connector.
func (s *Streamer) Class() external.Class { return external.ClassExchange }

// subscribeReq is the JSON envelope we send post-connect to
// register the trade channel for a symbol list. Kraken v2 accepts
// an array of symbols in a single method call; no need to send N
// separate subscriptions.
type subscribeReq struct {
	Method string         `json:"method"`
	Params subscribeParam `json:"params"`
}

type subscribeParam struct {
	Channel string   `json:"channel"`
	Symbol  []string `json:"symbol"`
}

// Start implements external.Streamer. Connects to v2, subscribes to
// the trade channel for the supplied pairs, spawns the read loop,
// returns a channel that emits canonical.Trade values until ctx
// cancel or unrecoverable error.
func (s *Streamer) Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
	if len(pairs) == 0 {
		return nil, errors.New("kraken: pairs required")
	}
	symbols, err := s.symbolsFor(pairs)
	if err != nil {
		return nil, err
	}

	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	out := make(chan canonical.Trade, 128)
	go s.run(ctx, symbols, logger, out)
	return out, nil
}

func (s *Streamer) run(ctx context.Context, symbols []string, logger *slog.Logger, out chan<- canonical.Trade) {
	defer close(out)

	initialBackoff := s.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = 5 * time.Second
	}
	maxBackoff := s.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}
	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return
		}
		connectedAt := time.Now()
		err := s.runOnce(ctx, symbols, out)
		if ctx.Err() != nil {
			return
		}
		lifetime := time.Since(connectedAt)
		reason := classifyDisconnect(err)
		obs.CEXStreamDisconnectTotal.WithLabelValues(SourceName, reason).Inc()

		// Healthy-lifetime reset (F-0029): a long-lived connection that
		// finally dropped is NOT evidence of a wedged venue — reset the
		// backoff so the next cycle isn't penalised for prior failures.
		if lifetime >= healthyConnectionThreshold {
			backoff = initialBackoff
		}
		logger.Warn("kraken stream disconnected, reconnecting",
			"source", SourceName, "err", err,
			"lifetime", lifetime, "backoff", backoff, "reason", reason)

		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// classifyDisconnect maps a runOnce error into a small, bounded reason
// label set — keeps the disconnect counter's cardinality low while
// distinguishing the wire-level cause. Mirrors the binance helper.
func classifyDisconnect(err error) string {
	if err == nil {
		return "other"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection reset by peer"):
		return "reset"
	case strings.Contains(msg, "broken pipe"):
		return "broken_pipe"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.HasPrefix(msg, "dial:"):
		return "dial"
	default:
		return "other"
	}
}

// keepAliveHTTPClient builds an *http.Client whose Transport dials TCP
// with a 30 s OS-level keepalive. Go's net.Dialer defaults to no
// keepalive on the underlying socket; venues that issue TCP RST after
// their own timeout window then surface as "connection reset by peer"
// reads instead of being detected earlier by the dialer. F-0029.
func keepAliveHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}
}

func (s *Streamer) runOnce(ctx context.Context, symbols []string, out chan<- canonical.Trade) error {
	if s.Endpoint == "" {
		s.Endpoint = WSEndpoint
	}
	conn, resp, err := websocket.Dial(ctx, s.Endpoint, &websocket.DialOptions{
		HTTPClient: keepAliveHTTPClient(),
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "client shutdown") }()

	// Send subscribe AFTER the status frame arrives on real
	// Kraken. Doing so upfront works too — v2 queues the
	// subscription until the session is ready. We send immediately
	// for simplicity.
	sub := subscribeReq{
		Method: "subscribe",
		Params: subscribeParam{
			Channel: ChannelTrade,
			Symbol:  symbols,
		},
	}
	subBytes, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("marshal subscribe: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, subBytes); err != nil {
		return fmt.Errorf("write subscribe: %w", err)
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		trades, err := parseFrame(data, s.PairMap)
		if err != nil {
			// Malformed frame — skip, stream stays up.
			// F-1235 (codex audit-2026-05-12): count it so the
			// decode-error runbook signals on schema drift.
			obs.SourceDecodeErrorsTotal.WithLabelValues("kraken").Inc()
			continue
		}
		for _, t := range trades {
			select {
			case <-ctx.Done():
				return nil
			case out <- t:
			}
		}
	}
}

func (s *Streamer) symbolsFor(pairs []canonical.Pair) ([]string, error) {
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		sym, ok := inverse[p.String()]
		if !ok {
			return nil, fmt.Errorf("kraken: pair %s not in configured PairMap", p.String())
		}
		out = append(out, sym)
	}
	return out, nil
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := float64(d) * 0.25
	offset := (rand.Float64()*2 - 1) * delta
	return d + time.Duration(offset)
}
