package phoenix

import (
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// Tests for the provide_liquidity / withdraw_liquidity / bond /
// unbond reassembly + decode paths (Task #27).
//
// We exercise the same surface area as the swap tests:
//   - classify the topic[0] to the right action
//   - absorb the N field-events into the correlation buffer
//   - confirm the action completes on the Nth field, not earlier
//   - prove out-of-order arrival still completes (no order
//     dependency, mirroring the swap robustness guarantee)
//   - prove the dispatcher_adapter routes through to the right
//     consumer.Event subtype
//   - cover decode errors / unknown-field rejection / orphans /
//     backfilled-age robustness
//
// Body decoding uses the shared SDK fakes installed by
// install*Fakes — same pattern as TestDecodeSwap_happyPath.

const (
	plPool    = "CDPL000000000000000000000000000000000000000000000000A"
	wlPool    = "CDWL000000000000000000000000000000000000000000000000B"
	stakeC    = "CDSTAKE000000000000000000000000000000000000000000000C"
	plTokenA  = "CDTKNA000000000000000000000000000000000000000000000D"
	plTokenB  = "CDTKNB000000000000000000000000000000000000000000000E"
	plSender  = "GPLSENDER0000000000000000000000000000000000000000000F"
	plTxHash  = "feedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfee0"
	wlTxHash  = "feedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfee1"
	bondTx    = "feedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfee2"
	unbondTx  = "feedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfeedfee3"
	lpTokenC  = "CDLPTKN0000000000000000000000000000000000000000000000G"
	stakeUser = "GPLBONDR0000000000000000000000000000000000000000000000H"
)

// ─── classifyAny ─────────────────────────────────────────────────

func TestClassifyAny(t *testing.T) {
	cases := []struct {
		name       string
		topics     []string
		wantAction action
		wantField  string
	}{
		{"swap sender", []string{TopicSymbolSwap, TopicSymbolSender}, actionSwap, TopicSymbolSender},
		{"provide token_a-amount", []string{TopicSymbolProvideLiquidity, TopicSymbolPLTokenAAmt}, actionProvideLiquidity, TopicSymbolPLTokenAAmt},
		{"withdraw shares", []string{TopicSymbolWithdrawLiquidity, TopicSymbolWLSharesAmount}, actionWithdrawLiquidity, TopicSymbolWLSharesAmount},
		{"withdraw auto unbonded", []string{TopicSymbolWithdrawLiquidity, TopicSymbolWLAutoUnbonded}, actionWithdrawLiquidity, TopicSymbolWLAutoUnbonded},
		{"bond user", []string{TopicSymbolBond, TopicSymbolStakeUser}, actionBond, TopicSymbolStakeUser},
		{"unbond amount", []string{TopicSymbolUnbond, TopicSymbolStakeAmount}, actionUnbond, TopicSymbolStakeAmount},
		// EVERY-event policy — admin + initialize were silently dropped pre-2026-05-27.
		{"admin replacement requested", []string{TopicSymbolAdmin, "any-detail"}, actionAdmin, "any-detail"},
		{"initialize token_a", []string{TopicSymbolInitialize, "any-detail"}, actionInitialize, "any-detail"},
		{"unknown topic[0]", []string{"some_other_action", TopicSymbolStakeAmount}, actionUnknown, ""},
		{"too few topics", []string{TopicSymbolBond}, actionUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, f := classifyAny(&events.Event{Topic: tc.topics})
			if a != tc.wantAction || f != tc.wantField {
				t.Errorf("classifyAny = (%v, %q); want (%v, %q)", a, f, tc.wantAction, tc.wantField)
			}
		})
	}
}

// ─── shared fakes ─────────────────────────────────────────────────

// installAddressI128Fakes swaps the SDK decoders for deterministic
// fakes — same pattern as TestDecodeSwap_happyPath. The fakes
// interpret a body string as either an address tag ("addr:<C>") or
// an i128 decimal ("i128:<n>"), so test data is human-readable in
// the test source.
func installAddressI128Fakes(t *testing.T) (restore func()) {
	t.Helper()
	prevAddr, prevAsset, prevI128 := decodeAddress, decodeAsset, decodeI128
	decodeAddress = func(v string) (string, error) {
		if len(v) > 5 && v[:5] == "addr:" {
			return v[5:], nil
		}
		t.Fatalf("fake decodeAddress: unexpected body %q", v)
		return "", nil
	}
	decodeI128 = func(v string) (canonical.Amount, error) {
		if len(v) > 5 && v[:5] == "i128:" {
			n := new(big.Int)
			if _, ok := n.SetString(v[5:], 10); !ok {
				t.Fatalf("fake decodeI128: bad number %q", v)
			}
			return canonical.NewAmount(n), nil
		}
		t.Fatalf("fake decodeI128: unexpected body %q", v)
		return canonical.NewAmount(big.NewInt(0)), nil
	}
	decodeAsset = prevAsset // unused for liquidity / stake paths
	return func() {
		decodeAddress, decodeAsset, decodeI128 = prevAddr, prevAsset, prevI128
	}
}

func plClosedAt() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) }

func plField(topic1, value, txHash string) events.Event {
	return events.Event{
		Topic:          []string{TopicSymbolProvideLiquidity, topic1},
		Value:          value,
		Ledger:         62_500_000,
		TxHash:         txHash,
		OperationIndex: 0,
		LedgerClosedAt: plClosedAt().Format(time.RFC3339),
		ContractID:     plPool,
	}
}

func wlField(topic1, value, txHash string) events.Event {
	return events.Event{
		Topic:          []string{TopicSymbolWithdrawLiquidity, topic1},
		Value:          value,
		Ledger:         62_500_000,
		TxHash:         txHash,
		OperationIndex: 0,
		LedgerClosedAt: plClosedAt().Format(time.RFC3339),
		ContractID:     wlPool,
	}
}

func bondField(topic1, value, txHash string) events.Event {
	return events.Event{
		Topic:          []string{TopicSymbolBond, topic1},
		Value:          value,
		Ledger:         62_500_001,
		TxHash:         txHash,
		OperationIndex: 0,
		LedgerClosedAt: plClosedAt().Format(time.RFC3339),
		ContractID:     stakeC,
	}
}

func unbondField(topic1, value, txHash string) events.Event {
	return events.Event{
		Topic:          []string{TopicSymbolUnbond, topic1},
		Value:          value,
		Ledger:         62_500_002,
		TxHash:         txHash,
		OperationIndex: 0,
		LedgerClosedAt: plClosedAt().Format(time.RFC3339),
		ContractID:     stakeC,
	}
}

// ─── provide_liquidity completes on 5th field ─────────────────────

func TestDecoder_ProvideLiquidity_completesOnFifthField(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolPLSender, "addr:" + plSender},
		{TopicSymbolPLTokenA, "addr:" + plTokenA},
		{TopicSymbolPLTokenAAmt, "i128:1000000000"}, // 100 token_a
		{TopicSymbolPLTokenB, "addr:" + plTokenB},
		{TopicSymbolPLTokenBAmt, "i128:50000000"}, // 5 token_b
	}

	var out []consumer.Event
	for i, f := range fields {
		emitted, err := d.Decode(plField(f.topic, f.body, plTxHash))
		if err != nil {
			t.Fatalf("field %d (%s): %v", i, f.topic, err)
		}
		if i < 4 && len(emitted) != 0 {
			t.Fatalf("field %d: got %d events, want 0 (still buffering)", i, len(emitted))
		}
		if i == 4 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	le, ok := out[0].(LiquidityEvent)
	if !ok {
		t.Fatalf("expected LiquidityEvent, got %T", out[0])
	}
	if le.Change.Action != EventActionProvideLiquidity {
		t.Errorf("Action = %q, want %q", le.Change.Action, EventActionProvideLiquidity)
	}
	if le.Change.Pool != plPool {
		t.Errorf("Pool = %q, want %q", le.Change.Pool, plPool)
	}
	if le.Change.Sender != plSender {
		t.Errorf("Sender = %q, want %q", le.Change.Sender, plSender)
	}
	if le.Change.TokenA != plTokenA || le.Change.TokenB != plTokenB {
		t.Errorf("tokens = (%q,%q), want (%q,%q)", le.Change.TokenA, le.Change.TokenB, plTokenA, plTokenB)
	}
	if le.Change.AmountA.BigInt().Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("AmountA = %s", le.Change.AmountA)
	}
	if le.Change.AmountB.BigInt().Cmp(big.NewInt(50_000_000)) != 0 {
		t.Errorf("AmountB = %s", le.Change.AmountB)
	}
}

func TestDecoder_ProvideLiquidity_outOfOrder(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	// Reverse contract emission order — the buffer is order-independent.
	fields := []struct{ topic, body string }{
		{TopicSymbolPLTokenBAmt, "i128:50000000"},
		{TopicSymbolPLTokenB, "addr:" + plTokenB},
		{TopicSymbolPLTokenAAmt, "i128:1000000000"},
		{TopicSymbolPLTokenA, "addr:" + plTokenA},
		{TopicSymbolPLSender, "addr:" + plSender},
	}
	var out []consumer.Event
	for i, f := range fields {
		emitted, err := d.Decode(plField(f.topic, f.body, plTxHash))
		if err != nil {
			t.Fatalf("field %d (%s): %v", i, f.topic, err)
		}
		if i == 4 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1 after 5th field", len(out))
	}
}

// ─── withdraw_liquidity ─────────────────────────────────────────

func TestDecoder_WithdrawLiquidity_completesOnFourthField(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolWLSender, "addr:" + plSender},
		{TopicSymbolWLSharesAmount, "i128:7000000"},
		{TopicSymbolWLReturnAmountA, "i128:99000000"},
		{TopicSymbolWLReturnAmountB, "i128:4900000"},
	}
	var out []consumer.Event
	for i, f := range fields {
		emitted, err := d.Decode(wlField(f.topic, f.body, wlTxHash))
		if err != nil {
			t.Fatalf("field %d (%s): %v", i, f.topic, err)
		}
		if i < 3 && len(emitted) != 0 {
			t.Fatalf("field %d: got %d events, want 0 (still buffering)", i, len(emitted))
		}
		if i == 3 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	le := out[0].(LiquidityEvent)
	if le.Change.Action != EventActionWithdrawLiquidity {
		t.Errorf("Action = %q", le.Change.Action)
	}
	if le.Change.Pool != wlPool {
		t.Errorf("Pool = %q, want %q", le.Change.Pool, wlPool)
	}
	if le.Change.SharesAmount.BigInt().Cmp(big.NewInt(7_000_000)) != 0 {
		t.Errorf("SharesAmount = %s", le.Change.SharesAmount)
	}
	// Withdraw rows must NOT carry token addresses (contract doesn't emit them).
	if le.Change.TokenA != "" || le.Change.TokenB != "" {
		t.Errorf("withdraw token addresses leaked: a=%q b=%q", le.Change.TokenA, le.Change.TokenB)
	}
}

// TestDecoder_WithdrawLiquidity_optionalAutoUnbondedIgnored proves
// the optional 5th event (auto unbonded) is recognised (no
// ErrUnknownField) but discarded — the withdraw record completes on
// the 4 required fields regardless of its arrival.
func TestDecoder_WithdrawLiquidity_optionalAutoUnbondedIgnored(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolWLSender, "addr:" + plSender},
		{TopicSymbolWLAutoUnbonded, "ignored-tuple-body"}, // interleaved — must not break correlation
		{TopicSymbolWLSharesAmount, "i128:7000000"},
		{TopicSymbolWLReturnAmountA, "i128:99000000"},
		{TopicSymbolWLReturnAmountB, "i128:4900000"},
	}
	var out []consumer.Event
	for _, f := range fields {
		emitted, err := d.Decode(wlField(f.topic, f.body, wlTxHash))
		if err != nil {
			t.Fatalf("field %s: %v", f.topic, err)
		}
		if len(emitted) > 0 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1 (auto unbonded should not block completion)", len(out))
	}
}

// ─── bond / unbond ──────────────────────────────────────────────

func TestDecoder_Bond_completesOnThirdField(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolStakeUser, "addr:" + stakeUser},
		{TopicSymbolStakeToken, "addr:" + lpTokenC},
		{TopicSymbolStakeAmount, "i128:12345678"},
	}
	var out []consumer.Event
	for i, f := range fields {
		emitted, err := d.Decode(bondField(f.topic, f.body, bondTx))
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		if i < 2 && len(emitted) != 0 {
			t.Fatalf("field %d: got %d events, want 0 (still buffering)", i, len(emitted))
		}
		if i == 2 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	se := out[0].(StakeEvent)
	if se.Change.Action != EventActionBond {
		t.Errorf("Action = %q, want %q", se.Change.Action, EventActionBond)
	}
	if se.Change.Contract != stakeC {
		t.Errorf("Contract = %q", se.Change.Contract)
	}
	if se.Change.User != stakeUser || se.Change.LPToken != lpTokenC {
		t.Errorf("user/token = (%q, %q)", se.Change.User, se.Change.LPToken)
	}
	if se.Change.Amount.BigInt().Cmp(big.NewInt(12_345_678)) != 0 {
		t.Errorf("Amount = %s", se.Change.Amount)
	}
}

// TestDecoder_BondAndUnbond_independentBuffers proves bond + unbond
// from the same (ledger, tx, op) do NOT collide — they use distinct
// per-action correlation maps. (Phoenix's stake contract uses the
// same field name `amount` for both, so a single shared map would
// merge them.)
func TestDecoder_BondAndUnbond_independentBuffers(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	// Same ledger / tx / op shared across bond + unbond — proves
	// per-action sharding of the buffer.
	const sharedTx = "shareeshareeshareeshareeshareeshareeshareeshareeshareesharee123"

	bondFields := []struct{ topic, body string }{
		{TopicSymbolStakeUser, "addr:" + stakeUser},
		{TopicSymbolStakeToken, "addr:" + lpTokenC},
		{TopicSymbolStakeAmount, "i128:1000"},
	}
	unbondFields := []struct{ topic, body string }{
		{TopicSymbolStakeUser, "addr:" + stakeUser},
		{TopicSymbolStakeToken, "addr:" + lpTokenC},
		{TopicSymbolStakeAmount, "i128:500"},
	}

	emit := func(ev events.Event) consumer.Event {
		out, err := d.Decode(ev)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out) == 1 {
			return out[0]
		}
		return nil
	}

	// Interleave: bond[0], unbond[0], bond[1], unbond[1], bond[2], unbond[2]
	var bondOut, unbondOut consumer.Event
	for i := 0; i < 3; i++ {
		bondEv := bondField(bondFields[i].topic, bondFields[i].body, sharedTx)
		if v := emit(bondEv); v != nil {
			bondOut = v
		}
		// Force unbond into the SAME (ledger, tx, op) by overriding
		// — distinct only in the action topic[0]. This is the worst
		// case for buffer-key collision.
		unbondEv := unbondField(unbondFields[i].topic, unbondFields[i].body, sharedTx)
		unbondEv.Ledger = bondEv.Ledger
		unbondEv.OperationIndex = bondEv.OperationIndex
		if v := emit(unbondEv); v != nil {
			unbondOut = v
		}
	}
	if bondOut == nil || unbondOut == nil {
		t.Fatalf("both should complete: bond=%v unbond=%v", bondOut != nil, unbondOut != nil)
	}
	if bondOut.(StakeEvent).Change.Action != EventActionBond {
		t.Errorf("bondOut.Action = %q", bondOut.(StakeEvent).Change.Action)
	}
	if unbondOut.(StakeEvent).Change.Action != EventActionUnbond {
		t.Errorf("unbondOut.Action = %q", unbondOut.(StakeEvent).Change.Action)
	}
	if bondOut.(StakeEvent).Change.Amount.BigInt().Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("bond amount = %s, want 1000", bondOut.(StakeEvent).Change.Amount)
	}
	if unbondOut.(StakeEvent).Change.Amount.BigInt().Cmp(big.NewInt(500)) != 0 {
		t.Errorf("unbond amount = %s, want 500", unbondOut.(StakeEvent).Change.Amount)
	}
}

// ─── Decoder.Matches ────────────────────────────────────────────

func TestDecoder_Matches_allFiveActions(t *testing.T) {
	d := NewDecoder()
	cases := []struct {
		name  string
		topic []string
	}{
		{"swap", []string{TopicSymbolSwap, TopicSymbolSender}},
		{"provide_liquidity", []string{TopicSymbolProvideLiquidity, TopicSymbolPLSender}},
		{"withdraw_liquidity", []string{TopicSymbolWithdrawLiquidity, TopicSymbolWLSender}},
		{"bond", []string{TopicSymbolBond, TopicSymbolStakeUser}},
		{"unbond", []string{TopicSymbolUnbond, TopicSymbolStakeUser}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !d.Matches(events.Event{Topic: tc.topic}) {
				t.Errorf("Matches((%s, …)) = false", tc.name)
			}
		})
	}
	if d.Matches(events.Event{Topic: []string{"unrelated_action", TopicSymbolSender}}) {
		t.Error("Matches(unrelated topic[0]) = true")
	}
}

// ─── consumer.Event impls ───────────────────────────────────────

func TestLiquidityEvent_implementsConsumerEvent(t *testing.T) {
	le := LiquidityEvent{}
	if le.EventKind() != "phoenix.liquidity" {
		t.Errorf("EventKind() = %q", le.EventKind())
	}
	if le.Source() != SourceName {
		t.Errorf("Source() = %q", le.Source())
	}
	var _ consumer.Event = le
}

func TestStakeEvent_implementsConsumerEvent(t *testing.T) {
	se := StakeEvent{}
	if se.EventKind() != "phoenix.stake" {
		t.Errorf("EventKind() = %q", se.EventKind())
	}
	if se.Source() != SourceName {
		t.Errorf("Source() = %q", se.Source())
	}
	var _ consumer.Event = se
}

// ─── Incompleteness / orphan guards ─────────────────────────────

func TestDecodeProvideLiquidity_incomplete(t *testing.T) {
	r := &RawProvideLiquidity{Sender: &events.Event{}}
	if _, err := decodeProvideLiquidity(r); err == nil {
		t.Fatal("expected ErrIncompleteLiquidity")
	}
}

func TestDecodeWithdrawLiquidity_incomplete(t *testing.T) {
	r := &RawWithdrawLiquidity{Sender: &events.Event{}}
	if _, err := decodeWithdrawLiquidity(r); err == nil {
		t.Fatal("expected ErrIncompleteLiquidity")
	}
}

func TestDecodeStake_incomplete(t *testing.T) {
	r := &RawStake{User: &events.Event{}}
	if _, err := decodeStake(r); err == nil {
		t.Fatal("expected ErrIncompleteStake")
	}
}

// TestBuffer_ProvideLiquidity_backfillOldEventsComplete proves the
// 5-event reassembly survives a 6-hour-old ClosedAt — the same
// regression guard the swap path has. Without using the event's own
// ClosedAt for eviction reference, replaying ancient events would
// evict the first-absorbed field when the 5th arrived.
func TestBuffer_ProvideLiquidity_backfillOldEventsComplete(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	old := time.Now().UTC().Add(-6 * time.Hour)
	fields := []struct{ topic, body string }{
		{TopicSymbolPLSender, "addr:" + plSender},
		{TopicSymbolPLTokenA, "addr:" + plTokenA},
		{TopicSymbolPLTokenAAmt, "i128:1000"},
		{TopicSymbolPLTokenB, "addr:" + plTokenB},
		{TopicSymbolPLTokenBAmt, "i128:2000"},
	}
	var out []consumer.Event
	for i, f := range fields {
		ev := plField(f.topic, f.body, plTxHash)
		ev.LedgerClosedAt = old.Format(time.RFC3339)
		emitted, err := d.Decode(ev)
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		if len(emitted) > 0 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatal("backfilled 5-event provide failed to complete")
	}
}

// TestDecoder_Liquidity_PopulatesEventIndex pins F-1324: the completed
// provide_liquidity / withdraw_liquidity reassembly must carry the
// FIRST field-event's in-op EventIndex onto the LiquidityChange so two
// same-(op,action) liquidity actions don't collapse on the
// phoenix_liquidity PK (migration 0060) via ON CONFLICT.
func TestDecoder_Liquidity_PopulatesEventIndex(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolPLSender, "addr:" + plSender},
		{TopicSymbolPLTokenA, "addr:" + plTokenA},
		{TopicSymbolPLTokenAAmt, "i128:1000000000"},
		{TopicSymbolPLTokenB, "addr:" + plTokenB},
		{TopicSymbolPLTokenBAmt, "i128:50000000"},
	}
	var out []consumer.Event
	for i, f := range fields {
		ev := plField(f.topic, f.body, plTxHash)
		// The buffer stamps EventIndex from the FIRST arriving field —
		// give the rest distinct indices to prove only the first wins.
		ev.EventIndex = 11 + i
		emitted, err := d.Decode(ev)
		if err != nil {
			t.Fatalf("field %d (%s): %v", i, f.topic, err)
		}
		if i == 4 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	le := out[0].(LiquidityEvent)
	if le.Change.EventIndex != 11 {
		t.Errorf("EventIndex = %d, want 11 (first field-event's index, F-1324)", le.Change.EventIndex)
	}
}

// TestDecoder_Stake_PopulatesEventIndex pins F-1324 for the stake path
// (phoenix_stake_events PK, migration 0060): the bond / unbond
// reassembly carries the first field-event's in-op EventIndex.
func TestDecoder_Stake_PopulatesEventIndex(t *testing.T) {
	restore := installAddressI128Fakes(t)
	defer restore()
	d := NewDecoder()

	fields := []struct{ topic, body string }{
		{TopicSymbolStakeUser, "addr:" + plSender},
		{TopicSymbolStakeToken, "addr:" + plTokenA},
		{TopicSymbolStakeAmount, "i128:7000000"},
	}
	var out []consumer.Event
	for i, f := range fields {
		ev := bondField(f.topic, f.body, bondTx)
		ev.EventIndex = 20 + i
		emitted, err := d.Decode(ev)
		if err != nil {
			t.Fatalf("field %d (%s): %v", i, f.topic, err)
		}
		if i == 2 {
			out = emitted
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	se := out[0].(StakeEvent)
	if se.Change.EventIndex != 20 {
		t.Errorf("EventIndex = %d, want 20 (first field-event's index, F-1324)", se.Change.EventIndex)
	}
}
