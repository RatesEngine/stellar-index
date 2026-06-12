package platform_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// TestSentinelErrors_AreDistinct pins that callers can
// errors.Is each sentinel without false positives. This is the
// contract the rest of the codebase relies on for routing
// errors back to HTTP statuses.
func TestSentinelErrors_AreDistinct(t *testing.T) {
	all := []error{
		platform.ErrNotFound,
		platform.ErrTokenExpired,
		platform.ErrConflict,
		platform.ErrAlreadyProcessed,
		platform.ErrLastOwner,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("%v errors.Is %v — sentinels not distinct", a, b)
			}
		}
	}
}

// TestSentinelErrors_WrapForRouting confirms wrapping with %w
// preserves errors.Is — the standard pattern stores use to
// surface store-layer failures up to handlers.
func TestSentinelErrors_WrapForRouting(t *testing.T) {
	wrapped := fmt.Errorf("get user by email: %w", platform.ErrNotFound)
	if !errors.Is(wrapped, platform.ErrNotFound) {
		t.Errorf("wrapped sentinel lost identity")
	}
	if errors.Is(wrapped, platform.ErrConflict) {
		t.Errorf("wrapped sentinel matched the wrong target")
	}
}
