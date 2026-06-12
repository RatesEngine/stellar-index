package supply_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/supply"
)

// stubReader is a minimal [supply.ReserveBalanceReader] for tests.
// Returns balance unchanged for the supplied accounts; lets a single
// test fixture cover the success path. err controls the error path.
type stubReader struct {
	balance *big.Int
	err     error
	calls   int
	last    struct {
		accounts []string
		ledger   uint32
	}
}

func (s *stubReader) ReserveBalanceTotal(_ context.Context, accounts []string, ledger uint32) (*big.Int, error) {
	s.calls++
	s.last.accounts = accounts
	s.last.ledger = ledger
	if s.err != nil {
		return nil, s.err
	}
	if s.balance == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(s.balance), nil
}

// TestXLMTotalSupplyStroops_FrozenValue — the constant must match
// the network-frozen value to the stroop. Renaming or recomputing
// this is a wire break we'd reject in review — the test guards
// against an accidental nudge.
func TestXLMTotalSupplyStroops_FrozenValue(t *testing.T) {
	got := supply.XLMTotalSupplyStroops()
	want, _ := new(big.Int).SetString("500018068120000000", 10) // 50_001_806_812 × 10^7
	if got.Cmp(want) != 0 {
		t.Errorf("XLMTotalSupplyStroops = %s, want %s", got.String(), want.String())
	}
}

// TestXLMTotalSupplyStroops_ReturnsCopy — the function must hand
// back a freshly-allocated big.Int so callers can't mutate the
// package-level constant.
func TestXLMTotalSupplyStroops_ReturnsCopy(t *testing.T) {
	a := supply.XLMTotalSupplyStroops()
	a.SetInt64(0) // mutate caller's copy
	b := supply.XLMTotalSupplyStroops()
	if b.Sign() == 0 {
		t.Errorf("XLMTotalSupplyStroops shares state across calls; mutation leaked")
	}
}

// TestNewXLMComputer_RejectsNilReaderWithReserves — operator misconfig
// (reserve accounts configured but no reader wired) would silently
// over-state circulating; constructor must fail loudly instead.
func TestNewXLMComputer_RejectsNilReaderWithReserves(t *testing.T) {
	_, err := supply.NewXLMComputer([]string{"GA..."}, nil)
	if !errors.Is(err, supply.ErrNilReader) {
		t.Errorf("err = %v, want ErrNilReader", err)
	}
}

// TestNewXLMComputer_AllowsNilReaderWithEmptyReserves — a deployment
// that hasn't configured reserve accounts yet should still build a
// working computer (circulating == total).
func TestNewXLMComputer_AllowsNilReaderWithEmptyReserves(t *testing.T) {
	c, err := supply.NewXLMComputer(nil, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got, err := c.Compute(context.Background(), 12345, time.Unix(1_777_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := supply.XLMTotalSupplyStroops()
	if got.CirculatingSupply.Cmp(want) != 0 {
		t.Errorf("with no reserves, circulating = %s, want %s",
			got.CirculatingSupply.String(), want.String())
	}
	if got.AssetKey != "XLM" {
		t.Errorf("AssetKey = %q, want %q", got.AssetKey, "XLM")
	}
	if got.Basis != supply.BasisXLMSDFReserveExclusion {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisXLMSDFReserveExclusion)
	}
}

// TestCompute_HappyPath — reserves configured, reader returns a
// known balance; circulating = total − reserved exactly.
func TestCompute_HappyPath(t *testing.T) {
	const reservedXLM = 1_000_000 // 1M XLM
	reservedStroops := new(big.Int).Mul(big.NewInt(reservedXLM), big.NewInt(10_000_000))
	reader := &stubReader{balance: reservedStroops}

	accounts := []string{"GAAA...", "GBBB...", "GCCC..."}
	c, err := supply.NewXLMComputer(accounts, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	const ledger = 50_000_000
	closeTime := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got, err := c.Compute(context.Background(), ledger, closeTime)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	wantTotal := supply.XLMTotalSupplyStroops()
	wantCirculating := new(big.Int).Sub(wantTotal, reservedStroops)
	if got.TotalSupply.Cmp(wantTotal) != 0 {
		t.Errorf("TotalSupply = %s, want %s", got.TotalSupply, wantTotal)
	}
	if got.CirculatingSupply.Cmp(wantCirculating) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s", got.CirculatingSupply, wantCirculating)
	}
	// Algorithm 1: max == total.
	if got.MaxSupply.Cmp(wantTotal) != 0 {
		t.Errorf("MaxSupply = %s, want %s (== total)", got.MaxSupply, wantTotal)
	}
	if got.LedgerSequence != ledger {
		t.Errorf("LedgerSequence = %d, want %d", got.LedgerSequence, ledger)
	}
	if !got.ObservedAt.Equal(closeTime) {
		t.Errorf("ObservedAt = %v, want %v", got.ObservedAt, closeTime)
	}
	if reader.calls != 1 {
		t.Errorf("reader.calls = %d, want 1", reader.calls)
	}
	if got, want := reader.last.ledger, uint32(ledger); got != want {
		t.Errorf("reader saw ledger %d, want %d", got, want)
	}
	if len(reader.last.accounts) != len(accounts) {
		t.Errorf("reader saw %d accounts, want %d", len(reader.last.accounts), len(accounts))
	}
}

// TestCompute_PropagatesReaderError — a reader failure must NOT
// fall back to "circulating == total" (which would silently mask a
// reserves outage); surface the error so the caller can decide.
func TestCompute_PropagatesReaderError(t *testing.T) {
	reader := &stubReader{err: errors.New("postgres unavailable")}
	c, err := supply.NewXLMComputer([]string{"GAAA..."}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if _, err := c.Compute(context.Background(), 1, time.Now()); err == nil {
		t.Error("expected error when reader fails; got nil")
	}
}

// TestCompute_GuardsAgainstNilReaderReturn — defensive: a misbehaving
// reader returning (nil, nil) used to nil-pointer the Sub call.
// Verify the explicit guard surfaces a clear error.
func TestCompute_GuardsAgainstNilReaderReturn(t *testing.T) {
	// stubReader with both balance=nil AND err=nil triggers the
	// "balance is nil" path (via the early return inside the
	// reader stub) — but the stub returns big.NewInt(0) in that
	// case. Use a separate naughty stub.
	c, _ := supply.NewXLMComputer([]string{"GAAA..."}, &nilReturningReader{})
	if _, err := c.Compute(context.Background(), 1, time.Now()); err == nil {
		t.Error("expected error from nil-returning reader; got nil")
	}
}

type nilReturningReader struct{}

func (nilReturningReader) ReserveBalanceTotal(_ context.Context, _ []string, _ uint32) (*big.Int, error) {
	return nil, nil
}

// TestCompute_DefensiveCopyOfReserveAccounts — mutating the slice
// the caller handed in must NOT change what the computer queries on
// subsequent Compute calls.
func TestCompute_DefensiveCopyOfReserveAccounts(t *testing.T) {
	reader := &stubReader{balance: big.NewInt(0)}
	accounts := []string{"GAAA...", "GBBB..."}
	c, err := supply.NewXLMComputer(accounts, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	// Mutate the caller's slice — should not affect the computer.
	accounts[0] = "MUTATED"
	if _, err := c.Compute(context.Background(), 1, time.Now()); err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if reader.last.accounts[0] == "MUTATED" {
		t.Errorf("reserve accounts mutation leaked into computer; saw %q", reader.last.accounts[0])
	}
}

// freshnessReader is a reader that ALSO satisfies
// [supply.ReserveBalanceFreshnessReader], used by the F-1236
// (codex audit-2026-05-12) XLM freshness gate tests below.
type freshnessReader struct {
	balance   *big.Int
	minLedger uint32
	freshErr  error
}

func (f *freshnessReader) ReserveBalanceTotal(_ context.Context, _ []string, _ uint32) (*big.Int, error) {
	return new(big.Int).Set(f.balance), nil
}

func (f *freshnessReader) MinReserveAccountLedger(_ context.Context, _ []string, _ uint32) (uint32, error) {
	if f.freshErr != nil {
		return 0, f.freshErr
	}
	return f.minLedger, nil
}

// TestCompute_FreshnessReaderPopulatesMinComponentLedger — when
// the reader implements ReserveBalanceFreshnessReader, the
// computed [supply.Supply] carries the per-component freshness
// signal the Refresher's stale-component gate uses. F-1236
// (codex audit-2026-05-12) — closes the third leg of the gate
// after classic + SEP41 shipped in waves 17 + 18.
func TestCompute_FreshnessReaderPopulatesMinComponentLedger(t *testing.T) {
	reader := &freshnessReader{balance: big.NewInt(1_000_000), minLedger: 49_999_000}
	c, err := supply.NewXLMComputer([]string{"GA1", "GA2"}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got, err := c.Compute(context.Background(), 50_000_000, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.MinComponentLedger != 49_999_000 {
		t.Errorf("MinComponentLedger = %d, want 49_999_000", got.MinComponentLedger)
	}
}

// TestCompute_FreshnessReaderErrorIsNonFatal — a freshness probe
// failure must NOT take the snapshot down. The Refresher's gate
// stays permissive (MinComponentLedger=0) on freshness-query
// errors, same shape classic + SEP41 already use. Operators read
// the WARN log on the reader's path.
func TestCompute_FreshnessReaderErrorIsNonFatal(t *testing.T) {
	reader := &freshnessReader{
		balance:  big.NewInt(1_000_000),
		freshErr: errors.New("transient db failure"),
	}
	c, err := supply.NewXLMComputer([]string{"GA1"}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got, err := c.Compute(context.Background(), 50_000_000, time.Now())
	if err != nil {
		t.Fatalf("Compute (should swallow freshness err): %v", err)
	}
	if got.MinComponentLedger != 0 {
		t.Errorf("MinComponentLedger = %d, want 0 (permissive on freshness-query error)", got.MinComponentLedger)
	}
	if got.CirculatingSupply == nil {
		t.Error("CirculatingSupply must still be populated when only freshness fails")
	}
}

// TestCompute_LegacyReader_NoFreshnessSignal — a reader that
// does NOT implement ReserveBalanceFreshnessReader leaves
// MinComponentLedger at 0, preserving the pre-F-1236 permissive
// posture for deployments that haven't migrated.
func TestCompute_LegacyReader_NoFreshnessSignal(t *testing.T) {
	reader := &stubReader{balance: big.NewInt(1_000_000)}
	c, err := supply.NewXLMComputer([]string{"GA1"}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got, err := c.Compute(context.Background(), 50_000_000, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.MinComponentLedger != 0 {
		t.Errorf("MinComponentLedger = %d, want 0 (legacy reader → no freshness signal)", got.MinComponentLedger)
	}
}
