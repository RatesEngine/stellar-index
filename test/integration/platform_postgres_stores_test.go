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

	t.Run("BillingStore/Subscription/UpsertAndGetActive", func(t *testing.T) {
		billing := postgresstore.NewBillingStore(store)

		acct, err := accounts.Create(ctx, platform.Account{
			Name:         "Subbed Co",
			Slug:         "subbed-" + strings.ToLower(uuid.New().String()[:8]),
			BillingEmail: "billing-" + uuid.New().String() + "@s.example",
			Tier:         platform.TierStarter,
			Status:       platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}

		// 1. No active subscription → ErrNotFound.
		if _, err := billing.GetActiveSubscriptionForAccount(ctx, acct.ID); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound on fresh account, got %v", err)
		}

		// 2. Insert a Pro subscription with a future period end.
		now := time.Now().UTC()
		stripeSubID := "sub_test_" + uuid.New().String()[:12]
		err = billing.UpsertSubscription(ctx, platform.Subscription{
			AccountID:            acct.ID,
			StripeSubscriptionID: stripeSubID,
			Plan:                 platform.PlanPro,
			CurrentPeriodStart:   now.Add(-24 * time.Hour),
			CurrentPeriodEnd:     now.Add(29 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("UpsertSubscription (insert): %v", err)
		}

		// 3. GetActiveSubscriptionForAccount now succeeds + carries
		//    the right plan / period.
		got, err := billing.GetActiveSubscriptionForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("GetActiveSubscriptionForAccount: %v", err)
		}
		if got.Plan != platform.PlanPro {
			t.Errorf("Plan = %q, want %q", got.Plan, platform.PlanPro)
		}
		if got.StripeSubscriptionID != stripeSubID {
			t.Errorf("StripeSubscriptionID = %q, want %q", got.StripeSubscriptionID, stripeSubID)
		}
		if !got.IsActive(now) {
			t.Error("IsActive = false on a future-period subscription")
		}

		// 4. Idempotent re-upsert with the SAME stripe_subscription_id
		//    must update the plan without creating a duplicate row.
		err = billing.UpsertSubscription(ctx, platform.Subscription{
			AccountID:            acct.ID,
			StripeSubscriptionID: stripeSubID,
			Plan:                 platform.PlanBusiness, // upgraded
			CurrentPeriodStart:   now,
			CurrentPeriodEnd:     now.Add(30 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("UpsertSubscription (update): %v", err)
		}
		got, err = billing.GetActiveSubscriptionForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("GetActiveSubscriptionForAccount (after upgrade): %v", err)
		}
		if got.Plan != platform.PlanBusiness {
			t.Errorf("Plan after upgrade = %q, want %q", got.Plan, platform.PlanBusiness)
		}

		// 5. Expired subscription (period_end in the past) is NOT
		//    active — even without canceled_at set.
		expiredID := "sub_expired_" + uuid.New().String()[:12]
		expiredAcct, err := accounts.Create(ctx, platform.Account{
			Name:         "Expired Co",
			Slug:         "expired-" + strings.ToLower(uuid.New().String()[:8]),
			BillingEmail: "exp-" + uuid.New().String() + "@s.example",
			Tier:         platform.TierFree,
			Status:       platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create expired-test account: %v", err)
		}
		err = billing.UpsertSubscription(ctx, platform.Subscription{
			AccountID:            expiredAcct.ID,
			StripeSubscriptionID: expiredID,
			Plan:                 platform.PlanPro,
			CurrentPeriodStart:   now.Add(-60 * 24 * time.Hour),
			CurrentPeriodEnd:     now.Add(-30 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("UpsertSubscription (expired): %v", err)
		}
		if _, err := billing.GetActiveSubscriptionForAccount(ctx, expiredAcct.ID); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expired subscription should not surface as active: err = %v", err)
		}

		// 6. Validation: empty AccountID is rejected.
		err = billing.UpsertSubscription(ctx, platform.Subscription{
			StripeSubscriptionID: "sub_no_account",
			Plan:                 platform.PlanPro,
			CurrentPeriodStart:   now,
			CurrentPeriodEnd:     now.Add(time.Hour),
		})
		if err == nil || !strings.Contains(err.Error(), "AccountID") {
			t.Errorf("expected AccountID-required error, got %v", err)
		}
	})

	t.Run("WebhookStore/CRUD+queue", func(t *testing.T) {
		webhooks := postgresstore.NewWebhookStore(store)
		acct, err := accounts.Create(ctx, platform.Account{
			Name:         "Hooked Co",
			Slug:         "hooked-" + strings.ToLower(uuid.New().String()[:8]),
			BillingEmail: "hook-" + uuid.New().String() + "@h.example",
			Tier:         platform.TierStarter,
			Status:       platform.AccountActive,
		})
		if err != nil {
			t.Fatalf("create webhook-test account: %v", err)
		}

		// 1. Create + Get
		hash := sha256.Sum256([]byte("seekrit"))
		created, err := webhooks.CreateWebhook(ctx, platform.CustomerWebhook{
			AccountID:  acct.ID,
			Name:       "ops-slack",
			URL:        "https://hooks.slack.example/services/T/B/X",
			SecretHash: hash[:],
			Events: []string{
				string(platform.WebhookEventIncidentSEV1),
				string(platform.WebhookEventAnomalyFreeze),
			},
			Enabled: true,
		})
		if err != nil {
			t.Fatalf("CreateWebhook: %v", err)
		}
		if created.ID == uuid.Nil {
			t.Error("ID not populated on create")
		}
		got, err := webhooks.GetWebhook(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetWebhook: %v", err)
		}
		if got.URL != "https://hooks.slack.example/services/T/B/X" {
			t.Errorf("URL round-trip: %q", got.URL)
		}
		if len(got.Events) != 2 {
			t.Errorf("Events round-trip: %v", got.Events)
		}

		// 2. List
		listed, err := webhooks.ListWebhooksForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("ListWebhooksForAccount: %v", err)
		}
		if len(listed) != 1 {
			t.Errorf("expected 1 webhook in list, got %d", len(listed))
		}

		// 3. Update
		got.Name = "ops-slack-renamed"
		got.Enabled = false
		if err := webhooks.UpdateWebhook(ctx, got); err != nil {
			t.Fatalf("UpdateWebhook: %v", err)
		}
		after, _ := webhooks.GetWebhook(ctx, got.ID)
		if after.Name != "ops-slack-renamed" {
			t.Errorf("name not updated: %q", after.Name)
		}
		if after.Enabled {
			t.Errorf("enabled should be false after update")
		}

		// 4. EnqueueDelivery + ListPendingDeliveries
		err = webhooks.EnqueueDelivery(ctx, platform.WebhookDelivery{
			WebhookID:     created.ID,
			EventType:     string(platform.WebhookEventIncidentSEV1),
			Payload:       []byte(`{"incident_id":"abc","summary":"test"}`),
			NextAttemptAt: time.Now().UTC().Add(-time.Second), // due immediately
		})
		if err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
		pending, err := webhooks.ListPendingDeliveries(ctx, 10)
		if err != nil {
			t.Fatalf("ListPendingDeliveries: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending delivery, got %d", len(pending))
		}
		if pending[0].EventType != string(platform.WebhookEventIncidentSEV1) {
			t.Errorf("EventType round-trip: %q", pending[0].EventType)
		}
		if pending[0].AttemptCount != 0 {
			t.Errorf("AttemptCount should start at 0, got %d", pending[0].AttemptCount)
		}

		// 5. MarkAttemptFailed bumps the counter and reschedules
		nextTry := time.Now().UTC().Add(2 * time.Minute)
		if err := webhooks.MarkAttemptFailed(ctx, pending[0].ID, "503 bad gateway", 503, nextTry); err != nil {
			t.Fatalf("MarkAttemptFailed: %v", err)
		}
		// Should no longer be in the due-now list.
		pending, err = webhooks.ListPendingDeliveries(ctx, 10)
		if err != nil {
			t.Fatalf("ListPendingDeliveries after retry: %v", err)
		}
		if len(pending) != 0 {
			t.Errorf("expected 0 pending (rescheduled to future), got %d", len(pending))
		}

		// 6. MarkDelivered closes the row out
		// (write a fresh delivery + immediately mark delivered)
		err = webhooks.EnqueueDelivery(ctx, platform.WebhookDelivery{
			WebhookID:     created.ID,
			EventType:     string(platform.WebhookEventAnomalyFreeze),
			Payload:       []byte(`{}`),
			NextAttemptAt: time.Now().UTC().Add(-time.Second),
		})
		if err != nil {
			t.Fatalf("EnqueueDelivery #2: %v", err)
		}
		pending, _ = webhooks.ListPendingDeliveries(ctx, 10)
		if len(pending) != 1 {
			t.Fatalf("expected 1 fresh pending delivery, got %d", len(pending))
		}
		if err := webhooks.MarkDelivered(ctx, pending[0].ID, 200); err != nil {
			t.Fatalf("MarkDelivered: %v", err)
		}
		// 7. ListDeliveries returns the full history including the just-marked one
		hist, err := webhooks.ListDeliveries(ctx, created.ID, 10)
		if err != nil {
			t.Fatalf("ListDeliveries: %v", err)
		}
		if len(hist) != 2 {
			t.Errorf("expected 2 attempts in history, got %d", len(hist))
		}

		// 8. Delete cascades to deliveries
		if err := webhooks.DeleteWebhook(ctx, created.ID); err != nil {
			t.Fatalf("DeleteWebhook: %v", err)
		}
		if _, err := webhooks.GetWebhook(ctx, created.ID); !errors.Is(err, platform.ErrNotFound) {
			t.Errorf("expected ErrNotFound on deleted webhook, got %v", err)
		}
		histAfter, _ := webhooks.ListDeliveries(ctx, created.ID, 10)
		if len(histAfter) != 0 {
			t.Errorf("expected deliveries cascade-deleted with webhook; got %d", len(histAfter))
		}
	})
}
