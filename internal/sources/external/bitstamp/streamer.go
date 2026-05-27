package bitstamp

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

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// healthyConnectionThreshold — a connection that lived this long
// before disconnecting is considered healthy, and the next reconnect
// resets backoff to InitialBackoff. F-0029.
const healthyConnectionThreshold = 5 * time.Minute

// Streamer implements external.Streamer for Bitstamp. One
// connection per process; sends N subscribe frames on connect
// (Bitstamp does not accept a symbol array like Kraken).
// Honours `bts:request_reconnect` by closing + reconnecting via the
// normal backoff path.
type Streamer struct {
	// PairMap: Bitstamp symbol ("xlmusd") → canonical.Pair. See
	// pairs.go:DefaultPairs.
	PairMap map[string]canonical.Pair

	Logger   *slog.Logger
	Endpoint string

	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

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

// subscribeReq is the JSON shape Bitstamp expects for channel
// subscriptions — `bts:subscribe` with a `channel` name.
type subscribeReq struct {
	Event string           `json:"event"`
	Data  subscribeReqData `json:"data"`
}

type subscribeReqData struct {
	Channel string `json:"channel"`
}

// Start implements external.Streamer.
func (s *Streamer) Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
	if len(pairs) == 0 {
		return nil, errors.New("bitstamp: pairs required")
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

		// F-0029: a healthy long-lived connection rewinds backoff so
		// the next cycle isn't penalised for ancient prior failures.
		if lifetime >= healthyConnectionThreshold {
			backoff = initialBackoff
		}
		// Server-initiated reconnect is benign — log at info, use
		// initial backoff (don't grow the backoff window for a
		// normal rebalance request).
		if errors.Is(err, ErrRequestedReconnect) {
			logger.Info("bitstamp reconnecting per server request",
				"source", SourceName)
			backoff = initialBackoff
		} else {
			logger.Warn("bitstamp stream disconnected, reconnecting",
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

// classifyDisconnect maps a runOnce error into a small reason label
// for the disconnect counter. See binance.classifyDisconnect for the
// same logic — duplicated rather than shared so this package stays
// independently auditable. F-0029.
func classifyDisconnect(err error) string {
	if err == nil {
		return "other"
	}
	if errors.Is(err, ErrRequestedReconnect) {
		return "server_requested"
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

// keepAliveHTTPClient — see binance.keepAliveHTTPClient. F-0029.
func keepAliveHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}
}

func (s *Streamer) runOnce(ctx context.Context, symbols []string, out chan<- canonical.Trade) error { //nolint:gocognit // dispatch-heavy; splitting would reduce linearity
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

	// One subscribe frame per symbol — Bitstamp serialises them.
	for _, sym := range symbols {
		req := subscribeReq{
			Event: "bts:subscribe",
			Data:  subscribeReqData{Channel: ChannelPrefix + sym},
		}
		bs, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal subscribe: %w", err)
		}
		if err := conn.Write(ctx, websocket.MessageText, bs); err != nil {
			return fmt.Errorf("write subscribe %s: %w", sym, err)
		}
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
			if errors.Is(err, ErrRequestedReconnect) {
				return err
			}
			// Malformed or unknown — skip, stream stays up.
			// F-1235 (codex audit-2026-05-12): count it so the
			// decode-error runbook signals on schema drift.
			obs.SourceDecodeErrorsTotal.WithLabelValues("bitstamp").Inc()
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

func (s *Streamer) symbolsFor(pairs []canonical.Pair) ([]string, error) {
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		sym, ok := inverse[p.String()]
		if !ok {
			return nil, fmt.Errorf("bitstamp: pair %s not in configured PairMap", p.String())
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
