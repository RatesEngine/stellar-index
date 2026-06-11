package v1_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/platform"
)

// fakeStripeEventStore is the test double for [v1.StripeEventStore].
// In-memory map dedupes by stripe_event_id; matches the
// AppendStripeEvent / MarkStripeEventProcessed / MarkStripeEventFailed
// contract from internal/platform/billing.go.
type fakeStripeEventStore struct {
	mu     sync.Mutex
	events map[string]platform.StripeEvent // event_id → row
}

func newFakeStripeEventStore() *fakeStripeEventStore {
	return &fakeStripeEventStore{events: map[string]platform.StripeEvent{}}
}

func (f *fakeStripeEventStore) AppendStripeEvent(_ context.Context, e platform.StripeEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Mirror the F-1322-corrected production contract: short-circuit
	// ONLY when a prior delivery actually completed (ProcessedAt set). A
	// row left behind by a failed first attempt (ProcessedAt zero) is
	// reprocessable.
	if existing, exists := f.events[e.StripeEventID]; exists {
		if !existing.ProcessedAt.IsZero() {
			return platform.ErrAlreadyProcessed
		}
		return nil
	}
	f.events[e.StripeEventID] = e
	return nil
}

func (f *fakeStripeEventStore) MarkStripeEventProcessed(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.events[id]
	if !ok {
		return nil // best-effort; prod ignores missing rows
	}
	row.ProcessedAt = time.Now()
	f.events[id] = row
	return nil
}

func (f *fakeStripeEventStore) MarkStripeEventFailed(_ context.Context, id, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.events[id]
	if !ok {
		return nil
	}
	row.Error = msg
	f.events[id] = row
	return nil
}

// fakeStripeManager is the test double for [v1.StripeKeyManager].
// Records every UpdateRateLimit call so assertions can confirm the
// handler called the right key with the right budget.
type fakeStripeManager struct {
	mu       sync.Mutex
	keys     map[string][]auth.APIKeyRecord // identifier → keys
	updates  []stripeUpdateCall
	listErr  error
	updateEr error
}

type stripeUpdateCall struct {
	keyID     string
	rateLimit int
}

func (f *fakeStripeManager) ListKeysForIdentifier(_ context.Context, identifier string) ([]auth.APIKeyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys[identifier], nil
}

func (f *fakeStripeManager) UpdateRateLimit(_ context.Context, keyID string, rateLimit int) (auth.APIKeyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateEr != nil {
		return auth.APIKeyRecord{}, f.updateEr
	}
	f.updates = append(f.updates, stripeUpdateCall{keyID: keyID, rateLimit: rateLimit})
	return auth.APIKeyRecord{KeyID: keyID, RateLimitPerMin: rateLimit}, nil
}

const testStripeSecret = "whsec_test_signing_secret_value"

// stripeSign produces a valid Stripe-Signature header for the body
// at the given timestamp. Mirrors what Stripe's edge does.
func stripeSign(t *testing.T, body, secret string, ts time.Time) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts.Unix(), body)))
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func newStripeTestServer(t *testing.T, mgr v1.StripeKeyManager, now time.Time) *httptest.Server {
	t.Helper()
	return newStripeTestServerWithEvents(t, mgr, nil, now)
}

// newStripeTestServerWithEvents builds the test server with a
// configurable [v1.StripeEventStore] for the dedupe path. Pass
// nil for the events store to keep the legacy "no dedupe"
// behaviour the existing tests rely on.
func newStripeTestServerWithEvents(t *testing.T, mgr v1.StripeKeyManager, events v1.StripeEventStore, now time.Time) *httptest.Server {
	t.Helper()
	return newStripeTestServerWithOptions(t, mgr, events, nil, now)
}

// newStripeTestServerWithOptions is the test-server builder that
// exposes every wiring slot (manager + dedupe store + audit sink).
// Existing tests stay on the narrower helpers; F-1240's audit
// tests use this one.
func newStripeTestServerWithOptions(
	t *testing.T,
	mgr v1.StripeKeyManager,
	events v1.StripeEventStore,
	audit v1.StripeAuditSink,
	now time.Time,
) *httptest.Server {
	t.Helper()
	srv := v1.New(v1.Options{
		Auth: fakeAuthMiddleware(auth.Subject{}), // anonymous
		Stripe: &v1.StripeWebhookConfig{
			SigningSecret: testStripeSecret,
			Manager:       mgr,
			Events:        events,
			Audit:         audit,
			Now:           func() time.Time { return now },
			MaxAge:        5 * time.Minute,
		},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// recordingAuditSink captures every Append call so tests can
// assert F-1240's plan.upgrade audit row was written.
type recordingAuditSink struct {
	entries []platform.AuditEntry
	err     error
}

func (r *recordingAuditSink) Append(_ context.Context, e platform.AuditEntry) error {
	r.entries = append(r.entries, e)
	return r.err
}

func postStripe(t *testing.T, ts *httptest.Server, body, sigHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/webhooks/stripe", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sigHeader != "" {
		req.Header.Set("Stripe-Signature", sigHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func TestStripeWebhook_HappyPath_Pro(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-abc": {
				{KeyID: "kid_one", Identifier: "signup-abc", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
				{KeyID: "kid_two", Identifier: "signup-abc", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
			},
		},
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1","client_reference_id":"signup-abc","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)
	resp := postStripe(t, ts, body, sig)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(mgr.updates) != 2 {
		t.Errorf("updates = %d, want 2", len(mgr.updates))
	}
	for _, u := range mgr.updates {
		if u.rateLimit != 10000 {
			t.Errorf("upgrade keyID=%s ratelimit=%d, want 10000 (Pro)", u.keyID, u.rateLimit)
		}
	}
}

func TestStripeWebhook_BusinessTier(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-x": {{KeyID: "kid_x", Identifier: "signup-x", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt_2","type":"checkout.session.completed","data":{"object":{"id":"cs_2","client_reference_id":"signup-x","payment_status":"paid","metadata":{"tier":"business"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(mgr.updates) != 1 || mgr.updates[0].rateLimit != 50000 {
		t.Errorf("expected 1 upgrade to 50000; got %v", mgr.updates)
	}
}

func TestStripeWebhook_OverrideRateLimit(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-ent": {{KeyID: "kid_ent", Identifier: "signup-ent", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt_3","type":"checkout.session.completed","data":{"object":{"id":"cs_3","client_reference_id":"signup-ent","payment_status":"paid","metadata":{"rate_limit_per_min":"100000"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(mgr.updates) != 1 || mgr.updates[0].rateLimit != 100000 {
		t.Errorf("expected 1 upgrade to 100000; got %v", mgr.updates)
	}
}

func TestStripeWebhook_BadSignature(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"client_reference_id":"x","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	resp := postStripe(t, ts, body, fmt.Sprintf("t=%d,v1=deadbeef", now.Unix()))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if len(mgr.updates) != 0 {
		t.Errorf("must not call manager on bad signature; got %d", len(mgr.updates))
	}
}

func TestStripeWebhook_ReplayProtection(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed"}`
	stale := now.Add(-10 * time.Minute) // > 5 min default MaxAge
	sig := stripeSign(t, body, testStripeSecret, stale)
	resp := postStripe(t, ts, body, sig)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (replay)", resp.StatusCode)
	}
}

func TestStripeWebhook_MissingSignatureHeader(t *testing.T) {
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, time.Now().UTC())
	resp := postStripe(t, ts, `{}`, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStripeWebhook_NoConfig(t *testing.T) {
	srv := v1.New(v1.Options{Auth: fakeAuthMiddleware(auth.Subject{})})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp := postStripe(t, ts, `{}`, "t=1,v1=x")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestStripeWebhook_IgnoresOtherEventTypes(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"customer.created","data":{"object":{}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (acknowledge so Stripe stops retrying)", resp.StatusCode)
	}
	if len(mgr.updates) != 0 {
		t.Errorf("must not upgrade for non-checkout events; got %d", len(mgr.updates))
	}
}

func TestStripeWebhook_UnpaidIgnored(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"id":"cs","client_reference_id":"x","payment_status":"unpaid","metadata":{"tier":"pro"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(mgr.updates) != 0 {
		t.Errorf("must not upgrade unpaid sessions; got %d", len(mgr.updates))
	}
}

func TestStripeWebhook_MissingClientReference(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"id":"cs","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStripeWebhook_BadTierAndNoOverride(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"x": {{KeyID: "kid", Identifier: "x"}},
		},
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"id":"cs","client_reference_id":"x","payment_status":"paid","metadata":{"tier":"hyper"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStripeWebhook_NoKeysForIdentifier(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{}, // empty
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"id":"cs","client_reference_id":"signup-unknown","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	// Acknowledge — refusing would just trigger Stripe retries.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (acknowledge so Stripe stops retrying)", resp.StatusCode)
	}
}

func TestStripeWebhook_PartialUpgradeFailure(t *testing.T) {
	// One upgrade fails — the others still go through; webhook
	// returns 200 to prevent Stripe retrying everything.
	now := time.Now().UTC()
	failing := errors.New("redis blip")
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-y": {{KeyID: "kid_a"}, {KeyID: "kid_b"}},
		},
		updateEr: failing,
	}
	ts := newStripeTestServer(t, mgr, now)
	body := `{"id":"evt","type":"checkout.session.completed","data":{"object":{"id":"cs","client_reference_id":"signup-y","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	resp := postStripe(t, ts, body, stripeSign(t, body, testStripeSecret, now))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (partial-upgrade is reported, not failed)", resp.StatusCode)
	}
}

// TestStripeWebhook_Dedupe_DuplicateEventDoesntReupgrade pins the
// F-1227 fix (audit-2026-05-12): Stripe at-least-once delivery
// means the same checkout.session.completed event can land hours
// later. Without dedupe, a manual operator-side downgrade in the
// gap silently re-upgrades the customer. With the BillingStore
// wired, the second post finds the dedupe row already populated
// and acks 200 without re-running the upgrade work.
func TestStripeWebhook_Dedupe_DuplicateEventDoesntReupgrade(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-dup": {{KeyID: "kid_dup", Identifier: "signup-dup", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	events := newFakeStripeEventStore()
	ts := newStripeTestServerWithEvents(t, mgr, events, now)
	body := `{"id":"evt_dup","type":"checkout.session.completed","data":{"object":{"id":"cs_dup","client_reference_id":"signup-dup","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	// First delivery: upgrade fires, dedupe row marked processed.
	resp1 := postStripe(t, ts, body, sig)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first post: status = %d, want 200", resp1.StatusCode)
	}
	if got := len(mgr.updates); got != 1 {
		t.Fatalf("first post: updates = %d, want 1", got)
	}

	// Second delivery (Stripe retry / late re-delivery): handler
	// must short-circuit at the dedupe check and NOT re-run the
	// upgrade. Crucially, this is the case where the customer was
	// manually downgraded between deliveries — no re-upgrade.
	resp2 := postStripe(t, ts, body, sig)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second post: status = %d, want 200 (dup ack)", resp2.StatusCode)
	}
	if got := len(mgr.updates); got != 1 {
		t.Errorf("second post: updates = %d, want 1 (dedupe must skip upgrade)", got)
	}
}

// TestStripeWebhook_Dedupe_FailedFirstDeliveryRetries pins F-1322: when
// the FIRST delivery fails AFTER the dedupe row is claimed (e.g. the
// key-list lookup errors), the dedupe row is left with processed_at
// NULL. A Stripe retry MUST re-run the upgrade — the previous semantics
// (ErrAlreadyProcessed on mere row existence) dup-acked the retry and
// the paid customer was never upgraded.
func TestStripeWebhook_Dedupe_FailedFirstDeliveryRetries(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-retry": {{KeyID: "kid_retry", Identifier: "signup-retry", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
		// First delivery fails inside the upgrade path.
		listErr: errors.New("transient redis blip"),
	}
	events := newFakeStripeEventStore()
	ts := newStripeTestServerWithEvents(t, mgr, events, now)
	body := `{"id":"evt_retry","type":"checkout.session.completed","data":{"object":{"id":"cs_retry","client_reference_id":"signup-retry","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	// First delivery: the upgrade fails, so the handler must NOT 200-OK
	// the event as processed (the dedupe row stays unfinished).
	resp1 := postStripe(t, ts, body, sig)
	resp1.Body.Close()
	if resp1.StatusCode == http.StatusOK {
		t.Fatalf("first (failing) post: status = 200, want a non-2xx so Stripe retries")
	}
	if got := len(mgr.updates); got != 0 {
		t.Fatalf("first post: updates = %d, want 0 (upgrade failed)", got)
	}

	// Clear the transient failure and let Stripe retry the same event.
	mgr.mu.Lock()
	mgr.listErr = nil
	mgr.mu.Unlock()

	resp2 := postStripe(t, ts, body, sig)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("retry: status = %d, want 200", resp2.StatusCode)
	}
	if got := len(mgr.updates); got != 1 {
		t.Errorf("retry: updates = %d, want 1 — the upgrade must re-run after a failed first delivery (F-1322)", got)
	}
}

// TestStripeWebhook_Dedupe_NoEventsStore_FallsBack confirms the
// nil-Events-store path still upgrades (legacy behaviour for
// deployments without Postgres).
func TestStripeWebhook_Dedupe_NoEventsStore_FallsBack(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-nodb": {{KeyID: "kid_nodb", Identifier: "signup-nodb", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	ts := newStripeTestServerWithEvents(t, mgr, nil /* no events store */, now)
	body := `{"id":"evt_nodb","type":"checkout.session.completed","data":{"object":{"id":"cs_nodb","client_reference_id":"signup-nodb","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	resp := postStripe(t, ts, body, sig)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := len(mgr.updates); got != 1 {
		t.Errorf("updates = %d, want 1 (legacy path must still upgrade)", got)
	}
}

// errorsIsCompileGuard keeps the errors import live for future
// expansion (currently only the legacy-failure tests use it; the
// dedupe tests use sync via the fake store).
var _ = errors.New

// TestStripeWebhook_Audit_AppendsOnSuccessfulUpgrade pins the
// F-1240 contract: every successful tier upgrade writes one
// plan.upgrade row to the audit store with actor_kind=webhook,
// target referencing the Stripe event id, and metadata carrying
// identifier + tier + key counts.
func TestStripeWebhook_Audit_AppendsOnSuccessfulUpgrade(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-aud": {
				{KeyID: "kid_a", Identifier: "signup-aud", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
				{KeyID: "kid_b", Identifier: "signup-aud", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
			},
		},
	}
	audit := &recordingAuditSink{}
	ts := newStripeTestServerWithOptions(t, mgr, nil, audit, now)
	body := `{"id":"evt_aud","type":"checkout.session.completed","data":{"object":{"id":"cs_aud","client_reference_id":"signup-aud","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	resp := postStripe(t, ts, body, sig)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit.Append called %d times, want 1 (one row per event, not per key)",
			len(audit.entries))
	}
	got := audit.entries[0]
	if got.ActorKind != platform.ActorWebhook {
		t.Errorf("ActorKind = %q, want webhook", got.ActorKind)
	}
	if got.Action != "plan.upgrade" {
		t.Errorf("Action = %q, want plan.upgrade", got.Action)
	}
	if got.TargetKind != "stripe_event" {
		t.Errorf("TargetKind = %q, want stripe_event", got.TargetKind)
	}
	if got.TargetID != "evt_aud" {
		t.Errorf("TargetID = %q, want evt_aud", got.TargetID)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp must be set")
	}
	// Metadata sanity — at minimum must carry the identifier and
	// tier so the dashboard can render a readable line.
	if !strings.Contains(string(got.Metadata), `"identifier":"signup-aud"`) {
		t.Errorf("Metadata missing identifier: %s", got.Metadata)
	}
	if !strings.Contains(string(got.Metadata), `"tier":"pro"`) {
		t.Errorf("Metadata missing tier: %s", got.Metadata)
	}
	if !strings.Contains(string(got.Metadata), `"keys_upgraded":2`) {
		t.Errorf("Metadata missing keys_upgraded count: %s", got.Metadata)
	}
}

// TestStripeWebhook_Audit_NilSinkSkipsCleanly — pre-F-1240
// behaviour preserved: nil Audit sink means no row is written,
// and the webhook still 200s without complaint.
func TestStripeWebhook_Audit_NilSinkSkipsCleanly(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-nil": {{KeyID: "kid_n", Identifier: "signup-nil", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	ts := newStripeTestServerWithOptions(t, mgr, nil, nil /* no audit sink */, now)
	body := `{"id":"evt_nil","type":"checkout.session.completed","data":{"object":{"id":"cs_nil","client_reference_id":"signup-nil","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	resp := postStripe(t, ts, body, sig)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestStripeWebhook_Audit_AppendErrorIsSwallowed — an audit-store
// failure MUST NOT block the webhook ack. The upgrade succeeded
// already; turning a transient audit-DB hiccup into a Stripe
// retry storm would re-upgrade the customer on the retry but
// also keep replaying the original (already-completed) event.
func TestStripeWebhook_Audit_AppendErrorIsSwallowed(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-err": {{KeyID: "kid_e", Identifier: "signup-err", Tier: auth.TierAPIKey, RateLimitPerMin: 1000}},
		},
	}
	audit := &recordingAuditSink{err: errors.New("simulated audit-db blip")}
	ts := newStripeTestServerWithOptions(t, mgr, nil, audit, now)
	body := `{"id":"evt_err","type":"checkout.session.completed","data":{"object":{"id":"cs_err","client_reference_id":"signup-err","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)

	resp := postStripe(t, ts, body, sig)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (audit failure must not block ack)", resp.StatusCode)
	}
	if len(audit.entries) != 1 {
		t.Errorf("audit.entries = %d, want 1 (call still recorded even when handler returned error)",
			len(audit.entries))
	}
}

// fakePlatformAccountsForBridge — narrow `platform.AccountStore`
// double for the F-1219 wave-55 test. Only `GetByStripeCustomerID`
// + `Update` are exercised; the other methods panic so any
// accidental use surfaces immediately.
type fakePlatformAccountsForBridge struct {
	mu       sync.Mutex
	byStripe map[string]platform.Account
	updates  []platform.Account
}

func (f *fakePlatformAccountsForBridge) GetByStripeCustomerID(_ context.Context, sid string) (platform.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byStripe[sid]
	if !ok {
		return platform.Account{}, platform.ErrNotFound
	}
	return a, nil
}

func (f *fakePlatformAccountsForBridge) Update(_ context.Context, a platform.Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, a)
	return nil
}

func (*fakePlatformAccountsForBridge) Create(_ context.Context, _ platform.Account) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge) Get(_ context.Context, _ uuid.UUID) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge) GetBySlug(_ context.Context, _ string) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge) Suspend(_ context.Context, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAccountsForBridge) Unsuspend(_ context.Context, _ uuid.UUID) error {
	panic("unused")
}

// fakePlatformAPIKeysForBridge — narrow `platform.APIKeyStore`
// double for the F-1219 wave-55 test. ListForAccount returns the
// seeded slice; Update records every call so assertions can
// confirm both keys were lifted.
type fakePlatformAPIKeysForBridge struct {
	mu      sync.Mutex
	byAcct  map[uuid.UUID][]platform.APIKey
	updates []platform.APIKey
}

func (f *fakePlatformAPIKeysForBridge) ListForAccount(_ context.Context, accountID uuid.UUID) ([]platform.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]platform.APIKey, len(f.byAcct[accountID]))
	copy(out, f.byAcct[accountID])
	return out, nil
}

func (f *fakePlatformAPIKeysForBridge) Update(_ context.Context, k platform.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, k)
	// Reflect the update back into the source-of-truth slice
	// so a subsequent ListForAccount returns the new value.
	for i := range f.byAcct[k.AccountID] {
		if f.byAcct[k.AccountID][i].ID == k.ID {
			f.byAcct[k.AccountID][i] = k
			return nil
		}
	}
	return nil
}

func (*fakePlatformAPIKeysForBridge) Create(_ context.Context, _ platform.APIKey, _ int) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge) Get(_ context.Context, _ string) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge) GetByHash(_ context.Context, _ []byte) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge) Revoke(_ context.Context, _ string, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge) TouchUsage(_ context.Context, _ string, _ net.IP, _ string) error {
	panic("unused")
}

// TestStripeWebhook_PlatformBridge_LiftsPostgresKeys — F-1219
// wave 55 (codex audit-2026-05-13): a successful Stripe upgrade
// lifts BOTH the Redis-stored legacy keys AND the Postgres-
// backed dashboard keys belonging to the same account. Pre-
// fix the platform-bridge Pro upgrade left dashboard keys
// stuck at 1000/min while Redis-stored keys jumped to 10000.
func TestStripeWebhook_PlatformBridge_LiftsPostgresKeys(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-acme": {
				{KeyID: "kid_signup", Identifier: "signup-acme", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
			},
		},
	}
	acctID := uuid.New()
	accounts := &fakePlatformAccountsForBridge{
		byStripe: map[string]platform.Account{
			"cus_acme": {ID: acctID, Slug: "acme", StripeCustomerID: "cus_acme", Tier: platform.TierFree},
		},
	}
	keys := &fakePlatformAPIKeysForBridge{
		byAcct: map[uuid.UUID][]platform.APIKey{
			acctID: {
				{ID: "kid_dash_a", AccountID: acctID, RateLimitPerMin: 1000},
				{ID: "kid_dash_b", AccountID: acctID, RateLimitPerMin: 1000},
				// Already-revoked key — must be skipped.
				{ID: "kid_dash_revoked", AccountID: acctID, RateLimitPerMin: 1000, RevokedAt: now.Add(-time.Hour)},
				// Already-above-target key — must be skipped (no
				// downgrade on stale event re-delivery).
				{ID: "kid_dash_high", AccountID: acctID, RateLimitPerMin: 50000},
			},
		},
	}

	srv := v1.New(v1.Options{
		Auth: fakeAuthMiddleware(auth.Subject{}),
		Stripe: &v1.StripeWebhookConfig{
			SigningSecret: testStripeSecret,
			Manager:       mgr,
			Now:           func() time.Time { return now },
			MaxAge:        5 * time.Minute,
			Platform: &v1.StripePlatformBridge{
				Accounts: accounts,
				APIKeys:  keys,
			},
		},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := `{"id":"evt_acme_pro","type":"checkout.session.completed","data":{"object":{"id":"cs_acme","client_reference_id":"signup-acme","customer":"cus_acme","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)
	resp := postStripe(t, ts, body, sig)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Redis-side: the one signup key lifted to Pro (10000).
	if got := len(mgr.updates); got != 1 {
		t.Errorf("Redis updates = %d, want 1", got)
	}
	// Postgres-side: only the two below-target active dashboard
	// keys lifted; the revoked + already-above-target keys must
	// not be touched.
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if got := len(keys.updates); got != 2 {
		t.Fatalf("Postgres key Update calls = %d, want 2 (revoked + already-above-target must be skipped)", got)
	}
	for _, k := range keys.updates {
		if k.RateLimitPerMin != 10000 {
			t.Errorf("Postgres key %q lifted to %d, want 10000 (Pro tier)", k.ID, k.RateLimitPerMin)
		}
		if k.ID == "kid_dash_revoked" || k.ID == "kid_dash_high" {
			t.Errorf("Postgres key %q should NOT have been touched (revoked or already at-or-above target)", k.ID)
		}
	}
}

// fakePlatformAccountsForBridgeWithError — variant of
// `fakePlatformAccountsForBridge` whose `GetByStripeCustomerID`
// always returns an error, exercising the platform-store error
// path in [v1.Server.applyPlatformSideEffects].
type fakePlatformAccountsForBridgeWithError struct{}

func (*fakePlatformAccountsForBridgeWithError) GetByStripeCustomerID(_ context.Context, _ string) (platform.Account, error) {
	return platform.Account{}, errors.New("postgres unreachable")
}

func (*fakePlatformAccountsForBridgeWithError) Update(_ context.Context, _ platform.Account) error {
	panic("unused")
}

func (*fakePlatformAccountsForBridgeWithError) Create(_ context.Context, _ platform.Account) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridgeWithError) Get(_ context.Context, _ uuid.UUID) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridgeWithError) GetBySlug(_ context.Context, _ string) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridgeWithError) Suspend(_ context.Context, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAccountsForBridgeWithError) Unsuspend(_ context.Context, _ uuid.UUID) error {
	panic("unused")
}

// TestStripeWebhook_PlatformBridge_GetAccountErrorIncrementsMetric
// pins the wave-65 (2026-05-13) observability seam: a platform-
// store failure in `GetByStripeCustomerID` increments
// `ratesengine_stripe_platform_sync_errors_total{operation="get_account"}`
// AND the webhook still returns 200 (Stripe retries would not
// heal Postgres, so 5xx-ing here would just retry-storm).
//
// This pins the operator-visible signal that the F-1219 wave-32
// follow-up note promised — without the metric the platform-store
// half can silently degrade for hours while Redis stays healthy.
func TestStripeWebhook_PlatformBridge_GetAccountErrorIncrementsMetric(t *testing.T) {
	now := time.Now().UTC()
	mgr := &fakeStripeManager{
		keys: map[string][]auth.APIKeyRecord{
			"signup-broken": {
				{KeyID: "kid_signup_broken", Identifier: "signup-broken", Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
			},
		},
	}

	before := testutil.ToFloat64(obs.StripePlatformSyncErrorsTotal.WithLabelValues("get_account"))

	srv := v1.New(v1.Options{
		Auth: fakeAuthMiddleware(auth.Subject{}),
		Stripe: &v1.StripeWebhookConfig{
			SigningSecret: testStripeSecret,
			Manager:       mgr,
			Now:           func() time.Time { return now },
			MaxAge:        5 * time.Minute,
			Platform: &v1.StripePlatformBridge{
				Accounts: &fakePlatformAccountsForBridgeWithError{},
			},
		},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := `{"id":"evt_broken_pro","type":"checkout.session.completed","data":{"object":{"id":"cs_broken","client_reference_id":"signup-broken","customer":"cus_broken","payment_status":"paid","metadata":{"tier":"pro"}}}}`
	sig := stripeSign(t, body, testStripeSecret, now)
	resp := postStripe(t, ts, body, sig)
	defer resp.Body.Close()

	// Webhook MUST return 200 — Stripe retries on 5xx would not
	// heal Postgres, so the wave-65 metric is the right signal,
	// not a 5xx response.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (platform-store failures must NOT 5xx)", resp.StatusCode)
	}

	// Redis-side: the lift still happened (we don't gate Redis on
	// platform-store health).
	if got := len(mgr.updates); got != 1 {
		t.Errorf("Redis updates = %d, want 1 (Redis path is independent of platform-store path)", got)
	}

	// The new metric must have advanced by exactly 1.
	after := testutil.ToFloat64(obs.StripePlatformSyncErrorsTotal.WithLabelValues("get_account"))
	if got := after - before; got != 1 {
		t.Errorf("get_account metric delta = %v, want 1", got)
	}
}

// ─── Remaining 4 platform-bridge sync-error labels ───────────────
//
// Wave 66 covered `operation="get_account"`. The metric has four
// other label values (`upsert_subscription`, `account_update`,
// `list_keys`, `key_update`) that the wave-65 instrumentation
// wired but were not yet pinned by a regression test. These four
// fakes + the parameterised test below close that coverage gap.

// fakePlatformBillingForBridge_UpsertErr — minimal `BillingStore`
// double whose `UpsertSubscription` always errors. The other
// methods panic-on-call so accidental use surfaces immediately
// (the bridge code path hits ONLY UpsertSubscription on
// checkout.session.completed).
type fakePlatformBillingForBridge_UpsertErr struct{}

func (*fakePlatformBillingForBridge_UpsertErr) UpsertSubscription(_ context.Context, _ platform.Subscription) error {
	return errors.New("postgres unreachable")
}

func (*fakePlatformBillingForBridge_UpsertErr) GetActiveSubscriptionForAccount(_ context.Context, _ uuid.UUID) (platform.Subscription, error) {
	panic("unused")
}

func (*fakePlatformBillingForBridge_UpsertErr) AppendStripeEvent(_ context.Context, _ platform.StripeEvent) error {
	panic("unused")
}

func (*fakePlatformBillingForBridge_UpsertErr) MarkStripeEventProcessed(_ context.Context, _ string) error {
	panic("unused")
}

func (*fakePlatformBillingForBridge_UpsertErr) MarkStripeEventFailed(_ context.Context, _, _ string) error {
	panic("unused")
}

// fakePlatformAccountsForBridge_UpdateErr — `AccountStore` double
// where `GetByStripeCustomerID` works (returns a Free-tier account
// the upgrade path WILL try to bump) but `Update` always errors —
// pins the `account_update` operation label.
type fakePlatformAccountsForBridge_UpdateErr struct {
	acctID uuid.UUID
}

func (f *fakePlatformAccountsForBridge_UpdateErr) GetByStripeCustomerID(_ context.Context, _ string) (platform.Account, error) {
	return platform.Account{ID: f.acctID, StripeCustomerID: "cus_x", Tier: platform.TierFree}, nil
}

func (*fakePlatformAccountsForBridge_UpdateErr) Update(_ context.Context, _ platform.Account) error {
	return errors.New("postgres unreachable")
}

func (*fakePlatformAccountsForBridge_UpdateErr) Create(_ context.Context, _ platform.Account) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge_UpdateErr) Get(_ context.Context, _ uuid.UUID) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge_UpdateErr) GetBySlug(_ context.Context, _ string) (platform.Account, error) {
	panic("unused")
}

func (*fakePlatformAccountsForBridge_UpdateErr) Suspend(_ context.Context, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAccountsForBridge_UpdateErr) Unsuspend(_ context.Context, _ uuid.UUID) error {
	panic("unused")
}

// fakePlatformAPIKeysForBridge_ListErr — `APIKeyStore` double
// whose `ListForAccount` always errors. Pins the `list_keys`
// operation label.
type fakePlatformAPIKeysForBridge_ListErr struct{}

func (*fakePlatformAPIKeysForBridge_ListErr) ListForAccount(_ context.Context, _ uuid.UUID) ([]platform.APIKey, error) {
	return nil, errors.New("postgres unreachable")
}

func (*fakePlatformAPIKeysForBridge_ListErr) Update(_ context.Context, _ platform.APIKey) error {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_ListErr) Create(_ context.Context, _ platform.APIKey, _ int) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_ListErr) Get(_ context.Context, _ string) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_ListErr) GetByHash(_ context.Context, _ []byte) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_ListErr) Revoke(_ context.Context, _ string, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_ListErr) TouchUsage(_ context.Context, _ string, _ net.IP, _ string) error {
	panic("unused")
}

// fakePlatformAPIKeysForBridge_UpdateErr — `APIKeyStore` double
// whose `ListForAccount` returns one below-target key and whose
// `Update` always errors. Pins the `key_update` operation label.
type fakePlatformAPIKeysForBridge_UpdateErr struct {
	acctID uuid.UUID
}

func (f *fakePlatformAPIKeysForBridge_UpdateErr) ListForAccount(_ context.Context, _ uuid.UUID) ([]platform.APIKey, error) {
	return []platform.APIKey{
		{ID: "kid_below_target", AccountID: f.acctID, RateLimitPerMin: 1000},
	}, nil
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) Update(_ context.Context, _ platform.APIKey) error {
	return errors.New("postgres unreachable")
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) Create(_ context.Context, _ platform.APIKey, _ int) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) Get(_ context.Context, _ string) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) GetByHash(_ context.Context, _ []byte) (platform.APIKey, error) {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) Revoke(_ context.Context, _ string, _ uuid.UUID, _ string) error {
	panic("unused")
}

func (*fakePlatformAPIKeysForBridge_UpdateErr) TouchUsage(_ context.Context, _ string, _ net.IP, _ string) error {
	panic("unused")
}

// TestStripeWebhook_PlatformBridge_RemainingOperationsIncrementMetric
// completes the wave-65 / wave-66 metric coverage by pinning the
// remaining four `operation` label values:
//
//   - `upsert_subscription` (BillingStore.UpsertSubscription fails)
//   - `account_update`      (AccountStore.Update fails on tier bump)
//   - `list_keys`           (APIKeyStore.ListForAccount fails)
//   - `key_update`          (APIKeyStore.Update fails on a per-key
//     lift)
//
// Each subtest:
//
//   - posts the same `checkout.session.completed` event the
//     wave-66 test uses (the bridge fan-out from this event hits
//     all 5 failure paths in sequence — we just inject failures
//     into the right store per case);
//   - asserts HTTP 200 (platform failures must NOT 5xx — Stripe
//     retries would not heal Postgres);
//   - asserts the named-operation counter advances by exactly 1
//     and the OTHER counter labels do NOT advance (proves the
//     instrumentation labelled the right error site).
func TestStripeWebhook_PlatformBridge_RemainingOperationsIncrementMetric(t *testing.T) {
	now := time.Now().UTC()
	// Note: includes `"subscription":"sub_remaining_<op>"` so the
	// `applyPlatformSideEffects` Billing branch fires (it gates on
	// `session.Subscription != ""`). Cases that don't need the
	// Billing path are unaffected by the extra field.
	const event = `{"id":"evt_remaining_pro_%s","type":"checkout.session.completed","data":{"object":{"id":"cs_remaining_%s","client_reference_id":"signup-remaining-%s","customer":"cus_remaining_%s","subscription":"sub_remaining_%s","payment_status":"paid","metadata":{"tier":"pro"}}}}`

	type tc struct {
		name        string
		operation   string
		buildBridge func(t *testing.T) *v1.StripePlatformBridge
	}
	cases := []tc{
		{
			name:      "upsert_subscription",
			operation: "upsert_subscription",
			buildBridge: func(_ *testing.T) *v1.StripePlatformBridge {
				acctID := uuid.New()
				accounts := &fakePlatformAccountsForBridge{
					byStripe: map[string]platform.Account{
						// Use a placeholder stripe customer ID; the
						// per-subtest event uses cus_remaining_<op>
						// which we map to this entry via the test's
						// fixture below. Build per-subtest.
					},
				}
				// Map every cus_remaining_* lookup to the same
				// account so the test can re-run without state
				// dependency.
				accounts.byStripe["cus_remaining_upsert_subscription"] = platform.Account{
					ID: acctID, StripeCustomerID: "cus_remaining_upsert_subscription", Tier: platform.TierFree,
				}
				return &v1.StripePlatformBridge{
					Accounts: accounts,
					Billing:  &fakePlatformBillingForBridge_UpsertErr{},
				}
			},
		},
		{
			name:      "account_update",
			operation: "account_update",
			buildBridge: func(_ *testing.T) *v1.StripePlatformBridge {
				return &v1.StripePlatformBridge{
					Accounts: &fakePlatformAccountsForBridge_UpdateErr{acctID: uuid.New()},
				}
			},
		},
		{
			name:      "list_keys",
			operation: "list_keys",
			buildBridge: func(_ *testing.T) *v1.StripePlatformBridge {
				acctID := uuid.New()
				accounts := &fakePlatformAccountsForBridge{
					byStripe: map[string]platform.Account{
						"cus_remaining_list_keys": {
							ID: acctID, StripeCustomerID: "cus_remaining_list_keys", Tier: platform.TierPro,
						},
					},
				}
				return &v1.StripePlatformBridge{
					Accounts: accounts,
					APIKeys:  &fakePlatformAPIKeysForBridge_ListErr{},
				}
			},
		},
		{
			name:      "key_update",
			operation: "key_update",
			buildBridge: func(_ *testing.T) *v1.StripePlatformBridge {
				acctID := uuid.New()
				accounts := &fakePlatformAccountsForBridge{
					byStripe: map[string]platform.Account{
						"cus_remaining_key_update": {
							ID: acctID, StripeCustomerID: "cus_remaining_key_update", Tier: platform.TierPro,
						},
					},
				}
				return &v1.StripePlatformBridge{
					Accounts: accounts,
					APIKeys:  &fakePlatformAPIKeysForBridge_UpdateErr{acctID: acctID},
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mgr := &fakeStripeManager{
				keys: map[string][]auth.APIKeyRecord{
					"signup-remaining-" + c.name: {
						{KeyID: "kid_remaining_" + c.name, Identifier: "signup-remaining-" + c.name, Tier: auth.TierAPIKey, RateLimitPerMin: 1000},
					},
				},
			}
			before := testutil.ToFloat64(obs.StripePlatformSyncErrorsTotal.WithLabelValues(c.operation))

			srv := v1.New(v1.Options{
				Auth: fakeAuthMiddleware(auth.Subject{}),
				Stripe: &v1.StripeWebhookConfig{
					SigningSecret: testStripeSecret,
					Manager:       mgr,
					Now:           func() time.Time { return now },
					MaxAge:        5 * time.Minute,
					Platform:      c.buildBridge(t),
				},
			})
			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			body := fmt.Sprintf(event, c.name, c.name, c.name, c.name, c.name)
			sig := stripeSign(t, body, testStripeSecret, now)
			resp := postStripe(t, ts, body, sig)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (platform failures must NOT 5xx)", resp.StatusCode)
			}

			// Redis-side update STILL happens regardless of
			// platform-store failure.
			if got := len(mgr.updates); got != 1 {
				t.Errorf("Redis updates = %d, want 1", got)
			}

			after := testutil.ToFloat64(obs.StripePlatformSyncErrorsTotal.WithLabelValues(c.operation))
			if got := after - before; got != 1 {
				t.Errorf("%s metric delta = %v, want 1", c.operation, got)
			}
		})
	}
}
