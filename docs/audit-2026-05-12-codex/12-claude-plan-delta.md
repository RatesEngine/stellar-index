# Claude Plan Delta Review

Compared: `docs/audit-2026-05-12/` against
`docs/audit-2026-05-12-codex/`.

Date: `2026-05-12`

## Summary

Claude's plan does cover several artifacts or gates that the original
Codex plan covered only generically. The Codex plan is now amended with
dedicated matrices/protocols for the material deltas.

## Material Deltas Added

| Delta | Claude Coverage | Original Codex State | Codex Addition |
| --- | --- | --- | --- |
| CG/CMC parity matrix | Dedicated 106-row style matrix | W22 only, less explicit | [08-competitive-parity-matrix.md](08-competitive-parity-matrix.md) |
| Stellar-depth matrix | Dedicated Stellar-native checklist | W06/W08-W10/W22, less explicit | [09-stellar-depth-matrix.md](09-stellar-depth-matrix.md) |
| Attack tree | Concrete adversarial leaves | Hostile protocol and journeys, less concrete | [10-adversarial-attack-tree.md](10-adversarial-attack-tree.md) |
| R1 probe protocol | Specific read-only commands and reconciliations | General R1 rules | [11-r1-live-probe-protocol.md](11-r1-live-probe-protocol.md) |
| Launch/public flip | Dedicated workstream | Present under W18/W22 but not separately gated | Added closure gates in tracker and attack tree |
| Contract schema evolution/WASM history | Dedicated workstream | Present under W08/W18/W23 but not separately gated | Added Stellar-depth rows and tracker gate |
| Severity/wave discipline | Separate severity rubric with closure rules | Severity protocol present, less launch-wave detail | Attack tree and parity matrix now force launch-blocking gaps |
| Generated sub-inventories | API route, migration, metrics, runbooks, web, workflows | Only per-file inventory | Marked as execution artifacts to generate during P1/P8 |

## Deltas Not Added As Separate Files

- Claude has richer generated inventory files. Codex retains one
  canonical per-file inventory and requires route/migration/metric/etc.
  inventories during execution. The execution auditor should generate
  those as evidence artifacts rather than maintain two divergent static
  inventory systems.
- Claude includes initial live R1 observations. Codex does not copy
  those into findings because this audit must capture fresh R1 evidence
  under its own evidence IDs.

## Result

The Codex plan now covers all materially useful scope from Claude's
plan while preserving the cold-audit rule: no prior findings or live
observations are accepted without fresh evidence in this audit.
