package timescale

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// IssuerRow is the read-side projection of one row from the
// `issuers` table. Auth flags are pointers so callers can
// distinguish "we know the value" from "no observation yet."
type IssuerRow struct {
	GStrkey        string
	HomeDomain     string
	AuthRequired   *bool
	AuthRevocable  *bool
	AuthImmutable  *bool
	AuthClawback   *bool
	SEP1ResolvedAt *string // RFC 3339; pointer for nullable column
	SEP1Payload    json.RawMessage
	CreationLedger *uint32
}

// GetIssuer returns the row for one G-strkey. Returns sql.ErrNoRows
// when the issuer hasn't been observed yet.
func (s *Store) GetIssuer(ctx context.Context, gStrkey string) (IssuerRow, error) {
	const q = `
		SELECT
		    g_strkey,
		    COALESCE(home_domain, ''),
		    auth_required,
		    auth_revocable,
		    auth_immutable,
		    auth_clawback,
		    to_char(sep1_resolved_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		    sep1_payload,
		    creation_ledger
		  FROM issuers
		 WHERE g_strkey = $1
	`
	var (
		row              IssuerRow
		authReq, authRev sql.NullBool
		authImm, authClb sql.NullBool
		resolvedAt       sql.NullString
		payload          sql.NullString
		creation         sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, q, gStrkey).Scan(
		&row.GStrkey,
		&row.HomeDomain,
		&authReq, &authRev, &authImm, &authClb,
		&resolvedAt, &payload, &creation,
	)
	if err != nil {
		return IssuerRow{}, err
	}
	if authReq.Valid {
		v := authReq.Bool
		row.AuthRequired = &v
	}
	if authRev.Valid {
		v := authRev.Bool
		row.AuthRevocable = &v
	}
	if authImm.Valid {
		v := authImm.Bool
		row.AuthImmutable = &v
	}
	if authClb.Valid {
		v := authClb.Bool
		row.AuthClawback = &v
	}
	if resolvedAt.Valid {
		v := resolvedAt.String
		row.SEP1ResolvedAt = &v
	}
	if payload.Valid {
		row.SEP1Payload = json.RawMessage(payload.String)
	}
	if creation.Valid {
		v := uint32(creation.Int64) //nolint:gosec
		row.CreationLedger = &v
	}
	return row, nil
}

// IssuerSummary is one entry in the issuer-directory listing —
// the (g_strkey, optional home_domain, total observation count
// across all issued assets, asset count) tuple. Returned by
// [Store.ListIssuers].
type IssuerSummary struct {
	GStrkey               string
	HomeDomain            string
	AssetCount            int64
	TotalObservationCount int64
}

// ListIssuers returns the issuer directory ordered by total
// observation count desc — the proxy-for-activity ranking the
// /v1/issuers endpoint exposes. limit clamps to [1, 500].
//
// Joins issuers with classic_assets and aggregates so the
// home_domain (when populated by the SEP-1 fetcher) flows through
// without a per-row lookup. issuers without any classic_assets row
// are excluded — without an asset, an issuer entry is just an
// orphan G-strkey we have no activity for.
func (s *Store) ListIssuers(ctx context.Context, limit int) ([]IssuerSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT i.g_strkey,
               COALESCE(i.home_domain, ''),
               count(c.asset_id)::bigint           AS asset_count,
               COALESCE(sum(c.observation_count), 0)::bigint AS total_obs
          FROM issuers i
          JOIN classic_assets c ON c.issuer_g_strkey = i.g_strkey
         GROUP BY i.g_strkey, i.home_domain
         ORDER BY total_obs DESC, i.g_strkey ASC
         LIMIT $1
    `
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListIssuers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]IssuerSummary, 0, limit)
	for rows.Next() {
		var r IssuerSummary
		if err := rows.Scan(&r.GStrkey, &r.HomeDomain, &r.AssetCount, &r.TotalObservationCount); err != nil {
			return nil, fmt.Errorf("timescale: ListIssuers scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListIssuers rows: %w", err)
	}
	return out, nil
}

// IssuerAsset is one entry in the issuer's asset list.
type IssuerAsset struct {
	AssetID          string
	Code             string
	Slug             string
	FirstSeenLedger  uint32
	LastSeenLedger   uint32
	ObservationCount int64
}

// ListIssuerAssets returns every classic asset issued by the given
// G-strkey, ordered by observation count desc (a cheap activity
// proxy).
func (s *Store) ListIssuerAssets(ctx context.Context, gStrkey string) ([]IssuerAsset, error) {
	const q = `
		SELECT
		    asset_id,
		    code,
		    COALESCE(slug, code),
		    first_seen_ledger,
		    last_seen_ledger,
		    observation_count
		  FROM classic_assets
		 WHERE issuer_g_strkey = $1
		 ORDER BY observation_count DESC, asset_id ASC
	`
	rows, err := s.db.QueryContext(ctx, q, gStrkey)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListIssuerAssets %s: %w", gStrkey, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]IssuerAsset, 0, 8)
	for rows.Next() {
		var a IssuerAsset
		var first, last int64
		if err := rows.Scan(&a.AssetID, &a.Code, &a.Slug, &first, &last, &a.ObservationCount); err != nil {
			return nil, fmt.Errorf("timescale: ListIssuerAssets scan: %w", err)
		}
		a.FirstSeenLedger = uint32(first) //nolint:gosec
		a.LastSeenLedger = uint32(last)   //nolint:gosec
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListIssuerAssets rows: %w", err)
	}
	return out, nil
}
