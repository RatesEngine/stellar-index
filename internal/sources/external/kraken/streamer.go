package kraken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/coder/websocket"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

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
func NewStreamer(pairMap map[string]canonical.Pair) *Streamer {
	return &Streamer{
		PairMap:        pairMap,
		Endpoint:       WSEndpoint,
		InitialBackoff: 1 * time.Second,
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

	backoff := s.InitialBackoff
	if backoff <= 0 {
		backoff = 1 * time.Second
	}
	maxBackoff := s.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}

	for {
		if ctx.Err() != nil {
			return
		}
		err := s.runOnce(ctx, symbols, out)
		if ctx.Err() != nil {
			return
		}
		logger.Warn("kraken stream disconnected, reconnecting",
			"source", SourceName, "err", err, "backoff", backoff)

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

func (s *Streamer) runOnce(ctx context.Context, symbols []string, out chan<- canonical.Trade) error {
	if s.Endpoint == "" {
		s.Endpoint = WSEndpoint
	}
	conn, resp, err := websocket.Dial(ctx, s.Endpoint, nil)
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
