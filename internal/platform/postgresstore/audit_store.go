package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RatesEngine/rates-engine/internal/platform"
)

// AuditStore implements [platform.AuditStore] against the
// `audit_log` table from migration 0027.
//
// Append is the load-bearing operation — every privileged action
// (key.mint, plan.upgrade, session.revoke, …) lands one row. List
// powers the dashboard's audit-trail surface + the operator's
// staff-mode "across everything" view.
//
// All writes are fire-and-forget from the caller's perspective:
// audit-log unavailability never blocks customer / staff workflows
// (the caller logs the error + continues). F-1240 wired the Stripe
// webhook into this path for tier-upgrade visibility.
type AuditStore struct{ s *Store }

// NewAuditStore returns the Postgres-backed implementation.
func NewAuditStore(s *Store) *AuditStore { return &AuditStore{s: s} }

// Compile-time interface conformance.
var _ platform.AuditStore = (*AuditStore)(nil)

// Append inserts one audit_log row. Returns nil even if the
// account_id or actor_user_id reference doesn't exist (the FK is
// ON DELETE SET NULL — the row stays, just unlinked).
func (a *AuditStore) Append(ctx context.Context, e platform.AuditEntry) error {
	if e.Action == "" {
		return errors.New("postgresstore: Append: AuditEntry.Action is empty")
	}
	if e.ActorKind == "" {
		return errors.New("postgresstore: Append: AuditEntry.ActorKind is empty")
	}
	const q = `
		INSERT INTO audit_log
		    (id, account_id, actor_user_id, actor_kind,
		     action, target_kind, target_id, metadata,
		     ip, user_agent, ts)
		VALUES ($1, $2, $3, $4,
		        $5, $6, $7, $8,
		        $9, $10, COALESCE(NULLIF($11, '0001-01-01 00:00:00+00'::timestamptz), now()))
	`
	id := e.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	_, err := a.s.db.ExecContext(ctx, q,
		id,
		nullableUUID(e.AccountID),
		nullableUUID(e.ActorUserID),
		string(e.ActorKind),
		e.Action,
		nullableString(e.TargetKind),
		nullableString(e.TargetID),
		nullableJSONB(e.Metadata),
		nullableInet(e.IP),
		nullableString(e.UserAgent),
		e.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("postgresstore: Append audit %s: %w", e.Action, err)
	}
	return nil
}

// AppendBatch is the bulk variant — currently a loop over Append
// since the audit_log volume is low (< 100 rows / sec at design
// scale). A future high-throughput caller can introduce a COPY-
// based fast path without changing the interface.
func (a *AuditStore) AppendBatch(ctx context.Context, entries []platform.AuditEntry) error {
	for i, e := range entries {
		if err := a.Append(ctx, e); err != nil {
			return fmt.Errorf("postgresstore: AppendBatch entry %d: %w", i, err)
		}
	}
	return nil
}

// List returns rows matching the query, ordered ts DESC. Limit
// defaults to 100 when zero, capped at 1000 (the dashboard paginates;
// the staff console asks for narrower windows).
func (a *AuditStore) List(ctx context.Context, q platform.AuditQuery) ([]platform.AuditEntry, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	whereSQL, args := buildAuditWhere(q)

	// limit is clamped to [1, 1000] above; fmt.Sprintf only sees an
	// integer literal from a code-controlled clamp, never user input
	// — gosec G202 is a false positive here.
	//nolint:gosec // limit is clamped to [1,1000]; whereSQL built only with positional placeholders
	query := "SELECT id, account_id, actor_user_id, actor_kind, " +
		"action, target_kind, target_id, metadata, host(ip), user_agent, ts " +
		"FROM audit_log " + whereSQL +
		" ORDER BY ts DESC LIMIT " + strconv.Itoa(limit)

	rows, err := a.s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: List audit: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]platform.AuditEntry, 0, limit)
	for rows.Next() {
		entry, err := scanAuditRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: List audit rows: %w", err)
	}
	return out, nil
}

// buildAuditWhere translates [platform.AuditQuery] into a WHERE
// clause + positional-arg slice. Empty query → empty WHERE +
// nil args.
func buildAuditWhere(q platform.AuditQuery) (string, []any) {
	var (
		args  []any
		where []string
	)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.AccountID != uuid.Nil {
		where = append(where, "account_id = "+addArg(q.AccountID))
	}
	if q.ActorKind != "" {
		where = append(where, "actor_kind = "+addArg(string(q.ActorKind)))
	}
	if q.Action != "" {
		where = append(where, "action = "+addArg(q.Action))
	}
	if !q.From.IsZero() {
		where = append(where, "ts >= "+addArg(q.From))
	}
	if !q.To.IsZero() {
		where = append(where, "ts < "+addArg(q.To))
	}
	if len(where) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(where, " AND "), args
}

// scanAuditRow reads one audit_log row off the cursor and maps it
// into the typed [platform.AuditEntry] shape (nullable columns,
// inet → net.IP, jsonb pass-through).
func scanAuditRow(rows *sql.Rows) (platform.AuditEntry, error) {
	var (
		e           platform.AuditEntry
		accountID   sql.NullString
		actorUserID sql.NullString
		actorKind   string
		targetKind  sql.NullString
		targetID    sql.NullString
		metadata    []byte
		ip          sql.NullString
		userAgent   sql.NullString
		ts          time.Time
	)
	if err := rows.Scan(
		&e.ID,
		&accountID,
		&actorUserID,
		&actorKind,
		&e.Action,
		&targetKind,
		&targetID,
		&metadata,
		&ip,
		&userAgent,
		&ts,
	); err != nil {
		return platform.AuditEntry{}, fmt.Errorf("postgresstore: List audit scan: %w", err)
	}
	if accountID.Valid {
		if id, err := uuid.Parse(accountID.String); err == nil {
			e.AccountID = id
		}
	}
	if actorUserID.Valid {
		if id, err := uuid.Parse(actorUserID.String); err == nil {
			e.ActorUserID = id
		}
	}
	e.ActorKind = platform.ActorKind(actorKind)
	if targetKind.Valid {
		e.TargetKind = targetKind.String
	}
	if targetID.Valid {
		e.TargetID = targetID.String
	}
	if len(metadata) > 0 {
		e.Metadata = metadata
	}
	if ip.Valid {
		e.IP = net.ParseIP(ip.String)
	}
	if userAgent.Valid {
		e.UserAgent = userAgent.String
	}
	e.Timestamp = ts
	return e, nil
}

// ─── small helpers ──────────────────────────────────────────────

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableJSONB(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullableInet(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}
