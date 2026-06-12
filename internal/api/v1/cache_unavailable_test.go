package v1_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// miscOnfErr models the wire-format MISCONF reply Redis returns when
// stop-writes-on-bgsave-error is active (the May-10 SEV-2 shape):
//
//	"MISCONF Redis is configured to save RDB snapshots, but it's
//	 currently unable to persist to disk."
//
// go-redis surfaces this as a proto.RedisError (the unexported
// concrete type implementing the redis.Error interface) — but its
// matcher, redis.HasErrorPrefix, ALSO trips on a plain
// fmt.Errorf("MISCONF ...") wrap because it only requires
// errors.As(err, &redis.Error). The fallback string-prefix branch in
// IsCacheUnavailable catches the plain-string case so test stubs
// (which can't import proto/) still drive the right branch.
var miscOnfErr = errors.New("MISCONF Redis is configured to save RDB snapshots, but it's currently unable to persist to disk on disk.")

// IsCacheUnavailable sanity check — the helper is unit-tested via the
// handler integration tests below, but a direct test pins the contract
// in isolation so a future change to the predicate fails fast.
func TestIsCacheUnavailable_Predicate(t *testing.T) {
	t.Run("nil is not cache unavailable", func(t *testing.T) {
		if v1.IsCacheUnavailable(nil) {
			t.Fatalf("nil err must not match")
		}
	})
	t.Run("price-not-found is not cache unavailable", func(t *testing.T) {
		if v1.IsCacheUnavailable(v1.ErrPriceNotFound) {
			t.Fatalf("ErrPriceNotFound is an application-layer sentinel, not a cache failure")
		}
	})
	t.Run("plain MISCONF string matches", func(t *testing.T) {
		if !v1.IsCacheUnavailable(miscOnfErr) {
			t.Fatalf("MISCONF reply must surface as cache-unavailable")
		}
	})
	t.Run("wrapped MISCONF matches", func(t *testing.T) {
		wrapped := fmt.Errorf("redis set vwap:foo: %w", miscOnfErr)
		if !v1.IsCacheUnavailable(wrapped) {
			t.Fatalf("wrapped MISCONF must still match (audit-2026-05-27)")
		}
	})
	t.Run("unrelated error does not match", func(t *testing.T) {
		if v1.IsCacheUnavailable(errors.New("hypertable timeout")) {
			t.Fatalf("generic storage error must not be misclassified as cache-unavailable")
		}
	})
	t.Run("context.DeadlineExceeded does not match", func(t *testing.T) {
		// Handler-side deadlines surface via the per-handler timeout
		// 503 path, not the cache-unavailable branch.
		if v1.IsCacheUnavailable(context.DeadlineExceeded) {
			t.Fatalf("ctx deadline must stay on the timeout path, not cache-unavailable")
		}
	})
}

// assertCacheUnavailable asserts the response is the canonical
// cache-unavailable 503 shape: status 503, Retry-After: 30, problem
// type URL "errors/cache-unavailable".
func assertCacheUnavailable(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "errors/cache-unavailable") {
		t.Errorf("body missing cache-unavailable type URL: %s", body)
	}
}

// ─── /v1/oracle/latest ────────────────────────────────────────────

// TestOracleLatest_CacheUnavailable503 — Redis MISCONF surfaces as
// 503 + Retry-After (F-0086).
func TestOracleLatest_CacheUnavailable503(t *testing.T) {
	reader := &stubOracleReader{err: miscOnfErr}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=native")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/oracle/streams ───────────────────────────────────────────

// TestOracleStreams_CacheUnavailable503 — same MISCONF cascade as
// /v1/oracle/latest, on the streams variant (F-0086 / F-0145).
func TestOracleStreams_CacheUnavailable503(t *testing.T) {
	reader := &stubOracleReader{err: miscOnfErr}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/streams")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/lending/pools ────────────────────────────────────────────

// TestLendingPools_CacheUnavailable503 — F-0087. The handler had a
// 503 timeout path and a 500 fallthrough; MISCONF now lands on the
// cache-unavailable 503 instead of the generic 500.
func TestLendingPools_CacheUnavailable503(t *testing.T) {
	reader := &stubLendingReader{err: miscOnfErr}
	srv := v1.New(v1.Options{Lending: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/lending/pools")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/vwap ─────────────────────────────────────────────────────

// TestVWAP_CacheUnavailable503 — F-0089. The TradesInRange call
// returning MISCONF lands on the cache-unavailable 503 branch.
func TestVWAP_CacheUnavailable503(t *testing.T) {
	reader := &stubHistoryReader{err: miscOnfErr}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/observations ─────────────────────────────────────────────

// TestObservations_CacheUnavailable503 — F-0090. The fiat:USD short-
// circuit skips storage, so this test uses a CONCRETE classic quote
// (USDC-G…) to force the LatestTradePerSource path that actually
// hits the cache layer.
func TestObservations_CacheUnavailable503(t *testing.T) {
	hist := &stubHistoryReader{err: miscOnfErr}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations?asset=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/observations/stream ──────────────────────────────────────

// TestObservationsStream_CacheUnavailable503 — F-0146. The pre-flight
// computeObservations call (run synchronously before switching to SSE
// mode, so the handler can still set a non-200 status) lands on the
// cache-unavailable 503 branch on MISCONF.
func TestObservationsStream_CacheUnavailable503(t *testing.T) {
	hist := &stubHistoryReader{err: miscOnfErr}
	srv := v1.New(v1.Options{History: hist})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/observations/stream?asset=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	assertCacheUnavailable(t, resp)
}

// ─── /v1/price/tip ────────────────────────────────────────────────

// tipCacheUnavailablePriceReader is a PriceReader stub that returns
// MISCONF on every LatestPrice call. The tip handler's computeTip
// path goes Reader → fallback chain; the fallback chain's first hop
// is also a Redis lookup, so returning MISCONF from the reader is
// enough to drive the unavailable branch.
type tipCacheUnavailablePriceReader struct{}

func (tipCacheUnavailablePriceReader) LatestPrice(_ context.Context, _, _ canonical.Asset) (v1.PriceSnapshot, []string, bool, error) {
	return v1.PriceSnapshot{}, nil, false, miscOnfErr
}

func (tipCacheUnavailablePriceReader) RecentClosedSnapshots(_ context.Context, _, _ canonical.Asset, _ int) ([]v1.PriceSnapshot, error) {
	return nil, miscOnfErr
}

// TestPriceTip_CacheUnavailable503 — F-0145. handlePriceTip's
// computeTip helper now distinguishes a MISCONF surfacing from
// PriceReader.LatestPrice from a generic internal error.
func TestPriceTip_CacheUnavailable503(t *testing.T) {
	srv := v1.New(v1.Options{Prices: tipCacheUnavailablePriceReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/price/tip?asset=native&quote=fiat:USD")
	assertCacheUnavailable(t, resp)
}

// TestPriceTipStream_CacheUnavailable503 — F-0146 (stream variant).
// The pre-stream synchronous computeTip call lands on cache-
// unavailable 503 instead of generic 500.
func TestPriceTipStream_CacheUnavailable503(t *testing.T) {
	srv := v1.New(v1.Options{Prices: tipCacheUnavailablePriceReader{}})
	tsv := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, tsv.URL+"/v1/price/tip/stream?asset=native&quote=fiat:USD")
	assertCacheUnavailable(t, resp)
}
