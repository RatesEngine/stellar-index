package metadata

import (
	"context"
	"testing"
)

// ─── normaliseNumeric ─────────────────────────────────────────

func TestNormaliseNumeric(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string passes through", "100000000", "100000000"},
		{"int formatted", int(42), "42"},
		{"int64 formatted", int64(1_000_000_000), "1000000000"},
		{"nil → empty", nil, ""},
		{"unsupported type → empty", true, ""},
		{"float64 → empty (TOML lib doesn't emit floats here)", 3.14, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseNumeric(tc.in); got != tc.want {
				t.Errorf("normaliseNumeric(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── Cache.Invalidate ─────────────────────────────────────────

func TestCache_Invalidate_nilRedisIsNoop(t *testing.T) {
	// Production wiring always provides a live redis client, but
	// Cache.Invalidate must NOT panic when rdb is nil — the call
	// path is reachable through configurations that bypass the
	// cache (operator tooling, dry-run modes).
	c := &Cache{rdb: nil}
	if err := c.Invalidate(context.Background(), "example.com"); err != nil {
		t.Errorf("Invalidate with nil rdb returned %v, want nil", err)
	}
}
