// Lint enforcing ADR-0018's URL discipline on the OpenAPI spec.
//
// The three API consistency surfaces — closed-bucket, tip,
// observations — are distinguishable only by URL. ADR-0018
// §"URL discipline as the contract enforcer" says:
//
//	"Query parameters MUST NOT change a surface's consistency
//	 contract. ?freshness=tip on /v1/price is prohibited by this
//	 ADR — tip semantics require the /v1/price/tip URL."
//
// This linter walks every operation's query parameters and rejects
// any that look like they're selecting a consistency tier rather
// than refining a request within one. Two rules:
//
//  1. Forbidden parameter NAMES — `freshness`, `consistency`,
//     `surface`, `tier`. These are the literal pattern the ADR
//     prohibits, regardless of what their enum says.
//
//  2. Forbidden parameter ENUMS — any query parameter whose enum
//     contains TWO OR MORE values from a known set of consistency-tier
//     names (`closed`, `tip`, `latest`, `raw`, `observations`,
//     `bucketed`, `live`). One value is fine — `enum: [tip]` on a
//     param could be a stub. Two+ means the param is selecting BETWEEN
//     surfaces.
//
// Single-value enums and unrelated enums (e.g. `vwap|twap`,
// `native|classic|soroban|fiat`) pass cleanly.
//
// Usage:
//
//	go run ./scripts/ci/lint-openapi-urls openapi/rates-engine.v1.yaml
//
// Exits 0 on clean, 1 on any rule failure (with a list of offending
// path/parameter pairs to stderr).
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	forbiddenNames = map[string]bool{
		"freshness":   true,
		"consistency": true,
		"surface":     true,
		"tier":        true,
	}

	tierEnumValues = map[string]bool{
		"closed":       true,
		"tip":          true,
		"latest":       true,
		"raw":          true,
		"observations": true,
		"bucketed":     true,
		"live":         true,
	}
)

// param is the subset of an OpenAPI 3.1 parameter object we need to
// reason about. Inline params and `$ref` params share this shape
// after one resolution pass.
type param struct {
	Ref    string `yaml:"$ref"`
	Name   string `yaml:"name"`
	In     string `yaml:"in"`
	Schema struct {
		Type string   `yaml:"type"`
		Enum []string `yaml:"enum"`
	} `yaml:"schema"`
}

// operation captures the parameter list at one (path, verb).
type operation struct {
	Parameters []param `yaml:"parameters"`
}

// spec is the trimmed view we walk.
type spec struct {
	Paths      map[string]map[string]operation `yaml:"paths"`
	Components struct {
		Parameters map[string]param `yaml:"parameters"`
	} `yaml:"components"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: lint-openapi-urls <openapi-spec>")
		os.Exit(2)
	}
	specPath := os.Args[1]
	data, err := os.ReadFile(specPath) //nolint:gosec // CI tool — operator-supplied spec path is the whole point
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", specPath, err)
		os.Exit(2)
	}
	var s spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", specPath, err)
		os.Exit(2)
	}

	violations := lint(&s)
	if len(violations) == 0 {
		fmt.Println("openapi-urls: all query parameters comply with ADR-0018 URL discipline")
		os.Exit(0)
	}

	sort.Strings(violations)
	fmt.Fprintln(os.Stderr, "openapi-urls: ADR-0018 URL-discipline violations found:")
	fmt.Fprintln(os.Stderr)
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, "  "+v)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Query parameters MUST NOT change a surface's consistency contract.")
	fmt.Fprintln(os.Stderr, "Use a separate URL (/v1/price vs /v1/price/tip vs /v1/observations) instead.")
	fmt.Fprintln(os.Stderr, "See docs/adr/0018-api-consistency-surfaces.md.")
	os.Exit(1)
}

// lint walks every (path, verb, query parameter) tuple and returns a
// slice of human-readable violation strings.
func lint(s *spec) []string {
	var violations []string

	for path, verbs := range s.Paths {
		for verb, op := range verbs {
			for _, p := range op.Parameters {
				resolved, ok := resolve(s, p)
				if !ok {
					// $ref to a non-existent component — that's a
					// separate spec-validity problem, surfaced by
					// Spectral. Skip here.
					continue
				}
				if resolved.In != "query" {
					continue
				}
				for _, msg := range checkParam(resolved) {
					violations = append(violations,
						fmt.Sprintf("%s %s — query param %q: %s",
							strings.ToUpper(verb), path, resolved.Name, msg))
				}
			}
		}
	}
	return violations
}

// resolve handles inline params (returned as-is) and `$ref` params
// (looked up in components.parameters). Returns ok=false when a
// `$ref` points at a missing component.
func resolve(s *spec, p param) (param, bool) {
	if p.Ref == "" {
		return p, true
	}
	const prefix = "#/components/parameters/"
	if !strings.HasPrefix(p.Ref, prefix) {
		return param{}, false
	}
	name := strings.TrimPrefix(p.Ref, prefix)
	target, ok := s.Components.Parameters[name]
	return target, ok
}

// checkParam runs both rules and returns one message per failure
// (zero messages = clean). Multiple rules can fire on the same param.
func checkParam(p param) []string {
	var msgs []string
	if forbiddenNames[strings.ToLower(p.Name)] {
		msgs = append(msgs,
			fmt.Sprintf("name is on the prohibited list (%v) — these names imply selecting between consistency surfaces, which must be done by URL not by query parameter",
				sortedKeys(forbiddenNames)))
	}
	if hits := tierHits(p.Schema.Enum); len(hits) >= 2 {
		msgs = append(msgs,
			fmt.Sprintf("enum contains multiple consistency-tier values %v — a single query parameter selecting between tiers is exactly the pattern ADR-0018 prohibits",
				hits))
	}
	return msgs
}

// tierHits returns the subset of `enum` values that match a known
// consistency-tier name. Order-stable so error messages are
// deterministic.
func tierHits(enum []string) []string {
	var hits []string
	for _, v := range enum {
		if tierEnumValues[strings.ToLower(v)] {
			hits = append(hits, v)
		}
	}
	return hits
}

// sortedKeys returns a deterministic, alphabetised list of map keys
// for use in error messages.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
