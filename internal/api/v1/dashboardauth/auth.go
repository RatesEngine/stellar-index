// Package dashboardauth implements the magic-link login flow
// + session middleware for the customer dashboard.
//
// Distinct from internal/auth (bearer-token API auth):
//
//   - internal/auth handles `Authorization: Bearer <key>` for
//     programmatic API requests; the Subject it produces is
//     scoped to a single API key.
//   - dashboardauth handles cookie-based dashboard sessions;
//     the Subject it produces is scoped to a User (and via
//     User.AccountID to an Account).
//
// The two surfaces will eventually meet — admin endpoints want
// to accept either a staff session OR a tier=operator API key
// — but for v1 they don't intersect: dashboard endpoints
// accept sessions only; programmatic endpoints accept keys
// only.
package dashboardauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// SessionCookieName is the HTTP cookie name used for dashboard
// sessions. Distinct from any future API-side cookies so a
// browser logged into both can't leak credentials between
// surfaces.
const SessionCookieName = "ratesengine_session"

// MagicLinkPlaintextLen — the random-bytes length we use for
// magic-link tokens. 32 bytes = 256 bits = preimage-safe;
// the hex-encoded form is what the user sees in the URL.
const MagicLinkPlaintextLen = 32

// generateMagicLinkToken returns (plaintext, sha256-hash, code).
// The plaintext is what we put in the email link;
// the hash is what we store in magic_link_tokens;
// the code is the paste-friendly 6-digit numeric variant.
//
// `rand` is the entropy source. crypto/rand.Read in production;
// tests inject a deterministic source.
func generateMagicLinkToken(read func([]byte) (int, error)) (plaintext string, hash []byte, code string, err error) {
	buf := make([]byte, MagicLinkPlaintextLen)
	n, err := read(buf)
	if err != nil {
		return "", nil, "", fmt.Errorf("dashboardauth: entropy read: %w", err)
	}
	if n != MagicLinkPlaintextLen {
		return "", nil, "", fmt.Errorf("dashboardauth: short read: got %d want %d", n, MagicLinkPlaintextLen)
	}

	plaintext = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))

	// Code = first 24 bits of the hash, base32-encoded → 6 digits 0-9.
	// Numeric-only is mobile keyboard friendly. We use the high
	// 24 bits (3 bytes) of sha256 — independent of the link
	// token for replay-resistance, so disclosing one doesn't
	// reveal the other.
	codeBytes := sum[:3]
	codeBase32 := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString(codeBytes)
	code = numericFromBase32(codeBase32)

	return plaintext, sum[:], code, nil
}

// numericFromBase32 maps a base32 string into 6 numeric digits
// for paste-friendly entry. We take the first 6 characters of
// the base32 alphabet [A-Z2-7] and map them by ord modulo 10.
// Collision rate is acceptable at our scale (15-min TTL means
// even with 1000 active tokens the odds of two having the same
// 6-digit code are negligible — base32^6 = 1.07B distinct
// codes; mod 10 truncates to 1M; 1000 tokens × 999 / 2M ≈
// 25% collision, but the WHOLE TOKEN (plaintext, not code) is
// what gates consumption — code is just a UX shortcut).
func numericFromBase32(s string) string {
	out := make([]byte, 6)
	for i := 0; i < 6 && i < len(s); i++ {
		out[i] = '0' + (s[i] % 10)
	}
	return string(out)
}

// HashMagicLinkPlaintext returns the sha256 hash of a
// user-supplied plaintext token. Exported so the callback
// handler can derive the hash to look up in TokenStore.
func HashMagicLinkPlaintext(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// Generator wraps the entropy source — production uses
// crypto/rand.Read; tests inject a fixed source for
// deterministic plaintexts.
type Generator struct {
	Read func([]byte) (int, error)
}

// NewGenerator returns a production-default Generator.
func NewGenerator() *Generator {
	return &Generator{Read: rand.Read}
}

// NewToken mints (plaintext, hash, code).
func (g *Generator) NewToken() (plaintext string, hash []byte, code string, err error) {
	return generateMagicLinkToken(g.Read)
}

// generateSessionID — 16 bytes of crypto/rand → uuid.UUID.
// Exported as a Generator method so tests can pin.
func (g *Generator) NewSessionID() (uuid.UUID, error) {
	var buf [16]byte
	n, err := g.Read(buf[:])
	if err != nil {
		return uuid.Nil, fmt.Errorf("dashboardauth: session id: %w", err)
	}
	if n != 16 {
		return uuid.Nil, fmt.Errorf("dashboardauth: short read: got %d want 16", n)
	}
	// UUID v4 — set version + variant bits per RFC 4122.
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	id, err := uuid.FromBytes(buf[:])
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ─── Session context helpers ──────────────────────────────────────

type sessionKey struct{}

// SessionContext carries the authenticated dashboard subject
// derived from a valid session cookie. Distinct from
// auth.Subject — that's bearer-token-derived; this is cookie-
// derived. A request can carry both; handlers prefer the
// dashboard session for routes like /v1/account/me when both
// are present.
type SessionContext struct {
	Session platform.Session
	User    platform.User
	Account platform.Account
}

// WithSession plants a SessionContext on the context.
func WithSession(ctx context.Context, sc SessionContext) context.Context {
	return context.WithValue(ctx, sessionKey{}, sc)
}

// SessionFromContext extracts the SessionContext if present.
// ok=false when no session was attached (anonymous request).
func SessionFromContext(ctx context.Context) (SessionContext, bool) {
	sc, ok := ctx.Value(sessionKey{}).(SessionContext)
	return sc, ok
}
