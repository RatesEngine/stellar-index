package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/usage"
)

// TestUsageReaderOrNil_RedisAbsent — when the usage counter is
// nil (Redis-less deployment), the helper must return a
// typed-nil v1.UsageReader rather than wrapping the nil counter
// in a non-nil adapter. The /v1/account/usage handler treats
// `usageReader == nil` as "no backend wired" and short-circuits
// to `[]`; the buggy pre-fix shape was a non-nil adapter that
// nil-deref'd on `Read`. F-1258 (codex audit-2026-05-12).
func TestUsageReaderOrNil_RedisAbsent(t *testing.T) {
	if r := usageReaderOrNil(nil); r != nil {
		t.Errorf("usageReaderOrNil(nil) = %v (non-nil), want nil — handler short-circuits on nil; non-nil wrapper would deref the inner nil counter on Read", r)
	}
}

// TestUsageReaderOrNil_RedisPresent — when the counter is
// non-nil, the helper returns a non-nil adapter that bridges
// to the v1 package. Uses miniredis so the underlying
// usage.New() doesn't itself short-circuit to nil.
func TestUsageReaderOrNil_RedisPresent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c := usage.New(rdb)
	if c == nil {
		t.Fatal("usage.New(real rdb) returned nil — test setup invariant broken")
	}
	r := usageReaderOrNil(c)
	if r == nil {
		t.Fatal("usageReaderOrNil(real-counter) = nil, want non-nil")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildDivergenceReferences_DefaultsCoinGeckoOnly(t *testing.T) {
	cfg := config.DivergenceConfig{
		CoinGecko: config.DivergenceCoinGeckoConfig{Enabled: true},
		Chainlink: config.DivergenceChainlinkConfig{Enabled: false},
	}
	refs := buildDivergenceReferences(cfg, discardLogger())
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1 (CoinGecko only)", len(refs))
	}
	if got := refs[0].Name(); got != "coingecko" {
		t.Errorf("refs[0].Name() = %q, want %q", got, "coingecko")
	}
}

func TestBuildDivergenceReferences_BothWiredWhenChainlinkConfigured(t *testing.T) {
	cfg := config.DivergenceConfig{
		CoinGecko: config.DivergenceCoinGeckoConfig{Enabled: true},
		Chainlink: config.DivergenceChainlinkConfig{
			Enabled: true,
			FeedMap: map[string]config.ChainlinkFeedConfig{
				"fiat:EUR/fiat:USD": {
					Address:  "0xb49f677943BC038e9857d61E7d053CaA2C1734C1",
					Decimals: 8,
				},
			},
		},
	}
	refs := buildDivergenceReferences(cfg, discardLogger())
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2", len(refs))
	}
	names := []string{refs[0].Name(), refs[1].Name()}
	wantSet := map[string]bool{"coingecko": true, "chainlink": true}
	for _, n := range names {
		if !wantSet[n] {
			t.Errorf("unexpected reference: %q", n)
		}
	}
}

func TestBuildDivergenceReferences_ChainlinkEnabledButEmptyFeedMap_Skips(t *testing.T) {
	cfg := config.DivergenceConfig{
		CoinGecko: config.DivergenceCoinGeckoConfig{Enabled: false},
		Chainlink: config.DivergenceChainlinkConfig{
			Enabled: true,
			FeedMap: map[string]config.ChainlinkFeedConfig{},
		},
	}
	refs := buildDivergenceReferences(cfg, discardLogger())
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0 (empty FeedMap should not wire Chainlink)", len(refs))
	}
}

func TestBuildDivergenceReferences_AllDisabled(t *testing.T) {
	cfg := config.DivergenceConfig{
		CoinGecko: config.DivergenceCoinGeckoConfig{Enabled: false},
		Chainlink: config.DivergenceChainlinkConfig{Enabled: false},
	}
	refs := buildDivergenceReferences(cfg, discardLogger())
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0", len(refs))
	}
}
