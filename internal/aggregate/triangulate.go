package aggregate

import (
	"errors"
	"math/big"
)

// ErrTriangulateZero is returned from [Triangulate] when either
// input price is zero or negative. Triangulated prices are only
// well-defined for strictly positive rates.
var ErrTriangulateZero = errors.New("aggregate: triangulate requires positive input prices")

// Triangulate returns the implied price of A→C given the direct
// prices of A→B and B→C:
//
//	price(A→C) = price(A→B) × price(B→C)
//
// Example: with price(USDC→XLM) = 9.5 and price(XLM→EURC) = 0.09,
// Triangulate returns 0.855 as the implied price(USDC→EURC).
//
// Both inputs are read-only. The result is a freshly-allocated
// *big.Rat independent of either input — safe to mutate.
func Triangulate(aToB, bToC *big.Rat) (*big.Rat, error) {
	if aToB == nil || bToC == nil {
		return nil, ErrTriangulateZero
	}
	if aToB.Sign() <= 0 || bToC.Sign() <= 0 {
		return nil, ErrTriangulateZero
	}
	return new(big.Rat).Mul(aToB, bToC), nil
}

// TriangulateChain returns the implied price along an arbitrary-
// length chain of direct prices. TriangulateChain(p1, p2, p3) =
// p1 × p2 × p3 (= price of the first asset in the last asset's
// terms).
//
// Requires ≥ 1 price. Single-price input is a pass-through
// (returns a defensive copy so the caller can mutate the result).
// Any zero/negative price returns [ErrTriangulateZero].
func TriangulateChain(prices ...*big.Rat) (*big.Rat, error) {
	if len(prices) == 0 {
		return nil, ErrTriangulateZero
	}
	out := new(big.Rat).SetInt64(1)
	for _, p := range prices {
		if p == nil || p.Sign() <= 0 {
			return nil, ErrTriangulateZero
		}
		out.Mul(out, p)
	}
	return out, nil
}
