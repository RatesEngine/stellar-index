package dashboardauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// fakeEmailLocker is the in-memory analogue of [auth.RedisSignupEmailLocker]
// for unit tests. Acquire returns true exactly once per key; Release
// removes the key so a subsequent Acquire wins again. Mirrors the
// SETNX-style ownership the Redis adapter implements.
type fakeEmailLocker struct {
	mu   sync.Mutex
	held map[string]bool
}

func newFakeEmailLocker() *fakeEmailLocker {
	return &fakeEmailLocker{held: map[string]bool{}}
}

func (l *fakeEmailLocker) Acquire(_ context.Context, key string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return false, nil
	}
	l.held[key] = true
	return true, nil
}

func (l *fakeEmailLocker) Release(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, key)
	return nil
}

// TestSignupNewUser_EmailLocker_PreemptsLoser — when the locker is
// already held for an email (a concurrent winner is provisioning),
// the loser must wait + return the winner's user WITHOUT creating
// a speculative Account row.
//
// F-1255 (codex audit-2026-05-12): proves the full-fix path. The
// fallback Suspend-on-conflict recovery still serves as defence
// in depth, but the lock path should never trigger it.
func TestSignupNewUser_EmailLocker_PreemptsLoser(t *testing.T) {
	r := newTestRig(t)
	locker := newFakeEmailLocker()
	r.cfg.EmailLocker = locker

	// Simulate the winner: pre-hold the lock + insert a User row
	// behind the winner's Account so the loser's poll converges.
	emailHash := hashEmailForLocker("ash@example.com")
	if ok, err := locker.Acquire(context.Background(), emailHash, time.Second); !ok || err != nil {
		t.Fatalf("pre-acquire: ok=%v err=%v", ok, err)
	}

	winnerAcct, err := r.accounts.Create(context.Background(), platform.Account{
		Name: "winner", Slug: "winner", BillingEmail: "ash@example.com",
		Tier: platform.TierFree, Status: platform.AccountActive,
	})
	if err != nil {
		t.Fatalf("seed winner account: %v", err)
	}
	winner, err := r.users.CreateUser(context.Background(), platform.User{
		AccountID: winnerAcct.ID, Email: "ash@example.com", Role: platform.RoleOwner,
	})
	if err != nil {
		t.Fatalf("seed winner user: %v", err)
	}

	before := len(r.accounts.byID)

	// Loser comes through signupNewUser. Lock acquire fails ->
	// waitForWinnerUser converges to the winner row -> return.
	got, err := r.h.signupNewUser(context.Background(), "ash@example.com")
	if err != nil {
		t.Fatalf("signupNewUser as loser: %v", err)
	}
	if got.ID != winner.ID {
		t.Errorf("loser got user %v, want winner %v", got.ID, winner.ID)
	}
	if got.AccountID != winnerAcct.ID {
		t.Errorf("loser got AccountID %v, want winner's %v", got.AccountID, winnerAcct.ID)
	}
	if delta := len(r.accounts.byID) - before; delta != 0 {
		t.Errorf("speculative-account rows created by loser = %d, want 0 (lock should pre-empt before Account.Create)", delta)
	}
}

// TestSignupNewUser_EmailLocker_WinnerSucceeds — happy path with
// the lock available. Winner acquires, provisions, releases. The
// loser case is covered above; this case just proves the lock
// doesn't break the normal flow.
func TestSignupNewUser_EmailLocker_WinnerSucceeds(t *testing.T) {
	r := newTestRig(t)
	r.cfg.EmailLocker = newFakeEmailLocker()

	got, err := r.h.signupNewUser(context.Background(), "fresh@example.com")
	if err != nil {
		t.Fatalf("signupNewUser as winner: %v", err)
	}
	if got.Email != "fresh@example.com" {
		t.Errorf("winner.Email = %q", got.Email)
	}
	if got.AccountID == [16]byte{} {
		t.Errorf("winner.AccountID is zero — Account.Create should have run")
	}
}
