package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetTrustedProxyCIDRs covers parse + apply + roundtrip.
// The package-level config is global state — tests reset it on
// teardown to avoid cross-test pollution.
func TestSetTrustedProxyCIDRs(t *testing.T) {
	resetTrustedProxyConfig(t)

	t.Run("valid CIDRs apply", func(t *testing.T) {
		err := SetTrustedProxyCIDRs([]string{"10.0.0.0/8", "192.168.1.0/24", "::1/128"})
		if err != nil {
			t.Fatalf("SetTrustedProxyCIDRs: %v", err)
		}
		// Round-trip: each CIDR should match an in-range IP.
		cases := []struct {
			ip   string
			want bool
		}{
			{"10.5.5.5", true},
			{"192.168.1.42", true},
			{"::1", true},
			{"8.8.8.8", false},
			{"172.16.0.1", false},
			{"not-an-ip", false},
		}
		for _, tc := range cases {
			got := requestCameViaTrustedProxy(tc.ip)
			if got != tc.want {
				t.Errorf("requestCameViaTrustedProxy(%q) = %v, want %v", tc.ip, got, tc.want)
			}
		}
	})

	t.Run("invalid CIDR rejected", func(t *testing.T) {
		err := SetTrustedProxyCIDRs([]string{"not-a-cidr"})
		if err == nil {
			t.Error("expected error from invalid CIDR; got nil")
		}
	})

	t.Run("empty list clears trust", func(t *testing.T) {
		_ = SetTrustedProxyCIDRs([]string{"10.0.0.0/8"})
		if !requestCameViaTrustedProxy("10.5.5.5") {
			t.Fatal("setup failed")
		}
		if err := SetTrustedProxyCIDRs(nil); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if requestCameViaTrustedProxy("10.5.5.5") {
			t.Error("expected empty allow-list to reject every IP")
		}
	})

	t.Run("whitespace + empty entries skipped", func(t *testing.T) {
		err := SetTrustedProxyCIDRs([]string{"", "  ", "\t10.0.0.0/8\n"})
		if err != nil {
			t.Fatalf("SetTrustedProxyCIDRs: %v", err)
		}
		if !requestCameViaTrustedProxy("10.5.5.5") {
			t.Error("trimmed CIDR should still apply")
		}
	})
}

// TestRightmostUntrustedForwardedFor — the XFF header carries a
// comma-separated chain like `client, proxy1, proxy2` where each
// trusted proxy APPENDS the peer it saw. We walk RIGHT-TO-LEFT and
// return the first entry NOT inside a trusted-proxy CIDR (the
// closest untrusted hop). Taking the leftmost would let a client
// forge its own value. (F-1338)
func TestRightmostUntrustedForwardedFor(t *testing.T) {
	resetTrustedProxyConfig(t)
	// Trust the 10/8 proxy tier for these cases.
	if err := SetTrustedProxyCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := []struct {
		name string
		xff  string
		want string
	}{
		// Single real client behind one trusted proxy: the proxy
		// appended itself but here only the client is in the header.
		{"single untrusted", "1.2.3.4", "1.2.3.4"},
		// client, then a trusted proxy hop appended on the right.
		{"client then trusted hop", "1.2.3.4, 10.0.0.5", "1.2.3.4"},
		// Two trusted hops appended; the leftmost is the real client.
		{"client then two trusted hops", "1.2.3.4, 10.0.0.5, 10.0.0.6", "1.2.3.4"},
		// FORGED leftmost: attacker sent `9.9.9.9` as their own XFF;
		// the proxy appended the attacker's real IP (8.8.8.8) plus
		// the internal hop. Rightmost-untrusted must pick 8.8.8.8,
		// NOT the forged 9.9.9.9.
		{"forged leftmost loses", "9.9.9.9, 8.8.8.8, 10.0.0.5", "8.8.8.8"},
		// All hops trusted → degenerate, caller falls back to peer.
		{"all trusted", "10.0.0.5, 10.0.0.6", ""},
		{"whitespace tolerated", "  1.2.3.4 , 10.0.0.5 ", "1.2.3.4"},
		{"ipv6 single", "2001:db8::1", "2001:db8::1"},
		{"empty header", "", ""},
		// A malformed hop breaks the trust chain → "".
		{"malformed rightmost", "1.2.3.4, not-an-ip", ""},
		{"only comma", ",", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rightmostUntrustedForwardedFor(tc.xff); got != tc.want {
				t.Errorf("rightmostUntrustedForwardedFor(%q) = %q, want %q", tc.xff, got, tc.want)
			}
		})
	}
}

// TestRemoteAddrHost — Go's stdlib leaves http.Request.RemoteAddr
// as `host:port`. We strip the port; ipv6 in brackets needs care.
func TestRemoteAddrHost(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"ipv4 with port", "1.2.3.4:5678", "1.2.3.4"},
		{"ipv6 with port", "[2001:db8::1]:5678", "2001:db8::1"},
		{"ipv4 no port", "1.2.3.4", "1.2.3.4"},
		{"empty", "", ""},
		{"hostname falls through", "my-host", "my-host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remoteAddrHost(tc.raw); got != tc.want {
				t.Errorf("remoteAddrHost(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRemoteIPFor exercises the full XFF-trust decision: when the
// peer is in the trusted-proxy allow-list, we honour XFF; when it
// isn't, we ignore XFF and use the peer.
func TestRemoteIPFor(t *testing.T) {
	resetTrustedProxyConfig(t)

	t.Run("untrusted peer ignores XFF", func(t *testing.T) {
		_ = SetTrustedProxyCIDRs(nil) // no proxies trusted
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8:1234"
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		got := remoteIPFor(req)
		if got != "8.8.8.8" {
			t.Errorf("got %q, want 8.8.8.8 (peer; XFF should be ignored)", got)
		}
	})

	t.Run("trusted peer honours rightmost-untrusted XFF entry", func(t *testing.T) {
		_ = SetTrustedProxyCIDRs([]string{"10.0.0.0/8"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		// Appending proxy chain: real client 1.2.3.4, internal hop
		// 10.0.0.5 appended on the right. Rightmost-untrusted = client.
		req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.5")
		got := remoteIPFor(req)
		if got != "1.2.3.4" {
			t.Errorf("got %q, want 1.2.3.4 (rightmost untrusted XFF entry)", got)
		}
	})

	t.Run("forged leftmost XFF behind appending trusted proxy does NOT win", func(t *testing.T) {
		// Attack: a hostile client SENDS `X-Forwarded-For: 9.9.9.9`.
		// The trusted L7 proxy APPENDS the connection peer it saw
		// (the attacker's real IP 8.8.8.8), then the internal hop
		// 10.0.0.5 is appended. A leftmost parser would attribute the
		// request to the forged 9.9.9.9 — spoofing rate-limit identity
		// and bypassing per-key IP allowlists. The rightmost-untrusted
		// walk must resolve to 8.8.8.8. (F-1338)
		_ = SetTrustedProxyCIDRs([]string{"10.0.0.0/8"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8, 10.0.0.5")
		got := remoteIPFor(req)
		if got == "9.9.9.9" {
			t.Fatalf("forged leftmost XFF won: got %q (per-IP identity spoofed)", got)
		}
		if got != "8.8.8.8" {
			t.Errorf("got %q, want 8.8.8.8 (rightmost untrusted hop)", got)
		}
	})

	t.Run("trusted peer with empty XFF falls back to peer", func(t *testing.T) {
		_ = SetTrustedProxyCIDRs([]string{"10.0.0.0/8"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		// No XFF header set.
		got := remoteIPFor(req)
		if got != "10.0.0.5" {
			t.Errorf("got %q, want 10.0.0.5 (peer fallback)", got)
		}
	})

	t.Run("trusted peer with malformed XFF falls back to peer", func(t *testing.T) {
		_ = SetTrustedProxyCIDRs([]string{"10.0.0.0/8"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("X-Forwarded-For", "garbage")
		got := remoteIPFor(req)
		if got != "10.0.0.5" {
			t.Errorf("got %q, want 10.0.0.5 (malformed XFF; peer fallback)", got)
		}
	})

	t.Run("empty RemoteAddr returns empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ""
		got := remoteIPFor(req)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// resetTrustedProxyConfig clears the package-level allow-list at
// the start AND end of a test, so tests don't leak state to each
// other and don't poison subsequent test runs.
func resetTrustedProxyConfig(t *testing.T) {
	t.Helper()
	if err := SetTrustedProxyCIDRs(nil); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		_ = SetTrustedProxyCIDRs(nil)
	})
}
