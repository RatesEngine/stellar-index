package supply

import (
	"context"
	"math/big"
	"strings"
	"testing"
)

func TestConfigReserveBalanceReader_HappyPath(t *testing.T) {
	r, err := NewConfigReserveBalanceReader(map[string]string{
		"GA1": "100",
		"GA2": "200",
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	got, err := r.ReserveBalanceTotal(context.Background(), []string{"GA1", "GA2"}, 0)
	if err != nil {
		t.Fatalf("ReserveBalanceTotal: %v", err)
	}
	if got.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("total=%s want 300", got.String())
	}
}

func TestConfigReserveBalanceReader_LargeStroops(t *testing.T) {
	// 5e9 XLM × 1e7 stroops/XLM = 5e16, well above int64 range.
	want, _ := new(big.Int).SetString("50000000000000000", 10)
	r, err := NewConfigReserveBalanceReader(map[string]string{
		"GA1": "50000000000000000",
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	got, err := r.ReserveBalanceTotal(context.Background(), []string{"GA1"}, 0)
	if err != nil {
		t.Fatalf("ReserveBalanceTotal: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Errorf("total=%s want %s", got, want)
	}
}

func TestConfigReserveBalanceReader_RejectsEmptyKey(t *testing.T) {
	_, err := NewConfigReserveBalanceReader(map[string]string{
		"": "100",
	})
	if err == nil {
		t.Fatal("want error on empty account key")
	}
}

func TestConfigReserveBalanceReader_RejectsMalformed(t *testing.T) {
	_, err := NewConfigReserveBalanceReader(map[string]string{
		"GA1": "not-a-number",
	})
	if err == nil {
		t.Fatal("want error on non-decimal balance")
	}
	if !strings.Contains(err.Error(), "GA1") {
		t.Errorf("err=%v should mention the failing account", err)
	}
}

func TestConfigReserveBalanceReader_RejectsNegative(t *testing.T) {
	_, err := NewConfigReserveBalanceReader(map[string]string{
		"GA1": "-100",
	})
	if err == nil {
		t.Fatal("want error on negative balance")
	}
}

// TestConfigReserveBalanceReader_MissingAccountErrors — silently
// treating an unknown account as zero would yield an over-stated
// circulating supply (the exact failure mode ADR-0011 prohibits).
// The reader must error.
func TestConfigReserveBalanceReader_MissingAccountErrors(t *testing.T) {
	r, err := NewConfigReserveBalanceReader(map[string]string{
		"GA1": "100",
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	_, err = r.ReserveBalanceTotal(context.Background(), []string{"GA1", "GA_UNKNOWN"}, 0)
	if err == nil {
		t.Fatal("want error when account missing from config")
	}
	if !strings.Contains(err.Error(), "GA_UNKNOWN") {
		t.Errorf("err=%v should name the missing account", err)
	}
}

// TestConfigReserveBalanceReader_EmptyConstructionAllowed — an empty
// balance map is a legal config (operator hasn't enumerated any
// reserves yet). The reader returns 0 for any empty account list.
func TestConfigReserveBalanceReader_EmptyConstructionAllowed(t *testing.T) {
	r, err := NewConfigReserveBalanceReader(map[string]string{})
	if err != nil {
		t.Fatalf("construct empty: %v", err)
	}
	got, err := r.ReserveBalanceTotal(context.Background(), nil, 0)
	if err != nil {
		t.Fatalf("ReserveBalanceTotal nil accounts: %v", err)
	}
	if got.Sign() != 0 {
		t.Errorf("empty config + empty accounts = %s, want 0", got)
	}
}
