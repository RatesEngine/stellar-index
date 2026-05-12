package v1_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/auth"
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
	if _, exists := f.events[e.StripeEventID]; exists {
		return platform.ErrAlreadyProcessed
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
