//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/RatesEngine/rates-engine/internal/platform"
	"github.com/RatesEngine/rates-engine/internal/platform/postgresstore"
)

// TestPlatformPostgresStores exercises the AccountStore +
// UserStore + TokenStore implementations against the schema
// from migration 0027. One container per test (no shared
// fixture) per the existing storage-test convention.
func TestPlatformPostgresStores(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := postgresstore.New(db)
	accounts := postgresstore.NewAccountStore(store)
	users := postgresstore.NewUserStore(store)
	tokens := postgresstore.NewTokenStore(store)

	t.Run("Account/CRUD", func(t *testing.T) {
		acme, err := accounts.Create(ctx, platform.Account{
			Name:         "Acme Corp",
			Slug:         "acme",
			BillingEmail: "billing@acme.example",
			Tier:         platform.TierFree,
			Status:       platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if acme.ID == uuid.Nil {
			t.Fatal("ID not populated")
		}
		if acme.CreatedAt.IsZero() {
			t.Fatal("CreatedAt not populated")
		}

		// Get by id, slug.
		got, err := accounts.Get(ctx, acme.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Name != "Acme Corp" {
			t.Errorf("Name = %q", got.Name)
		}

		bySlug, err := accounts.GetBySlug(ctx, "acme")
		if err != nil {
			t.Fatalf("get by slug: %v", err)
		}
		if bySlug.ID != acme.ID {
			t.Errorf("slug lookup got different account")
		}

		// Update tier; verify.
		acme.Tier = platform.TierPro
		if err := accounts.Update(ctx, acme); err != nil {
			t.Fatalf("update: %v", err)
		}
		got, _ = accounts.Get(ctx, acme.ID)
		if got.Tier != platform.TierPro {
			t.Errorf("tier didn't persist: %q", got.Tier)
		}

		// Suspend → unsuspend (idempotency).
		if err := accounts.Suspend(ctx, acme.ID, "abuse"); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if err := accounts.Suspend(ctx, acme.ID, "abuse-again"); err != nil {
			t.Fatalf("suspend (idempotent): %v", err)
		}
		got, _ = accounts.Get(ctx, acme.ID)
		if got.Status != platform.AccountSuspended {
			t.Errorf("not suspended: %q", got.Status)
		}
		if got.SuspendedAt.IsZero() {
			t.Errorf("SuspendedAt not stamped")
		}
		if got.SuspendedReason != "abuse-again" {
			t.Errorf("SuspendedReason = %q", got.SuspendedReason)
		}

		if err := accounts.Unsuspend(ctx, acme.ID); err != nil {
			t.Fatalf("unsuspend: %v", err)
		}
		got, _ = accounts.Get(ctx, acme.ID)
		if got.Status != platform.AccountActive {
			t.Errorf("not active after unsuspend: %q", got.Status)
		}
		if !got.SuspendedAt.IsZero() {
			t.Errorf("SuspendedAt not cleared")
		}

		// Slug uniqueness → ErrConflict.
		_, err = accounts.Create(ctx, platform.Account{
			Name: "Acme 2", Slug: "acme",
			BillingEmail: "x@y.com",
			Tier:         platform.TierFree, Status: platform.AccountActive,
		})
		if !errors.Is(err, platform.ErrConflict) {
			t.Errorf("expected ErrConflict on duplicate slug, got %v", err)
		}

		// ErrNotFound on absent.
		if _, err := accounts.Get(ctx, uuid.New()); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("User/CRUD+sessions", func(t *testing.T) {
		acct, err := accounts.Create(ctx, platform.Account{
			Name: "Beta Co", Slug: "beta",
			BillingEmail: "b@beta.example",
			Tier:         platform.TierStarter, Status: platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}

		alice, err := users.CreateUser(ctx, platform.User{
			AccountID: acct.ID,
			Email:     "alice@beta.example",
			Role:      platform.RoleOwner,
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if alice.ID == uuid.Nil {
			t.Fatal("user ID not populated")
		}

		// Email lookup is case-insensitive (citext column).
		got, err := users.GetUserByEmail(ctx, "ALICE@BETA.EXAMPLE")
		if err != nil {
			t.Fatalf("get by email (case-insensitive): %v", err)
		}
		if got.ID != alice.ID {
			t.Errorf("citext lookup didn't match")
		}

		// Duplicate email → ErrConflict.
		_, err = users.CreateUser(ctx, platform.User{
			AccountID: acct.ID,
			Email:     "alice@beta.example",
			Role:      platform.RoleMember,
		})
		if !errors.Is(err, platform.ErrConflict) {
			t.Errorf("expected ErrConflict on duplicate email, got %v", err)
		}

		// List users for account.
		_, err = users.CreateUser(ctx, platform.User{
			AccountID: acct.ID,
			Email:     "bob@beta.example",
			Role:      platform.RoleMember,
		})
		if err != nil {
			t.Fatalf("create bob: %v", err)
		}

		list, err := users.ListUsersForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("list len = %d, want 2", len(list))
		}

		// Session round-trip.
		ip := net.ParseIP("203.0.113.42")
		sess, err := users.CreateSession(ctx, platform.Session{
			UserID:       alice.ID,
			ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
			IPFirstSeen:  ip,
			IPLastSeen:   ip,
			UserAgent:    "Mozilla/5.0",
			GeoFirstSeen: "US",
			GeoLastSeen:  "US",
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}

		gotSess, err := users.GetSession(ctx, sess.ID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if gotSess.UserID != alice.ID {
			t.Errorf("session UserID = %v", gotSess.UserID)
		}

		// Touch updates last_seen + ip_last + UA.
		newIP := net.ParseIP("203.0.113.99")
		if err := users.TouchSession(ctx, sess.ID, newIP, "curl/8"); err != nil {
			t.Fatalf("touch: %v", err)
		}
		gotSess, _ = users.GetSession(ctx, sess.ID)
		if !gotSess.IPLastSeen.Equal(newIP) {
			t.Errorf("IPLastSeen = %v, want %v", gotSess.IPLastSeen, newIP)
		}
		if gotSess.UserAgent != "curl/8" {
			t.Errorf("UserAgent = %q", gotSess.UserAgent)
		}

		// Revoke → subsequent GetSession returns ErrNotFound.
		if err := users.RevokeSession(ctx, sess.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := users.GetSession(ctx, sess.ID); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound after revoke, got %v", err)
		}

		// Re-revoke is a no-op.
		if err := users.RevokeSession(ctx, sess.ID); err != nil {
			t.Errorf("re-revoke: %v", err)
		}
	})

	t.Run("MagicLinkToken/lifecycle", func(t *testing.T) {
		hash := sha256.Sum256([]byte("token-1"))

		// Future expiry: consume succeeds.
		err := tokens.CreateMagicLinkToken(ctx, platform.MagicLinkToken{
			TokenHash:   hash[:],
			Email:       "user@example.com",
			Purpose:     platform.TokenPurposeLogin,
			ExpiresAt:   time.Now().Add(15 * time.Minute),
			RequestedIP: net.ParseIP("203.0.113.1"),
		})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}

		got, err := tokens.ConsumeMagicLinkToken(ctx, hash[:])
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		if got.Email != "user@example.com" {
			t.Errorf("email = %q", got.Email)
		}
		if got.ConsumedAt.IsZero() {
			t.Errorf("ConsumedAt not stamped")
		}

		// Second consume → ErrNotFound (already consumed).
		if _, err := tokens.ConsumeMagicLinkToken(ctx, hash[:]); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound on re-consume, got %v", err)
		}

		// Expired token: classify as ErrTokenExpired.
		expHash := sha256.Sum256([]byte("expired-token"))
		err = tokens.CreateMagicLinkToken(ctx, platform.MagicLinkToken{
			TokenHash:   expHash[:],
			Email:       "user2@example.com",
			Purpose:     platform.TokenPurposeLogin,
			ExpiresAt:   time.Now().Add(-1 * time.Minute),
			RequestedIP: net.ParseIP("203.0.113.2"),
		})
		if err != nil {
			t.Fatalf("create expired token: %v", err)
		}
		_, err = tokens.ConsumeMagicLinkToken(ctx, expHash[:])
		if !errors.Is(err, platform.ErrTokenExpired) {
			t.Errorf("expected ErrTokenExpired, got %v", err)
		}

		// Missing token → ErrNotFound.
		nope := sha256.Sum256([]byte("never-existed"))
		if _, err := tokens.ConsumeMagicLinkToken(ctx, nope[:]); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("Invite/lifecycle", func(t *testing.T) {
		acct, err := accounts.Create(ctx, platform.Account{
			Name: "Invite Co", Slug: "invite-co-" + strings.ToLower(uuid.New().String()[:8]),
			BillingEmail: "i@i.example",
			Tier:         platform.TierFree, Status: platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}

		inviter, err := users.CreateUser(ctx, platform.User{
			AccountID: acct.ID,
			Email:     "inviter-" + uuid.New().String() + "@x.example",
			Role:      platform.RoleOwner,
		})
		if err != nil {
			t.Fatalf("create inviter: %v", err)
		}

		hash := sha256.Sum256([]byte("invite-1"))
		err = tokens.CreateInvite(ctx, platform.Invite{
			TokenHash:       hash[:],
			AccountID:       acct.ID,
			Email:           "newcomer@i.example",
			Role:            platform.RoleMember,
			InvitedByUserID: inviter.ID,
			ExpiresAt:       time.Now().Add(7 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("create invite: %v", err)
		}

		// Pending list should include it.
		pending, err := tokens.ListInvitesForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("list invites: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("pending = %d, want 1", len(pending))
		}

		// Accept.
		got, err := tokens.AcceptInvite(ctx, hash[:])
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		if got.AccountID != acct.ID || got.Email != "newcomer@i.example" {
			t.Errorf("invite shape mismatched: %+v", got)
		}

		// Pending list now empty.
		pending, _ = tokens.ListInvitesForAccount(ctx, acct.ID)
		if len(pending) != 0 {
			t.Errorf("pending after accept = %d, want 0", len(pending))
		}

		// Re-accept → ErrNotFound.
		if _, err := tokens.AcceptInvite(ctx, hash[:]); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("re-accept: expected ErrNotFound, got %v", err)
		}

		// Revoke pre-accept (separate token).
		hash2 := sha256.Sum256([]byte("invite-2"))
		_ = tokens.CreateInvite(ctx, platform.Invite{
			TokenHash:       hash2[:],
			AccountID:       acct.ID,
			Email:           "second@i.example",
			Role:            platform.RoleMember,
			InvitedByUserID: inviter.ID,
			ExpiresAt:       time.Now().Add(time.Hour),
		})
		if err := tokens.RevokeInvite(ctx, hash2[:]); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		// Accepting a revoked invite → ErrNotFound.
		if _, err := tokens.AcceptInvite(ctx, hash2[:]); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("accept-after-revoke: expected ErrNotFound, got %v", err)
		}
	})

	t.Run("APIKey/CRUD+revoke+touch", func(t *testing.T) {
		keys := postgresstore.NewAPIKeyStore(store)

		acct, err := accounts.Create(ctx, platform.Account{
			Name: "Keyed Co", Slug: "keyed-" + strings.ToLower(uuid.New().String()[:8]),
			BillingEmail: "k@k.example",
			Tier:         platform.TierStarter, Status: platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		owner, err := users.CreateUser(ctx, platform.User{
			AccountID: acct.ID,
			Email:     "owner-" + uuid.New().String() + "@k.example",
			Role:      platform.RoleOwner,
		})
		if err != nil {
			t.Fatalf("create owner: %v", err)
		}

		hash := sha256.Sum256([]byte("rek_plaintext_xyz"))
		key := platform.APIKey{
			ID:                     "kid_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12],
			AccountID:              acct.ID,
			CreatedByUserID:        owner.ID,
			Name:                   "primary",
			Description:            "production traffic",
			KeyHash:                hash[:],
			KeyPrefix:              "rek_4f9c1d8b",
			Tier:                   platform.APIKeyTierAPIKey,
			RateLimitPerMin:        1000,
			MonthlyQuota:           500000,
			Permissions:            platform.KeyPermissions{All: true},
			RefererAllowlist:       []string{"https://example.com"},
			UsageAlertThresholdPct: 80,
		}
		// Add an IP allowlist entry to exercise cidr[] path.
		prefix, perr := netip.ParsePrefix("203.0.113.0/24")
		if perr != nil {
			t.Fatalf("parse prefix: %v", perr)
		}
		key.IPAllowlist = []netip.Prefix{prefix}

		out, err := keys.Create(ctx, key)
		if err != nil {
			t.Fatalf("create key: %v", err)
		}
		if out.CreatedAt.IsZero() {
			t.Error("CreatedAt not populated")
		}
		if out.AccountID != acct.ID {
			t.Errorf("AccountID round-trip: got %v want %v", out.AccountID, acct.ID)
		}
		if !out.Permissions.All {
			t.Errorf("Permissions.All didn't round-trip")
		}
		if len(out.IPAllowlist) != 1 || out.IPAllowlist[0].String() != "203.0.113.0/24" {
			t.Errorf("IPAllowlist round-trip: %+v", out.IPAllowlist)
		}
		if len(out.RefererAllowlist) != 1 || out.RefererAllowlist[0] != "https://example.com" {
			t.Errorf("RefererAllowlist round-trip: %+v", out.RefererAllowlist)
		}

		// Get by id, by hash.
		byID, err := keys.Get(ctx, key.ID)
		if err != nil {
			t.Fatalf("get by id: %v", err)
		}
		if byID.Name != "primary" {
			t.Errorf("Name = %q", byID.Name)
		}
		byHash, err := keys.GetByHash(ctx, hash[:])
		if err != nil {
			t.Fatalf("get by hash: %v", err)
		}
		if byHash.ID != key.ID {
			t.Errorf("hash lookup got different key")
		}

		// List for account.
		list, err := keys.ListForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("list len = %d, want 1", len(list))
		}

		// Update: bump rate limit + add description.
		byID.RateLimitPerMin = 5000
		byID.Description = "production traffic — bumped"
		if err := keys.Update(ctx, byID); err != nil {
			t.Fatalf("update: %v", err)
		}
		got, _ := keys.Get(ctx, byID.ID)
		if got.RateLimitPerMin != 5000 {
			t.Errorf("RateLimitPerMin = %d", got.RateLimitPerMin)
		}
		if !strings.Contains(got.Description, "bumped") {
			t.Errorf("Description didn't persist")
		}

		// Touch usage.
		ip := net.ParseIP("198.51.100.7")
		if err := keys.TouchUsage(ctx, byID.ID, ip, "curl/8"); err != nil {
			t.Fatalf("touch: %v", err)
		}
		got, _ = keys.Get(ctx, byID.ID)
		if got.LastUsedAt.IsZero() {
			t.Errorf("LastUsedAt not stamped")
		}
		if !got.LastUsedIP.Equal(ip) {
			t.Errorf("LastUsedIP = %v", got.LastUsedIP)
		}

		// Revoke + idempotency.
		if err := keys.Revoke(ctx, byID.ID, owner.ID, "rotated"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		got, _ = keys.Get(ctx, byID.ID)
		if got.RevokedAt.IsZero() {
			t.Errorf("RevokedAt not stamped")
		}
		if got.IsActive(time.Now()) {
			t.Errorf("IsActive returned true on revoked key")
		}
		if err := keys.Revoke(ctx, byID.ID, owner.ID, "still rotated"); err != nil {
			t.Errorf("re-revoke: %v", err)
		}

		// Hash-collision (re-Create same hash) → ErrConflict.
		dup := key
		dup.ID = "kid_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
		_, err = keys.Create(ctx, dup)
		if !errors.Is(err, platform.ErrConflict) {
			t.Errorf("expected ErrConflict on duplicate hash, got %v", err)
		}

		// ErrNotFound on absent.
		if _, err := keys.Get(ctx, "kid_nonexistent00"); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
