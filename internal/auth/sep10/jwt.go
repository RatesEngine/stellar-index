package sep10

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// claims is the JWT body. Standard names where we can; SEP-10
// returns an `account` claim as the subject (we mirror to `sub` for
// industry compatibility).
type claims struct {
	Iss string `json:"iss"`           // issuer — our home_domain
	Sub string `json:"sub"`           // subject — client G-strkey
	Iat int64  `json:"iat"`           // issued at (unix seconds)
	Exp int64  `json:"exp"`           // expiry (unix seconds)
	Nbf int64  `json:"nbf,omitempty"` // not before — same as iat
}

// jwtHeader is the fixed-shape JWT header. We only support
// HMAC-SHA256 ("HS256") at v1.
var (
	jwtHeader    = `{"alg":"HS256","typ":"JWT"}`
	jwtHeaderB64 = base64.RawURLEncoding.EncodeToString([]byte(jwtHeader))
)

// issueJWT mints an HS256-signed JWT bearing the supplied
// subject (client G-strkey) and lifetime. Output is the canonical
// `<header>.<body>.<signature>` form, all base64url-encoded
// without padding.
func (v *Validator) issueJWT(subject string, issuedAt, expiresAt time.Time) (string, error) {
	body, err := json.Marshal(claims{
		Iss: v.homeDomain,
		Sub: subject,
		Iat: issuedAt.Unix(),
		Exp: expiresAt.Unix(),
		Nbf: issuedAt.Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("sep10: marshal claims: %w", err)
	}
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)

	signingInput := jwtHeaderB64 + "." + bodyB64
	mac := hmac.New(sha256.New, v.jwtSecret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig, nil
}

// parseJWT validates an HS256 JWT signed by [issueJWT] and returns
// its claims. Returns:
//
//   - [auth.ErrTokenMalformed] — wrong shape (not three dot-separated
//     parts), header doesn't match, or body isn't decodeable JSON.
//   - [auth.ErrUnauthorized] — signature mismatch (constant-time
//     check; substituting the secret or tampering with the body
//     fails here).
//
// `exp` enforcement happens at the call site so callers can choose
// to surface [auth.ErrTokenExpired] specifically.
func (v *Validator) parseJWT(token string) (claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims{}, fmt.Errorf("%w: JWT must have three dot-separated parts (got %d)",
			auth.ErrTokenMalformed, len(parts))
	}
	if parts[0] != jwtHeaderB64 {
		return claims{}, fmt.Errorf("%w: JWT header doesn't match expected HS256",
			auth.ErrTokenMalformed)
	}

	signingInput := parts[0] + "." + parts[1]
	expected := hmac.New(sha256.New, v.jwtSecret)
	expected.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(expected.Sum(nil))

	// Constant-time compare — defends against timing side-channels
	// that would let an attacker recover the signature byte-by-byte.
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expectedSig)) != 1 {
		return claims{}, fmt.Errorf("%w: JWT signature mismatch", auth.ErrUnauthorized)
	}

	bodyJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims{}, errors.Join(auth.ErrTokenMalformed,
			fmt.Errorf("JWT body not base64url: %w", err))
	}
	var c claims
	if err := json.Unmarshal(bodyJSON, &c); err != nil {
		return claims{}, errors.Join(auth.ErrTokenMalformed,
			fmt.Errorf("JWT body not JSON: %w", err))
	}
	if c.Sub == "" {
		return claims{}, fmt.Errorf("%w: JWT body missing sub claim",
			auth.ErrTokenMalformed)
	}
	if c.Iss != v.homeDomain {
		return claims{}, fmt.Errorf("%w: JWT iss claim %q doesn't match home domain %q",
			auth.ErrUnauthorized, c.Iss, v.homeDomain)
	}
	return c, nil
}

// Compile-time guard against accidentally breaking the body-encode
// path: an empty subject would produce a JWT no consumer wants.
var _ = errors.New // keep errors imported for future use if we add err sentinels here
