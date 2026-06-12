package obs

import (
	"log/slog"
	"os"
	"strings"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
)

// NewLogger builds the shared *slog.Logger every binary uses.
//
// Configuration comes from the operator's [obs] block:
//
//   - LogLevel: case-insensitive; "debug" / "info" / "warn"
//     (alias "warning") / "error". Anything else falls back to
//     info.
//   - LogFormat: case-insensitive; "console" or "text" emits the
//     human-friendly text handler; anything else (including the
//     default "json") emits the structured JSON handler.
//
// Every logger is stamped with a `binary` attribute so a unified
// log aggregator (Loki) can filter per-binary without grepping
// path prefixes. binaryName is what gets stamped — pass the
// program's basename (e.g. "ratesengine-indexer").
//
// Output goes to stderr because that's what systemd's StandardError
// captures into the journal by default; stdout is reserved for
// human-targeted CLI output (e.g. the ops binary's tabular reports).
func NewLogger(cfg config.ObsConfig, binaryName string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.LogFormat) {
	case "console", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	logger := slog.New(handler)
	if binaryName != "" {
		logger = logger.With("binary", binaryName)
	}
	return logger
}
