# Checklist — add an API endpoint (v1)

Reference: any `internal/api/v1/*.go` handler + `server.go::mountRoutes`.

- [ ] `openapi/stellar-index.v1.yaml` — add the path (no `/v1` prefix) + response schema. **Source of truth.**
- [ ] `internal/api/v1/<area>.go` — `func (s *Server) handleX(w, r)`. Reuse `envelope.go` for
      the response envelope; reuse the `Cached*Reader` wrappers if the read is hot — and if you
      prewarm, call the reader with **byte-identical args** to the handler (cache-key-drift trap).
- [ ] `internal/api/v1/server.go` → `mountRoutes`: `s.mux.HandleFunc("GET /v1/x", s.handleX)`.
      (If it needs middleware, wrap it — but note the route lint currently only greps `HandleFunc(`,
      so a `Handle(`-wrapped route escapes the spec drift-check.)
- [ ] `internal/api/v1/<area>_test.go` — handler test (table-driven; assert the wire shape).
- [ ] Regenerate **all three** spec artifacts: `make docs-api` (drift-guarded), `make docs-postman`,
      `make web-generate-api`. Commit every diff.
- [ ] `CHANGELOG.md` `[Unreleased]`; bump the API minor (additive) / major (breaking).

**Guard:** `scripts/ci/lint-docs.sh` fails if a `HandleFunc("GET /v1/…")` route is missing from
the spec or vice-versa. **Done when:** handler test green; `make docs-all` produces no diff.
