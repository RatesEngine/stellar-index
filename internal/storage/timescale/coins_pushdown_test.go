package timescale

import (
	"strings"
	"testing"
)

// TestListCoinsBaseSelectSQL_NoPushdown verifies that the renderer
// without a pushdown predicate strips the marker comments and
// returns SQL that does NOT contain the chosen_assets CTE. This is
// the equivalence path for unfiltered LIST queries.
func TestListCoinsBaseSelectSQL_NoPushdown(t *testing.T) {
	t.Parallel()
	sql := listCoinsBaseSelectSQL("")
	if strings.Contains(sql, "/*PUSHDOWN_BASE*/") {
		t.Error("PUSHDOWN_BASE markers must be stripped when no pushdown predicate")
	}
	if strings.Contains(sql, "/*PUSHDOWN_QUOTE*/") {
		t.Error("PUSHDOWN_QUOTE markers must be stripped when no pushdown predicate")
	}
	if strings.Contains(sql, "chosen_assets") {
		t.Error("chosen_assets CTE must NOT appear without pushdown predicate")
	}
}

// TestListCoinsBaseSelectSQL_WithPushdown verifies the pushdown
// branch: chosen_assets CTE prepended, markers replaced with
// per-asset IN-clauses, original CTE structure preserved.
func TestListCoinsBaseSelectSQL_WithPushdown(t *testing.T) {
	t.Parallel()
	sql := listCoinsBaseSelectSQL("issuer_g_strkey = $1")

	if !strings.Contains(sql, "WITH chosen_assets AS (SELECT asset_id FROM classic_assets WHERE issuer_g_strkey = $1)") {
		t.Errorf("missing chosen_assets CTE in pushdown SQL; got:\n%s", sql[:500])
	}
	if strings.Contains(sql, "/*PUSHDOWN_BASE*/") || strings.Contains(sql, "/*PUSHDOWN_QUOTE*/") {
		t.Error("PUSHDOWN markers must be replaced (not left as raw comments) when pushdown active")
	}
	// All 9 per-asset CTEs should receive the base-side filter:
	// per_asset_24h_vol (base side), direct_usd, direct_usd_1h/24h/7d,
	// asset_vs_xlm, asset_vs_xlm_1h/24h/7d = 9 total. per_asset_24h_vol
	// has a SECOND row for the quote side via PUSHDOWN_QUOTE, but the
	// base count is 9.
	baseFilter := "AND base_asset IN (SELECT asset_id FROM chosen_assets)"
	if got := strings.Count(sql, baseFilter); got != 9 {
		t.Errorf("expected 9 base-side pushdown filters, got %d", got)
	}
	quoteFilter := "AND quote_asset IN (SELECT asset_id FROM chosen_assets)"
	if got := strings.Count(sql, quoteFilter); got != 1 {
		t.Errorf("expected 1 quote-side pushdown filter (per_asset_24h_vol UNION's quote branch), got %d", got)
	}
	// xlm_usd CTEs must NOT receive the pushdown — they look up XLM
	// specifically, not the caller-supplied asset. There are 4 of them.
	xlmUSDCount := strings.Count(sql, "xlm_usd AS (") +
		strings.Count(sql, "xlm_usd_1h AS (") +
		strings.Count(sql, "xlm_usd_24h AS (") +
		strings.Count(sql, "xlm_usd_7d AS (")
	if xlmUSDCount != 4 {
		t.Errorf("expected 4 xlm_usd CTEs unaffected by pushdown; got %d", xlmUSDCount)
	}
}

// TestBuildCoinsQuery_NoIssuer_NoPushdown verifies the existing
// unfiltered path is unchanged: no chosen_assets CTE, no pushdown
// markers in the output, same arg shape.
func TestBuildCoinsQuery_NoIssuer_NoPushdown(t *testing.T) {
	t.Parallel()
	sql, args := buildCoinsQuery(100, "", "", "", CoinsOrderObservationCountDesc)
	if strings.Contains(sql, "chosen_assets") {
		t.Error("no-filter query must not include chosen_assets CTE")
	}
	if strings.Contains(sql, "PUSHDOWN") {
		t.Error("no-filter query must not have raw PUSHDOWN markers")
	}
	if len(args) != 1 || args[0] != 100 {
		t.Errorf("expected args=[100] (just LIMIT); got %v", args)
	}
}

// TestBuildCoinsQuery_IssuerFilter_PushdownActive verifies that an
// issuer filter activates the pushdown: chosen_assets is emitted,
// the issuer arg is bound to $1, the outer WHERE still references
// the same arg.
func TestBuildCoinsQuery_IssuerFilter_PushdownActive(t *testing.T) {
	t.Parallel()
	issuer := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	sql, args := buildCoinsQuery(100, issuer, "", "", CoinsOrderObservationCountDesc)

	if !strings.Contains(sql, "WITH chosen_assets AS (SELECT asset_id FROM classic_assets WHERE issuer_g_strkey = $1)") {
		t.Errorf("issuer-filter query must inject chosen_assets CTE referencing $1; got first 600 chars:\n%s", sql[:600])
	}
	if !strings.Contains(sql, "ca.issuer_g_strkey = $1") {
		t.Error("issuer-filter query must keep the outer WHERE on ca.issuer_g_strkey")
	}
	if len(args) != 2 || args[0] != issuer || args[1] != 100 {
		t.Errorf("expected args=[issuer, limit]; got %v", args)
	}
}

// TestBuildCoinsQuery_QFilter_NoPushdown verifies that a q-only
// search does NOT activate pushdown (q-pushdown is intentionally
// deferred per the comment in buildCoinsQuery).
func TestBuildCoinsQuery_QFilter_NoPushdown(t *testing.T) {
	t.Parallel()
	sql, _ := buildCoinsQuery(50, "", "", "USDC", CoinsOrderObservationCountDesc)
	if strings.Contains(sql, "chosen_assets") {
		t.Error("q-only filter must NOT activate pushdown (deferred per buildCoinsQuery comment)")
	}
}

// TestBuildCoinsQuery_IssuerAndQ_PushdownOnIssuer verifies that
// when BOTH issuer and q are set, pushdown still activates (the
// issuer arm is still the dominant narrowing) but the LIKE pattern
// stays in the outer WHERE.
func TestBuildCoinsQuery_IssuerAndQ_PushdownOnIssuer(t *testing.T) {
	t.Parallel()
	issuer := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	sql, args := buildCoinsQuery(100, issuer, "", "USD", CoinsOrderObservationCountDesc)
	if !strings.Contains(sql, "WITH chosen_assets AS") {
		t.Error("issuer-set query must activate pushdown even when q is also set")
	}
	if !strings.Contains(sql, "LIKE LOWER($2)") {
		t.Error("q-LIKE pattern must use $2 when issuer is $1")
	}
	if len(args) != 3 || args[0] != issuer || args[1] != "%USD%" || args[2] != 100 {
		t.Errorf("expected args=[issuer, %%USD%%, 100]; got %v", args)
	}
}
