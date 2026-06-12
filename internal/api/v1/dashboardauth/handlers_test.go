package dashboardauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/notify"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// newTestRig wires the in-memory fakes + a noop sender into a
// NewHandlers. Returns the handlers + the underlying fakes so
// tests can poke + assert at internal state directly.
type testRig struct {
	h        *Handlers
	cfg      *Config
	accounts *fakeAccountStore
	users    *fakeUserStore
	tokens   *fakeTokenStore
	sender   *notify.NoopSender
	now      func() time.Time
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) }
	accounts := newFakeAccountStore()
	users := newFakeUserStore()
	tokens := newFakeTokenStore(now)
	sender := &notify.NoopSender{}
	cfg := Config{
		Accounts:         accounts,
		Users:            users,
		Tokens:           tokens,
		Sender:           sender,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              now,
		DashboardBaseURL: "https://app.ratesengine.net",
		EmailFrom:        "Rates Engine <hello@ratesengine.net>",
		MagicLinkTTL:     15 * time.Minute,
		SessionTTL:       30 * 24 * time.Hour,
	}
	h, err := NewHandlers(cfg)
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}
	return &testRig{
		h: h, cfg: h.cfg,
		accounts: accounts, users: users, tokens: tokens,
		sender: sender, now: now,
	}
}

func (r *testRig) postLogin(t *testing.T, email string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(loginRequest{Email: email})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleLogin(w, req)
	return w
}

// extractTokenFromSentEmail pulls the magic-link plaintext out of
// the most-recently-sent NoopSender Message. The plaintext shows
// up in the rendered link URL — we parse it back out so tests can
// hit /v1/auth/callback without recomputing.
func (r *testRig) extractTokenFromSentEmail(t *testing.T) string {
	t.Helper()
	msg, ok := r.sender.Last()
	if !ok {
		t.Fatal("no email sent")
	}
	idx := strings.Index(msg.Text, "?token=")
	if idx < 0 {
		t.Fatalf("no ?token= in sent email: %s", msg.Text)
	}
	tok := msg.Text[idx+len("?token="):]
	if newline := strings.IndexAny(tok, "\n\r "); newline >= 0 {
		tok = tok[:newline]
	}
	return tok
}

func TestHandleLogin_HappyPath_NewEmail(t *testing.T) {
	r := newTestRig(t)
	w := r.postLogin(t, "alice@example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if r.sender.SentCount() != 1 {
		t.Errorf("sent emails = %d, want 1", r.sender.SentCount())
	}
	last, _ := r.sender.Last()
	if got := last.To; len(got) != 1 || got[0] != "alice@example.com" {
		t.Errorf("recipient = %v", got)
	}
}

func TestHandleLogin_HappyPath_ExistingEmailDoesNotLeakEnumeration(t *testing.T) {
	r := newTestRig(t)
	// Pre-existing user.
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "x", Slug: "x", BillingEmail: "alice@example.com",
		Tier: platform.TierFree, Status: platform.AccountActive,
	})
	_, err := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "alice@example.com", Role: platform.RoleOwner,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	wExisting := r.postLogin(t, "alice@example.com")
	wMissing := r.postLogin(t, "noone@example.com")

	if wExisting.Code != wMissing.Code {
		t.Errorf("status differs: existing=%d missing=%d (enumeration leak)", wExisting.Code, wMissing.Code)
	}
	if got, want := wExisting.Body.String(), wMissing.Body.String(); got != want {
		t.Errorf("response body differs:\n  existing: %s\n  missing:  %s", got, want)
	}
}

func TestHandleLogin_RejectsMalformedEmail(t *testing.T) {
	r := newTestRig(t)
	for _, email := range []string{"", "no-at-sign", "@", "a@b"} {
		w := r.postLogin(t, email)
		if w.Code != http.StatusBadRequest {
			t.Errorf("email=%q got %d, want 400", email, w.Code)
		}
	}
}

func TestHandleLogin_RejectsMalformedJSON(t *testing.T) {
	r := newTestRig(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{bad`))
	req.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleLogin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestHandleCallback_HappyPath_FirstTimeSignupCreatesAccount(t *testing.T) {
	r := newTestRig(t)
	// Mint a token via /login so we exercise the same plumbing.
	if w := r.postLogin(t, "newuser@example.com"); w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	plaintext := r.extractTokenFromSentEmail(t)

	cb := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token="+url.QueryEscape(plaintext), nil)
	cb.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleCallback(w, cb)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "https://app.ratesengine.net/") {
		t.Errorf("Location = %q", loc)
	}
	cookies := w.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatal("session cookie not set")
	}
	if !session.HttpOnly {
		t.Error("session cookie not HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v", session.SameSite)
	}

	// Verify the user + account were created with sensible defaults.
	user, err := r.users.GetUserByEmail(context.Background(), "newuser@example.com")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.Role != platform.RoleOwner {
		t.Errorf("role = %v, want owner", user.Role)
	}
	if user.EmailVerifiedAt.IsZero() {
		t.Error("email_verified_at not set after callback")
	}
	acct, err := r.accounts.Get(context.Background(), user.AccountID)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if acct.Tier != platform.TierFree {
		t.Errorf("tier = %v, want free", acct.Tier)
	}
}

func TestHandleCallback_HappyPath_ExistingUserNoDuplicateAccount(t *testing.T) {
	r := newTestRig(t)
	// Pre-seed.
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "Acme", Slug: "acme", BillingEmail: "ash@acme.com",
		Tier: platform.TierStarter, Status: platform.AccountActive,
	})
	original, _ := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "ash@acme.com", Role: platform.RoleOwner,
	})

	if w := r.postLogin(t, "ash@acme.com"); w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	plaintext := r.extractTokenFromSentEmail(t)

	cb := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token="+url.QueryEscape(plaintext), nil)
	cb.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleCallback(w, cb)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	post, err := r.users.GetUserByEmail(context.Background(), "ash@acme.com")
	if err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	if post.ID != original.ID {
		t.Errorf("user.ID changed: existing=%v post-callback=%v (duplicate user created)", original.ID, post.ID)
	}
	if post.AccountID != acct.ID {
		t.Errorf("AccountID changed: existing=%v post-callback=%v", acct.ID, post.AccountID)
	}
}

func TestHandleCallback_ExpiredTokenReturns410(t *testing.T) {
	r := newTestRig(t)
	if w := r.postLogin(t, "alice@example.com"); w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	plaintext := r.extractTokenFromSentEmail(t)

	// Fast-forward clock past the 15-minute TTL.
	r.tokens.now = func() time.Time { return r.now().Add(20 * time.Minute) }

	cb := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token="+url.QueryEscape(plaintext), nil)
	cb.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleCallback(w, cb)

	if w.Code != http.StatusGone {
		t.Errorf("expired token: status = %d, want 410", w.Code)
	}
}

func TestHandleCallback_InvalidTokenReturns400(t *testing.T) {
	r := newTestRig(t)
	cb := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token=deadbeef", nil)
	cb.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleCallback(w, cb)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid token: status = %d, want 400", w.Code)
	}
}

func TestHandleCallback_TokenSingleUse(t *testing.T) {
	r := newTestRig(t)
	if w := r.postLogin(t, "alice@example.com"); w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	plaintext := r.extractTokenFromSentEmail(t)

	// Consume once.
	cb1 := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token="+url.QueryEscape(plaintext), nil)
	cb1.RemoteAddr = "203.0.113.5:55123"
	w1 := httptest.NewRecorder()
	r.h.HandleCallback(w1, cb1)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("first callback: %d", w1.Code)
	}

	// Replay must fail.
	cb2 := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?token="+url.QueryEscape(plaintext), nil)
	cb2.RemoteAddr = "203.0.113.5:55123"
	w2 := httptest.NewRecorder()
	r.h.HandleCallback(w2, cb2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("replay: status = %d, want 400 (single-use token)", w2.Code)
	}
}

func TestHandleCallback_NextParamPathOnly_RejectsOpenRedirect(t *testing.T) {
	r := newTestRig(t)
	if w := r.postLogin(t, "alice@example.com"); w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	plaintext := r.extractTokenFromSentEmail(t)

	// Try to redirect to evil.com via //evil.com (protocol-relative).
	target := "/v1/auth/callback?token=" + url.QueryEscape(plaintext) + "&next=" + url.QueryEscape("//evil.com/x")
	cb := httptest.NewRequest(http.MethodGet, target, nil)
	cb.RemoteAddr = "203.0.113.5:55123"
	w := httptest.NewRecorder()
	r.h.HandleCallback(w, cb)
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://app.ratesengine.net/") {
		t.Errorf("open-redirect through //evil.com bypassed; Location = %q", loc)
	}
	if strings.Contains(loc, "evil.com") {
		t.Errorf("Location leaked attacker host: %q", loc)
	}
}

func TestHandleLogout_IdempotentWithoutCookie(t *testing.T) {
	r := newTestRig(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	r.h.HandleLogout(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Cookie should be cleared anyway.
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge >= 0 {
			t.Errorf("logout did not clear cookie: %+v", c)
		}
	}
}

func TestHandleLogout_RevokesActiveSession(t *testing.T) {
	r := newTestRig(t)
	// Mint a session directly.
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "x", Slug: "x", Tier: platform.TierFree, Status: platform.AccountActive,
	})
	user, _ := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "ash@example.com", Role: platform.RoleOwner,
	})
	sess, _ := r.users.CreateSession(context.Background(), platform.Session{
		UserID: user.ID, ExpiresAt: r.now().Add(24 * time.Hour),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID.String()})
	w := httptest.NewRecorder()
	r.h.HandleLogout(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
	// Subsequent GetSession must return ErrNotFound.
	if _, err := r.users.GetSession(context.Background(), sess.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("session not revoked after logout: err=%v", err)
	}
}

func TestHandleLogout_TolersInvalidCookieValue(t *testing.T) {
	r := newTestRig(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "not-a-uuid"})
	w := httptest.NewRecorder()
	r.h.HandleLogout(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (idempotent)", w.Code)
	}
}

func TestSlugFromEmail(t *testing.T) {
	cases := map[string]string{
		"alice@example.com":  "alice",
		"ash.francis@x.com":  "ash-francis",
		"BIG.CAPS@y.com":     "big-caps",
		"under_score@y.com":  "under-score",
		"plus+tag@y.com":     "plustag",
		"only-symbols@y.com": "only-symbols",
		"":                   "user",
		"@nothing":           "user",
	}
	for in, want := range cases {
		if got := slugFromEmail(in); got != want {
			t.Errorf("slugFromEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeEmail(t *testing.T) {
	cases := map[string]bool{
		"alice@example.com":               true,
		"a@b.c":                           true,
		"":                                false,
		"@b.c":                            false,
		"a@":                              false,
		"a@b":                             false,
		"abc":                             false,
		strings.Repeat("a", 256) + "@b.c": false,
	}
	for in, want := range cases {
		if got := looksLikeEmail(in); got != want {
			t.Errorf("looksLikeEmail(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRequireSession_AnonRequest401(t *testing.T) {
	r := newTestRig(t)
	guarded := RequireSession(r.cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anon: status = %d, want 401", w.Code)
	}
}

func TestMiddleware_CookieToContext(t *testing.T) {
	r := newTestRig(t)
	// Mint a session directly.
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "x", Slug: "x", Tier: platform.TierFree, Status: platform.AccountActive,
	})
	user, _ := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "ash@example.com", Role: platform.RoleOwner,
	})
	sess, _ := r.users.CreateSession(context.Background(), platform.Session{
		UserID: user.ID, ExpiresAt: r.now().Add(24 * time.Hour),
	})

	var got SessionContext
	var ok bool
	stack := Middleware(r.cfg)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got, ok = SessionFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID.String()})
	stack.ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("session not planted on context")
	}
	if got.User.ID != user.ID {
		t.Errorf("user.ID = %v, want %v", got.User.ID, user.ID)
	}
	if got.Account.ID != acct.ID {
		t.Errorf("account.ID = %v, want %v", got.Account.ID, acct.ID)
	}
}

func TestMiddleware_RevokedSessionDropsContext(t *testing.T) {
	r := newTestRig(t)
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "x", Slug: "x", Tier: platform.TierFree, Status: platform.AccountActive,
	})
	user, _ := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "ash@example.com", Role: platform.RoleOwner,
	})
	sess, _ := r.users.CreateSession(context.Background(), platform.Session{
		UserID: user.ID, ExpiresAt: r.now().Add(24 * time.Hour),
	})
	// Revoke it.
	_ = r.users.RevokeSession(context.Background(), sess.ID)

	var ok bool
	stack := Middleware(r.cfg)(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, ok = SessionFromContext(req.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID.String()})
	stack.ServeHTTP(httptest.NewRecorder(), req)
	if ok {
		t.Error("revoked session leaked into context")
	}
}

func TestMiddleware_SuspendedAccountRevokesAndDrops(t *testing.T) {
	r := newTestRig(t)
	acct, _ := r.accounts.Create(context.Background(), platform.Account{
		Name: "x", Slug: "x", Tier: platform.TierFree, Status: platform.AccountActive,
	})
	user, _ := r.users.CreateUser(context.Background(), platform.User{
		AccountID: acct.ID, Email: "ash@example.com", Role: platform.RoleOwner,
	})
	sess, _ := r.users.CreateSession(context.Background(), platform.Session{
		UserID: user.ID, ExpiresAt: r.now().Add(24 * time.Hour),
	})
	// Suspend the account.
	if err := r.accounts.Suspend(context.Background(), acct.ID, "test"); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	var ok bool
	stack := Middleware(r.cfg)(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, ok = SessionFromContext(req.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID.String()})
	stack.ServeHTTP(httptest.NewRecorder(), req)

	if ok {
		t.Error("suspended-account session leaked into context")
	}
	// Side effect: revoke must have been called.
	if _, err := r.users.GetSession(context.Background(), sess.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Error("middleware did not revoke session for suspended account")
	}
}

func TestTouchTracker_Debounces(t *testing.T) {
	tt := newTouchTracker(time.Minute)
	id := uuid.New()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	if !tt.shouldTouch(id, t0) {
		t.Fatal("first call must touch")
	}
	if tt.shouldTouch(id, t0.Add(30*time.Second)) {
		t.Error("inside-interval call must not touch")
	}
	if !tt.shouldTouch(id, t0.Add(2*time.Minute)) {
		t.Error("post-interval call must touch")
	}
}
