package defindex

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// TestClassify_depositWithdraw covers the topic-byte equality path —
// ensures topic[0] = ScvString("BlendStrategy") + topic[1] in
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
			topic:     []string{TopicPrefixStrategy, TopicSymbolDeposit},
			wantClass: EventDeposit,
		},
		{
			name:      "withdraw",
			topic:     []string{TopicPrefixStrategy, TopicSymbolWithdraw},
			wantClass: EventWithdraw,
		},
		{
			name:      "wrong prefix (SoroswapPair)",
			topic:     []string{mustB64String(t, "SoroswapPair"), TopicSymbolDeposit},
			wantClass: "",
		},
		{
			name:      "prefix as Symbol not String",
			topic:     []string{mustB64Symbol(t, "BlendStrategy"), TopicSymbolDeposit},
			wantClass: "",
		},
		{
			// EVERY-event policy (2026-05-27): harvest is now classified
			// even though we don't produce a StrategyFlow for it yet.
			name:      "harvest (classification-only)",
			topic:     []string{TopicPrefixStrategy, TopicSymbolHarvest},
			wantClass: EventHarvest,
		},
		{
			name:      "single-element topic",
			topic:     []string{TopicPrefixStrategy},
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

// TestDecodeFlow_deposit covers the happy-path decode of a deposit
// event with an account (G-strkey) `from`. Verifies amount
// preservation (no truncation per ADR-0003) and address round-trip.
func TestDecodeFlow_deposit(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_000_000,
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		ContractID:     "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP",
		OperationIndex: 2,
		TxHash:         "abc123",
		Topic:          []string{TopicPrefixStrategy, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "from", addrSCVal(makeAccountAddress(t, 0xAA))),
			mapEntry(t, "amount", i128SCVal(big.NewInt(123_456_789_000))),
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
	if flow.From == "" || flow.From[0] != 'G' {
		t.Errorf("From = %q, want a G-strkey account address", flow.From)
	}
	if got, want := flow.Amount.String(), "123456789000"; got != want {
		t.Errorf("Amount = %q, want %q (no truncation)", got, want)
	}
	if flow.Ledger != 60_000_000 || flow.OpIndex != 2 || flow.TxHash != "abc123" {
		t.Errorf("header fields not preserved: %+v", flow)
	}
}

// TestDecodeFlow_withdrawFromContract covers the withdraw branch
// AND the real-world case where `from` is the vault/router
// *contract* (a C-strkey), not an end-user account — exactly what
// scan-soroban-events observed on mainnet. The body shape is
// identical to deposit; only Direction differs.
func TestDecodeFlow_withdrawFromContract(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_000_001,
		LedgerClosedAt: "2026-05-14T10:31:00Z",
		ContractID:     "CC5CE6MWISDXT3MLNQ7R3FVILFVFEIH3COWGH45GJKL6BD2ZHF7F7JVI",
		Topic:          []string{TopicPrefixStrategy, TopicSymbolWithdraw},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "from", addrSCVal(makeContractAddress(t, 0xBB))),
			mapEntry(t, "amount", i128SCVal(big.NewInt(29_999_999))),
		)),
	}
	flow, err := decodeFlow(ev, EventWithdraw)
	if err != nil {
		t.Fatalf("decodeFlow: %v", err)
	}
	if flow.Direction != DirectionWithdraw {
		t.Errorf("Direction = %q, want withdraw", flow.Direction)
	}
	if flow.From == "" || flow.From[0] != 'C' {
		t.Errorf("From = %q, want a C-strkey contract address", flow.From)
	}
	if got, want := flow.Amount.String(), "29999999"; got != want {
		t.Errorf("Amount = %q, want %q", got, want)
	}
}

// TestDecodeFlow_missingField covers the malformed-input path. A
// body missing `amount` must return ErrMalformedPayload, not panic
// on a nil-deref.
func TestDecodeFlow_missingField(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		ContractID:     "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP",
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		Topic:          []string{TopicPrefixStrategy, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "from", addrSCVal(makeAccountAddress(t, 0xAA))),
			// no amount
		)),
	}
	_, err := decodeFlow(ev, EventDeposit)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("err = %v, want ErrMalformedPayload", err)
	}
}

// TestDecodeFlow_badKind defends the defensive default branch — a
// kind classify() would never return must still error cleanly.
func TestDecodeFlow_badKind(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		LedgerClosedAt: "2026-05-14T10:30:00Z",
		Topic:          []string{TopicPrefixStrategy, TopicSymbolDeposit},
		Value:          mustB64(t, mapSCVal(t)),
	}
	_, err := decodeFlow(ev, "rebalance")
	if !errors.Is(err, ErrUnknownEvent) {
		t.Errorf("err = %v, want ErrUnknownEvent", err)
	}
}

// ─── Phase B (vault layer) tests ──────────────────────────────

// TestClassifyVault_depositWithdraw mirrors TestClassify_depositWithdraw
// for the vault-wrapper topic prefix. Topic[1] symbols are shared
// between strategy + vault layers (`deposit` / `withdraw`), so the
// reject paths here mainly cover topic[0] discrimination.
func TestClassifyVault_depositWithdraw(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		topic     []string
		wantClass string
	}{
		{
			name:      "vault deposit",
			topic:     []string{TopicPrefixVault, TopicSymbolDeposit},
			wantClass: EventDeposit,
		},
		{
			name:      "vault withdraw",
			topic:     []string{TopicPrefixVault, TopicSymbolWithdraw},
			wantClass: EventWithdraw,
		},
		{
			name:      "strategy prefix routes to classify(), not classifyVault()",
			topic:     []string{TopicPrefixStrategy, TopicSymbolDeposit},
			wantClass: "",
		},
		{
			name:      "vault prefix encoded as Symbol not String",
			topic:     []string{mustB64Symbol(t, "DeFindexVault"), TopicSymbolDeposit},
			wantClass: "",
		},
		// EVERY-event policy (2026-05-27): the nine vault governance /
		// admin / multiplexed-rebalance topics are now classified
		// (still no decoder — classification only). Pre-policy these
		// returned "" and got silently dropped.
		{name: "vault rescue", topic: []string{TopicPrefixVault, TopicSymbolRescue}, wantClass: EventRescue},
		{name: "vault paused", topic: []string{TopicPrefixVault, TopicSymbolPaused}, wantClass: EventPaused},
		{name: "vault unpaused", topic: []string{TopicPrefixVault, TopicSymbolUnpaused}, wantClass: EventUnpaused},
		{name: "vault nreceiver", topic: []string{TopicPrefixVault, TopicSymbolNReceiver}, wantClass: EventNReceiver},
		{name: "vault nmanager", topic: []string{TopicPrefixVault, TopicSymbolNManager}, wantClass: EventNManager},
		{name: "vault nemanager", topic: []string{TopicPrefixVault, TopicSymbolNEManager}, wantClass: EventNEManager},
		{name: "vault rbmanager", topic: []string{TopicPrefixVault, TopicSymbolRBManager}, wantClass: EventRBManager},
		{name: "vault dfees", topic: []string{TopicPrefixVault, TopicSymbolDFees}, wantClass: EventDFees},
		{name: "vault rebalance (multiplexed body)", topic: []string{TopicPrefixVault, TopicSymbolRebalance}, wantClass: EventRebalance},
		{
			name:      "single-element topic",
			topic:     []string{TopicPrefixVault},
			wantClass: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &events.Event{Topic: tc.topic}
			got := classifyVault(ev)
			if got != tc.wantClass {
				t.Errorf("classifyVault = %q, want %q", got, tc.wantClass)
			}
		})
	}
}

// TestClassifyFactory_createNfee covers the factory layer added per
// EVERY-event policy (project_every_event_principle). Factory events
// are classified-only — Decode returns (nil, nil) on a factory match
// so the dispatcher's drop-counter doesn't file them as unmatched.
func TestClassifyFactory_createNfee(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		topic     []string
		wantClass string
	}{
		{
			name:      "factory create",
			topic:     []string{TopicPrefixFactory, TopicSymbolCreate},
			wantClass: EventCreate,
		},
		{
			name:      "factory n_fee",
			topic:     []string{TopicPrefixFactory, TopicSymbolNFee},
			wantClass: EventNFee,
		},
		{
			name:      "strategy prefix routes to classify(), not classifyFactory()",
			topic:     []string{TopicPrefixStrategy, TopicSymbolCreate},
			wantClass: "",
		},
		{
			name:      "vault prefix routes to classifyVault(), not classifyFactory()",
			topic:     []string{TopicPrefixVault, TopicSymbolCreate},
			wantClass: "",
		},
		{
			name:      "factory prefix encoded as Symbol not String",
			topic:     []string{mustB64Symbol(t, "DeFindexFactory"), TopicSymbolCreate},
			wantClass: "",
		},
		{
			name:      "factory with deposit symbol (wrong topic[1])",
			topic:     []string{TopicPrefixFactory, TopicSymbolDeposit},
			wantClass: "",
		},
		{
			name:      "single-element topic",
			topic:     []string{TopicPrefixFactory},
			wantClass: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &events.Event{Topic: tc.topic}
			got := classifyFactory(ev)
			if got != tc.wantClass {
				t.Errorf("classifyFactory = %q, want %q", got, tc.wantClass)
			}
		})
	}
}

// TestDecode_factoryEvent_isClassifiedButEmits0Events verifies that
// the dispatcher's Decode entrypoint returns (nil, nil) — not the
// `ErrUnknownEvent` sentinel — for a factory match. This is the
// closed-loop completeness check: Matches() returns true → Decode()
// returns no error and no events, the event is consumed cleanly
// rather than recorded as an unmatched-topic drop.
func TestDecode_factoryEvent_isClassifiedButEmits0Events(t *testing.T) {
	t.Parallel()
	d := NewDecoder()
	ev := events.Event{Topic: []string{TopicPrefixFactory, TopicSymbolCreate}}
	if !d.Matches(ev) {
		t.Fatal("Matches(factory create) = false, want true")
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Errorf("Decode(factory create) err = %v, want nil", err)
	}
	if len(out) != 0 {
		t.Errorf("Decode(factory create) emitted %d events, want 0", len(out))
	}
}

// TestDecodeVaultFlow_deposit covers the happy path for a vault
// deposit event with the audit-doc body schema: a G-strkey
// `depositor`, a single-element `amounts` Vec<i128>, and
// `df_tokens_minted` i128. Verifies the User strkey round-trips,
// amounts preserve precision (ADR-0003), and direction is set.
func TestDecodeVaultFlow_deposit(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_500_000,
		LedgerClosedAt: "2026-05-15T08:00:00Z",
		ContractID:     "CCA2ZJP5BVRXYTQH4FAGHCAUMRYCXVC4CRYC2NXHWMR7TIVX36U7F5HR",
		OperationIndex: 1,
		TxHash:         "vault-dep-abc",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "amounts", vecSCVal(t, i128SCVal(big.NewInt(10_000_000)))),
			mapEntry(t, "depositor", addrSCVal(makeAccountAddress(t, 0xCC))),
			mapEntry(t, "df_tokens_minted", i128SCVal(big.NewInt(9_876_543))),
		)),
	}
	flow, err := decodeVaultFlow(ev, EventDeposit)
	if err != nil {
		t.Fatalf("decodeVaultFlow: %v", err)
	}
	if flow.Source != SourceName {
		t.Errorf("Source = %q, want %q", flow.Source, SourceName)
	}
	if flow.Direction != DirectionDeposit {
		t.Errorf("Direction = %q, want deposit", flow.Direction)
	}
	if flow.User == "" || flow.User[0] != 'G' {
		t.Errorf("User = %q, want a G-strkey depositor", flow.User)
	}
	if got, want := len(flow.Amounts), 1; got != want {
		t.Fatalf("len(Amounts) = %d, want %d", got, want)
	}
	if got, want := flow.Amounts[0].String(), "10000000"; got != want {
		t.Errorf("Amounts[0] = %q, want %q", got, want)
	}
	if got, want := flow.DfTokens.String(), "9876543"; got != want {
		t.Errorf("DfTokens = %q, want %q", got, want)
	}
	if flow.Ledger != 60_500_000 || flow.OpIndex != 1 || flow.TxHash != "vault-dep-abc" {
		t.Errorf("header fields not preserved: %+v", flow)
	}
}

// TestDecodeVaultFlow_withdraw covers the withdraw branch and the
// per-direction field-name swap (`withdrawer` / `amounts_withdrawn`
// / `df_tokens_burned`). Body has a multi-asset amounts vec to
// confirm the Vec-decode loop handles >1 element.
func TestDecodeVaultFlow_withdraw(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Type:           "contract",
		Ledger:         60_500_001,
		LedgerClosedAt: "2026-05-15T08:01:00Z",
		ContractID:     "CCA2ZJP5BVRXYTQH4FAGHCAUMRYCXVC4CRYC2NXHWMR7TIVX36U7F5HR",
		Topic:          []string{TopicPrefixVault, TopicSymbolWithdraw},
		Value: mustB64(t, mapSCVal(t,
			// Two-asset basket exercises the Vec loop.
			mapEntry(t, "amounts_withdrawn", vecSCVal(t,
				i128SCVal(big.NewInt(5_000_000)),
				i128SCVal(big.NewInt(2_500_000)),
			)),
			mapEntry(t, "df_tokens_burned", i128SCVal(big.NewInt(7_400_000))),
			mapEntry(t, "withdrawer", addrSCVal(makeAccountAddress(t, 0xDD))),
		)),
	}
	flow, err := decodeVaultFlow(ev, EventWithdraw)
	if err != nil {
		t.Fatalf("decodeVaultFlow: %v", err)
	}
	if flow.Direction != DirectionWithdraw {
		t.Errorf("Direction = %q, want withdraw", flow.Direction)
	}
	if flow.User == "" || flow.User[0] != 'G' {
		t.Errorf("User = %q, want a G-strkey withdrawer", flow.User)
	}
	if got, want := len(flow.Amounts), 2; got != want {
		t.Fatalf("len(Amounts) = %d, want %d (multi-asset vec)", got, want)
	}
	if flow.Amounts[0].String() != "5000000" || flow.Amounts[1].String() != "2500000" {
		t.Errorf("Amounts = [%s, %s], want [5000000, 2500000]",
			flow.Amounts[0], flow.Amounts[1])
	}
	if got, want := flow.DfTokens.String(), "7400000"; got != want {
		t.Errorf("DfTokens = %q, want %q", got, want)
	}
}

// TestDecodeVaultFlow_routerDepositorContract confirms the
// occasional case where the depositor is a router/aggregator
// C-strkey (e.g. coming via a Soroswap-route into the vault) rather
// than a direct user G-strkey. Both decode the same way; only the
// User prefix differs.
func TestDecodeVaultFlow_routerDepositorContract(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		Ledger:         60_500_002,
		LedgerClosedAt: "2026-05-15T08:02:00Z",
		ContractID:     "CCA2ZJP5BVRXYTQH4FAGHCAUMRYCXVC4CRYC2NXHWMR7TIVX36U7F5HR",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "amounts", vecSCVal(t, i128SCVal(big.NewInt(1_111_111)))),
			mapEntry(t, "depositor", addrSCVal(makeContractAddress(t, 0xEE))),
			mapEntry(t, "df_tokens_minted", i128SCVal(big.NewInt(1_000_000))),
		)),
	}
	flow, err := decodeVaultFlow(ev, EventDeposit)
	if err != nil {
		t.Fatalf("decodeVaultFlow: %v", err)
	}
	if flow.User == "" || flow.User[0] != 'C' {
		t.Errorf("User = %q, want a C-strkey contract address", flow.User)
	}
}

// TestDecodeVaultFlow_missingField defends the malformed-input
// path. The vault body has more required fields than the strategy
// body, so we explicitly verify the per-direction field names get
// surfaced in the error.
func TestDecodeVaultFlow_missingField(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		ContractID:     "CCA2ZJP5BVRXYTQH4FAGHCAUMRYCXVC4CRYC2NXHWMR7TIVX36U7F5HR",
		LedgerClosedAt: "2026-05-15T08:00:00Z",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "depositor", addrSCVal(makeAccountAddress(t, 0xCC))),
			// no amounts, no df_tokens_minted
		)),
	}
	_, err := decodeVaultFlow(ev, EventDeposit)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("err = %v, want ErrMalformedPayload", err)
	}
}

// TestDecodeVaultFlow_emptyAmountsVec covers the degenerate but
// valid case of a zero-asset deposit — the Vec is empty rather
// than missing. Empty Vec is legal SCVal and the decoder accepts
// it (downstream consumers can decide what to do with no flow).
func TestDecodeVaultFlow_emptyAmountsVec(t *testing.T) {
	t.Parallel()
	ev := &events.Event{
		ContractID:     "CCA2ZJP5BVRXYTQH4FAGHCAUMRYCXVC4CRYC2NXHWMR7TIVX36U7F5HR",
		LedgerClosedAt: "2026-05-15T08:00:00Z",
		Topic:          []string{TopicPrefixVault, TopicSymbolDeposit},
		Value: mustB64(t, mapSCVal(t,
			mapEntry(t, "amounts", vecSCVal(t)),
			mapEntry(t, "depositor", addrSCVal(makeAccountAddress(t, 0xCC))),
			mapEntry(t, "df_tokens_minted", i128SCVal(big.NewInt(0))),
		)),
	}
	flow, err := decodeVaultFlow(ev, EventDeposit)
	if err != nil {
		t.Fatalf("decodeVaultFlow: %v", err)
	}
	if len(flow.Amounts) != 0 {
		t.Errorf("len(Amounts) = %d, want 0", len(flow.Amounts))
	}
}

// vecSCVal builds a Vec<ScVal>. Mirrors the helper in
// internal/scval/scval_test.go (kept here rather than DRYed because
// the production package doesn't export test builders).
func vecSCVal(t *testing.T, elts ...sdkxdr.ScVal) sdkxdr.ScVal {
	t.Helper()
	vec := sdkxdr.ScVec(elts)
	pv := &vec
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvVec, Vec: &pv}
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

func makeContractAddress(t *testing.T, fillByte byte) sdkxdr.ScAddress {
	t.Helper()
	var cid sdkxdr.ContractId
	for i := range cid {
		cid[i] = fillByte
	}
	return sdkxdr.ScAddress{Type: sdkxdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
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
