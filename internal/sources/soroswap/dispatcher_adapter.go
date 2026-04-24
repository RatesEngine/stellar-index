package soroswap

import (
	"sync"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// pairTokens captures the (token0, token1) identities of a
// Soroswap pair contract. Populated from factory new_pair events;
// consumed by decodeSwap via the Decoder's registry.
type pairTokens struct {
	Token0 canonical.Asset
	Token1 canonical.Asset
}

// Decoder is the dispatcher-facing view of Soroswap. It owns two
// pieces of state:
//
//  1. A swap+sync correlation buffer (per discovery doc Q-notes;
//     Soroswap emits a SwapEvent followed by an immediately-
//     following SyncEvent in the same transaction).
//  2. A pair→(token0, token1) registry seeded by factory new_pair
//     events. The swap event itself only carries amounts; token
//     identities come from the pair contract's deploy record.
//
// The Decoder processes three topic shapes:
//   - SoroswapPair:swap  → feeds the swap+sync buffer
//   - SoroswapPair:sync  → feeds the swap+sync buffer; completes a pair
//   - SoroswapFactory:new_pair → populates the pair→tokens registry
//
// Other pair-contract events (deposit/withdraw/skim) match but
// produce no output — they're not trades.
//
// Per docs/architecture/ingest-pipeline.md the dispatcher is
// serial, but the mutex is belt-and-braces and also lets operator
// tooling call SeedPair concurrently at startup to warm the cache
// from Timescale.
type Decoder struct {
	mu  sync.RWMutex
	buf *buffer
	// pairTokens maps pair-contract C-strkey → (token0, token1).
	// Populated from factory new_pair events live, and seedable
	// from Timescale at startup via SeedPair.
	pairTokens map[string]pairTokens

	// Counters surfaced for test assertions. Production wiring
	// maps them to obs.SourceOrphanEventsTotal /
	// SourceDecodeErrorsTotal in PR 165d.
	evictedOrphans     int
	skippedUnknownPair int
}

// NewDecoder constructs a Soroswap Decoder with empty state.
func NewDecoder(opts ...DecoderOption) *Decoder {
	d := &Decoder{
		buf:        newBuffer(),
		pairTokens: map[string]pairTokens{},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DecoderOption configures a Decoder at construction time.
type DecoderOption func(*Decoder)

// WithSeededPairTokensDecoder pre-loads the pair→tokens cache.
// Operator tooling calls this at startup to avoid re-walking
// factory events from genesis every boot (we can seed from the
// distinct (source, pair_contract) tuples already persisted in
// the trades hypertable).
func WithSeededPairTokensDecoder(seed map[string]pairTokens) DecoderOption {
	return func(d *Decoder) {
		for k, v := range seed {
			d.pairTokens[k] = v
		}
	}
}

// SeedPair adds a pair→tokens mapping live. Safe to call at any
// time from any goroutine.
func (d *Decoder) SeedPair(pair string, token0, token1 canonical.Asset) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pairTokens[pair] = pairTokens{Token0: token0, Token1: token1}
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Claims any Soroswap pair-
// contract event (swap/sync/deposit/withdraw) or Soroswap factory
// new_pair event. Non-trade events match so recordNewPair gets a
// shot — but Decode returns zero outputs for those.
func (*Decoder) Matches(ev events.Event) bool {
	return classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder].
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := classify(&ev)
	if kind == "" {
		return nil, nil
	}

	// Factory new_pair: populate the registry, emit nothing.
	if kind == EventNewPair {
		fields, err := decodeNewPair(ev.Value)
		if err != nil {
			return nil, err
		}
		d.SeedPair(fields.Pair, fields.Token0, fields.Token1)
		return nil, nil
	}

	// We only care about swap + sync from pair contracts for
	// trade emission. deposit/withdraw match classify but fall
	// through to a no-op return here.
	if kind != EventSwap && kind != EventSync {
		return nil, nil
	}

	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	completed, evicted := d.buf.absorb(&ev, kind, closedAt)
	d.evictedOrphans += len(evicted)
	d.mu.Unlock()

	if len(completed) == 0 {
		return nil, nil // still buffering
	}

	out := make([]consumer.Event, 0, len(completed))
	for _, r := range completed {
		d.mu.RLock()
		tokens, ok := d.pairTokens[r.Pair]
		d.mu.RUnlock()
		if !ok {
			// No factory event seen for this pair yet (either we
			// started ingesting mid-history, or the factory event
			// arrived out-of-order within this same ledger). Skip
			// and count; operator tools can backfill missing pairs
			// from the factory's new_pair history.
			d.mu.Lock()
			d.skippedUnknownPair++
			d.mu.Unlock()
			continue
		}
		trade, err := decodeSwap(r, tokens.Token0, tokens.Token1)
		if err != nil {
			return nil, err
		}
		out = append(out, TradeEvent{Trade: trade})
	}
	return out, nil
}

// EvictedOrphans is the count of swap-only (no matching sync) or
// sync-only (no matching swap) buffer entries dropped by age-out.
func (d *Decoder) EvictedOrphans() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.evictedOrphans
}

// SkippedUnknownPair is the count of completed swap+sync pairs
// whose token mapping wasn't in the registry at decode time.
func (d *Decoder) SkippedUnknownPair() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.skippedUnknownPair
}
