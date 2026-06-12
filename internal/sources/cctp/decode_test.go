package cctp

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── SDK-encode helpers ───────────────────────────────────────────

func symbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func i128(n *big.Int) xdr.ScVal {
	hi, lo := splitBigInt128(n)
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func u32(v uint32) xdr.ScVal {
	x := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &x}
}

func scBytes(b []byte) xdr.ScVal {
	bb := xdr.ScBytes(b)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &bb}
}

func scMap(entries ...xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	pm := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
}

func makeContractStrkey(t *testing.T, seedByte byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seedByte
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func makeAccountStrkey(t *testing.T, seedByte byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seedByte
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func contractAddrFromStrkey(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	var cid xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, strk)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", strk, err)
	}
	copy(cid[:], raw)
	scAddr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
}

func accountAddrFromStrkey(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, strk)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", strk, err)
	}
	var ed xdr.Uint256
	copy(ed[:], raw)
	scAccount := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &ed,
	}
	scAddr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &scAccount,
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
}

func b64(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func splitBigInt128(n *big.Int) (hi int64, lo uint64) {
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		return hiBig.Int64(), loBig.Uint64()
	}
	twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Add(twoTo128, n)
	loBig := new(big.Int).And(u, mask64)
	hiBig := new(big.Int).Rsh(u, 64)
	return int64(hiBig.Uint64()), loBig.Uint64()
}

// makeBytesN32 builds a deterministic 32-byte buffer for fixture use.
func makeBytesN32(seed byte) []byte {
	out := make([]byte, 32)
	out[0] = seed
	out[31] = seed ^ 0xff
	return out
}

// ─── Classify ────────────────────────────────────────────────────

func TestClassify_AllFourEventTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		topic    string
		expected string
	}{
		{"deposit_for_burn", TopicSymbolDepositForBurn, EventDepositForBurn},
		{"mint_and_withdraw", TopicSymbolMintAndWithdraw, EventMintAndWithdraw},
		{"message_sent", TopicSymbolMessageSent, EventMessageSent},
		{"message_received", TopicSymbolMessageReceived, EventMessageReceived},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := &events.Event{Topic: []string{c.topic}}
			if got := Classify(e); got != c.expected {
				t.Errorf("Classify(%s) = %q, want %q", c.name, got, c.expected)
			}
		})
	}
}

func TestClassify_UnknownTopic(t *testing.T) {
	t.Parallel()
	other := b64(t, symbol("transfer"))
	e := &events.Event{Topic: []string{other}}
	if got := Classify(e); got != "" {
		t.Errorf("Classify on unknown topic = %q, want empty", got)
	}
}

func TestClassify_EmptyTopic(t *testing.T) {
	t.Parallel()
	e := &events.Event{Topic: nil}
	if got := Classify(e); got != "" {
		t.Errorf("Classify on empty topic = %q, want empty", got)
	}
}

// ─── DecodeDepositForBurn ────────────────────────────────────────

func TestDecodeDepositForBurn_HappyPath(t *testing.T) {
	t.Parallel()
	burnToken := makeContractStrkey(t, 0x10) // USDC SAC
	depositor := makeAccountStrkey(t, 0x20)
	mintRecipient := makeBytesN32(0x30)
	destTokenMessenger := makeBytesN32(0x40)
	destCaller := makeBytesN32(0x50) // zero or set; here non-zero
	hookData := []byte("hook-payload")

	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(12_345_678))},
		xdr.ScMapEntry{Key: symbol("destination_caller"), Val: scBytes(destCaller)},
		xdr.ScMapEntry{Key: symbol("destination_domain"), Val: u32(0)}, // Ethereum
		xdr.ScMapEntry{Key: symbol("destination_token_messenger"), Val: scBytes(destTokenMessenger)},
		xdr.ScMapEntry{Key: symbol("hook_data"), Val: scBytes(hookData)},
		xdr.ScMapEntry{Key: symbol("max_fee"), Val: i128(big.NewInt(500))},
		xdr.ScMapEntry{Key: symbol("mint_recipient"), Val: scBytes(mintRecipient)},
	))
	e := &events.Event{
		Type:           "contract",
		Ledger:         62_700_000,
		LedgerClosedAt: "2026-05-20T14:00:00Z",
		ContractID:     MainnetTokenMessengerMinter,
		OperationIndex: 0,
		TxHash:         "abc123",
		Topic: []string{
			TopicSymbolDepositForBurn,
			b64(t, contractAddrFromStrkey(t, burnToken)),
			b64(t, accountAddrFromStrkey(t, depositor)),
			b64(t, u32(2000)), // min_finality_threshold (typical value)
		},
		Value: body,
	}
	got, err := DecodeDepositForBurn(e)
	if err != nil {
		t.Fatalf("DecodeDepositForBurn: %v", err)
	}
	if got.BurnToken != burnToken {
		t.Errorf("BurnToken = %q, want %q", got.BurnToken, burnToken)
	}
	if got.Depositor != depositor {
		t.Errorf("Depositor = %q, want %q", got.Depositor, depositor)
	}
	if got.MinFinalityThreshold != 2000 {
		t.Errorf("MinFinalityThreshold = %d, want 2000", got.MinFinalityThreshold)
	}
	if got.Amount != "12345678" {
		t.Errorf("Amount = %q", got.Amount)
	}
	if got.MintRecipient != hex.EncodeToString(mintRecipient) {
		t.Errorf("MintRecipient hex roundtrip mismatch: got %q", got.MintRecipient)
	}
	if got.DestinationDomain != 0 {
		t.Errorf("DestinationDomain = %d, want 0", got.DestinationDomain)
	}
	if got.DestinationTokenMessenger != hex.EncodeToString(destTokenMessenger) {
		t.Errorf("DestinationTokenMessenger mismatch")
	}
	if got.MaxFee != "500" {
		t.Errorf("MaxFee = %q", got.MaxFee)
	}
	if got.HookData != hex.EncodeToString(hookData) {
		t.Errorf("HookData mismatch")
	}
}

// TestDecodeDepositForBurn_LargeI128 — ADR-0003 boundary on the
// amount field. CCTP transfers can be large institutional volumes;
// truncation would silently corrupt USDC supply attribution.
func TestDecodeDepositForBurn_LargeI128(t *testing.T) {
	t.Parallel()
	big1 := new(big.Int)
	big1.SetString("999999999999999999999999999999", 10) // >> 2^53
	burnToken := makeContractStrkey(t, 0x10)
	depositor := makeAccountStrkey(t, 0x20)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big1)},
		xdr.ScMapEntry{Key: symbol("destination_caller"), Val: scBytes(makeBytesN32(0))},
		xdr.ScMapEntry{Key: symbol("destination_domain"), Val: u32(1)},
		xdr.ScMapEntry{Key: symbol("destination_token_messenger"), Val: scBytes(makeBytesN32(0x40))},
		xdr.ScMapEntry{Key: symbol("hook_data"), Val: scBytes(nil)},
		xdr.ScMapEntry{Key: symbol("max_fee"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("mint_recipient"), Val: scBytes(makeBytesN32(0x30))},
	))
	e := &events.Event{
		Topic: []string{
			TopicSymbolDepositForBurn,
			b64(t, contractAddrFromStrkey(t, burnToken)),
			b64(t, accountAddrFromStrkey(t, depositor)),
			b64(t, u32(2000)),
		},
		Value: body,
	}
	got, err := DecodeDepositForBurn(e)
	if err != nil {
		t.Fatalf("DecodeDepositForBurn: %v", err)
	}
	if got.Amount != big1.String() {
		t.Errorf("Amount round-trip lost precision: got %q, want %q", got.Amount, big1.String())
	}
}

func TestDecodeDepositForBurn_ShortTopic(t *testing.T) {
	t.Parallel()
	e := &events.Event{Topic: []string{TopicSymbolDepositForBurn}} // only 1 topic
	_, err := DecodeDepositForBurn(e)
	if err == nil {
		t.Fatal("expected ErrMalformedTopic")
	}
	if !errors.Is(err, ErrMalformedTopic) {
		t.Errorf("want ErrMalformedTopic, got %v", err)
	}
}

func TestDecodeDepositForBurn_MissingBodyField(t *testing.T) {
	t.Parallel()
	burnToken := makeContractStrkey(t, 0x10)
	depositor := makeAccountStrkey(t, 0x20)
	// Body missing 'amount' — schema drift.
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("destination_caller"), Val: scBytes(makeBytesN32(0))},
		xdr.ScMapEntry{Key: symbol("destination_domain"), Val: u32(0)},
		xdr.ScMapEntry{Key: symbol("destination_token_messenger"), Val: scBytes(makeBytesN32(0x40))},
		xdr.ScMapEntry{Key: symbol("hook_data"), Val: scBytes(nil)},
		xdr.ScMapEntry{Key: symbol("max_fee"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("mint_recipient"), Val: scBytes(makeBytesN32(0x30))},
	))
	e := &events.Event{
		Topic: []string{
			TopicSymbolDepositForBurn,
			b64(t, contractAddrFromStrkey(t, burnToken)),
			b64(t, accountAddrFromStrkey(t, depositor)),
			b64(t, u32(2000)),
		},
		Value: body,
	}
	_, err := DecodeDepositForBurn(e)
	if err == nil {
		t.Fatal("expected ErrMalformedBody")
	}
	if !errors.Is(err, ErrMalformedBody) {
		t.Errorf("want ErrMalformedBody, got %v", err)
	}
	if !strings.Contains(err.Error(), "amount") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

// ─── DecodeMintAndWithdraw ───────────────────────────────────────

func TestDecodeMintAndWithdraw_HappyPath(t *testing.T) {
	t.Parallel()
	mintRecipient := makeAccountStrkey(t, 0x60)
	mintToken := makeContractStrkey(t, 0x70) // USDC SAC
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(1_000_000_000))},
		xdr.ScMapEntry{Key: symbol("fee_collected"), Val: i128(big.NewInt(50))},
	))
	e := &events.Event{
		Ledger:         62_700_005,
		LedgerClosedAt: "2026-05-20T14:00:25Z",
		ContractID:     MainnetTokenMessengerMinter,
		Topic: []string{
			TopicSymbolMintAndWithdraw,
			b64(t, accountAddrFromStrkey(t, mintRecipient)),
			b64(t, contractAddrFromStrkey(t, mintToken)),
		},
		Value: body,
	}
	got, err := DecodeMintAndWithdraw(e)
	if err != nil {
		t.Fatalf("DecodeMintAndWithdraw: %v", err)
	}
	if got.MintRecipient != mintRecipient {
		t.Errorf("MintRecipient = %q, want %q", got.MintRecipient, mintRecipient)
	}
	if got.MintToken != mintToken {
		t.Errorf("MintToken = %q, want %q", got.MintToken, mintToken)
	}
	if got.Amount != "1000000000" {
		t.Errorf("Amount = %q", got.Amount)
	}
	if got.FeeCollected != "50" {
		t.Errorf("FeeCollected = %q", got.FeeCollected)
	}
}

func TestDecodeMintAndWithdraw_ShortTopic(t *testing.T) {
	t.Parallel()
	e := &events.Event{Topic: []string{TopicSymbolMintAndWithdraw}}
	_, err := DecodeMintAndWithdraw(e)
	if err == nil {
		t.Fatal("expected ErrMalformedTopic")
	}
	if !errors.Is(err, ErrMalformedTopic) {
		t.Errorf("want ErrMalformedTopic, got %v", err)
	}
}

// ─── DecodeMessageSent ───────────────────────────────────────────

func TestDecodeMessageSent_MapBody(t *testing.T) {
	t.Parallel()
	// The contract publishes MessageSent { message: Bytes } via
	// #[contractevent]. Soroban's macro lays single-field structs
	// out as ScMap with the field name.
	msg := []byte("serialised-envelope-bytes")
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("message"), Val: scBytes(msg)},
	))
	e := &events.Event{
		Topic: []string{TopicSymbolMessageSent},
		Value: body,
	}
	got, err := DecodeMessageSent(e)
	if err != nil {
		t.Fatalf("DecodeMessageSent: %v", err)
	}
	if got.Message != hex.EncodeToString(msg) {
		t.Errorf("Message hex roundtrip mismatch: got %q", got.Message)
	}
}

func TestDecodeMessageSent_RawBytesFallback(t *testing.T) {
	t.Parallel()
	// Forward-compat: if the Soroban macro ever changes to publish
	// the single-field struct as raw Bytes, the decoder still works.
	msg := []byte("raw-envelope")
	body := b64(t, scBytes(msg))
	e := &events.Event{
		Topic: []string{TopicSymbolMessageSent},
		Value: body,
	}
	got, err := DecodeMessageSent(e)
	if err != nil {
		t.Fatalf("DecodeMessageSent raw-bytes path: %v", err)
	}
	if got.Message != hex.EncodeToString(msg) {
		t.Errorf("raw-bytes Message hex roundtrip mismatch: got %q", got.Message)
	}
}

// ─── DecodeMessageReceived ───────────────────────────────────────

func TestDecodeMessageReceived_HappyPath(t *testing.T) {
	t.Parallel()
	caller := makeAccountStrkey(t, 0x80)
	nonce := makeBytesN32(0x90)
	sender := makeBytesN32(0xA0)
	messageBody := []byte("relayed-payload")
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("message_body"), Val: scBytes(messageBody)},
		xdr.ScMapEntry{Key: symbol("sender"), Val: scBytes(sender)},
		xdr.ScMapEntry{Key: symbol("source_domain"), Val: u32(0)}, // Ethereum
	))
	e := &events.Event{
		Topic: []string{
			TopicSymbolMessageReceived,
			b64(t, accountAddrFromStrkey(t, caller)),
			b64(t, scBytes(nonce)),
			b64(t, u32(2000)), // finality_threshold_executed
		},
		Value: body,
	}
	got, err := DecodeMessageReceived(e)
	if err != nil {
		t.Fatalf("DecodeMessageReceived: %v", err)
	}
	if got.Caller != caller {
		t.Errorf("Caller = %q, want %q", got.Caller, caller)
	}
	if got.Nonce != hex.EncodeToString(nonce) {
		t.Errorf("Nonce mismatch: got %q", got.Nonce)
	}
	if got.FinalityThresholdExecuted != 2000 {
		t.Errorf("FinalityThresholdExecuted = %d", got.FinalityThresholdExecuted)
	}
	if got.SourceDomain != 0 {
		t.Errorf("SourceDomain = %d, want 0", got.SourceDomain)
	}
	if got.Sender != hex.EncodeToString(sender) {
		t.Errorf("Sender mismatch")
	}
	if got.MessageBody != hex.EncodeToString(messageBody) {
		t.Errorf("MessageBody mismatch")
	}
}

func TestDecodeMessageReceived_ShortTopic(t *testing.T) {
	t.Parallel()
	e := &events.Event{Topic: []string{TopicSymbolMessageReceived}}
	_, err := DecodeMessageReceived(e)
	if err == nil {
		t.Fatal("expected ErrMalformedTopic")
	}
	if !errors.Is(err, ErrMalformedTopic) {
		t.Errorf("want ErrMalformedTopic, got %v", err)
	}
}

// ─── Topic-symbol encoding stability ────────────────────────────

func TestTopicSymbol_StableEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want xdr.ScVal
	}{
		{"deposit_for_burn", TopicSymbolDepositForBurn, symbol(EventDepositForBurn)},
		{"mint_and_withdraw", TopicSymbolMintAndWithdraw, symbol(EventMintAndWithdraw)},
		{"message_sent", TopicSymbolMessageSent, symbol(EventMessageSent)},
		{"message_received", TopicSymbolMessageReceived, symbol(EventMessageReceived)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := b64(t, c.want)
			if c.got != want {
				t.Errorf("%s drift: pkg = %q, re-encoded = %q", c.name, c.got, want)
			}
		})
	}
}

// ─── ErrUnknownEvent is exposed for downstream consumer use ─────

func TestErrUnknownEvent_Defined(t *testing.T) {
	t.Parallel()
	if ErrUnknownEvent == nil {
		t.Error("ErrUnknownEvent is nil; package-level sentinel must be defined")
	}
	if !strings.Contains(ErrUnknownEvent.Error(), "unknown event") {
		t.Errorf("ErrUnknownEvent message stable check: got %q", ErrUnknownEvent.Error())
	}
}
