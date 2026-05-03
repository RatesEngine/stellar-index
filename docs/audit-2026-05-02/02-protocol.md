# Execution Protocol

## 1. Zero-Trust Rule

Docs are not facts. For any claim from markdown, architecture notes,
prior audits, or discovery artifacts:

1. locate the live code path
2. locate the test or runtime wiring
3. record whether the doc is true, stale, partial, or contradicted

## 2. Evidence Discipline

Nothing is accepted from memory.

Every material claim must cite at least one of:

- a local file reference with line anchor
- a generated inventory artifact in this directory
- a command output captured in `evidence/log.md`
- a test file and the behavior it actually asserts

## 3. Evidence Log Format

Use [evidence/log.md](evidence/log.md).

Each row records:

- `EV-XXXX`
- date
- claim or observation
- source refs
- notes

## 4. Cross-File Interaction Log

Use [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md).

Record seams such as:

- binary -> package wiring
- package -> package interfaces
- handler -> storage adapter
- decoder -> sink -> storage
- aggregator -> Redis -> API
- workflow -> script -> generated artifact
- runbook or proposal text -> code path it purports to describe

## 5. Per-File Audit Loop

For every tracked file in `inventory/file-coverage.tsv`:

1. identify its role
2. identify inbound dependencies
3. identify outbound dependencies
4. identify invariants or trust boundaries
5. identify tests that exercise it, if any
6. identify docs that describe it, if any
7. assign terminal status and evidence refs

Allowed statuses:

- `todo`
- `in_progress`
- `done`
- `blocked`
- `excluded`

## 6. Findings Rules

Use [05-findings-register.md](05-findings-register.md).

Each finding needs:

- stable ID
- severity
- concise title
- affected surface
- evidence refs
- disposition

Severity scale:

- `critical`
- `high`
- `medium`
- `low`
- `note`

## 7. Exclusions Rules

Use [06-exclusions-register.md](06-exclusions-register.md).

Any skipped scope must record:

- exact excluded thing
- reason
- temporary or permanent for this audit
- evidence needed to re-enter scope

## 8. Test Interpretation Rules

Tests prove asserted behavior only.

For each important suite, record:

- what it actually asserts
- what it leaves unproven
- whether CI runs it
- whether live runtime wiring could still break despite green tests

## 9. Docs-Truth Rules

When docs disagree with code:

- do not silently trust either side
- log evidence from both sides
- state whether the doc overstated, understated, or contradicted reality

## 10. Final Condition

The audit is complete only when the control docs, evidence logs,
inventory, findings, exclusions, and remediation plan can be followed
without relying on undocumented context.
