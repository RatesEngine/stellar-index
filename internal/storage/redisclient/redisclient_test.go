package redisclient_test

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/redisclient"
)

func TestBuild_DisabledWhenAllEmpty(t *testing.T) {
	got := redisclient.Build(config.StorageConfig{})
	if got != nil {
		t.Fatalf("expected nil client when both RedisAddr and RedisSentinelAddrs are empty, got %T", got)
	}
	if mode := redisclient.Mode(config.StorageConfig{}); mode != "disabled" {
		t.Errorf("Mode: want disabled, got %q", mode)
	}
}

func TestBuild_SinglePrefersSentinelWhenBothSet(t *testing.T) {
	// When both are set, Sentinel wins — operators upgrading from
	// single-node leave RedisAddr in place, set the Sentinel
	// fields, and the deploy switches over without a config-strip
	// step.
	cfg := config.StorageConfig{
		RedisAddr:          "127.0.0.1:6379",
		RedisSentinelAddrs: []string{"sentinel-1:26379", "sentinel-2:26379"},
		RedisMasterName:    "ratesengine-test-cache",
	}
	got := redisclient.Build(cfg)
	if got == nil {
		t.Fatal("expected non-nil FailoverClient")
	}
	defer func() { _ = got.Close() }()
	if mode := redisclient.Mode(cfg); mode != "sentinel" {
		t.Errorf("Mode: want sentinel, got %q", mode)
	}
}

func TestBuild_SingleWhenNoSentinel(t *testing.T) {
	cfg := config.StorageConfig{RedisAddr: "127.0.0.1:6379"}
	got := redisclient.Build(cfg)
	if got == nil {
		t.Fatal("expected non-nil Client")
	}
	defer func() { _ = got.Close() }()
	if mode := redisclient.Mode(cfg); mode != "single" {
		t.Errorf("Mode: want single, got %q", mode)
	}
}
