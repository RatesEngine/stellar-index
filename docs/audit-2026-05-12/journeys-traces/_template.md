# Journey Trace — J## — &lt;short name&gt;

Trace date: <YYYY-MM-DD>
Auditor session: <short ID>
Journey definition: see `../03-journeys.md` § J##

## Inputs

- <input 1: shape, source, scale>
- <input 2>
- <hostile-input variant if walked adversarially>

## Hops

| # | Component | File:line | Action |
| --- | --- | --- | --- |
| 1 | … | `internal/…/file.go:NN` | … |
| 2 | … | … | … |

## Sinks

- <DB row, Redis key, HTTP response, log line, metric, alert>

## Failure modes observed

- … (with evidence ref to `../evidence/log.md` `EV-####`)

## Tests covering this journey

- `path/to/test_file_test.go::TestName` — what it asserts
- gap: …

## Live R1 evidence (where applicable)

- transcript: `../evidence/r1-probes/<file>.md`

## Disposition

- `done` / `gap` / `blocked`
- finding(s): `F-####`
