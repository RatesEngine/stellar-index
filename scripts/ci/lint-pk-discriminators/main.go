// lint-pk-discriminators enforces ADR-0033's anti-data-loss rule: every
// protocol-row hypertable — a table that stores one row per emitted
// contract event / trade — MUST key on a PER-EVENT discriminator, so that
// when a single operation emits several rows of the same kind (a path-payment
// crossing multiple offers, a Blend action touching several positions, a
// multi-feed oracle push) they don't collide on the primary key and get
// silently dropped by ON CONFLICT DO NOTHING.
//
// The canonical discriminator is `event_index` (the contract event's index
// within its transaction). A table may instead use an equally-unique
// alternative (e.g. (asset, user_address) for blend_positions) — those are
// recorded in the allow map below WITH A REASON. A protocol-row table that
// has neither fails the lint.
//
// This is the static, PR-time complement to the data-driven guard
// (compute-completeness's aggregate reconcile, run on a timer): the reconcile
// catches a collision in production as a projection delta; this catches a new
// table that would introduce one before it ships.
//
// Usage: go run ./scripts/ci/lint-pk-discriminators
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// protocolRowTables are the hypertables that store one row per emitted
// event/trade. Adding a new such table here is the point: it forces a
// discriminator decision. Pure observation/registry/aggregate tables are not
// listed (they key on the observed entity, not per-event).
var protocolRowTables = []string{
	"trades",
	"oracle_updates",
	"blend_positions", "blend_emissions", "blend_admin", "blend_auctions",
	"phoenix_liquidity", "phoenix_stake_events",
	"comet_liquidity",
	"soroswap_skim_events", "soroswap_router_swaps",
	"cctp_events", "rozo_events",
	"defindex_flows",
	"sep41_supply_events", "sep41_transfers",
}

// allow maps a protocol-row table to the reason its PK is collision-free
// WITHOUT event_index. Two flavours:
//   - "OK:"   a genuine alternative discriminator — permanent.
//   - "TODO:" a known coarse-PK data-loss bug being fixed (remove the entry
//     when the fix lands; the lint then enforces event_index).
var allow = map[string]string{
	"oracle_updates":        "OK: decoders fan out op_index per feed; (source,ledger,tx,op,ts) is unique per update",
	"cctp_events":           "OK: (contract_id,…,event_type,ts) — one event per (type,ts) per op",
	"rozo_events":           "OK: (contract_id,…,event_type,ts) — one event per (type,ts) per op",
	"blend_positions":       "OK: (asset,user_address) added to PK in migration 0053 — distinct positions never collide",
	"blend_auctions":        "OK: auction events are one-per-(ledger,tx,op); legacy table",
	"comet_liquidity":       "OK: (event_kind, token) distinguishes per-token liquidity changes; completeness reconcile Δ=0",
	"sep41_supply_events":   "OK: supply-observer snapshots keyed by observed_at, not per-event; SEP-41 observer not enabled (CLAUDE.md)",
	"trades":                "OK: op_index is FANNED (canonical.FanoutOpIndex: opIndex<<16|event_index; SDEX opIdx*1024+claim_index) — encodes the per-trade discriminator, so multi-trade ops never collide",
	"phoenix_liquidity":     "OK: (op_index, action) distinguishes provide/withdraw per op; completeness reconcile Δ=0",
	"phoenix_stake_events":  "OK: (op_index, action) distinguishes bond/unbond per op; completeness reconcile Δ=0",
	"soroswap_router_swaps": "TODO: PK (ledger_close_time,ledger,tx,op) — no per-route discriminator; verify multi-route ops. Tracked.",
	"defindex_flows":        "TODO: PK keys on `layer` not event_index — verify Δ=70 source. Tracked.",
}

var (
	stmtSplit    = regexp.MustCompile(`;`)
	ws           = regexp.MustCompile(`\s+`)
	lineComment  = regexp.MustCompile(`--[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	createRe     = regexp.MustCompile(`(?i)CREATE TABLE\s+(?:IF NOT EXISTS\s+)?([a-z0-9_]+)`)
	alterRe      = regexp.MustCompile(`(?i)ALTER TABLE\s+(?:ONLY\s+)?([a-z0-9_]+)`)
	pkRe         = regexp.MustCompile(`(?i)PRIMARY KEY\s*\(([^)]+)\)`)
)

// parsePKs returns the latest PRIMARY KEY column list per table across the
// given migration files (sorted ascending so later definitions win).
func parsePKs(entries []string) (map[string]string, error) {
	pk := map[string]string{}
	for _, f := range entries {
		b, rerr := os.ReadFile(f) //nolint:gosec // f comes from a fixed Glob of migrations/, not user input
		if rerr != nil {
			return nil, fmt.Errorf("read %s: %w", f, rerr)
		}
		// Strip comments first — a `;` inside a comment would otherwise split a
		// CREATE statement away from its PRIMARY KEY clause.
		content := blockComment.ReplaceAllString(string(b), "")
		content = lineComment.ReplaceAllString(content, "")
		for _, stmt := range stmtSplit.Split(content, -1) {
			norm := ws.ReplaceAllString(stmt, " ")
			m := pkRe.FindStringSubmatch(norm)
			if m == nil {
				continue
			}
			var table string
			if cm := createRe.FindStringSubmatch(norm); cm != nil {
				table = cm[1]
			} else if am := alterRe.FindStringSubmatch(norm); am != nil {
				table = am[1]
			} else {
				continue
			}
			pk[strings.ToLower(table)] = strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	return pk, nil
}

func main() {
	root := "migrations"
	entries, err := filepath.Glob(filepath.Join(root, "*.up.sql"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "lint-pk-discriminators: no migrations found under %s/\n", root)
		os.Exit(2)
	}
	sort.Strings(entries) // 0001 < 0002 < … so later PK definitions win

	pk, perr := parsePKs(entries)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "lint-pk-discriminators: %v\n", perr)
		os.Exit(2)
	}

	var failures, stale []string
	for _, t := range protocolRowTables {
		cols, ok := pk[t]
		if !ok {
			failures = append(failures, fmt.Sprintf("%-22s no PRIMARY KEY found in migrations (typo in the table list?)", t))
			continue
		}
		hasEventIndex := strings.Contains(cols, "event_index")
		reason, allowed := allow[t]
		switch {
		case hasEventIndex && allowed && strings.HasPrefix(reason, "TODO"):
			// Fixed but still listed — nudge to tidy the allowlist.
			stale = append(stale, fmt.Sprintf("%-22s now has event_index — remove its TODO allowlist entry", t))
		case hasEventIndex:
			// good
		case allowed:
			// justified alternative or tracked TODO — fine for now
		default:
			failures = append(failures, fmt.Sprintf("%-22s PK (%s) lacks event_index and has no allowlist entry — add a per-event discriminator or document why it's collision-free", t, cols))
		}
	}

	if len(stale) > 0 {
		fmt.Println("lint-pk-discriminators: allowlist drift (non-fatal):")
		for _, s := range stale {
			fmt.Println("  •", s)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "lint-pk-discriminators: FAIL — protocol-row tables without a per-event PK discriminator:")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "  ✗", f)
		}
		fmt.Fprintln(os.Stderr, "\nSee ADR-0033 / the coarse-PK data-loss class. Either add event_index to the PK")
		fmt.Fprintln(os.Stderr, "or, if the PK is genuinely collision-free, add the table to `allow` with an OK: reason.")
		os.Exit(1)
	}
	fmt.Printf("lint-pk-discriminators: OK — %d protocol-row tables checked; all key on a per-event discriminator (or justified)\n", len(protocolRowTables))
}
