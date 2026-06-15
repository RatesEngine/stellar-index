package blend_backstop

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/events"
)

// ─── real-lake golden frames (base64 SCVal) ──────────────────────
//
// Captured from real mainnet ledgers; the contract id is the V2
// backstop. These PIN the reverse-engineered schemas: if a decode
// helper drifts, the asserted promoted fields change.

const goldenContractV2 = MainnetBackstopV2

var goldenFrames = map[string]struct {
	topics []string
	data   string
}{
	"deposit": {
		topics: []string{
			"AAAADwAAAAdkZXBvc2l0AA==",
			"AAAAEgAAAAESnMjMYzbx/bvcwPOYNDTDzbhf2eqFaXo3gtMY2HSlgA==",
			"AAAAEgAAAAAAAAAAWYH8JZJI+MYX5VM6OjGDm14Ek5xVtC/UzF0n7glLQkw=",
		},
		data: "AAAAEAAAAAEAAAACAAAACgAAAAAAAAAAAAAAAAqxpkoAAAAKAAAAAAAAAAAAAAAAB88kBQ==",
	},
	"claim": {
		topics: []string{
			"AAAADwAAAAVjbGFpbQAAAA==",
			"AAAAEgAAAAAAAAAAWYH8JZJI+MYX5VM6OjGDm14Ek5xVtC/UzF0n7glLQkw=",
		},
		data: "AAAACgAAAAAAAAAAAAAAAAqxpko=",
	},
	"distribute": {
		topics: []string{"AAAADwAAAApkaXN0cmlidXRlAAA="},
		data:   "AAAACgAAAAAAAAAAAAABjj4rKgA=",
	},
	"queue_withdrawal": {
		topics: []string{
			"AAAADwAAABBxdWV1ZV93aXRoZHJhd2Fs",
			"AAAAEgAAAAESnMjMYzbx/bvcwPOYNDTDzbhf2eqFaXo3gtMY2HSlgA==",
			"AAAAEgAAAAAAAAAAWYH8JZJI+MYX5VM6OjGDm14Ek5xVtC/UzF0n7glLQkw=",
		},
		data: "AAAAEAAAAAEAAAACAAAACgAAAAAAAAAAAAAAACuGk0QAAAAFAAAAAGpGyk0=",
	},
}

func goldenEvent(t *testing.T, name string) *events.Event {
	t.Helper()
	f, ok := goldenFrames[name]
	if !ok {
		t.Fatalf("no golden frame %q", name)
	}
	return &events.Event{
		Type:           "contract",
		ContractID:     goldenContractV2,
		Ledger:         56_700_000,
		LedgerClosedAt: "2026-06-15T00:00:00Z",
		TxHash:         "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef0",
		Topic:          f.topics,
		Value:          f.data,
	}
}

// TestGolden_Deposit pins the deposit decode against the real lake
// sample — the canonical sanity check for the two-i128 body shape.
func TestGolden_Deposit(t *testing.T) {
	t.Parallel()
	d, err := decodeDeposit(goldenEvent(t, "deposit"))
	if err != nil {
		t.Fatalf("decodeDeposit: %v", err)
	}
	if d.Amount != "179414602" {
		t.Errorf("Amount = %q, want 179414602", d.Amount)
	}
	if d.Amount2 != "131015685" {
		t.Errorf("Amount2 (shares) = %q, want 131015685", d.Amount2)
	}
	if d.Pool == "" || d.Pool[0] != 'C' {
		t.Errorf("Pool should be a contract strkey, got %q", d.Pool)
	}
	if d.UserAddress == "" || d.UserAddress[0] != 'G' {
		t.Errorf("UserAddress should be an account strkey, got %q", d.UserAddress)
	}
}

// TestGolden_Claim — claim carries a user + amount and NO pool.
func TestGolden_Claim(t *testing.T) {
	t.Parallel()
	d, err := decodeClaim(goldenEvent(t, "claim"))
	if err != nil {
		t.Fatalf("decodeClaim: %v", err)
	}
	if d.Amount != "179414602" {
		t.Errorf("Amount = %q, want 179414602", d.Amount)
	}
	if d.Pool != "" {
		t.Errorf("claim should carry no pool, got %q", d.Pool)
	}
	if d.UserAddress == "" {
		t.Error("claim should carry a user")
	}
}

// TestGolden_Distribute — single i128 amount, no pool/user.
func TestGolden_Distribute(t *testing.T) {
	t.Parallel()
	d, err := decodeDistribute(goldenEvent(t, "distribute"))
	if err != nil {
		t.Fatalf("decodeDistribute: %v", err)
	}
	if d.Amount != "1710440000000" {
		t.Errorf("Amount = %q, want 1710440000000", d.Amount)
	}
	if d.Pool != "" || d.UserAddress != "" {
		t.Error("distribute should carry no pool/user")
	}
}

// TestGolden_QueueWithdrawal — Vec[i128 shares, u64 expiration]; the
// expiration lands in attributes, shares in amount.
func TestGolden_QueueWithdrawal(t *testing.T) {
	t.Parallel()
	d, err := decodeQueueWithdrawal(goldenEvent(t, "queue_withdrawal"))
	if err != nil {
		t.Fatalf("decodeQueueWithdrawal: %v", err)
	}
	if d.Amount != "730239812" {
		t.Errorf("Amount (shares) = %q, want 730239812", d.Amount)
	}
	exp, ok := d.Attributes["expiration"].(uint64)
	if !ok {
		t.Fatalf("expiration attr missing/wrong type: %v", d.Attributes["expiration"])
	}
	if exp != 1783024205 {
		t.Errorf("expiration = %d, want 1783024205", exp)
	}
}

// TestGolden_RoundTripViaDecodeOne — the full classify→decode→Event
// projection over every golden frame, exercising the consumer.go join.
func TestGolden_RoundTripViaDecodeOne(t *testing.T) {
	t.Parallel()
	for name := range goldenFrames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ev, err := decodeOne(goldenEvent(t, name))
			if err != nil {
				t.Fatalf("decodeOne(%s): %v", name, err)
			}
			if ev.EventType != name {
				t.Errorf("EventType = %q, want %q", ev.EventType, name)
			}
			if ev.ObservedAt.IsZero() {
				t.Error("ObservedAt should be parsed from LedgerClosedAt")
			}
			if ev.Source() != SourceName {
				t.Errorf("Source() = %q, want %q", ev.Source(), SourceName)
			}
		})
	}
}

// ─── synthetic-SCVal helpers (for the negative / edge cases the
//     golden frames don't cover) ────────────────────────────────

func symbolSV(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func i128SV(n *big.Int) xdr.ScVal {
	hi, lo := splitBigInt128(n)
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func u32SV(v uint32) xdr.ScVal {
	x := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &x}
}

func vecSV(vals ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(vals)
	pv := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pv}
}

func contractStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func accountStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func contractAddrSV(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	var cid xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, strk)
	if err != nil {
		t.Fatalf("strkey.Decode: %v", err)
	}
	copy(cid[:], raw)
	a := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func accountAddrSV(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, strk)
	if err != nil {
		t.Fatalf("strkey.Decode: %v", err)
	}
	var ed xdr.Uint256
	copy(ed[:], raw)
	acc := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &ed}
	a := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &acc}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func b64SV(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func splitBigInt128(n *big.Int) (hi int64, lo uint64) {
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		return hiBig.Int64(), loBig.Uint64()
	}
	twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Add(twoTo128, n)
	loBig := new(big.Int).And(u, mask64)
	hiBig := new(big.Int).Rsh(u, 64)
	return int64(hiBig.Uint64()), loBig.Uint64()
}

// ─── Classify ────────────────────────────────────────────────────

func TestClassify_AllTenEventTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		topic    string
		expected string
	}{
		{TopicSymbolDeposit, EventDeposit},
		{TopicSymbolClaim, EventClaim},
		{TopicSymbolDonate, EventDonate},
		{TopicSymbolQueueWithdrawal, EventQueueWithdrawal},
		{TopicSymbolWithdraw, EventWithdraw},
		{TopicSymbolDistribute, EventDistribute},
		{TopicSymbolGulpEmissions, EventGulpEmissions},
		{TopicSymbolDequeueWithdrawal, EventDequeueWithdrawal},
		{TopicSymbolDraw, EventDraw},
		{TopicSymbolRwZoneAdd, EventRwZoneAdd},
	}
	for _, c := range cases {
		c := c
		t.Run(c.expected, func(t *testing.T) {
			t.Parallel()
			e := &events.Event{Topic: []string{c.topic}}
			if got := Classify(e); got != c.expected {
				t.Errorf("Classify(%s) = %q, want %q", c.expected, got, c.expected)
			}
		})
	}
}

func TestClassify_UnknownAndEmpty(t *testing.T) {
	t.Parallel()
	if got := Classify(&events.Event{Topic: []string{b64SV(t, symbolSV("transfer"))}}); got != "" {
		t.Errorf("unknown topic classified as %q", got)
	}
	if got := Classify(&events.Event{Topic: nil}); got != "" {
		t.Errorf("empty topic classified as %q", got)
	}
}

// ─── synthetic decode coverage for the kinds the golden frames don't
//     carry (withdraw / gulp_emissions / dequeue / draw / rw_zone_add)

func TestDecodeWithdraw_Synthetic(t *testing.T) {
	t.Parallel()
	pool := contractStrkey(t, 0x11)
	user := accountStrkey(t, 0x22)
	body := b64SV(t, vecSV(i128SV(big.NewInt(900)), i128SV(big.NewInt(450))))
	e := &events.Event{
		Topic: []string{
			TopicSymbolWithdraw,
			b64SV(t, contractAddrSV(t, pool)),
			b64SV(t, accountAddrSV(t, user)),
		},
		Value: body,
	}
	d, err := decodeWithdraw(e)
	if err != nil {
		t.Fatalf("decodeWithdraw: %v", err)
	}
	if d.Amount != "900" || d.Amount2 != "450" {
		t.Errorf("Amount/Amount2 = %q/%q, want 900/450", d.Amount, d.Amount2)
	}
	if d.Pool != pool || d.UserAddress != user {
		t.Errorf("pool/user mismatch: %q / %q", d.Pool, d.UserAddress)
	}
}

func TestDecodeGulpEmissions_Synthetic(t *testing.T) {
	t.Parallel()
	token := contractStrkey(t, 0x33)
	body := b64SV(t, vecSV(i128SV(big.NewInt(7)), i128SV(big.NewInt(8))))
	e := &events.Event{
		Topic: []string{TopicSymbolGulpEmissions, b64SV(t, contractAddrSV(t, token))},
		Value: body,
	}
	d, err := decodeGulpEmissions(e)
	if err != nil {
		t.Fatalf("decodeGulpEmissions: %v", err)
	}
	if d.Amount != "7" || d.Amount2 != "8" {
		t.Errorf("Amount/Amount2 = %q/%q, want 7/8", d.Amount, d.Amount2)
	}
	if d.Attributes["token"] != token {
		t.Errorf("token attr = %v, want %q", d.Attributes["token"], token)
	}
}

func TestDecodeDraw_Synthetic(t *testing.T) {
	t.Parallel()
	pool := contractStrkey(t, 0x44)
	to := contractStrkey(t, 0x55)
	body := b64SV(t, vecSV(contractAddrSV(t, to), i128SV(big.NewInt(123))))
	e := &events.Event{
		Topic: []string{TopicSymbolDraw, b64SV(t, contractAddrSV(t, pool))},
		Value: body,
	}
	d, err := decodeDraw(e)
	if err != nil {
		t.Fatalf("decodeDraw: %v", err)
	}
	if d.Amount != "123" {
		t.Errorf("Amount = %q, want 123", d.Amount)
	}
	if d.Pool != pool {
		t.Errorf("Pool = %q, want %q", d.Pool, pool)
	}
	if d.Attributes["to"] != to {
		t.Errorf("to attr = %v, want %q", d.Attributes["to"], to)
	}
}

func TestDecodeDequeueWithdrawal_Synthetic(t *testing.T) {
	t.Parallel()
	pool := contractStrkey(t, 0x66)
	user := accountStrkey(t, 0x77)
	e := &events.Event{
		Topic: []string{
			TopicSymbolDequeueWithdrawal,
			b64SV(t, contractAddrSV(t, pool)),
			b64SV(t, accountAddrSV(t, user)),
		},
		Value: b64SV(t, i128SV(big.NewInt(555))),
	}
	d, err := decodeDequeueWithdrawal(e)
	if err != nil {
		t.Fatalf("decodeDequeueWithdrawal: %v", err)
	}
	if d.Amount != "555" || d.Pool != pool || d.UserAddress != user {
		t.Errorf("got amount=%q pool=%q user=%q", d.Amount, d.Pool, d.UserAddress)
	}
}

func TestDecodeRwZoneAdd_Synthetic(t *testing.T) {
	t.Parallel()
	pool := contractStrkey(t, 0x88)
	body := b64SV(t, vecSV(contractAddrSV(t, pool), u32SV(3)))
	e := &events.Event{Topic: []string{TopicSymbolRwZoneAdd}, Value: body}
	d, err := decodeRwZoneAdd(e)
	if err != nil {
		t.Fatalf("decodeRwZoneAdd: %v", err)
	}
	if d.Pool != pool {
		t.Errorf("Pool = %q, want %q", d.Pool, pool)
	}
	if d.Attributes["index"] != uint32(3) {
		t.Errorf("index attr = %v, want 3", d.Attributes["index"])
	}
}

func TestDecodeDonate_Synthetic(t *testing.T) {
	t.Parallel()
	pool := contractStrkey(t, 0x99)
	from := contractStrkey(t, 0xAA)
	e := &events.Event{
		Topic: []string{
			TopicSymbolDonate,
			b64SV(t, contractAddrSV(t, pool)),
			b64SV(t, contractAddrSV(t, from)),
		},
		Value: b64SV(t, i128SV(big.NewInt(42))),
	}
	d, err := decodeDonate(e)
	if err != nil {
		t.Fatalf("decodeDonate: %v", err)
	}
	if d.Amount != "42" || d.Pool != pool {
		t.Errorf("amount/pool = %q/%q", d.Amount, d.Pool)
	}
	if d.Attributes["from"] != from {
		t.Errorf("from attr = %v, want %q", d.Attributes["from"], from)
	}
}

// ─── ADR-0003 large-i128 guard ───────────────────────────────────

func TestDecode_LargeI128_NoTruncation(t *testing.T) {
	t.Parallel()
	big1 := new(big.Int)
	big1.SetString("999999999999999999999999999999", 10) // >> 2^53
	e := &events.Event{
		Topic: []string{TopicSymbolDistribute},
		Value: b64SV(t, i128SV(big1)),
	}
	d, err := decodeDistribute(e)
	if err != nil {
		t.Fatalf("decodeDistribute: %v", err)
	}
	if d.Amount != big1.String() {
		t.Errorf("large i128 lost precision: got %q, want %q", d.Amount, big1.String())
	}
}

// ─── malformed-event guards ──────────────────────────────────────

func TestDecode_ShortTopic(t *testing.T) {
	t.Parallel()
	if _, err := decodeDeposit(&events.Event{Topic: []string{TopicSymbolDeposit}}); !errors.Is(err, ErrMalformedTopic) {
		t.Errorf("deposit short-topic: want ErrMalformedTopic, got %v", err)
	}
	if _, err := decodeClaim(&events.Event{Topic: []string{TopicSymbolClaim}}); !errors.Is(err, ErrMalformedTopic) {
		t.Errorf("claim short-topic: want ErrMalformedTopic, got %v", err)
	}
}

func TestDecode_MalformedBody(t *testing.T) {
	t.Parallel()
	// deposit body must be a 2-Vec; hand it a bare i128.
	pool := contractStrkey(t, 0x11)
	user := accountStrkey(t, 0x22)
	e := &events.Event{
		Topic: []string{
			TopicSymbolDeposit,
			b64SV(t, contractAddrSV(t, pool)),
			b64SV(t, accountAddrSV(t, user)),
		},
		Value: b64SV(t, i128SV(big.NewInt(1))),
	}
	if _, err := decodeDeposit(e); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("deposit malformed-body: want ErrMalformedBody, got %v", err)
	}
}

// ─── topic-symbol encoding stability ─────────────────────────────

func TestTopicSymbol_StableEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		got  string
		want xdr.ScVal
	}{
		{TopicSymbolDeposit, symbolSV(EventDeposit)},
		{TopicSymbolClaim, symbolSV(EventClaim)},
		{TopicSymbolDonate, symbolSV(EventDonate)},
		{TopicSymbolQueueWithdrawal, symbolSV(EventQueueWithdrawal)},
		{TopicSymbolWithdraw, symbolSV(EventWithdraw)},
		{TopicSymbolDistribute, symbolSV(EventDistribute)},
		{TopicSymbolGulpEmissions, symbolSV(EventGulpEmissions)},
		{TopicSymbolDequeueWithdrawal, symbolSV(EventDequeueWithdrawal)},
		{TopicSymbolDraw, symbolSV(EventDraw)},
		{TopicSymbolRwZoneAdd, symbolSV(EventRwZoneAdd)},
	}
	for _, c := range cases {
		c := c
		if c.got != b64SV(t, c.want) {
			t.Errorf("symbol drift: pkg = %q, re-encoded = %q", c.got, b64SV(t, c.want))
		}
	}
}
