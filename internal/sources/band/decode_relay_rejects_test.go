package band

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// decodeRelayArgs has many reject paths — existing tests cover
// happy/USD-skip/unknown-symbol/empty-rates/too-few-args. This file
// pins the remaining structural rejects so a malformed Band
// invocation can't slip through to the storage layer:
//   - force_relay with too few args
//   - unknown function name (returns ErrNotBandCall — guards the
//     ContractCallDecoder routing seam)
//   - resolve_time pre-epoch fallback to ledger close time

func TestDecodeForceRelay_TooFewArgs_Malformed(t *testing.T) {
	// force_relay needs 3 args; pass 1.
	args := []string{
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{{"BTC", 1}}),
	}
	_, err := decodeRelayArgs(FnForceRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if !errors.Is(err, ErrMalformedArgs) {
		t.Errorf("expected ErrMalformedArgs, got %v", err)
	}
}

func TestDecodeRelayArgs_UnknownFunction_NotBandCall(t *testing.T) {
	// A future Band ABI extension — or a misrouted call — must NOT
	// be decoded as relay/force_relay. ErrNotBandCall is the marker
	// the dispatcher uses to keep dispatching downstream.
	_, err := decodeRelayArgs("get_ref_data", nil, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if !errors.Is(err, ErrNotBandCall) {
		t.Errorf("expected ErrNotBandCall for unknown function, got %v", err)
	}
}

func TestDecodeRelay_PreEpochResolveTimeFallsBackToClosedAt(t *testing.T) {
	// resolveSeconds < 1e9 (pre-2001) is treated as relayer-corruption
	// and replaced with ledger close time. Real-world Band payloads
	// are always post-2020; this defensive path keeps a bogus
	// resolve_time from stamping garbage timestamps on the row.
	closedAt := time.Unix(1_745_000_500, 0).UTC()
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeSymbolRatesArg(t, []struct {
			Symbol string
			Rate   uint64
		}{{"BTC", 50_000_000_000_000}}),
		encodeU64Arg(t, 0), // pre-epoch — triggers fallback
		encodeU64Arg(t, 1),
	}
	updates, err := decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", closedAt)
	if err != nil {
		t.Fatalf("decodeRelayArgs: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	if !updates[0].Timestamp.Equal(closedAt) {
		t.Errorf("Timestamp = %v, want closedAt %v (pre-epoch resolve_time should fall back)",
			updates[0].Timestamp, closedAt)
	}
}

func TestDecodeRelay_MalformedSymbolRatesArg_Rejected(t *testing.T) {
	// Pass a non-Vec for symbol_rates — the parse step itself
	// succeeds but AsVec must reject. Surface as ErrMalformedArgs
	// so dispatcher's drop-stat counter increments rather than
	// crashing the ledger pass.
	notAVec := xdr.ScSymbol("not-a-vec")
	bogusSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &notAVec}
	bogusBytes, err := bogusSv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bogus := base64.StdEncoding.EncodeToString(bogusBytes)

	args := []string{
		encodeAddressArg(t, relayerG),
		bogus, // symbol_rates not a Vec
		encodeU64Arg(t, 1_745_000_000),
		encodeU64Arg(t, 1),
	}
	_, err = decodeRelayArgs(FnRelay, args, adapterC,
		52_000_000, "abcd", 0, "", "", time.Now())
	if !errors.Is(err, ErrMalformedArgs) {
		t.Errorf("expected ErrMalformedArgs for non-Vec symbol_rates, got %v", err)
	}
}

// keep the canonical import live — used implicitly by the
// happy-path test in this same package via shared types.
var _ = canonical.NewFiatAsset
