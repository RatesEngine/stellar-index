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

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/auth"
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

// _ keeps the strings import live across edits.
var _ = strings.TrimSpace
