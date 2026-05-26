package phoenix

import (
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// TradeEvent is the [consumer.Event] Phoenix's Decoder emits on a
// completed 8-field swap assembly.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "phoenix.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time check that TradeEvent satisfies consumer.Event.
var _ consumer.Event = TradeEvent{}

// LiquidityChange is the canonical projection of one completed
// provide_liquidity / withdraw_liquidity event (5-event or 4-event
// reassembly respectively). Mirrors the phoenix_liquidity row shape
// (migration 0044): pool + sender + per-token (asset,amount) +
// withdraw-only shares amount.
//
// Field semantics:
//   - Action: one of EventActionProvideLiquidity /
//     EventActionWithdrawLiquidity — drives the row's action column
//     and the SQL discriminator.
//   - Pool: emitting contract's C-strkey (event.ContractID).
//   - Sender: G/C-strkey of the LP. On withdraw this is the user
//     burning shares; on provide it's the user depositing tokens.
//   - TokenA / TokenB: classic-or-Soroban asset addresses for the
//     pool's two assets. Withdraw events DO NOT carry these — the
//     contract emits return_amount_a / return_amount_b without the
//     paired token addresses — so they stay empty on withdraw rows.
//     Downstream joins phoenix_liquidity.pool to a recent
//     provide_liquidity row for the same pool to resolve the
//     addresses if needed.
//   - AmountA / AmountB: per-token amount. On provide that's the
//     `actual received` deposit (after slippage truncation); on
//     withdraw that's `return_amount_a` / `return_amount_b`.
//   - SharesAmount: only populated on withdraw rows. The number of
//     LP-share tokens burned. On provide, LP-shares-minted is NOT
//     emitted by the pool contract — the share-token mint shows up
//     as a SEP-41 mint event on the pool's share-token contract,
//     which the sep41_supply observer handles separately.
type LiquidityChange struct {
	Action       string
	Pool         string
	Ledger       uint32
	TxHash       string
	OpIndex      int
	ClosedAt     time.Time
	Sender       string
	TokenA       string
	AmountA      canonical.Amount
	TokenB       string
	AmountB      canonical.Amount
	SharesAmount canonical.Amount // withdraw-only; zero on provide
}

// LiquidityEvent is the [consumer.Event] wrapping a LiquidityChange.
// The sink type-switches on this at its output channel
// (internal/pipeline/sink.go) and writes via
// Store.InsertPhoenixLiquidityChange.
type LiquidityEvent struct {
	Change LiquidityChange
}

// EventKind implements [consumer.Event].
func (LiquidityEvent) EventKind() string { return "phoenix.liquidity" }

// Source implements [consumer.Event].
func (LiquidityEvent) Source() string { return SourceName }

// Compile-time check.
var _ consumer.Event = LiquidityEvent{}

// StakeChange is the canonical projection of one completed
// bond / unbond event (3-event reassembly each). Mirrors the
// phoenix_stake_events row shape (migration 0044).
//
// Phoenix's stake contract is per-pool — there is one stake contract
// per liquidity pool, and it accepts only the pool's LP-share token
// as the staked asset. `Contract` is the stake contract's C-strkey
// (the event emitter), `LPToken` is the share-token contract the
// user is bonding / unbonding, and `Amount` is the share-token
// amount (positive on both bond and unbond — the `Action` discrim
// distinguishes the direction).
type StakeChange struct {
	Action   string
	Contract string
	Ledger   uint32
	TxHash   string
	OpIndex  int
	ClosedAt time.Time
	User     string
	LPToken  string
	Amount   canonical.Amount
}

// StakeEvent is the [consumer.Event] wrapping a StakeChange.
type StakeEvent struct {
	Change StakeChange
}

// EventKind implements [consumer.Event].
func (StakeEvent) EventKind() string { return "phoenix.stake" }

// Source implements [consumer.Event].
func (StakeEvent) Source() string { return SourceName }

// Compile-time check.
var _ consumer.Event = StakeEvent{}

// ─── 8-field correlation buffer ─────────────────────────────────
// Phoenix emits one swap as 8 separate events (one per field).
// An entry sits in the buffer until all 8 slots are populated —
// a missing field (pagination race, contract bug, malformed pool)
// otherwise leaves it hanging forever. Age-based eviction bounds
// memory usage.

// defaultOrphanMaxAge caps how long an incomplete entry waits for
// missing fields. 5 minutes is generous — all 8 events should land
// within the same transaction, seconds apart on-chain.
const defaultOrphanMaxAge = 5 * time.Minute

type buffer struct {
	m      map[groupKey]*RawSwap
	pl     map[groupKey]*RawProvideLiquidity
	wl     map[groupKey]*RawWithdrawLiquidity
	bond   map[groupKey]*RawStake
	unbond map[groupKey]*RawStake
	maxAge time.Duration
	nowFn  func() time.Time
}

func newBuffer() *buffer {
	return &buffer{
		m:      map[groupKey]*RawSwap{},
		pl:     map[groupKey]*RawProvideLiquidity{},
		wl:     map[groupKey]*RawWithdrawLiquidity{},
		bond:   map[groupKey]*RawStake{},
		unbond: map[groupKey]*RawStake{},
		maxAge: defaultOrphanMaxAge,
		nowFn:  time.Now,
	}
}

// absorb stores one field-event in the appropriate RawSwap slot.
// Returns:
//   - completed: non-nil *RawSwap when all 8 slots are populated.
//   - evicted:   entries whose ClosedAt is older than maxAge.
//   - err:       ErrUnknownField / decode errors for the current event.
func (b *buffer) absorb(e *events.Event, fieldTopic string, closedAt time.Time) (completed *RawSwap, evicted []RawSwap, err error) {
	// Reference time for orphan eviction is the incoming event's
	// ClosedAt, not wall-clock — so backfill of historical events
	// correctly compares against the timeline being replayed.
	evicted = b.sweepStale(closedAt)
	// Also age-out stale entries from the liquidity / stake buffers.
	// The dispatcher reports orphan counts in aggregate; counting
	// per-buffer here would just split the same number across labels.
	_ = b.sweepStaleAll(closedAt)

	k := keyOf(e)
	r, ok := b.m[k]
	if !ok {
		r = &RawSwap{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pool: e.ContractID, ClosedAt: closedAt,
		}
		b.m[k] = r
	}
	if err := r.assign(e, fieldTopic); err != nil {
		return nil, evicted, err
	}
	if r.Complete() {
		delete(b.m, k)
		return r, evicted, nil
	}
	return nil, evicted, nil
}

// sweepStale removes entries older than maxAge relative to `ref`,
// returning them as orphans. A zero `ref` falls back to nowFn()
// for drain-at-shutdown calls.
func (b *buffer) sweepStale(ref time.Time) []RawSwap {
	if b.maxAge <= 0 {
		return nil
	}
	if ref.IsZero() {
		ref = b.nowFn()
	}
	cutoff := ref.Add(-b.maxAge)
	var evicted []RawSwap
	for k, r := range b.m {
		if r.ClosedAt.Before(cutoff) {
			evicted = append(evicted, *r)
			delete(b.m, k)
		}
	}
	return evicted
}

// orphans returns incomplete entries. Called after a bounded-range
// ingest ends; incompletes indicate contract or pagination anomaly.
func (b *buffer) orphans() []RawSwap {
	out := make([]RawSwap, 0, len(b.m))
	for _, r := range b.m {
		out = append(out, *r)
	}
	return out
}

// size returns the in-flight swap-entry count. Used by tests.
func (b *buffer) size() int { return len(b.m) }

// ─── provide_liquidity / withdraw_liquidity / bond / unbond absorb ──
//
// Same shape as the swap absorb path: groupKey by (ledger, tx, op);
// stage the field events into the per-action map; return a completed
// record when the action's required field count is met; sweep stale
// peer entries from EVERY buffer using the current event's ClosedAt
// as the reference (so an old swap left over from a different
// contract still ages out when a new liquidity event arrives).
//
// The orphan-eviction counters are advisory: each per-action map has
// its own sweep return so the dispatcher's evictedOrphans tally can
// add them up. We chose per-action maps over a single sum-type buffer
// because the field sets are heterogeneous (5 / 4 / 3 fields) and a
// single map would either store an interface or a fat union — neither
// scales when we add the 8th, 9th… action.

func (b *buffer) absorbProvideLiquidity(e *events.Event, fieldTopic string, closedAt time.Time) (*RawProvideLiquidity, int, error) {
	evicted := b.sweepStaleAll(closedAt)
	k := keyOf(e)
	r, ok := b.pl[k]
	if !ok {
		r = &RawProvideLiquidity{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pool: e.ContractID, ClosedAt: closedAt,
		}
		b.pl[k] = r
	}
	if err := r.assign(e, fieldTopic); err != nil {
		return nil, evicted, err
	}
	if r.Complete() {
		delete(b.pl, k)
		return r, evicted, nil
	}
	return nil, evicted, nil
}

func (b *buffer) absorbWithdrawLiquidity(e *events.Event, fieldTopic string, closedAt time.Time) (*RawWithdrawLiquidity, int, error) {
	evicted := b.sweepStaleAll(closedAt)
	k := keyOf(e)
	r, ok := b.wl[k]
	if !ok {
		r = &RawWithdrawLiquidity{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pool: e.ContractID, ClosedAt: closedAt,
		}
		b.wl[k] = r
	}
	if err := r.assign(e, fieldTopic); err != nil {
		return nil, evicted, err
	}
	if r.Complete() {
		delete(b.wl, k)
		return r, evicted, nil
	}
	return nil, evicted, nil
}

func (b *buffer) absorbStake(e *events.Event, fieldTopic string, closedAt time.Time, isBond bool) (*RawStake, int, error) {
	evicted := b.sweepStaleAll(closedAt)
	k := keyOf(e)
	target := b.unbond
	if isBond {
		target = b.bond
	}
	r, ok := target[k]
	if !ok {
		r = &RawStake{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Contract: e.ContractID, ClosedAt: closedAt, IsBond: isBond,
		}
		target[k] = r
	}
	if err := r.assign(e, fieldTopic); err != nil {
		return nil, evicted, err
	}
	if r.Complete() {
		delete(target, k)
		return r, evicted, nil
	}
	return nil, evicted, nil
}

// sweepStaleAll runs the age-out across every per-action map and
// returns the TOTAL count evicted. The swap buffer's sweep keeps
// its existing typed return (used by the existing test surface);
// here we only need the count for the dispatcher's orphan tally.
func (b *buffer) sweepStaleAll(ref time.Time) int {
	if b.maxAge <= 0 {
		return 0
	}
	if ref.IsZero() {
		ref = b.nowFn()
	}
	cutoff := ref.Add(-b.maxAge)

	n := 0
	for k, r := range b.pl {
		if r.ClosedAt.Before(cutoff) {
			n++
			delete(b.pl, k)
		}
	}
	for k, r := range b.wl {
		if r.ClosedAt.Before(cutoff) {
			n++
			delete(b.wl, k)
		}
	}
	for k, r := range b.bond {
		if r.ClosedAt.Before(cutoff) {
			n++
			delete(b.bond, k)
		}
	}
	for k, r := range b.unbond {
		if r.ClosedAt.Before(cutoff) {
			n++
			delete(b.unbond, k)
		}
	}
	return n
}
