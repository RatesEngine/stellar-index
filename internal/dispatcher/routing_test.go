package dispatcher_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
)

// TestEndToEndRouting_withRealFixtures wires all four source
// Decoders into one Dispatcher, then feeds every captured mainnet
// fixture through Dispatcher.Route and verifies the right
// decoder claimed each event.
//
// This is the integration test for PR 165b — proves the
// Decoder interface + per-source adapters correctly route real
// bytes without duplication or loss. End-to-end ledger-meta →
// dispatcher wiring is in PR 165d.
func TestEndToEndRouting_withRealFixtures(t *testing.T) {
	disp := dispatcher.New(
		reflector.NewDecoder(reflector.VariantDEX,
			"CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M"),
		reflector.NewDecoder(reflector.VariantCEX,
			"CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN"),
		reflector.NewDecoder(reflector.VariantFX,
			"CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC"),
		soroswapDecoderWithStubbedTokens(t),
		aquarius.NewDecoder(),
		phoenix.NewDecoder(),
	)

	// Per-source expectations:
	//   reflector/DEX  → AssetSoroban in the update asset slot.
	//   reflector/CEX  → AssetCrypto (per PR 164e).
	//   reflector/FX   → AssetFiat.
	//   soroswap       → stubbed tokens → Trade with native base / classic quote.
	//   aquarius       → one Trade per event.
	//   phoenix        → 8 events → one Trade on the 8th, nil on 1-7.
	// Accounting: events-seen-per-source, outputs-emitted-per-
	// source. Sources with correlation state (soroswap, phoenix)
	// emit FEWER outputs than events seen — that's the point of
	// the buffer. The assertion below verifies the ratios per
	// source instead of insisting on one-output-per-event.
	eventsSeen := map[string]int{}    // by fixture dir
	outputsByName := map[string]int{} // by emitted canonical.*.Source

	for _, source := range []string{"reflector", "aquarius", "soroswap", "phoenix"} {
		files := orderedFixturesForReplay(t, source, findFixtures(t, source))
		for _, fixPath := range files {
			evs := loadEventsFromFixture(t, source, fixPath)
			eventsSeen[source] += len(evs)
			for _, ev := range evs {
				outs, err := disp.Route(ev)
				if err != nil {
					t.Logf("%s/%s: decode error: %v", source, filepath.Base(fixPath), err)
					continue
				}
				for _, o := range outs {
					outputsByName[o.Source()]++
					if err := validateOutput(o); err != nil {
						t.Errorf("%s/%s: output validation: %v",
							source, filepath.Base(fixPath), err)
					}
				}
			}
		}
	}

	// Per-source output-count expectations, derived from known
	// fixture counts. These double as a drift canary: if someone
	// adds fixtures the ratios shift and the assertion nudges them
	// to update the expected counts here.
	//   reflector: each fixture's event yields MANY OracleUpdates
	//     (one per asset in the update_data vector) — so we assert
	//     on minimums, not exact.
	//   aquarius: 1 trade per fixture event.
	//   soroswap: 1 trade per (swap + sync) pair, so outputs =
	//     events / 2.
	//   phoenix: 1 trade per (8-event) fixture group.
	if got := outputsByName["aquarius"]; got != eventsSeen["aquarius"] {
		t.Errorf("aquarius: got %d outputs for %d events; want 1:1",
			got, eventsSeen["aquarius"])
	}
	if got, want := outputsByName["soroswap"], eventsSeen["soroswap"]/2; got != want {
		t.Errorf("soroswap: got %d outputs for %d events; want %d (swap+sync pairs)",
			got, eventsSeen["soroswap"], want)
	}
	if got, want := outputsByName["phoenix"], eventsSeen["phoenix"]/8; got != want {
		t.Errorf("phoenix: got %d outputs for %d events; want %d (8-event groups)",
			got, eventsSeen["phoenix"], want)
	}
	// Reflector: at least one update per fixture event (could be
	// many more — one per asset in the price vector).
	totalReflector := outputsByName["reflector-dex"] +
		outputsByName["reflector-cex"] + outputsByName["reflector-fx"]
	if totalReflector < eventsSeen["reflector"] {
		t.Errorf("reflector: only %d outputs from %d events — expected ≥1 per event",
			totalReflector, eventsSeen["reflector"])
	}

	// Dispatch statistics sanity: UnmatchedHits should be zero.
	// Every fixture comes from one of the four sources we registered;
	// none should fall through unclaimed.
	if got := disp.Stats().UnmatchedHits; got != 0 {
		t.Errorf("UnmatchedHits = %d, want 0 — some real fixture didn't match any decoder", got)
	}

	t.Logf("events seen: %+v  outputs emitted: %+v", eventsSeen, outputsByName)
	if len(outputsByName) == 0 {
		t.Skip("no fixtures present — run scripts/dev/capture-*-fixtures.sh")
	}
}

// validateOutput is a shared sanity check on the canonical payload
// each consumer.Event wraps. All we need at the dispatch layer is
// that the source name stamps right and the identity fields are
// non-zero; per-source decode-correctness is covered in each
// source's real_fixture_test.go.
func validateOutput(o interface{}) error {
	switch e := o.(type) {
	case soroswap.TradeEvent:
		if e.Trade.Source != soroswap.SourceName {
			return missing("Trade.Source", e.Trade.Source)
		}
		if e.Trade.Ledger == 0 {
			return missing("Trade.Ledger", e.Trade.Ledger)
		}
	case aquarius.TradeEvent:
		if e.Trade.Source != aquarius.SourceName {
			return missing("Trade.Source", e.Trade.Source)
		}
	case phoenix.TradeEvent:
		if e.Trade.Source != phoenix.SourceName {
			return missing("Trade.Source", e.Trade.Source)
		}
	case reflector.UpdateEvent:
		if !strings.HasPrefix(e.Update.Source, "reflector-") {
			return missing("Update.Source", e.Update.Source)
		}
		if e.Update.Asset.IsZero() {
			return missing("Update.Asset", "zero")
		}
	default:
		return &mismatch{what: "unknown output type"}
	}
	return nil
}

type mismatch struct{ what string }

func (m *mismatch) Error() string { return m.what }
func missing(name string, got any) error {
	return &mismatch{what: name + "=" + fmtAny(got)}
}

func fmtAny(v any) string {
	if s, ok := v.(string); ok {
		return "\"" + s + "\""
	}
	return "(non-string)"
}

// soroswapDecoderWithStubbedTokens pre-seeds a Soroswap Decoder's
// pair registry with stub tokens for every Soroswap fixture. The
// real decoder flow learns these from factory new_pair events;
// fixtures don't include those, so we seed directly.
func soroswapDecoderWithStubbedTokens(t *testing.T) *soroswap.Decoder {
	t.Helper()
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}
	d := soroswap.NewDecoder()
	for _, fixPath := range findFixtures(t, "soroswap") {
		raw, err := os.ReadFile(fixPath)
		if err != nil {
			continue
		}
		var fx struct {
			ContractID string `json:"contract_id"`
		}
		_ = json.Unmarshal(raw, &fx)
		if fx.ContractID != "" {
			d.SeedPair(fx.ContractID, xlm, usdc)
		}
	}
	return d
}

// orderedFixturesForReplay reorders fixture paths so the replay
// matches real on-chain ordering — critical for sources with
// correlation buffers. Alphabetical ordering of os.ReadDir groups
// all swap_* files before any sync_* file, which spans wall-clock
// gaps larger than the buffer's orphan-eviction maxAge (5 min)
// and strands every swap before its sync arrives.
//
// On-chain, swap and sync of the same trade land in the same tx
// within seconds; the dispatcher sees them adjacent. This helper
// simulates that by pairing swap_* with sync_* of the same tx_hash
// prefix and yielding them consecutively.
//
// For sources without correlation buffers (reflector / aquarius /
// phoenix), this is a no-op — alphabetical order is fine.
func orderedFixturesForReplay(t *testing.T, source string, files []string) []string {
	t.Helper()
	if source != "soroswap" {
		return files
	}
	// Group by "<ledger>_<tx12>" suffix; emit swap then sync.
	type pair struct{ swap, sync string }
	groups := map[string]*pair{}
	order := []string{}
	for _, f := range files {
		base := filepath.Base(f)
		var key, kind string
		switch {
		case strings.HasPrefix(base, "swap_"):
			key = strings.TrimSuffix(strings.TrimPrefix(base, "swap_"), ".json")
			kind = "swap"
		case strings.HasPrefix(base, "sync_"):
			key = strings.TrimSuffix(strings.TrimPrefix(base, "sync_"), ".json")
			kind = "sync"
		default:
			continue
		}
		if groups[key] == nil {
			groups[key] = &pair{}
			order = append(order, key)
		}
		if kind == "swap" {
			groups[key].swap = f
		} else {
			groups[key].sync = f
		}
	}
	var out []string
	for _, k := range order {
		p := groups[k]
		if p.swap != "" {
			out = append(out, p.swap)
		}
		if p.sync != "" {
			out = append(out, p.sync)
		}
	}
	return out
}

// findFixtures returns every *.json path under
// test/fixtures/<source>/<wasm_hash>/.
func findFixtures(t *testing.T, source string) []string {
	t.Helper()
	root := filepath.Join("..", "..", "test", "fixtures", source)
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				out = append(out, filepath.Join(root, d.Name(), f.Name()))
			}
		}
	}
	return out
}

// loadEventsFromFixture normalizes the four sources' JSON schemas
// into events.Event values. Reflector / Aquarius / Soroswap are
// one-event-per-file; Phoenix is an 8-event group.
func loadEventsFromFixture(t *testing.T, source, path string) []events.Event {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if source == "phoenix" {
		var fx struct {
			ContractID     string `json:"contract_id"`
			Ledger         uint32 `json:"ledger"`
			TxHash         string `json:"tx_hash"`
			OpIndex        int    `json:"op_index"`
			LedgerClosedAt string `json:"ledger_closed_at"`
			Events         []struct {
				Topics []string `json:"topics"`
				Value  string   `json:"value"`
			} `json:"events"`
		}
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("unmarshal phoenix fixture: %v", err)
		}
		out := make([]events.Event, len(fx.Events))
		for i, ev := range fx.Events {
			out[i] = events.Event{
				Type:           "contract",
				ContractID:     fx.ContractID,
				Ledger:         fx.Ledger,
				TxHash:         fx.TxHash,
				OperationIndex: fx.OpIndex,
				LedgerClosedAt: fx.LedgerClosedAt,
				Topic:          ev.Topics,
				Value:          ev.Value,
			}
		}
		return out
	}

	var fx struct {
		ContractID     string   `json:"contract_id"`
		Ledger         uint32   `json:"ledger"`
		TxHash         string   `json:"tx_hash"`
		LedgerClosedAt string   `json:"ledger_closed_at"`
		Topics         []string `json:"topics"`
		Value          string   `json:"value"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal %s fixture: %v", source, err)
	}
	if fx.LedgerClosedAt == "" {
		fx.LedgerClosedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return []events.Event{{
		Type:           "contract",
		ContractID:     fx.ContractID,
		Ledger:         fx.Ledger,
		TxHash:         fx.TxHash,
		LedgerClosedAt: fx.LedgerClosedAt,
		Topic:          fx.Topics,
		Value:          fx.Value,
	}}
}
