package binance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// healthyConnectionThreshold — if a connection survived at least this
// long before disconnecting, treat the next reconnect as a fresh start
// and reset backoff to InitialBackoff. Without this, an indefinite
// stream of healthy 6-12 min Binance cycles (their 24h policy plus
// PING-driven recycles) eventually pins backoff at MaxBackoff (60 s)
// forever — losing ~60 s of data per cycle instead of the expected
// 5 s (F-0029).
const healthyConnectionThreshold = 5 * time.Minute

// Streamer implements external.Streamer for Binance's combined
// aggTrade feed. One instance per indexer process — serialises all
// subscribed pairs onto a single WebSocket.
type Streamer struct {
	// PairMap maps Binance symbol (e.g. "XLMUSDT") to the canonical
	// Pair to stamp on emitted trades. Required at construction
	// time; Start() rejects pairs not present here rather than
	// subscribing blind.
	PairMap map[string]canonical.Pair

	// Logger receives structured reconnect / error messages. If
	// nil, slog.Default() is used.
	Logger *slog.Logger

	// Endpoint overrides the wss:// URL. Default is [WSEndpoint];
	// integration tests use this to point at an httptest WS server.
	Endpoint string

	// InitialBackoff is the first reconnect delay after a dropped
	// connection. Each subsequent failure doubles it (with jitter)
	// up to MaxBackoff. Defaults to 5 s (F-0029).
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential growth. Defaults to 60 s.
	MaxBackoff time.Duration
}

// NewStreamer constructs a Streamer with the supplied pair map and
// sensible defaults for the rest. Logger defaults to slog.Default().
//
// Backoff defaults (F-0029, audit-2026-05-27): InitialBackoff 5 s,
// MaxBackoff 60 s. Combined with the healthy-connection reset in
// run() (a connection that stays alive ≥ healthyConnectionThreshold
// rewinds backoff to InitialBackoff on its next failure), the effect
// is bounded 5-60 s reconnect windows instead of the 60 s blanket
// observed pre-fix on r1.
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

// Start implements external.Streamer. Connects to the combined
// stream for the requested pairs, parses frames, and emits
// canonical.Trade values until ctx is cancelled. Reconnects with
// bounded exponential backoff on dropped connections; only persistent
// configuration errors (empty pair list, URL that doesn't parse)
// return through Start itself.
//
// Empty `pairs` is rejected — Binance requires explicit subscription.
// Auto-enumeration of all listed symbols is a future capability; for
// v1 the operator configures the pair set explicitly via the indexer
// config.
func (s *Streamer) Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
	if len(pairs) == 0 {
		return nil, errors.New("binance: pairs required — auto-enumeration not yet supported")
	}
	symbols, err := s.symbolsFor(pairs)
	if err != nil {
		return nil, err
	}
	streamURL, err := s.buildStreamURL(symbols)
	if err != nil {
		return nil, err
	}

	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	out := make(chan canonical.Trade, 128)

	go s.run(ctx, streamURL, logger, out)

	return out, nil
}

// run is the reconnect-forever loop. Closes `out` on ctx
// cancellation; transient errors cause exponential-backoff reconnect
// without closing the channel (downstream consumers see a gap in
// timestamps but no stream termination).
//
// F-0029 (audit-2026-05-27): backoff resets to InitialBackoff on any
// runOnce that lived ≥ healthyConnectionThreshold. Before the fix,
// production r1 logs showed backoff pinned at 60 s because every
// Binance recycle (every 6-12 min) doubled an already-large window
// and there was no reset path; the indexer was dropping ~60 s of CEX
// trades per cycle.
func (s *Streamer) run(ctx context.Context, streamURL string, logger *slog.Logger, out chan<- canonical.Trade) {
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
		err := s.runOnce(ctx, streamURL, out)
		if ctx.Err() != nil {
			return
		}
		lifetime := time.Since(connectedAt)
		reason := classifyDisconnect(err)
		obs.CEXStreamDisconnectTotal.WithLabelValues(SourceName, reason).Inc()

		// Healthy-lifetime reset: a long-lived connection that finally
		// dropped is NOT evidence of a wedged venue — reset the
		// backoff so the next cycle isn't penalised for prior failures.
		if lifetime >= healthyConnectionThreshold {
			backoff = initialBackoff
		}
		// Transient — log, backoff, retry.
		logger.Warn("binance stream disconnected, reconnecting",
			"source", SourceName, "err", err,
			"lifetime", lifetime, "backoff", backoff, "reason", reason)

		sleep := jitter(backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// classifyDisconnect maps a runOnce error into one of a small,
// bounded reason label set — keeps the disconnect counter's
// cardinality low while still distinguishing the wire-level cause.
// Reasons: "reset" (TCP RST from venue), "broken_pipe" (write failed,
// peer hung up mid-frame), "timeout" (read timed out), "dial"
// (handshake failed), "other" (everything else, including EOF /
// context cancellations that slipped past the ctx.Err() check).
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

// keepAliveHTTPClient builds an *http.Client whose Transport dials
// TCP with a 30 s OS-level keepalive. Go's net.Dialer defaults to
// no keepalive on the underlying socket; venues that issue TCP RST
// after their own timeout window then surface as "connection reset
// by peer" reads instead of being detected earlier by the dialer.
// F-0029.
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

// runOnce handles one connect-and-read cycle. Returns on EOF, ctx
// cancel (the caller checks ctx), or read/parse error. Successful
// close returns nil; disconnect scenarios return an error so run()
// can decide whether to backoff.
func (s *Streamer) runOnce(ctx context.Context, streamURL string, out chan<- canonical.Trade) error {
	conn, resp, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{
		HTTPClient: keepAliveHTTPClient(),
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "client shutdown")
	}()

	// Binance disconnects clients after 24h of connection life —
	// deliberate server-side policy. We just handle that like any
	// other disconnect; bounded backoff reconnects.

	for {
		if ctx.Err() != nil {
			return nil
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		trade, err := parseAggTradeFrame(data, s.PairMap)
		if err != nil {
			// Single-frame parse errors are non-fatal (e.g. a new
			// symbol subscribed that isn't in PairMap yet). Count
			// + continue; dropping the whole stream would be a
			// gross overreaction to one bad line. F-1235 (codex
			// audit-2026-05-12): the count line was previously
			// missing — operators had no signal on schema drift
			// despite the runbook claiming decode-error coverage.
			obs.SourceDecodeErrorsTotal.WithLabelValues("binance").Inc()
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case out <- trade:
		}
	}
}

// symbolsFor resolves canonical.Pair → Binance symbol by inverting
// s.PairMap. Unknown pairs are rejected — we never subscribe to
// a symbol we can't decode on the way back.
func (s *Streamer) symbolsFor(pairs []canonical.Pair) ([]string, error) {
	// Build inverse map once; O(pairs × map) is fine for small N.
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		sym, ok := inverse[p.String()]
		if !ok {
			return nil, fmt.Errorf("binance: pair %s not in configured PairMap — add mapping before subscribing", p.String())
		}
		out = append(out, sym)
	}
	return out, nil
}

// buildStreamURL turns a list of Binance symbols into the combined-
// stream URL. Format:
//
//	wss://stream.binance.com:9443/stream?streams=xlmusdt@aggTrade/btcusdt@aggTrade
//
// Symbols are lowercased per Binance convention for the URL (the
// wire-side Symbol field arrives uppercase).
func (s *Streamer) buildStreamURL(symbols []string) (string, error) {
	if s.Endpoint == "" {
		s.Endpoint = WSEndpoint
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint parse: %w", err)
	}
	streams := make([]string, len(symbols))
	for i, sym := range symbols {
		streams[i] = strings.ToLower(sym) + "@aggTrade"
	}
	q := u.Query()
	q.Set("streams", strings.Join(streams, "/"))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// jitter adds ±25% random variation to a backoff duration to avoid
// thundering-herd reconnects if many streamers happen to time their
// retries on the same boundary.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := float64(d) * 0.25
	offset := (rand.Float64()*2 - 1) * delta
	return d + time.Duration(offset)
}
