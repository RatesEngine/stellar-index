package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/RatesEngine/rates-engine/internal/platform"
)

// WebhookStore implements [platform.WebhookStore] against the
// `customer_webhooks` + `webhook_deliveries` tables from migration
// 0027.
//
// F-1270 (audit-2026-05-12): the data plane for customer-facing
// incident callbacks. The delivery worker that drains the queue
// is a follow-up; this commit lands the store half so the wire is
// end-to-end-ready.
type WebhookStore struct{ s *Store }

// NewWebhookStore returns the Postgres-backed implementation.
func NewWebhookStore(s *Store) *WebhookStore {
	return &WebhookStore{s: s}
}

// Compile-time interface conformance.
var _ platform.WebhookStore = (*WebhookStore)(nil)

// CreateWebhook inserts the registry row, enforcing the per-account
// `maxPerAccount` cap atomically. F-1248 (codex audit-2026-05-12):
// the handler's pre-check (`SELECT … then INSERT`) was raceable —
// N parallel HandleCreate requests for an account at 9 webhooks
// could all pass the precheck and each insert one row, taking the
// account to 9+N.
//
// Closure shape: the create runs inside a transaction guarded by
// `pg_advisory_xact_lock(hashtext('webhook:'||account_id))`. The
// advisory lock serialises every concurrent caller for the same
// account through one critical section, so the count + insert
// observes a stable view: at most ONE statement appends a row at
// a time, and the loser's count-CTE sees the winner's INSERT and
// short-circuits via WHERE n < $7. The lock auto-releases at
// COMMIT/ROLLBACK so a crashing client can't strand it. Keyed
// by account_id, so two different accounts creating concurrently
// don't serialise against each other.
//
// `maxPerAccount` is the value passed by the handler
// (MaxWebhooksPerAccount = 10 at time of writing). Tests can pass
// a smaller value to drive the race deterministically.
func (c *WebhookStore) CreateWebhook(ctx context.Context, w platform.CustomerWebhook, maxPerAccount int) (platform.CustomerWebhook, error) {
	if w.AccountID == uuid.Nil {
		return platform.CustomerWebhook{}, errors.New("postgresstore: CreateWebhook: AccountID is empty")
	}
	if w.URL == "" {
		return platform.CustomerWebhook{}, errors.New("postgresstore: CreateWebhook: URL is empty")
	}
	if len(w.Events) == 0 {
		return platform.CustomerWebhook{}, errors.New("postgresstore: CreateWebhook: Events is empty")
	}
	if maxPerAccount <= 0 {
		maxPerAccount = 10
	}
	tx, err := c.s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.CustomerWebhook{}, fmt.Errorf("postgresstore: CreateWebhook: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// F-1248 (codex audit-2026-05-12): per-account advisory lock
	// inside the transaction. `hashtext` deterministically maps
	// 'webhook:'||account_id::text → int4 which pg_advisory_xact_lock
	// accepts as int8 via Postgres's implicit widening.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('webhook:' || $1::text))`,
		w.AccountID); err != nil {
		return platform.CustomerWebhook{}, fmt.Errorf("postgresstore: CreateWebhook: advisory lock: %w", err)
	}

	const q = `
		WITH current_count AS (
		    SELECT COUNT(*) AS n
		      FROM customer_webhooks
		     WHERE account_id = $1
		)
		INSERT INTO customer_webhooks
		    (account_id, name, url, secret_hash, events, enabled)
		SELECT $1, $2, $3, $4, $5, $6
		  FROM current_count
		 WHERE current_count.n < $7
		RETURNING id, created_at, updated_at
	`
	events := w.Events
	row := tx.QueryRowContext(ctx, q,
		w.AccountID, w.Name, w.URL, w.SecretHash,
		pq.Array(events), w.Enabled, maxPerAccount,
	)
	if err := row.Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.CustomerWebhook{}, platform.ErrWebhookQuotaExceeded
		}
		return platform.CustomerWebhook{}, fmt.Errorf("postgresstore: CreateWebhook: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.CustomerWebhook{}, fmt.Errorf("postgresstore: CreateWebhook: commit: %w", err)
	}
	return w, nil
}

// ListWebhooksForAccount returns every webhook the account has
// registered, ordered by CreatedAt desc.
func (c *WebhookStore) ListWebhooksForAccount(ctx context.Context, accountID uuid.UUID) ([]platform.CustomerWebhook, error) {
	const q = `
		SELECT id, account_id, name, url, secret_hash, events, enabled,
		       created_at, updated_at
		  FROM customer_webhooks
		 WHERE account_id = $1
		 ORDER BY created_at DESC
	`
	rows, err := c.s.db.QueryContext(ctx, q, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: ListWebhooksForAccount: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.CustomerWebhook
	for rows.Next() {
		w, err := scanWebhookRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: ListWebhooksForAccount rows: %w", err)
	}
	return out, nil
}

// ListWebhooksSubscribedTo returns every enabled webhook subscribed
// to `eventType` across all accounts. The fan-out service iterates
// the result and calls EnqueueDelivery for each. The events column
// is a text[] in Postgres; ANY($1) is the membership predicate.
// F-1249 (codex audit-2026-05-12).
func (c *WebhookStore) ListWebhooksSubscribedTo(ctx context.Context, eventType platform.WebhookEventType) ([]platform.CustomerWebhook, error) {
	const q = `
		SELECT id, account_id, name, url, secret_hash, events, enabled,
		       created_at, updated_at
		  FROM customer_webhooks
		 WHERE enabled = TRUE
		   AND $1 = ANY(events)
	`
	rows, err := c.s.db.QueryContext(ctx, q, string(eventType))
	if err != nil {
		return nil, fmt.Errorf("postgresstore: ListWebhooksSubscribedTo: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.CustomerWebhook
	for rows.Next() {
		w, err := scanWebhookRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: ListWebhooksSubscribedTo rows: %w", err)
	}
	return out, nil
}

// GetWebhook returns one row by ID. ErrNotFound when absent.
func (c *WebhookStore) GetWebhook(ctx context.Context, id uuid.UUID) (platform.CustomerWebhook, error) {
	const q = `
		SELECT id, account_id, name, url, secret_hash, events, enabled,
		       created_at, updated_at
		  FROM customer_webhooks
		 WHERE id = $1
	`
	row := c.s.db.QueryRowContext(ctx, q, id)
	w, err := scanWebhookRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.CustomerWebhook{}, platform.ErrNotFound
	}
	return w, err
}

// UpdateWebhook persists name / url / events / enabled changes.
// SecretHash + AccountID are immutable post-create.
func (c *WebhookStore) UpdateWebhook(ctx context.Context, w platform.CustomerWebhook) error {
	if w.ID == uuid.Nil {
		return errors.New("postgresstore: UpdateWebhook: ID is empty")
	}
	if len(w.Events) == 0 {
		return errors.New("postgresstore: UpdateWebhook: Events is empty")
	}
	const q = `
		UPDATE customer_webhooks
		   SET name       = $2,
		       url        = $3,
		       events     = $4,
		       enabled    = $5,
		       updated_at = now()
		 WHERE id = $1
	`
	events := w.Events
	res, err := c.s.db.ExecContext(ctx, q, w.ID, w.Name, w.URL, pq.Array(events), w.Enabled)
	if err != nil {
		return fmt.Errorf("postgresstore: UpdateWebhook %s: %w", w.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgresstore: UpdateWebhook %s rows affected: %w", w.ID, err)
	}
	if n == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// DeleteWebhook removes the row + cascades to webhook_deliveries.
func (c *WebhookStore) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM customer_webhooks WHERE id = $1`
	if _, err := c.s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("postgresstore: DeleteWebhook %s: %w", id, err)
	}
	return nil
}

// EnqueueDelivery inserts one pending delivery row.
func (c *WebhookStore) EnqueueDelivery(ctx context.Context, d platform.WebhookDelivery) error {
	if d.WebhookID == uuid.Nil {
		return errors.New("postgresstore: EnqueueDelivery: WebhookID is empty")
	}
	if d.EventType == "" {
		return errors.New("postgresstore: EnqueueDelivery: EventType is empty")
	}
	payload := d.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	const q = `
		INSERT INTO webhook_deliveries
		    (webhook_id, event_type, payload, attempt_count, next_attempt_at)
		VALUES ($1, $2, $3, 0, COALESCE(NULLIF($4, '0001-01-01 00:00:00+00'::timestamptz), now()))
	`
	if _, err := c.s.db.ExecContext(ctx, q,
		d.WebhookID, string(d.EventType), payload, d.NextAttemptAt,
	); err != nil {
		return fmt.Errorf("postgresstore: EnqueueDelivery: %w", err)
	}
	return nil
}

// ListPendingDeliveries atomically claims up to `limit` due
// deliveries FIFO. F-1247 (codex audit-2026-05-12): claim happens
// in the same statement as the read via UPDATE…RETURNING +
// `FOR UPDATE SKIP LOCKED`, so two workers running concurrently
// (horizontal scale or blue/green overlap during deploy) never
// hand the same row to two HTTP-POST paths.
//
// The lease is implemented by pushing `next_attempt_at` 5 minutes
// into the future as part of the claim. Any worker that subsequently
// runs the same query won't see the row (its next_attempt_at is now
// `now() + 5m`). On successful delivery [MarkDelivered] sets
// `delivered_at`; on failure [MarkAttemptFailed] writes the
// genuine backoff back into next_attempt_at. If a worker crashes
// after claiming but before either update, the lease expires after
// 5 minutes and another worker can pick the row up — that's
// idempotent because the receiver-side dedupe (event_id header)
// catches it; and customer-side metrics treat
// duplicate-post-after-worker-crash as the same class as 5xx-retry.
//
// FIFO ordering is preserved via the `ORDER BY next_attempt_at ASC`
// inside the SELECT subquery.
func (c *WebhookStore) ListPendingDeliveries(ctx context.Context, limit int) ([]platform.WebhookDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	const q = `
		WITH claimed AS (
		    SELECT id
		      FROM webhook_deliveries
		     WHERE delivered_at IS NULL
		       AND next_attempt_at IS NOT NULL
		       AND next_attempt_at <= now()
		     ORDER BY next_attempt_at ASC
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		)
		UPDATE webhook_deliveries
		   SET next_attempt_at = now() + interval '5 minutes'
		  FROM claimed
		 WHERE webhook_deliveries.id = claimed.id
		RETURNING webhook_deliveries.id,
		          webhook_deliveries.webhook_id,
		          webhook_deliveries.event_type,
		          webhook_deliveries.payload,
		          webhook_deliveries.attempt_count,
		          COALESCE(webhook_deliveries.next_attempt_at, '0001-01-01 00:00:00+00'::timestamptz),
		          COALESCE(webhook_deliveries.delivered_at,    '0001-01-01 00:00:00+00'::timestamptz),
		          COALESCE(webhook_deliveries.last_error, ''),
		          COALESCE(webhook_deliveries.last_response_status, 0),
		          webhook_deliveries.created_at
	`
	rows, err := c.s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: ListPendingDeliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.WebhookDelivery
	for rows.Next() {
		d, err := scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: ListPendingDeliveries rows: %w", err)
	}
	return out, nil
}

// MarkDelivered records a successful POST.
func (c *WebhookStore) MarkDelivered(ctx context.Context, id uuid.UUID, responseStatus int) error {
	const q = `
		UPDATE webhook_deliveries
		   SET delivered_at         = now(),
		       attempt_count        = attempt_count + 1,
		       next_attempt_at      = NULL,
		       last_response_status = $2,
		       last_error           = NULL
		 WHERE id = $1
	`
	if _, err := c.s.db.ExecContext(ctx, q, id, responseStatus); err != nil {
		return fmt.Errorf("postgresstore: MarkDelivered %s: %w", id, err)
	}
	return nil
}

// MarkAttemptFailed records a failed POST + schedules the next try.
func (c *WebhookStore) MarkAttemptFailed(ctx context.Context, id uuid.UUID, errMsg string, responseStatus int, nextAttemptAt time.Time) error {
	// nextAttemptAt zero → permanently failed: clear next_attempt_at
	// so the row drops out of the pending-listing predicate.
	var nextArg any
	if !nextAttemptAt.IsZero() {
		nextArg = nextAttemptAt
	}
	const q = `
		UPDATE webhook_deliveries
		   SET attempt_count        = attempt_count + 1,
		       last_error           = $2,
		       last_response_status = $3,
		       next_attempt_at      = $4
		 WHERE id = $1
	`
	if _, err := c.s.db.ExecContext(ctx, q, id, errMsg, responseStatus, nextArg); err != nil {
		return fmt.Errorf("postgresstore: MarkAttemptFailed %s: %w", id, err)
	}
	return nil
}

// ─── helpers ────────────────────────────────────────────────────

// rowScanner is the subset of *sql.Row + *sql.Rows that
// scanWebhookRow + scanDeliveryRow need. Lets one helper handle
// both single-row and rows-iterator paths.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanWebhookRow(s rowScanner) (platform.CustomerWebhook, error) {
	var (
		w      platform.CustomerWebhook
		events pq.StringArray
	)
	if err := s.Scan(
		&w.ID, &w.AccountID, &w.Name, &w.URL, &w.SecretHash,
		&events, &w.Enabled, &w.CreatedAt, &w.UpdatedAt,
	); err != nil {
		return platform.CustomerWebhook{}, fmt.Errorf("postgresstore: scan webhook: %w", err)
	}
	w.Events = []string(events)
	return w, nil
}

func scanDeliveryRow(s rowScanner) (platform.WebhookDelivery, error) {
	var (
		d         platform.WebhookDelivery
		eventType string
	)
	if err := s.Scan(
		&d.ID, &d.WebhookID, &eventType, &d.Payload,
		&d.AttemptCount,
		&d.NextAttemptAt, &d.DeliveredAt,
		&d.LastError, &d.LastResponseStatus,
		&d.CreatedAt,
	); err != nil {
		return platform.WebhookDelivery{}, fmt.Errorf("postgresstore: scan delivery: %w", err)
	}
	d.EventType = string(eventType)
	// COALESCE pushed sentinel zero-times for nullable columns;
	// translate them back to Go zero-value time.Time so callers
	// can use IsZero() consistently.
	if d.NextAttemptAt.Year() == 1 {
		d.NextAttemptAt = time.Time{}
	}
	if d.DeliveredAt.Year() == 1 {
		d.DeliveredAt = time.Time{}
	}
	return d, nil
}

// ─── Dashboard-flow surfaces (extends EnqueueDelivery/MarkDelivered) ─────

// RotateWebhookSecret replaces the signing secret. Returns the new
// plaintext.
//
// At-rest model: the bytes ARE persisted as the canonical
// `customer_webhooks.secret_hash` bytea — the delivery worker
// reads them back to sign future requests, identical to the
// create path. The field name is a historical misnomer
// (originally promised hash-only persistence, the implementation
// always stored the live HMAC key); see
// [platform.CustomerWebhook] for the full at-rest discussion
// including the F-1244 (codex audit-2026-05-13) reconciliation.
//
// Customer-facing visibility: the plaintext is returned by this
// call exactly once and never served back through any
// subsequent read. That one-time-visibility property is what
// "shown once" referred to in the prior docstring; it is NOT
// the same as "never persisted to disk", and the prior
// conflation of the two was the gap F-1244 closed.
//
// Stub: today returns "" + a not-implemented error. Customers
// rotate by deleting + recreating the webhook (which already
// works via the create path), so the in-place rotation surface
// isn't on the critical path. The interface seam stays here so
// the v2 dashboard can plug in-place rotation without
// re-shaping the store boundary.
func (c *WebhookStore) RotateWebhookSecret(ctx context.Context, id uuid.UUID) (string, error) {
	_ = ctx
	_ = id
	return "", errors.New("postgresstore: RotateWebhookSecret not yet implemented (dashboard rotates by delete + recreate today; the v2 in-place path lands when the dashboard CRUD UI ships it)")
}

// AppendDelivery records one delivery attempt. Returns the
// inserted row with `ID` + `CreatedAt` populated. Used by the
// dashboard-flow path where the caller has the full attempt state
// already (vs EnqueueDelivery which seeds a fresh queue row).
func (c *WebhookStore) AppendDelivery(ctx context.Context, d platform.WebhookDelivery) (platform.WebhookDelivery, error) {
	if d.WebhookID == uuid.Nil {
		return platform.WebhookDelivery{}, errors.New("postgresstore: AppendDelivery: WebhookID is empty")
	}
	payload := d.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	var (
		deliveredAt    any
		nextAttemptAt  any
		responseStatus any
		lastError      any
	)
	if !d.DeliveredAt.IsZero() {
		deliveredAt = d.DeliveredAt
	}
	if !d.NextAttemptAt.IsZero() {
		nextAttemptAt = d.NextAttemptAt
	}
	if d.LastResponseStatus != 0 {
		responseStatus = d.LastResponseStatus
	}
	if d.LastError != "" {
		lastError = d.LastError
	}
	const q = `
		INSERT INTO webhook_deliveries
		    (webhook_id, event_type, payload,
		     attempt_count, next_attempt_at, delivered_at,
		     last_error, last_response_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`
	row := c.s.db.QueryRowContext(ctx, q,
		d.WebhookID, string(d.EventType), payload,
		d.AttemptCount, nextAttemptAt, deliveredAt,
		lastError, responseStatus,
	)
	if err := row.Scan(&d.ID, &d.CreatedAt); err != nil {
		return platform.WebhookDelivery{}, fmt.Errorf("postgresstore: AppendDelivery: %w", err)
	}
	return d, nil
}

// UpdateDelivery rewrites the attempt-state fields. Idempotent; the
// row is keyed by ID and only the mutable fields are touched.
func (c *WebhookStore) UpdateDelivery(ctx context.Context, d platform.WebhookDelivery) error {
	if d.ID == uuid.Nil {
		return errors.New("postgresstore: UpdateDelivery: ID is empty")
	}
	var (
		deliveredAt    any
		nextAttemptAt  any
		responseStatus any
		lastError      any
	)
	if !d.DeliveredAt.IsZero() {
		deliveredAt = d.DeliveredAt
	}
	if !d.NextAttemptAt.IsZero() {
		nextAttemptAt = d.NextAttemptAt
	}
	if d.LastResponseStatus != 0 {
		responseStatus = d.LastResponseStatus
	}
	if d.LastError != "" {
		lastError = d.LastError
	}
	const q = `
		UPDATE webhook_deliveries
		   SET attempt_count        = $2,
		       next_attempt_at      = $3,
		       delivered_at         = $4,
		       last_error           = $5,
		       last_response_status = $6
		 WHERE id = $1
	`
	if _, err := c.s.db.ExecContext(ctx, q,
		d.ID, d.AttemptCount, nextAttemptAt, deliveredAt,
		lastError, responseStatus,
	); err != nil {
		return fmt.Errorf("postgresstore: UpdateDelivery %s: %w", d.ID, err)
	}
	return nil
}

// ListDeliveries returns the most-recent `limit` attempts for one
// webhook, newest first. Used by the dashboard delivery log.
func (c *WebhookStore) ListDeliveries(ctx context.Context, webhookID uuid.UUID, limit int) ([]platform.WebhookDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	const q = `
		SELECT id, webhook_id, event_type, payload, attempt_count,
		       COALESCE(next_attempt_at, '0001-01-01 00:00:00+00'::timestamptz),
		       COALESCE(delivered_at,    '0001-01-01 00:00:00+00'::timestamptz),
		       COALESCE(last_error, ''),
		       COALESCE(last_response_status, 0),
		       created_at
		  FROM webhook_deliveries
		 WHERE webhook_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`
	rows, err := c.s.db.QueryContext(ctx, q, webhookID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: ListDeliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []platform.WebhookDelivery
	for rows.Next() {
		d, err := scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: ListDeliveries rows: %w", err)
	}
	return out, nil
}
