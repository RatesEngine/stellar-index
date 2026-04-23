package middleware

import "net/http"

// SecurityHeaders sets the minimal set of HTTP security response
// headers that are meaningful for a JSON API. It does NOT try to
// replace the reverse-proxy's job — HSTS, CSP, and TLS-bound
// headers belong on the edge where the scheme is known. This
// middleware only sets headers whose value is always safe and
// whose benefit is application-layer.
//
// Set here:
//
//   - X-Content-Type-Options: nosniff — stops a client's
//     heuristic from ever treating a JSON response as HTML or
//     JavaScript. Prevents MIME-sniffing attacks where a hostile
//     value inside a response is sniffed into an executable
//     context by an overly helpful browser.
//
// Not set (deliberate):
//
//   - Strict-Transport-Security — scheme-bound; the HAProxy
//     edge (HA plan §10) adds this with the right max-age and
//     includeSubDomains policy.
//   - Content-Security-Policy — primarily for HTML-serving
//     origins; our responses are JSON, so there's no DOM to
//     restrict.
//   - X-Frame-Options — prevents clickjacking of HTML pages;
//     a JSON response can't be framed.
//   - Referrer-Policy — the API doesn't emit navigation links.
//
// Operators behind a reverse proxy that ALSO sets nosniff get
// the same value twice — idempotent.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
