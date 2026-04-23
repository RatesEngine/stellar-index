package aggregate_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
)

func TestTriangulate_Basic(t *testing.T) {
	// USDC→XLM = 19/2 (9.5), XLM→EURC = 9/100 (0.09)
	//   → USDC→EURC = 19/2 × 9/100 = 171/200 = 0.855
	aToB := big.NewRat(19, 2)
	bToC := big.NewRat(9, 100)

	got, err := aggregate.Triangulate(aToB, bToC)
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(171, 200)
	if got.Cmp(want) != 0 {
		t.Errorf("Triangulate = %v, want %v", got, want)
	}
}

func TestTriangulate_ExactPrecision(t *testing.T) {
	// Third-values that would drift through float64 must stay exact.
	aToB := big.NewRat(1, 3)
	bToC := big.NewRat(1, 3)
	got, err := aggregate.Triangulate(aToB, bToC)
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(1, 9)
	if got.Cmp(want) != 0 {
		t.Errorf("Triangulate = %v, want 1/9", got)
	}
}

func TestTriangulate_Rejects(t *testing.T) {
	valid := big.NewRat(1, 1)
	zero := new(big.Rat)
	neg := big.NewRat(-1, 1)

	for name, inputs := range map[string][2]*big.Rat{
		"nil A":    {nil, valid},
		"nil B":    {valid, nil},
		"zero A":   {zero, valid},
		"zero B":   {valid, zero},
		"neg A":    {neg, valid},
		"neg B":    {valid, neg},
	} {
		_, err := aggregate.Triangulate(inputs[0], inputs[1])
		if !errors.Is(err, aggregate.ErrTriangulateZero) {
			t.Errorf("%s: err = %v, want ErrTriangulateZero", name, err)
		}
	}
}

func TestTriangulate_DoesNotMutateInputs(t *testing.T) {
	a := big.NewRat(2, 1)
	b := big.NewRat(3, 1)
	_, err := aggregate.Triangulate(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Cmp(big.NewRat(2, 1)) != 0 {
		t.Errorf("input A mutated: %v", a)
	}
	if b.Cmp(big.NewRat(3, 1)) != 0 {
		t.Errorf("input B mutated: %v", b)
	}
}

func TestTriangulateChain_ThreeHop(t *testing.T) {
	// 2 × 3 × 5 = 30
	got, err := aggregate.TriangulateChain(
		big.NewRat(2, 1),
		big.NewRat(3, 1),
		big.NewRat(5, 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(30, 1)) != 0 {
		t.Errorf("chain = %v, want 30", got)
	}
}

func TestTriangulateChain_SingleIsPassthrough(t *testing.T) {
	p := big.NewRat(7, 3)
	got, err := aggregate.TriangulateChain(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(p) != 0 {
		t.Errorf("passthrough = %v, want %v", got, p)
	}
	// Mutating the output must not touch the input.
	got.Add(got, big.NewRat(1, 1))
	if p.Cmp(big.NewRat(7, 3)) != 0 {
		t.Error("passthrough returned the caller's rat directly — not defensive")
	}
}

func TestTriangulateChain_Empty(t *testing.T) {
	_, err := aggregate.TriangulateChain()
	if !errors.Is(err, aggregate.ErrTriangulateZero) {
		t.Fatalf("err = %v, want ErrTriangulateZero", err)
	}
}
