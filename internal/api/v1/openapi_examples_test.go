package v1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestOpenAPIExamplesParseAsCanonicalAssets walks the OpenAPI spec
// and asserts that every documented `asset` / `asset_id` / `base` /
// `quote` / `asset_ids` parameter example is a value the canonical
// asset parser actually accepts.
//
// Why this exists: 2026-05-08 the user reported every Scalar
// default test request returning 400 Bad Request — examples in the
// spec used short symbols like `USDC` / `XLM` that the
// canonical-asset validator rejects. The fix updated the examples;
// this test prevents the drift class from recurring. If a future
// PR sets `example: BTC` on `/v1/price?asset=`, this test fails
// at PR time rather than at deploy time.
//
// Two namespaces are accepted:
//   - canonical asset IDs (`native`, `<code>-<G…>`, `<contract>`)
//     parsed by canonical.ParseAsset.
//   - SEP-40 oracle keys (`crypto:XLM`, `fiat:EUR`) which
//     canonical.ParseAsset also accepts.
//
// Tickers (`EUR`, `USD`) on the `/v1/currencies/{ticker}` path are
// NOT canonical assets — they're ISO-4217 codes — so we accept
// 3-letter alpha tokens via a permissive matcher. Same for the
// asset slug (`/v1/coins/{slug}`) which has its own resolver.
func TestOpenAPIExamplesParseAsCanonicalAssets(t *testing.T) {
	spec := loadOpenAPISpec(t)

	// Walk every operation in every path, collecting (path, verb,
	// param-name, example) tuples for the asset-typed params.
	type finding struct {
		path, verb, paramName, example string
	}
	var findings []finding

	for path, item := range spec.Paths {
		for verb, op := range item {
			if op == nil {
				continue
			}
			for _, p := range op.Parameters {
				if !isAssetParam(p.Name) {
					continue
				}
				ex := p.example()
				if ex == "" {
					continue
				}
				findings = append(findings, finding{path, verb, p.Name, ex})
			}
		}
	}
	// Also walk shared components.parameters — those flow into every
	// $ref-using operation. Each component params object has the
	// same shape as an inline parameter.
	for refName, p := range spec.Components.Parameters {
		if !isAssetParam(p.Name) {
			continue
		}
		ex := p.example()
		if ex == "" {
			continue
		}
		findings = append(findings, finding{
			path: "components.parameters." + refName, verb: "*",
			paramName: p.Name, example: ex,
		})
	}

	if len(findings) == 0 {
		t.Fatal("found zero asset-typed examples in the OpenAPI spec — " +
			"either the spec is empty or this test's discovery is broken")
	}

	// Path-param exemptions: ticker (ISO-4217) and slug (asset slug
	// resolver) deliberately accept short forms that the strict
	// canonical parser would reject. This test covers the strict
	// surfaces; loose-input handlers have their own tests.
	exempt := map[string]bool{
		"ticker": true,
		"slug":   true,
	}

	for _, f := range findings {
		if exempt[f.paramName] {
			continue
		}
		t.Run(f.path+"_"+f.paramName, func(t *testing.T) {
			values := []string{f.example}
			if f.paramName == "asset_ids" {
				values = strings.Split(f.example, ",")
			}
			for _, v := range values {
				v = strings.TrimSpace(v)
				if _, err := canonical.ParseAsset(v); err != nil {
					t.Errorf(
						"openapi: example %q on %s.%s parameter %q "+
							"does not parse as a canonical asset (%v) — "+
							"Scalar's default Send button will return 400. "+
							"Use `native`, `<code>-<G…>`, `crypto:<sym>`, "+
							"or `fiat:<ISO>`.",
						v, f.path, f.verb, f.paramName, err)
				}
			}
		})
	}
}

// isAssetParam returns true for the parameter names whose values
// are canonical asset identifiers per the API's strict validators.
// Excludes `ticker` (ISO-4217), `slug` (loose asset-slug resolver),
// and `keyID` / `id` / `g_strkey` / `entity_type` / `name` which
// are not asset identifiers.
func isAssetParam(name string) bool {
	switch name {
	case "asset", "asset_id", "asset_ids", "base", "quote":
		return true
	}
	return false
}

// openAPISpec is the subset of the OpenAPI 3.1 document we walk.
// Path-item verbs and shared components.parameters are the two
// entry points.
type openAPISpec struct {
	Paths      map[string]map[string]*openAPIOperation `yaml:"paths"`
	Components struct {
		Parameters map[string]openAPIParameter `yaml:"parameters"`
	} `yaml:"components"`
}

type openAPIOperation struct {
	Parameters []openAPIParameter `yaml:"parameters"`
}

type openAPIParameter struct {
	Ref     string  `yaml:"$ref"`
	Name    string  `yaml:"name"`
	Example any     `yaml:"example"`
	Schema  *schema `yaml:"schema"`
}

type schema struct {
	Example any `yaml:"example"`
}

// example returns the parameter's example value, preferring the
// top-level `example:` field but falling back to `schema.example`
// (the OpenAPI 3.1 alternate location). Both are accepted by Scalar
// and other tooling. Returns "" when neither is set or the value
// isn't a string.
func (p openAPIParameter) example() string {
	if s, ok := p.Example.(string); ok {
		return s
	}
	if p.Schema != nil {
		if s, ok := p.Schema.Example.(string); ok {
			return s
		}
	}
	return ""
}

func loadOpenAPISpec(t *testing.T) openAPISpec {
	t.Helper()
	// Walk up to repo root looking for openapi/rates-engine.v1.yaml —
	// this test runs from internal/api/v1/ when invoked through `go
	// test ./...` from any directory.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var specPath string
	for i := 0; i < 8; i++ {
		try := filepath.Join(dir, "openapi", "rates-engine.v1.yaml")
		if _, err := os.Stat(try); err == nil {
			specPath = try
			break
		}
		dir = filepath.Dir(dir)
	}
	if specPath == "" {
		t.Fatal("could not locate openapi/rates-engine.v1.yaml from cwd; " +
			"this test must run inside the rates-engine repo tree")
	}
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var spec openAPISpec
	if err := yaml.Unmarshal(body, &spec); err != nil {
		t.Fatalf("yaml decode: %v", err)
	}
	return spec
}
