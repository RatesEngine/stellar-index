package reflector

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// quoteForVariant has a default-fallback branch for unknown Variant
// values (e.g. a future ADR-0014 oracle variant we haven't wired
// up). Pin: the fallback returns native XLM rather than zero — a
// zero-asset return would later break canonical.NewPair downstream.
//
// TestQuoteForVariant in source_test.go covers DEX/CEX/FX. This
// adds the default branch.

func TestQuoteForVariant_unknownFallsBackToNative(t *testing.T) {
	const bogus Variant = 99
	got := quoteForVariant(bogus)
	if got.Type != canonical.AssetNative {
		t.Errorf("quoteForVariant(unknown) = %+v, want native (default fallback)", got)
	}
}

// sdkDecodeUpdateTimestamp's parse-error branch — invalid base64 →
// surfaced as wrapped error so callers can drop the event without
// crashing.
func TestSdkDecodeUpdateTimestamp_invalidBase64(t *testing.T) {
	if _, err := sdkDecodeUpdateTimestamp("!!!not-base64!!!"); err == nil {
		t.Error("expected parse error for invalid base64, got nil")
	}
}

// sdkDecodeUpdateTimestamp wrong-kind path — a Symbol where U64 is
// expected. AsU64 must reject; otherwise the timestamp slot would
// silently be 0.
func TestSdkDecodeUpdateTimestamp_wrongKind(t *testing.T) {
	// Encode an ScSymbol where U64 is expected — AsU64 must reject.
	sym := xdr.ScSymbol("not-a-u64")
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	raw, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bad := base64.StdEncoding.EncodeToString(raw)
	if _, err := sdkDecodeUpdateTimestamp(bad); err == nil {
		t.Error("expected error decoding Symbol as U64, got nil")
	}
}
