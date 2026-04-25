package bitstamp

import (
	"strings"
	"testing"
	"time"
)

// ─── granularityToSeconds ────────────────────────────────────

func TestGranularityToSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{1 * time.Minute, 60},
		{3 * time.Minute, 180},
		{5 * time.Minute, 300},
		{15 * time.Minute, 900},
		{30 * time.Minute, 1800},
		{1 * time.Hour, 3600},
		{2 * time.Hour, 7200},
		{4 * time.Hour, 14400},
		{6 * time.Hour, 21600},
		{12 * time.Hour, 43200},
		{24 * time.Hour, 86400},
		{3 * 24 * time.Hour, 259200},
	}
	for _, tc := range cases {
		t.Run(tc.in.String(), func(t *testing.T) {
			got, err := granularityToSeconds(tc.in)
			if err != nil {
				t.Fatalf("granularityToSeconds(%v): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGranularityToSeconds_unsupported(t *testing.T) {
	for _, d := range []time.Duration{
		7 * time.Minute,    // not in API list
		90 * time.Second,   // sub-supported
		2 * 24 * time.Hour, // 2d not in list (1d / 3d only)
		0,                  // zero
		-1 * time.Hour,     // negative
	} {
		t.Run(d.String(), func(t *testing.T) {
			_, err := granularityToSeconds(d)
			if err == nil {
				t.Errorf("expected error for unsupported granularity %v, got nil", d)
			}
			if !strings.Contains(err.Error(), "unsupported granularity") {
				t.Errorf("error %q missing the \"unsupported granularity\" fragment", err.Error())
			}
		})
	}
}

// ─── parseMicrotimestamp ─────────────────────────────────────

func TestParseMicrotimestamp_microPreferred(t *testing.T) {
	// us=1_700_000_000_123_456 → epoch s=1_700_000_000.123456.
	got, err := parseMicrotimestamp("1700000000123456", "0")
	if err != nil {
		t.Fatalf("parseMicrotimestamp: %v", err)
	}
	want := time.UnixMicro(1_700_000_000_123_456).UTC()
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Also verify second-resolution fallback was NOT used (would
	// land on 1700000000.0 if it had been).
	if got.Nanosecond() == 0 {
		t.Errorf("expected microsecond precision, got %v", got)
	}
}

func TestParseMicrotimestamp_secondsFallback(t *testing.T) {
	// micro empty, secs populated → second-resolution timestamp.
	got, err := parseMicrotimestamp("", "1700000000")
	if err != nil {
		t.Fatalf("parseMicrotimestamp: %v", err)
	}
	want := time.Unix(1_700_000_000, 0).UTC()
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseMicrotimestamp_bothEmpty(t *testing.T) {
	_, err := parseMicrotimestamp("", "")
	if err == nil {
		t.Error("expected error on empty inputs, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q missing \"empty\" fragment", err.Error())
	}
}

func TestParseMicrotimestamp_microMalformed(t *testing.T) {
	_, err := parseMicrotimestamp("not-a-number", "1700000000")
	if err == nil {
		t.Error("expected error on malformed microtimestamp, got nil")
	}
	// Must NOT silently fall through to the seconds field — that
	// would mask upstream-corruption bugs.
	if !strings.Contains(err.Error(), "microtimestamp") {
		t.Errorf("error %q missing \"microtimestamp\" fragment", err.Error())
	}
}

func TestParseMicrotimestamp_secondsMalformed(t *testing.T) {
	_, err := parseMicrotimestamp("", "not-a-number")
	if err == nil {
		t.Error("expected error on malformed seconds timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Errorf("error %q missing \"timestamp\" fragment", err.Error())
	}
}
