package pipeline

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// realisticGStrkey is a syntactically-valid 56-char G-prefixed
// strkey reused as a fixture below. The accounts.NewObserver
// constructor only checks for non-empty strings; stricter strkey
// validation happens upstream at config.Validate.
const realisticGStrkey = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// TestRegisterSupplyEntryDecoders_AccountsNoOpWhenEmpty pins the
// safe default: an operator who hasn't opted into supply-side
// observation has no behaviour change. RegisterSupplyEntryDecoders
// returns an empty slice + nil error; the dispatcher remains as
// BuildDispatcher left it.
func TestRegisterSupplyEntryDecoders_AccountsNoOpWhenEmpty(t *testing.T) {
	disp := dispatcher.New()
	registered, err := RegisterSupplyEntryDecoders(disp, config.SupplyConfig{})
	if err != nil {
		t.Fatalf("RegisterSupplyEntryDecoders: %v", err)
	}
	if len(registered) != 0 {
		t.Errorf("registered = %v, want empty (no watched accounts)", registered)
	}
}

// TestRegisterSupplyEntryDecoders_AccountsRegistersWhenWatched
// confirms the accounts observer attaches when SDFReserveAccounts
// is non-empty. The returned slice carries the source name for boot-
// log clarity. Dispatcher state is exercised via the observer's
// public name.
func TestRegisterSupplyEntryDecoders_AccountsRegistersWhenWatched(t *testing.T) {
	disp := dispatcher.New()
	cfg := config.SupplyConfig{
		SDFReserveAccounts: []string{realisticGStrkey},
	}
	registered, err := RegisterSupplyEntryDecoders(disp, cfg)
	if err != nil {
		t.Fatalf("RegisterSupplyEntryDecoders: %v", err)
	}
	if len(registered) != 1 {
		t.Fatalf("registered = %v, want exactly 1 (accounts)", registered)
	}
	if registered[0] != "accounts" {
		t.Errorf("registered[0] = %q, want 'accounts'", registered[0])
	}
}

// TestRegisterSupplyEntryDecoders_RejectsEmptyStrkey reaches into
// accounts.NewObserver's defensive guard: an SDFReserveAccounts
// list that survived config.Validate but contains an empty string
// (e.g. a programmatic builder typo) must surface as a hard error
// at registration time. Operators see a misconfiguration before
// the indexer starts processing ledgers, not later.
func TestRegisterSupplyEntryDecoders_RejectsEmptyStrkey(t *testing.T) {
	disp := dispatcher.New()
	cfg := config.SupplyConfig{
		SDFReserveAccounts: []string{realisticGStrkey, ""},
	}
	_, err := RegisterSupplyEntryDecoders(disp, cfg)
	if err == nil {
		t.Fatal("expected error for empty G-strkey in watched-accounts list")
	}
}

// TestRegisterSupplyEntryDecoders_ClassicTrioRegisters confirms the
// three classic-asset observers (trustlines / claimable_balances /
// liquidity_pools) all attach when WatchedClassicAssets is set.
// They share the same watched-set because Algorithm 2 sums all three
// components per asset; an operator who watches an asset MUST get
// every component or the sum is wrong.
func TestRegisterSupplyEntryDecoders_ClassicTrioRegisters(t *testing.T) {
	disp := dispatcher.New()
	cfg := config.SupplyConfig{
		WatchedClassicAssets: []string{"USDC-" + realisticGStrkey},
	}
	registered, err := RegisterSupplyEntryDecoders(disp, cfg)
	if err != nil {
		t.Fatalf("RegisterSupplyEntryDecoders: %v", err)
	}
	wantSet := map[string]bool{
		"trustlines":         true,
		"claimable_balances": true,
		"liquidity_pools":    true,
	}
	gotSet := map[string]bool{}
	for _, name := range registered {
		gotSet[name] = true
	}
	for name := range wantSet {
		if !gotSet[name] {
			t.Errorf("registered set missing %q (got %v)", name, registered)
		}
	}
}

// TestRegisterSupplyEntryDecoders_SACRegistersWhenWrappers confirms
// the SAC observer attaches when sac_wrappers is set, independently
// of WatchedClassicAssets. Operators who run the SAC observer for
// a SAC-wrapped classic without the trustline/claimable/LP trio
// (e.g. cross-check-only deployments) hit this path.
func TestRegisterSupplyEntryDecoders_SACRegistersWhenWrappers(t *testing.T) {
	disp := dispatcher.New()
	cfg := config.SupplyConfig{
		SACWrappers: map[string]string{realisticCStrkey: "USDC:" + realisticGStrkey},
	}
	registered, err := RegisterSupplyEntryDecoders(disp, cfg)
	if err != nil {
		t.Fatalf("RegisterSupplyEntryDecoders: %v", err)
	}
	if len(registered) != 1 || registered[0] != "sac_balances" {
		t.Errorf("registered = %v, want [sac_balances]", registered)
	}
}

// TestRegisterSupplyEntryDecoders_FullConfigRegistersFive exercises
// the full opt-in surface: every watched-set populated → every
// LedgerEntryChangeDecoder registered. (sep41_supply is an event-
// stream Decoder, not in this helper's scope; follow-up PR.)
func TestRegisterSupplyEntryDecoders_FullConfigRegistersFive(t *testing.T) {
	disp := dispatcher.New()
	cfg := config.SupplyConfig{
		SDFReserveAccounts:   []string{realisticGStrkey},
		WatchedClassicAssets: []string{"USDC-" + realisticGStrkey},
		SACWrappers:          map[string]string{realisticCStrkey: "USDC:" + realisticGStrkey},
	}
	registered, err := RegisterSupplyEntryDecoders(disp, cfg)
	if err != nil {
		t.Fatalf("RegisterSupplyEntryDecoders: %v", err)
	}
	if len(registered) != 5 {
		t.Errorf("registered count = %d, want 5 (accounts + trustlines + claimable + lp + sac); got %v",
			len(registered), registered)
	}
}

// realisticCStrkey is a 56-char C-prefixed Soroban contract id.
// Mirrors the supply-package test fixture pattern. Public ledger
// entry — not a credential.
const realisticCStrkey = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUOZWS4HG3B5UPHHC2QQA"
