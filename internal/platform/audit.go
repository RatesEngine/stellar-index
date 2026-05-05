package platform

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/google/uuid"
)

// ActorKind classifies who performed an audit-logged action.
type ActorKind string

const (
	ActorUser    ActorKind = "user"    // customer-account user
	ActorStaff   ActorKind = "staff"   // platform staff
	ActorSystem  ActorKind = "system"  // background worker / cron
	ActorWebhook ActorKind = "webhook" // inbound webhook (e.g. Stripe)
)

// AuditEntry is a single row in audit_log. Append-only; never
// updated or deleted (except by the offline retention archiver).
type AuditEntry struct {
	ID          uuid.UUID
	AccountID   uuid.UUID // zero for system-level actions not tied to an account
	ActorUserID uuid.UUID // zero for non-user actors
	ActorKind   ActorKind
	Action      string          // "key.mint", "plan.upgrade", "session.revoke", ...
	TargetKind  string          // "api_key", "invoice", "user", ... (optional)
	TargetID    string          // free-form ID of the target object (optional)
	Metadata    json.RawMessage // structured detail; e.g. {"from": "starter", "to": "pro"}
	IP          net.IP
	UserAgent   string
	Timestamp   time.Time
}

// AuditQuery scopes a list call.
type AuditQuery struct {
	AccountID uuid.UUID // zero = staff-mode "across everything"
	ActorKind ActorKind // empty = any kind
	Action    string    // empty = any action
	From      time.Time
	To        time.Time
	Limit     int // 0 = default 100
}

// AuditStore appends + reads audit rows. Append is fire-and-
// forget; the dashboard reads via List with a 90d window.
type AuditStore interface {
	// Append writes one row. Errors are logged by the caller
	// but don't fail the underlying action — audit-log
	// unavailability never blocks customer / staff workflows.
	Append(ctx context.Context, e AuditEntry) error

	// AppendBatch is the bulk variant for replaying captured
	// actions (e.g. data import).
	AppendBatch(ctx context.Context, entries []AuditEntry) error

	// List returns rows matching the query, ordered ts DESC.
	List(ctx context.Context, q AuditQuery) ([]AuditEntry, error)
}
