package bitstamp

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
		InitialBackoff: 1 * time.Second,
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
		// Server-initiated reconnect is benign — log at info, use
		// initial backoff (don't grow the backoff window for a
		// normal rebalance request).
		if errors.Is(err, ErrRequestedReconnect) {
			logger.Info("bitstamp reconnecting per server request",
				"source", SourceName)
			backoff = s.InitialBackoff
			if backoff <= 0 {
				backoff = 1 * time.Second
			}
		} else {
			logger.Warn("bitstamp stream disconnected, reconnecting",
				"source", SourceName, "err", err, "backoff", backoff)
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

func (s *Streamer) runOnce(ctx context.Context, symbols []string, out chan<- canonical.Trade) error {
	if s.Endpoint == "" {
		s.Endpoint = WSEndpoint
	}
	conn, _, err := websocket.Dial(ctx, s.Endpoint, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
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
