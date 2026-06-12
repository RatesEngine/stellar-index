package dashboardkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/dashboardauth"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// MaxKeysPerAccount caps how many active keys a single account
// can hold. Tier-aware quotas can replace this once the billing
// pipeline is wired (Phase 2); for now a flat 25 prevents an
// enthusiastic operator from minting hundreds in a loop.
const MaxKeysPerAccount = 25

// Config wires the handlers' dependencies. Constructed once in
// cmd/ratesengine-api/main.go alongside the dashboardauth
// handlers.
//
// The runtime auth validator still reads keys from Redis during
// Phase 1; keys minted from this dashboard surface land in
// Postgres only and DO NOT authenticate against the runtime
// API until the Phase 1 Week 4 cutover ships. The dashboard
// surfaces a notice on new keys to make this explicit. Once the
// cutover lands the Postgres store becomes canonical and the
// notice disappears.
type Config struct {
	// Keys is the Postgres-backed APIKeyStore — the new source
	// of truth for the dashboard's key management surface.
	Keys platform.APIKeyStore
	// CacheInvalidator, when non-nil, is called by HandleRevoke
	// after a successful Postgres revoke so the runtime auth
	// validator's Redis cache stops authenticating the just-
	// revoked key. Production wires
	// auth.PostgresAPIKeyValidator.InvalidateCachedKey here.
	// Nil leaves the cache TTL to roll the row off naturally —
	// workable but means a revoked key keeps authenticating
	// until the TTL expires.
	CacheInvalidator CacheInvalidator
	Logger           *slog.Logger
	Now              func() time.Time
}

// CacheInvalidator is the subset of
// auth.PostgresAPIKeyValidator the dashboard needs for cache
// eviction on revoke. Defined here as an interface so
// dashboardkeys doesn't import internal/auth.
type CacheInvalidator interface {
	InvalidateCachedKey(ctx context.Context, hexHash string) error
}

func (c *Config) validate() error {
	if c.Keys == nil {
		return errors.New("dashboardkeys: Keys store is required")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	return nil
}

// Handlers exposes the routes to be mounted in the v1 mux.
type Handlers struct{ cfg *Config }

// NewHandlers validates the config and returns a mount-ready
// Handlers.
func NewHandlers(cfg Config) (*Handlers, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Handlers{cfg: &cfg}, nil
}

// Mount installs the dashboard key-management routes:
//
//	GET    /v1/dashboard/keys           — list account's keys
//	POST   /v1/dashboard/keys           — mint a new key
//	DELETE /v1/dashboard/keys/{id}      — revoke
//
// Each handler reads the SessionContext from the request context
// (planted by dashboardauth.Middleware). Anonymous requests
// short-circuit to 401 here rather than depending on a separate
// RequireSession wrapper — the dashboard surface always requires
// auth, so embedding the check keeps the route table tight.
func (h *Handlers) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dashboard/keys", h.HandleList)
	mux.HandleFunc("POST /v1/dashboard/keys", h.HandleCreate)
	mux.HandleFunc("DELETE /v1/dashboard/keys/{id}", h.HandleRevoke)
}

// keyDTO is the wire shape the dashboard reads. The plaintext is
// only ever set on the Create response — never returned by
// subsequent reads. KeyHash is omitted entirely so the API can't
// be mis-used to seed an offline brute-force.
type keyDTO struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Description            string    `json:"description,omitempty"`
	KeyPrefix              string    `json:"key_prefix"`
	Tier                   string    `json:"tier"`
	RateLimitPerMin        int       `json:"rate_limit_per_min"`
	MonthlyQuota           int64     `json:"monthly_quota,omitempty"`
	UsageAlertThresholdPct int       `json:"usage_alert_threshold_pct,omitempty"`
	IPAllowlist            []string  `json:"ip_allowlist,omitempty"`
	RefererAllowlist       []string  `json:"referer_allowlist,omitempty"`
	ExpiresAt              time.Time `json:"expires_at,omitempty"`
	RevokedAt              time.Time `json:"revoked_at,omitempty"`
	RevokedReason          string    `json:"revoked_reason,omitempty"`
	LastUsedAt             time.Time `json:"last_used_at,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

func toDTO(k platform.APIKey) keyDTO {
	dto := keyDTO{
		ID:                     k.ID,
		Name:                   k.Name,
		Description:            k.Description,
		KeyPrefix:              k.KeyPrefix,
		Tier:                   string(k.Tier),
		RateLimitPerMin:        k.RateLimitPerMin,
		MonthlyQuota:           k.MonthlyQuota,
		UsageAlertThresholdPct: k.UsageAlertThresholdPct,
		RefererAllowlist:       k.RefererAllowlist,
		ExpiresAt:              k.ExpiresAt,
		RevokedAt:              k.RevokedAt,
		RevokedReason:          k.RevokedReason,
		LastUsedAt:             k.LastUsedAt,
		CreatedAt:              k.CreatedAt,
	}
	if len(k.IPAllowlist) > 0 {
		dto.IPAllowlist = make([]string, len(k.IPAllowlist))
		for i, p := range k.IPAllowlist {
			dto.IPAllowlist[i] = p.String()
		}
	}
	return dto
}

type listResponse struct {
	Keys []keyDTO `json:"keys"`
}

// HandleList returns every key (active + revoked) for the
// session's account, ordered oldest-first.
func (h *Handlers) HandleList(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	keys, err := h.cfg.Keys.ListForAccount(r.Context(), sc.Account.ID)
	if err != nil {
		h.cfg.Logger.Error("list keys", "err", err, "account_id", sc.Account.ID)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	out := listResponse{Keys: make([]keyDTO, 0, len(keys))}
	for _, k := range keys {
		out.Keys = append(out.Keys, toDTO(k))
	}
	writeJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	RateLimitPerMin        int      `json:"rate_limit_per_min"`
	MonthlyQuota           int64    `json:"monthly_quota"`
	IPAllowlist            []string `json:"ip_allowlist"`
	RefererAllowlist       []string `json:"referer_allowlist"`
	ExpiresAt              string   `json:"expires_at"`
	UsageAlertThresholdPct int      `json:"usage_alert_threshold_pct"`
}

type createResponse struct {
	Plaintext string `json:"plaintext"`
	Key       keyDTO `json:"key"`
}

// HandleCreate mints a new key. The plaintext is returned only
// in this response — subsequent reads only see the prefix.
func (h *Handlers) HandleCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	if !canManageKeys(sc.User.Role) {
		writeProblem(w, http.StatusForbidden, "your role can't mint keys", r.URL.Path)
		return
	}

	req, status, problem := parseCreateRequest(r)
	if problem != "" {
		writeProblem(w, status, problem, r.URL.Path)
		return
	}

	// F-1212 (codex audit-2026-05-12): clamp customer-supplied
	// rate_limit_per_min to the account's tier ceiling. Free
	// accounts get 60/min; paid tiers get their respective caps.
	// Without this clamp the handler honoured any value up to
	// 100_000, letting a Free account self-mint a key with 100×
	// the Starter budget.
	req.RateLimitPerMin = clampRateLimitToTier(req.RateLimitPerMin, sc.Account.Tier)

	if status, problem := h.checkQuota(r, sc.Account.ID); problem != "" {
		writeProblem(w, status, problem, r.URL.Path)
		return
	}

	// Mint the plaintext + hash.
	plaintext, err := generatePlaintext()
	if err != nil {
		h.cfg.Logger.Error("generate plaintext", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	hash := sha256.Sum256([]byte(plaintext))
	keyID, err := generateKeyID()
	if err != nil {
		h.cfg.Logger.Error("generate key id", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}

	var expiresAt time.Time
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "expires_at must be RFC 3339", r.URL.Path)
			return
		}
		if !t.After(h.cfg.Now()) {
			writeProblem(w, http.StatusBadRequest, "expires_at must be in the future", r.URL.Path)
			return
		}
		expiresAt = t
	}

	rec := platform.APIKey{
		ID:                     keyID,
		AccountID:              sc.Account.ID,
		CreatedByUserID:        sc.User.ID,
		Name:                   req.Name,
		Description:            req.Description,
		KeyHash:                hash[:],
		KeyPrefix:              plaintext[:12],
		Tier:                   platform.APIKeyTierAPIKey,
		RateLimitPerMin:        req.RateLimitPerMin,
		MonthlyQuota:           req.MonthlyQuota,
		Permissions:            platform.KeyPermissions{All: true},
		RefererAllowlist:       req.RefererAllowlist,
		ExpiresAt:              expiresAt,
		UsageAlertThresholdPct: req.UsageAlertThresholdPct,
	}
	if len(req.IPAllowlist) > 0 {
		prefixes, err := parsePrefixes(req.IPAllowlist)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error(), r.URL.Path)
			return
		}
		rec.IPAllowlist = prefixes
	}

	out, err := h.cfg.Keys.Create(r.Context(), rec, MaxKeysPerAccount)
	if err != nil {
		// F-1257 race-window loser: another concurrent create
		// pushed this account over the 25-key cap between the
		// precheck and the INSERT. Surface the same 409 the
		// precheck would have.
		if errors.Is(err, platform.ErrAPIKeyQuotaExceeded) {
			writeProblem(w, http.StatusConflict,
				fmt.Sprintf("account already has %d active keys (max %d) — revoke one first", MaxKeysPerAccount, MaxKeysPerAccount),
				r.URL.Path)
			return
		}
		h.cfg.Logger.Error("create key in postgres", "err", err, "account_id", sc.Account.ID)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}

	writeJSON(w, http.StatusCreated, createResponse{
		Plaintext: plaintext,
		Key:       toDTO(out),
	})
}

// HandleRevoke soft-deletes a key. Idempotent — revoking an
// already-revoked key returns 204.
func (h *Handlers) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	if !canManageKeys(sc.User.Role) {
		writeProblem(w, http.StatusForbidden, "your role can't revoke keys", r.URL.Path)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeProblem(w, http.StatusBadRequest, "missing id", r.URL.Path)
		return
	}

	// Verify the key belongs to the session's account before
	// revoking — otherwise an attacker who knows another
	// account's key_id could revoke it.
	existing, err := h.cfg.Keys.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, platform.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "key not found", r.URL.Path)
			return
		}
		h.cfg.Logger.Error("get key for revoke", "err", err, "key_id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	if existing.AccountID != sc.Account.ID {
		// Same shape as not-found — don't leak that the key
		// exists on a different account.
		writeProblem(w, http.StatusNotFound, "key not found", r.URL.Path)
		return
	}

	if err := h.cfg.Keys.Revoke(r.Context(), id, sc.User.ID, "revoked from dashboard"); err != nil {
		h.cfg.Logger.Error("revoke key in postgres", "err", err, "key_id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	// Best-effort cache invalidation. A failure here means the
	// runtime auth cache keeps authenticating the revoked key
	// until the TTL rolls it off; we log + 204 anyway so the
	// dashboard UI updates and the operator notices via the log.
	if h.cfg.CacheInvalidator != nil && len(existing.KeyHash) > 0 {
		hexHash := hex.EncodeToString(existing.KeyHash)
		if err := h.cfg.CacheInvalidator.InvalidateCachedKey(r.Context(), hexHash); err != nil {
			h.cfg.Logger.Warn("invalidate auth cache after revoke",
				"err", err, "key_id", id)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseCreateRequest reads + validates the body. Returns the
// parsed request, an HTTP status, and a problem detail. Empty
// problem means "validation passed".
func parseCreateRequest(r *http.Request) (createRequest, int, string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		return createRequest{}, http.StatusBadRequest, "body too large"
	}
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return createRequest{}, http.StatusBadRequest, "malformed JSON"
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return req, http.StatusBadRequest, "name is required"
	}
	if len(req.Name) > 200 {
		return req, http.StatusBadRequest, "name must be 200 chars or fewer"
	}
	if req.RateLimitPerMin <= 0 {
		req.RateLimitPerMin = 1000
	}
	if req.RateLimitPerMin > 100000 {
		return req, http.StatusBadRequest, "rate_limit_per_min must be ≤ 100000"
	}
	return req, 0, ""
}

// clampRateLimitToTier returns the lower of `requested` and the
// account's per-tier ceiling. Free accounts that try to mint a key
// with `rate_limit_per_min: 100000` get silently downgraded to the
// free-tier cap (60/min) rather than rejected — this matches the
// per-tier-default fallback pattern at line 365 and keeps the
// dashboard UX simple (one field, one cap). F-1212 (codex
// audit-2026-05-12).
//
// Operator-issued or partner-issued keys aren't created through this
// handler — they go through ratesengine-ops and are not subject to
// this clamp. See [platform.Tier.MaxRateLimitPerMin] for the
// per-tier ladder.
func clampRateLimitToTier(requested int, tier platform.Tier) int {
	ceiling := tier.MaxRateLimitPerMin()
	if requested > ceiling {
		return ceiling
	}
	return requested
}

// checkQuota counts active keys for the account; returns
// (status, problem) on failure, (0, "") on pass.
func (h *Handlers) checkQuota(r *http.Request, accountID uuid.UUID) (int, string) {
	existing, err := h.cfg.Keys.ListForAccount(r.Context(), accountID)
	if err != nil {
		h.cfg.Logger.Error("list keys for quota", "err", err, "account_id", accountID)
		return http.StatusInternalServerError, "internal error"
	}
	active := 0
	for _, k := range existing {
		if k.RevokedAt.IsZero() {
			active++
		}
	}
	if active >= MaxKeysPerAccount {
		return http.StatusConflict,
			fmt.Sprintf("account already has %d active keys (max %d) — revoke one first", active, MaxKeysPerAccount)
	}
	return 0, ""
}

// canManageKeys gates create/revoke on role. Owner + admin can
// always; member can manage their own keys (Phase 1: every
// member can mint). Billing and viewer can't.
func canManageKeys(role platform.Role) bool {
	switch role {
	case platform.RoleOwner, platform.RoleAdmin, platform.RoleMember:
		return true
	default:
		return false
	}
}

// generatePlaintext mints a new `rek_<64hex>` plaintext using
// crypto/rand. 32 bytes = 256 bits = preimage-safe.
func generatePlaintext() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read entropy: %w", err)
	}
	return "rek_" + hex.EncodeToString(buf[:]), nil
}

// generateKeyID returns `kid_<24hex>` — the schema's regex
// requires `kid_[a-f0-9]{12,}`; we mint 12 bytes (24 hex) which
// is collision-resistant up to ~2^48 keys per account.
func generateKeyID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read entropy: %w", err)
	}
	return "kid_" + hex.EncodeToString(buf[:]), nil
}

// parsePrefixes converts CIDR strings to a slice of netip.Prefix.
// A bare IP is auto-promoted to /32 (v4) or /128 (v6) so dashboard
// callers don't have to know the CIDR conventions.
func parsePrefixes(raws []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(raws))
	for _, raw := range raws {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if p, err := netip.ParsePrefix(raw); err == nil {
			out = append(out, p)
			continue
		}
		// Try as a bare IP.
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("ip_allowlist[%q]: not a valid IP or CIDR", raw)
		}
		bits := 32
		if addr.Is6() && !addr.Is4In6() {
			bits = 128
		}
		out = append(out, netip.PrefixFrom(addr, bits))
	}
	return out, nil
}

// writeJSON sends `body` as application/json with the given
// status. Cache-Control: no-store keeps responses out of
// intermediate caches — these endpoints carry session-scoped
// data.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeProblem mirrors dashboardauth.writeProblem so the error
// shape is consistent across the dashboard surface.
func writeProblem(w http.ResponseWriter, status int, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":     "https://api.ratesengine.net/errors/dashboard",
		"title":    http.StatusText(status),
		"status":   status,
		"detail":   detail,
		"instance": instance,
	})
}

// Compile-time assertion: dashboardauth.SessionContext.User.ID
// is uuid.UUID. The package binds to that shape; this guard
// catches a refactor that changes it.
var _ = func() uuid.UUID { return dashboardauth.SessionContext{}.User.ID }
