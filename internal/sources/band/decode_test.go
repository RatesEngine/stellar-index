package band

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ─── fixture helpers ─────────────────────────────────────────────

const (
	// adapterC is the mainnet StandardReference address per
	// docs/discovery/oracles/band.md. The decoder matches against it
	// but doesn't otherwise touch network — any valid C-strkey works.
	adapterC = "CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M"
)

// relayerG is a valid G-strkey generated from a fixed seed so we
// don't hard-code a checksum that could drift.
var relayerG = func() string {
	seed := [32]byte{
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00,
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, seed[:])
	if err != nil {
		panic("strkey encode seed: " + err.Error())
	}
	return s
}()

// encodeAddressArg marshals a G-strkey as base64 SCVal::Address.
func encodeAddressArg(t *testing.T, g string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, g)
	if err != nil {
		t.Fatalf("decode strkey: %v", err)
	}
	var pub xdr.Uint256
	copy(pub[:], raw)
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	addr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &aid,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodeSymbolRatesArg marshals a Vec<(Symbol, u64)> as the base64
// SCVal wire form the relayer sends. Each (symbol, rate) entry is
// an ScvVec of length 2 — soroban-sdk's tuple serialization.
func encodeSymbolRatesArg(t *testing.T, pairs []struct {
	Symbol string
	Rate   uint64
},
) string {
	t.Helper()
	items := make([]xdr.ScVal, len(pairs))
	for i, p := range pairs {
		s := xdr.ScSymbol(p.Symbol)
		symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &s}
		u := xdr.Uint64(p.Rate)
		rateSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
		tuple := xdr.ScVec{symSv, rateSv}
		pt := &tuple
		items[i] = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pt}
	}
	outer := xdr.ScVec(items)
	po := &outer
	sv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &po}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal symbol_rates: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodeU64Arg marshals a u64 as base64 SCVal::U64.
func encodeU64Arg(t *testing.T, n uint64) string {
	t.Helper()
	u := xdr.Uint64(n)
	sv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal u64: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ─── tests ───────────────────────────────────────────────────────

func TestDecodeRelay_HappyPath(t *testing.T) {
	const resolveSec = uint64(1_745_000_000)
	const btcRateE9 = uint64(500_000_000_000_000) // $500k at E9
	const ethRateE9 = uint64(35_000_000_000_000)  // $35k at E9

	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{
			{"BTC", btcRateE9},
			{"ETH", ethRateE9},
		}),
		encodeU64Arg(t, resolveSec),
		encodeU64Arg(t, 1), // request_id
	}

	updates, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if err != nil {
		t.Fatalf("decodeRelayArgs: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	btc, _ := canonical.NewCryptoAsset("BTC")
	eth, _ := canonical.NewCryptoAsset("ETH")
	if !updates[0].Asset.Equal(btc) {
		t.Errorf("updates[0].Asset = %+v want BTC", updates[0].Asset)
	}
	if !updates[1].Asset.Equal(eth) {
		t.Errorf("updates[1].Asset = %+v want ETH", updates[1].Asset)
	}
	if updates[0].Price.BigInt().Cmp(new(big.Int).SetUint64(btcRateE9)) != 0 {
		t.Errorf("BTC price = %s want %d", updates[0].Price, btcRateE9)
	}
	if updates[0].Decimals != 9 {
		t.Errorf("decimals = %d want 9", updates[0].Decimals)
	}
	// Timestamp sourced from resolve_time (seconds), not close.
	if updates[0].Timestamp.Unix() != int64(resolveSec) {
		t.Errorf("timestamp %v != resolveSec %d", updates[0].Timestamp, resolveSec)
	}
	// Observer = `from` arg on relay().
	if updates[0].Observer != relayerG {
		t.Errorf("observer = %q want %q", updates[0].Observer, relayerG)
	}
	// OpIndex fan-out: slot 0, slot 1 under same base.
	if updates[0].OpIndex != 0 || updates[1].OpIndex != 1 {
		t.Errorf("OpIndex fan-out wrong: [%d, %d]", updates[0].OpIndex, updates[1].OpIndex)
	}
	// Quote = USD (Band single-symbol convention).
	usd, _ := canonical.NewFiatAsset("USD")
	if !updates[0].Quote.Equal(usd) {
		t.Errorf("quote = %+v want USD", updates[0].Quote)
	}
}

func TestDecodeForceRelay_HappyPath(t *testing.T) {
	// force_relay has 3 args (no `from`). Observer should fall back
	// to opSource (here a G-strkey we pass directly).
	const resolveSec = uint64(1_745_000_100)
	args := []string{
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{
			{"XLM", 120_000_000},
		}),
		encodeU64Arg(t, resolveSec),
		encodeU64Arg(t, 42),
	}
	updates, err := decodeRelayArgs(FnForceRelay, args, adapterC,
		52_000_001, "ef01", 0, relayerG /*opSource*/, "" /*txSource*/, time.Now())
	if err != nil {
		t.Fatalf("decodeRelayArgs: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Observer != relayerG {
		t.Errorf("force_relay observer = %q want %q (opSource fallback)",
			updates[0].Observer, relayerG)
	}
}

func TestDecodeRelay_USDSymbolSkipped(t *testing.T) {
	// USD is special-cased in Band's storage (always 1@E9, relayer
	// writes rejected). Mixed-payload: USD skipped, BTC lands.
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{
			{"USD", 1_000_000_000}, // should be skipped
			{"BTC", 500_000_000_000_000},
		}),
		encodeU64Arg(t, 1_745_000_000),
		encodeU64Arg(t, 1),
	}
	updates, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if err != nil {
		t.Fatalf("decodeRelayArgs: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update (BTC only), got %d", len(updates))
	}
	btc, _ := canonical.NewCryptoAsset("BTC")
	if !updates[0].Asset.Equal(btc) {
		t.Errorf("updates[0].Asset = %+v want BTC", updates[0].Asset)
	}
	// OpIndex preserves slot 1 (USD was at slot 0).
	if updates[0].OpIndex != 1 {
		t.Errorf("OpIndex = %d want 1 (USD skipped at slot 0)", updates[0].OpIndex)
	}
}

func TestDecodeRelay_UnknownSymbolSkipped(t *testing.T) {
	// Partial-event skip: BTC lands, NOTACOIN skipped.
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{
			{"NOTACOIN", 999},
			{"BTC", 500_000_000_000_000},
		}),
		encodeU64Arg(t, 1_745_000_000),
		encodeU64Arg(t, 1),
	}
	updates, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if err != nil {
		t.Fatalf("decodeRelayArgs: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
}

func TestDecodeRelay_EmptyRates_Rejects(t *testing.T) {
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, nil),
		encodeU64Arg(t, 1_745_000_000),
		encodeU64Arg(t, 1),
	}
	_, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if !errors.Is(err, ErrEmptyRates) {
		t.Errorf("expected ErrEmptyRates, got %v", err)
	}
}

func TestDecodeRelay_TooFewArgs_Malformed(t *testing.T) {
	// relay requires 4 args; supply only 2.
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{{"BTC", 1}}),
	}
	_, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if !errors.Is(err, ErrMalformedArgs) {
		t.Errorf("expected ErrMalformedArgs, got %v", err)
	}
}

func TestDecoder_MatchesOnlyRelayFunctions(t *testing.T) {
	d := NewDecoder(adapterC)
	if !d.Matches(adapterC, "relay") {
		t.Error("expected match on relay")
	}
	if !d.Matches(adapterC, "force_relay") {
		t.Error("expected match on force_relay")
	}
	if d.Matches(adapterC, "get_ref_data") {
		t.Error("get_ref_data should not match (read-only)")
	}
	if d.Matches("CWRONGADDRESS3333333333333333333333333333333333333333333", "relay") {
		t.Error("wrong contract should not match")
	}
}
