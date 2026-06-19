package dashboardauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/StellarIndex/stellar-index/internal/platform"
)

// adminLookupInstance is the problem `instance` for the staff look-up.
const adminLookupInstance = "/v1/account/admin/lookup"

// AdminAccountView is the account half of the staff customer look-up.
type AdminAccountView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Tier            string `json:"tier"`
	Status          string `json:"status"`
	BillingEmail    string `json:"billing_email,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	SuspendedReason string `json:"suspended_reason,omitempty"`
	// RateLimitPerMinOverride / MonthlyRequestQuotaOverride are 0 when the
	// account inherits its tier default.
	RateLimitPerMinOverride     int   `json:"rate_limit_per_min_override,omitempty"`
	MonthlyRequestQuotaOverride int64 `json:"monthly_request_quota_override,omitempty"`
}

// AdminUserView is one user under the looked-up account.
type AdminUserView struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name,omitempty"`
	Role          string `json:"role"`
	IsStaff       bool   `json:"is_staff"`
	EmailVerified bool   `json:"email_verified"`
	LastLoginAt   string `json:"last_login_at,omitempty"`
}

// AdminLookupResponse is the staff customer look-up wire shape.
type AdminLookupResponse struct {
	Account AdminAccountView `json:"account"`
	Users   []AdminUserView  `json:"users"`
}

// HandleAdminLookup serves GET /v1/account/admin/lookup?email=|slug= — the
// staff "Customer look-up" tool (platform spec §6): resolve an account by a
// user's email or by account slug and return its tier/status plus the users
// on it. Staff-only: the route is wrapped in RequireSession and this handler
// additionally gates on the session user's IsStaff flag (a logged-in
// non-staff customer gets 403, never another customer's data). Read-only.
func (h *Handlers) HandleAdminLookup(w http.ResponseWriter, r *http.Request) {
	sc, ok := SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", adminLookupInstance)
		return
	}
	if !sc.User.IsStaff {
		writeProblem(w, http.StatusForbidden, "staff access required", adminLookupInstance)
		return
	}

	email := strings.TrimSpace(r.URL.Query().Get("email"))
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))

	acct, err := h.resolveLookupAccount(r, email, slug)
	if errors.Is(err, errLookupNoQuery) {
		writeProblem(w, http.StatusBadRequest, "provide ?email= or ?slug=", adminLookupInstance)
		return
	}
	if errors.Is(err, platform.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "no matching customer", adminLookupInstance)
		return
	}
	if err != nil {
		if h.cfg.Logger != nil {
			h.cfg.Logger.Warn("admin lookup failed", "err", err, "actor", sc.User.Email)
		}
		writeProblem(w, http.StatusInternalServerError, "internal error", adminLookupInstance)
		return
	}

	users, _ := h.cfg.Users.ListUsersForAccount(r.Context(), acct.ID)
	// Staff access to customer data is auditable — record who looked up what.
	if h.cfg.Logger != nil {
		h.cfg.Logger.Info("staff customer lookup", "actor", sc.User.Email, "account", acct.Slug)
	}

	resp := AdminLookupResponse{Account: adminAccountView(acct), Users: adminUserViews(users)}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// errLookupNoQuery signals neither email nor slug was supplied.
var errLookupNoQuery = errors.New("dashboardauth: admin lookup needs email or slug")

// resolveLookupAccount resolves the target account by email (via its user) or
// by slug. Returns errLookupNoQuery when neither is set, platform.ErrNotFound
// when nothing matches.
func (h *Handlers) resolveLookupAccount(r *http.Request, email, slug string) (platform.Account, error) {
	switch {
	case email != "":
		u, err := h.cfg.Users.GetUserByEmail(r.Context(), strings.ToLower(email))
		if err != nil {
			return platform.Account{}, err
		}
		return h.cfg.Accounts.Get(r.Context(), u.AccountID)
	case slug != "":
		return h.cfg.Accounts.GetBySlug(r.Context(), strings.ToLower(slug))
	default:
		return platform.Account{}, errLookupNoQuery
	}
}

func adminAccountView(a platform.Account) AdminAccountView {
	v := AdminAccountView{
		ID:                          a.ID.String(),
		Name:                        a.Name,
		Slug:                        a.Slug,
		Tier:                        string(a.Tier),
		Status:                      string(a.Status),
		BillingEmail:                a.BillingEmail,
		SuspendedReason:             a.SuspendedReason,
		RateLimitPerMinOverride:     a.RateLimitPerMinOverride,
		MonthlyRequestQuotaOverride: a.MonthlyRequestQuotaOverride,
	}
	if !a.CreatedAt.IsZero() {
		v.CreatedAt = a.CreatedAt.UTC().Format(time.RFC3339)
	}
	return v
}

func adminUserViews(users []platform.User) []AdminUserView {
	out := make([]AdminUserView, 0, len(users))
	for _, u := range users {
		uv := AdminUserView{
			ID:            u.ID.String(),
			Email:         u.Email,
			DisplayName:   u.DisplayName,
			Role:          string(u.Role),
			IsStaff:       u.IsStaff,
			EmailVerified: !u.EmailVerifiedAt.IsZero(),
		}
		if !u.LastLoginAt.IsZero() {
			uv.LastLoginAt = u.LastLoginAt.UTC().Format(time.RFC3339)
		}
		out = append(out, uv)
	}
	return out
}
