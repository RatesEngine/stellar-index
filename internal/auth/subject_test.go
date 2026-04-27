package auth

import (
	"context"
	"testing"
)

// TestSubjectFrom_RoundTrip is the everyday path: the auth
// middleware writes a Subject; downstream code reads it back.
// Identifier + Tier + Scopes survive the round-trip.
func TestSubjectFrom_RoundTrip(t *testing.T) {
	want := Subject{
		Identifier: "GAB123",
		Tier:       TierSEP10,
		Scopes:     []string{"price:read", "history:read"},
	}
	ctx := WithSubject(context.Background(), want)
	got, ok := SubjectFrom(ctx)
	if !ok {
		t.Fatal("SubjectFrom: ok=false on a context that had Subject attached")
	}
	if got.Identifier != want.Identifier || got.Tier != want.Tier {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("scopes round-trip: got %v, want %v", got.Scopes, want.Scopes)
	}
}

// TestSubjectFrom_NoSubject covers the bypass case (tests, panic
// recovery, anything that didn't go through the auth middleware).
// Returns zero Subject + ok=false; downstream code interprets that
// as "treat as anonymous-with-empty-id."
func TestSubjectFrom_NoSubject(t *testing.T) {
	got, ok := SubjectFrom(context.Background())
	if ok {
		t.Errorf("SubjectFrom: ok=true on bare context, got %+v", got)
	}
	// Zero value: empty identifier + empty Tier (not "anonymous"!).
	// Anonymous is something the middleware OPTS INTO; absence is
	// a different signal.
	if got.Identifier != "" || got.Tier != "" {
		t.Errorf("expected zero Subject on missing key, got %+v", got)
	}
}

// TestAnonymous_KeepsIdentifier confirms the anonymous helper
// stamps the supplied identifier on the Subject. The rate-limit
// middleware needs a stable key per anonymous client, so the
// helper is the load-bearing constructor.
func TestAnonymous_KeepsIdentifier(t *testing.T) {
	s := Anonymous("anon-deadbeef")
	if s.Tier != TierAnonymous {
		t.Errorf("Anonymous tier = %q, want anonymous", s.Tier)
	}
	if s.Identifier != "anon-deadbeef" {
		t.Errorf("identifier dropped: got %q", s.Identifier)
	}
}

// TestTierString_ProtectsLabels locks down the wire representation
// of each Tier. These values appear in Prometheus labels + the
// rate-limit key prefix; renaming them is a wire break that should
// be deliberate, not accidental.
func TestTierString_ProtectsLabels(t *testing.T) {
	cases := map[Tier]string{
		TierAnonymous: "anonymous",
		TierAPIKey:    "apikey",
		TierSEP10:     "sep10",
		TierOperator:  "operator",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("tier %q stringified as %q (want %q) — wire break", tier, got, want)
		}
	}
}
