package sep10_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/txnbuild"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
	"github.com/StellarAtlas/stellar-atlas/internal/auth/sep10"
)

const (
	testWebDomain  = "auth.ratesengine.test"
	testHomeDomain = "ratesengine.test"
)

var testJWTSecret = []byte("test-jwt-secret-must-be-32-bytes-or-more!!")

// newTestValidator constructs a Validator with a freshly-generated
// server keypair, testnet passphrase, and a deterministic clock.
// Returns the validator + the server keypair (so tests can introspect
// the server account address) + a clock-mover.
func newTestValidator(t *testing.T) (*sep10.Validator, *keypair.Full, *fakeClock) {
	t.Helper()
	server, err := keypair.Random()
	if err != nil {
		t.Fatalf("keypair.Random: %v", err)
	}
	clk := &fakeClock{now: time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)}
	v, err := sep10.NewValidator(sep10.Options{
		ServerSeed:        server.Seed(),
		NetworkPassphrase: network.TestNetworkPassphrase,
		WebAuthDomain:     testWebDomain,
		HomeDomain:        testHomeDomain,
		ChallengeTTL:      15 * time.Minute,
		JWTTTL:            1 * time.Hour,
		JWTSecret:         testJWTSecret,
		Now:               clk.Now,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v, server, clk
}

// fakeClock returns a configurable time.Now for tests.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// signChallenge signs a challenge XDR with the supplied client
// keypair using the txnbuild API. Mirrors what a real client SDK
// would do.
func signChallenge(t *testing.T, xdr string, client *keypair.Full) string {
	t.Helper()
	tx, err := txnbuild.TransactionFromXDR(xdr)
	if err != nil {
		t.Fatalf("TransactionFromXDR: %v", err)
	}
	innerTx, ok := tx.Transaction()
	if !ok {
		t.Fatal("expected inner transaction")
	}
	signed, err := innerTx.Sign(network.TestNetworkPassphrase, client)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	signedXDR, err := signed.Base64()
	if err != nil {
		t.Fatalf("Base64: %v", err)
	}
	return signedXDR
}

// TestNewValidator_RequiredFields — every required Option field
// must be present; missing one fails loud at construction.
func TestNewValidator_RequiredFields(t *testing.T) {
	server, _ := keypair.Random()
	base := sep10.Options{
		ServerSeed:        server.Seed(),
		NetworkPassphrase: network.TestNetworkPassphrase,
		WebAuthDomain:     testWebDomain,
		HomeDomain:        testHomeDomain,
		JWTSecret:         testJWTSecret,
	}

	mutate := func(f func(*sep10.Options), wantSubstr string) {
		t.Helper()
		opts := base
		f(&opts)
		_, err := sep10.NewValidator(opts)
		if err == nil {
			t.Errorf("expected error for missing %s; got nil", wantSubstr)
			return
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Errorf("error %q lacks %q", err, wantSubstr)
		}
	}
	mutate(func(o *sep10.Options) { o.ServerSeed = "" }, "ServerSeed")
	mutate(func(o *sep10.Options) { o.NetworkPassphrase = "" }, "NetworkPassphrase")
	mutate(func(o *sep10.Options) { o.WebAuthDomain = "" }, "WebAuthDomain")
	mutate(func(o *sep10.Options) { o.HomeDomain = "" }, "HomeDomain")
	mutate(func(o *sep10.Options) { o.JWTSecret = []byte("short") }, "32 bytes")
	mutate(func(o *sep10.Options) { o.ServerSeed = "not-a-strkey" }, "parse ServerSeed")
}

// TestChallenge_HappyPath — Challenge produces a valid SEP-10 XDR
// the SDK's verifier can read back.
func TestChallenge_HappyPath(t *testing.T) {
	v, server, clk := newTestValidator(t)
	client, _ := keypair.Random()

	ch, err := v.Challenge(context.Background(), client.Address())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if ch.TransactionXDR == "" {
		t.Error("TransactionXDR is empty")
	}
	if ch.NetworkPassphrase != network.TestNetworkPassphrase {
		t.Errorf("NetworkPassphrase = %q", ch.NetworkPassphrase)
	}
	if !ch.IssuedAt.Equal(clk.Now()) {
		t.Errorf("IssuedAt = %v, want %v", ch.IssuedAt, clk.Now())
	}
	if got := ch.ValidUntil.Sub(ch.IssuedAt); got != 15*time.Minute {
		t.Errorf("validity window = %v, want 15m", got)
	}

	// SDK round-trip — confirm what we built parses as a SEP-10 challenge.
	_, clientAddrBack, _, _, err := txnbuild.ReadChallengeTx(
		ch.TransactionXDR,
		server.Address(),
		network.TestNetworkPassphrase,
		testWebDomain,
		[]string{testHomeDomain},
	)
	if err != nil {
		t.Fatalf("ReadChallengeTx round-trip: %v", err)
	}
	if clientAddrBack != client.Address() {
		t.Errorf("client account back = %q, want %q", clientAddrBack, client.Address())
	}
}

// TestChallenge_RejectsBadClientAccount — feeding a non-G-strkey
// must fail at challenge issuance, not propagate to verify.
func TestChallenge_RejectsBadClientAccount(t *testing.T) {
	v, _, _ := newTestValidator(t)
	_, err := v.Challenge(context.Background(), "not-a-strkey")
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

// TestVerify_HappyPath — full round-trip: Challenge → client signs
// → Verify accepts + returns a JWT bearing the client's account.
func TestVerify_HappyPath(t *testing.T) {
	v, _, _ := newTestValidator(t)
	client, _ := keypair.Random()

	ch, err := v.Challenge(context.Background(), client.Address())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	signedXDR := signChallenge(t, ch.TransactionXDR, client)

	tok, err := v.Verify(context.Background(), signedXDR)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.JWT == "" {
		t.Error("JWT is empty")
	}
	if tok.Subject.Identifier != client.Address() {
		t.Errorf("Subject.Identifier = %q, want %q", tok.Subject.Identifier, client.Address())
	}
	if tok.Subject.Tier != auth.TierSEP10 {
		t.Errorf("Subject.Tier = %q, want %q", tok.Subject.Tier, auth.TierSEP10)
	}
	if got := tok.ExpiresAt.Sub(tok.IssuedAt); got != 1*time.Hour {
		t.Errorf("token TTL = %v, want 1h", got)
	}
}

// TestVerify_RejectsUnsignedChallenge — a transaction without the
// client's signature MUST NOT produce a token. The SDK's
// VerifyChallengeTxSigners is what catches this.
func TestVerify_RejectsUnsignedChallenge(t *testing.T) {
	v, _, _ := newTestValidator(t)
	client, _ := keypair.Random()

	ch, err := v.Challenge(context.Background(), client.Address())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	// Don't sign — pass the unsigned XDR as if a malicious client
	// tried to skip the signing step.
	_, err = v.Verify(context.Background(), ch.TransactionXDR)
	if err == nil {
		t.Fatal("expected error on unsigned challenge; got nil")
	}
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("err = %v, want wrap of ErrUnauthorized", err)
	}
}

// TestVerify_RejectsWrongSigner — a challenge issued for client A but
// signed by client B must fail. SEP-10's whole point is to bind the
// JWT to the account that proved key ownership.
func TestVerify_RejectsWrongSigner(t *testing.T) {
	v, _, _ := newTestValidator(t)
	clientA, _ := keypair.Random()
	clientB, _ := keypair.Random()

	ch, err := v.Challenge(context.Background(), clientA.Address())
	if err != nil {
		t.Fatal(err)
	}
	signedXDR := signChallenge(t, ch.TransactionXDR, clientB) // wrong signer
	if _, err := v.Verify(context.Background(), signedXDR); err == nil {
		t.Error("expected error when wrong key signs; got nil")
	}
}

// TestVerify_RejectsExpiredChallenge — a challenge whose time-bound
// window has already passed (constructed with a ~200ms TTL + sleep)
// fails with ErrTokenExpired. Real-time-based because the SDK's
// VerifyChallengeTxSigners reads time.Now() directly rather than an
// injected clock.
func TestVerify_RejectsExpiredChallenge(t *testing.T) {
	server, _ := keypair.Random()
	v, err := sep10.NewValidator(sep10.Options{
		ServerSeed:        server.Seed(),
		NetworkPassphrase: network.TestNetworkPassphrase,
		WebAuthDomain:     testWebDomain,
		HomeDomain:        testHomeDomain,
		ChallengeTTL:      2 * time.Second, // SDK enforces ≥1s; 2s gives CI headroom past sleep granularity
		JWTTTL:            1 * time.Hour,
		JWTSecret:         testJWTSecret,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	client, _ := keypair.Random()

	ch, err := v.Challenge(context.Background(), client.Address())
	if err != nil {
		t.Fatal(err)
	}
	signedXDR := signChallenge(t, ch.TransactionXDR, client)

	// Wait past the time-bound window. 4 s past a 2 s TTL leaves
	// generous slack for slow-CI clock granularity (the txnbuild
	// SDK reads wall clock directly, not our injected one).
	time.Sleep(4 * time.Second)

	_, err = v.Verify(context.Background(), signedXDR)
	if err == nil {
		t.Fatal("expected error on expired challenge; got nil")
	}
	// classifyVerifyError maps the SDK's "not within range" / "expired"
	// substring to ErrTokenExpired.
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("err = %v, want wrap of ErrTokenExpired", err)
	}
}

// TestVerify_RejectsMalformedXDR — random garbage as the signed XDR
// surfaces ErrTokenMalformed (not ErrUnauthorized).
func TestVerify_RejectsMalformedXDR(t *testing.T) {
	v, _, _ := newTestValidator(t)
	if _, err := v.Verify(context.Background(), "not-a-base64-xdr"); err == nil {
		t.Error("expected error on malformed XDR; got nil")
	} else if !errors.Is(err, auth.ErrTokenMalformed) {
		t.Errorf("err = %v, want wrap of ErrTokenMalformed", err)
	}
}

// TestVerifyJWT_RoundTrip — Verify issues a JWT; VerifyJWT validates
// it back to the same Subject.
func TestVerifyJWT_RoundTrip(t *testing.T) {
	v, _, _ := newTestValidator(t)
	client, _ := keypair.Random()

	ch, _ := v.Challenge(context.Background(), client.Address())
	signedXDR := signChallenge(t, ch.TransactionXDR, client)
	tok, err := v.Verify(context.Background(), signedXDR)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	subj, err := v.VerifyJWT(context.Background(), tok.JWT)
	if err != nil {
		t.Fatalf("VerifyJWT: %v", err)
	}
	if subj.Identifier != client.Address() {
		t.Errorf("Subject.Identifier = %q, want %q", subj.Identifier, client.Address())
	}
	if subj.Tier != auth.TierSEP10 {
		t.Errorf("Subject.Tier = %q", subj.Tier)
	}
}

// TestVerifyJWT_ExpiredToken — once the JWT's exp claim has passed,
// VerifyJWT returns ErrTokenExpired (not generic ErrUnauthorized).
func TestVerifyJWT_ExpiredToken(t *testing.T) {
	v, _, clk := newTestValidator(t)
	client, _ := keypair.Random()

	ch, _ := v.Challenge(context.Background(), client.Address())
	signedXDR := signChallenge(t, ch.TransactionXDR, client)
	tok, err := v.Verify(context.Background(), signedXDR)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(2 * time.Hour) // past the 1h JWT TTL
	_, err = v.VerifyJWT(context.Background(), tok.JWT)
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
}

// TestVerifyJWT_TamperedSignature — flipping bits in the signature
// portion fails with ErrUnauthorized via constant-time compare.
func TestVerifyJWT_TamperedSignature(t *testing.T) {
	v, _, _ := newTestValidator(t)
	client, _ := keypair.Random()

	ch, _ := v.Challenge(context.Background(), client.Address())
	signedXDR := signChallenge(t, ch.TransactionXDR, client)
	tok, _ := v.Verify(context.Background(), signedXDR)

	// Replace the signature portion with junk.
	parts := strings.Split(tok.JWT, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT shape: got %d parts", len(parts))
	}
	tampered := parts[0] + "." + parts[1] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	_, err := v.VerifyJWT(context.Background(), tampered)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

// TestVerifyJWT_RejectsMalformedShape — JWT must be three
// dot-separated parts; anything else is ErrTokenMalformed.
func TestVerifyJWT_RejectsMalformedShape(t *testing.T) {
	v, _, _ := newTestValidator(t)
	for _, bad := range []string{"", "no-dots", "only.two", "four.parts.are.too.many"} {
		if _, err := v.VerifyJWT(context.Background(), bad); !errors.Is(err, auth.ErrTokenMalformed) {
			t.Errorf("input %q: err = %v, want wrap of ErrTokenMalformed", bad, err)
		}
	}
}

// TestVerifyJWT_RejectsWrongIssuer — a token whose iss claim doesn't
// match the validator's home_domain is unauthorized (different
// deployment / hostile signer with our secret).
func TestVerifyJWT_RejectsWrongIssuer(t *testing.T) {
	v1, _, _ := newTestValidator(t)
	// Build a second validator with a DIFFERENT home_domain but the
	// SAME jwt_secret. v1 should reject v2's tokens.
	server2, _ := keypair.Random()
	v2, err := sep10.NewValidator(sep10.Options{
		ServerSeed:        server2.Seed(),
		NetworkPassphrase: network.TestNetworkPassphrase,
		WebAuthDomain:     "other.example.test",
		HomeDomain:        "other.example.test",
		JWTSecret:         testJWTSecret,
	})
	if err != nil {
		t.Fatal(err)
	}

	client, _ := keypair.Random()
	ch, _ := v2.Challenge(context.Background(), client.Address())
	signedXDR := signChallenge(t, ch.TransactionXDR, client)
	tok, err := v2.Verify(context.Background(), signedXDR)
	if err != nil {
		t.Fatal(err)
	}

	// v1 should reject v2's token even though the HMAC verifies —
	// the iss claim doesn't match v1.homeDomain.
	if _, err := v1.VerifyJWT(context.Background(), tok.JWT); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("v1 accepted v2's token; err = %v", err)
	}
}
