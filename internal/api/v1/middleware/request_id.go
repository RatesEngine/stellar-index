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
//   - If the incoming request already has one, it's kept as-is
//     (string up to 128 bytes; longer values are truncated).
//   - Otherwise, a freshly-generated 16-byte random hex string.
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
		if id == "" {
			id = generateRequestID()
		} else if len(id) > 128 {
			id = id[:128]
		}

		w.Header().Set(HeaderRequestID, id)
		ctx := withString(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
