package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// fakeSignupVerifier is a per-test in-memory `v1.SignupVerifier`
// that mirrors the Redis-backed adapter's GETDEL single-use
// semantics. F-1218 (codex audit-2026-05-12).
type fakeSignupVerifier struct {
	mu     sync.Mutex
	tokens map[string]string // token → keyID; entries removed on consume
	err    error
}

func newFakeSignupVerifier(initial map[string]string) *fakeSignupVerifier {
	out := &fakeSignupVerifier{tokens: map[string]string{}}
	for k, v := range initial {
		out.tokens[k] = v
	}
	return out
}

func (f *fakeSignupVerifier) Consume(_ context.Context, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	keyID, ok := f.tokens[token]
	if !ok {
		return "", auth.ErrSignupVerifyNotFound
	}
	delete(f.tokens, token) // single-use
	return keyID, nil
}

// Reserve mirrors auth.RedisSignupVerifier.Reserve for tests
// that exercise the wave-44 token-issue side of the flow.
// `ttl` is honoured by the production impl but ignored here
// (the in-memory fake doesn't expire entries between calls).
func (f *fakeSignupVerifier) Reserve(_ context.Context, token, keyID string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if existing, ok := f.tokens[token]; ok {
		if existing == keyID {
			return nil
		}
		return auth.ErrSignupVerifyReserved
	}
	f.tokens[token] = keyID
	return nil
}

func newSignupVerifyTestServer(t *testing.T, verifier v1.SignupVerifier) *httptest.Server {
	t.Helper()
	srv := v1.New(v1.Options{
		Auth:           fakeAuthMiddleware(auth.Subject{}),
		SignupVerifier: verifier,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestSignupVerify_HappyPath — token consumed → 200 +
// {verified:true, key_id:"…"}; second click returns 404
// (single-use).
func TestSignupVerify_HappyPath(t *testing.T) {
	verifier := newFakeSignupVerifier(map[string]string{
		"tok_abc": "kid_alpha",
	})
	ts := newSignupVerifyTestServer(t, verifier)

	resp, err := http.Get(ts.URL + "/v1/signup/verify?token=tok_abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Data v1.SignupVerifyResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Data.Verified {
		t.Errorf("verified = false, want true")
	}
	if got.Data.KeyID != "kid_alpha" {
		t.Errorf("key_id = %q, want kid_alpha", got.Data.KeyID)
	}

	// Second call → 404 (token already consumed).
	resp2, err := http.Get(ts.URL + "/v1/signup/verify?token=tok_abc")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second click status = %d, want 404 (single-use)", resp2.StatusCode)
	}
}

// TestSignupVerify_UnknownToken — a never-Reserved token gets 404.
func TestSignupVerify_UnknownToken(t *testing.T) {
	verifier := newFakeSignupVerifier(nil)
	ts := newSignupVerifyTestServer(t, verifier)
	resp, err := http.Get(ts.URL + "/v1/signup/verify?token=tok_unknown")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestSignupVerify_MissingToken — `?token=` empty / absent is
// a 400 with a Problem-JSON body.
func TestSignupVerify_MissingToken(t *testing.T) {
	verifier := newFakeSignupVerifier(nil)
	ts := newSignupVerifyTestServer(t, verifier)
	resp, err := http.Get(ts.URL + "/v1/signup/verify")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSignupVerify_NoVerifierConfigured — Redis-less deployment
// (verifier nil) returns 503 with a clear message instead of
// silently 404'ing every token.
func TestSignupVerify_NoVerifierConfigured(t *testing.T) {
	ts := newSignupVerifyTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/v1/signup/verify?token=anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestSignupVerify_StoreError — a verifier-side non-NotFound
// error surfaces as 500 (so operators can alert), distinct from
// the 404 happy-path-loser surface customers should expect on
// click-twice.
func TestSignupVerify_StoreError(t *testing.T) {
	verifier := newFakeSignupVerifier(nil)
	verifier.err = errors.New("redis blip")
	ts := newSignupVerifyTestServer(t, verifier)
	resp, err := http.Get(ts.URL + "/v1/signup/verify?token=anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestSignupVerify_TokenWithSpecialChars — query-escaped tokens
// round-trip cleanly. Defence-in-depth: the actual tokens are
// hex-only, but the handler shouldn't break on a customer
// pasting a quoted URL.
func TestSignupVerify_TokenWithSpecialChars(t *testing.T) {
	const tok = "tok with spaces"
	verifier := newFakeSignupVerifier(map[string]string{tok: "kid_x"})
	ts := newSignupVerifyTestServer(t, verifier)
	q := url.Values{}
	q.Set("token", tok)
	resp, err := http.Get(ts.URL + "/v1/signup/verify?" + q.Encode())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// fakeSignupVerifyEmailer records every send so the F-1218
// wave-44 signup integration tests can assert that the
// signup-handler actually emails the verify URL.
type fakeSignupVerifyEmailer struct {
	mu      sync.Mutex
	sends   []sentEmail
	sendErr error
}

type sentEmail struct {
	to        string
	verifyURL string
}

func (f *fakeSignupVerifyEmailer) SendSignupVerification(_ context.Context, toEmail, verifyURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sends = append(f.sends, sentEmail{to: toEmail, verifyURL: verifyURL})
	return nil
}

// TestSignup_IssuesVerificationToken_WhenWired — F-1218 wave
// 44: when BOTH a SignupVerifier and a SignupVerifyEmailer
// are configured, POST /v1/signup must (1) Reserve a token
// against the new keyID, (2) email the verify URL, (3) set
// `email_verification_sent: true` on the wire.
func TestSignup_IssuesVerificationToken_WhenWired(t *testing.T) {
	store := &fakeAccountStore{
		rec: auth.APIKeyRecord{
			KeyID:           "kid_verify",
			Identifier:      "signup-aaaaaaaaaaaaaaaa",
			Tier:            auth.TierAPIKey,
			RateLimitPerMin: 1000,
			CreatedAt:       time.Now().UTC(),
		},
		plain: "rek_plain",
	}
	signups := newFakeSignupTracker()
	verifier := newFakeSignupVerifier(nil)
	emailer := &fakeSignupVerifyEmailer{}

	srv := v1.New(v1.Options{
		Auth:                fakeAuthMiddleware(auth.Subject{}),
		Accounts:            store,
		Signups:             signups,
		SignupVerifier:      verifier,
		SignupVerifyEmailer: emailer,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postSignup(t, ts, `{"email":"verify@example.com"}`)
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
	if !body.Data.EmailVerificationSent {
		t.Errorf("EmailVerificationSent = false, want true (verifier + emailer both wired)")
	}
	emailer.mu.Lock()
	defer emailer.mu.Unlock()
	if len(emailer.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(emailer.sends))
	}
	if emailer.sends[0].to != "verify@example.com" {
		t.Errorf("To = %q, want verify@example.com", emailer.sends[0].to)
	}
	if !strings.Contains(emailer.sends[0].verifyURL, "/v1/signup/verify?token=") {
		t.Errorf("verifyURL = %q, missing /v1/signup/verify?token= path", emailer.sends[0].verifyURL)
	}
	// The Reserved token must round-trip through Consume.
	verifier.mu.Lock()
	if len(verifier.tokens) != 1 {
		t.Errorf("Reserved tokens = %d, want 1", len(verifier.tokens))
	}
	verifier.mu.Unlock()
}

// TestSignup_SkipsVerificationWhenEmailerMissing — wave 44
// degradation: verifier wired but no emailer → email_verification_sent
// must be false (operator hasn't fully turned on the flow), and the
// signup still returns the key.
func TestSignup_SkipsVerificationWhenEmailerMissing(t *testing.T) {
	store := &fakeAccountStore{
		rec:   auth.APIKeyRecord{KeyID: "kid_noemail", Tier: auth.TierAPIKey},
		plain: "rek_p",
	}
	signups := newFakeSignupTracker()
	verifier := newFakeSignupVerifier(nil)
	// Emailer intentionally nil.

	srv := v1.New(v1.Options{
		Auth:           fakeAuthMiddleware(auth.Subject{}),
		Accounts:       store,
		Signups:        signups,
		SignupVerifier: verifier,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postSignup(t, ts, `{"email":"a@b.example"}`)
	defer resp.Body.Close()
	var body struct {
		Data v1.SignupResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.EmailVerificationSent {
		t.Errorf("EmailVerificationSent = true, want false (no emailer)")
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	if len(verifier.tokens) != 0 {
		t.Errorf("Reserved tokens = %d, want 0 (no emailer → skip Reserve too)", len(verifier.tokens))
	}
}

// TestSignup_SendErrorIsNonFatal — wave 44 best-effort: if
// SendSignupVerification fails, the signup still returns 200
// with the key + email_verification_sent: false. The customer
// gets their key; an alert fires on the operator side via the
// WARN log path.
func TestSignup_SendErrorIsNonFatal(t *testing.T) {
	store := &fakeAccountStore{
		rec:   auth.APIKeyRecord{KeyID: "kid_senderr", Tier: auth.TierAPIKey},
		plain: "rek_p",
	}
	signups := newFakeSignupTracker()
	verifier := newFakeSignupVerifier(nil)
	emailer := &fakeSignupVerifyEmailer{sendErr: errors.New("resend timeout")}

	srv := v1.New(v1.Options{
		Auth:                fakeAuthMiddleware(auth.Subject{}),
		Accounts:            store,
		Signups:             signups,
		SignupVerifier:      verifier,
		SignupVerifyEmailer: emailer,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postSignup(t, ts, `{"email":"err@example.com"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (send error must not fail signup)", resp.StatusCode)
	}
	var body struct {
		Data v1.SignupResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.EmailVerificationSent {
		t.Errorf("EmailVerificationSent = true, want false (send failed)")
	}
}

// _ keeps the strings import live across edits.
var _ = strings.TrimSpace
