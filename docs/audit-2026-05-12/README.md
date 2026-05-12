# Audit Workspace — 2026-05-12 (Cold + Adversarial)

This directory is the execution workspace for the 2026-05-12 cold
adversarial audit of the Rates Engine repository.

This audit assumes nothing. Markdown, ADRs, architecture notes,
proposal text, discovery notes, prior audits (`docs/audit-2026-04-29`,
`docs/audit-2026-05-02`), and even CLAUDE.md are inputs to reconcile
against live code, live tests, and the live R1 deployment — they
are never accepted as fact on their own.

The audit goal is twofold:

1. Prove that every file, every cross-file seam, every binary, and
   every operator path is correct, current, secure, and observable.
2. Identify every gap relative to two stretch goals:
   - **CoinGecko / CoinMarketCap parity** for the cross-asset price /
     market / charting / metadata surfaces (`08-cgcmc-parity-matrix.md`).
   - **Deeper Stellar-specific coverage** than CG/CMC across DEX/AMM
     trades, oracle feeds, supply derivation, SAC wrappers, SEP-1
     overlays, and SEP-40 oracle compatibility
     (`09-stellar-coverage-matrix.md`).

Good is not the bar. Perfect is the bar — anything else turns into
a CG/CMC parity gap or a Stellar-data trust gap once consumer
traffic ramps.

## Start Here (in order)

1. [00-plan.md](00-plan.md) — full workstream catalogue (W01..W26).
2. [02-protocol.md](02-protocol.md) — execution rules, evidence ID
   taxonomy (`EV/CMD/XFI/J/R1`), per-file role classification,
   finding-status taxonomy.
3. [11-severity-rubric.md](11-severity-rubric.md) — how findings are ranked.
4. [03-journeys.md](03-journeys.md) — mandatory end-to-end journeys (J01..J66).
5. [04-reconciliation.md](04-reconciliation.md) — comparison passes (R00, R0a, R01..R14).
6. [10-attack-tree.md](10-attack-tree.md) — adversarial vectors to test.
7. [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md) — R1 probe protocol + allowed observation classes.
8. [01-tracker.md](01-tracker.md) — master tracker and closure state.
9. [inventory/](inventory/) — generated repo snapshot, route map,
   migrations, ADRs, alerts. `file-coverage.tsv` carries
   `file_kind` + `workstream` columns for filtering.
10. [workstreams/](workstreams/) — per-workstream sub-plans (W01..W26).
11. [05-findings-register.md](05-findings-register.md) — findings ledger.
12. [06-exclusions-register.md](06-exclusions-register.md) — explicit out-of-scope.
13. [07-remediation-plan.md](07-remediation-plan.md) — remediation plan tied to findings.
14. [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md) — CG/CMC parity audit checklist (106 rows).
15. [09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md) — Stellar-specific coverage audit checklist (111 rows).

## Working Rules

- Every claim needs an evidence reference (`EV-####`).
- Every exclusion is written down explicitly.
- Every tracked file moves to a terminal status in
  [inventory/file-coverage.tsv](inventory/file-coverage.tsv).
- Every material cross-file seam is logged in
  [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md).
- Findings go in [05-findings-register.md](05-findings-register.md),
  not in ad hoc notes.
- Prior audits are baselines to **challenge**, not to inherit. Every
  prior finding is re-tested against the current snapshot from a
  clean slate; "closed" only means "demonstrated to no longer apply
  by present-day code + test + runtime evidence in this audit."
- R1 live state can be probed at any time via SSH (root@136.243.90.96).
  See [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md).
- The system is in live-development phase: deployed to R1 but not
  publicly launched. Findings should treat both pre-launch and
  post-launch concerns; severity rubric in
  [11-severity-rubric.md](11-severity-rubric.md) handles both.

## Directory Layout

- [00-plan.md](00-plan.md) — repo-specific audit plan.
- [01-tracker.md](01-tracker.md) — master tracker and closure state.
- [02-protocol.md](02-protocol.md) — execution rules and evidence discipline.
- [03-journeys.md](03-journeys.md) — mandatory end-to-end journeys.
- [04-reconciliation.md](04-reconciliation.md) — comparison passes.
- [05-findings-register.md](05-findings-register.md) — findings ledger.
- [06-exclusions-register.md](06-exclusions-register.md) — exclusions and blocked scope.
- [07-remediation-plan.md](07-remediation-plan.md) — prioritized remediation sequence.
- [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md) — CG/CMC parity audit checklist.
- [09-stellar-coverage-matrix.md](09-stellar-coverage-matrix.md) — Stellar-specific coverage matrix.
- [10-attack-tree.md](10-attack-tree.md) — adversarial vectors.
- [11-severity-rubric.md](11-severity-rubric.md) — severity definitions.
- [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md) — R1 SSH probe protocol.
- [evidence/](evidence/) — five ledgers split by ID prefix:
  `log.md` (EV-####), `commands.md` (CMD-####),
  `cross-file-interactions.md` (XFI-#### owned by W26),
  `tree-observations.md` (T-####), plus `r1-probes/`
  (R1-####), `journeys/`, `workstreams/`.
- [inventory/](inventory/) — generated repo snapshot and granular
  inventories (routes, migrations, ADRs, alerts, metrics,
  dependencies). `file-coverage.tsv` carries `file_kind` + `workstream`.
- [workstreams/](workstreams/) — per-workstream sub-plans
  (W01..W26) with their own per-file checklists. W26 is the
  cross-file gate that closes last.
- [journeys-traces/](journeys-traces/) — per-journey trace
  logs (J-#### IDs).

## Inventory Refresh

Refresh inventory from the current checkout with:

```sh
./docs/audit-2026-05-12/inventory/generate.sh
```

This regenerates: `repo-snapshot.md`, `area-counts.md`,
`file-coverage.tsv`, plus the granular inventories
(`api-route-inventory.md`, `migration-inventory.md`,
`source-decoder-inventory.md`, `workflow-inventory.md`,
`runbook-inventory.md`, `adr-inventory.md`,
`external-source-inventory.md`, `alert-rule-inventory.md`,
`metric-name-inventory.md`, `dependency-inventory.md`,
`docker-systemd-inventory.md`, `webfrontend-inventory.md`).

## Scope Reminder

This workspace audits the local repository snapshot **and** the live
R1 deployment. Hosted GitHub controls (branch protection, secrets,
required-checks), Cloudflare Pages settings, GHCR retention, and R2/R3
infrastructure that don't yet exist as code in this checkout can
only be marked verified when (a) repo proves it, (b) a live probe
is executed and logged as evidence, or (c) the operator confirms
the setting in a captured probe transcript.
