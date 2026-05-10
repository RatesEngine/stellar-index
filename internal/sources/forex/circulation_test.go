package forex

import (
	"strings"
	"testing"
	"time"
)

// TestLoadCirculationTable_ParsesCleanly — the embedded CSV must
// parse without skipped rows. A skipped row indicates a typo
// (malformed amount, bad date, missing field) — we don't want
// those silently shipping to /v1/currencies.
func TestLoadCirculationTable_ParsesCleanly(t *testing.T) {
	table, err := loadCirculationTable()
	if err != nil {
		t.Fatalf("loadCirculationTable: %v", err)
	}
	if len(table) == 0 {
		t.Fatal("circulation table is empty — bootstrap row USD missing")
	}
}

// TestLoadCirculationTable_MajorsPresent — guard the bootstrap set
// of currencies that >95% of fx volume settles in. Removing any of
// these from the CSV is almost certainly a mistake.
func TestLoadCirculationTable_MajorsPresent(t *testing.T) {
	table, err := loadCirculationTable()
	if err != nil {
		t.Fatalf("loadCirculationTable: %v", err)
	}
	for _, ticker := range []string{
		"usd", "eur", "jpy", "gbp", "chf", "cny", "cad", "aud",
		"nzd", "inr", "krw", "brl", "mxn", "rub", "zar",
	} {
		entry, ok := table[ticker]
		if !ok {
			t.Errorf("major missing: %s", ticker)
			continue
		}
		if entry.AggregateLocalUnits <= 0 {
			t.Errorf("%s: AggregateLocalUnits = %f, want > 0", ticker, entry.AggregateLocalUnits)
		}
		if entry.AsOf.IsZero() {
			t.Errorf("%s: AsOf is zero", ticker)
		}
		if entry.Source == "" {
			t.Errorf("%s: Source is empty", ticker)
		}
	}
}

// TestLoadCirculationTable_BroadCoverage — after the 2026-05-08 WB
// expansion the table covers ~106 of 110 Massive currencies. Lock
// the floor at 100 so a future curation regression that drops most
// of the WB rows fails the test.
func TestLoadCirculationTable_BroadCoverage(t *testing.T) {
	table, err := loadCirculationTable()
	if err != nil {
		t.Fatalf("loadCirculationTable: %v", err)
	}
	if got := len(table); got < 100 {
		t.Errorf("circulation table has %d entries; want at least 100 (WB expansion baseline)", got)
	}
}

// TestLoadCirculationTable_AsOfReasonable — `as_of` dates must
// not be in the future (typo guard), and not so far back as to be
// useless. The WB publishes some emerging-market series with a
// multi-year lag — a few rows are legitimately 5-7 years old. The
// 8-year floor catches genuine staleness (decade-old typo) without
// rejecting the WB-lag entries that are still better-than-null.
func TestLoadCirculationTable_AsOfReasonable(t *testing.T) {
	table, err := loadCirculationTable()
	if err != nil {
		t.Fatalf("loadCirculationTable: %v", err)
	}
	now := time.Now()
	floor := now.AddDate(-8, 0, 0)
	for ticker, entry := range table {
		if entry.AsOf.After(now) {
			t.Errorf("%s: as_of %s is in the future", ticker, entry.AsOf.Format("2006-01-02"))
		}
		if entry.AsOf.Before(floor) {
			t.Errorf("%s: as_of %s is more than 8 years stale", ticker, entry.AsOf.Format("2006-01-02"))
		}
	}
}

// TestLoadCirculationTable_SourcesCited — every row must name a
// source. Empty Source defeats the audit story (operator can't
// re-verify a row's provenance from the CSV alone).
func TestLoadCirculationTable_SourcesCited(t *testing.T) {
	table, err := loadCirculationTable()
	if err != nil {
		t.Fatalf("loadCirculationTable: %v", err)
	}
	for ticker, entry := range table {
		if strings.TrimSpace(entry.Source) == "" {
			t.Errorf("%s: source is blank", ticker)
		}
	}
}
