package v1_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// stubSEP10Validator implements auth.SEP10Validator for tests.
// Each method delegates to a function field so tests can wire
// per-test behaviour without subclassing.
type stubSEP10Validator struct {
	challenge func(ctx context.Context, account string) (auth.Challenge, error)
	verify    func(ctx context.Context, signedXDR string) (auth.Token, error)
	verifyJWT func(ctx context.Context, jwt string) (auth.Subject, error)
}

func (s *stubSEP10Validator) Challenge(ctx context.Context, account string) (auth.Challenge, error) {
	return s.challenge(ctx, account)
}

func (s *stubSEP10Validator) Verify(ctx context.Context, signedXDR string) (auth.Token, error) {
	return s.verify(ctx, signedXDR)
}

func (s *stubSEP10Validator) VerifyJWT(ctx context.Context, jwt string) (auth.Subject, error) {
	if s.verifyJWT == nil {
		return auth.Subject{}, errors.New("not used in this test")
	}
	return s.verifyJWT(ctx, jwt)
}

// TestSEP10Challenge_NoValidator_503 — without a validator wired
// the endpoint returns a 503 with the expected error type.
func TestSEP10Challenge_NoValidator_503(t *testing.T) {
	srv := v1.New(v1.Options{}) // no SEP10
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/auth/sep10/challenge?account=GBLAH")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "sep10-unavailable") {
		t.Errorf("error type missing: %s", body)
	}
}

// TestSEP10Challenge_MissingAccount_400 — empty account query
// returns 400.
func TestSEP10Challenge_MissingAccount_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			challenge: func(_ context.Context, _ string) (auth.Challenge, error) {
				t.Error("Challenge should not be invoked without an account")
				return auth.Challenge{}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/auth/sep10/challenge")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Challenge_InvalidAccount_400 — validator returns
// ErrUnauthorized; handler maps to 400.
func TestSEP10Challenge_InvalidAccount_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			challenge: func(_ context.Context, _ string) (auth.Challenge, error) {
				return auth.Challenge{}, auth.ErrUnauthorized
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/auth/sep10/challenge?account=not-a-strkey")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Challenge_HappyPath — body carries the SEP-10 wire fields
// (transaction + network_passphrase) plus convenience timestamps.
func TestSEP10Challenge_HappyPath(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			challenge: func(_ context.Context, _ string) (auth.Challenge, error) {
				return auth.Challenge{
					TransactionXDR:    "FAKE-XDR-BASE64==",
					NetworkPassphrase: "Test SDF Network ; September 2015",
					IssuedAt:          now,
					ValidUntil:        now.Add(15 * time.Minute),
				}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/auth/sep10/challenge?account=GAIN")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"transaction":"FAKE-XDR-BASE64=="`,
		`"network_passphrase":"Test SDF Network ; September 2015"`,
		`"issued_at":"2026-04-28T12:00:00Z"`,
		`"valid_until":"2026-04-28T12:15:00Z"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n  body=%s", want, body)
		}
	}
}

// TestSEP10Token_NoValidator_503 — without a validator wired the
// endpoint returns 503.
func TestSEP10Token_NoValidator_503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{"transaction":"X"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// TestSEP10Token_MissingBody_400 — empty body returns 400.
func TestSEP10Token_MissingBody_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				t.Error("Verify should not be invoked with empty body")
				return auth.Token{}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Token_MalformedJSON_400 — bad JSON returns 400.
func TestSEP10Token_MalformedJSON_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", "{not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Token_MissingTransaction_400 — body present but no
// transaction field returns 400.
func TestSEP10Token_MissingTransaction_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Token_HappyPath — Validator succeeds; response carries
// token + expires_at + account.
func TestSEP10Token_HappyPath(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{
					JWT:       "fake.jwt.token",
					IssuedAt:  now,
					ExpiresAt: now.Add(time.Hour),
					Subject: auth.Subject{
						Identifier: "GCLIENT",
						Tier:       auth.TierSEP10,
						CreatedAt:  now,
					},
				}, nil
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{"transaction":"signed-xdr"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"token":"fake.jwt.token"`,
		`"expires_at":"2026-04-28T13:00:00Z"`,
		`"account":"GCLIENT"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n  body=%s", want, body)
		}
	}
}

// TestSEP10Token_ExpiredChallenge_410 — ErrTokenExpired surfaces
// 410 Gone (the challenge can't be retried — the client must
// request a fresh one).
func TestSEP10Token_ExpiredChallenge_410(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{}, auth.ErrTokenExpired
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{"transaction":"signed-xdr"}`)
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "sep10-challenge-expired") {
		t.Errorf("error type missing: %s", body)
	}
}

// TestSEP10Token_MalformedXDR_400 — ErrTokenMalformed → 400.
func TestSEP10Token_MalformedXDR_400(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{}, auth.ErrTokenMalformed
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{"transaction":"junk"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSEP10Token_VerificationFailed_401 — ErrUnauthorized → 401.
func TestSEP10Token_VerificationFailed_401(t *testing.T) {
	srv := v1.New(v1.Options{
		SEP10: &stubSEP10Validator{
			verify: func(_ context.Context, _ string) (auth.Token, error) {
				return auth.Token{}, auth.ErrUnauthorized
			},
		},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/auth/sep10/token", `{"transaction":"signed"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
