package platform

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
)

// TokenPurpose distinguishes the three flows a single-use email
// token can authorise. Stored on the row so a leaked login
// token can't be replayed against an invite-accept route.
type TokenPurpose string

const (
	TokenPurposeLogin        TokenPurpose = "login"
	TokenPurposeEmailVerify  TokenPurpose = "email-verify"
	TokenPurposeInviteAccept TokenPurpose = "invite-accept"
)

// MagicLinkToken is the row backing a magic-link or email-code
// flow. The plaintext is never stored — TokenHash is sha256 of
// the random bytes the server generated. Callers compare by
// hashing the user-supplied token at consumption time.
type MagicLinkToken struct {
	TokenHash   []byte
	Email       string
	Purpose     TokenPurpose
	ExpiresAt   time.Time
	ConsumedAt  time.Time // zero = unconsumed
	RequestedIP net.IP
	CreatedAt   time.Time
}

// Invite is a pending team-member invitation. The TokenHash key
// matches the magic_link_tokens table's shape so a single
// consumption path handles both magic-login and invite-accept
// flows.
type Invite struct {
	TokenHash       []byte
	AccountID       uuid.UUID
	Email           string
	Role            Role
	InvitedByUserID uuid.UUID
	ExpiresAt       time.Time
	AcceptedAt      time.Time // zero = pending
	RevokedAt       time.Time // zero = active
	CreatedAt       time.Time
}

// TokenStore persists [MagicLinkToken] and [Invite]. The two
// share the token-hash primary key shape (32-byte sha256) but
// live in separate tables because their lifecycle and
// authorisation rules diverge.
type TokenStore interface {
	// CreateMagicLinkToken inserts and returns nothing. Caller
	// already holds the plaintext (to email it).
	CreateMagicLinkToken(ctx context.Context, t MagicLinkToken) error

	// ConsumeMagicLinkToken looks up by hash, atomically marks
	// consumed, and returns the row. ErrNotFound if absent or
	// already consumed; ErrTokenExpired if past expires_at.
	ConsumeMagicLinkToken(ctx context.Context, tokenHash []byte) (MagicLinkToken, error)

	// CreateInvite inserts.
	CreateInvite(ctx context.Context, i Invite) error

	// AcceptInvite atomically marks accepted + returns the row.
	// Same error semantics as ConsumeMagicLinkToken.
	AcceptInvite(ctx context.Context, tokenHash []byte) (Invite, error)

	// RevokeInvite marks revoked. Owner / admin can call it on
	// pending invites they want to cancel.
	RevokeInvite(ctx context.Context, tokenHash []byte) error

	// ListInvitesForAccount returns active (unrevoked, unaccepted)
	// invites. Used by the team-management UI.
	ListInvitesForAccount(ctx context.Context, accountID uuid.UUID) ([]Invite, error)
}
