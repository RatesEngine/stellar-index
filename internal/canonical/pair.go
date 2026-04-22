package canonical

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Pair is a directed (base, quote) market. The direction is
// semantically significant: `XLM/USD` means "price of XLM in USD",
// which is the inverse of `USD/XLM`. Aggregation across venues with
// flipped pair orientations MUST be normalised through [Pair.Flip]
// before combining.
//
// Two Pairs with the same asset on both sides are invalid — see
// [Pair.Validate].
type Pair struct {
	Base  Asset `json:"base"`
	Quote Asset `json:"quote"`
}

// NewPair constructs a Pair and validates it.
func NewPair(base, quote Asset) (Pair, error) {
	p := Pair{Base: base, Quote: quote}
	if err := p.Validate(); err != nil {
		return Pair{}, err
	}
	return p, nil
}

// Validate returns nil if both assets are valid and distinct.
func (p Pair) Validate() error {
	if err := p.Base.Validate(); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	if err := p.Quote.Validate(); err != nil {
		return fmt.Errorf("quote: %w", err)
	}
	if p.Base.Equal(p.Quote) {
		return fmt.Errorf("%w: base and quote are the same asset (%s)",
			ErrPairMismatch, p.Base.String())
	}
	return nil
}

// String emits the wire form `<base>/<quote>` using the asset
// canonical identifiers.
func (p Pair) String() string {
	return p.Base.String() + "/" + p.Quote.String()
}

// Flip returns the reversed Pair. `XLM/USD` → `USD/XLM`. The derived
// price of the flipped pair is 1 divided by the original.
func (p Pair) Flip() Pair {
	return Pair{Base: p.Quote, Quote: p.Base}
}

// Equal reports whether two pairs have the same base and quote in
// the same direction. `XLM/USD != USD/XLM`.
func (p Pair) Equal(q Pair) bool {
	return p.Base.Equal(q.Base) && p.Quote.Equal(q.Quote)
}

// EqualEitherWay reports whether p and q name the same market in
// either direction. Useful for venue-normalisation before aggregation.
func (p Pair) EqualEitherWay(q Pair) bool {
	return p.Equal(q) || p.Equal(q.Flip())
}

// ParsePair inverts String. Input: `<base-asset-id>/<quote-asset-id>`.
func ParsePair(s string) (Pair, error) {
	// Splitting on "/" is subtle: classic assets use "-" internally,
	// Soroban contracts have no separator, and native is literal.
	// The first "/" after the base part is the pair separator.
	// Strategy: scan left-to-right looking for a "/" that could be
	// the separator — i.e., any "/" that isn't inside a known token.
	// The asset grammar in docs/reference/api-design.md §3 forbids
	// "/" inside any asset form, so a simple split on the first "/"
	// from the right works too. We use strings.LastIndex for clarity.
	idx := strings.LastIndex(s, "/")
	if idx <= 0 || idx == len(s)-1 {
		return Pair{}, fmt.Errorf("%w: %q is not a valid pair (expected BASE/QUOTE)",
			ErrPairMismatch, s)
	}
	base, err := ParseAsset(s[:idx])
	if err != nil {
		return Pair{}, fmt.Errorf("base: %w", err)
	}
	quote, err := ParseAsset(s[idx+1:])
	if err != nil {
		return Pair{}, fmt.Errorf("quote: %w", err)
	}
	return NewPair(base, quote)
}

// MarshalJSON emits the structured form {base:..., quote:...}.
// (We deliberately emit the object form here rather than a string,
// because pairs are rarer as user-facing identifiers than assets.)
func (p Pair) MarshalJSON() ([]byte, error) {
	type raw Pair
	return json.Marshal(raw(p))
}

// UnmarshalJSON accepts both string form ("XLM/USD") and object form.
func (p *Pair) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, perr := ParsePair(s)
		if perr != nil {
			return perr
		}
		*p = parsed
		return nil
	}
	type raw Pair
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("pair must be string or object: %w", err)
	}
	*p = Pair(r)
	return p.Validate()
}
