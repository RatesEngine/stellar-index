---
title: Incidents — public-facing customer comms (moved)
last_verified: 2026-05-07
status: redirect
---

# Incidents — moved

The customer-facing incident posts now live at
**[`internal/incidents/data/`](../../../internal/incidents/data/)**.

They were moved out of `docs/operations/incidents/` so the
ratesengine-api binary can `go:embed` them at build time and serve
the parsed corpus via `GET /v1/incidents`. The public status page
(status.ratesengine.net) reads from that endpoint instead of the
hardcoded array it shipped with previously.

## Why the move

`go:embed` paths can't traverse upward (`..` is forbidden), so the
data directory has to live under the importing package's tree. We
chose to move the source-of-truth rather than mirror the files via
a Makefile rule — single source of truth, no drift.

## File naming + workflow

Unchanged. Files still use `<YYYY-MM-DD>-<short-slug>.md` with the
same YAML frontmatter shape (see
[`internal/incidents/data/_template.md`](../../../internal/incidents/data/_template.md)).
The same SEV declaration / update / resolution playbook from
[`sev-playbook.md`](../sev-playbook.md) applies — only the
filesystem destination changed.

## API surface

`GET /v1/incidents` returns the parsed corpus as JSON, sorted by
`started_at` descending (most recent first). Files starting with
`_` (templates) are skipped.

## Internal postmortems

Distinct from the customer-facing posts above, internal post-mortems
still live at [`docs/operations/postmortems/`](../postmortems/) — that
directory is unaffected by this move.
