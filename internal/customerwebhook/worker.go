// Package customerwebhook drains the platform.WebhookStore
// delivery queue. Each pending row gets HMAC-signed, POSTed to the
// customer's registered URL, and either marked delivered (2xx) or
// rescheduled with exponential backoff (transient failure / 5xx
// / network error). Permanent failures (4xx, attempt budget
// exhausted) leave the row with delivered_at unset + next_attempt_at
// unset so it drops out of the pending-listing predicate but
// remains visible in the dashboard delivery log.
//
// F-1270 (audit-2026-05-12). Architecture: one poll loop per
// process, configurable poll interval (default 5s), one HTTP
// client shared across deliveries. The worker is safe to run
// alongside a second instance — postgres `FOR UPDATE SKIP LOCKED`
// would normally be the deduplication primitive, but since the
// expected concurrency is "operator runs one worker" we rely on
// the worker's idempotent MarkDelivered / MarkAttemptFailed
// semantics + the dashboard's per-account scope to make
// double-delivery cosmetically harmless.
package customerwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// DeliveryStore is the worker's subset of [platform.WebhookStore].
// Pulled out so tests can substitute an in-memory fake without
// pulling the full CRUD surface.
type DeliveryStore interface {
	ListPendingDeliveries(ctx context.Context, limit int) ([]platform.WebhookDelivery, error)
	GetWebhook(ctx context.Context, id uuid.UUID) (platform.CustomerWebhook, error)
	MarkDelivered(ctx context.Context, id uuid.UUID, responseStatus int) error
	MarkAttemptFailed(ctx context.Context, id uuid.UUID, errMsg string, responseStatus int, nextAttemptAt time.Time) error
}

// Options tunes the worker. Zero values yield production defaults.
type Options struct {
	// PollInterval between queue drains. Default 5s — tight
	// enough that SEV-1 deliveries feel real-time, loose enough
	// that an idle worker doesn't hammer postgres.
	PollInterval time.Duration

	// BatchLimit caps how many deliveries each poll drains.
	// Default 100. Higher values bias toward throughput at the
	// cost of postgres lock duration per cycle.
	BatchLimit int

	// MaxAttempts before a delivery is marked permanently failed.
	// Default 15 (matches migration 0027's docblock — "Stripe-
	// style: signed deliveries, exponential retry over 72h").
	MaxAttempts int

	// HTTPClient sends the actual POST. Default has a 10s
	// per-request timeout. Inject one in tests to capture the
	// request bodies.
	HTTPClient *http.Client

	// Logger receives the worker's structured logs. Default
	// slog.Default(). Worker logs at INFO on every delivery
	// (success + failure) so operators can dashboard the
	// activity; WARN on configuration drift (webhook not found).
	Logger *slog.Logger

	// Clock is the time source. Override in tests; production
	// uses time.Now.
	Clock func() time.Time
}

// Worker drains the pending-delivery queue.
type Worker struct {
	store  DeliveryStore
	opts   Options
	stopCh chan struct{}
	doneCh chan struct{}
	signFn func(secret, payload []byte) string
}

// New constructs a Worker. store must be non-nil; opts gets
// production defaults applied to every zero field.
func New(store DeliveryStore, opts Options) *Worker {
	if store == nil {
		panic("customerwebhook: New: store must not be nil")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.BatchLimit <= 0 {
		opts.BatchLimit = 100
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 15
	}
	if opts.HTTPClient == nil {
		// F-1245 (codex audit-2026-05-12): SSRF defence at delivery
		// time. The dial hook re-resolves the host on every send so
		// DNS rebinding between registration and delivery is
		// caught here. Redirects are disabled via CheckRedirect to
		// stop a 302 from a public host pointing at an internal
		// destination from being followed automatically.
		opts.HTTPClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: ssrfGuardedDialContext,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Worker{
		store:  store,
		opts:   opts,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		signFn: signHMACSHA256,
	}
}

// Run drives the poll loop until ctx is cancelled. Returns the
// context error on shutdown. Safe to call once; calling Run twice
// on the same Worker panics.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	defer close(w.doneCh)

	// Drain once immediately so a fresh worker doesn't wait one
	// full PollInterval before processing the existing backlog.
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// Stop signals Run to exit cleanly. Returns after the in-flight
// tick (if any) completes.
func (w *Worker) Stop() {
	select {
	case <-w.stopCh:
		// already stopped
	default:
		close(w.stopCh)
	}
	<-w.doneCh
}

// tick drains one batch from the pending queue. Errors during the
// batch are logged + counted; the loop continues to the next
// delivery so one bad row doesn't stall the queue.
func (w *Worker) tick(ctx context.Context) {
	pending, err := w.store.ListPendingDeliveries(ctx, w.opts.BatchLimit)
	if err != nil {
		w.opts.Logger.Warn("customer-webhook: ListPendingDeliveries failed",
			"err", err)
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("list_error").Inc()
		return
	}
	for _, d := range pending {
		w.deliverOne(ctx, d)
	}
}

// deliverOne processes a single delivery. POSTs the payload, signs
// it, and marks delivered/failed based on the response.
func (w *Worker) deliverOne(ctx context.Context, d platform.WebhookDelivery) {
	wh, err := w.store.GetWebhook(ctx, d.WebhookID)
	if err != nil {
		// Webhook was deleted between enqueue + delivery; mark
		// the delivery as terminally failed so it drops out of
		// the pending listing.
		w.opts.Logger.Warn("customer-webhook: GetWebhook failed; permanently failing delivery",
			"err", err, "delivery_id", d.ID, "webhook_id", d.WebhookID)
		_ = w.store.MarkAttemptFailed(ctx, d.ID,
			fmt.Sprintf("webhook lookup: %v", err), 0, time.Time{})
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("webhook_missing").Inc()
		return
	}
	if !wh.Enabled {
		// Webhook is disabled — silently terminate the delivery
		// rather than retry forever.
		_ = w.store.MarkAttemptFailed(ctx, d.ID,
			"webhook disabled", 0, time.Time{})
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("disabled").Inc()
		return
	}

	signature := w.signFn(wh.SecretHash, d.Payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(d.Payload))
	if err != nil {
		// URL malformed at request-build time. This is
		// permanently broken — the URL is set per-webhook by the
		// customer; we can't fix it by retrying.
		_ = w.store.MarkAttemptFailed(ctx, d.ID,
			fmt.Sprintf("build request: %v", err), 0, time.Time{})
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("build_error").Inc()
		// Build error: no HTTP roundtrip happened. Record a
		// near-zero duration so the histogram still has a sample
		// at this label and operators see the build_error bucket
		// populate rather than disappear.
		obs.CustomerWebhookDeliveryDurationSeconds.WithLabelValues("build_error").Observe(0)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RatesEngine-Event", d.EventType)
	req.Header.Set("X-RatesEngine-Signature", "sha256="+signature)
	req.Header.Set("X-RatesEngine-Delivery-Id", d.ID.String())

	// Time the HTTP roundtrip + body drain. Recorded against the
	// outcome label so operators can chart p95/p99 latency
	// separately for delivered vs failure paths — a customer
	// endpoint that's slow but eventually 200s shows up in the
	// `delivered` bucket; a customer endpoint that's slow AND
	// 500s shows up in the `server_error` bucket. Helps isolate
	// "their endpoint is slow" from "we have a delivery problem".
	start := time.Now()
	resp, err := w.opts.HTTPClient.Do(req)
	if err != nil {
		obs.CustomerWebhookDeliveryDurationSeconds.WithLabelValues("network_error").Observe(time.Since(start).Seconds())
		w.handleFailure(ctx, d, 0, fmt.Sprintf("POST %s: %v", wh.URL, err), "network_error")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain the body so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	elapsed := time.Since(start).Seconds()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		obs.CustomerWebhookDeliveryDurationSeconds.WithLabelValues("delivered").Observe(elapsed)
		if err := w.store.MarkDelivered(ctx, d.ID, resp.StatusCode); err != nil {
			w.opts.Logger.Warn("customer-webhook: MarkDelivered failed",
				"err", err, "delivery_id", d.ID)
			obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("mark_error").Inc()
			return
		}
		w.opts.Logger.Info("customer-webhook: delivered",
			"delivery_id", d.ID, "webhook_id", d.WebhookID,
			"event_type", d.EventType, "status", resp.StatusCode)
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("delivered").Inc()
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// 4xx is the customer's responsibility (auth, bad URL,
		// validation). Don't retry — they need to fix it.
		obs.CustomerWebhookDeliveryDurationSeconds.WithLabelValues("client_error").Observe(elapsed)
		w.handleFailure(ctx, d, resp.StatusCode,
			fmt.Sprintf("HTTP %d (4xx terminal)", resp.StatusCode), "client_error")
	default:
		// 5xx + 3xx → transient, retry with backoff.
		obs.CustomerWebhookDeliveryDurationSeconds.WithLabelValues("server_error").Observe(elapsed)
		w.handleFailure(ctx, d, resp.StatusCode,
			fmt.Sprintf("HTTP %d (transient)", resp.StatusCode), "server_error")
	}
}

// handleFailure routes a non-2xx response into the right
// MarkAttemptFailed call: terminal failures clear next_attempt_at;
// transient failures schedule the next try with exponential
// backoff capped at 1 hour.
func (w *Worker) handleFailure(ctx context.Context, d platform.WebhookDelivery, status int, msg, outcome string) {
	nextAttempt := w.scheduleRetry(d.AttemptCount + 1)
	if outcome == "client_error" || d.AttemptCount+1 >= w.opts.MaxAttempts {
		// Terminal: clear schedule so the row drops out of the
		// pending list.
		nextAttempt = time.Time{}
		if outcome != "client_error" {
			outcome = "exhausted"
		}
	}
	if err := w.store.MarkAttemptFailed(ctx, d.ID, msg, status, nextAttempt); err != nil {
		w.opts.Logger.Warn("customer-webhook: MarkAttemptFailed failed",
			"err", err, "delivery_id", d.ID)
		obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues("mark_error").Inc()
		return
	}
	w.opts.Logger.Info("customer-webhook: delivery failed",
		"delivery_id", d.ID, "webhook_id", d.WebhookID,
		"event_type", d.EventType, "status", status,
		"outcome", outcome, "attempt", d.AttemptCount+1,
		"next_attempt", nextAttempt)
	obs.CustomerWebhookDeliveryAttemptsTotal.WithLabelValues(outcome).Inc()
}

// scheduleRetry returns the next attempt time given a 1-based
// attempt number. Exponential backoff: 30s, 1m, 2m, 4m, 8m, …
// capped at 1h. Caller decides whether to use this or to mark
// the delivery terminally failed.
func (w *Worker) scheduleRetry(nextAttempt int) time.Time {
	const (
		base    = 30 * time.Second
		maxWait = time.Hour
	)
	delay := base << (nextAttempt - 1) //nolint:gosec // attempt count is bounded by MaxAttempts (15)
	if delay <= 0 || delay > maxWait {
		delay = maxWait
	}
	return w.opts.Clock().Add(delay)
}

// signHMACSHA256 produces the hex-encoded HMAC-SHA-256 signature
// over `payload` using `secret`. Consumers verify by recomputing
// HMAC-SHA-256(secret, body) and comparing against the
// `X-RatesEngine-Signature: sha256=…` header.
func signHMACSHA256(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrAlreadyRunning is returned when Worker.Run is called more
// than once on the same Worker instance.
var ErrAlreadyRunning = errors.New("customerwebhook: worker already running")
