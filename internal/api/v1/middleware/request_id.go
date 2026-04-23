package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// HeaderRequestID is the canonical header name. We accept client-
// supplied values (so traces can correlate across services) and
// mint a fresh one otherwise.
const HeaderRequestID = "X-Request-ID"

// RequestID ensures every request has an X-Request-ID.
//
//   - If the incoming request already has one AND it only contains
//     trace-safe characters (alphanum plus `-`, `_`, `.`), it's
//     kept (up to 128 bytes; longer values are truncated).
//   - Otherwise — missing, too exotic, or empty after sanitising —
//     a freshly-generated 16-byte random hex string.
//
// The resulting ID is:
//   - placed on the response header as X-Request-ID
//   - stored in the request context ([RequestIDFrom])
//
// The default UUIDv7 standard was considered but a 32-char hex
// is smaller, collision-free at our scale, and doesn't carry a
// timestamp (which can leak information about clock skew).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !isSafeRequestID(id) {
			// Either empty, too long, or contained CR/LF/control
			// bytes that would otherwise leak into logs or the
			// response header. Fall back to a minted one — a client
			// that sent garbage gets a server-chosen trace ID.
			id = generateRequestID()
		}

		w.Header().Set(HeaderRequestID, id)
		ctx := withString(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isSafeRequestID accepts non-empty strings up to 128 bytes
// containing only trace-safe characters: alphanum + `-` + `_` +
// `.`. Anything else — CR/LF, spaces, quotes, percent-encoded
// bytes, unicode — gets rejected so the middleware mints a fresh
// ID. Keeps headers valid for net/http serialization and logs free
// of injection surface.
func isSafeRequestID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '_', c == '.':
			// ok
		default:
			return false
		}
	}
	return true
}

// generateRequestID produces a 32-char hex identifier from 16 bytes
// of crypto/rand. Panics only if the OS entropy source fails —
// same bar as any crypto code.
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is a panic-worthy condition — every
		// other middleware assumes ids exist.
		panic("middleware: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
