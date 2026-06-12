package comet

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// encodeLiquidityBody builds a Map body for the join_pool / exit_pool /
// deposit variants — three-field body (caller, <tokenField>,
// <amountField>). Used by both per-event decoder tests and the
// dispatcher-adapter routing tests.
func encodeLiquidityBody(t *testing.T, caller, token string, tokenField, amountField string, amount *big.Int) string {
	t.Helper()
	callerSv := addressScValFromStrkey(t, caller)
	tokenSv := addressScValFromStrkey(t, token)
	amountSv := i128ScVal(t, amount)

	keys := []string{"caller", tokenField, amountField}
	vals := []xdr.ScVal{callerSv, tokenSv, amountSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodeWithdrawBody builds the four-field withdraw body
// (caller, token_out, token_amount_out, pool_amount_in).
func encodeWithdrawBody(t *testing.T, caller, tokenOut string, amount, poolAmount *big.Int) string {
	t.Helper()
	callerSv := addressScValFromStrkey(t, caller)
	tokenSv := addressScValFromStrkey(t, tokenOut)
	amountSv := i128ScVal(t, amount)
	poolSv := i128ScVal(t, poolAmount)

	keys := []string{"caller", "token_out", "token_amount_out", "pool_amount_in"}
	vals := []xdr.ScVal{callerSv, tokenSv, amountSv, poolSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ─── classify ───────────────────────────────────────────────────

func TestClassify_RecognisesAllFiveKinds(t *testing.T) {
	cases := []struct {
		name     string
		topic1   string
		wantKind string
	}{
		{"swap", TopicSymbolSwap, EventSwap},
		{"join_pool", TopicSymbolJoinPool, EventJoinPool},
		{"exit_pool", TopicSymbolExitPool, EventExitPool},
		{"deposit", TopicSymbolDeposit, EventDeposit},
		{"withdraw", TopicSymbolWithdraw, EventWithdraw},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &events.Event{Topic: []string{TopicSymbolPool, tc.topic1}}
			if got := classify(ev); got != tc.wantKind {
				t.Errorf("classify = %q, want %q", got, tc.wantKind)
			}
		})
	}
}

func TestClassify_RejectsNonPoolNamespace(t *testing.T) {
	// topic[0] is "swap" rather than POOL — must NOT classify as a
	// Comet event even if topic[1] happens to be a known kind.
	ev := &events.Event{Topic: []string{TopicSymbolSwap, TopicSymbolJoinPool}}
	if got := classify(ev); got != "" {
		t.Errorf("classify of non-POOL namespace = %q, want empty", got)
	}
}

func TestClassify_RejectsUnknownTopicSymbol(t *testing.T) {
	// (POOL, "bind") — a hypothetical contract upgrade that emits
	// `bind` should NOT classify under any of the five known kinds.
	bind := mustEncodeSymbolForTest(t, "bind")
	ev := &events.Event{Topic: []string{TopicSymbolPool, bind}}
	if got := classify(ev); got != "" {
		t.Errorf("classify of (POOL, bind) = %q, want empty (unknown kind)", got)
	}
}

func mustEncodeSymbolForTest(t *testing.T, s string) string {
	t.Helper()
	sym := xdr.ScSymbol(s)
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal symbol: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ─── LiquidityKind helpers ───────────────────────────────────────

func TestLiquidityKind_Direction(t *testing.T) {
	cases := []struct {
		k    LiquidityKind
		want string
	}{
		{LiquidityJoinPool, "add"},
		{LiquidityDeposit, "add"},
		{LiquidityExitPool, "remove"},
		{LiquidityWithdraw, "remove"},
		{LiquidityKind("garbage"), ""},
	}
	for _, tc := range cases {
		if got := tc.k.Direction(); got != tc.want {
			t.Errorf("Direction(%q) = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestLiquidityKind_IsValid(t *testing.T) {
	if !LiquidityJoinPool.IsValid() {
		t.Error("LiquidityJoinPool.IsValid() = false")
	}
	if LiquidityKind("garbage").IsValid() {
		t.Error("garbage kind reported valid")
	}
}

// ─── decodeJoinPool / decodeExitPool / decodeDeposit ─────────────

func TestDecodeJoinPool_HappyPath(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x40)
	token := contractStrkeyFromSeed(t, 0x41)
	amount := big.NewInt(1_500_000_000)

	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", amount)
	ev := &events.Event{
		ContractID:     "CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM",
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          body,
		Ledger:         52_000_100,
		TxHash:         "feed01",
		OperationIndex: 2,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.Kind != LiquidityJoinPool {
		t.Errorf("Kind = %q, want %q", out.Kind, LiquidityJoinPool)
	}
	if out.Caller != caller {
		t.Errorf("Caller = %q, want %q", out.Caller, caller)
	}
	if out.Token != token {
		t.Errorf("Token = %q, want %q", out.Token, token)
	}
	if out.Amount.BigInt().Cmp(amount) != 0 {
		t.Errorf("Amount = %s, want %s", out.Amount, amount)
	}
	if !out.PoolAmountIn.IsZero() {
		t.Errorf("PoolAmountIn = %s, want zero (join_pool carries no BPT burn)", out.PoolAmountIn)
	}
	if out.OpIndex != 2 {
		t.Errorf("OpIndex = %d, want 2", out.OpIndex)
	}
}

func TestDecodeExitPool_HappyPath(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x42)
	token := contractStrkeyFromSeed(t, 0x43)
	amount := big.NewInt(800_000_000)

	body := encodeLiquidityBody(t, caller, token, "token_out", "token_amount_out", amount)
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolExitPool},
		Value:          body,
		Ledger:         52_000_200,
		TxHash:         "feed02",
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.Kind != LiquidityExitPool {
		t.Errorf("Kind = %q, want %q", out.Kind, LiquidityExitPool)
	}
	if out.Token != token {
		t.Errorf("Token = %q, want %q", out.Token, token)
	}
	if out.Amount.BigInt().Cmp(amount) != 0 {
		t.Errorf("Amount = %s, want %s", out.Amount, amount)
	}
}

func TestDecodeDeposit_HappyPath(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x44)
	token := contractStrkeyFromSeed(t, 0x45)
	amount := big.NewInt(500_000)

	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", amount)
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolDeposit},
		Value:          body,
		Ledger:         52_000_300,
		TxHash:         "feed03",
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.Kind != LiquidityDeposit {
		t.Errorf("Kind = %q, want %q", out.Kind, LiquidityDeposit)
	}
	if out.Caller != caller || out.Token != token {
		t.Errorf("Caller/Token mismatch: caller=%q token=%q", out.Caller, out.Token)
	}
	if out.Amount.BigInt().Cmp(amount) != 0 {
		t.Errorf("Amount = %s, want %s", out.Amount, amount)
	}
}

func TestDecodeWithdraw_HappyPathCarriesPoolAmountIn(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x46)
	token := contractStrkeyFromSeed(t, 0x47)
	amount := big.NewInt(700_000)
	poolAmount := big.NewInt(12_345)

	body := encodeWithdrawBody(t, caller, token, amount, poolAmount)
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolWithdraw},
		Value:          body,
		Ledger:         52_000_400,
		TxHash:         "feed04",
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.Kind != LiquidityWithdraw {
		t.Errorf("Kind = %q, want %q", out.Kind, LiquidityWithdraw)
	}
	if out.Amount.BigInt().Cmp(amount) != 0 {
		t.Errorf("Amount = %s, want %s", out.Amount, amount)
	}
	if out.PoolAmountIn.BigInt().Cmp(poolAmount) != 0 {
		t.Errorf("PoolAmountIn = %s, want %s", out.PoolAmountIn, poolAmount)
	}
}

func TestDecodeWithdraw_MissingPoolAmountIn_Malformed(t *testing.T) {
	// Build a withdraw body without the pool_amount_in field — must
	// reject as malformed; downstream NULL-vs-omit would be ambiguous.
	caller := accountStrkeyFromSeed(t, 0x48)
	token := contractStrkeyFromSeed(t, 0x49)
	body := encodeLiquidityBody(t, caller, token, "token_out", "token_amount_out", big.NewInt(1))
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolWithdraw},
		Value:          body,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	_, err := decodeLiquidityEvent(ev, closedAt)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}

func TestDecodeLiquidity_NonPositiveAmount_Rejects(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x4a)
	token := contractStrkeyFromSeed(t, 0x4b)
	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", big.NewInt(0))
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          body,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	_, err := decodeLiquidityEvent(ev, closedAt)
	if !errors.Is(err, ErrNonPositiveAmounts) {
		t.Errorf("expected ErrNonPositiveAmounts, got %v", err)
	}
}

func TestDecodeLiquidity_WrongTopic_Rejects(t *testing.T) {
	// (POOL, swap) is a known kind but not a liquidity event —
	// decodeLiquidityEvent must reject so the adapter routes it
	// through decodeSwap instead.
	ev := &events.Event{Topic: []string{TopicSymbolPool, TopicSymbolSwap}}
	_, err := decodeLiquidityEvent(ev, time.Now())
	if !errors.Is(err, ErrNotCometEvent) {
		t.Errorf("expected ErrNotCometEvent for swap, got %v", err)
	}
}

func TestDecodeLiquidity_MissingCaller_Malformed(t *testing.T) {
	// Build a join_pool body missing the caller field.
	token := contractStrkeyFromSeed(t, 0x4c)
	tokenSv := addressScValFromStrkey(t, token)
	amountSv := i128ScVal(t, big.NewInt(1))

	keys := []string{"token_in", "token_amount_in"}
	vals := []xdr.ScVal{tokenSv, amountSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, _ := body.MarshalBinary()

	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          base64.StdEncoding.EncodeToString(b),
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	_, err := decodeLiquidityEvent(ev, closedAt)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}

// TestDecodeLiquidity_LargeI128 preserves the ADR-0003 invariant
// even on the liquidity-event surface — a multi-token join with a
// large reserve token must round-trip through *big.Int / NUMERIC
// without truncation.
func TestDecodeLiquidity_LargeI128(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x4d)
	token := contractStrkeyFromSeed(t, 0x4e)
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)

	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", huge)
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          body,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.Amount.BigInt().Cmp(huge) != 0 {
		t.Errorf("Amount = %s, want %s — i128 round-trip lost precision", out.Amount, huge)
	}
}

// TestDecodeLiquidity_PopulatesEventIndex pins F-1324: the liquidity
// path must carry events.Event.EventIndex onto the row so two
// same-(kind,token) liquidity events emitted by ONE operation don't
// collapse on the comet_liquidity PK (migration 0059) via ON CONFLICT.
// (The swap path already fans op_index via canonical.FanoutOpIndex;
// the liquidity path keys on event_index directly.)
func TestDecodeLiquidity_PopulatesEventIndex(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x70)
	token := contractStrkeyFromSeed(t, 0x71)
	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", big.NewInt(1))
	ev := &events.Event{
		ContractID:     "CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM",
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          body,
		OperationIndex: 0,
		EventIndex:     6,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeLiquidityEvent(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeLiquidityEvent: %v", err)
	}
	if out.EventIndex != 6 {
		t.Errorf("EventIndex = %d, want 6 (F-1324)", out.EventIndex)
	}
}
