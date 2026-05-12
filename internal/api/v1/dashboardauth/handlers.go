package dashboardauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/notify"
	"github.com/RatesEngine/rates-engine/internal/platform"
)

// Config wires the auth handlers' dependencies. Constructed
// once at server boot; mounted into the v1 mux.
type Config struct {
	Accounts  platform.AccountStore
	Users     platform.UserStore
	Tokens    platform.TokenStore
	Sender    notify.Sender
	Generator *Generator
	Logger    *slog.Logger
	Now       func() time.Time
	// DashboardBaseURL is the absolute URL of the customer
	// dashboard SPA (typically https://app.ratesengine.net).
	// The magic-link callback URL embedded in emails is
	// `{DashboardBaseURL}/auth/callback?token=<plaintext>`.
	DashboardBaseURL string
	// EmailFrom is the From: address (e.g.
	// `Rates Engine <hello@ratesengine.net>`).
	EmailFrom string
	// MagicLinkTTL — link validity. Default 15 minutes.
	MagicLinkTTL time.Duration
	// SessionTTL — cookie session lifetime. Default 30 days
	// rolling.
	SessionTTL time.Duration
	// CookieSecure — set Secure flag on the cookie. Production
	// = true; local dev = false (the dashboard runs over
	// http://localhost during dev).
	CookieSecure bool
	// CookieDomain — empty = host-only cookie (recommended for
	// app.ratesengine.net). Set to ".ratesengine.net" if a
	// future surface needs the cookie shared across subdomains.
	CookieDomain string
}

// validate fills in defaults and rejects unworkable configs.
func (c *Config) validate() error {
	if c.Accounts == nil || c.Users == nil || c.Tokens == nil {
		return errors.New("dashboardauth: stores are required")
	}
	if c.Sender == nil {
		return errors.New("dashboardauth: sender is required")
	}
	if c.Generator == nil {
		c.Generator = NewGenerator()
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.DashboardBaseURL == "" {
		return errors.New("dashboardauth: DashboardBaseURL is required")
	}
	if c.EmailFrom == "" {
		return errors.New("dashboardauth: EmailFrom is required")
	}
	if c.MagicLinkTTL == 0 {
		c.MagicLinkTTL = 15 * time.Minute
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	return nil
}

// Handlers exposes the auth flow to be mounted in the v1 mux.
type Handlers struct{ cfg *Config }

// NewHandlers validates the config and returns a mount-ready
// Handlers.
func NewHandlers(cfg Config) (*Handlers, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Handlers{cfg: &cfg}, nil
}

// Mount installs the auth routes onto a mux:
//
//	POST /v1/auth/login        — request magic link
//	GET  /v1/auth/callback     — consume token, mint session
//	POST /v1/auth/logout       — revoke current session
//
// Caller wires the result into the regular middleware stack
// (RequestID + Logger + Recoverer + RateLimit + ...). These
// routes intentionally do NOT pass through the API-key auth
// middleware — they're the entry point for unauthenticated
// users.
func (h *Handlers) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/login", h.HandleLogin)
	mux.HandleFunc("GET /v1/auth/callback", h.HandleCallback)
	mux.HandleFunc("POST /v1/auth/logout", h.HandleLogout)
}

// loginRequest is the JSON body POST /v1/auth/login accepts.
type loginRequest struct {
	Email string `json:"email"`
}

type loginResponse struct {
	Status string `json:"status"`
}

// HandleLogin issues a magic-link email. Always returns 200
// with `{status: "sent"}` regardless of whether the email
// matches an existing user — leaking that information would
// let attackers enumerate valid emails. Real signup happens
// on consumption: if the user doesn't exist when they click
// the link, the callback handler creates the account.
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "body too large", "/v1/auth/login")
		return
	}
	var req loginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON", "/v1/auth/login")
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !looksLikeEmail(email) {
		writeProblem(w, http.StatusBadRequest, "invalid email", "/v1/auth/login")
		return
	}

	plaintext, hash, code, err := h.cfg.Generator.NewToken()
	if err != nil {
		h.cfg.Logger.Error("magic link token generation failed", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/login")
		return
	}

	if err := h.cfg.Tokens.CreateMagicLinkToken(r.Context(), platform.MagicLinkToken{
		TokenHash:   hash,
		Email:       email,
		Purpose:     platform.TokenPurposeLogin,
		ExpiresAt:   h.cfg.Now().Add(h.cfg.MagicLinkTTL),
		RequestedIP: clientIP(r),
	}); err != nil {
		h.cfg.Logger.Error("create magic link token", "err", err, "email", email)
		writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/login")
		return
	}

	// Build callback URL: {dashboard}/auth/callback?token=<plaintext>
	cb, err := url.Parse(h.cfg.DashboardBaseURL)
	if err != nil {
		h.cfg.Logger.Error("invalid DashboardBaseURL", "err", err, "url", h.cfg.DashboardBaseURL)
		writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/login")
		return
	}
	cb.Path = strings.TrimRight(cb.Path, "/") + "/auth/callback"
	q := cb.Query()
	q.Set("token", plaintext)
	cb.RawQuery = q.Encode()

	msg, err := notify.MagicLinkMessage(h.cfg.EmailFrom, email, notify.MagicLinkInput{
		LinkURL:          cb.String(),
		Code:             code,
		ExpiresInMinutes: int(h.cfg.MagicLinkTTL / time.Minute),
		IPAddress:        clientIP(r).String(),
		UserAgent:        truncateUA(r.UserAgent()),
	})
	if err != nil {
		h.cfg.Logger.Error("render magic link template", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/login")
		return
	}
	if err := h.cfg.Sender.Send(r.Context(), msg); err != nil {
		// Log + return 200 anyway — the user shouldn't see
		// "we tried to email you and failed" because that's a
		// signal an attacker can use to confirm an email
		// exists. Operator gets the alert via Loki / Sentry.
		h.cfg.Logger.Error("send magic link email", "err", err, "email", email)
	}

	_ = json.NewEncoder(w).Encode(loginResponse{Status: "sent"})
}

// HandleCallback consumes a magic-link token, finds-or-creates
// the user (single-org v1: each email gets one account on first
// signup), and issues a session cookie.
func (h *Handlers) HandleCallback(w http.ResponseWriter, r *http.Request) {
	plaintext := r.URL.Query().Get("token")
	if plaintext == "" {
		writeProblem(w, http.StatusBadRequest, "missing token", "/v1/auth/callback")
		return
	}
	tok, err := h.cfg.Tokens.ConsumeMagicLinkToken(r.Context(), HashMagicLinkPlaintext(plaintext))
	if err != nil {
		switch {
		case errors.Is(err, platform.ErrTokenExpired):
			writeProblem(w, http.StatusGone, "this link has expired — request a fresh one", "/v1/auth/callback")
		case errors.Is(err, platform.ErrNotFound):
			writeProblem(w, http.StatusBadRequest, "invalid token", "/v1/auth/callback")
		default:
			h.cfg.Logger.Error("consume magic link", "err", err)
			writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/callback")
		}
		return
	}
	if tok.Purpose != platform.TokenPurposeLogin {
		// A token minted for invite-accept can't be used to
		// log in. Same-error-shape as not-found to avoid
		// leaking whether a token exists for a different purpose.
		writeProblem(w, http.StatusBadRequest, "invalid token", "/v1/auth/callback")
		return
	}

	// Find-or-create user.
	user, err := h.cfg.Users.GetUserByEmail(r.Context(), tok.Email)
	if err != nil {
		if !errors.Is(err, platform.ErrNotFound) {
			h.cfg.Logger.Error("get user by email", "err", err, "email", tok.Email)
			writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/callback")
			return
		}
		user, err = h.signupNewUser(r.Context(), tok.Email)
		if err != nil {
			h.cfg.Logger.Error("signup new user", "err", err, "email", tok.Email)
			writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/callback")
			return
		}
	}

	// Mark email verified + last_login_at.
	now := h.cfg.Now()
	user.EmailVerifiedAt = now
	user.LastLoginAt = now
	if err := h.cfg.Users.UpdateUser(r.Context(), user); err != nil {
		// Non-fatal — the session can still issue, but we
		// want to log this since it means the verified-at
		// field will be wrong on the next /me lookup.
		h.cfg.Logger.Warn("update user post-login", "err", err, "user_id", user.ID)
	}

	sess, err := h.cfg.Users.CreateSession(r.Context(), platform.Session{
		UserID:       user.ID,
		ExpiresAt:    now.Add(h.cfg.SessionTTL),
		IPFirstSeen:  clientIP(r),
		IPLastSeen:   clientIP(r),
		UserAgent:    truncateUA(r.UserAgent()),
		GeoFirstSeen: r.Header.Get("CF-IPCountry"), // safe to leave empty when CF isn't fronting
		GeoLastSeen:  r.Header.Get("CF-IPCountry"),
	})
	if err != nil {
		h.cfg.Logger.Error("create session", "err", err, "user_id", user.ID)
		writeProblem(w, http.StatusInternalServerError, "internal error", "/v1/auth/callback")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sess.ID.String(),
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	// Redirect into the dashboard. Caller-supplied `next` URL
	// is path-only (we never honour absolute URLs to avoid
	// open-redirect attacks); empty falls through to "/".
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	dest := strings.TrimRight(h.cfg.DashboardBaseURL, "/") + next
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// HandleLogout revokes the current session and clears the
// cookie. Idempotent — calling without a session cookie is a
// 200, not a 401.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if id, err := uuid.Parse(c.Value); err == nil {
			if err := h.cfg.Users.RevokeSession(r.Context(), id); err != nil {
				h.cfg.Logger.Warn("revoke session at logout", "err", err)
			}
		}
	}
	// Clear the cookie regardless — even an invalid one, so
	// the browser stops sending it.
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusOK)
}

// signupNewUser creates the account + first user from a
// just-verified email. Single-org v1: every new email gets
// its own account with the user as owner.
//
// F-1255 (codex audit-2026-05-12): concurrent /v1/auth/callback
// callbacks for the same just-verified email can race — both pass
// the GetUserByEmail check, both create an account, only the
// first user-insert wins on `users_email_idx`. Pre-fix the loser
// returned 500 and the speculative-account row stayed orphaned.
// Now we catch the ErrConflict path on CreateUser, re-fetch the
// winning user (so both callers converge on the same User row),
// and return it. The speculative account row is now an orphan
// the operator-side reaper handles; better than 500ing the
// customer's first-login attempt.
func (h *Handlers) signupNewUser(ctx context.Context, email string) (platform.User, error) {
	slug := slugFromEmail(email)
	acct, err := h.cfg.Accounts.Create(ctx, platform.Account{
		Name:         email, // operator can rename later
		Slug:         slug,
		BillingEmail: email,
		Tier:         platform.TierFree,
		Status:       platform.AccountActive,
	})
	if err != nil {
		// Slug collision retry — append a 4-hex suffix and try
		// once. v1 single-org keeps this rare; v2 will use a
		// more robust handle generator.
		if errors.Is(err, platform.ErrConflict) {
			acct, err = h.cfg.Accounts.Create(ctx, platform.Account{
				Name:         email,
				Slug:         slug + "-" + uuid.New().String()[:4],
				BillingEmail: email,
				Tier:         platform.TierFree,
				Status:       platform.AccountActive,
			})
		}
		if err != nil {
			return platform.User{}, fmt.Errorf("create account: %w", err)
		}
	}
	user, err := h.cfg.Users.CreateUser(ctx, platform.User{
		AccountID: acct.ID,
		Email:     email,
		Role:      platform.RoleOwner,
	})
	if err != nil {
		if errors.Is(err, platform.ErrConflict) {
			// Race lost on the email unique index. The OTHER
			// concurrent callback won, its CreateUser
			// succeeded, and its account is the canonical one.
			// Re-fetch and use that user; our speculative
			// account becomes an orphan for the operator reaper.
			h.cfg.Logger.Warn("signup race: rolling back to winning user",
				"email", email, "speculative_account_id", acct.ID)
			winner, getErr := h.cfg.Users.GetUserByEmail(ctx, email)
			if getErr != nil {
				return platform.User{}, fmt.Errorf("create user conflict + reload: %w", getErr)
			}
			return winner, nil
		}
		return platform.User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

// ─── Helpers ─────────────────────────────────────────────────────

func looksLikeEmail(s string) bool {
	if len(s) < 3 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at >= len(s)-1 {
		return false
	}
	if strings.IndexByte(s[at+1:], '.') < 0 {
		return false
	}
	return true
}

func clientIP(r *http.Request) net.IP {
	// F-1224 (codex audit-2026-05-12): use the trusted-proxy-
	// resolved IP from `middleware.RemoteIP` rather than parsing
	// r.RemoteAddr directly. Behind Caddy / Cloudflare, the
	// socket peer is the local proxy; the real client IP is in
	// X-Forwarded-For, and the middleware decides whether to
	// honour it based on the `trusted_proxy_cidrs` config.
	//
	// Falls back to the socket peer when middleware.RemoteIP
	// returns empty (well-formed requests always carry one;
	// the empty case is left to upstream policy).
	if resolved := middleware.RemoteIP(r); resolved != "" {
		if ip := net.ParseIP(resolved); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func truncateUA(ua string) string {
	const maxUALen = 256
	if len(ua) <= maxUALen {
		return ua
	}
	return ua[:maxUALen]
}

// slugFromEmail derives an account slug from the local part of
// an email. Lowercase, hyphenated, ASCII-only.
func slugFromEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "user"
	}
	local := email[:at]
	var b strings.Builder
	b.Grow(len(local))
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "user"
	}
	return out
}

// writeProblem emits a problem+json error body matching the
// rest of the v1 surface. Local helper because we don't want
// dashboardauth depending on internal/api/v1's writeProblem.
func writeProblem(w http.ResponseWriter, status int, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":     "https://api.ratesengine.net/errors/auth",
		"title":    http.StatusText(status),
		"status":   status,
		"detail":   detail,
		"instance": instance,
	})
}
