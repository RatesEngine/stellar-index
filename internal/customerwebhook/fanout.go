// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package customerwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// FanoutStore is the subset of [platform.WebhookStore] the fan-out
// service uses. Pulled out as a narrow interface so producers can
// substitute fakes in unit tests without standing up a full store.
type FanoutStore interface {
	ListWebhooksSubscribedTo(ctx context.Context, eventType platform.WebhookEventType) ([]platform.CustomerWebhook, error)
	EnqueueDelivery(ctx context.Context, d platform.WebhookDelivery) error
}

// Fanout enqueues one webhook delivery per subscribed enabled
// webhook when a product event fires. Producers (freeze sink,
// incident creator, divergence service) call Fanout.Publish with
// the event type + JSON payload; the service looks up every
// subscriber and inserts a pending delivery row, which the worker
// then drains.
//
// F-1249 (codex audit-2026-05-12): pre-fix the codebase shipped
// the dashboard CRUD + worker + runbook but no production caller
// inserted delivery rows. Customers could register hooks that
// never fired.
//
// All operations are best-effort: a fan-out failure (store error,
// partial enqueue success) is logged at WARN and does not
// propagate. The event that triggered fan-out is already durable
// in its own table (freeze_events, incidents, divergence_runs).
type Fanout struct {
	store  FanoutStore
	logger *slog.Logger
	clock  func() time.Time
}

// NewFanout constructs a Fanout. nil store returns nil — the
// producer call sites short-circuit on nil Fanout, matching the
// "webhook subsystem optional in this deployment" posture for
// Redis-less / Postgres-less binaries.
func NewFanout(store FanoutStore, logger *slog.Logger) *Fanout {
	if store == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Fanout{store: store, logger: logger, clock: time.Now}
}

// Publish enqueues one delivery per subscribed enabled webhook.
// `payload` must be valid JSON (caller-marshalled) and small
// enough to live comfortably in a Postgres jsonb column.
//
// Best-effort: returns nil even when no subscribers exist or
// when individual enqueue calls fail. Per-subscriber failures
// log at WARN with the webhook ID so an operator can correlate
// against the dashboard delivery log.
func (f *Fanout) Publish(ctx context.Context, eventType platform.WebhookEventType, payload []byte) {
	if f == nil {
		return
	}
	if !json.Valid(payload) {
		f.logger.Warn("customerwebhook.fanout: payload is not valid JSON; skipping",
			"event_type", eventType)
		return
	}
	subs, err := f.store.ListWebhooksSubscribedTo(ctx, eventType)
	if err != nil {
		f.logger.Warn("customerwebhook.fanout: list subscribers failed",
			"event_type", eventType, "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	enqueued := 0
	failed := 0
	for _, sub := range subs {
		d := platform.WebhookDelivery{
			ID:        uuid.New(),
			WebhookID: sub.ID,
			EventType: string(eventType),
			Payload:   payload,
			// NextAttemptAt zero is normalised by the store to "now"
			// so the worker's next poll picks it up immediately.
		}
		if err := f.store.EnqueueDelivery(ctx, d); err != nil {
			f.logger.Warn("customerwebhook.fanout: enqueue failed",
				"event_type", eventType, "webhook_id", sub.ID, "err", err)
			failed++
			continue
		}
		enqueued++
	}
	if failed > 0 {
		f.logger.Warn("customerwebhook.fanout: partial fan-out",
			"event_type", eventType, "enqueued", enqueued, "failed", failed)
	}
}

// MarshalPayload is a tiny convenience for callers that already
// have a Go struct: marshal it to JSON and return the bytes, or
// log + return nil if marshalling fails (callers should fan-out
// or skip based on the nil check).
func MarshalPayload(logger *slog.Logger, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		if logger != nil {
			logger.Warn("customerwebhook.MarshalPayload: marshal failed", "err", err)
		}
		return nil
	}
	return b
}

// ErrFanoutNotConfigured is the sentinel callers can use when
// the producer wants a typed signal rather than a nil
// short-circuit. Most call sites just check `if f != nil`.
var ErrFanoutNotConfigured = errors.New("customerwebhook: fanout not configured")

// _ guards against unused-error-variable lint when only one of the
// call sites references the sentinel.
var _ = fmt.Sprintf
