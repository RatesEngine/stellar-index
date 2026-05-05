package platform_test

import (
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/platform"
)

func TestAPIKey_IsActive(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		key        platform.APIKey
		now        time.Time
		wantActive bool
	}{
		{
			name:       "fresh key, no expiry, not revoked",
			key:        platform.APIKey{},
			now:        now,
			wantActive: true,
		},
		{
			name:       "expired",
			key:        platform.APIKey{ExpiresAt: now.Add(-time.Hour)},
			now:        now,
			wantActive: false,
		},
		{
			name:       "expires_at in the future is fine",
			key:        platform.APIKey{ExpiresAt: now.Add(time.Hour)},
			now:        now,
			wantActive: true,
		},
		{
			name:       "expires_at exactly now is expired (not Before)",
			key:        platform.APIKey{ExpiresAt: now},
			now:        now,
			wantActive: false,
		},
		{
			name:       "revoked, no expiry",
			key:        platform.APIKey{RevokedAt: now.Add(-time.Hour)},
			now:        now,
			wantActive: false,
		},
		{
			name:       "revoked AND expired",
			key:        platform.APIKey{ExpiresAt: now.Add(-time.Hour), RevokedAt: now.Add(-time.Minute)},
			now:        now,
			wantActive: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.key.IsActive(tc.now); got != tc.wantActive {
				t.Errorf("IsActive() = %v, want %v", got, tc.wantActive)
			}
		})
	}
}

func TestSubscription_IsActive(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		sub  platform.Subscription
		want bool
	}{
		{
			name: "current period active",
			sub:  platform.Subscription{CurrentPeriodEnd: now.Add(7 * 24 * time.Hour)},
			want: true,
		},
		{
			name: "period ended",
			sub:  platform.Subscription{CurrentPeriodEnd: now.Add(-time.Hour)},
			want: false,
		},
		{
			name: "scheduled to cancel later — still active until period end",
			sub: platform.Subscription{
				CurrentPeriodEnd:  now.Add(time.Hour),
				CancelAtPeriodEnd: true,
			},
			want: true,
		},
		{
			name: "already canceled",
			sub: platform.Subscription{
				CurrentPeriodEnd: now.Add(7 * 24 * time.Hour),
				CanceledAt:       now.Add(-time.Hour),
			},
			want: false,
		},
		{
			name: "canceled in the future (scheduled) — still active now",
			sub: platform.Subscription{
				CurrentPeriodEnd: now.Add(7 * 24 * time.Hour),
				CanceledAt:       now.Add(time.Hour),
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sub.IsActive(now); got != tc.want {
				t.Errorf("IsActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWebhookDelivery_IsTerminal(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name string
		d    platform.WebhookDelivery
		want bool
	}{
		{
			name: "delivered",
			d:    platform.WebhookDelivery{DeliveredAt: now},
			want: true,
		},
		{
			name: "retry exhausted (NextAttemptAt zero, not delivered)",
			d:    platform.WebhookDelivery{},
			want: true,
		},
		{
			name: "scheduled for retry",
			d:    platform.WebhookDelivery{NextAttemptAt: now.Add(5 * time.Minute)},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.IsTerminal(); got != tc.want {
				t.Errorf("IsTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}
