package forex

import (
	"bufio"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// circulationCSV is the curated monetary-base table joined onto
// /v1/currencies. Lives in repo at data/currencies/circulation.csv.
// Refresh cadence: quarterly, one CSV row per central-bank
// publication. The file's header comments document the methodology.
//
//go:embed circulation_data.csv
var circulationCSV string

// CirculationEntry is one row from the curated table — the broad
// money supply figure and the date the central bank stamped on
// the underlying release. Float64 is fine for display; precision
// loss above 2^53 isn't a concern (the largest ticker is JPY at
// ~1.5e15, well inside float64's safe-integer range; we accept
// 1-unit rounding for the wire shape).
type CirculationEntry struct {
	AggregateLocalUnits float64   // M2 / M3 / M4 in the currency's natural unit
	AsOf                time.Time // Data vintage stamped on the source release
	Source              string    // Series identifier — opaque to callers, useful for audit
}

// loadCirculationTable parses the embedded CSV at startup.
// Returns a lower-case-ticker → entry map. Malformed lines are
// skipped with a warning baked into the returned error string;
// callers (Worker) log it and proceed with whatever parsed cleanly.
func loadCirculationTable() (map[string]CirculationEntry, error) {
	out := map[string]CirculationEntry{}
	if circulationCSV == "" {
		return out, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(circulationCSV))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lineNo := 0
	var headerSeen bool
	var skipped []string
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if !headerSeen {
			// First non-comment, non-blank line is the header — skip
			// it without trying to parse as data.
			headerSeen = true
			continue
		}
		fields := strings.Split(raw, ",")
		if len(fields) < 4 {
			skipped = append(skipped, fmt.Sprintf("line %d: want 4 fields, got %d", lineNo, len(fields)))
			continue
		}
		ticker := strings.ToLower(strings.TrimSpace(fields[0]))
		amountStr := strings.TrimSpace(fields[1])
		asOfStr := strings.TrimSpace(fields[2])
		source := strings.TrimSpace(fields[3])
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("line %d ticker=%q: %v", lineNo, ticker, err))
			continue
		}
		asOf, err := time.Parse("2006-01-02", asOfStr)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("line %d ticker=%q: %v", lineNo, ticker, err))
			continue
		}
		out[ticker] = CirculationEntry{
			AggregateLocalUnits: amount,
			AsOf:                asOf,
			Source:              source,
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan circulation csv: %w", err)
	}
	if len(skipped) > 0 {
		// Non-fatal — caller logs. We still return the rows that did
		// parse so /v1/currencies degrades gracefully on a typo.
		return out, fmt.Errorf("skipped %d rows: %s", len(skipped), strings.Join(skipped, "; "))
	}
	return out, nil
}
