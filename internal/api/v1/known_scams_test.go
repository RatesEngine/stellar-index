package v1

import (
	"strings"
	"testing"
)

// TestScamReason_KnownEntries — every entry in scamIssuers must
// resolve to a non-empty Reason via scamReason. Catches accidental
// regressions where someone adds a key without a value (or vice
// versa) when extending the curated list.
func TestScamReason_KnownEntries(t *testing.T) {
	if len(scamIssuers) == 0 {
		t.Fatal("scamIssuers is empty — bootstrap entry GBYBVW…GUARD missing")
	}
	for g, entry := range scamIssuers {
		got := scamReason(g)
		if got == "" {
			t.Errorf("scamReason(%q) returned empty; map entry has Reason=%q", g, entry.Reason)
		}
		if got != entry.Reason {
			t.Errorf("scamReason(%q) = %q; want %q", g, got, entry.Reason)
		}
	}
}

// TestScamReason_Unknown — non-flagged G-strkeys MUST return empty.
// Callers (issuers.go) treat empty as "not flagged"; a stray
// non-empty default would falsely warn on legitimate issuers.
func TestScamReason_Unknown(t *testing.T) {
	cases := []string{
		// Real, legitimate issuers that should NEVER trip the flag.
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", // Circle USDC
		"GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA", // Aquarius
		// Empty + obviously-malformed.
		"",
		"not-a-strkey",
		"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, g := range cases {
		if got := scamReason(g); got != "" {
			t.Errorf("scamReason(%q) = %q; want \"\"", g, got)
		}
	}
}

// TestScamIssuers_GStrkeyShape — guards against typos / pasted
// trailing whitespace in the curated list. Each entry's key must
// be a 56-char G-strkey starting with 'G'.
func TestScamIssuers_GStrkeyShape(t *testing.T) {
	for g := range scamIssuers {
		if len(g) != 56 {
			t.Errorf("scamIssuers key %q: length %d, want 56", g, len(g))
		}
		if !strings.HasPrefix(g, "G") {
			t.Errorf("scamIssuers key %q: must start with 'G'", g)
		}
	}
}

// TestScamIssuers_ReasonsCiteSource — every Reason must mention its
// curation source. Today the only sourcing path is stellar.expert's
// directory, so `(stellar.expert)` is required. If we add a second
// curation source later, broaden this check.
func TestScamIssuers_ReasonsCiteSource(t *testing.T) {
	for g, entry := range scamIssuers {
		if !strings.Contains(entry.Reason, "stellar.expert") {
			t.Errorf("scamIssuers[%s].Reason = %q: missing source citation", g, entry.Reason)
		}
	}
}
