package clickhouse

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestBuildWasmDisassembly_BestEffort(t *testing.T) {
	info := ContractWasmInfo{}
	buildWasmDisassembly(context.Background(), &info, contractRegisterWasm)

	_, hasWat2 := exec.LookPath("wasm2wat")
	if hasWat2 == nil {
		// Tooling present (dev box / CI with wabt): WAT must render and start
		// with the module header.
		if !strings.HasPrefix(strings.TrimSpace(info.Wat), "(module") {
			t.Errorf("wat does not look like WAT: %.40q", info.Wat)
		}
	} else {
		// Tooling absent (e.g. a stock r1 without `apt install wabt`): the
		// field is empty and the note explains why. The endpoint still ships
		// metadata + exports — this is the graceful-degradation contract.
		if info.Wat != "" {
			t.Errorf("expected empty wat without wasm2wat, got %d bytes", len(info.Wat))
		}
		if !strings.Contains(info.ToolNote, "not on PATH") {
			t.Errorf("expected a 'not on PATH' note, got %q", info.ToolNote)
		}
	}
}

func TestRunWasmTool_MissingTool(t *testing.T) {
	out, note := runWasmTool(context.Background(), "definitely-not-a-real-tool-xyz", contractRegisterWasm)
	if out != "" {
		t.Errorf("expected empty output for missing tool, got %d bytes", len(out))
	}
	if !strings.Contains(note, "not on PATH") {
		t.Errorf("expected 'not on PATH' note, got %q", note)
	}
}
