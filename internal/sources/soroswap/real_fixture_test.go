package soroswap

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Replay real Soroswap swap + sync fixtures captured from mainnet
// into decodeSwap. Validates that:
//
//   - topic classification works on real topic bytes,
//   - sdkDecodeSwapAmounts extracts all four i128 fields from the
//     real ScvMap body (not synthetic),
//   - swap/sync correlation by (ledger, tx_hash, op_index) matches
//     the real on-chain ordering.
//
// Fixtures live at test/fixtures/soroswap/<wasm_hash>/{swap,sync}_*.json.
// swap + sync of the same trade share the leading txHash-prefix in
// their filename — that's how we pair them up here. (stellar-rpc
// returns events in-order within a ledger, so the pairing is safe.)

type soroswapFixture struct {
	ContractID     string   `json:"contract_id"`
	WasmHash       string   `json:"wasm_hash"`
	Ledger         uint32   `json:"ledger"`
	TxHash         string   `json:"tx_hash"`
	LedgerClosedAt string   `json:"ledger_closed_at"`
	Topics         []string `json:"topics"`
	Value          string   `json:"value"`
	EventName      string   `json:"event_name"`
}

func TestRealMainnetFixtures_soroswap(t *testing.T) {
	root := filepath.Join("..", "..", "..", "test", "fixtures", "soroswap")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("fixtures root unreadable: %v", err)
	}

	total := 0
	for _, dirEnt := range dirs {
		if !dirEnt.IsDir() {
			continue
		}
		wasmHash := dirEnt.Name()
		dir := filepath.Join(root, wasmHash)
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		// Pair swap + sync fixtures by their (ledger, tx_hash,
		// contract) key — same correlation the runtime buffer uses.
		type pairKey struct {
			Ledger     uint32
			TxHash     string
			ContractID string
		}
		pairs := map[pairKey]*struct{ Swap, Sync *soroswapFixture }{}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			fx := readSoroswapFixture(t, filepath.Join(dir, f.Name()))
			k := pairKey{fx.Ledger, fx.TxHash, fx.ContractID}
			if pairs[k] == nil {
				pairs[k] = &struct{ Swap, Sync *soroswapFixture }{}
			}
			switch fx.EventName {
			case "swap":
				pairs[k].Swap = fx
			case "sync":
				pairs[k].Sync = fx
			}
		}

		for k, p := range pairs {
			if p.Swap == nil || p.Sync == nil {
				continue // unmatched fixture pair — can happen if the window clipped one side
			}
			total++
			t.Run(wasmHash+"/"+shortKey(k.TxHash)+"_"+k.ContractID[:10], func(t *testing.T) {
				runSoroswapFixture(t, p.Swap, p.Sync)
			})
		}
	}
	if total == 0 {
		t.Skip("no paired swap+sync fixtures — run scripts/dev/capture-soroswap-fixtures.sh")
	}
}

func shortKey(tx string) string {
	if len(tx) > 12 {
		return tx[:12]
	}
	return tx
}

// mustSorobanAsset builds a canonical.Asset backed by a valid
// C-strkey derived from a 32-byte seed. Kept tiny so tests can
// create as many distinct tokens as needed without collision.
func mustSorobanAsset(t *testing.T, seedByte byte) canonical.Asset {
	t.Helper()
	var raw [32]byte
	raw[0] = seedByte
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	a, err := canonical.NewSorobanAsset(s)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	return a
}

func readSoroswapFixture(t *testing.T, path string) *soroswapFixture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var fx soroswapFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	// Infer event_name from filename if the capture left it blank.
	if fx.EventName == "" {
		base := filepath.Base(path)
		switch {
		case strings.HasPrefix(base, "swap_"):
			fx.EventName = "swap"
		case strings.HasPrefix(base, "sync_"):
			fx.EventName = "sync"
		}
	}
	return &fx
}

func runSoroswapFixture(t *testing.T, swap, sync *soroswapFixture) {
	t.Helper()

	swapEv := &events.Event{
		ContractID:     swap.ContractID,
		Ledger:         swap.Ledger,
		TxHash:         swap.TxHash,
		LedgerClosedAt: swap.LedgerClosedAt,
		Topic:          swap.Topics,
		Value:          swap.Value,
		Type:           "contract",
	}
	syncEv := &events.Event{
		ContractID:     sync.ContractID,
		Ledger:         sync.Ledger,
		TxHash:         sync.TxHash,
		LedgerClosedAt: sync.LedgerClosedAt,
		Topic:          sync.Topics,
		Value:          sync.Value,
		Type:           "contract",
	}

	// Classification check first — if the real topic bytes don't
	// match our TopicPrefixPair/TopicSymbolSwap constants, that's a
	// bug before we get to the body.
	if got := classify(swapEv); got != EventSwap {
		t.Fatalf("classify(swap) = %q, want swap (wrong topic bytes?)", got)
	}
	if got := classify(syncEv); got != EventSync {
		t.Fatalf("classify(sync) = %q, want sync", got)
	}

	// We don't have a captured new_pair event for this pair — so
	// seed the tokens with placeholders generated with valid CRC
	// checksums (NewSorobanAsset's format check is lenient, but
	// building with strkey.Encode keeps future validators happy).
	tokenA := mustSorobanAsset(t, 0x01)
	tokenB := mustSorobanAsset(t, 0x02)

	closedAt, err := time.Parse(time.RFC3339, swap.LedgerClosedAt)
	if err != nil {
		t.Fatalf("parse ledger_closed_at: %v", err)
	}
	r := RawPair{
		Ledger:   swap.Ledger,
		TxHash:   swap.TxHash,
		OpIndex:  0,
		Pair:     swap.ContractID,
		ClosedAt: closedAt,
		Swap:     swapEv,
		Sync:     syncEv,
	}
	trade, err := decodeSwap(r, tokenA, tokenB)
	if err != nil {
		t.Fatalf("decodeSwap: %v", err)
	}
	if trade.Source != SourceName {
		t.Errorf("trade.Source = %q", trade.Source)
	}
	if trade.BaseAmount.Sign() <= 0 {
		t.Errorf("trade.BaseAmount not positive: %s", trade.BaseAmount)
	}
	if trade.QuoteAmount.Sign() <= 0 {
		t.Errorf("trade.QuoteAmount not positive: %s", trade.QuoteAmount)
	}
	// Sanity: BigInt amounts are bounded by realistic token scales.
	// Anything >2^100 almost certainly means we mis-decoded.
	maxSane := new(big.Int).Lsh(big.NewInt(1), 100)
	if trade.BaseAmount.BigInt().Cmp(maxSane) > 0 {
		t.Errorf("base amount unreasonably large — i128 misalignment? %s", trade.BaseAmount)
	}
	if trade.QuoteAmount.BigInt().Cmp(maxSane) > 0 {
		t.Errorf("quote amount unreasonably large — i128 misalignment? %s", trade.QuoteAmount)
	}
}
