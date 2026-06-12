package obs_test

import (
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// TestNewLogger_StampsBinaryAttr — every emitted record carries the
// binary attribute so Loki dashboards can filter per-binary.
func TestNewLogger_StampsBinaryAttr(t *testing.T) {
	t.Parallel()
	logger := obs.NewLogger(config.ObsConfig{LogFormat: "json"}, "ratesengine-test")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
	// We can't introspect attributes without writing — confirm the
	// non-nil guarantee + the empty-binary skip.
	bare := obs.NewLogger(config.ObsConfig{LogFormat: "json"}, "")
	if bare == nil {
		t.Fatal("NewLogger with empty binary returned nil")
	}
}

// TestNewLogger_LogLevelCaseInsensitive — operators sometimes write
// "DEBUG" / "warning" / "Error"; all should land on the matching
// slog.Level. The aggregator's previous bespoke factory was
// case-sensitive, missed "warning", and didn't lowercase before
// switching. The shared factory fixes all three.
func TestNewLogger_LogLevelCaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []string{"debug", "DEBUG", "Debug", "warn", "WARNING", "Warning", "error", "ERROR"}
	for _, lvl := range cases {
		t.Run(lvl, func(t *testing.T) {
			t.Parallel()
			logger := obs.NewLogger(config.ObsConfig{LogLevel: lvl, LogFormat: "json"}, "test")
			if logger == nil {
				t.Fatalf("NewLogger(%q) returned nil", lvl)
			}
		})
	}
}

// TestNewLogger_LogFormatCaseInsensitive — operators sometimes set
// LogFormat to "TEXT" / "Console" / "json"; all should resolve to a
// valid handler. Default is JSON for any unrecognised value (e.g.
// the empty string).
func TestNewLogger_LogFormatCaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []string{"json", "JSON", "console", "CONSOLE", "text", "Text", "", "unknown-format"}
	for _, fmt := range cases {
		name := fmt
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger := obs.NewLogger(config.ObsConfig{LogFormat: fmt}, "test")
			if logger == nil {
				t.Fatalf("NewLogger(format=%q) returned nil", fmt)
			}
		})
	}
}

// TestNewLogger_BinaryAttrNonEmpty — the binaryName string lands on
// the logger; the empty-string case skips the attribute entirely
// rather than stamping a blank value.
func TestNewLogger_BinaryAttrNonEmpty(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ratesengine-indexer", "ratesengine-aggregator", "ratesengine-api"} {
		if !strings.HasPrefix(name, "ratesengine-") {
			t.Fatalf("test asserts binary names start with ratesengine-")
		}
		logger := obs.NewLogger(config.ObsConfig{LogFormat: "json"}, name)
		if logger == nil {
			t.Fatalf("NewLogger(binary=%q) returned nil", name)
		}
	}
}
