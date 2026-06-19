package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// IssuersReader is the seam the issuer handlers read through.
// timescale.Store satisfies it via GetIssuer + ListIssuerAssets +
// ListIssuers.
type IssuersReader interface {
	GetIssuer(ctx context.Context, gStrkey string) (timescale.IssuerRow, error)
	ListIssuerAssets(ctx context.Context, gStrkey string) ([]timescale.IssuerAsset, error)
	ListIssuers(ctx context.Context, limit int) ([]timescale.IssuerSummary, error)
}

// IssuerListEntry is the wire shape of one row in /v1/issuers.
// Compact summary suitable for the issuer-directory page.
//
// OrgName is the issuer's organisation name from SEP-1
// (`[DOCUMENTATION].ORG_NAME` in stellar.toml). Populated by
// the `stellarindex-ops sep1-refresh` job; empty when never
// resolved or when the issuer has no documentation block.
type IssuerListEntry struct {
	GStrkey               string `json:"g_strkey"`
	HomeDomain            string `json:"home_domain,omitempty"`
	OrgName               string `json:"org_name,omitempty"`
	AssetCount            int64  `json:"asset_count"`
	TotalObservationCount int64  `json:"total_observation_count"`
	// ScamReason is non-empty when the issuer is flagged as scam /
	// malicious by the curated `known_scams.go` map (sourced from
	// stellar.expert's directory). Clients should render a warning
	// badge — this issuer's assets shouldn't be trusted.
	ScamReason string `json:"scam_reason,omitempty"`
}

// Issuer is the wire shape returned by /v1/issuers/{g_strkey}.
type Issuer struct {
	GStrkey    string `json:"g_strkey"`
	HomeDomain string `json:"home_domain,omitempty"`
	// OrgName is the issuer's organisation name extracted from
	// SEP-1 (`[DOCUMENTATION].ORG_NAME`). Same field as the
	// listing endpoint surfaces; populated by the
	// `stellarindex-ops sep1-refresh` job.
	OrgName string `json:"org_name,omitempty"`
	// ScamReason is non-empty when the issuer is flagged as scam /
	// malicious by the curated `known_scams.go` map (sourced from
	// stellar.expert's directory).
	ScamReason     string          `json:"scam_reason,omitempty"`
	AuthRequired   *bool           `json:"auth_required,omitempty"`
	AuthRevocable  *bool           `json:"auth_revocable,omitempty"`
	AuthImmutable  *bool           `json:"auth_immutable,omitempty"`
	AuthClawback   *bool           `json:"auth_clawback,omitempty"`
	SEP1ResolvedAt *string         `json:"sep1_resolved_at,omitempty"`
	SEP1Payload    json.RawMessage `json:"sep1_payload,omitempty"`
	CreationLedger *uint32         `json:"creation_ledger,omitempty"`
	Assets         []IssuedAsset   `json:"assets,omitempty"`
}

// IssuedAsset is one entry in the issuer's `assets` list.
type IssuedAsset struct {
	AssetID          string `json:"asset_id"`
	Code             string `json:"code"`
	Slug             string `json:"slug"`
	FirstSeenLedger  uint32 `json:"first_seen_ledger"`
	LastSeenLedger   uint32 `json:"last_seen_ledger"`
	ObservationCount int64  `json:"observation_count"`
}

// handleIssuersList serves GET /v1/issuers.
//
// Returns the issuer directory ordered by total observation count
// across the issuer's classic assets — the proxy-for-activity
// ranking the explorer /issuers page exposes. Returns 503 when
// no IssuersReader is wired and 400 on out-of-range limit.
func (s *Server) handleIssuersList(w http.ResponseWriter, r *http.Request) {
	if s.issuers == nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/issuers-unavailable",
			"Issuers unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the issuer reader yet.")
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be 1-500")
			return
		}
		limit = n
	}
	// 8s ceiling — same pattern as the cold-path series.
	listCtx, listCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer listCancel()
	rows, err := s.issuers.ListIssuers(listCtx, limit)
	if err != nil {
		if clientAborted(r, err) {
			// Client went away mid-query — e.g. concurrent callers
			// (the sla-probe, a browser navigating away) that cancel
			// in-flight requests. lib/pq surfaces the resulting
			// context cancellation as `canceling statement due to
			// user request` (SQLSTATE 57014); that is NOT a server
			// fault. Return quietly: no ERROR log, no 500 (which
			// otherwise pollutes the 5xx rate + SLA availability).
			// Same canonical ordering as handleObservations
			// (envelope.go clientAborted doc).
			return
		}
		if handlerTimedOut(listCtx, err) {
			s.logger.Warn("ListIssuers deadline exceeded", "limit", limit)
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/issuers-timeout",
				"Issuers list timed out", http.StatusServiceUnavailable,
				"the issuer registry scan didn't return in 8s; retry shortly.")
			return
		}
		if transientStorageErr(err) {
			// Postgres-side cancellation (57014 from statement_timeout
			// / lock_timeout / idle_in_transaction reset), bad-connection
			// after pool retries exhausted, or a network EOF. These
			// are infrastructure transients that a retry would succeed
			// on — surface 503 so sla-probe doesn't book it as a
			// permanent availability failure. #34 residual.
			s.logger.Warn("issuers list: transient storage error", "err", err)
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/issuers-transient",
				"Issuers list temporarily unavailable", http.StatusServiceUnavailable,
				"the storage layer hit a transient error; retry shortly.")
			return
		}
		s.logger.Warn("issuers list", "err", err)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/issuers-error",
			"Issuers list failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}
	out := make([]IssuerListEntry, len(rows))
	for i, r := range rows {
		homeDomain, orgName := enrichIssuer(r.GStrkey, r.HomeDomain, r.OrgName)
		out[i] = IssuerListEntry{
			GStrkey:               r.GStrkey,
			HomeDomain:            homeDomain,
			OrgName:               orgName,
			AssetCount:            r.AssetCount,
			TotalObservationCount: r.TotalObservationCount,
			ScamReason:            scamReason(r.GStrkey),
		}
	}
	writeJSON(w, out, Flags{})
}

// handleIssuer serves GET /v1/issuers/{g_strkey}.
//
// Returns 404 (problem+json) when the issuer has never been observed.
// Always includes the assets array so the explorer issuer card has
// the per-issuer drill-down data without a second request.
func (s *Server) handleIssuer(w http.ResponseWriter, r *http.Request) {
	if s.issuers == nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/issuers-unavailable",
			"Issuers unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the issuer reader yet.")
		return
	}

	gStrkey := r.PathValue("g_strkey")
	if gStrkey == "" {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-g-strkey",
			"Invalid G-strkey", http.StatusBadRequest,
			"g_strkey path segment is required")
		return
	}
	// Stellar G-strkeys are uppercase base32 by SEP-23 convention;
	// the storage layer keys off the canonical uppercase form.
	// URL clients (chat clients, search tools, manual typing)
	// regularly lowercase, which used to 404 outright. Normalise
	// at input — base32 alphabet is case-insensitive in Stellar
	// SDK validation, so the underlying ed25519 public key is the
	// same. No risk of merging two distinct accounts.
	gStrkey = strings.ToUpper(gStrkey)

	// 8s ceiling spans both calls — GetIssuer is fast but the
	// fan-out to ListIssuerAssets can hit the trades hypertable
	// for the per-asset observation count.
	iCtx, iCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer iCancel()
	row, err := s.issuers.GetIssuer(iCtx, gStrkey)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/issuer-not-found",
			"Issuer not found", http.StatusNotFound,
			"This G-strkey hasn't been observed as an issuer.")
		return
	}
	if err != nil {
		if handlerTimedOut(iCtx, err) {
			s.logger.Warn("GetIssuer deadline exceeded", "g_strkey", gStrkey)
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/issuer-timeout",
				"Issuer read timed out", http.StatusServiceUnavailable,
				"the issuer + asset list scan didn't return in 8s; retry shortly.")
			return
		}
		s.logger.Warn("issuer read", "g_strkey", gStrkey, "err", err)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/issuer-error",
			"Issuer read failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	assets, err := s.issuers.ListIssuerAssets(iCtx, gStrkey)
	if err != nil {
		// Soft-fail on the asset list — the issuer card still
		// renders without it. Includes deadline exceeded.
		s.logger.Warn("issuer assets", "g_strkey", gStrkey, "err", err)
		assets = nil
	}

	homeDomain, orgName := enrichIssuer(row.GStrkey, row.HomeDomain, row.OrgName)
	out := Issuer{
		GStrkey:        row.GStrkey,
		HomeDomain:     homeDomain,
		OrgName:        orgName,
		ScamReason:     scamReason(row.GStrkey),
		AuthRequired:   row.AuthRequired,
		AuthRevocable:  row.AuthRevocable,
		AuthImmutable:  row.AuthImmutable,
		AuthClawback:   row.AuthClawback,
		SEP1ResolvedAt: row.SEP1ResolvedAt,
		SEP1Payload:    row.SEP1Payload,
		CreationLedger: row.CreationLedger,
	}
	// The dedicated issuer resolver isn't populating row.Auth* / row.HomeDomain
	// in prod (audit 2026-06-19), but we already index the on-chain AccountEntry
	// via the explorer's AccountState. Decode the auth-flags bitmask + the
	// home_domain here so the issuer panel shows real values instead of "not
	// yet resolved" (works for dormant issuers too once the account-state
	// backfill lands — data-truth G2). Stellar AccountEntry flags:
	// AUTH_REQUIRED=1, AUTH_REVOCABLE=2, AUTH_IMMUTABLE=4, AUTH_CLAWBACK=8.
	if (out.AuthRequired == nil || out.HomeDomain == "") && s.explorer != nil {
		if st, aerr := s.explorer.AccountState(iCtx, gStrkey); aerr == nil && st.Exists {
			if out.AuthRequired == nil {
				req := st.Flags&0x1 != 0
				rev := st.Flags&0x2 != 0
				imm := st.Flags&0x4 != 0
				claw := st.Flags&0x8 != 0
				out.AuthRequired = &req
				out.AuthRevocable = &rev
				out.AuthImmutable = &imm
				out.AuthClawback = &claw
			}
			if out.HomeDomain == "" && st.HomeDomain != "" {
				out.HomeDomain = st.HomeDomain
			}
		}
	}
	for _, a := range assets {
		out.Assets = append(out.Assets, IssuedAsset{
			AssetID:          a.AssetID,
			Code:             a.Code,
			Slug:             a.Slug,
			FirstSeenLedger:  a.FirstSeenLedger,
			LastSeenLedger:   a.LastSeenLedger,
			ObservationCount: a.ObservationCount,
		})
	}
	writeJSON(w, out, Flags{})
}
