package redstone

import (
	"strings"
	"testing"
	"time"
)

// ─── pickTimestamp ─────────────────────────────────────────────

func TestPickTimestamp_zeroFallsBackToClosedAt(t *testing.T) {
	closedAt := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	got := pickTimestamp(0, closedAt)
	if !got.Equal(closedAt) {
		t.Errorf("got %v, want %v (closedAt fallback)", got, closedAt)
	}
}

func TestPickTimestamp_packageMsPreferred(t *testing.T) {
	// packageMs is the contract-declared package timestamp in
	// milliseconds; non-zero values must be honoured even when
	// closedAt is wildly later.
	closedAt := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	pkgMs := uint64(1_700_000_000_000) // 2023-11-14T22:13:20Z
	got := pickTimestamp(pkgMs, closedAt)
	want := time.UnixMilli(1_700_000_000_000).UTC()
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPickTimestamp_resultIsUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no timezone data")
	}
	got := pickTimestamp(0, time.Date(2026, 4, 26, 12, 0, 0, 0, loc))
	if got.Location() != time.UTC {
		t.Errorf("Location() = %v, want UTC", got.Location())
	}
}

// ─── feedIDsFromOpArgs ─────────────────────────────────────────

func TestFeedIDsFromOpArgs_tooFewArgs(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		args := make([]string, n)
		_, _, err := feedIDsFromOpArgs(args)
		if err == nil {
			t.Errorf("expected error for arity %d, got nil", n)
		}
		if !strings.Contains(err.Error(), "arity") {
			t.Errorf("error %q missing \"arity\" fragment", err.Error())
		}
	}
}

func TestFeedIDsFromOpArgs_invalidUpdaterArg(t *testing.T) {
	// args[0] is supposed to be a base64-encoded SCVal::Address.
	// Garbage triggers the args[0]-tagged error.
	args := []string{"not-base64", "ignored", "ignored"}
	_, _, err := feedIDsFromOpArgs(args)
	if err == nil {
		t.Fatal("expected error on malformed args[0], got nil")
	}
	if !strings.Contains(err.Error(), "args[0]") {
		t.Errorf("error %q missing \"args[0]\" fragment", err.Error())
	}
}

func TestFeedIDsFromOpArgs_invalidFeedsArg(t *testing.T) {
	// args[0] is a valid SCVal::Address (the relayer), args[1] is
	// junk → we should see an args[1]-tagged error.
	args := []string{
		encodeAddressArg(t, relayerG),
		"not-base64",
		"ignored",
	}
	_, _, err := feedIDsFromOpArgs(args)
	if err == nil {
		t.Fatal("expected error on malformed args[1], got nil")
	}
	if !strings.Contains(err.Error(), "args[1]") {
		t.Errorf("error %q missing \"args[1]\" fragment", err.Error())
	}
}

func TestFeedIDsFromOpArgs_happyPath(t *testing.T) {
	want := []string{"BTC", "ETH"}
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, want),
		encodePayloadArg(t),
	}
	feedIDs, updater, err := feedIDsFromOpArgs(args)
	if err != nil {
		t.Fatalf("feedIDsFromOpArgs: %v", err)
	}
	if updater != relayerG {
		t.Errorf("updater = %q, want %q", updater, relayerG)
	}
	if len(feedIDs) != 2 || feedIDs[0] != "BTC" || feedIDs[1] != "ETH" {
		t.Errorf("feedIDs = %v, want %v", feedIDs, want)
	}
}
