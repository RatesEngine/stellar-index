package soroswap_router

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// TestDecodeRouterArgs_swapExactTokensForTokens covers the
// happy-path decoding of the most-common router entry point.
// Verifies argument positions, i128 amount preservation (no
// truncation per ADR-0003), and the address-strkey round-trip.
func TestDecodeRouterArgs_swapExactTokensForTokens(t *testing.T) {
	t.Parallel()
	// One USDC → XLM → BTC three-hop swap. Path is 3 contract
	// addresses (USDC, XLM-native-wrapper, BTC).
	usdc := makeContractAddress(t, byte(0x00))
	xlm := makeContractAddress(t, byte(0x01))
	btc := makeContractAddress(t, byte(0x02))
	to := makeAccountAddress(t, byte(0x03))

	args := []string{
		mustB64(t, i128SCVal(big.NewInt(100_000_000_000))), // amount_in: 100 USDC at e8
		mustB64(t, i128SCVal(big.NewInt(150_000_000))),     // amount_out_min: 0.0015 BTC at e8
		mustB64(t, vecSCVal(addrSCVal(usdc), addrSCVal(xlm), addrSCVal(btc))),
		mustB64(t, addrSCVal(to)),
		mustB64(t, u64SCVal(1735689600)), // 2025-01-01 deadline
	}

	swap, err := decodeRouterArgs(
		FnSwapExactTokensForTokens, args,
		MainnetRouter,
		uint32(56_000_000),
		"abc123",
		7, // op_index
		"GAOPSOURCE...", "GATXSOURCE...",
		time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("decodeRouterArgs: %v", err)
	}
	if got, want := swap.Source, SourceName; got != want {
		t.Errorf("Source = %q, want %q", got, want)
	}
	if got, want := swap.Function, FnSwapExactTokensForTokens; got != want {
		t.Errorf("Function = %q, want %q", got, want)
	}
	if got, want := swap.AmountIn.String(), "100000000000"; got != want {
		t.Errorf("AmountIn = %q, want %q", got, want)
	}
	if got, want := swap.AmountOut.String(), "150000000"; got != want {
		t.Errorf("AmountOut = %q, want %q", got, want)
	}
	if got, want := len(swap.Path), 3; got != want {
		t.Fatalf("len(Path) = %d, want %d", got, want)
	}
	if !swap.DeadlineTs.Equal(time.Unix(1735689600, 0).UTC()) {
		t.Errorf("DeadlineTs = %v, want 2025-01-01 UTC", swap.DeadlineTs)
	}
	if swap.Recipient == "" {
		t.Errorf("Recipient empty")
	}
}

// TestDecodeRouterArgs_swapTokensForExactTokens covers the
// inverse function — args[0] is exact-out, args[1] is in-max.
// Confirms the (a0, a1) → (AmountIn, AmountOut) inversion in
// the per-function branch.
func TestDecodeRouterArgs_swapTokensForExactTokens(t *testing.T) {
	t.Parallel()
	a := makeContractAddress(t, byte(0x10))
	b := makeContractAddress(t, byte(0x11))
	to := makeAccountAddress(t, byte(0x12))

	args := []string{
		mustB64(t, i128SCVal(big.NewInt(50_000_000))), // amount_out (exact)
		mustB64(t, i128SCVal(big.NewInt(75_000_000))), // amount_in_max
		mustB64(t, vecSCVal(addrSCVal(a), addrSCVal(b))),
		mustB64(t, addrSCVal(to)),
		mustB64(t, u64SCVal(1735689600)),
	}

	swap, err := decodeRouterArgs(
		FnSwapTokensForExactTokens, args,
		MainnetRouter,
		uint32(56_000_001),
		"def456",
		3,
		"GAOPSOURCE...", "GATXSOURCE...",
		time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("decodeRouterArgs: %v", err)
	}
	// Inversion: AmountIn = a1 (in-max), AmountOut = a0 (exact-out).
	if got, want := swap.AmountIn.String(), "75000000"; got != want {
		t.Errorf("AmountIn = %q, want %q (a1 = in-max)", got, want)
	}
	if got, want := swap.AmountOut.String(), "50000000"; got != want {
		t.Errorf("AmountOut = %q, want %q (a0 = exact-out)", got, want)
	}
}

// TestDecodeRouterArgs_unknownFunction defends the dispatcher's
// pre-filter — if some other function name reaches us we return
// ErrUnknownFunction (not a panic, not silent acceptance).
func TestDecodeRouterArgs_unknownFunction(t *testing.T) {
	t.Parallel()
	_, err := decodeRouterArgs("init", nil, MainnetRouter, 0, "", 0, "", "", time.Time{})
	if !errors.Is(err, ErrUnknownFunction) {
		t.Errorf("err = %v, want ErrUnknownFunction", err)
	}
}

// TestDecodeRouterArgs_shortArgs covers the malformed-input path.
// Arity mismatch is the most common malformation (someone calls
// with a partial arg slice during contract migration).
func TestDecodeRouterArgs_shortArgs(t *testing.T) {
	t.Parallel()
	_, err := decodeRouterArgs(
		FnSwapExactTokensForTokens,
		[]string{"only", "two", "args"}, // need 5
		MainnetRouter, 0, "", 0, "", "", time.Time{},
	)
	if !errors.Is(err, ErrMalformedArgs) {
		t.Errorf("err = %v, want ErrMalformedArgs", err)
	}
}

// TestDecodeRouterArgs_pathTooShort covers the path-length
// invariant. Router itself rejects len < 2 at the contract level;
// our decoder mirrors that so a malformed call gets dropped via
// the dispatcher's drop-counter rather than panicking on a
// nil-element vec access downstream.
func TestDecodeRouterArgs_pathTooShort(t *testing.T) {
	t.Parallel()
	a := makeContractAddress(t, byte(0x20))
	to := makeAccountAddress(t, byte(0x21))

	args := []string{
		mustB64(t, i128SCVal(big.NewInt(1))),
		mustB64(t, i128SCVal(big.NewInt(1))),
		mustB64(t, vecSCVal(addrSCVal(a))), // length 1 — too short
		mustB64(t, addrSCVal(to)),
		mustB64(t, u64SCVal(0)),
	}
	_, err := decodeRouterArgs(
		FnSwapExactTokensForTokens, args,
		MainnetRouter, 0, "", 0, "", "", time.Time{},
	)
	if !errors.Is(err, ErrMalformedArgs) {
		t.Errorf("err = %v, want ErrMalformedArgs (path too short)", err)
	}
}

// TestDecodeRouterArgs_zeroDeadlineYieldsZeroTime confirms a
// deadline of 0 (a "no deadline" sentinel) leaves DeadlineTs as the
// zero time.Time so the sink's IsZero() guard NULLs the column,
// rather than stamping the 1970-01-01 epoch.
func TestDecodeRouterArgs_zeroDeadlineYieldsZeroTime(t *testing.T) {
	t.Parallel()
	a := makeContractAddress(t, byte(0x30))
	b := makeContractAddress(t, byte(0x31))
	to := makeAccountAddress(t, byte(0x32))

	args := []string{
		mustB64(t, i128SCVal(big.NewInt(10))),
		mustB64(t, i128SCVal(big.NewInt(20))),
		mustB64(t, vecSCVal(addrSCVal(a), addrSCVal(b))),
		mustB64(t, addrSCVal(to)),
		mustB64(t, u64SCVal(0)), // deadline == 0 sentinel
	}
	swap, err := decodeRouterArgs(
		FnSwapExactTokensForTokens, args,
		MainnetRouter, 0, "tx", 0, "", "",
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("decodeRouterArgs: %v", err)
	}
	if !swap.DeadlineTs.IsZero() {
		t.Errorf("DeadlineTs = %v, want zero time (so the sink NULLs the column)", swap.DeadlineTs)
	}
}

// ─── SCVal builders for tests ─────────────────────────────────
// Keep simple and self-contained — duplicates similar helpers in
// other source packages but the test-time graph is small enough
// that DRYing across packages would obscure rather than help.

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
		// two's complement
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

func u64SCVal(n uint64) sdkxdr.ScVal {
	v := sdkxdr.Uint64(n)
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvU64, U64: &v}
}

func vecSCVal(elems ...sdkxdr.ScVal) sdkxdr.ScVal {
	v := sdkxdr.ScVec(elems)
	pv := &v
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvVec, Vec: &pv}
}

func addrSCVal(addr sdkxdr.ScAddress) sdkxdr.ScVal {
	return sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvAddress, Address: &addr}
}

func makeContractAddress(t *testing.T, fillByte byte) sdkxdr.ScAddress {
	t.Helper()
	var hash sdkxdr.Hash
	for i := range hash {
		hash[i] = fillByte
	}
	cid := sdkxdr.ContractId(hash)
	return sdkxdr.ScAddress{Type: sdkxdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
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

func mustB64(t *testing.T, sv sdkxdr.ScVal) string {
	t.Helper()
	bs, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal scval: %v", err)
	}
	return base64.StdEncoding.EncodeToString(bs)
}
