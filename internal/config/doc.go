// Package config defines the root configuration shape for every
// Rates Engine binary, plus the loader + the struct-tag-based doc
// generator that emits docs/reference/config/README.md.
//
// # Shape
//
// The [Config] struct is the root. Each binary reads the whole file
// and uses only the substructs that pertain to it — the indexer
// cares about Region + Stellar + Ingestion; the API cares about
// Region + Storage + API + Obs; and so on. Sharing one shape means
// operators maintain one config file across a deployment.
//
// # Extending the schema
//
// Adding or renaming a field is a load-bearing change. Every field
// MUST have the `doc:"…"` tag or CI fails (lint-docs.sh §1 checks
// that every exported field round-trips into the reference doc).
//
// Add the field → `go run ./cmd/ratesengine-ops docs-config >
// docs/reference/config/README.md` → commit both in the same PR.
//
// # Invariants
//
//   - TOML is the wire format (operators hand-edit config.toml).
//   - Every field has a `default:` tag. The fully defaulted Config
//     (as returned by [Default]) always passes [Config.Validate] —
//     guarded by TestValidate_DefaultPasses. A literal zero-value
//     Config{} is NOT valid because required fields like Region.ID
//     are empty; always load via [Load] / [LoadWithEnv] which
//     apply defaults first.
//   - Secrets (passwords, API keys) are never in this file. They
//     come from environment variables or a secret store, referenced
//     by name here (e.g. `Password: "env:RATESENGINE_PG_PASSWORD"`).
//
// See:
//   - docs/reference/config/README.md — generated reference.
//   - docs/architecture/repo-hygiene-plan.md §1 — doc-code round-trip rule.
package config
