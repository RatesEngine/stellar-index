package defindex

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// TestClassify_depositWithdraw covers the topic-byte equality path —
// ensures topic[0] = ScvString("DeFindexVault") + topic[1] in
// {deposit, withdraw} is the only thing the decoder picks up.
// Verifies the byte-equality constants line up with the SDK encoder.
func TestClassify_depositWithdraw(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		topic     []string
		wantClass string
	}{
		{
			name:      "deposit",
			topic:     []string{TopicPrefixVault, TopicSymbolDeposit},
			wantClass: EventDeposit,
		},
		{
			name:      "withdraw",
			topic:     []string{TopicPrefixVault, TopicSymbolWithdraw},
			wantClass: EventWithdraw,
		},
		{
			name:      "wrong prefix",
			topic:     []string{mustB64String(t, "SoroswapPair"), TopicSymbolDeposit},
			wantClass: "",
		},
		{
			name:      "rescue (not Phase A)",
			topic:     []string{TopicPrefixVault, mustB64Symbol(t, "rescue")},
			wantClass: "",
		},
		{
			name:      "single-element topic",
			topic:     []string{TopicPrefixVault},
			wantClass: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &events.Event{Topic: tc.topic}
			got := classify(ev)
			if got != tc.wantClass {
				t.Errorf("classify = %q, want %q", got, tc.wantClass)
			}
		})
	}
}

// TestDecodeFlow_deposit covers the happy-path decode of a single-
// asset deposit event. Verifies amount preservation (no truncation
// per ADR-0003), depositor address round-trip, and df_token_minted
// is captured separately from the underlying-asset amount.
func TestDecodeFlow_deposit(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_000_000,
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		ContractID:     MainnetVaultUSDC,
		OperationIndex: 2,
		TxHash:         "abc123",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "depositor", addrSCVal(makeAccountAddress(t, 0xAA))),
			mapEntry(t, "amounts", vecSCVal(i128SCVal(big.NewInt(123_456_789_000)))), // 1234.5 USDC at 1e8
			mapEntry(t, "df_tokens_minted", i128SCVal(big.NewInt(1_000_000_000))),    // 10 shares at 1e8
		)),
	}
	flow, err := decodeFlow(ev, EventDeposit)
	if err != nil {
		t.Fatalf("decodeFlow: %v", err)
	}
	if flow.Source != SourceName {
		t.Errorf("Source = %q, want %q", flow.Source, SourceName)
	}
	if flow.Direction != DirectionDeposit {
		t.Errorf("Direction = %q, want deposit", flow.Direction)
	}
	if flow.VaultName != "usdc-autocompound" {
		t.Errorf("VaultName = %q, want usdc-autocompound", flow.VaultName)
	}
	if flow.Counterparty == "" {
		t.Errorf("Counterparty empty")
	}
	if got, want := len(flow.Amounts), 1; got != want {
		t.Fatalf("len(Amounts) = %d, want %d", got, want)
	}
	if got, want := flow.Amounts[0].String(), "123456789000"; got != want {
		t.Errorf("Amounts[0] = %q, want %q (no truncation)", got, want)
	}
	if got, want := flow.DfTokenDelta.String(), "1000000000"; got != want {
		t.Errorf("DfTokenDelta = %q, want %q", got, want)
	}
	if flow.Ledger != 60_000_000 || flow.OpIndex != 2 || flow.TxHash != "abc123" {
		t.Errorf("header fields not preserved: %+v", flow)
	}
}

// TestDecodeFlow_withdraw confirms the withdraw branch picks the
// correct field names (`withdrawer`, `amounts_withdrawn`,
// `df_tokens_burned`). These differ from deposit and a wrong field-
// name lookup would silently zero the body fields.
func TestDecodeFlow_withdraw(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_000_001,
		LedgerClosedAt: "2026-05-14T10:31:00Z",
		ContractID:     MainnetVaultEURC,
		Topic:          []string{TopicPrefixVault, TopicSymbolWithdraw},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "withdrawer", addrSCVal(makeAccountAddress(t, 0xBB))),
			mapEntry(t, "amounts_withdrawn", vecSCVal(i128SCVal(big.NewInt(50_000_000)))),
			mapEntry(t, "df_tokens_burned", i128SCVal(big.NewInt(500_000_000))),
		)),
	}
	flow, err := decodeFlow(ev, EventWithdraw)
	if err != nil {
		t.Fatalf("decodeFlow: %v", err)
	}
	if flow.Direction != DirectionWithdraw {
		t.Errorf("Direction = %q, want withdraw", flow.Direction)
	}
	if flow.VaultName != "eurc-autocompound" {
		t.Errorf("VaultName = %q, want eurc-autocompound", flow.VaultName)
	}
	if got, want := flow.Amounts[0].String(), "50000000"; got != want {
		t.Errorf("Amounts[0] = %q, want %q", got, want)
	}
	if got, want := flow.DfTokenDelta.String(), "500000000"; got != want {
		t.Errorf("DfTokenDelta = %q, want %q (df_tokens_burned)", got, want)
	}
}

// TestDecodeFlow_unknownVault defends the contract-id pre-filter.
// An event from a vault not in MainnetVaults should return
// ErrUnknownVault (the dispatcher's Matches() should have filtered
// first; this is a defensive double-check).
func TestDecodeFlow_unknownVault(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		ContractID:     "CINVALID",
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value:          "",
	}
	_, err := decodeFlow(ev, EventDeposit)
	if !errors.Is(err, ErrUnknownVault) {
		t.Errorf("err = %v, want ErrUnknownVault", err)
	}
}

// TestDecodeFlow_missingField covers the malformed-input path.
// If the body is missing `df_tokens_minted` we should return
// ErrMalformedPayload, not panic on a nil-deref.
func TestDecodeFlow_missingField(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		ContractID:     MainnetVaultUSDC,
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "depositor", addrSCVal(makeAccountAddress(t, 0xAA))),
			mapEntry(t, "amounts", vecSCVal(i128SCVal(big.NewInt(1)))),
			// no df_tokens_minted
		)),
	}
	_, err := decodeFlow(ev, EventDeposit)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("err = %v, want ErrMalformedPayload", err)
	}
}

// ─── SCVal builders for tests ─────────────────────────────────
// Mirrored from internal/sources/soroswap_router/decode_test.go —
// keeping per-package builders rather than DRYing into a shared
// test helper because the test-time graph stays small + the
// builders are pure Go (no production dependencies to manage).

func i128SCVal(n *big.Int) sdkxdr.ScVal {
	abs := new(big.Int).Set(n)
	if abs.Sign() < 0 {
		abs.Neg(abs)
	}
	bytes := abs.Bytes()
	for len(bytes) < 16 {
		bytes = append([]byte{0}, bytes...)
	}
	hi := int64(0)
	for i := 0; i < 8; i++ {
		hi = (hi << 8) | int64(bytes[i])
	}
	lo := uint64(0)
	for i := 8; i < 16; i++ {
		lo = (lo << 8) | uint64(bytes[i])
	}
	if n.Sign() < 0 {
		hi = ^hi
		lo = ^lo + 1
		if lo == 0 {
			hi++
		}
	}
	return sdkxdr.ScVal{
		Type: sdkxdr.ScValTypeScvI128,
		I128: &sdkxdr.Int128Parts{
			Hi: sdkxdr.Int64(hi),
			Lo: sdkxdr.Uint64(lo),
		},
	}
}

func vecSCVal(elems ...sdkxdr.ScVal) sdkxdr.ScVal {
	v := sdkxdr.ScVec(elems)
	pv := &v
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvVec, Vec: &pv}
}

func addrSCVal(addr sdkxdr.ScAddress) sdkxdr.ScVal {
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvAddress, Address: &addr}
}

func makeAccountAddress(t *testing.T, fillByte byte) sdkxdr.ScAddress {
	t.Helper()
	var ed25519 sdkxdr.Uint256
	for i := range ed25519 {
		ed25519[i] = fillByte
	}
	acct := sdkxdr.AccountId{
		Type:    sdkxdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &ed25519,
	}
	return sdkxdr.ScAddress{Type: sdkxdr.ScAddressTypeScAddressTypeAccount, AccountId: &acct}
}

func mapEntry(t *testing.T, key string, val sdkxdr.ScVal) sdkxdr.ScMapEntry {
	t.Helper()
	sym := sdkxdr.ScSymbol(key)
	keySv := sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvSymbol, Sym: &sym}
	return sdkxdr.ScMapEntry{Key: keySv, Val: val}
}

func mapSCVal(t *testing.T, entries ...sdkxdr.ScMapEntry) sdkxdr.ScVal {
	t.Helper()
	m := sdkxdr.ScMap(entries)
	pm := &m
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvMap, Map: &pm}
}

func mustB64(t *testing.T, sv sdkxdr.ScVal) string {
	t.Helper()
	bs, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal scval: %v", err)
	}
	return base64.StdEncoding.EncodeToString(bs)
}

func mustB64String(t *testing.T, s string) string {
	t.Helper()
	xs := sdkxdr.ScString(s)
	sv := sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvString, Str: &xs}
	return mustB64(t, sv)
}

func mustB64Symbol(t *testing.T, s string) string {
	t.Helper()
	sym := sdkxdr.ScSymbol(s)
	sv := sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvSymbol, Sym: &sym}
	return mustB64(t, sv)
}
