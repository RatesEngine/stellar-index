package coinbase

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
// and reset backoff to InitialBackoff. Prevents an indefinite stream
// of healthy multi-minute Coinbase cycles from pinning backoff at
// MaxBackoff forever (F-0029, ported G10-03).
const healthyConnectionThreshold = 5 * time.Minute

// Streamer implements external.Streamer for Coinbase Exchange.
// Single subscription (with an array of product_ids) covers every
// configured pair on one connection.
type Streamer struct {
	PairMap        map[string]canonical.Pair
	Logger         *slog.Logger
	Endpoint       string
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// NewStreamer constructs a Streamer with sensible defaults.
//
// Backoff defaults (F-0029, ported G10-03): InitialBackoff 5 s,
// MaxBackoff 60 s, plus the healthy-connection reset in run().
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

type subscribeReq struct {
	Type     string             `json:"type"`
	Channels []subscribeChannel `json:"channels"`
}

type subscribeChannel struct {
	Name       string   `json:"name"`
	ProductIDs []string `json:"product_ids"`
}

// Start implements external.Streamer.
func (s *Streamer) Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
	if len(pairs) == 0 {
		return nil, errors.New("coinbase: pairs required")
	}
	products, err := s.productsFor(pairs)
	if err != nil {
		return nil, err
	}

	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	out := make(chan canonical.Trade, 128)
	go s.run(ctx, products, logger, out)
	return out, nil
}

func (s *Streamer) run(ctx context.Context, products []string, logger *slog.Logger, out chan<- canonical.Trade) {
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
		err := s.runOnce(ctx, products, out)
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

		// Subscription rejection is usually a config bug — log
		// loudly but still reconnect (operator may have fixed the
		// config mid-flight).
		if errors.Is(err, ErrSubscriptionRejected) {
			logger.Error("coinbase subscription rejected — check product_ids in DefaultPairs",
				"source", SourceName, "err", err, "reason", reason)
		} else {
			logger.Warn("coinbase stream disconnected, reconnecting",
				"source", SourceName, "err", err,
				"lifetime", lifetime, "backoff", backoff, "reason", reason)
		}
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
// distinguishing the wire-level cause. The ErrSubscriptionRejected
// case gets its own label so operators can tell a config-reject loop
// apart from transient wire drops. Mirrors the binance helper.
func classifyDisconnect(err error) string {
	if err == nil {
		return "other"
	}
	if errors.Is(err, ErrSubscriptionRejected) {
		return "subscription_rejected"
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

func (s *Streamer) runOnce(ctx context.Context, products []string, out chan<- canonical.Trade) error {
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

	sub := subscribeReq{
		Type: "subscribe",
		Channels: []subscribeChannel{
			{Name: ChannelName, ProductIDs: products},
		},
	}
	bs, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("marshal subscribe: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, bs); err != nil {
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
		trade, isTrade, err := parseFrame(data, s.PairMap)
		if err != nil {
			if errors.Is(err, ErrSubscriptionRejected) {
				return err
			}
			// F-1235 (codex audit-2026-05-12): count parse errors
			// so the decode-error runbook signals on schema drift.
			obs.SourceDecodeErrorsTotal.WithLabelValues("coinbase").Inc()
			continue
		}
		if !isTrade {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case out <- trade:
		}
	}
}

func (s *Streamer) productsFor(pairs []canonical.Pair) ([]string, error) {
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		sym, ok := inverse[p.String()]
		if !ok {
			return nil, fmt.Errorf("coinbase: pair %s not in configured PairMap", p.String())
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
