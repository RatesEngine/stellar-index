package config

import "testing"

// TestMetadataConfig_HomeDomainFor covers the curated-lookup helper
// the API binary uses to populate AssetDetail.HomeDomain. Three
// edge cases worth pinning:
//
//  1. nil/empty map → ("", false) for any issuer.
//  2. unknown issuer → ("", false).
//  3. empty-string entry → ("", false) — falsy entries are
//     treated as un-curated rather than asserting an empty domain.
func TestMetadataConfig_HomeDomainFor(t *testing.T) {
	t.Run("nil map yields not-found", func(t *testing.T) {
		var m MetadataConfig
		hd, ok := m.HomeDomainFor("GA5Z…")
		if ok || hd != "" {
			t.Errorf("nil map: got (%q, %v), want (\"\", false)", hd, ok)
		}
	})

	t.Run("empty map yields not-found", func(t *testing.T) {
		m := MetadataConfig{IssuerHomeDomains: map[string]string{}}
		hd, ok := m.HomeDomainFor("GA5Z…")
		if ok || hd != "" {
			t.Errorf("empty map: got (%q, %v), want (\"\", false)", hd, ok)
		}
	})

	t.Run("unknown issuer yields not-found", func(t *testing.T) {
		m := MetadataConfig{IssuerHomeDomains: map[string]string{
			"GA5Z…": "centre.io",
		}}
		hd, ok := m.HomeDomainFor("GDHU…")
		if ok || hd != "" {
			t.Errorf("unknown issuer: got (%q, %v), want (\"\", false)", hd, ok)
		}
	})

	t.Run("empty-string entry treated as not-curated", func(t *testing.T) {
		m := MetadataConfig{IssuerHomeDomains: map[string]string{
			"GA5Z…": "",
		}}
		hd, ok := m.HomeDomainFor("GA5Z…")
		if ok || hd != "" {
			t.Errorf("empty entry: got (%q, %v), want (\"\", false) — operator typos shouldn't assert metadata", hd, ok)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		m := MetadataConfig{IssuerHomeDomains: map[string]string{
			"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN": "centre.io",
		}}
		hd, ok := m.HomeDomainFor("GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
		if !ok || hd != "centre.io" {
			t.Errorf("got (%q, %v), want (\"centre.io\", true)", hd, ok)
		}
	})
}
