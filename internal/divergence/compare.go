package divergence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Result is the outcome of one divergence comparison. Carries
// enough detail for both the aggregator's flag-gating decision and
// the operator's diagnostic dashboards.
type Result struct {
	// Pair is the pair being compared.
	Pair canonical.Pair

	// OurPrice is what our aggregator computed.
	OurPrice float64

	// Median is the median of references that returned a price.
	// Zero when SuccessCount == 0 (no references reported);
	// caller MUST check SuccessCount before reading Median.
	Median float64

	// DivergencePct is `abs(OurPrice - Median) / Median * 100`,
	// or 0 when SuccessCount == 0.
	DivergencePct float64

	// Sources maps reference Name() → reported price. Only
	// successful references appear here.
	Sources map[string]float64

	// Failures maps reference Name() → error message. Empty when
	// every reference succeeded; non-empty entries indicate which
	// sources we couldn't reach this run.
	Failures map[string]string

	// SuccessCount is len(Sources). Convenience for callers that
	// gate "is this divergence signal trustworthy?" — typically
	// require SuccessCount >= 2 before raising flags.divergence_warning.
	SuccessCount int

	// FailureCount is len(Failures). Combined with SuccessCount,
	// the operator can see the full reference universe outcome.
	FailureCount int
}

// CompareOptions tunes [Compare]'s behaviour.
type CompareOptions struct {
	// PerReferenceTimeout caps the time spent on each reference's
	// LookupPrice call. Default 5s. Set lower for hot-path use
	// where divergence is best-effort.
	PerReferenceTimeout time.Duration

	// MinSuccessForMedian — Compare returns a zero Median when
	// fewer than this many references returned successfully.
	// Default 1 (any reference is better than none); operators
	// can require 2+ for stronger consensus.
	MinSuccessForMedian int
}

// Compare gathers prices from each reference in parallel and
// computes the divergence between ourPrice and the median of the
// successful responses.
//
// Behaviour:
//
//   - Each reference runs in its own goroutine with the per-
//     reference timeout from opts.
//   - References returning [ErrAssetUnsupported] are recorded in
//     Failures with a stable label (operator can decide whether
//     that asset belongs in the source's supported set).
//   - References returning [ErrPriceUnavailable] are recorded the
//     same way; the divergence threshold logic distinguishes
//     "this source is down" from "we disagree with this source".
//   - Other errors (network timeout, panic recovered, etc.) are
//     recorded in Failures with the verbatim error message.
//
// The aggregator's caller-side gating logic typically reads:
//
//	res := divergence.Compare(ctx, refs, pair, ourPrice, ts, opts)
//	if res.SuccessCount >= 2 && res.DivergencePct > threshold {
//	    flags.DivergenceWarning = true
//	}
func Compare(
	ctx context.Context,
	refs []Reference,
	pair canonical.Pair,
	ourPrice float64,
	observedAt time.Time,
	opts CompareOptions,
) Result {
	if opts.PerReferenceTimeout <= 0 {
		opts.PerReferenceTimeout = 5 * time.Second
	}
	if opts.MinSuccessForMedian <= 0 {
		opts.MinSuccessForMedian = 1
	}

	res := Result{
		Pair:     pair,
		OurPrice: ourPrice,
		Sources:  map[string]float64{},
		Failures: map[string]string{},
	}

	if len(refs) == 0 {
		return res
	}

	type fetchOutcome struct {
		name  string
		price float64
		err   error
	}
	results := make(chan fetchOutcome, len(refs))

	var wg sync.WaitGroup
	for _, ref := range refs {
		wg.Add(1)
		go func(r Reference) {
			defer wg.Done()
			// Capture the reference's name once up-front so we can
			// report the failure even if r.Name() itself is what
			// panics (cheap defence; references are operator-supplied
			// in some deployments).
			name := safeName(r)

			// Panic-recover the LookupPrice call. Per Compare's
			// docstring, "panic recovered" failures are recorded in
			// Failures the same way as a normal error — operators
			// see "this reference is broken" without losing the
			// other references' results. A misbehaving reference
			// MUST NOT take the whole comparison run down.
			defer func() {
				if rv := recover(); rv != nil {
					// Best-effort send; channel is buffered to len(refs)
					// so this is a non-blocking write.
					results <- fetchOutcome{
						name: name,
						err:  fmt.Errorf("reference panicked: %v", rv),
					}
				}
			}()

			perCtx, cancel := context.WithTimeout(ctx, opts.PerReferenceTimeout)
			defer cancel()
			price, err := r.LookupPrice(perCtx, pair, observedAt)
			results <- fetchOutcome{name: name, price: price, err: err}
		}(ref)
	}
	wg.Wait()
	close(results)

	prices := make([]float64, 0, len(refs))
	for o := range results {
		if o.err != nil {
			res.Failures[o.name] = classifyError(o.err)
			continue
		}
		if !isFinitePositive(o.price) {
			res.Failures[o.name] = "non-positive or non-finite price"
			continue
		}
		res.Sources[o.name] = o.price
		prices = append(prices, o.price)
	}
	res.SuccessCount = len(res.Sources)
	res.FailureCount = len(res.Failures)

	if res.SuccessCount < opts.MinSuccessForMedian {
		return res
	}
	sort.Float64s(prices)
	res.Median = median(prices)
	if res.Median > 0 {
		res.DivergencePct = math.Abs(ourPrice-res.Median) / res.Median * 100.0
	}
	return res
}

// classifyError maps known sentinels to short stable labels so the
// operator dashboard can distinguish "asset not on this source"
// from "transport failure". Panics surface as "panicked: <text>"
// (the safeName-wrapped goroutine recovers + wraps via
// `fmt.Errorf("reference panicked: %v", rv)`); we strip the
// "reference " prefix here so the dashboard label stays compact.
// Unknown errors pass through verbatim.
func classifyError(err error) string {
	switch {
	case errors.Is(err, ErrAssetUnsupported):
		return "asset_unsupported"
	case errors.Is(err, ErrPriceUnavailable):
		return "price_unavailable"
	default:
		// Drop the "reference panicked:" prefix when the underlying
		// goroutine wrapped a panic — operator-facing label reads
		// "panicked: foo" rather than "reference panicked: foo".
		msg := err.Error()
		const prefix = "reference panicked: "
		if strings.HasPrefix(msg, prefix) {
			return "panicked: " + msg[len(prefix):]
		}
		return msg
	}
}

// safeName returns r.Name() with a panic-recover guard so a
// misbehaving reference can't take down the wrapping goroutine
// before we even reach LookupPrice. Returns "_unknown" when
// Name() panics — the operator-facing failure label still
// surfaces but tied to a synthetic name. Real production
// references never panic from Name() (the function returns a
// constant string), but operator-supplied custom references
// might.
func safeName(r Reference) (out string) {
	defer func() {
		if rv := recover(); rv != nil {
			out = "_unknown"
		}
	}()
	return r.Name()
}

// isFinitePositive guards against +/-Inf and NaN making it through
// JSON parsing of upstream responses, plus zero/negative prices.
// Any of those land us in the failure bucket.
func isFinitePositive(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x > 0
}

// median returns the median of a sorted slice. For even-length
// slices it returns the arithmetic mean of the two middle values.
// Caller MUST sort the slice ascending; we don't sort here so the
// median computation is O(1) for the comparator's hot path.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2.0
}
