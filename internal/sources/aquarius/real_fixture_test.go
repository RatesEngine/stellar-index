package aquarius

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Replay real Aquarius trade fixtures captured from mainnet
// into decodeTrade. This is the end-to-end harness that proves the
// real SCVal decoder path works against production wire format.
//
// Fixtures live at test/fixtures/aquarius/<wasm_hash>/trade_*.json,
// one file per captured event. Each fixture's topics + value are
// untouched base64 SCVals as stellar-rpc returned them.

type aquariusFixture struct {
	ContractID     string   `json:"contract_id"`
	WasmHash       string   `json:"wasm_hash"`
	Ledger         uint32   `json:"ledger"`
	TxHash         string   `json:"tx_hash"`
	LedgerClosedAt string   `json:"ledger_closed_at"`
	Topics         []string `json:"topics"`
	Value          string   `json:"value"`
	EventName      string   `json:"event_name"`
}

func TestRealMainnetFixtures_aquarius(t *testing.T) {
	root := filepath.Join("..", "..", "..", "test", "fixtures", "aquarius")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("fixtures root unreadable: %v", err)
	}

	total := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		wasmHash := d.Name()
		dir := filepath.Join(root, wasmHash)
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			total++
			t.Run(wasmHash+"/"+f.Name(), func(t *testing.T) {
				runAquariusFixture(t, filepath.Join(dir, f.Name()))
			})
		}
	}
	if total == 0 {
		t.Skip("no fixtures present — run scripts/dev/capture-aquarius-fixtures.sh")
	}
}

func runAquariusFixture(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var fx aquariusFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	e := &events.Event{
		Type:           "contract",
		ContractID:     fx.ContractID,
		Ledger:         fx.Ledger,
		TxHash:         fx.TxHash,
		LedgerClosedAt: fx.LedgerClosedAt,
		Topic:          fx.Topics,
		Value:          fx.Value,
	}

	// Classify-check first — if the real topic bytes don't match
	// our TopicSymbolTrade constant, the whole decoder is dead in
	// the water before we even look at the body.
	if got := classify(e); got != EventTrade {
		t.Fatalf("classify real event = %q, want %q (wrong topic[0] bytes?)", got, EventTrade)
	}

	closedAt, err := time.Parse(time.RFC3339, fx.LedgerClosedAt)
	if err != nil {
		t.Fatalf("parse ledger_closed_at: %v", err)
	}

	tr, err := decodeTrade(e, closedAt)
	if err != nil {
		t.Fatalf("decodeTrade: %v\nfixture: %s", err, path)
	}
	if tr.BaseAmount.Sign() <= 0 {
		t.Errorf("base amount not positive: %s", tr.BaseAmount)
	}
	if tr.QuoteAmount.Sign() <= 0 {
		t.Errorf("quote amount not positive: %s", tr.QuoteAmount)
	}
	if tr.Pair.Base.Type != canonical.AssetSoroban {
		t.Errorf("base asset type = %q, want soroban", tr.Pair.Base.Type)
	}
	if tr.Pair.Quote.Type != canonical.AssetSoroban {
		t.Errorf("quote asset type = %q, want soroban", tr.Pair.Quote.Type)
	}

	// Sanity: i128 amounts should fit in 2^100 for realistic tokens.
	// Anything bigger almost certainly means we mis-aligned hi/lo
	// halves during the i128 decode.
	maxSane := new(big.Int).Lsh(big.NewInt(1), 100)
	if tr.BaseAmount.BigInt().Cmp(maxSane) > 0 {
		t.Errorf("base unreasonably large — i128 misalign? %s", tr.BaseAmount)
	}
	if tr.QuoteAmount.BigInt().Cmp(maxSane) > 0 {
		t.Errorf("quote unreasonably large — i128 misalign? %s", tr.QuoteAmount)
	}

	// Timestamp passes through from closedAt unchanged — Aquarius
	// events don't carry their own timestamp (unlike Reflector).
	if !tr.Timestamp.Equal(closedAt) {
		t.Errorf("timestamp = %v, want %v (ledger close)", tr.Timestamp, closedAt)
	}
}
