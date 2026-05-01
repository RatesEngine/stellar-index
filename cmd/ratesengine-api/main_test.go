package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/config"
)

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
