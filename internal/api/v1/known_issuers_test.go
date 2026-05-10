package v1

import (
	"strings"
	"testing"
)

// TestEnrichIssuer_DBWins — when both home_domain and org_name come
// from the DB, the curated map MUST NOT override. The DB is the
// source of truth (operator's `ratesengine-ops sep1-refresh` cron
// reflects current SEP-1 state); the static map is only a fallback
// for issuers that haven't been resolved yet.
func TestEnrichIssuer_DBWins(t *testing.T) {
	usdc := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	domain, org := enrichIssuer(usdc, "centre.io.fresh", "Circle Fresh")
	if domain != "centre.io.fresh" {
		t.Errorf("domain = %q; want centre.io.fresh (DB value should win)", domain)
	}
	if org != "Circle Fresh" {
		t.Errorf("org = %q; want \"Circle Fresh\" (DB value should win)", org)
	}
}

// TestEnrichIssuer_FallbackOnEmpty — the curated map fills in only
// when the DB column is empty. Tests both fields independently.
func TestEnrichIssuer_FallbackOnEmpty(t *testing.T) {
	usdc := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	domain, org := enrichIssuer(usdc, "", "")
	if domain != "centre.io" {
		t.Errorf("empty/empty: domain = %q; want centre.io", domain)
	}
	if org != "Circle" {
		t.Errorf("empty/empty: org = %q; want Circle", org)
	}

	// Partial — DB has org but not domain.
	domain, org = enrichIssuer(usdc, "", "Circle Custom")
	if domain != "centre.io" {
		t.Errorf("partial: domain = %q; want centre.io (filled from map)", domain)
	}
	if org != "Circle Custom" {
		t.Errorf("partial: org = %q; want Circle Custom (DB value preserved)", org)
	}
}

// TestEnrichIssuer_UnknownPassthrough — a G-strkey not in the
// curated map returns whatever the DB had, even if empty. The
// caller then renders empty as a truncated G-strkey.
func TestEnrichIssuer_UnknownPassthrough(t *testing.T) {
	unknown := "GUNKNOWNAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	domain, org := enrichIssuer(unknown, "", "")
	if domain != "" || org != "" {
		t.Errorf("unknown returned (%q, %q); want (\"\", \"\")", domain, org)
	}
}

// TestKnownIssuers_GStrkeyShape — every key is a 56-char G-strkey
// starting with 'G'. Catches pasted whitespace / partial copies.
func TestKnownIssuers_GStrkeyShape(t *testing.T) {
	if len(knownIssuers) == 0 {
		t.Fatal("knownIssuers is empty — bootstrap entries (Circle USDC etc.) missing")
	}
	for g := range knownIssuers {
		if len(g) != 56 {
			t.Errorf("knownIssuers key %q: length %d, want 56", g, len(g))
		}
		if !strings.HasPrefix(g, "G") {
			t.Errorf("knownIssuers key %q: must start with 'G'", g)
		}
	}
}

// TestKnownIssuers_AllFieldsPopulated — every entry has BOTH
// home_domain and org_name. A half-populated entry is a curation
// bug — the whole point is to fill in for the explorer.
func TestKnownIssuers_AllFieldsPopulated(t *testing.T) {
	for g, entry := range knownIssuers {
		if entry.HomeDomain == "" {
			t.Errorf("knownIssuers[%s]: HomeDomain is empty", g)
		}
		if entry.OrgName == "" {
			t.Errorf("knownIssuers[%s]: OrgName is empty", g)
		}
	}
}

// TestKnownIssuers_NoOverlapWithScams — a G-strkey that's both
// "known legitimate" AND "known scam" is a curation contradiction.
// The scam path takes precedence in the API surface but the
// contradictory state shouldn't exist in the source.
func TestKnownIssuers_NoOverlapWithScams(t *testing.T) {
	for g := range knownIssuers {
		if _, scam := scamIssuers[g]; scam {
			t.Errorf("G-strkey %s is in BOTH knownIssuers AND scamIssuers — pick one", g)
		}
	}
}
