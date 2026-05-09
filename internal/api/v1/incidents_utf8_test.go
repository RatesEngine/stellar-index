package v1

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSummaryFromMarkdown_UTF8SafeTruncation pins the rune-aware
// truncation path. A naive byte-slice at 397 would split a
// multi-byte codepoint in half — incident posts with accented
// characters (é/ñ/ü/…) would emit invalid UTF-8 to the atom feed,
// rejecting strict validators (W3C feedvalidator.org) and showing
// replacement characters in the explorer.
func TestSummaryFromMarkdown_UTF8SafeTruncation(t *testing.T) {
	// Engineer a paragraph where byte 397 falls in the middle of
	// an `é` codepoint (UTF-8 = 0xC3 0xA9, 2 bytes per rune).
	// 396 ASCII bytes + "ééé trailing" → 411 bytes total, with
	// rune boundaries at 396, 398, 400, 402, 403…
	body := strings.Repeat("a", 396) + "ééé trailing"
	out := summaryFromMarkdown(body)

	if !utf8.ValidString(out) {
		t.Fatalf("output is invalid UTF-8 (mid-rune slice): %q", out[len(out)-10:])
	}
	if !strings.HasSuffix(out, "...") {
		t.Errorf("output should end in ellipsis: %q", out[len(out)-10:])
	}
	// Conservative truncation may slice slightly earlier than 397
	// when 397 is mid-rune; accept anything from a few bytes shy
	// of 400 down to 380 (worst case for very wide codepoints
	// near the boundary). The "..." adds 3 bytes.
	if l := len(out); l > 400 || l < 380 {
		t.Errorf("output length = %d; expected 380..400 with rune-safe truncation", l)
	}
}

// TestSummaryFromMarkdown_UTF8WithEmoji — emoji are 4-byte UTF-8.
// Engineer a body where a 4-byte codepoint straddles byte 397 to
// confirm the truncation walks back to a rune-start byte.
func TestSummaryFromMarkdown_UTF8WithEmoji(t *testing.T) {
	// Place a 🎉 (4 bytes: 0xF0 0x9F 0x8E 0x89) at byte 395 so
	// bytes 395-398 are this rune; the naive slice at 397 would
	// land in the middle.
	body := strings.Repeat("a", 395) + "🎉" + strings.Repeat("b", 50)
	out := summaryFromMarkdown(body)

	if !utf8.ValidString(out) {
		t.Errorf("output is invalid UTF-8: %q", out[len(out)-15:])
	}
	if !strings.HasSuffix(out, "...") {
		t.Errorf("output should end in ellipsis: %q", out[len(out)-10:])
	}
}
