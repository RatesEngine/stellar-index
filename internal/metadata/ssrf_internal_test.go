package metadata

import (
	"net"
	"net/http"
	"testing"
)

// TestIsBlocked_CoversCloudMetadataAndReservedRanges pins the deny-list
// against a grid of real-world SSRF pivot addresses. The stdlib
// helpers (IsLoopback, IsLinkLocalUnicast, IsPrivate) cover most
// classes; the extraBlockedNets list closes the cloud-metadata gaps
// that fall outside RFC 1918 / 3927 / 4193.
//
// If this table shrinks, an attacker just got a new SSRF target.
func TestIsBlocked_CoversCloudMetadataAndReservedRanges(t *testing.T) {
	d := &ssrfDialer{}
	cases := []struct {
		ip   string
		want bool
		why  string
	}{
		// Loopback — IsLoopback.
		{"127.0.0.1", true, "IPv4 loopback"},
		{"127.255.255.1", true, "IPv4 loopback /8"},
		{"::1", true, "IPv6 loopback"},

		// Link-local — IsLinkLocalUnicast (includes cloud metadata).
		{"169.254.0.1", true, "IPv4 link-local /16"},
		{"169.254.169.254", true, "AWS/Azure/GCP/DO metadata"},
		{"fe80::1", true, "IPv6 link-local"},

		// RFC 1918 + RFC 4193 — IsPrivate.
		{"10.0.0.1", true, "RFC 1918 10/8"},
		{"172.16.0.1", true, "RFC 1918 172.16/12"},
		{"192.168.0.1", true, "RFC 1918 192.168/16"},
		{"fc00::1", true, "RFC 4193 ULA /7"},
		{"fd00::1", true, "RFC 4193 ULA /7 upper half"},

		// Unspecified.
		{"0.0.0.0", true, "IPv4 unspecified"},
		{"::", true, "IPv6 unspecified"},

		// extraBlockedNets — these FAIL without our additions.
		{"100.64.0.1", true, "RFC 6598 CGNAT /10"},
		{"100.100.100.200", true, "Alibaba Cloud metadata (CGNAT)"},
		{"192.0.0.192", true, "Oracle Cloud metadata"},
		{"192.0.0.8", true, "IETF Protocol Assignments /24"},
		{"198.18.0.1", true, "RFC 2544 benchmarking /15"},
		{"198.19.255.254", true, "RFC 2544 benchmarking upper"},

		// Allowed — real public Internet IPs. These must NOT be
		// blocked or the resolver can't fetch anything.
		{"8.8.8.8", false, "public DNS"},
		{"1.1.1.1", false, "public DNS"},
		{"93.184.216.34", false, "example.com"},
		{"2606:4700:4700::1111", false, "Cloudflare IPv6"},

		// 192.0.0.0/24 is blocked, but 192.0.1.0+ is public again.
		{"192.0.1.1", false, "public (outside IETF /24)"},
		// 100.63.x.x is below CGNAT; 100.128.x.x is above. Both public.
		{"100.63.255.255", false, "public (just below CGNAT)"},
		{"100.128.0.1", false, "public (just above CGNAT)"},
	}

	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("test bug: bad IP %q", c.ip)
		}
		got := d.isBlocked(ip)
		if got != c.want {
			t.Errorf("isBlocked(%s) = %v, want %v — %s", c.ip, got, c.want, c.why)
		}
	}
}

// TestResolverTransportDisablesProxy is the F-1336 regression. The
// SEP-1 fetch transport MUST NOT honour HTTP(S)_PROXY: with a proxy
// configured, the transport dials the PROXY (which passes our
// DialContext SSRF guard) and hands it the issuer-controlled target
// host, so the guard never validates the real target — letting an
// attacker who controls a home-domain reach internal hosts straight
// through the proxy. Pin Proxy == nil so every fetch is forced
// through our own SSRF-checked dialer.
func TestResolverTransportDisablesProxy(t *testing.T) {
	r := NewResolver(Options{})
	tr, ok := r.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("resolver transport is %T, want *http.Transport", r.client.Transport)
	}
	if tr.Proxy != nil {
		t.Error("SEP-1 transport.Proxy must be nil (F-1336): a configured " +
			"HTTP(S)_PROXY would dial the proxy and bypass the SSRF guard on the target host")
	}
}

// TestIsBlocked_AllowPrivateIPsBypass confirms the test-only escape
// hatch still works — necessary for httptest-backed tests that need
// to dial 127.0.0.1.
func TestIsBlocked_AllowPrivateIPsBypass(t *testing.T) {
	d := &ssrfDialer{allowPrivateIPs: true}
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200"} {
		if d.isBlocked(net.ParseIP(s)) {
			t.Errorf("AllowPrivateIPs=true must not block %s", s)
		}
	}
}
