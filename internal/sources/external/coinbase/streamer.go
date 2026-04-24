package coinbase

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
		err := s.runOnce(ctx, products, out)
		if ctx.Err() != nil {
			return
		}
		// Subscription rejection is usually a config bug — log
		// loudly but still reconnect (operator may have fixed the
		// config mid-flight).
		if errors.Is(err, ErrSubscriptionRejected) {
			logger.Error("coinbase subscription rejected — check product_ids in DefaultPairs",
				"source", SourceName, "err", err)
		} else {
			logger.Warn("coinbase stream disconnected, reconnecting",
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

func (s *Streamer) runOnce(ctx context.Context, products []string, out chan<- canonical.Trade) error {
	if s.Endpoint == "" {
		s.Endpoint = WSEndpoint
	}
	conn, _, err := websocket.Dial(ctx, s.Endpoint, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
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
