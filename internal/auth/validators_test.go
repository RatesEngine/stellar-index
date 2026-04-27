package auth

import (
	"context"
	"errors"
	"testing"
)

// TestNoopAPIKeyValidator_FailLoud is the safety check this
// validator exists for. A deployment that enables auth_mode=apikey
// without wiring a real validator MUST fail every request rather
// than silently demote to anonymous (which would be the wrong
// default — apikey was opted into for a reason).
func TestNoopAPIKeyValidator_FailLoud(t *testing.T) {
	_, err := NoopAPIKeyValidator{}.Lookup(context.Background(), "anything")
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Lookup should return ErrNotImplemented, got %v", err)
	}
}

// TestNoopSEP10Validator_FailLoud — same story for the SEP-10
// stub. All three protocol functions return ErrNotImplemented;
// the middleware translates that to 503 so an operator sees the
// misconfiguration on the first failed request rather than
// discovering it from a security audit later.
func TestNoopSEP10Validator_FailLoud(t *testing.T) {
	v := NoopSEP10Validator{}
	if _, err := v.Challenge(context.Background(), "GA…"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Challenge: got %v, want ErrNotImplemented", err)
	}
	if _, err := v.Verify(context.Background(), "any-xdr"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Verify: got %v, want ErrNotImplemented", err)
	}
	if _, err := v.VerifyJWT(context.Background(), "any.jwt.string"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("VerifyJWT: got %v, want ErrNotImplemented", err)
	}
}
