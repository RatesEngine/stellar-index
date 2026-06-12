package customerwebhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/customerwebhook"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/obstest"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// fakeStore implements the worker's narrow DeliveryStore
// interface in-memory. Thread-safe so multiple ticks can interact
// with it.
type fakeStore struct {
	mu        sync.Mutex
	pending   []platform.WebhookDelivery
	webhooks  map[uuid.UUID]platform.CustomerWebhook
	delivered map[uuid.UUID]int    // delivery id → response status
	failures  map[uuid.UUID][]fail // delivery id → ordered fail records
}

type fail struct {
	msg      string
	status   int
	nextAt   time.Time
	terminal bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		webhooks:  map[uuid.UUID]platform.CustomerWebhook{},
		delivered: map[uuid.UUID]int{},
		failures:  map[uuid.UUID][]fail{},
	}
}

func (s *fakeStore) addWebhook(w platform.CustomerWebhook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhooks[w.ID] = w
}

func (s *fakeStore) enqueue(d platform.WebhookDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, d)
}

func (s *fakeStore) ListPendingDeliveries(_ context.Context, limit int) ([]platform.WebhookDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	// Drain so subsequent ticks don't re-fire on the same row.
	s.pending = nil
	return out, nil
}

func (s *fakeStore) GetWebhook(_ context.Context, id uuid.UUID) (platform.CustomerWebhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.webhooks[id]; ok {
		return w, nil
	}
	return platform.CustomerWebhook{}, platform.ErrNotFound
}

func (s *fakeStore) MarkDelivered(_ context.Context, id uuid.UUID, responseStatus int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered[id] = responseStatus
	return nil
}

func (s *fakeStore) MarkAttemptFailed(_ context.Context, id uuid.UUID, errMsg string, status int, next time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[id] = append(s.failures[id], fail{
		msg: errMsg, status: status, nextAt: next, terminal: next.IsZero(),
	})
	return nil
}

func makeWebhook(t *testing.T, url string, enabled bool) (uuid.UUID, []byte) {
	t.Helper()
	id := uuid.New()
	secret := []byte("test-secret-bytes")
	_ = id
	return id, secret
}

func runOneTick(t *testing.T, store *fakeStore, opts customerwebhook.Options) {
	t.Helper()
	// Tests target httptest.NewServer URLs on 127.0.0.1, which the
	// production SSRF-guard would reject. F-1245 (codex
	// audit-2026-05-12): supply a permissive http.Client when
	// callers haven't already, so the test suite can still verify
	// retry / status / signature behaviour against the local
	// server.
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	w := customerwebhook.New(store, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)
}

// TestWorker_DeliversOn2xx — the happy path: webhook URL returns
// 200, MarkDelivered is called with the response code, and the
// HMAC signature header is set.
func TestWorker_DeliversOn2xx(t *testing.T) {
	var (
		gotSignature string
		gotEventHdr  string
		gotBody      []byte
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-RatesEngine-Signature")
		gotEventHdr = r.Header.Get("X-RatesEngine-Event")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store := newFakeStore()
	webhookID, secret := makeWebhook(t, ts.URL, true)
	store.addWebhook(platform.CustomerWebhook{
		ID:         webhookID,
		URL:        ts.URL,
		SecretHash: secret,
		Enabled:    true,
	})
	deliveryID := uuid.New()
	payload := []byte(`{"hello":"world"}`)
	store.enqueue(platform.WebhookDelivery{
		ID:            deliveryID,
		WebhookID:     webhookID,
		EventType:     string(platform.WebhookEventIncidentSEV1),
		Payload:       payload,
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	runOneTick(t, store, customerwebhook.Options{
		PollInterval: 30 * time.Millisecond,
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if status, ok := store.delivered[deliveryID]; !ok || status != 200 {
		t.Errorf("delivery not marked OK: delivered=%v", store.delivered)
	}

	// Verify the signature matches HMAC-SHA-256(secret, body).
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSignature != want {
		t.Errorf("signature header = %q, want %q", gotSignature, want)
	}
	if gotEventHdr != string(platform.WebhookEventIncidentSEV1) {
		t.Errorf("event header = %q", gotEventHdr)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
}

// TestWorker_5xxRetryThenSchedules — non-2xx (5xx) marks the
// delivery as attempt-failed + schedules a retry (nextAt non-zero).
func TestWorker_5xxRetryThenSchedules(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	store := newFakeStore()
	webhookID, secret := makeWebhook(t, ts.URL, true)
	store.addWebhook(platform.CustomerWebhook{
		ID: webhookID, URL: ts.URL, SecretHash: secret, Enabled: true,
	})
	deliveryID := uuid.New()
	store.enqueue(platform.WebhookDelivery{
		ID: deliveryID, WebhookID: webhookID,
		EventType:     string(platform.WebhookEventAnomalyFreeze),
		Payload:       []byte(`{}`),
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	runOneTick(t, store, customerwebhook.Options{
		PollInterval: 30 * time.Millisecond,
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failures[deliveryID]) != 1 {
		t.Fatalf("expected 1 failure record, got %d", len(store.failures[deliveryID]))
	}
	f := store.failures[deliveryID][0]
	if f.status != 503 {
		t.Errorf("recorded status = %d, want 503", f.status)
	}
	if f.terminal {
		t.Errorf("5xx should schedule retry, not terminal")
	}
}

// TestWorker_4xxIsTerminal — 4xx responses don't retry. The
// customer's URL is broken (auth, validation); they need to fix
// it.
func TestWorker_4xxIsTerminal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	store := newFakeStore()
	webhookID, secret := makeWebhook(t, ts.URL, true)
	store.addWebhook(platform.CustomerWebhook{
		ID: webhookID, URL: ts.URL, SecretHash: secret, Enabled: true,
	})
	deliveryID := uuid.New()
	store.enqueue(platform.WebhookDelivery{
		ID: deliveryID, WebhookID: webhookID,
		EventType:     string(platform.WebhookEventDivergenceFiring),
		Payload:       []byte(`{}`),
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	runOneTick(t, store, customerwebhook.Options{
		PollInterval: 30 * time.Millisecond,
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	f := store.failures[deliveryID][0]
	if !f.terminal {
		t.Errorf("4xx must be terminal; failure record: %+v", f)
	}
}

// TestWorker_DisabledWebhookTerminates — when the registry row's
// Enabled=false, the worker silently terminates the delivery
// rather than retry forever.
func TestWorker_DisabledWebhookTerminates(t *testing.T) {
	store := newFakeStore()
	webhookID, secret := makeWebhook(t, "https://wherever.example", false)
	store.addWebhook(platform.CustomerWebhook{
		ID: webhookID, URL: "https://wherever.example",
		SecretHash: secret, Enabled: false, // disabled
	})
	deliveryID := uuid.New()
	store.enqueue(platform.WebhookDelivery{
		ID: deliveryID, WebhookID: webhookID,
		EventType:     string(platform.WebhookEventIncidentSEV1),
		Payload:       []byte(`{}`),
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	runOneTick(t, store, customerwebhook.Options{
		PollInterval: 30 * time.Millisecond,
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failures[deliveryID]) != 1 {
		t.Fatalf("expected 1 failure record on disabled webhook, got %d", len(store.failures[deliveryID]))
	}
	if !store.failures[deliveryID][0].terminal {
		t.Errorf("disabled webhook must terminate the delivery")
	}
	if _, ok := store.delivered[deliveryID]; ok {
		t.Errorf("disabled webhook MUST NOT mark delivered")
	}
}

// TestWorker_MissingWebhookTerminates — webhook row was deleted
// between enqueue + delivery. Mark terminal so the queue doesn't
// retry forever.
func TestWorker_MissingWebhookTerminates(t *testing.T) {
	store := newFakeStore()
	deliveryID := uuid.New()
	store.enqueue(platform.WebhookDelivery{
		ID: deliveryID, WebhookID: uuid.New(), // not in store.webhooks
		EventType:     string(platform.WebhookEventIncidentResolved),
		Payload:       []byte(`{}`),
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	runOneTick(t, store, customerwebhook.Options{
		PollInterval: 30 * time.Millisecond,
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failures[deliveryID]) != 1 {
		t.Fatalf("expected 1 failure record on missing webhook, got %d", len(store.failures[deliveryID]))
	}
	if !store.failures[deliveryID][0].terminal {
		t.Errorf("missing webhook must terminate the delivery")
	}
}

// errorsIs keeps the errors import live for future expansion.
var _ = errors.Is

// TestWorker_DeliveryDurationMetricRecorded pins the wave-88
// (2026-05-13) latency-histogram wiring: a successful delivery
// produces a sample on
// `ratesengine_customer_webhook_delivery_duration_seconds`
// labelled `outcome="delivered"`. Without this test, a future
// refactor could silently delete the timing call without any
// signal — the existing TestWorker_DeliversOn2xx asserts the
// counter side but not the histogram.
//
// Uses CollectAndCount on the metric's WithLabelValues child so
// the assertion stays independent of bucket-by-bucket values
// (which depend on test-machine performance).
func TestWorker_DeliveryDurationMetricRecorded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store := newFakeStore()
	webhookID, secret := makeWebhook(t, ts.URL, true)
	store.addWebhook(platform.CustomerWebhook{
		ID: webhookID, URL: ts.URL, SecretHash: secret, Enabled: true,
	})
	store.enqueue(platform.WebhookDelivery{
		ID:            uuid.New(),
		WebhookID:     webhookID,
		EventType:     string(platform.WebhookEventIncidentSEV1),
		Payload:       []byte(`{}`),
		NextAttemptAt: time.Now().Add(-time.Second),
	})

	// Use obstest.HistogramSampleCount because
	// HistogramVec.WithLabelValues(...) returns a
	// prometheus.Observer (not Collector) — testutil.CollectAndCount
	// can't act on the per-label child directly. The helper sums
	// sample counts across every series matching the (label key,
	// value) pair, equivalent to the wire-format `_count` suffix.
	before := obstest.HistogramSampleCount(t, obs.CustomerWebhookDeliveryDurationSeconds, "outcome", "delivered")
	runOneTick(t, store, customerwebhook.Options{PollInterval: 30 * time.Millisecond})
	after := obstest.HistogramSampleCount(t, obs.CustomerWebhookDeliveryDurationSeconds, "outcome", "delivered")

	if after <= before {
		t.Errorf("delivery duration histogram did not advance: before=%d after=%d", before, after)
	}
}
