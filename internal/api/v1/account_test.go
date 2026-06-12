package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// fakeAccountStore is the handler-level test double for
// [v1.AccountStore]. Records arguments + returns a canned record so
// the handler test exercises the wire shape without pulling in
// miniredis.
type fakeAccountStore struct {
	gotReq    auth.CreateAPIKeyRequest
	rec       auth.APIKeyRecord
	plain     string
	err       error
	calls     int
	listed    map[string][]auth.APIKeyRecord // identifier → keys
	listErr   error
	listCalls int
	revokeErr error
}

func (f *fakeAccountStore) Create(_ context.Context, req auth.CreateAPIKeyRequest) (auth.APIKeyRecord, string, error) {
	f.calls++
	f.gotReq = req
	if f.err != nil {
		return auth.APIKeyRecord{}, "", f.err
	}
	return f.rec, f.plain, nil
}

func (f *fakeAccountStore) ListKeysForIdentifier(_ context.Context, identifier string) ([]auth.APIKeyRecord, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listed == nil {
		return nil, nil
	}
	return f.listed[identifier], nil
}

func (f *fakeAccountStore) RevokeKeyByID(_ context.Context, _ string, _ string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	return nil
}

// fakeAuthMiddleware returns a middleware that stamps the supplied
// Subject onto the request context. Standing in for the real auth
// middleware so handler tests can run without configuring a
// validator + Redis.
//
// Pass the zero Subject to leave the context bare (simulates an
// anonymous request that didn't go through any auth layer at all).
func fakeAuthMiddleware(s auth.Subject) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.Tier == "" && s.Identifier == "" {
				next.ServeHTTP(w, r)
				return
			}
			r = r.WithContext(auth.WithSubject(r.Context(), s))
			next.ServeHTTP(w, r)
		})
	}
}

// newAccountTestServer wires a Server with a controlled subject +
// optional account store. Subject's zero value means "anonymous /
// no auth attached" — the handlers should respond 401 for those
// requests.
func newAccountTestServer(t *testing.T, subject auth.Subject, store v1.AccountStore) *httptest.Server {
	t.Helper()
	srv := v1.New(v1.Options{
		Auth:     fakeAuthMiddleware(subject),
		Accounts: store,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestAccountMe_Unauthenticated covers the 401 path. /me is
// meaningless without a credential; an anonymous request must not
// receive a default echo back.
func TestAccountMe_Unauthenticated(t *testing.T) {
	ts := newAccountTestServer(t, auth.Subject{}, nil)
	resp, err := http.Get(ts.URL + "/v1/account/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("content-type = %q, want problem+json", ct)
	}
}

// TestAccountMe_Authenticated returns the caller's Account info.
// Field-level assertions guard the wire shape against a future
// rename that would silently break clients.
func TestAccountMe_Authenticated(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	ts := newAccountTestServer(t, auth.Subject{
		Identifier:      "owner-42",
		Tier:            auth.TierAPIKey,
		KeyID:           "kid_abc123",
		Label:           "ci-bot",
		RateLimitPerMin: 600,
		CreatedAt:       now,
	}, nil)

	resp, err := http.Get(ts.URL + "/v1/account/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.Account `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Data.KeyID != "kid_abc123" {
		t.Errorf("KeyID = %q", env.Data.KeyID)
	}
	if env.Data.Label != "ci-bot" {
		t.Errorf("Label = %q, want ci-bot", env.Data.Label)
	}
	if env.Data.Tier != "apikey" {
		t.Errorf("Tier = %q", env.Data.Tier)
	}
	if env.Data.RateLimitPerMin != 600 {
		t.Errorf("RateLimitPerMin = %d", env.Data.RateLimitPerMin)
	}
	if !env.Data.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v", env.Data.CreatedAt)
	}
}

// TestAccountUsage_EmptyList — the counter store is not yet wired,
// so the handler returns an empty UsageRow array. The wire shape
// is locked: clients can integrate today and continue working when
// real counters land.
func TestAccountUsage_EmptyList(t *testing.T) {
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-9",
		Tier:       auth.TierAPIKey,
	}, nil)
	resp, err := http.Get(ts.URL + "/v1/account/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data []v1.UsageRow `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 0 {
		t.Errorf("data should be empty array, got %d entries", len(env.Data))
	}
}

// TestAccountUsage_Unauthenticated — same 401 contract as /me.
func TestAccountUsage_Unauthenticated(t *testing.T) {
	ts := newAccountTestServer(t, auth.Subject{}, nil)
	resp, err := http.Get(ts.URL + "/v1/account/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAccountKeysCreate_Happy returns 201 + the plaintext + key_id.
// The fake store records the inbound CreateAPIKeyRequest so the
// handler's identifier-inheritance + tier-inheritance contract is
// exercised end-to-end.
func TestAccountKeysCreate_Happy(t *testing.T) {
	store := &fakeAccountStore{
		rec: auth.APIKeyRecord{
			KeyID:     "kid_new",
			Label:     "ci-bot-2",
			CreatedAt: time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
		},
		plain: "rek_freshly_minted",
	}
	ts := newAccountTestServer(t, auth.Subject{
		Identifier:      "owner-42",
		Tier:            auth.TierAPIKey,
		RateLimitPerMin: 600,
	}, store)

	body := strings.NewReader(`{"label":"ci-bot-2"}`)
	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.KeyCreated `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Plaintext != "rek_freshly_minted" {
		t.Errorf("plaintext not echoed: %q", env.Data.Plaintext)
	}
	if env.Data.KeyID != "kid_new" {
		t.Errorf("KeyID = %q", env.Data.KeyID)
	}
	if store.calls != 1 {
		t.Errorf("Create called %d times, want 1", store.calls)
	}
	if store.gotReq.Identifier != "owner-42" {
		t.Errorf("Create.Identifier = %q, want owner-42 (inherited from caller)", store.gotReq.Identifier)
	}
	if store.gotReq.Tier != auth.TierAPIKey {
		t.Errorf("Create.Tier = %q, want apikey (inherited from caller)", store.gotReq.Tier)
	}
	if store.gotReq.RateLimitPerMin != 600 {
		t.Errorf("Create.RateLimitPerMin = %d, want 600 (inherited from caller)", store.gotReq.RateLimitPerMin)
	}
	if store.gotReq.Label != "ci-bot-2" {
		t.Errorf("Create.Label = %q", store.gotReq.Label)
	}
}

// TestAccountKeysCreate_Unauthenticated — anonymous callers can't
// mint keys. The handler short-circuits before touching the store.
func TestAccountKeysCreate_Unauthenticated(t *testing.T) {
	store := &fakeAccountStore{}
	ts := newAccountTestServer(t, auth.Subject{}, store)

	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json",
		strings.NewReader(`{"label":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if store.calls != 0 {
		t.Errorf("store should not be touched on 401; got %d calls", store.calls)
	}
}

// TestAccountKeysCreate_StoreUnavailable — when the binary didn't
// wire a store (Redis unreachable at startup), POST /keys returns
// 503 rather than misleading the customer with a 401 or 500.
func TestAccountKeysCreate_StoreUnavailable(t *testing.T) {
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-42",
		Tier:       auth.TierAPIKey,
	}, nil) // store deliberately nil

	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json",
		strings.NewReader(`{"label":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestAccountKeysCreate_MissingLabel — the body must include a
// non-empty label. Empty body, missing label field, and explicit
// empty string all 400.
func TestAccountKeysCreate_MissingLabel(t *testing.T) {
	store := &fakeAccountStore{}
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-42",
		Tier:       auth.TierAPIKey,
	}, store)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"empty object", "{}"},
		{"empty label", `{"label":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
	if store.calls != 0 {
		t.Errorf("store should not be touched on validation failure; got %d calls", store.calls)
	}
}

// TestAccountKeysCreate_LabelTooLong — labels over 128 chars 400.
// Surface for sanity (no Redis bytes-budget reason, just a UI cap).
func TestAccountKeysCreate_LabelTooLong(t *testing.T) {
	store := &fakeAccountStore{}
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-42",
		Tier:       auth.TierAPIKey,
	}, store)

	long := strings.Repeat("a", 129)
	body := `{"label":"` + long + `"}`
	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestAccountKeysCreate_MalformedJSON — non-JSON body 400s rather
// than 500ing.
func TestAccountKeysCreate_MalformedJSON(t *testing.T) {
	store := &fakeAccountStore{}
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-42",
		Tier:       auth.TierAPIKey,
	}, store)

	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json",
		strings.NewReader("{not-json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestAccountKeysCreate_StoreFailure — when the store errors, the
// handler returns 500 with a problem+json body. Plaintext is never
// surfaced (the store contract guarantees an empty plaintext on
// failure; the handler obeys).
func TestAccountKeysCreate_StoreFailure(t *testing.T) {
	store := &fakeAccountStore{err: errors.New("redis down")}
	ts := newAccountTestServer(t, auth.Subject{
		Identifier: "owner-42",
		Tier:       auth.TierAPIKey,
	}, store)

	resp, err := http.Post(ts.URL+"/v1/account/keys", "application/json",
		strings.NewReader(`{"label":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	if strings.Contains(string(body[:n]), "rek_") {
		t.Error("response body should not contain plaintext-shaped strings on failure")
	}
}

// TestAccountKeysList_Unauthenticated covers the 401 path.
func TestAccountKeysList_Unauthenticated(t *testing.T) {
	ts := newAccountTestServer(t, auth.Subject{}, nil)
	resp, err := http.Get(ts.URL + "/v1/account/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAccountKeysList_NoStore — endpoint returns 503 if Accounts
// wasn't wired (typical Redis-down scenario).
func TestAccountKeysList_NoStore(t *testing.T) {
	subj := auth.Subject{Identifier: "signup-acme", Tier: auth.TierAPIKey}
	ts := newAccountTestServer(t, subj, nil)
	resp, err := http.Get(ts.URL + "/v1/account/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestAccountKeysList_HappyPath — authenticated caller gets back
// every key whose Identifier matches their Subject.
func TestAccountKeysList_HappyPath(t *testing.T) {
	subj := auth.Subject{Identifier: "signup-acme", Tier: auth.TierAPIKey}
	store := &fakeAccountStore{
		listed: map[string][]auth.APIKeyRecord{
			"signup-acme": {
				{KeyID: "kid_first", Identifier: "signup-acme", Tier: auth.TierAPIKey, RateLimitPerMin: 1000, Label: "first", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				{KeyID: "kid_second", Identifier: "signup-acme", Tier: auth.TierAPIKey, RateLimitPerMin: 10000, Label: "rotated", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
	}
	ts := newAccountTestServer(t, subj, store)

	resp, err := http.Get(ts.URL + "/v1/account/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Data []v1.Account `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(body.Data))
	}
	// Sorted oldest-first.
	if body.Data[0].KeyID != "kid_first" {
		t.Errorf("keys[0] = %q, want kid_first (oldest)", body.Data[0].KeyID)
	}
	if body.Data[1].KeyID != "kid_second" {
		t.Errorf("keys[1] = %q, want kid_second (newest)", body.Data[1].KeyID)
	}
	if body.Data[1].RateLimitPerMin != 10000 {
		t.Errorf("rotated key RateLimitPerMin = %d, want 10000", body.Data[1].RateLimitPerMin)
	}
	if store.listCalls != 1 {
		t.Errorf("store called %d times, want 1", store.listCalls)
	}
}

// TestAccountKeysList_StoreError — list-side Redis blip surfaces
// as 500.
func TestAccountKeysList_StoreError(t *testing.T) {
	subj := auth.Subject{Identifier: "signup-x", Tier: auth.TierAPIKey}
	store := &fakeAccountStore{listErr: errors.New("redis blip")}
	ts := newAccountTestServer(t, subj, store)
	resp, err := http.Get(ts.URL + "/v1/account/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestAccountKeysList_Empty — authenticated caller with no keys
// (somehow — typically can't happen since they have to authenticate
// to reach the endpoint) gets an empty list.
func TestAccountKeysList_Empty(t *testing.T) {
	subj := auth.Subject{Identifier: "signup-empty", Tier: auth.TierAPIKey}
	store := &fakeAccountStore{listed: map[string][]auth.APIKeyRecord{}}
	ts := newAccountTestServer(t, subj, store)
	resp, err := http.Get(ts.URL + "/v1/account/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Data []v1.Account `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 0 {
		t.Errorf("data len = %d, want 0", len(body.Data))
	}
}
