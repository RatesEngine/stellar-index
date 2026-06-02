package completeness

import (
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// fakeRecognizer recognizes events by contract id (Reconstruct copies
// Row.ContractID onto the event), so the test controls which samples
// are "handled".
type fakeRecognizer struct{ known map[string]bool }

func (f fakeRecognizer) Recognize(ev events.Event) (string, bool) {
	if f.known[ev.ContractID] {
		return "decoder", true
	}
	return "", false
}

func validRow(contractID, sym string) sorobanevents.Row {
	return sorobanevents.Row{
		Ledger:          60_000_000,
		LedgerCloseTime: time.Unix(0, 0).UTC(),
		TxHash:          make([]byte, 32),
		ContractID:      contractID,
		ContractIDHex:   make([]byte, 32),
		TopicCount:      1,
		Topic0Sym:       sym,
		Topic0XDR:       []byte{0x00, 0x00, 0x00, 0x01},
		BodyXDR:         []byte{0x00, 0x00, 0x00, 0x00},
	}
}

func TestAuditRecognition(t *testing.T) {
	samples := []timescale.TopicSample{
		{Row: validRow("CONTRACT_A", "swap"), Count: 10, MinLedger: 60_000_000, MaxLedger: 60_000_100},
		{Row: validRow("CONTRACT_B", "mystery"), Count: 3, MinLedger: 60_000_050, MaxLedger: 60_000_090},
		// Empty ContractID → Reconstruct fails → reported as a gap.
		{Row: validRow("", "broken"), Count: 1, MinLedger: 60_000_010, MaxLedger: 60_000_010},
	}
	rec := fakeRecognizer{known: map[string]bool{"CONTRACT_A": true}}

	gaps := AuditRecognition(samples, rec)
	if len(gaps) != 2 {
		t.Fatalf("got %d gaps, want 2: %+v", len(gaps), gaps)
	}

	var sawMystery, sawBroken bool
	for _, g := range gaps {
		switch g.ContractID {
		case "CONTRACT_B":
			sawMystery = true
			if g.Topic0Sym != "mystery" || g.Count != 3 || g.Reason != "no decoder matches" {
				t.Errorf("CONTRACT_B gap = %+v, want topic=mystery count=3 reason='no decoder matches'", g)
			}
		case "":
			sawBroken = true
			if !strings.HasPrefix(g.Reason, "unreconstructable") {
				t.Errorf("empty-contract gap reason = %q, want 'unreconstructable…'", g.Reason)
			}
		default:
			t.Errorf("unexpected gap for recognized contract: %+v", g)
		}
	}
	if !sawMystery || !sawBroken {
		t.Errorf("expected both the unrecognized + unreconstructable gaps (mystery=%v broken=%v)", sawMystery, sawBroken)
	}
}
