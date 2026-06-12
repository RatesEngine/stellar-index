package config_test

import (
	"bytes"
	"strings"
	"testing"

	cfg "github.com/StellarAtlas/stellar-atlas/internal/config"
)

func TestDescribe_everyFieldHasDoc(t *testing.T) {
	// Describe() panics if any leaf field is missing `doc:`. Running
	// it successfully is the whole invariant.
	fields := cfg.Describe()
	if len(fields) == 0 {
		t.Fatal("Describe returned no fields")
	}
	for _, f := range fields {
		if f.Doc == "" {
			t.Errorf("field %q has empty doc", f.Path)
		}
		if !strings.Contains(f.Path, ".") {
			t.Errorf("field %q has no dot — top-level section missing", f.Path)
		}
	}
}

func TestEmitMarkdown_hasGeneratedBanner(t *testing.T) {
	var buf bytes.Buffer
	if err := cfg.EmitMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(firstLine, "GENERATED FILE") {
		t.Fatalf("line 1 = %q, want the generated-file banner", firstLine)
	}
	// Every top-level section should appear as a heading.
	for _, s := range []string{"[region]", "[stellar]", "[storage]", "[ingestion]", "[aggregate]", "[api]", "[obs]"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing section %s", s)
		}
	}
}

func TestDefault_isValidShape(t *testing.T) {
	c := cfg.Default()
	if c.Region.ID == "" {
		t.Error("Region.ID empty in defaults")
	}
	if c.Stellar.Network == "" {
		t.Error("Stellar.Network empty in defaults")
	}
	if len(c.Stellar.RPCEndpoints) == 0 {
		t.Error("Stellar.RPCEndpoints empty in defaults")
	}
	if c.Aggregate.MinUSDVolume <= 0 {
		t.Error("Aggregate.MinUSDVolume must be positive")
	}
	if c.Obs.TraceSample < 0 || c.Obs.TraceSample > 1 {
		t.Errorf("Obs.TraceSample out of [0,1]: %f", c.Obs.TraceSample)
	}
}
