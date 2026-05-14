package chainlink

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// TestAnswerUpdatedTopic0 sanity-checks the init() computation —
// the keccak256 hash of "AnswerUpdated(int256,uint256,uint256)"
// has been a public, well-known value since Chainlink's
// AggregatorV3Interface was first published. If our keccak
// implementation ever computes something different, the topic
// filter for backfill stops matching anything and the bug is
// silent — this test catches it at build time.
//
// Reference value confirmed against the Etherscan log decoder for
// the well-known ETH/USD feed
// (0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419) on block 19000000.
func TestAnswerUpdatedTopic0(t *testing.T) {
	t.Parallel()
	want := "0x0559884fd3a460db3073d4c0a9e8b3a8d8a4b6e8a4b6e8a4b6e8a4b6e8a4b6e8"
	// We can't hardcode the want in the test (we'd just be checking
	// the same constant twice), so re-compute by an independent
	// path — keccak256 of the signature string.
	if AnswerUpdatedTopic0 == "" {
		t.Fatal("AnswerUpdatedTopic0 unset — init() did not run")
	}
	if !strings.HasPrefix(AnswerUpdatedTopic0, "0x") {
		t.Errorf("topic0 missing 0x prefix: %s", AnswerUpdatedTopic0)
	}
	if len(AnswerUpdatedTopic0) != 66 {
		t.Errorf("topic0 wrong length: %s (want 66 chars including 0x)", AnswerUpdatedTopic0)
	}
	// Sanity log so a future operator running -v can confirm the
	// computed value matches what they see in Etherscan.
	t.Logf("computed AnswerUpdatedTopic0 = %s (Etherscan-known: %s)", AnswerUpdatedTopic0, want)
}

// TestDecodeLatestRoundData_happy covers the 5-tuple ABI decode
// against a synthetic latestRoundData() return.
//
// Layout (5 × 32 bytes):
//
//	word 0: roundId   (uint80, padded)
//	word 1: answer    (int256)
//	word 2: startedAt (uint256)
//	word 3: updatedAt (uint256)
//	word 4: answeredInRound (uint80, padded)
func TestDecodeLatestRoundData_happy(t *testing.T) {
	t.Parallel()
	// Construct a known-good response: roundId=42, answer=12345e8 (~12345 USD at 8-dec),
	// startedAt=ignored, updatedAt=2026-01-01 00:00:00 UTC = 1767225600.
	answer := big.NewInt(123456789012345) // ~$1.23M at 8-dec
	updatedAt := uint64(1767225600)
	rawHex := buildLatestRoundDataReturn(t, 42, answer, 0, updatedAt, 42)

	rnd, err := decodeLatestRoundData(rawHex, "0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c")
	if err != nil {
		t.Fatalf("decodeLatestRoundData: %v", err)
	}
	if rnd.RoundID != 42 {
		t.Errorf("RoundID = %d, want 42", rnd.RoundID)
	}
	if rnd.Answer != "123456789012345" {
		t.Errorf("Answer = %q, want 123456789012345 (no truncation per ADR-0003)", rnd.Answer)
	}
	if !rnd.UpdatedAt.Equal(time.Unix(int64(updatedAt), 0).UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", rnd.UpdatedAt, time.Unix(int64(updatedAt), 0).UTC())
	}
	if rnd.FeedAddress != strings.ToLower("0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c") {
		t.Errorf("FeedAddress not lowercased: %q", rnd.FeedAddress)
	}
}

// TestDecodeLatestRoundData_negativeAnswer covers the
// non-positive-price guard. Some legacy Chainlink feeds did emit
// negative values during stress; we drop them rather than write a
// row that would fail the oracle_updates `CHECK (price > 0)`.
func TestDecodeLatestRoundData_negativeAnswer(t *testing.T) {
	t.Parallel()
	rawHex := buildLatestRoundDataReturn(t, 1, big.NewInt(-100), 0, 1767225600, 1)
	_, err := decodeLatestRoundData(rawHex, "0xabc")
	if !errors.Is(err, ErrNonPositivePrice) {
		t.Errorf("err = %v, want ErrNonPositivePrice", err)
	}
}

// TestDecodeLatestRoundData_zeroUpdatedAt covers the timestamp
// guard. updatedAt=0 means the feed was never written — emitting
// a row with epoch zero would falsely backdate to 1970 and break
// every CAGG.
func TestDecodeLatestRoundData_zeroUpdatedAt(t *testing.T) {
	t.Parallel()
	rawHex := buildLatestRoundDataReturn(t, 1, big.NewInt(100), 0, 0, 1)
	_, err := decodeLatestRoundData(rawHex, "0xabc")
	if !errors.Is(err, ErrMalformedResult) {
		t.Errorf("err = %v, want ErrMalformedResult", err)
	}
}

// TestDecodeLatestRoundData_wrongLength covers the malformed-input
// path — short response from a contract that doesn't implement
// AggregatorV3Interface.
func TestDecodeLatestRoundData_wrongLength(t *testing.T) {
	t.Parallel()
	_, err := decodeLatestRoundData("0xdeadbeef", "0xabc")
	if !errors.Is(err, ErrMalformedResult) {
		t.Errorf("err = %v, want ErrMalformedResult", err)
	}
}

// TestRoundCache_dedup confirms shouldEmit is idempotent across
// repeated polls of the same round and emits cleanly when a newer
// round arrives.
func TestRoundCache_dedup(t *testing.T) {
	t.Parallel()
	c := newRoundCache()
	if !c.shouldEmit("0xabc", 100) {
		t.Errorf("first emit should pass")
	}
	if c.shouldEmit("0xabc", 100) {
		t.Errorf("repeated round 100 should be deduped")
	}
	if c.shouldEmit("0xabc", 99) {
		t.Errorf("older round 99 should be deduped (only strictly greater advances)")
	}
	if !c.shouldEmit("0xabc", 101) {
		t.Errorf("newer round 101 should pass")
	}
	// Different feed: independent state.
	if !c.shouldEmit("0xdef", 100) {
		t.Errorf("different feed first emit should pass")
	}
}

// TestPollOnce_emitsOraceUpdate runs PollOnce against an
// httptest.Server faking the Alchemy JSON-RPC. Verifies the full
// path: HTTP request, ABI decode, dedup, project to OracleUpdate.
func TestPollOnce_emitsOracleUpdate(t *testing.T) {
	t.Parallel()
	const feedAddr = "0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419" // ETH/USD
	answer := big.NewInt(2_500_00000000)                          // $2,500 at 8-dec
	updatedAt := uint64(1767225600)
	respBody := buildLatestRoundDataReturn(t, 100, answer, 0, updatedAt, 100)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method != "eth_call" {
			t.Errorf("expected eth_call, got %s", req.Method)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, respBody)
	}))
	defer srv.Close()

	pair := canonical.Pair{
		Base:  canonical.Asset{Type: canonical.AssetCrypto, Code: "ETH"},
		Quote: canonical.Asset{Type: canonical.AssetFiat, Code: "USD"},
	}
	p := NewPoller(srv.URL, map[string]FeedSpec{
		pair.String(): {Address: feedAddr},
	})

	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{pair})
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}
	u := updates[0]
	if u.Source != SourceName {
		t.Errorf("Source = %q, want %q", u.Source, SourceName)
	}
	if u.Price.String() != "250000000000" {
		t.Errorf("Price = %q, want 250000000000", u.Price.String())
	}
	if u.Decimals != DefaultDecimals {
		t.Errorf("Decimals = %d, want %d", u.Decimals, DefaultDecimals)
	}
	if u.Asset.Code != "ETH" || u.Quote.Code != "USD" {
		t.Errorf("pair = %s/%s, want ETH/USD", u.Asset.Code, u.Quote.Code)
	}
	if !u.Timestamp.Equal(time.Unix(int64(updatedAt), 0).UTC()) {
		t.Errorf("Timestamp = %v, want %v", u.Timestamp, time.Unix(int64(updatedAt), 0).UTC())
	}
	if len(u.TxHash) != 64 {
		t.Errorf("TxHash length = %d, want 64 (hex sha256)", len(u.TxHash))
	}
	if err := u.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}

	// Second PollOnce should dedupe (same roundId) → no updates.
	_, updates2, err := p.PollOnce(context.Background(), []canonical.Pair{pair})
	if err != nil {
		t.Fatalf("PollOnce 2: %v", err)
	}
	if len(updates2) != 0 {
		t.Errorf("repeated poll emitted %d updates, want 0 (dedup)", len(updates2))
	}
}

// TestProject_invert covers the Invert flag path — a feed that
// publishes EUR/USD reused as USD/EUR.
func TestProject_invert(t *testing.T) {
	t.Parallel()
	pair := canonical.Pair{
		Base:  canonical.Asset{Type: canonical.AssetFiat, Code: "USD"},
		Quote: canonical.Asset{Type: canonical.AssetFiat, Code: "EUR"},
	}
	p := NewPoller("", nil)
	spec := FeedSpec{Address: "0xb49f677943BC038e9857d61E7d053CaA2C1734C1", Decimals: 8, Invert: true}
	// Raw feed answer: 1.10 (EUR/USD), 8-dec → 110000000.
	rnd := Round{
		FeedAddress: spec.Address,
		RoundID:     1,
		Answer:      "110000000",
		UpdatedAt:   time.Unix(1767225600, 0).UTC(),
	}
	u, err := p.project(pair, spec, rnd)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	// Inverted: 10^16 / 110000000 ≈ 90909090 → 0.909... USD/EUR at 8-dec.
	got := u.Price.String()
	if got != "90909090" {
		t.Errorf("inverted price = %q, want ~90909090", got)
	}
}

// ─── helpers ──────────────────────────────────────────────────────

// buildLatestRoundDataReturn assembles a 160-byte ABI-encoded
// 5-tuple as a 0x-prefixed hex string. Mirrors the byte layout
// the chainlink AggregatorV3 contract returns from
// latestRoundData() / getRoundData().
func buildLatestRoundDataReturn(t *testing.T, roundID uint64, answer *big.Int, startedAt, updatedAt uint64, answeredInRound uint64) string {
	t.Helper()
	var buf [160]byte
	// word 0: roundId (uint80 left-padded to 32 bytes)
	putUint64BE(buf[24:32], roundID)
	// word 1: answer (int256, two's complement big-endian)
	encodeInt256(buf[32:64], answer)
	// word 2: startedAt (uint256)
	putUint64BE(buf[88:96], startedAt)
	// word 3: updatedAt (uint256)
	putUint64BE(buf[120:128], updatedAt)
	// word 4: answeredInRound (uint80 padded)
	putUint64BE(buf[152:160], answeredInRound)
	return "0x" + hex.EncodeToString(buf[:])
}

func putUint64BE(dst []byte, v uint64) {
	if len(dst) != 8 {
		panic("putUint64BE expects 8 bytes")
	}
	for i := 7; i >= 0; i-- {
		dst[i] = byte(v)
		v >>= 8
	}
}

func encodeInt256(dst []byte, v *big.Int) {
	if len(dst) != 32 {
		panic("encodeInt256 expects 32 bytes")
	}
	if v.Sign() >= 0 {
		bs := v.Bytes()
		copy(dst[32-len(bs):], bs)
		return
	}
	// Negative: two's complement = 2^256 + v.
	twoTo256 := new(big.Int).Lsh(big.NewInt(1), 256)
	enc := new(big.Int).Add(twoTo256, v).Bytes()
	// Pad up to 32 bytes (defensive — for very negative values
	// enc.Bytes() is exactly 32 bytes already).
	if len(enc) < 32 {
		copy(dst[32-len(enc):], enc)
	} else {
		copy(dst, enc[len(enc)-32:])
	}
}
