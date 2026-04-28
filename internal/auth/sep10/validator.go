package sep10

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"

	"github.com/RatesEngine/rates-engine/internal/auth"
)

// Options configures a [Validator].
type Options struct {
	// ServerSeed is the secret-seed for the server's SEP-10 signing
	// account. The corresponding G-address is what clients sign
	// against. Per SEP-10 best practice this account is dedicated to
	// SEP-10 (no other purpose) and rotated on a schedule.
	ServerSeed string

	// NetworkPassphrase is the Stellar network the challenge is
	// crafted for. Typically one of:
	//   - "Public Global Stellar Network ; September 2015" (pubnet)
	//   - "Test SDF Network ; September 2015" (testnet)
	NetworkPassphrase string

	// WebAuthDomain is the SEP-10 `web_auth_domain` — the host that
	// serves the auth endpoints. Exactly one entry. The challenge
	// transaction's manage_data op carries this so a client can
	// verify it isn't signing for the wrong domain.
	WebAuthDomain string

	// HomeDomain is the issuer's home domain (typically same as
	// WebAuthDomain). The challenge tx's first manage_data op carries
	// `<HomeDomain> auth`; SEP-10 verifies this matches.
	HomeDomain string

	// ChallengeTTL is how long a challenge stays valid (between
	// IssuedAt and ValidUntil). Default 15 minutes per SEP-10
	// recommendation. Operators tune higher for slow clients but
	// 15 m is the standard.
	ChallengeTTL time.Duration

	// JWTTTL is how long an issued JWT stays valid. Default 1 hour;
	// clients refresh by repeating the challenge → verify flow.
	JWTTTL time.Duration

	// JWTSecret is the HMAC-SHA256 key used to sign issued JWTs.
	// MUST be at least 32 bytes of entropy. Operators rotate this
	// at the same cadence as ServerSeed.
	JWTSecret []byte

	// Now overrides time.Now for tests. Production leaves this nil.
	Now func() time.Time
}

// Validator implements [auth.SEP10Validator] using the
// go-stellar-sdk txnbuild helpers for challenge construction +
// verification, plus a hand-rolled HMAC-SHA256 JWT for issuance.
//
// Safe for concurrent use; fields are read-only after construction.
type Validator struct {
	serverKP     *keypair.Full
	network      string
	webDomain    string
	homeDomain   string
	homeDomains  []string
	challengeTTL time.Duration
	jwtTTL       time.Duration
	jwtSecret    []byte
	now          func() time.Time
}

// NewValidator constructs a [Validator] from [Options]. Returns an
// error when required fields are missing or the seed isn't a
// parseable Stellar S-strkey.
func NewValidator(opts Options) (*Validator, error) {
	if opts.ServerSeed == "" {
		return nil, errors.New("sep10: ServerSeed is required")
	}
	if opts.NetworkPassphrase == "" {
		return nil, errors.New("sep10: NetworkPassphrase is required")
	}
	if opts.WebAuthDomain == "" {
		return nil, errors.New("sep10: WebAuthDomain is required")
	}
	if opts.HomeDomain == "" {
		return nil, errors.New("sep10: HomeDomain is required")
	}
	if len(opts.JWTSecret) < 32 {
		return nil, errors.New("sep10: JWTSecret must be at least 32 bytes")
	}

	parsed, err := keypair.Parse(opts.ServerSeed)
	if err != nil {
		return nil, fmt.Errorf("sep10: parse ServerSeed: %w", err)
	}
	full, ok := parsed.(*keypair.Full)
	if !ok {
		return nil, errors.New("sep10: ServerSeed must be a secret seed (S-strkey), not a public key")
	}

	challengeTTL := opts.ChallengeTTL
	if challengeTTL <= 0 {
		challengeTTL = 15 * time.Minute
	}
	jwtTTL := opts.JWTTTL
	if jwtTTL <= 0 {
		jwtTTL = 1 * time.Hour
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Validator{
		serverKP:     full,
		network:      opts.NetworkPassphrase,
		webDomain:    opts.WebAuthDomain,
		homeDomain:   opts.HomeDomain,
		homeDomains:  []string{opts.HomeDomain},
		challengeTTL: challengeTTL,
		jwtTTL:       jwtTTL,
		jwtSecret:    append([]byte(nil), opts.JWTSecret...),
		now:          now,
	}, nil
}

// Challenge implements [auth.SEP10Validator]. Generates a SEP-10
// challenge transaction for clientAccount (a G-strkey) using the
// txnbuild SDK helper.
//
// Returns [auth.ErrUnauthorized] when clientAccount isn't a
// parseable G-strkey — better to fail loudly at challenge issuance
// than to issue a valid-looking transaction the client can never
// satisfy.
func (v *Validator) Challenge(_ context.Context, clientAccount string) (auth.Challenge, error) {
	if _, err := keypair.ParseAddress(clientAccount); err != nil {
		return auth.Challenge{}, errors.Join(auth.ErrUnauthorized,
			fmt.Errorf("parse clientAccount: %w", err))
	}

	tx, err := txnbuild.BuildChallengeTx(
		v.serverKP.Seed(),
		clientAccount,
		v.webDomain,
		v.homeDomain,
		v.network,
		v.challengeTTL,
		nil, // no muxed-account memo
	)
	if err != nil {
		return auth.Challenge{}, fmt.Errorf("sep10: BuildChallengeTx: %w", err)
	}

	xdr, err := tx.Base64()
	if err != nil {
		return auth.Challenge{}, fmt.Errorf("sep10: serialise challenge: %w", err)
	}

	now := v.now().UTC()
	return auth.Challenge{
		TransactionXDR:    xdr,
		NetworkPassphrase: v.network,
		IssuedAt:          now,
		ValidUntil:        now.Add(v.challengeTTL),
	}, nil
}

// Verify implements [auth.SEP10Validator]. Validates a signed
// challenge transaction: structure (per SEP-10 §3.3), server +
// client signatures, time bounds, web auth domain, and home domain.
// On success issues a JWT bearing the authenticated client account.
//
// Errors:
//   - [auth.ErrTokenMalformed] — transaction XDR doesn't parse or
//     the server's signature is missing.
//   - [auth.ErrUnauthorized] — client signature missing/wrong, or
//     account isn't a known signer of the challenge.
//   - [auth.ErrTokenExpired] — challenge's time-bound window has
//     elapsed.
func (v *Validator) Verify(_ context.Context, signedXDR string) (auth.Token, error) {
	// ReadChallengeTx parses the transaction and validates structure
	// (server source account, single sequence number, manage_data
	// ops with the web auth domain, time bounds present). Returns
	// the client account id extracted from the first manage_data op.
	_, clientAccountID, _, _, err := txnbuild.ReadChallengeTx(
		signedXDR,
		v.serverKP.Address(),
		v.network,
		v.webDomain,
		v.homeDomains,
	)
	if err != nil {
		return auth.Token{}, classifyReadChallengeError(err)
	}

	// VerifyChallengeTxSigners validates that the client's signature
	// is present and the server's signature is intact. We don't need
	// the threshold variant — for SEP-10 v1 we accept any single
	// signature from the client account.
	signersFound, err := txnbuild.VerifyChallengeTxSigners(
		signedXDR,
		v.serverKP.Address(),
		v.network,
		v.webDomain,
		v.homeDomains,
		clientAccountID,
	)
	if err != nil {
		return auth.Token{}, classifyVerifyError(err)
	}
	if len(signersFound) == 0 {
		return auth.Token{}, fmt.Errorf("%w: no signers verified", auth.ErrUnauthorized)
	}

	now := v.now().UTC()
	expiresAt := now.Add(v.jwtTTL)
	jwt, err := v.issueJWT(clientAccountID, now, expiresAt)
	if err != nil {
		return auth.Token{}, fmt.Errorf("sep10: issue JWT: %w", err)
	}

	return auth.Token{
		JWT:       jwt,
		IssuedAt:  now,
		ExpiresAt: expiresAt,
		Subject: auth.Subject{
			Identifier: clientAccountID,
			Tier:       auth.TierSEP10,
			CreatedAt:  now,
		},
	}, nil
}

// VerifyJWT implements [auth.SEP10Validator]. Validates a JWT
// previously issued by [Verify] and returns the [auth.Subject] it
// represents. Returns [auth.ErrTokenExpired] when the exp claim has
// passed; [auth.ErrUnauthorized] for any other validation failure
// (bad signature, malformed body, wrong issuer).
func (v *Validator) VerifyJWT(_ context.Context, jwt string) (auth.Subject, error) {
	claims, err := v.parseJWT(jwt)
	if err != nil {
		return auth.Subject{}, err
	}
	now := v.now().Unix()
	if claims.Exp < now {
		return auth.Subject{}, auth.ErrTokenExpired
	}
	return auth.Subject{
		Identifier: claims.Sub,
		Tier:       auth.TierSEP10,
		CreatedAt:  time.Unix(claims.Iat, 0).UTC(),
	}, nil
}

// classifyVerifyError maps the SDK's error returns to our typed
// auth-error vocabulary. The SDK returns wrapped errors with
// readable messages; we pattern-match on substring rather than a
// brittle errors.Is chain because the SDK doesn't expose its
// internal sentinels.
func classifyVerifyError(err error) error {
	msg := err.Error()
	switch {
	case containsAny(msg, "transaction is not within range", "expired", "transaction has expired"):
		return errors.Join(auth.ErrTokenExpired, err)
	default:
		return errors.Join(auth.ErrUnauthorized, err)
	}
}

// classifyReadChallengeError maps ReadChallengeTx errors. Distinct
// from classifyVerifyError because ReadChallengeTx surfaces both
// genuine parse failures (→ ErrTokenMalformed) and time-bound
// expiry (→ ErrTokenExpired) — and we need to keep them distinct
// for callers to render the right HTTP status.
func classifyReadChallengeError(err error) error {
	msg := err.Error()
	wrapped := fmt.Errorf("read challenge: %w", err)
	switch {
	case containsAny(msg, "transaction is not within range", "expired", "transaction has expired"):
		return errors.Join(auth.ErrTokenExpired, wrapped)
	case containsAny(msg, "could not parse", "invalid", "malformed", "unable to unmarshal"):
		return errors.Join(auth.ErrTokenMalformed, wrapped)
	default:
		return errors.Join(auth.ErrUnauthorized, wrapped)
	}
}

// containsAny reports whether s contains any of the substrings.
// stdlib has strings.Contains but not a multi-needle variant; this
// keeps the switch above readable.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(s) >= len(n) && indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Compile-time check.
var _ auth.SEP10Validator = (*Validator)(nil)
