package main

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLint_RealSpecIsClean — the current rates-engine.v1.yaml MUST
// pass the lint. If a future PR introduces a violating param, this
// test fails before CI even sees it.
func TestLint_RealSpecIsClean(t *testing.T) {
	const specPath = "../../../openapi/rates-engine.v1.yaml"
	data := mustReadFile(t, specPath)
	var s spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	if v := lint(&s); len(v) > 0 {
		t.Errorf("real spec has %d violations; expected 0:\n  %s",
			len(v), strings.Join(v, "\n  "))
	}
}

// TestLint_ForbiddenName — a `?freshness=...` query param trips the
// name-rule even when its enum looks innocuous.
func TestLint_ForbiddenName(t *testing.T) {
	s := mustParse(t, `
paths:
  /price:
    get:
      parameters:
        - name: freshness
          in: query
          schema: { type: string, enum: [a, b, c] }
`)
	violations := lint(s)
	if len(violations) == 0 {
		t.Fatal("expected violation for ?freshness, got none")
	}
	if !strings.Contains(violations[0], "freshness") {
		t.Errorf("violation message should name the offending param; got %q",
			violations[0])
	}
}

// TestLint_TierEnumPair — a single enum that branches between two
// consistency-tier names is the exact anti-pattern ADR-0018
// prohibits, even when the param name is innocuous.
func TestLint_TierEnumPair(t *testing.T) {
	s := mustParse(t, `
paths:
  /price:
    get:
      parameters:
        - name: mode
          in: query
          schema: { type: string, enum: [closed, tip] }
`)
	violations := lint(s)
	if len(violations) == 0 {
		t.Fatal("expected violation for enum [closed, tip], got none")
	}
}

// TestLint_SingleTierValueIsFine — `enum: [tip]` alone might be a
// stub for a future expansion, OR a deliberate opt-in for that
// surface only on this URL. One value is not selecting between
// surfaces, so the rule doesn't fire.
func TestLint_SingleTierValueIsFine(t *testing.T) {
	s := mustParse(t, `
paths:
  /price/tip:
    get:
      parameters:
        - name: mode
          in: query
          schema: { type: string, enum: [tip] }
`)
	if v := lint(s); len(v) > 0 {
		t.Errorf("single-value enum should pass; got %v", v)
	}
}

// TestLint_BenignEnumPasses — `?price_type=vwap|twap` is a refinement
// within the closed-bucket contract, not selecting between surfaces.
// Must NOT fire.
func TestLint_BenignEnumPasses(t *testing.T) {
	s := mustParse(t, `
paths:
  /price:
    get:
      parameters:
        - name: price_type
          in: query
          schema: { type: string, enum: [vwap, twap] }
`)
	if v := lint(s); len(v) > 0 {
		t.Errorf("vwap/twap enum should pass; got %v", v)
	}
}

// TestLint_PathParamsIgnored — only query parameters are subject to
// URL discipline. Path params (`{asset_id}`) don't get the same check.
func TestLint_PathParamsIgnored(t *testing.T) {
	s := mustParse(t, `
paths:
  /price/{tier}:
    get:
      parameters:
        - name: tier
          in: path
          schema: { type: string }
`)
	if v := lint(s); len(v) > 0 {
		t.Errorf("path params should be ignored; got %v", v)
	}
}

// TestLint_RefResolution — a `$ref` to a forbidden-named component
// parameter trips the lint, same as an inline forbidden param.
func TestLint_RefResolution(t *testing.T) {
	s := mustParse(t, `
paths:
  /price:
    get:
      parameters:
        - { $ref: "#/components/parameters/Freshness" }
components:
  parameters:
    Freshness:
      name: freshness
      in: query
      schema: { type: string }
`)
	if v := lint(s); len(v) == 0 {
		t.Error("$ref to forbidden-named param should trip the lint")
	}
}

// TestLint_DanglingRefSkipped — a $ref to a missing component is a
// spec-validity bug (Spectral catches it). Our linter should not
// double-report it.
func TestLint_DanglingRefSkipped(t *testing.T) {
	s := mustParse(t, `
paths:
  /price:
    get:
      parameters:
        - { $ref: "#/components/parameters/DoesNotExist" }
`)
	if v := lint(s); len(v) > 0 {
		t.Errorf("dangling $ref should be skipped (Spectral's job); got %v", v)
	}
}

// TestForbiddenNames_Stable — the forbidden-name list shows up in
// error messages, which become operator-facing diagnostics. The
// sortedKeys helper ensures the output is deterministic.
func TestForbiddenNames_Stable(t *testing.T) {
	got := sortedKeys(forbiddenNames)
	want := []string{"consistency", "freshness", "surface", "tier"}
	if !equalStrings(got, want) {
		t.Errorf("sortedKeys(forbiddenNames) = %v, want %v", got, want)
	}
}

// --- helpers ---

func mustParse(t *testing.T, body string) *spec {
	t.Helper()
	var s spec
	if err := yaml.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("parse synthetic spec: %v", err)
	}
	return &s
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string{}, a...)
	bb := append([]string{}, b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
