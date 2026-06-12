package v1_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// stubChangeSummaryReader is the in-memory test seam.
type stubChangeSummaryReader struct {
	row timescale.ChangeSummaryRow
	err error

	lastEntityType, lastEntityID string
}

func (r *stubChangeSummaryReader) GetChangeSummary(_ context.Context, entityType, entityID string) (timescale.ChangeSummaryRow, error) {
	r.lastEntityType = entityType
	r.lastEntityID = entityID
	if r.err != nil {
		return timescale.ChangeSummaryRow{}, r.err
	}
	return r.row, nil
}

// TestHandleChangeSummary_503WhenReaderNil — feature-gated reader.
// Returns 503 with `change-summary-unavailable` so the explorer
// can hide the change-summary panel rather than render zeroes.
func TestHandleChangeSummary_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/coin/XLM")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "change-summary-unavailable") {
		t.Errorf("expected error type tag in body: %s", body)
	}
}

// TestHandleChangeSummary_InvalidEntityType400 — reject the
// {coin,protocol,pair,source} alternatives anywhere upstream of
// the storage layer. Mirrors the CHECK constraint on
// change_summary_5m so an operator typo gets a clean 400.
func TestHandleChangeSummary_InvalidEntityType400(t *testing.T) {
	srv := v1.New(v1.Options{ChangeSummary: &stubChangeSummaryReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/banana/XLM")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "invalid-entity-type") {
		t.Errorf("expected invalid-entity-type tag: %s", body)
	}
}

// TestHandleChangeSummary_NotFound404 — the worker hasn't computed
// a row yet (or the entity was added after the last refresh).
// Surfaces as 404 so the explorer renders an empty state rather
// than a confusing 500.
func TestHandleChangeSummary_NotFound404(t *testing.T) {
	reader := &stubChangeSummaryReader{err: sql.ErrNoRows}
	srv := v1.New(v1.Options{ChangeSummary: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/coin/XLM")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "change-summary-not-found") {
		t.Errorf("expected change-summary-not-found tag: %s", body)
	}
}

// TestHandleChangeSummary_HappyPath_Coin — full row decode pin.
// All four time-window slots populate; ATH/ATL with At fields
// formatted as RFC3339; nullable pointer fields surface verbatim.
func TestHandleChangeSummary_HappyPath_Coin(t *testing.T) {
	athAt := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	atlAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h1, h24, d7, d30 := 0.165, 0.158, 0.150, 0.142
	hd1, hd24, dd7, dd30 := 1.5, 5.2, 10.1, 16.7
	ath, atl := 1.03, 0.10
	streakDays := 3

	reader := &stubChangeSummaryReader{
		row: timescale.ChangeSummaryRow{
			EntityType:      "coin",
			EntityID:        "XLM",
			RefreshedAt:     time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC),
			CurrentValue:    0.1675,
			H1Value:         &h1,
			H1DeltaPct:      &hd1,
			H24Value:        &h24,
			H24DeltaPct:     &hd24,
			D7Value:         &d7,
			D7DeltaPct:      &dd7,
			D30Value:        &d30,
			D30DeltaPct:     &dd30,
			ATHValue:        &ath,
			ATHAt:           &athAt,
			ATLValue:        &atl,
			ATLAt:           &atlAt,
			StreakDirection: "up",
			StreakDays:      &streakDays,
			Acceleration:    "steady",
		},
	}
	srv := v1.New(v1.Options{ChangeSummary: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/coin/XLM")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ChangeSummaryResponse `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	d := env.Data
	if d.EntityType != "coin" || d.EntityID != "XLM" {
		t.Errorf("entity = (%q, %q)", d.EntityType, d.EntityID)
	}
	if d.CurrentValue != 0.1675 {
		t.Errorf("CurrentValue = %v", d.CurrentValue)
	}
	if d.H24DeltaPct == nil || *d.H24DeltaPct != 5.2 {
		t.Errorf("H24DeltaPct = %v, want 5.2", d.H24DeltaPct)
	}
	if d.ATHValue == nil || *d.ATHValue != 1.03 {
		t.Errorf("ATHValue = %v", d.ATHValue)
	}
	if d.ATHAt != "2026-05-04T12:00:00Z" {
		t.Errorf("ATHAt = %q (RFC3339 format pin)", d.ATHAt)
	}
	if d.StreakDirection != "up" || d.StreakDays == nil || *d.StreakDays != 3 {
		t.Errorf("streak = (%q, %v)", d.StreakDirection, d.StreakDays)
	}

	// Verify the handler threaded entity_type + id correctly to the
	// storage layer (regression: a swap would render the wrong row).
	if reader.lastEntityType != "coin" || reader.lastEntityID != "XLM" {
		t.Errorf("reader saw (%q, %q), want (coin, XLM)", reader.lastEntityType, reader.lastEntityID)
	}
}

// TestHandleChangeSummary_NullableFieldsOmitted — a young entity
// with <1h of history has every window pointer NULL. omitempty
// drops them from the wire so the explorer can branch on absence.
func TestHandleChangeSummary_NullableFieldsOmitted(t *testing.T) {
	reader := &stubChangeSummaryReader{
		row: timescale.ChangeSummaryRow{
			EntityType:   "coin",
			EntityID:     "FRESH",
			RefreshedAt:  time.Now().UTC(),
			CurrentValue: 1.0,
			// H1/H24/D7/D30/ATH/ATL all nil — fresh asset
		},
	}
	srv := v1.New(v1.Options{ChangeSummary: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/coin/FRESH")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, forbidden := range []string{
		`"h1_delta_pct"`,
		`"h24_delta_pct"`,
		`"d7_delta_pct"`,
		`"d30_delta_pct"`,
		`"ath_value"`,
		`"ath_at"`,
		`"atl_value"`,
		`"atl_at"`,
		`"streak_days"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body should NOT contain %q (omitempty broken): %s", forbidden, body)
		}
	}
}

// TestHandleChangeSummary_ReaderError500 — unwrapped storage error
// surfaces as 500 with `change-summary-error`. Distinct from
// sql.ErrNoRows (which is the 404 path).
func TestHandleChangeSummary_ReaderError500(t *testing.T) {
	reader := &stubChangeSummaryReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{ChangeSummary: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/changes/coin/XLM")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "change-summary-error") {
		t.Errorf("expected change-summary-error tag: %s", body)
	}
}
