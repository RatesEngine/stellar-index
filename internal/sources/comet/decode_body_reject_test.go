package comet

import (
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// sdkDecodeSwapBody has 5 required fields, each with a "missing"
// path and a "wrong type" path. Existing tests cover one missing
// field (token_out) and the happy path. This file pins:
//   - body that is not a Map at all
//   - missing caller / token_in / token_amount_in / token_amount_out
//   - wrong type for caller (symbol instead of Address)

// encodeSymbolBody builds an SCVal::Symbol body — not a Map. Every
// reject path that depends on AsMap should hit this.
func encodeSymbolBody(t *testing.T) string {
	t.Helper()
	sym := xdr.ScSymbol("definitely-not-a-map")
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodeMapBody assembles a Map from explicit (key, value) pairs —
// gives tests fine control over which fields are present and their
// types. Existing encodeSwapBody only builds well-formed swaps.
func encodeMapBody(t *testing.T, pairs []struct {
	K string
	V xdr.ScVal
},
) string {
	t.Helper()
	m := make(xdr.ScMap, len(pairs))
	for i, p := range pairs {
		sym := xdr.ScSymbol(p.K)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: p.V,
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestSdkDecodeSwapBody_bodyNotAMap(t *testing.T) {
	if _, err := sdkDecodeSwapBody(encodeSymbolBody(t)); err == nil {
		t.Fatal("expected error for non-Map body, got nil")
	} else if !strings.Contains(err.Error(), "Map") {
		t.Errorf("error %q should cite \"Map\"", err.Error())
	}
}

func TestSdkDecodeSwapBody_missingFields(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	callerSv := addressScValFromStrkey(t, caller)
	tokenInSv := addressScValFromStrkey(t, tokenIn)
	tokenOutSv := addressScValFromStrkey(t, tokenOut)
	amountInSv := i128ScVal(t, big.NewInt(1))
	amountOutSv := i128ScVal(t, big.NewInt(2))

	type kv = struct {
		K string
		V xdr.ScVal
	}
	all := []kv{
		{"caller", callerSv},
		{"token_in", tokenInSv},
		{"token_out", tokenOutSv},
		{"token_amount_in", amountInSv},
		{"token_amount_out", amountOutSv},
	}

	// Drop one field at a time and assert the error mentions it.
	for _, drop := range []string{"caller", "token_in", "token_amount_in", "token_amount_out"} {
		t.Run("missing/"+drop, func(t *testing.T) {
			pairs := make([]kv, 0, len(all)-1)
			for _, p := range all {
				if p.K != drop {
					pairs = append(pairs, p)
				}
			}
			_, err := sdkDecodeSwapBody(encodeMapBody(t, pairs))
			if err == nil {
				t.Fatalf("expected error for missing %q, got nil", drop)
			}
			if !strings.Contains(err.Error(), drop) {
				t.Errorf("error %q should cite missing field %q", err.Error(), drop)
			}
		})
	}
}

func TestSdkDecodeSwapBody_wrongTypeRejected(t *testing.T) {
	// caller present but as a Symbol, not an Address — the
	// AsAddressStrkey check must reject; otherwise downstream
	// strkey logic would emit a malformed string and break trade
	// attribution.
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	tokenInSv := addressScValFromStrkey(t, tokenIn)
	tokenOutSv := addressScValFromStrkey(t, tokenOut)
	amountInSv := i128ScVal(t, big.NewInt(1))
	amountOutSv := i128ScVal(t, big.NewInt(2))

	bogusCallerSym := xdr.ScSymbol("not-an-address")
	bogusCaller := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &bogusCallerSym}

	body := encodeMapBody(t, []struct {
		K string
		V xdr.ScVal
	}{
		{"caller", bogusCaller},
		{"token_in", tokenInSv},
		{"token_out", tokenOutSv},
		{"token_amount_in", amountInSv},
		{"token_amount_out", amountOutSv},
	})

	if _, err := sdkDecodeSwapBody(body); err == nil {
		t.Fatal("expected error for non-Address caller, got nil")
	} else if !strings.Contains(err.Error(), "caller") {
		t.Errorf("error %q should cite \"caller\"", err.Error())
	}

	// And via the public decodeSwap entry point — must return
	// ErrMalformedPayload so dispatcher's drop-stat counter increments
	// rather than aborting the ledger.
	ev := &events.Event{
		Topic: []string{TopicSymbolPool, TopicSymbolSwap},
		Value: body,
	}
	if _, err := decodeSwap(ev, time.Now()); !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("decodeSwap: expected ErrMalformedPayload, got %v", err)
	}
}
