package sorobanevents

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── SCVal encode helpers ───────────────────────────────────────
// Mirrored from internal/sources/cctp/decode_test.go's helpers so
// this package's tests are self-contained.

func symbolSV(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func stringSV(s string) xdr.ScVal {
	st := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &st}
}

func u32SV(v uint32) xdr.ScVal {
	x := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &x}
}

func i128SV(n *big.Int) xdr.ScVal {
	// Reuses the cctp test's two-half split semantics; for positive
	// values within int64 range this is straightforward.
	parts := xdr.Int128Parts{
		Hi: xdr.Int64(0),
		Lo: xdr.Uint64(n.Uint64()),
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}
}

func bytesSV(b []byte) xdr.ScVal {
	bb := xdr.ScBytes(b)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &bb}
}

func b64SV(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func mkContractStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func mkTxHashHex(seed byte) string {
	var h [32]byte
	for i := range h {
		h[i] = seed
	}
	return hex.EncodeToString(h[:])
}

// ─── tests ──────────────────────────────────────────────────────

// TestCapture_SymbolTopic verifies the common Symbol-topic[0] case
// — a "swap" event from a fake contract — sets topic_0_sym and
// preserves the XDR bytes.
func TestCapture_SymbolTopic(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0x42)
	txHash := mkTxHashHex(0xAB)

	topicSwap := b64SV(t, symbolSV("swap"))
	topicAddrLike := b64SV(t, u32SV(123))
	body := b64SV(t, i128SV(big.NewInt(1_000_000)))

	ev := events.Event{
		Type:           "contract",
		Ledger:         62_700_000,
		LedgerClosedAt: "2026-05-20T14:00:00Z",
		ContractID:     contractID,
		OperationIndex: 2,
		TxHash:         txHash,
		Topic:          []string{topicSwap, topicAddrLike},
		Value:          body,
	}

	row, err := Capture(ev)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if row.ContractID != contractID {
		t.Errorf("ContractID = %q, want %q", row.ContractID, contractID)
	}
	if len(row.ContractIDHex) != 32 {
		t.Errorf("ContractIDHex len = %d, want 32", len(row.ContractIDHex))
	}
	if row.Topic0Sym != "swap" {
		t.Errorf("Topic0Sym = %q, want %q", row.Topic0Sym, "swap")
	}
	if row.TopicCount != 2 {
		t.Errorf("TopicCount = %d, want 2", row.TopicCount)
	}
	// Topic0XDR must be the base64-decoded raw bytes.
	want0, _ := base64.StdEncoding.DecodeString(topicSwap)
	if !bytes.Equal(row.Topic0XDR, want0) {
		t.Errorf("Topic0XDR mismatch — got %x, want %x", row.Topic0XDR, want0)
	}
	want1, _ := base64.StdEncoding.DecodeString(topicAddrLike)
	if !bytes.Equal(row.Topic1XDR, want1) {
		t.Errorf("Topic1XDR mismatch — got %x, want %x", row.Topic1XDR, want1)
	}
	if row.Topic2XDR != nil {
		t.Errorf("Topic2XDR = %x, want nil", row.Topic2XDR)
	}
	if row.Topic3XDR != nil {
		t.Errorf("Topic3XDR = %x, want nil", row.Topic3XDR)
	}
	if len(row.BodyXDR) == 0 {
		t.Errorf("BodyXDR is empty")
	}
	if row.OpArgsXDR != nil {
		t.Errorf("OpArgsXDR = %x, want nil (no OpArgs on this event)", row.OpArgsXDR)
	}

	// TxHash must round-trip through hex.
	wantHash, _ := hex.DecodeString(txHash)
	if !bytes.Equal(row.TxHash, wantHash) {
		t.Errorf("TxHash mismatch — got %x, want %x", row.TxHash, wantHash)
	}
	if row.OpIndex != 2 {
		t.Errorf("OpIndex = %d, want 2", row.OpIndex)
	}
	if row.Ledger != 62_700_000 {
		t.Errorf("Ledger = %d, want 62700000", row.Ledger)
	}
}

// TestCapture_EventIndexDistinguishesSameOpEvents is the regression
// guard for the ADR-0033 silent-loss bug: before event_index was
// threaded, every contract event in one operation captured with
// event_index=0, so a multi-event op (Phoenix emits 8 per swap)
// collided on the (ledger, tx_hash, op_index, event_index) PK and
// the writer's ON CONFLICT DO NOTHING dropped all but the first.
// With event_index threaded, the eight events of one op produce
// eight distinct PKs.
func TestCapture_EventIndexDistinguishesSameOpEvents(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0x55)
	txHash := mkTxHashHex(0x99)
	body := b64SV(t, i128SV(big.NewInt(1)))

	type pk struct {
		op, ev int16
	}
	seen := make(map[pk]bool)

	// One operation (op_index=0), eight events — the Phoenix shape.
	for evIdx := 0; evIdx < 8; evIdx++ {
		ev := events.Event{
			Type:           "contract",
			Ledger:         62_800_000,
			LedgerClosedAt: "2026-05-21T00:00:00Z",
			ContractID:     contractID,
			OperationIndex: 0,
			EventIndex:     evIdx,
			TxHash:         txHash,
			Topic:          []string{b64SV(t, symbolSV("swap"))},
			Value:          body,
		}
		row, err := Capture(ev)
		if err != nil {
			t.Fatalf("Capture(evIdx=%d): %v", evIdx, err)
		}
		if int(row.EventIndex) != evIdx {
			t.Errorf("evIdx=%d: row.EventIndex = %d, want %d", evIdx, row.EventIndex, evIdx)
		}
		key := pk{op: row.OpIndex, ev: row.EventIndex}
		if seen[key] {
			t.Errorf("evIdx=%d: PK collision on (op=%d, event=%d) — multi-event op would drop rows",
				evIdx, key.op, key.ev)
		}
		seen[key] = true
	}
	if len(seen) != 8 {
		t.Errorf("got %d distinct (op, event) PKs, want 8", len(seen))
	}
}

// TestCapture_StringTopic verifies that a String-typed topic[0] is
// also surfaced through topic_0_sym (the convenience column covers
// both Symbol and String).
func TestCapture_StringTopic(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0x10)
	txHash := mkTxHashHex(0x01)

	topicStr := b64SV(t, stringSV("some-string-topic"))
	body := b64SV(t, u32SV(0))

	ev := events.Event{
		Type:           "contract",
		Ledger:         50_000_000,
		LedgerClosedAt: "2025-12-01T00:00:00Z",
		ContractID:     contractID,
		TxHash:         txHash,
		Topic:          []string{topicStr},
		Value:          body,
	}
	row, err := Capture(ev)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if row.Topic0Sym != "some-string-topic" {
		t.Errorf("Topic0Sym = %q, want %q", row.Topic0Sym, "some-string-topic")
	}
}

// TestCapture_NonSymbolTopic — when topic[0] is neither Symbol nor
// String, topic_0_sym must be "" (sink writes SQL NULL) but the
// raw XDR must still be captured.
func TestCapture_NonSymbolTopic(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0x77)
	txHash := mkTxHashHex(0x77)

	topic0 := b64SV(t, u32SV(0xDEADBEEF)) // U32 — neither Symbol nor String
	body := b64SV(t, bytesSV([]byte("opaque-body")))

	ev := events.Event{
		Type:           "contract",
		Ledger:         60_000_000,
		LedgerClosedAt: "2026-03-01T00:00:00Z",
		ContractID:     contractID,
		TxHash:         txHash,
		Topic:          []string{topic0},
		Value:          body,
	}
	row, err := Capture(ev)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if row.Topic0Sym != "" {
		t.Errorf("Topic0Sym = %q, want \"\" for non-Symbol/non-String topic[0]", row.Topic0Sym)
	}
	// But the raw bytes must still be preserved.
	want, _ := base64.StdEncoding.DecodeString(topic0)
	if !bytes.Equal(row.Topic0XDR, want) {
		t.Errorf("Topic0XDR mismatch — got %x, want %x", row.Topic0XDR, want)
	}
	if len(row.BodyXDR) == 0 {
		t.Errorf("BodyXDR empty for non-Symbol topic case")
	}
}

// TestCapture_OpArgsRoundTrip — when events.Event.OpArgs is
// populated (the dispatcher does this for InvokeContract-emitted
// events), the row's OpArgsXDR must be the XDR-marshalled ScVec of
// those args, and the round-trip must reproduce the input ScVals.
func TestCapture_OpArgsRoundTrip(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0xFE)
	txHash := mkTxHashHex(0xEF)

	topic0 := b64SV(t, symbolSV("write_prices"))
	body := b64SV(t, bytesSV([]byte("body-bytes")))

	// Three args — typical for RedStone's write_prices(updater,
	// feed_ids, payload) shape.
	arg0 := b64SV(t, u32SV(100))
	arg1 := b64SV(t, symbolSV("feed_a"))
	arg2 := b64SV(t, bytesSV([]byte("payload")))

	ev := events.Event{
		Type:           "contract",
		Ledger:         62_500_000,
		LedgerClosedAt: "2026-05-15T00:00:00Z",
		ContractID:     contractID,
		OperationIndex: 0,
		TxHash:         txHash,
		Topic:          []string{topic0},
		Value:          body,
		OpArgs:         []string{arg0, arg1, arg2},
	}
	row, err := Capture(ev)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if row.OpArgsXDR == nil {
		t.Fatalf("OpArgsXDR is nil — expected non-nil for OpArgs-bearing event")
	}

	// Round-trip: unmarshal the OpArgsXDR back to ScVal, verify
	// it's a ScvVec with three elements matching the inputs.
	var sv xdr.ScVal
	if err := sv.UnmarshalBinary(row.OpArgsXDR); err != nil {
		t.Fatalf("UnmarshalBinary(OpArgsXDR): %v", err)
	}
	if sv.Type != xdr.ScValTypeScvVec {
		t.Fatalf("OpArgsXDR ScVal type = %s, want ScvVec", sv.Type)
	}
	vecPtr := sv.Vec
	if vecPtr == nil || *vecPtr == nil {
		t.Fatalf("OpArgsXDR vec is nil")
	}
	vec := **vecPtr
	if len(vec) != 3 {
		t.Fatalf("OpArgsXDR vec len = %d, want 3", len(vec))
	}
	// Element 0 must be a U32 == 100.
	if vec[0].Type != xdr.ScValTypeScvU32 || vec[0].U32 == nil || uint32(*vec[0].U32) != 100 {
		t.Errorf("arg0 round-trip failed; got %+v", vec[0])
	}
	// Element 1 must be a Symbol == "feed_a".
	if vec[1].Type != xdr.ScValTypeScvSymbol || vec[1].Sym == nil || string(*vec[1].Sym) != "feed_a" {
		t.Errorf("arg1 round-trip failed; got %+v", vec[1])
	}
	// Element 2 must be Bytes("payload").
	if vec[2].Type != xdr.ScValTypeScvBytes || vec[2].Bytes == nil || string(*vec[2].Bytes) != "payload" {
		t.Errorf("arg2 round-trip failed; got %+v", vec[2])
	}
}

// TestCapture_NonContractEvent — system / diagnostic events must
// return ErrSkip rather than producing a row. Defence in depth:
// the dispatcher already filters in contractEventToEventsEvent
// (returns nil for non-contract events), so this path should never
// fire on production input.
func TestCapture_NonContractEvent(t *testing.T) {
	t.Parallel()
	_, err := Capture(events.Event{
		Type:       "diagnostic",
		ContractID: mkContractStrkey(t, 0x42),
		Topic:      []string{b64SV(t, symbolSV("debug"))},
		Value:      b64SV(t, u32SV(0)),
	})
	if !errors.Is(err, ErrSkip) {
		t.Errorf("Capture(non-contract) = %v, want ErrSkip", err)
	}
}

// TestCapture_EmptyContractID — defence: an event with an empty
// ContractID must return ErrSkip rather than producing a row with
// junk contract_id_hex.
func TestCapture_EmptyContractID(t *testing.T) {
	t.Parallel()
	_, err := Capture(events.Event{
		Type:           "contract",
		ContractID:     "",
		LedgerClosedAt: "2026-04-01T00:00:00Z",
		Topic:          []string{b64SV(t, symbolSV("x"))},
		Value:          b64SV(t, u32SV(0)),
	})
	if !errors.Is(err, ErrSkip) {
		t.Errorf("Capture(empty ContractID) = %v, want ErrSkip", err)
	}
}

// TestCapture_NoTopics — defence: zero-topic events would violate
// the migration's NOT NULL constraint on topic_0_xdr; must skip.
func TestCapture_NoTopics(t *testing.T) {
	t.Parallel()
	_, err := Capture(events.Event{
		Type:           "contract",
		ContractID:     mkContractStrkey(t, 1),
		LedgerClosedAt: "2026-04-01T00:00:00Z",
		Topic:          nil,
		Value:          b64SV(t, u32SV(0)),
	})
	if !errors.Is(err, ErrSkip) {
		t.Errorf("Capture(no topics) = %v, want ErrSkip", err)
	}
}

// TestCapture_FourTopics — events with exactly four topics must
// populate Topic0XDR..Topic3XDR all.
func TestCapture_FourTopics(t *testing.T) {
	t.Parallel()

	contractID := mkContractStrkey(t, 0xAB)
	txHash := mkTxHashHex(0xCD)

	topics := []string{
		b64SV(t, symbolSV("event")),
		b64SV(t, u32SV(1)),
		b64SV(t, u32SV(2)),
		b64SV(t, u32SV(3)),
	}
	ev := events.Event{
		Type:           "contract",
		Ledger:         60_000_000,
		LedgerClosedAt: "2026-04-01T00:00:00Z",
		ContractID:     contractID,
		TxHash:         txHash,
		Topic:          topics,
		Value:          b64SV(t, u32SV(0)),
	}
	row, err := Capture(ev)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if row.TopicCount != 4 {
		t.Errorf("TopicCount = %d, want 4", row.TopicCount)
	}
	if row.Topic0XDR == nil || row.Topic1XDR == nil || row.Topic2XDR == nil || row.Topic3XDR == nil {
		t.Errorf("expected all four Topic*XDR populated; got 0=%v 1=%v 2=%v 3=%v",
			row.Topic0XDR != nil, row.Topic1XDR != nil, row.Topic2XDR != nil, row.Topic3XDR != nil)
	}
}
