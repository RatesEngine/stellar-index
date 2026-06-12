package blend

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── Local fixture helpers ─────────────────────────────────────
//
// The auction-side test file (decode_test.go) defines
// contractStrkeyFromSeed / accountStrkeyFromSeed / addressScVal /
// i128ScVal / u32ScVal / symbolScVal / vecScVal / encodeScVal —
// we reuse those here. The helpers below cover the money-market
// shapes that don't show up in the auction tests.

// u64ScVal wraps a uint64.
func u64ScVal(n uint64) xdr.ScVal {
	x := xdr.Uint64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &x}
}

// boolScVal wraps a bool.
func boolScVal(b bool) xdr.ScVal {
	v := b
	return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &v}
}

// reserveConfigScVal builds a full ReserveConfig ScMap with
// sorted-by-symbol keys. soroban-sdk emits #[contracttype] structs
// as ScMap; the soroban-sdk sorts the entries by key at encode time
// but the decoder reads by name (resilient to reordering).
//
// Field ordering chosen to match what the contract emits. The
// decoder reads by MapField so the order doesn't matter for
// correctness; we sort here to mirror chain output.
func reserveConfigScVal(t *testing.T) xdr.ScVal {
	t.Helper()
	// Alphabetical-by-symbol-name to mirror soroban-sdk's encode
	// path. The decoder doesn't depend on the order.
	entries := []xdr.ScMapEntry{
		{Key: symbolScVal("c_factor"), Val: u32ScVal(8_500_000)},
		{Key: symbolScVal("decimals"), Val: u32ScVal(7)},
		{Key: symbolScVal("enabled"), Val: boolScVal(true)},
		{Key: symbolScVal("index"), Val: u32ScVal(3)},
		{Key: symbolScVal("l_factor"), Val: u32ScVal(9_000_000)},
		{Key: symbolScVal("max_util"), Val: u32ScVal(9_500_000)},
		{Key: symbolScVal("r_base"), Val: u32ScVal(100_000)},
		{Key: symbolScVal("r_one"), Val: u32ScVal(500_000)},
		{Key: symbolScVal("r_three"), Val: u32ScVal(2_000_000)},
		{Key: symbolScVal("r_two"), Val: u32ScVal(1_000_000)},
		{Key: symbolScVal("reactivity"), Val: u32ScVal(50_000)},
		{Key: symbolScVal("supply_cap"), Val: i128ScVal(t, big.NewInt(1_000_000_000_000))},
		{Key: symbolScVal("util"), Val: u32ScVal(8_000_000)},
	}
	return mapScVal(entries)
}

// ─── classifyAny ───────────────────────────────────────────────

func TestClassifyAny(t *testing.T) {
	// classifyAny() covers every Blend topic the package declares
	// — auctions + 18 money-market / admin events.
	cases := []struct {
		name string
		top0 string
		want string
	}{
		{"new_auction", TopicSymbolNewAuction, EventNewAuction},
		{"supply", TopicSymbolSupply, EventSupply},
		{"withdraw", TopicSymbolWithdraw, EventWithdraw},
		{"supply_collateral", TopicSymbolSupplyCollateral, EventSupplyCollateral},
		{"withdraw_collateral", TopicSymbolWithdrawCollateral, EventWithdrawCollateral},
		{"borrow", TopicSymbolBorrow, EventBorrow},
		{"repay", TopicSymbolRepay, EventRepay},
		{"flash_loan", TopicSymbolFlashLoan, EventFlashLoan},
		{"gulp", TopicSymbolGulp, EventGulp},
		{"claim", TopicSymbolClaim, EventClaim},
		{"reserve_emission_update", TopicSymbolReserveEmissions, EventReserveEmissions},
		{"gulp_emissions", TopicSymbolGulpEmissions, EventGulpEmissions},
		{"bad_debt", TopicSymbolBadDebt, EventBadDebt},
		{"defaulted_debt", TopicSymbolDefaultedDebt, EventDefaultedDebt},
		{"set_admin", TopicSymbolSetAdmin, EventSetAdmin},
		{"update_pool", TopicSymbolUpdatePool, EventUpdatePool},
		{"queue_set_reserve", TopicSymbolQueueSetReserve, EventQueueSetReserve},
		{"cancel_set_reserve", TopicSymbolCancelSetReserve, EventCancelSetReserve},
		{"set_reserve", TopicSymbolSetReserve, EventSetReserve},
		{"set_status", TopicSymbolSetStatus, EventSetStatus},
		{"deploy", TopicSymbolDeploy, EventDeploy},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topics := []string{tc.top0}
			if tc.top0 == "" {
				topics = nil
			}
			got := classifyAny(&events.Event{Topic: topics})
			if got != tc.want {
				t.Errorf("classifyAny=%q want %q", got, tc.want)
			}
		})
	}
}

// ─── Position-event decoder ────────────────────────────────────

func TestDecodePositionEvent_Supply(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x10)
	asset := contractStrkeyFromSeed(t, 0x20)
	user := accountStrkeyFromSeed(t, 0x21)

	body := vecScVal(
		i128ScVal(t, big.NewInt(1_000_000_000)), // tokens_in
		i128ScVal(t, big.NewInt(990_000_000)),   // b_tokens_minted
	)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSupply,
			encodeScVal(t, addressScVal(t, asset)),
			encodeScVal(t, addressScVal(t, user)),
		},
		Value:          encodeScVal(t, body),
		Ledger:         60_000_000,
		TxHash:         "deadbeef",
		OperationIndex: 2,
		LedgerClosedAt: "2026-05-20T10:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodePositionEvent(ev, EventSupply, closedAt)
	if err != nil {
		t.Fatalf("decodePositionEvent: %v", err)
	}
	if out.Pool != pool || out.Asset != asset || out.User != user {
		t.Errorf("identity mismatch: %+v", out)
	}
	if out.Kind != EventSupply {
		t.Errorf("Kind=%q want %q", out.Kind, EventSupply)
	}
	if out.TokenAmount.Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("TokenAmount=%s want 1_000_000_000", out.TokenAmount)
	}
	if out.BOrDAmount.Cmp(big.NewInt(990_000_000)) != 0 {
		t.Errorf("BOrDAmount=%s want 990_000_000", out.BOrDAmount)
	}
	if out.Counterparty != "" {
		t.Errorf("Counterparty=%q want empty for non-flash_loan", out.Counterparty)
	}
	if out.OpIndex != 2 {
		t.Errorf("OpIndex=%d want 2", out.OpIndex)
	}
}

func TestDecodePositionEvent_FlashLoan(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x11)
	asset := contractStrkeyFromSeed(t, 0x22)
	user := accountStrkeyFromSeed(t, 0x23)
	contract := contractStrkeyFromSeed(t, 0x24)

	body := vecScVal(
		i128ScVal(t, big.NewInt(500_000_000)),
		i128ScVal(t, big.NewInt(510_000_000)),
	)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolFlashLoan,
			encodeScVal(t, addressScVal(t, asset)),
			encodeScVal(t, addressScVal(t, user)),
			encodeScVal(t, addressScVal(t, contract)),
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T10:01:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodePositionEvent(ev, EventFlashLoan, closedAt)
	if err != nil {
		t.Fatalf("decodePositionEvent flash_loan: %v", err)
	}
	if out.Counterparty != contract {
		t.Errorf("Counterparty=%q want %q", out.Counterparty, contract)
	}
}

func TestDecodePositionEvent_TopicArityMismatch(t *testing.T) {
	// supply with 4 topics — should be 3.
	pool := contractStrkeyFromSeed(t, 0x12)
	asset := contractStrkeyFromSeed(t, 0x30)
	user := accountStrkeyFromSeed(t, 0x31)
	contract := contractStrkeyFromSeed(t, 0x32)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSupply,
			encodeScVal(t, addressScVal(t, asset)),
			encodeScVal(t, addressScVal(t, user)),
			encodeScVal(t, addressScVal(t, contract)), // extra
		},
		Value:          encodeScVal(t, vecScVal(i128ScVal(t, big.NewInt(1)), i128ScVal(t, big.NewInt(1)))),
		LedgerClosedAt: "2026-05-20T10:00:00Z",
	}
	_, err := decodePositionEvent(ev, EventSupply, time.Now())
	if err == nil {
		t.Fatal("expected ErrMalformedPayload for arity mismatch")
	}
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("err=%v doesn't wrap ErrMalformedPayload", err)
	}
}

// ─── Emission-event decoders ───────────────────────────────────

func TestDecodeGulp(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x40)
	asset := contractStrkeyFromSeed(t, 0x41)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolGulp,
			encodeScVal(t, addressScVal(t, asset)),
		},
		Value:          encodeScVal(t, i128ScVal(t, big.NewInt(12345))),
		LedgerClosedAt: "2026-05-20T11:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeGulp(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeGulp: %v", err)
	}
	if out.Asset != asset || out.Amount.Cmp(big.NewInt(12345)) != 0 {
		t.Errorf("gulp mismatch: %+v", out)
	}
}

func TestDecodeClaim(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x42)
	from := accountStrkeyFromSeed(t, 0x43)

	idsVec := vecScVal(u32ScVal(0), u32ScVal(2), u32ScVal(5))
	body := vecScVal(idsVec, i128ScVal(t, big.NewInt(7_500_000)))
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolClaim,
			encodeScVal(t, addressScVal(t, from)),
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T11:01:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeClaim(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeClaim: %v", err)
	}
	if out.User != from {
		t.Errorf("User=%q want %q", out.User, from)
	}
	if out.Amount.Cmp(big.NewInt(7_500_000)) != 0 {
		t.Errorf("Amount=%s want 7_500_000", out.Amount)
	}
	if len(out.ReserveTokenIDs) != 3 || out.ReserveTokenIDs[0] != 0 || out.ReserveTokenIDs[2] != 5 {
		t.Errorf("ReserveTokenIDs=%v want [0 2 5]", out.ReserveTokenIDs)
	}
}

func TestDecodeReserveEmissionUpdate(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x44)
	body := vecScVal(
		u32ScVal(7),
		u64ScVal(1_000_000),
		u64ScVal(1_900_000_000),
	)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolReserveEmissions,
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T11:02:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeReserveEmissionUpdate(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeReserveEmissionUpdate: %v", err)
	}
	if out.ResTokenID != 7 || out.EmissionsPerSec != 1_000_000 || out.Expiration != 1_900_000_000 {
		t.Errorf("reserve_emission_update mismatch: %+v", out)
	}
}

func TestDecodeGulpEmissions(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x45)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolGulpEmissions,
		},
		Value:          encodeScVal(t, i128ScVal(t, big.NewInt(42))),
		LedgerClosedAt: "2026-05-20T11:03:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeGulpEmissions(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeGulpEmissions: %v", err)
	}
	if out.Amount.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("Amount=%s want 42", out.Amount)
	}
}

func TestDecodeBadDebt(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x46)
	user := accountStrkeyFromSeed(t, 0x47)
	asset := contractStrkeyFromSeed(t, 0x48)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolBadDebt,
			encodeScVal(t, addressScVal(t, user)),
			encodeScVal(t, addressScVal(t, asset)),
		},
		Value:          encodeScVal(t, i128ScVal(t, big.NewInt(123_456_789))),
		LedgerClosedAt: "2026-05-20T11:04:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeBadDebt(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeBadDebt: %v", err)
	}
	if out.User != user || out.Asset != asset {
		t.Errorf("bad_debt identity mismatch: %+v", out)
	}
	if out.Amount.Cmp(big.NewInt(123_456_789)) != 0 {
		t.Errorf("Amount=%s want 123_456_789", out.Amount)
	}
}

func TestDecodeDefaultedDebt(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x49)
	asset := contractStrkeyFromSeed(t, 0x4a)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolDefaultedDebt,
			encodeScVal(t, addressScVal(t, asset)),
		},
		Value:          encodeScVal(t, i128ScVal(t, big.NewInt(99))),
		LedgerClosedAt: "2026-05-20T11:05:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeDefaultedDebt(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeDefaultedDebt: %v", err)
	}
	if out.Asset != asset || out.Amount.Cmp(big.NewInt(99)) != 0 {
		t.Errorf("defaulted_debt mismatch: %+v", out)
	}
}

// ─── Admin-event decoders ──────────────────────────────────────

func TestDecodeSetAdmin(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x50)
	admin := accountStrkeyFromSeed(t, 0x51)
	newAdmin := accountStrkeyFromSeed(t, 0x52)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSetAdmin,
			encodeScVal(t, addressScVal(t, admin)),
		},
		Value:          encodeScVal(t, addressScVal(t, newAdmin)),
		LedgerClosedAt: "2026-05-20T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeSetAdmin(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeSetAdmin: %v", err)
	}
	if out.Admin != admin || out.Target != newAdmin {
		t.Errorf("set_admin mismatch: admin=%q target=%q want admin=%q target=%q",
			out.Admin, out.Target, admin, newAdmin)
	}
}

func TestDecodeUpdatePool(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x53)
	admin := accountStrkeyFromSeed(t, 0x54)
	body := vecScVal(
		u32ScVal(2_000_000),                   // backstop_take_rate
		u32ScVal(8),                           // max_positions
		i128ScVal(t, big.NewInt(100_000_000)), // min_collateral
	)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolUpdatePool,
			encodeScVal(t, addressScVal(t, admin)),
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T12:01:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeUpdatePool(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeUpdatePool: %v", err)
	}
	if out.Admin != admin {
		t.Errorf("Admin=%q want %q", out.Admin, admin)
	}
	if out.BackstopTakeRate != 2_000_000 || out.MaxPositions != 8 {
		t.Errorf("update_pool mismatch: %+v", out)
	}
	if out.MinCollateral.Cmp(big.NewInt(100_000_000)) != 0 {
		t.Errorf("MinCollateral=%s want 100_000_000", out.MinCollateral)
	}
}

func TestDecodeQueueSetReserve(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x55)
	admin := accountStrkeyFromSeed(t, 0x56)
	asset := contractStrkeyFromSeed(t, 0x57)
	body := vecScVal(
		addressScVal(t, asset),
		reserveConfigScVal(t),
	)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolQueueSetReserve,
			encodeScVal(t, addressScVal(t, admin)),
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T12:02:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeQueueSetReserve(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeQueueSetReserve: %v", err)
	}
	if out.Admin != admin || out.Asset != asset {
		t.Errorf("queue_set_reserve mismatch: %+v", out)
	}
	if out.ReserveConfig == nil {
		t.Fatalf("ReserveConfig nil")
	}
	if got := out.ReserveConfig["index"]; got != uint64(3) {
		t.Errorf("ReserveConfig.index=%v want uint64(3)", got)
	}
	if got := out.ReserveConfig["enabled"]; got != true {
		t.Errorf("ReserveConfig.enabled=%v want true", got)
	}
	if got := out.ReserveConfig["supply_cap"]; got != "1000000000000" {
		t.Errorf("ReserveConfig.supply_cap=%v want \"1000000000000\"", got)
	}
}

func TestDecodeCancelSetReserve(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x58)
	admin := accountStrkeyFromSeed(t, 0x59)
	asset := contractStrkeyFromSeed(t, 0x5a)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolCancelSetReserve,
			encodeScVal(t, addressScVal(t, admin)),
		},
		Value:          encodeScVal(t, addressScVal(t, asset)),
		LedgerClosedAt: "2026-05-20T12:03:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeCancelSetReserve(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeCancelSetReserve: %v", err)
	}
	if out.Admin != admin || out.Asset != asset {
		t.Errorf("cancel_set_reserve mismatch: %+v", out)
	}
}

func TestDecodeSetReserve(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x5b)
	asset := contractStrkeyFromSeed(t, 0x5c)
	body := vecScVal(addressScVal(t, asset), u32ScVal(4))
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSetReserve,
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-05-20T12:04:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeSetReserve(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeSetReserve: %v", err)
	}
	if out.Asset != asset || out.ReserveIndex != 4 {
		t.Errorf("set_reserve mismatch: %+v", out)
	}
}

func TestDecodeSetStatus_NonAdmin(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x5d)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSetStatus,
		},
		Value:          encodeScVal(t, u32ScVal(2)),
		LedgerClosedAt: "2026-05-20T12:05:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeSetStatus(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeSetStatus non-admin: %v", err)
	}
	if out.NewStatus != 2 || out.ByAdmin || out.Admin != "" {
		t.Errorf("set_status non-admin mismatch: %+v", out)
	}
}

func TestDecodeSetStatus_Admin(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x5e)
	admin := accountStrkeyFromSeed(t, 0x5f)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolSetStatus,
			encodeScVal(t, addressScVal(t, admin)),
		},
		Value:          encodeScVal(t, u32ScVal(5)),
		LedgerClosedAt: "2026-05-20T12:06:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeSetStatus(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeSetStatus admin: %v", err)
	}
	if !out.ByAdmin || out.Admin != admin || out.NewStatus != 5 {
		t.Errorf("set_status admin mismatch: %+v", out)
	}
}

func TestDecodeDeploy(t *testing.T) {
	factory := contractStrkeyFromSeed(t, 0x60)
	poolAddr := contractStrkeyFromSeed(t, 0x61)
	ev := &events.Event{
		ContractID: factory,
		Topic: []string{
			TopicSymbolDeploy,
		},
		Value:          encodeScVal(t, addressScVal(t, poolAddr)),
		LedgerClosedAt: "2026-05-20T13:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeDeploy(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeDeploy: %v", err)
	}
	if out.ContractID != factory || out.Target != poolAddr {
		t.Errorf("deploy mismatch: %+v", out)
	}
}

// ─── Decoder.Decode end-to-end (one per event type) ────────────

func TestDecoderDecode_AllEventKinds(t *testing.T) {
	// Quick sanity that the dispatcher_adapter.Decode dispatch
	// table routes each topic to the right decoder and emits the
	// right consumer.Event implementor. Body shapes are minimal
	// happy-path shapes — the per-decoder tests above cover field-
	// level correctness.
	d := NewDecoder()
	pool := contractStrkeyFromSeed(t, 0x70)
	asset := contractStrkeyFromSeed(t, 0x71)
	user := accountStrkeyFromSeed(t, 0x72)
	admin := accountStrkeyFromSeed(t, 0x73)
	contract := contractStrkeyFromSeed(t, 0x74)
	now := time.Now().UTC().Format(time.RFC3339)

	mkEvent := func(topic0 string, topics []xdr.ScVal, body xdr.ScVal) events.Event {
		encodedTopics := make([]string, 0, len(topics)+1)
		encodedTopics = append(encodedTopics, topic0)
		for _, sv := range topics {
			encodedTopics = append(encodedTopics, encodeScVal(t, sv))
		}
		return events.Event{
			ContractID:     pool,
			Topic:          encodedTopics,
			Value:          encodeScVal(t, body),
			LedgerClosedAt: now,
		}
	}

	positionTopics := []xdr.ScVal{addressScVal(t, asset), addressScVal(t, user)}
	positionBody := vecScVal(i128ScVal(t, big.NewInt(1)), i128ScVal(t, big.NewInt(1)))

	cases := []struct {
		name   string
		ev     events.Event
		wantOK bool
	}{
		{"supply", mkEvent(TopicSymbolSupply, positionTopics, positionBody), true},
		{"withdraw", mkEvent(TopicSymbolWithdraw, positionTopics, positionBody), true},
		{"supply_collateral", mkEvent(TopicSymbolSupplyCollateral, positionTopics, positionBody), true},
		{"withdraw_collateral", mkEvent(TopicSymbolWithdrawCollateral, positionTopics, positionBody), true},
		{"borrow", mkEvent(TopicSymbolBorrow, positionTopics, positionBody), true},
		{"repay", mkEvent(TopicSymbolRepay, positionTopics, positionBody), true},
		{"flash_loan", mkEvent(TopicSymbolFlashLoan,
			[]xdr.ScVal{addressScVal(t, asset), addressScVal(t, user), addressScVal(t, contract)},
			positionBody), true},

		{"gulp", mkEvent(TopicSymbolGulp,
			[]xdr.ScVal{addressScVal(t, asset)},
			i128ScVal(t, big.NewInt(1))), true},
		{"claim", mkEvent(TopicSymbolClaim,
			[]xdr.ScVal{addressScVal(t, user)},
			vecScVal(vecScVal(), i128ScVal(t, big.NewInt(1)))), true},
		{"reserve_emission_update", mkEvent(TopicSymbolReserveEmissions,
			nil,
			vecScVal(u32ScVal(0), u64ScVal(0), u64ScVal(0))), true},
		{"gulp_emissions", mkEvent(TopicSymbolGulpEmissions,
			nil,
			i128ScVal(t, big.NewInt(0))), true},
		{"bad_debt", mkEvent(TopicSymbolBadDebt,
			[]xdr.ScVal{addressScVal(t, user), addressScVal(t, asset)},
			i128ScVal(t, big.NewInt(0))), true},
		{"defaulted_debt", mkEvent(TopicSymbolDefaultedDebt,
			[]xdr.ScVal{addressScVal(t, asset)},
			i128ScVal(t, big.NewInt(0))), true},

		{"set_admin", mkEvent(TopicSymbolSetAdmin,
			[]xdr.ScVal{addressScVal(t, admin)},
			addressScVal(t, admin)), true},
		{"update_pool", mkEvent(TopicSymbolUpdatePool,
			[]xdr.ScVal{addressScVal(t, admin)},
			vecScVal(u32ScVal(0), u32ScVal(0), i128ScVal(t, big.NewInt(0)))), true},
		{"queue_set_reserve", mkEvent(TopicSymbolQueueSetReserve,
			[]xdr.ScVal{addressScVal(t, admin)},
			vecScVal(addressScVal(t, asset), reserveConfigScVal(t))), true},
		{"cancel_set_reserve", mkEvent(TopicSymbolCancelSetReserve,
			[]xdr.ScVal{addressScVal(t, admin)},
			addressScVal(t, asset)), true},
		{"set_reserve", mkEvent(TopicSymbolSetReserve,
			nil,
			vecScVal(addressScVal(t, asset), u32ScVal(0))), true},
		{"set_status", mkEvent(TopicSymbolSetStatus,
			nil,
			u32ScVal(1)), true},
		{"deploy", mkEvent(TopicSymbolDeploy,
			nil,
			addressScVal(t, contract)), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := d.Decode(tc.ev)
			if tc.wantOK && err != nil {
				t.Fatalf("Decode(%s): err=%v", tc.name, err)
			}
			if tc.wantOK && len(out) != 1 {
				t.Fatalf("Decode(%s): emitted %d events, want 1", tc.name, len(out))
			}
		})
	}
}
