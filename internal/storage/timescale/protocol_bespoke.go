package timescale

import (
	"context"
	"fmt"
)

// BespokeBlock is the served-tier, per-category bespoke analytics for a
// protocol page (the v1 API maps it 1:1 to api/v1.ProtocolBespoke — timescale
// can't import v1). A generic container (headline KPIs + named time-series +
// named top-N tables) filled with content tailored to the protocol's category,
// so the UI renders the three shapes generically while the DATA is bespoke.
//
// All numeric values are PRE-FORMATTED strings here: the store owns formatting
// so amounts that exceed 2^53 stay exact (ADR-0003) and percentages/counts read
// the way the page shows them.
type BespokeBlock struct {
	Category string
	KPIs     []BespokeKPI
	Series   []BespokeSeries
	Tables   []BespokeTable
	Notes    []string
}

// BespokeKPI is one headline metric card.
type BespokeKPI struct {
	Label string
	Value string
	Unit  string
	Hint  string
}

// BespokeSeries is a named time-series for a chart.
type BespokeSeries struct {
	Name   string
	Unit   string
	Points []BespokeSeriesPt
}

// BespokeSeriesPt is one (date, value) point; Value is a numeric string.
type BespokeSeriesPt struct {
	Date  string
	Value string
}

// BespokeTable is a named top-N table — column headers + string rows.
type BespokeTable struct {
	Title   string
	Columns []string
	Rows    [][]string
}

// BuildProtocolBespoke assembles the bespoke block for source (the protocol
// name) given its category, over a trailing windowDays. Returns nil (not an
// error) for a category with no bespoke metrics yet, so the page degrades to
// its generic analytics. windowDays bounds every query to the ts-indexed recent
// window so the projected-table scans stay cheap.
func (s *Store) BuildProtocolBespoke(ctx context.Context, source, category string, windowDays int) (*BespokeBlock, error) {
	if windowDays <= 0 {
		windowDays = 90
	}
	switch category {
	case "bridge":
		return s.bespokeBridge(ctx, source, windowDays)
	}
	// dex / amm / lending / vault / oracle land here as they ship.
	return nil, nil
}

// bespokeBridge builds the bridge (CCTP / Rozo) bespoke block from cctp_events:
// total + daily cross-chain transfer volume and a by-destination-domain table.
// Reference implementation for the per-category pattern.
func (s *Store) bespokeBridge(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	// Only cctp_events carries the domain + amount shape today; rozo_events is
	// empty. Keep this scoped to cctp; other bridges return an empty block.
	if source != "cctp" {
		return &BespokeBlock{Category: "bridge"}, nil
	}
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "bridge",
		Notes: []string{
			"Volume is the summed CCTP event amount (USDC, 6-decimal units) over the window; deposit_for_burn + mint_and_withdraw legs both count.",
		},
	}

	// KPIs: total volume + transfer count over the window.
	var totalVol, txCount string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount),0)::text, count(*)::text
		FROM cctp_events WHERE ts > now() - $1::interval AND amount IS NOT NULL`, since).
		Scan(&totalVol, &txCount)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespokeBridge KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Transfer volume (%dd)", windowDays), Value: totalVol, Unit: "USDC-units", Hint: "summed event amount in 6-decimal USDC units"},
		BespokeKPI{Label: fmt.Sprintf("Transfers (%dd)", windowDays), Value: txCount},
	)

	// Daily volume series.
	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(date_trunc('day', ts), 'YYYY-MM-DD'), COALESCE(sum(amount),0)::text
		FROM cctp_events WHERE ts > now() - $1::interval AND amount IS NOT NULL
		GROUP BY 1 ORDER BY 1 ASC`, since)
	if err != nil {
		return nil, err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily transfer volume", Unit: "USDC-units", Points: series})
	}

	// By counterparty domain.
	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "By counterparty domain", Columns: []string{"Domain", "Transfers", "Volume (USDC-units)"}},
		`SELECT COALESCE(counterparty_domain::text,'—'), count(*)::text, COALESCE(sum(amount),0)::text
		   FROM cctp_events WHERE ts > now() - $1::interval
		  GROUP BY counterparty_domain ORDER BY count(*) DESC LIMIT 25`, since)
	if err != nil {
		return nil, err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}
	return blk, nil
}

// scanDailySeries runs a (date_text, value_text) query and returns the points.
func (s *Store) scanDailySeries(ctx context.Context, query string, args ...any) ([]BespokeSeriesPt, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespoke series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []BespokeSeriesPt
	for rows.Next() {
		var p BespokeSeriesPt
		if err := rows.Scan(&p.Date, &p.Value); err != nil {
			return nil, fmt.Errorf("timescale: bespoke series scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanTable runs a query whose columns match base.Columns and fills base.Rows
// (every value scanned as text). The header is taken from base.
func (s *Store) scanTable(ctx context.Context, base BespokeTable, query string, args ...any) (BespokeTable, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return base, fmt.Errorf("timescale: bespoke table %q: %w", base.Title, err)
	}
	defer func() { _ = rows.Close() }()
	n := len(base.Columns)
	for rows.Next() {
		cells := make([]string, n)
		ptrs := make([]any, n)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return base, fmt.Errorf("timescale: bespoke table %q scan: %w", base.Title, err)
		}
		base.Rows = append(base.Rows, cells)
	}
	return base, rows.Err()
}
