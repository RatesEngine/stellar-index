# W20 — CG/CMC parity execution

## Scope

Execute the parity matrix in
[../08-cgcmc-parity-matrix.md](../08-cgcmc-parity-matrix.md)
row-by-row. Every cell must be filled with `covered` /
`partial` / `gap` / `non-goal` / `n/a` and an evidence ref.

## Method

For each row:

1. Identify the code path that fulfils it (handler, table, doc,
   binary, ansible role, etc.).
2. Probe live R1 if the feature is observable on the wire.
3. Cross-check the OpenAPI shape vs the live response.
4. Sample CG/CMC documentation to verify the feature claim is
   accurate (link to their docs).
5. Score the cell + cite EV-####.
6. If `gap` or `partial`: open a finding (`F-####`) and append
   to the remediation plan with a wave assignment per
   `../11-severity-rubric.md`.

## Section-by-section ownership

| Section | Rows | Focus | Primary workstream cross-ref |
| --- | --- | --- | --- |
| A. Asset directory + identity | 13 | `/v1/assets`, `/v1/assets/verified`, `/v1/assets/{id}` | W12 |
| B. Price + market data | 25 | `/v1/price`, `/v1/markets`, `/v1/chart`, `/v1/trades` | W10 + W11 |
| C. Coverage breadth | 10 | source fleet + catalogue | W08 + W12 |
| D. History + reproducibility | 5 | `/v1/history`, `/v1/history/since-inception` | W11 + W09 |
| E. Streaming / real-time | 4 | `/v1/price/stream`, `/v1/observations/stream` | W11 |
| F. Developer experience | 16 | OpenAPI, SDKs, errors, key mgmt | W11 + W19 |
| G. Trust + transparency | 12 | methodology, source contributions, status page | W10 + W14 + W17 |
| H. Pricing model + commercial | 6 | billing, quotas | W19 |
| I. Frontend surface | 9 | explorer | W17 |
| J. Operational parity | 6 | uptime, multi-region | W14 + W23 |

## Severity rules for parity findings

- A `gap` row on a CG/CMC launch-headline feature (e.g. asset
  detail page, OHLC chart, market cap chart, methodology page,
  status page) = `high` severity (Wave 0 or 1).
- A `partial` row on a similar feature = `medium` (Wave 1 or 2).
- A `gap` row on a niche feature (e.g. order-book snapshot) =
  `medium` unless we claim parity in marketing material.
- A `non-goal` row requires evidence of the product decision
  (ADR, CHANGELOG, or memory entry); without it, treat as `gap`.

## Closure criteria

- Every row in the matrix has a status + evidence
- Every `gap` and `partial` row has a finding
- Roll-up tables at the bottom of `08-cgcmc-parity-matrix.md`
  are populated
- Zero blank cells
