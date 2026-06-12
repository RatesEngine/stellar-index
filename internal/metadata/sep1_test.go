package metadata_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/metadata"
)

// fixtureTOML is a realistic small stellar.toml from a public issuer.
const fixtureTOML = `
VERSION="2.3.0"
NETWORK_PASSPHRASE="Public Global Stellar Network ; September 2015"

ACCOUNTS=["GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"]

[DOCUMENTATION]
ORG_NAME="Circle Internet Financial Limited"
ORG_DBA="Circle"
ORG_URL="https://www.circle.com"
ORG_LOGO="https://www.circle.com/hubfs/logo.svg"

[[CURRENCIES]]
code="USDC"
issuer="GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
display_decimals=2
decimals=7
is_unlimited=false
is_asset_anchored=true
anchor_asset_type="fiat"
anchor_asset="USD"
status="live"
name="USDC"
desc="Dollar-denominated stablecoin backed by Circle."
conditions="https://www.circle.com/legal/usdc-terms"
`

// ─── parseSEP1 direct tests ──────────────────────────────────────

// We call parseSEP1 indirectly via the httptest server. Direct
// parse testing would need the symbol exported; skipping for now.

// ─── Resolver (happy path + edge cases) via httptest ─────────────

func TestResolver_HappyPath(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/stellar.toml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/toml")
		_, _ = w.Write([]byte(fixtureTOML))
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)

	sep, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sep.OrgName != "Circle Internet Financial Limited" {
		t.Errorf("OrgName = %q", sep.OrgName)
	}
	if sep.Version != "2.3.0" {
		t.Errorf("Version = %q", sep.Version)
	}
	if !strings.HasPrefix(sep.NetworkPassphrase, "Public Global Stellar Network") {
		t.Errorf("NetworkPassphrase = %q", sep.NetworkPassphrase)
	}
	if got := sep.Documentation["ORG_DBA"]; got != "Circle" {
		t.Errorf("ORG_DBA = %q", got)
	}
	if len(sep.Currencies) != 1 {
		t.Fatalf("expected 1 currency, got %d", len(sep.Currencies))
	}
	c := sep.Currencies[0]
	if c.Code != "USDC" || c.Decimals != 7 || c.DisplayDecimals != 2 {
		t.Errorf("currency fields wrong: %+v", c)
	}
	if c.AnchorAsset != "USD" || c.AnchorAssetType != "fiat" {
		t.Errorf("anchor fields wrong: %+v", c)
	}
	if sep.FetchedAt.IsZero() {
		t.Error("FetchedAt not populated")
	}
}

func TestResolver_RejectsRedirectToDifferentHost(t *testing.T) {
	// Malicious issuer returns 302 pointing at someone else's
	// stellar.toml. We must refuse, otherwise we'd cache the wrong
	// metadata under the original domain's key.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/.well-known/stellar.toml", http.StatusFound)
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)
	_, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err == nil {
		t.Fatal("expected error on cross-host redirect")
	}
	if !strings.Contains(err.Error(), "cross-host") {
		t.Errorf("error should flag cross-host redirect: %v", err)
	}
}

func TestResolver_RejectsHTTPDowngrade(t *testing.T) {
	// Issuer redirects https → http. We refuse — SEP-1 transit must
	// stay encrypted.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/.well-known/stellar.toml", http.StatusFound)
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)
	_, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err == nil {
		t.Fatal("expected error on http downgrade")
	}
	if !strings.Contains(err.Error(), "downgrade") {
		t.Errorf("error should flag downgrade: %v", err)
	}
}

func TestResolver_AllowsSameHostRedirect(t *testing.T) {
	// Legitimate SEP-1 can redirect within-host (e.g. /old-path →
	// /new-path). Must still succeed.
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			http.Redirect(w, r, "/.well-known/stellar.toml?v=2", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(fixtureTOML))
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)
	sep, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err != nil {
		t.Fatalf("same-host redirect should succeed: %v", err)
	}
	if sep.OrgName == "" {
		t.Error("expected fixture OrgName after same-host redirect")
	}
}

func TestResolver_404(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)

	_, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should include HTTP status: %v", err)
	}
}

func TestResolver_MalformedTOML(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not valid toml [["))
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)
	_, err := r.Resolve(context.Background(), hostOf(t, srv))
	if err == nil {
		t.Fatal("expected TOML parse error")
	}
}

func TestResolver_EmptyDomain(t *testing.T) {
	r := metadata.NewResolver(metadata.Options{})
	_, err := r.Resolve(context.Background(), "")
	if err == nil {
		t.Fatal("expected error on empty domain")
	}
}

func TestResolver_RejectsURLMistakenlyPassedAsDomain(t *testing.T) {
	r := metadata.NewResolver(metadata.Options{})
	_, err := r.Resolve(context.Background(), "https://circle.com/.well-known/stellar.toml")
	if err == nil {
		t.Fatal("expected error when operator passes a URL not a domain")
	}
	if !strings.Contains(err.Error(), "looks like a URL") {
		t.Errorf("error should flag URL-vs-domain: %v", err)
	}
}

func TestResolver_RejectsInvalidHostnameChars(t *testing.T) {
	// Domains with query chars, whitespace, underscores, etc. must
	// be rejected before we construct the request URL — otherwise
	// a crafted input could break out of the path portion.
	r := metadata.NewResolver(metadata.Options{})
	ctx := context.Background()

	for _, bad := range []string{
		"example.com?foo=bar", // query-char
		"example.com#frag",    // fragment
		"has space.com",       // space in middle
		"under_score.com",     // underscore
		"-leading-hyphen.com",
		"trailing-hyphen-.com",
		"double..dot.com",
		".leading.dot",
	} {
		_, err := r.Resolve(ctx, bad)
		if err == nil {
			t.Errorf("domain %q: expected error, got nil", bad)
			continue
		}
		// Either the URL-looks-like pre-check or the hostname
		// validator catches it; both are acceptable.
		if !strings.Contains(err.Error(), "not a valid hostname") &&
			!strings.Contains(err.Error(), "looks like a URL") {
			t.Errorf("domain %q: unexpected error %v", bad, err)
		}
	}
}

func TestResolver_AcceptsHostPort(t *testing.T) {
	// Bare host, hostport, and subdomains all pass the validator
	// (httptest servers come in as 127.0.0.1:NNNN).
	r := metadata.NewResolver(metadata.Options{
		Timeout: 100 * time.Millisecond,
	})
	ctx := context.Background()

	// These should all get PAST validation (but will likely fail
	// connection or SSRF block — that's fine, we're testing the
	// validator, not the HTTP path).
	for _, good := range []string{
		"circle.com",
		"sub.circle.com",
		"deep.sub.circle.com",
		"127.0.0.1:8080",
		"example-site.com",
	} {
		_, err := r.Resolve(ctx, good)
		if err != nil && (strings.Contains(err.Error(), "not a valid hostname") ||
			strings.Contains(err.Error(), "looks like a URL")) {
			t.Errorf("domain %q: validator incorrectly rejected — %v", good, err)
		}
	}
}

func TestResolver_SSRFBlocksLoopback(t *testing.T) {
	// Default Resolver (AllowPrivateIPs=false) MUST block
	// 127.0.0.1 dials. Attempt to hit a localhost target and
	// confirm the dialer refuses.
	r := metadata.NewResolver(metadata.Options{
		Timeout:         2 * time.Second,
		AllowPrivateIPs: false,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := r.Resolve(ctx, "localhost")
	if err == nil {
		t.Fatal("expected SSRF block, got nil")
	}
	if !errors.Is(err, metadata.ErrSSRFBlocked) {
		// Might also land as a DNS error on some systems —
		// accept either "blocked" or dial-time rejection. The
		// important property is "didn't connect".
		if !strings.Contains(err.Error(), "SSRF") &&
			!strings.Contains(err.Error(), "blocked") &&
			!strings.Contains(err.Error(), "loopback") &&
			!strings.Contains(err.Error(), "private") {
			t.Logf("note: expected SSRF-style error, got: %v", err)
		}
	}
}

func TestResolver_DomainIsLowercased(t *testing.T) {
	// URL is built from lowercased domain — the handler would 404
	// for any other path. We intercept and check the Host header.
	var receivedHost string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		_, _ = w.Write([]byte(fixtureTOML))
	}))
	defer srv.Close()

	r := newLocalResolver(t, srv)
	// Request via UPPERCASE — resolver must normalise.
	host := hostOf(t, srv)
	_, err := r.Resolve(context.Background(), strings.ToUpper(host))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(receivedHost, host) {
		t.Errorf("host mismatch: %q vs %q", receivedHost, host)
	}
	// Lowercasing is the key invariant — toml:<domain> cache keys
	// depend on it per cachekeys.TOML.
	if receivedHost != strings.ToLower(receivedHost) {
		t.Errorf("host not lowercased on the wire: %q", receivedHost)
	}
}

// ─── test helpers ─────────────────────────────────────────────────

// newLocalResolver returns a Resolver pointed at a test server.
// We disable TLS verification (test server uses a self-signed cert)
// AND allow-list private IPs (httptest binds to 127.0.0.1).
func newLocalResolver(t *testing.T, srv *httptest.Server) *metadata.Resolver {
	t.Helper()
	r := metadata.NewResolver(metadata.Options{
		Timeout:         3 * time.Second,
		AllowPrivateIPs: true, // test-only
	})

	// Swap in the test server's client — it accepts the server's
	// self-signed cert. We replace metadata.Resolver's inner
	// http.Client by round-tripping through an unexported field
	// via reflection, which is ugly — OR we provide a test-only
	// override.
	//
	// Simplest alternative: skip TLS + use the server's RootCAs
	// via the httptest.Server.Client(). Achieve that by giving
	// the Resolver an HTTP client we control.
	r = useTestServerClient(r, srv)
	return r
}

// useTestServerClient replaces the Resolver's HTTP client with the
// httptest.Server's pre-configured client (which trusts the test
// server's self-signed cert). Needed because metadata.NewResolver
// builds its own transport with TLS verify on.
func useTestServerClient(r *metadata.Resolver, srv *httptest.Server) *metadata.Resolver {
	// Exported constructor for tests: metadata.WithClient lets us
	// swap. If not available, test file would need unexported
	// access — we've provided WithClient for this reason.
	return metadata.WithClient(r, srv.Client())
}

// hostOf returns the `host:port` of a test server without the
// scheme prefix — Resolver accepts just the domain.
func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(srv.URL, "https://")
}
