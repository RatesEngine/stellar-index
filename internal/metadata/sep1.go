package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SEP1 is the parsed subset of a stellar.toml document we care
// about. The full raw content is preserved in [SEP1.Raw] for
// callers that need the uncommon fields (FEDERATION_SERVER,
// SIGNING_KEY, HORIZON_URL, etc.) — we don't pre-struct those
// until we have a use for them.
type SEP1 struct {
	// OrgName is the issuer's human-readable organisation name —
	// typically `DOCUMENTATION.ORG_NAME` in the TOML.
	OrgName string

	// Version is the TOML's declared SEP-1 version.
	Version string

	// NetworkPassphrase is the passphrase the operator claims
	// their assets trade on. Should match our configured
	// [StellarConfig.Network] — mismatch is a red flag.
	NetworkPassphrase string

	// Currencies is the [[CURRENCIES]] array — asset-specific
	// metadata per SEP-1 §Currencies. Limited to the fields we
	// surface via /v1/assets today; more land as needed.
	Currencies []Currency

	// Documentation maps each documented field to its value —
	// ORG_NAME, ORG_DBA, ORG_URL, ORG_LOGO, etc. Raw values so
	// callers can selectively surface them.
	Documentation map[string]string

	// Raw is the full parsed TOML as a map — callers that need
	// fields this package doesn't expose can grep.
	Raw map[string]any

	// FetchedAt is when the fetch happened (UTC). Populated by
	// [Resolver.Resolve] — not part of the TOML itself.
	FetchedAt time.Time
}

// Currency is one entry from the [[CURRENCIES]] array. Subset of
// SEP-1 §Currencies. We preserve numeric values as strings to
// avoid precision loss on `max_supply` + `fixed_number` fields
// (ADR-0003).
type Currency struct {
	Code            string
	Issuer          string
	Decimals        int
	DisplayDecimals int
	Name            string
	Description     string
	Conditions      string
	Image           string
	FixedNumber     string
	MaxNumber       string
	IsUnlimited     bool
	AnchorAsset     string
	AnchorAssetType string
	Status          string
}

// Resolver fetches + parses stellar.toml for a home-domain. Safe
// for concurrent use. Stateless across calls (no in-memory cache;
// callers layer a cache via [cachekeys.TOML] if they want one).
type Resolver struct {
	client *http.Client
}

// Options configures a [Resolver].
type Options struct {
	// Timeout is the per-request budget (connect + transfer).
	// Default 10 s matches the Phase-1 design.
	Timeout time.Duration

	// AllowPrivateIPs disables the SSRF guard. Tests set this to
	// true so httptest.Server (which listens on 127.0.0.1) is
	// reachable. Production MUST leave it false.
	AllowPrivateIPs bool
}

// WithClient replaces the Resolver's HTTP client. Test-only —
// production callers use NewResolver's built-in client with its
// SSRF-guarded transport + timeouts.
//
// Use case: httptest.Server listens on 127.0.0.1 with a
// self-signed cert; tests need a client that trusts it and allows
// the loopback address.
func WithClient(r *Resolver, c *http.Client) *Resolver {
	r.client = c
	return r
}

// NewResolver constructs a SEP-1 resolver.
func NewResolver(opts Options) *Resolver {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		// Honour standard proxy env vars (corp proxies etc.) BUT
		// still enforce SSRF — proxies can't bypass our guard
		// because the guard runs pre-dial on the requested host.
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&ssrfDialer{
			inner:           dialer,
			allowPrivateIPs: opts.AllowPrivateIPs,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		// Reject responses larger than 1 MiB — stellar.toml files
		// should be a few KB at most.
		MaxResponseHeaderBytes: 1 << 20,
	}

	return &Resolver{
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// Cap redirect chains — otherwise a malicious domain
			// could bounce through many redirects to consume our
			// budget.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("sep1: stopped after 5 redirects")
				}
				return nil
			},
		},
	}
}

// ErrSSRFBlocked is returned when a SEP-1 URL resolves to a
// private or loopback IP.
var ErrSSRFBlocked = errors.New("sep1: target IP is in a private or reserved range")

// ErrTOMLTooLarge is returned when the response body exceeds
// [maxBodyBytes].
var ErrTOMLTooLarge = errors.New("sep1: TOML body exceeds 1 MiB limit")

// maxBodyBytes caps the stellar.toml body size. SEP-1 files
// shouldn't exceed a few KB; 1 MiB is a generous safety net.
const maxBodyBytes = 1 << 20

// Resolve fetches the stellar.toml for domain and returns the
// parsed SEP1 record.
//
// domain is the bare home-domain (e.g. "circle.com"), NOT a URL.
// The resolver constructs `https://<domain>/.well-known/stellar.toml`.
// Domain is lowercased before use.
func (r *Resolver) Resolve(ctx context.Context, domain string) (*SEP1, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, errors.New("sep1: empty domain")
	}
	// Friendlier error first: a full URL is a very common user
	// mistake ("pass just the hostname"). Other bad characters get
	// the generic hostname-validation error.
	if strings.Contains(domain, "://") || strings.Contains(domain, "/") ||
		strings.HasPrefix(domain, "http:") || strings.HasPrefix(domain, "https:") {
		return nil, fmt.Errorf("sep1: %q looks like a URL; pass just the hostname", domain)
	}
	if !isValidDomainOrHostPort(domain) {
		return nil, fmt.Errorf("sep1: %q is not a valid hostname (or host:port)", domain)
	}

	rawURL := "https://" + domain + "/.well-known/stellar.toml"
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("sep1: bad URL %q: %w", rawURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("sep1: new request: %w", err)
	}
	req.Header.Set("Accept", "text/plain, application/toml, */*;q=0.1")
	req.Header.Set("User-Agent", "rates-engine/metadata (+https://ratesengine.net)")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sep1: %s: %w", domain, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sep1: %s: HTTP %d", domain, resp.StatusCode)
	}
	if resp.ContentLength > maxBodyBytes {
		return nil, ErrTOMLTooLarge
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("sep1: %s: read body: %w", domain, err)
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, ErrTOMLTooLarge
	}

	return parseSEP1(body)
}

// parseSEP1 decodes TOML bytes into a SEP1 struct. Separated from
// the HTTP path so tests can exercise the parser directly.
func parseSEP1(body []byte) (*SEP1, error) {
	raw := map[string]any{}
	if err := toml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("sep1: parse TOML: %w", err)
	}

	sep := &SEP1{
		Raw:           raw,
		FetchedAt:     time.Now().UTC(),
		Documentation: map[string]string{},
	}

	if v, ok := raw["VERSION"].(string); ok {
		sep.Version = v
	}
	if v, ok := raw["NETWORK_PASSPHRASE"].(string); ok {
		sep.NetworkPassphrase = v
	}

	if doc, ok := raw["DOCUMENTATION"].(map[string]any); ok {
		for k, v := range doc {
			if s, ok := v.(string); ok {
				sep.Documentation[k] = s
			}
		}
		if name, ok := doc["ORG_NAME"].(string); ok {
			sep.OrgName = name
		}
	}

	if currencies, ok := raw["CURRENCIES"].([]map[string]any); ok {
		for _, c := range currencies {
			sep.Currencies = append(sep.Currencies, parseCurrency(c))
		}
	}
	// BurntSushi/toml sometimes decodes [[ARRAY]] tables as
	// []any of map[string]any — handle that variant too.
	if arr, ok := raw["CURRENCIES"].([]any); ok {
		for _, entry := range arr {
			if m, ok := entry.(map[string]any); ok {
				sep.Currencies = append(sep.Currencies, parseCurrency(m))
			}
		}
	}

	return sep, nil
}

func parseCurrency(m map[string]any) Currency {
	c := Currency{}
	getString := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	getInt := func(k string) int {
		switch v := m[k].(type) {
		case int:
			return v
		case int64:
			return int(v)
		}
		return 0
	}
	getBool := func(k string) bool {
		if v, ok := m[k].(bool); ok {
			return v
		}
		return false
	}

	c.Code = getString("code")
	c.Issuer = getString("issuer")
	c.Decimals = getInt("decimals")
	c.DisplayDecimals = getInt("display_decimals")
	c.Name = getString("name")
	c.Description = getString("desc")
	c.Conditions = getString("conditions")
	c.Image = getString("image")
	// fixed_number + max_number are NUMERIC-scale values; TOML
	// might decode them as int64 or string — normalise to string.
	c.FixedNumber = normaliseNumeric(m["fixed_number"])
	c.MaxNumber = normaliseNumeric(m["max_number"])
	c.IsUnlimited = getBool("is_unlimited")
	c.AnchorAsset = getString("anchor_asset")
	c.AnchorAssetType = getString("anchor_asset_type")
	c.Status = getString("status")
	return c
}

// normaliseNumeric accepts whatever TOML gave us (int, int64,
// string, nil) and returns a decimal string.
func normaliseNumeric(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	}
	return ""
}

// ─── SSRF guard ───────────────────────────────────────────────────

// ssrfDialer wraps net.Dialer + blocks dials to private / loopback
// / link-local / multicast addresses. The block happens AFTER DNS
// resolution + BEFORE TCP connect, so it catches rebind attacks
// where a hostname resolves differently each call.
type ssrfDialer struct {
	inner           *net.Dialer
	allowPrivateIPs bool // tests only
}

func (d *ssrfDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("sep1 dialer: bad address %q: %w", address, err)
	}

	// Resolve to a specific IP ourselves so the guard acts on the
	// exact address we're about to connect to — not just the
	// hostname's first resolution. This closes DNS-rebinding races.
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("sep1 dialer: resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if d.isBlocked(ip) {
			return nil, fmt.Errorf("%w: %s → %s", ErrSSRFBlocked, host, ip)
		}
	}
	// Connect to the first ALLOWED IP explicitly.
	return d.inner.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// isBlocked reports whether ip is in a range we refuse to dial.
//
// isValidDomainOrHostPort reports whether s is a syntactically valid
// DNS name (optionally with a :port suffix). Guards the URL builder
// from query strings, fragments, whitespace, and other shenanigans
// that would otherwise survive into the request URL.
//
// Tolerates IPv4 literals (for httptest) but not IPv6 bracket form
// — we never ingest IPv6 literals as home-domains in practice.
func isValidDomainOrHostPort(s string) bool {
	// Split off optional :port.
	host, port, hasPort := strings.Cut(s, ":")
	if hasPort {
		// Port must be 1-5 digits, value 1-65535.
		if port == "" || len(port) > 5 {
			return false
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	if host == "" || len(host) > 253 {
		return false
	}
	// Hostname character set per RFC 952 + RFC 1123: letters,
	// digits, hyphens, and dots. No underscores, spaces, query
	// chars, slashes, etc.
	for _, c := range host {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			continue
		}
		return false
	}
	// Labels (between dots) must not start or end with a hyphen,
	// and must not be empty. "-foo" and "foo-" are invalid; ".foo"
	// (leading dot) is invalid; "foo..bar" (double dot) is invalid.
	labels := strings.Split(host, ".")
	for _, lbl := range labels {
		if lbl == "" {
			return false
		}
		if lbl[0] == '-' || lbl[len(lbl)-1] == '-' {
			return false
		}
		if len(lbl) > 63 {
			return false
		}
	}
	return true
}

// Allow-overrides in tests via Options.AllowPrivateIPs.
func (d *ssrfDialer) isBlocked(ip net.IP) bool {
	if d.allowPrivateIPs {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// Cloud-metadata well-known address (the classic SSRF pivot
	// target). IsPrivate() doesn't cover this one.
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
