module github.com/RatesEngine/rates-engine

go 1.25

// Direct production dependencies.
//
// Every entry below has either an ADR justifying it or a one-line
// comment explaining its role. No unaudited deps.
// See docs/discovery/engineering-standards.md §2.5.

// NOTE: this file is the skeleton for Phase 2 build work. Additional
// deps (database driver, Redis client, HTTP framework, Prometheus,
// TOML parser, k6 harness, etc.) land alongside the packages that
// use them. See CHANGELOG [Unreleased].

require github.com/golang-migrate/migrate/v4 v4.19.1

require github.com/lib/pq v1.12.3 // indirect
