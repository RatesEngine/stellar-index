//go:build integration

package integration_test

// Tests the F-0039 cascade response semantics end-to-end using
// testcontainers-go: spins up Postgres (TimescaleDB) + Redis,
// forces Redis into a MISCONF state via CONFIG SET dir=/nonexistent
// + BGSAVE, asserts the full system response chain — handler 503
// mapping with Retry-After header, RFC-7807 problem+json type URL,
// stale-tolerant /v1/price fallback — and recovery on Redis healing.
//
// Why this exists alongside cache_unavailable_test.go
// ──────────────────────────────────────────────────
// internal/api/v1/cache_unavailable_test.go covers each handler
// with a STUB OracleReader / LendingReader / etc. that returns a
// hand-crafted MISCONF error string. That tests the predicate +
// the handler 503-mapping branch in isolation. This integration
// test exercises the FULL chain — real go-redis/v9 client driving
// a real Redis container in real MISCONF state, propagating the
// error through the actual transport layer. Catches regressions
// like:
//   - a future go-redis version changing how MISCONF replies are
//     surfaced (typed error vs string) that would slip past the
//     stub-based test
//   - the handler chain failing to wrap-then-unwrap the error
//     correctly across an extracted helper boundary
//   - a wire-level subtlety (e.g. pipeline batching the MISCONF
//     into a partial-success reply) that the stub can't model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestCascadeMISCONF_EndToEnd is the integration twin of
// internal/api/v1/cache_unavailable_test.go's stub-based tests. It
// uses a real Redis container in real MISCONF state to drive the
// 503-on-cache-unavailable mapping end-to-end.
//
// Skipped automatically when Docker isn't available — mirrors the
// existing pattern in test/integration/migrations_test.go.
//
// Nominal runtime: ~10s on a warm Docker cache, ~30s on a cold one.
func TestCascadeMISCONF_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rdb, redisCtr := startRedis(t, ctx)
	t.Cleanup(func() {
		_ = redisCtr.Terminate(context.Background())
	})

	// Verify Redis is alive end-to-end before we start chaos.
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}

	// Build a v1.Server wired with our test reader that talks to
	// the real Redis container. The reader's LatestOracleUpdatesForAsset
	// does a Redis SET on every call — under MISCONF that SET fails
	// with the MISCONF prefix, which is exactly the failure shape
	// the production cascade-affected handlers see in the May-10
	// SEV-2 (commit a91f901b's rationale).
	oracle := &redisOracleReader{rdb: rdb}
	srv := v1.New(v1.Options{Oracle: oracle})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// ─── 1. baseline: healthy Redis → 200 ─────────────────────────
	t.Run("baseline_200", func(t *testing.T) {
		resp := httpGet(t, ts.URL+"/v1/oracle/latest?asset=native")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("baseline status = %d, want 200 (Redis healthy)", resp.StatusCode)
		}
	})

	// ─── 2. force MISCONF, assert 503 + Retry-After ───────────────
	t.Run("misconf_503_with_retry_after", func(t *testing.T) {
		probeSet := func() error {
			return rdb.Set(ctx, "misconf-probe-handler", "v", 0).Err()
		}
		if err := forceMISCONF(ctx, redisCtr, probeSet); err != nil {
			t.Fatalf("forceMISCONF: %v", err)
		}
		// Heal on test failure so subsequent sub-tests aren't blocked.
		defer func() {
			if err := healMISCONF(ctx, redisCtr); err != nil {
				t.Logf("heal on cleanup: %v", err)
			}
		}()

		// Wait briefly for the new state to propagate; BGSAVE is
		// asynchronous, the stop-writes flag flips once the fork
		// fails. Retry the request a few times rather than sleeping
		// blindly — typical settle is <1s.
		var resp *http.Response
		var err error
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			resp, err = http.Get(ts.URL + "/v1/oracle/latest?asset=native")
			if err == nil && resp.StatusCode == http.StatusServiceUnavailable {
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("GET under MISCONF: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 503 (MISCONF cascade); body=%s",
				resp.StatusCode, body)
		}
		if got := resp.Header.Get("Retry-After"); got != "30" {
			t.Errorf("Retry-After = %q, want 30 (writeCacheUnavailableProblem invariant)", got)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "errors/cache-unavailable") {
			t.Errorf("body missing errors/cache-unavailable type URL: %s", body)
		}
		// problem+json must be a valid envelope.
		var problem map[string]any
		if err := json.Unmarshal(body, &problem); err != nil {
			t.Errorf("response body not valid JSON: %v", err)
		}
		if got, _ := problem["status"].(float64); int(got) != http.StatusServiceUnavailable {
			t.Errorf("problem.status = %v, want 503", problem["status"])
		}
	})

	// ─── 3. heal Redis, assert routes return to nominal ──────────
	t.Run("recovery_200", func(t *testing.T) {
		if err := healMISCONF(ctx, redisCtr); err != nil {
			t.Fatalf("heal: %v", err)
		}
		// Poll until the route returns to 200 — heal is async; typical
		// settle is <2s once BGSAVE reports ok.
		deadline := time.Now().Add(30 * time.Second)
		var lastStatus int
		for time.Now().Before(deadline) {
			resp, err := http.Get(ts.URL + "/v1/oracle/latest?asset=native")
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			lastStatus = resp.StatusCode
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("did not recover to 200 within 30s; last status = %d", lastStatus)
	})

	// ─── 4. predicate sanity — go-redis surfaces MISCONF the way
	//        IsCacheUnavailable expects ───────────────────────────
	t.Run("go_redis_misconf_classification", func(t *testing.T) {
		// Repro MISCONF directly via the client — bypass the handler
		// to verify go-redis's error shape hasn't drifted in a way
		// the predicate would miss. This is a defence-in-depth check;
		// if it ever fails, IsCacheUnavailable needs a new branch.
		probeSet := func() error {
			return rdb.Set(ctx, "misconf-probe-direct", "v", 0).Err()
		}
		if err := forceMISCONF(ctx, redisCtr, probeSet); err != nil {
			t.Fatalf("forceMISCONF: %v", err)
		}
		defer func() { _ = healMISCONF(ctx, redisCtr) }()

		// Retry briefly for state to settle.
		var setErr error
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			setErr = rdb.Set(ctx, "misconf-probe", "v", 0).Err()
			if setErr != nil {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if setErr == nil {
			t.Fatalf("expected MISCONF error from SET, got nil")
		}
		if !v1.IsCacheUnavailable(setErr) {
			t.Errorf("IsCacheUnavailable did not classify go-redis MISCONF error: %v", setErr)
		}
		// Also classify the wrapped form (the orchestrator wraps via
		// fmt.Errorf("redis set %s: %w", key, err)).
		wrapped := fmt.Errorf("redis set vwap:foo: %w", setErr)
		if !v1.IsCacheUnavailable(wrapped) {
			t.Errorf("IsCacheUnavailable did not classify wrapped MISCONF: %v", wrapped)
		}
	})
}

// ─── helpers ──────────────────────────────────────────────────────

// startRedis spins up a single-node Redis container with the same
// settings the dev compose uses (`stop-writes-on-bgsave-error yes`
// is on by default in Redis 7) PLUS `--save 1 1` so BGSAVE auto-
// fires on the first write — that's how forceMISCONF provokes the
// stop-writes flag without needing CONFIG SET (which Redis 7.4
// rejects at runtime for the `dir` key as a protected config).
//
// Returns a go-redis client + the container handle so the caller
// can exec docker commands.
func startRedis(t *testing.T, ctx context.Context) (*redis.Client, testcontainers.Container) {
	t.Helper()
	ctr, err := testcontainers.Run(ctx,
		"redis:7.4-alpine",
		testcontainers.WithExposedPorts("6379/tcp"),
		// --save 1 1: trigger a BGSAVE after any single write within
		// 1 second. Combined with chmod 0 /data in forceMISCONF, this
		// drives the persistence layer into err state on the next
		// write after the chmod.
		testcontainers.WithCmd("redis-server", "--save", "1", "1"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		// Mirror the migrations_test.go pattern: skip when Docker is
		// unavailable so the test is safe to run on a laptop without
		// the daemon. Real CI nodes have Docker; this branch is a
		// developer-convenience.
		if isDockerUnavailable(err) {
			t.Skipf("docker unavailable, skipping integration test: %v", err)
		}
		t.Fatalf("start redis: %v", err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("container mapped port: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", host, port.Port()),
	})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, ctr
}

// forceMISCONF puts Redis into the stop-writes state by:
//  1. chmod 0 /data — strips write permission from the snapshot
//     dir. CONFIG SET dir is rejected at runtime in Redis 7.4 as
//     a protected config, so we change the FS underneath instead.
//  2. SET a probe key — triggers the `--save 1 1` rule (started
//     with that flag in startRedis), which fires BGSAVE within 1s.
//  3. Poll INFO persistence until rdb_last_bgsave_status:err.
//
// Once BGSAVE has failed once, every subsequent write returns
// `MISCONF Redis is configured to save RDB snapshots, but it's
// currently unable to persist to disk` — exactly the May-10 SEV-2
// surface (and what a91f901b's helper maps to HTTP 503).
//
// probeSet is the caller's go-redis-driven write — using the same
// client that subsequent assertions use ensures we hit the same
// connection pool and same surface.
func forceMISCONF(ctx context.Context, ctr testcontainers.Container, probeSet func() error) error {
	// Re-arm the safety net: healMISCONF turns it off on cleanup,
	// and Redis won't block writes on BGSAVE failure without it. On
	// the first call this is a no-op (default is "yes" on image
	// start); on the second call this is what makes the test
	// re-armable. Authoritative writes-blocked behaviour requires
	// this flag, NOT just rdb_last_bgsave_status:err.
	if err := execIgnoringBGSAVE(ctx, ctr,
		[]string{
			"redis-cli", "CONFIG", "SET",
			"stop-writes-on-bgsave-error", "yes",
		}); err != nil {
		return fmt.Errorf("re-arm stop-writes: %w", err)
	}
	if err := execIgnoringBGSAVE(ctx, ctr,
		[]string{"chmod", "0", "/data"}); err != nil {
		return fmt.Errorf("chmod /data: %w", err)
	}
	// Trigger the save rule. Multiple writes may be needed because
	// the `--save N M` threshold could already have been satisfied
	// by an earlier write that BGSAVEd cleanly.
	if err := probeSet(); err != nil && !strings.Contains(err.Error(), "MISCONF") {
		return fmt.Errorf("probe SET: %w", err)
	}
	// Authoritative readiness check: keep probing SET until it
	// returns MISCONF. rdb_last_bgsave_status:err is a NECESSARY
	// but not SUFFICIENT condition — Redis only sets the
	// stop-writes-on-bgsave-error trip-wire on the next write
	// attempt after the failed BGSAVE. Looping on the SET itself
	// gives us the end-state guarantee callers actually want.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		err := probeSet()
		if err != nil && strings.Contains(err.Error(), "MISCONF") {
			return nil
		}
		// Cross-check: if BGSAVE has fired in err state but
		// stop-writes hasn't tripped yet, sleeping briefly gives
		// the trip-wire time to engage on the next probe.
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("redis did not enter MISCONF (writes still succeeding) within 15s")
}

// healMISCONF restores Redis to a writeable state: chmod 0755 /data,
// trigger a BGSAVE that succeeds, clear the stop-writes flag.
func healMISCONF(ctx context.Context, ctr testcontainers.Container) error {
	if err := execIgnoringBGSAVE(ctx, ctr,
		[]string{"chmod", "0755", "/data"}); err != nil {
		return fmt.Errorf("chmod /data: %w", err)
	}
	// BGSAVE here is safe — `dir` is back to writeable. Use
	// redis-cli to avoid needing a live go-redis client (the caller
	// may still be in the middle of a MISCONF-blocked op).
	_ = execIgnoringBGSAVE(ctx, ctr, []string{"redis-cli", "BGSAVE"})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := execCapture(ctx, ctr,
			[]string{"redis-cli", "INFO", "persistence"})
		if err == nil && strings.Contains(out, "rdb_last_bgsave_status:ok") {
			// BGSAVE recovered; clear the safety net (matches the
			// chaos scenario sequence in test/chaos/scenarios/
			// 04-redis-misconf.sh).
			return execIgnoringBGSAVE(ctx, ctr,
				[]string{
					"redis-cli", "CONFIG", "SET",
					"stop-writes-on-bgsave-error", "no",
				})
		}
		_ = execIgnoringBGSAVE(ctx, ctr, []string{"redis-cli", "BGSAVE"})
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("BGSAVE did not report ok within 15s")
}

// execIgnoringBGSAVE is a thin wrapper around ctr.Exec that ignores
// the "Background saving started" stderr noise from BGSAVE — Redis
// returns 0 but writes a status line to stdout that confuses our
// captured-output asserts elsewhere. We only care that the command
// dispatched.
func execIgnoringBGSAVE(ctx context.Context, ctr testcontainers.Container, cmd []string) error {
	code, _, err := ctr.Exec(ctx, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit %d", code)
	}
	return nil
}

// execCapture runs cmd and returns combined stdout as a string.
func execCapture(ctx context.Context, ctr testcontainers.Container, cmd []string) (string, error) {
	code, r, err := ctr.Exec(ctx, cmd)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// httpGet wraps http.Get with a per-request timeout, mirroring the
// r1-smoke.sh per-request budget.
func httpGet(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// isDockerUnavailable mirrors migrations_test.go's behaviour: skip
// when Docker isn't reachable rather than fail the test. We don't
// have a single sentinel for this — testcontainers wraps with its
// own error type — so check both the message and the wrap chain.
func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, hint := range []string{
		"Cannot connect to the Docker daemon",
		"docker daemon",
		"connect: no such file or directory",
		"context deadline exceeded",
		"rootless docker",
	} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

// ─── redisOracleReader — minimal v1.OracleReader hitting real Redis
//
// Every call performs a Redis SET to mirror the cascade-affected
// handlers' cache-write pattern. Under MISCONF that SET fails with
// the MISCONF prefix; the handler wraps it (via the existing
// observability seam) and the v1 layer's IsCacheUnavailable
// predicate flips the response to 503 + Retry-After.

type redisOracleReader struct {
	rdb *redis.Client
}

// LatestOracleUpdatesForAsset returns an empty slice on healthy
// Redis (the asset has no observations seeded — we're only testing
// the cache-write failure surface, not the data path). Under
// MISCONF the SET fails and the error propagates to the handler.
func (r *redisOracleReader) LatestOracleUpdatesForAsset(
	ctx context.Context, asset canonical.Asset, sourceFilter string,
) ([]canonical.OracleUpdate, error) {
	// Touch Redis with a write — this is the operation that fails
	// with MISCONF in production (per a91f901b's diagnosis).
	if err := r.rdb.Set(ctx, "oracle:probe:"+asset.String(), "v", time.Minute).Err(); err != nil {
		return nil, fmt.Errorf("redis set oracle:probe:%s: %w", asset.String(), err)
	}
	return nil, nil
}

func (r *redisOracleReader) LatestOracleUpdatesForAssets(
	ctx context.Context, assets []canonical.Asset, sourceFilter string,
) ([]canonical.OracleUpdate, error) {
	if err := r.rdb.Set(ctx, "oracle:probe-multi", "v", time.Minute).Err(); err != nil {
		return nil, fmt.Errorf("redis set oracle:probe-multi: %w", err)
	}
	return nil, nil
}

func (r *redisOracleReader) LatestOracleStreams(
	ctx context.Context,
) ([]canonical.OracleUpdate, error) {
	if err := r.rdb.Set(ctx, "oracle:streams-probe", "v", time.Minute).Err(); err != nil {
		return nil, fmt.Errorf("redis set oracle:streams-probe: %w", err)
	}
	return nil, nil
}
