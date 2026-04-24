package reflector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// TestRealMainnetFixtures replays every captured fixture in
// test/fixtures/reflector/<wasm_hash>/*.json through decodeUpdate.
// This is the regression harness that makes sure a contract upgrade
// doesn't silently break the decoder (and that a decoder change
// doesn't silently stop matching real events).
//
// Per docs/architecture/contract-schema-evolution.md each
// <wasm_hash>/ directory carries fixtures captured under one
// specific contract WASM. As we add decoder variants per hash, the
// dispatcher here grows.
func TestRealMainnetFixtures(t *testing.T) {
	fixRoot := filepath.Join("..", "..", "..", "test", "fixtures", "reflector")
	entries, err := os.ReadDir(fixRoot)
	if err != nil {
		t.Skipf("fixtures root missing or unreadable: %v", err)
	}

	totalFiles := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		wasmHash := ent.Name()
		dir := filepath.Join(fixRoot, wasmHash)
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			totalFiles++
			t.Run(wasmHash+"/"+f.Name(), func(t *testing.T) {
				runOneFixture(t, filepath.Join(dir, f.Name()))
			})
		}
	}
	if totalFiles == 0 {
		t.Skip("no fixtures present — run scripts/dev/capture-reflector-fixtures.sh")
	}
}

type fixtureFile struct {
	ContractID     string   `json:"contract_id"`
	WasmHash       string   `json:"wasm_hash"`
	Ledger         uint32   `json:"ledger"`
	TxHash         string   `json:"tx_hash"`
	LedgerClosedAt string   `json:"ledger_closed_at"`
	Topics         []string `json:"topics"`
	Value          string   `json:"value"`
}

func runOneFixture(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	e := &events.Event{
		ContractID:     fx.ContractID,
		Ledger:         fx.Ledger,
		TxHash:         fx.TxHash,
		LedgerClosedAt: fx.LedgerClosedAt,
		Topic:          fx.Topics,
		Value:          fx.Value,
		Type:           "contract",
	}
	closedAt, err := time.Parse(time.RFC3339, fx.LedgerClosedAt)
	if err != nil {
		t.Fatalf("bad ledger_closed_at: %v", err)
	}

	// Variant selection for now is heuristic — match contract ID
	// against the three known mainnet Reflector instances.
	variant := variantForContract(fx.ContractID)

	updates, err := decodeUpdate(e, variant, DefaultDecimals, "", closedAt)
	if err != nil {
		t.Fatalf("decodeUpdate: %v\nfixture: %s", err, path)
	}
	if len(updates) == 0 {
		t.Fatalf("zero updates from a real Reflector event; fixture: %s", path)
	}

	// PR 164e verification: CEX-oracle updates must decode to
	// AssetCrypto (Asset::Other(Symbol) → ADR-0014). FX oracle
	// stays on AssetFiat. DEX stays on AssetSoroban.
	expectedAssetType := map[Variant]canonical.AssetType{
		VariantDEX: canonical.AssetSoroban,
		VariantCEX: canonical.AssetCrypto,
		VariantFX:  canonical.AssetFiat,
	}[variant]
	for i, u := range updates {
		if expectedAssetType != "" && u.Asset.Type != expectedAssetType {
			t.Errorf("updates[%d].Asset.Type = %q, want %q for %s variant",
				i, u.Asset.Type, expectedAssetType, variant.SourceName())
		}
	}

	// Timestamp must fall in a sane window around the ledger
	// close. Reflector resolution is 5 min; the oracle-emitted
	// timestamp is usually ≤ the close time but never in the future.
	for i, u := range updates {
		if u.Timestamp.IsZero() {
			t.Errorf("updates[%d].Timestamp zero", i)
		}
		skew := closedAt.Sub(u.Timestamp)
		// Real oracle timestamps lag close time by <= 10 min in
		// practice (5-min cadence + some buffer). A skew > 1h or
		// a future timestamp means we mis-decoded the unit.
		if skew > time.Hour || skew < -time.Minute {
			t.Errorf("updates[%d].Timestamp skew %v from close %v — unit bug?", i, skew, closedAt)
		}
		if u.Price.Sign() <= 0 {
			t.Errorf("updates[%d].Price = %s (non-positive)", i, u.Price)
		}
		if u.Asset.IsZero() {
			t.Errorf("updates[%d].Asset is zero", i)
		}
	}
}

// variantForContract maps a mainnet contract ID to its Variant.
// Until we have per-variant capture dirs, use the contract-ID
// lookup so the real-fixture test can run across all three
// Reflector feeds without operator intervention.
func variantForContract(contractID string) Variant {
	switch contractID {
	case "CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M":
		return VariantDEX
	case "CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN":
		return VariantCEX
	case "CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC":
		return VariantFX
	default:
		return VariantDEX
	}
}
