package soroswap

import (
	"sync"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// PairTokens captures the (token0, token1) identities of a
// Soroswap pair contract. Populated from factory new_pair events;
// consumed by decodeSwap via the Decoder's registry.
//
// Exported so callers outside the package can construct a seed map
// for [WithSeededPairTokensDecoder]. The indexer and backfill chunks
// build one of these from `timescale.LoadSoroswapPairRegistry` rows.
type PairTokens struct {
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
// The Decoder processes four topic shapes:
//   - SoroswapPair:swap  → feeds the swap+sync buffer
//   - SoroswapPair:sync  → feeds the swap+sync buffer; completes a pair
//   - SoroswapPair:skim  → emits a SkimEvent (excess-reserves claim)
//   - SoroswapFactory:new_pair → populates the pair→tokens registry
//
// Other pair-contract events (deposit/withdraw) match but produce
// no output — they're not trades and have their own follow-ups.
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
	pairTokens map[string]PairTokens

	// onNewPair, when non-nil, is invoked for every factory
	// new_pair event after the in-memory registry is updated. The
	// indexer + backfill main wire this to a postgres-backed
	// upsert so the mapping survives process restarts and is
	// visible to other parallel backfill chunks. Hook is called
	// with the decoder's mutex held — keep it cheap; the storage
	// implementation should ExecContext non-blockingly or buffer.
	onNewPair func(pairStrkey, token0Strkey, token1Strkey string)

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
		pairTokens: map[string]PairTokens{},
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
func WithSeededPairTokensDecoder(seed map[string]PairTokens) DecoderOption {
	return func(d *Decoder) {
		for k, v := range seed {
			d.pairTokens[k] = v
		}
	}
}

// WithPairUpsertHook installs a callback fired whenever the decoder
// observes a factory new_pair event. The callback receives the C-strkey
// of the pair contract and the C-strkeys of token_0 / token_1.
//
// Lets the indexer + backfill chunks persist the (pair, tokens)
// mapping to durable storage so future restarts and other parallel
// chunks inherit the registry. The hook is fired with the decoder's
// mutex held — keep it cheap (a queued ExecContext is fine; a
// blocking network call is not).
func WithPairUpsertHook(hook func(pairStrkey, token0Strkey, token1Strkey string)) DecoderOption {
	return func(d *Decoder) {
		d.onNewPair = hook
	}
}

// SeedPair adds a pair→tokens mapping live. Safe to call at any
// time from any goroutine. Fires the registered onNewPair hook (if
// any) so callers using SeedFromFactoryRPC also get the persistence
// side-effect for free.
func (d *Decoder) SeedPair(pair string, token0, token1 canonical.Asset) {
	d.mu.Lock()
	d.pairTokens[pair] = PairTokens{Token0: token0, Token1: token1}
	hook := d.onNewPair
	d.mu.Unlock()
	if hook != nil {
		hook(pair, token0.ContractID, token1.ContractID)
	}
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

	// Factory new_pair: populate the registry (which fires the
	// onNewPair hook for persistence), emit nothing.
	if kind == EventNewPair {
		fields, err := decodeNewPair(ev.Value)
		if err != nil {
			return nil, err
		}
		d.SeedPair(fields.Pair, fields.Token0, fields.Token1)
		return nil, nil
	}

	// Pair-contract skim: emit a SkimEvent so the storage sink can
	// land a row in soroswap_skim_events. Standalone event — does
	// NOT feed the swap+sync correlation buffer (skim is its own
	// pair-state mutation, not a trade).
	if kind == EventSkim {
		closedAt, err := ev.EventClosedAt()
		if err != nil {
			return nil, err
		}
		fields, err := decodeSkim(ev.Value)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{SkimEvent{
			ContractID: ev.ContractID,
			Ledger:     ev.Ledger,
			TxHash:     ev.TxHash,
			OpIndex:    uint32(ev.OperationIndex),
			EventIndex: 0,
			ObservedAt: closedAt,
			To:         fields.To,
			Amount0:    fields.Amount0,
			Amount1:    fields.Amount1,
		}}, nil
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
