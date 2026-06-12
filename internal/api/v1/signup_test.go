package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// fakeSignupTracker is the handler-level test double for
// [v1.SignupTracker]. Holds the in-memory mapping; mirrors the
// SETNX semantics of [auth.RedisSignupTracker] so race-loss is
// representable in tests.
type fakeSignupTracker struct {
	mu     sync.Mutex
	store  map[string]string
	getErr error
	setErr error
}

func newFakeSignupTracker() *fakeSignupTracker {
	return &fakeSignupTracker{store: map[string]string{}}
}

func (f *fakeSignupTracker) LookupByEmailHash(_ context.Context, h string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.store[h], nil
}

func (f *fakeSignupTracker) MarkSignup(_ context.Context, h, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	// MarkSignup is upgrade-or-set: it overwrites a pending
	// reservation with the real key_id. F-1218 (codex audit-2026-05-12).
	f.store[h] = keyID
	return nil
}

// ReserveEmail mirrors the Redis SETNX semantics: returns
// auth.ErrSignupEmailReserved when the email-hash is already
// claimed. F-1218 (codex audit-2026-05-12).
func (f *fakeSignupTracker) ReserveEmail(_ context.Context, h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	if _, exists := f.store[h]; exists {
		return auth.ErrSignupEmailReserved
	}
	f.store[h] = "pending"
	return nil
}

func newSignupTestServer(t *testing.T, store v1.AccountStore, signups v1.SignupTracker) *httptest.Server {
	t.Helper()
	srv := v1.New(v1.Options{
		Auth:     fakeAuthMiddleware(auth.Subject{}), // anonymous
		Accounts: store,
		Signups:  signups,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postSignup(t *testing.T, ts *httptest.Server, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(ts.URL+"/v1/signup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/signup: %v", err)
	}
	return resp
}

// TestSignup_HappyPath — a fresh email, AccountStore + Signups
// wired, returns 200 with a plaintext key + key_id + identifier.
func TestSignup_HappyPath(t *testing.T) {
	store := &fakeAccountStore{
		rec: auth.APIKeyRecord{
			KeyID:           "kid_test12345",
			Identifier:      "signup-abcdef0123456789",
			Tier:            auth.TierAPIKey,
			Label:           "my-app",
			RateLimitPerMin: 1000,
			CreatedAt:       time.Now().UTC(),
		},
		plain: "rek_topsecret",
	}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)

	resp := postSignup(t, ts, `{"email":"test@example.com","label":"my-app"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Data v1.SignupResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Plaintext != "rek_topsecret" {
		t.Errorf("Plaintext = %q, want rek_topsecret", body.Data.Plaintext)
	}
	if body.Data.KeyID != "kid_test12345" {
		t.Errorf("KeyID = %q", body.Data.KeyID)
	}
	if body.Data.Tier != "apikey" {
		t.Errorf("Tier = %q, want apikey", body.Data.Tier)
	}
	if body.Data.RateLimitPerMin != 1000 {
		t.Errorf("RateLimitPerMin = %d, want 1000", body.Data.RateLimitPerMin)
	}
	if !strings.HasPrefix(body.Data.Identifier, "signup-") {
		t.Errorf("Identifier = %q, want signup- prefix", body.Data.Identifier)
	}
	if store.calls != 1 {
		t.Errorf("AccountStore calls = %d, want 1", store.calls)
	}
	// Email-hash should now be in the tracker.
	if len(signups.store) != 1 {
		t.Errorf("tracker entries = %d, want 1", len(signups.store))
	}
}

// TestSignup_DuplicateEmail — same email twice returns 409 on
// second attempt.
func TestSignup_DuplicateEmail(t *testing.T) {
	store := &fakeAccountStore{
		rec:   auth.APIKeyRecord{KeyID: "kid_first", Tier: auth.TierAPIKey},
		plain: "rek_first",
	}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)

	// First signup — OK.
	r1 := postSignup(t, ts, `{"email":"dup@example.com"}`)
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first signup: status = %d, want 200", r1.StatusCode)
	}

	// Second signup with same email — 409.
	r2 := postSignup(t, ts, `{"email":"dup@example.com"}`)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("duplicate signup: status = %d, want 409", r2.StatusCode)
	}
	if store.calls != 1 {
		t.Errorf("AccountStore calls = %d, want 1 (second signup must NOT mint)", store.calls)
	}
}

// TestSignup_DuplicateEmailCaseInsensitive — Email validation
// lowercases before hashing; "Dup@Example.com" and "dup@example.com"
// collapse to the same identifier.
func TestSignup_DuplicateEmailCaseInsensitive(t *testing.T) {
	store := &fakeAccountStore{
		rec:   auth.APIKeyRecord{KeyID: "kid_first", Tier: auth.TierAPIKey},
		plain: "rek_first",
	}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)

	r1 := postSignup(t, ts, `{"email":"  dup@example.com  "}`)
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first: %d", r1.StatusCode)
	}
	r2 := postSignup(t, ts, `{"email":"DUP@EXAMPLE.COM"}`)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("case-different signup: status = %d, want 409", r2.StatusCode)
	}
}

// TestSignup_InvalidEmail — garbage in the email field returns 400.
func TestSignup_InvalidEmail(t *testing.T) {
	store := &fakeAccountStore{rec: auth.APIKeyRecord{KeyID: "kid"}, plain: "rek"}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)
	cases := []string{
		`{"email":""}`,
		`{"email":"not-an-email"}`,
		`{"email":"@example.com"}`,
		`{}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			resp := postSignup(t, ts, body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("body=%q: status = %d, want 400", body, resp.StatusCode)
			}
		})
	}
	if store.calls != 0 {
		t.Errorf("AccountStore must not be called for invalid input; calls = %d", store.calls)
	}
}

// TestSignup_NoAccountStore — endpoint returns 503 when the binary
// didn't wire the store (Redis missing at startup).
func TestSignup_NoAccountStore(t *testing.T) {
	ts := newSignupTestServer(t, nil, newFakeSignupTracker())
	resp := postSignup(t, ts, `{"email":"test@example.com"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestSignup_AlreadyAuthenticated — calling /v1/signup with an
// existing API key returns 400 (use POST /v1/account/keys instead).
func TestSignup_AlreadyAuthenticated(t *testing.T) {
	store := &fakeAccountStore{rec: auth.APIKeyRecord{KeyID: "kid"}, plain: "rek"}
	signups := newFakeSignupTracker()
	srv := v1.New(v1.Options{
		Auth: fakeAuthMiddleware(auth.Subject{
			Identifier: "existing-customer",
			Tier:       auth.TierAPIKey,
			KeyID:      "kid_existing",
		}),
		Accounts: store,
		Signups:  signups,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := postSignup(t, ts, `{"email":"new@example.com"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if store.calls != 0 {
		t.Errorf("must NOT mint for already-authed callers; calls = %d", store.calls)
	}
}

// TestSignup_NoSignupTracker — tracker is optional. Without one,
// duplicate detection is disabled but signup still succeeds (each
// call mints a fresh key).
func TestSignup_NoSignupTracker(t *testing.T) {
	store := &fakeAccountStore{
		rec:   auth.APIKeyRecord{KeyID: "kid", Tier: auth.TierAPIKey},
		plain: "rek",
	}
	ts := newSignupTestServer(t, store, nil)

	r1 := postSignup(t, ts, `{"email":"dup@example.com"}`)
	r1.Body.Close()
	r2 := postSignup(t, ts, `{"email":"dup@example.com"}`)
	defer r2.Body.Close()
	if r1.StatusCode != http.StatusOK || r2.StatusCode != http.StatusOK {
		t.Errorf("status1=%d status2=%d, both want 200 (no tracker = no dedup)", r1.StatusCode, r2.StatusCode)
	}
	if store.calls != 2 {
		t.Errorf("AccountStore calls = %d, want 2 (each signup mints fresh)", store.calls)
	}
}

// TestSignup_StoreFailure — AccountStore.Create returns an error;
// the handler responds 500.
func TestSignup_StoreFailure(t *testing.T) {
	store := &fakeAccountStore{err: errors.New("redis disconnected")}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)
	resp := postSignup(t, ts, `{"email":"test@example.com"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestSignup_BodyTooLarge — bodies over 4 KiB return 400.
func TestSignup_BodyTooLarge(t *testing.T) {
	store := &fakeAccountStore{rec: auth.APIKeyRecord{KeyID: "kid"}, plain: "rek"}
	signups := newFakeSignupTracker()
	ts := newSignupTestServer(t, store, signups)
	huge := strings.Repeat("a", 5*1024)
	resp := postSignup(t, ts, `{"email":"test@example.com","label":"`+huge+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
