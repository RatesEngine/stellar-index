// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// KeyPolicy returns a Middleware that enforces the per-key policy
// fields the dashboard exposes — IP allowlist, Referer allowlist,
// and per-endpoint permissions. Runs AFTER [Auth] so the
// authenticated Subject is on the request context; before
// [RateLimit] so policy-rejected requests never spend a rate-limit
// token.
//
// F-1226 (codex audit-2026-05-12): the dashboard let customers
// configure these fields but no middleware enforced them at
// request time. A 403 reply here is the same shape as Auth's
// problem+json; the body carries which control rejected (ip /
// referer / permission) so dashboard users can debug their own
// configuration.
//
// Behaviour per check:
//
//   - IPAllowlist empty: skip the IP gate (no opt-in).
//     Non-empty: the request's resolved client IP (via
//     [RemoteIP] which honours the trusted-proxy CIDRs) must
//     fall in at least one prefix.
//   - RefererAllowlist empty: skip. Non-empty: the request's
//     Referer header must be present and its host must exactly
//     match one entry (case-insensitive). Empty / missing
//     Referer is rejected — the customer asked for the gate.
//   - AllowAllPermissions=true: permission gate passes unless
//     a deny entry matches. False: at least one allow entry
//     must match, and no deny entry may.
//
// Anonymous subjects (Tier=anonymous) bypass every check — they
// don't carry per-key policy. Operator-tier subjects also bypass
// permissions but not IP/Referer (the operator may have a static
// IP allowlist for staff credentials).
func KeyPolicy() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := auth.SubjectFrom(r.Context())
			if !ok || subject.Tier == auth.TierAnonymous {
				next.ServeHTTP(w, r)
				return
			}

			if err := checkIPAllowlist(r, subject.IPAllowlist); err != nil {
				writeKeyPolicyDenied(w, r, "ip-not-allowed", err.Error())
				return
			}
			if err := checkRefererAllowlist(r, subject.RefererAllowlist); err != nil {
				writeKeyPolicyDenied(w, r, "referer-not-allowed", err.Error())
				return
			}
			if subject.Tier != auth.TierOperator {
				if err := checkPermissions(r, subject); err != nil {
					writeKeyPolicyDenied(w, r, "permission-denied", err.Error())
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func checkIPAllowlist(r *http.Request, allow []netip.Prefix) error {
	if len(allow) == 0 {
		return nil
	}
	raw := RemoteIP(r)
	if raw == "" {
		return fmt.Errorf("could not resolve client IP")
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return fmt.Errorf("client IP %q is malformed", raw)
	}
	for _, prefix := range allow {
		if prefix.Contains(addr) {
			return nil
		}
	}
	return fmt.Errorf("client IP %s not in this key's allowlist", addr.String())
}

func checkRefererAllowlist(r *http.Request, allow []string) error {
	if len(allow) == 0 {
		return nil
	}
	raw := r.Header.Get("Referer")
	if raw == "" {
		return fmt.Errorf("referer header is required for this key but was missing")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("referer header %q is malformed", raw)
	}
	host := strings.ToLower(u.Host)
	for _, entry := range allow {
		if strings.EqualFold(strings.TrimSpace(entry), host) {
			return nil
		}
	}
	return fmt.Errorf("referer host %q not in this key's allowlist", host)
}

func checkPermissions(r *http.Request, subject auth.Subject) error {
	if len(subject.DenyPermissions) > 0 && permissionMatches(r, subject.DenyPermissions) {
		return fmt.Errorf("this key is denied access to %s %s", r.Method, r.URL.Path)
	}
	if subject.AllowAllPermissions {
		return nil
	}
	if len(subject.AllowPermissions) == 0 {
		// Closed posture: no allow entries + AllowAllPermissions=false
		// means "no endpoints permitted". This shouldn't happen for
		// keys minted via the dashboard (the UI defaults to All:true);
		// guard against a future revoked-but-not-disabled key shape.
		return fmt.Errorf("this key has no permission entries; contact account owner")
	}
	if permissionMatches(r, subject.AllowPermissions) {
		return nil
	}
	return fmt.Errorf("this key is not permitted on %s %s", r.Method, r.URL.Path)
}

func permissionMatches(r *http.Request, entries []auth.SubjectPermissionEntry) bool {
	exact := r.Method + " " + r.URL.Path
	for _, e := range entries {
		if e.Endpoint != "" && e.Endpoint == exact {
			return true
		}
		if e.EndpointPrefix != "" && strings.HasPrefix(r.URL.Path, e.EndpointPrefix) {
			return true
		}
	}
	return false
}

// writeKeyPolicyDenied emits an RFC 9457 problem+json 403 with a
// reason-specific `type` URI so dashboard clients can render
// "your IP isn't in this key's allowlist" vs "you can't call this
// endpoint with this key" without parsing the detail string.
func writeKeyPolicyDenied(w http.ResponseWriter, r *http.Request, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	body, _ := json.Marshal(map[string]any{
		"type":     "https://api.ratesengine.net/errors/" + slug,
		"title":    "Forbidden",
		"status":   http.StatusForbidden,
		"detail":   detail,
		"instance": r.URL.Path,
	})
	_, _ = w.Write(body)
}
