# Audit Workspace — 2026-05-02

This directory is the execution workspace for the 2026-05-02 cold
adversarial audit of the Rates Engine repository.

This workspace is intentionally zero-trust toward repo docs. Markdown,
ADRs, architecture notes, proposal text, discovery notes, and prior
audit artifacts are inputs to reconcile, not facts to inherit.

## Start Here

Read in this order:

1. [00-plan.md](00-plan.md)
2. [02-protocol.md](02-protocol.md)
3. [03-journeys.md](03-journeys.md)
4. [04-reconciliation.md](04-reconciliation.md)
5. [01-tracker.md](01-tracker.md)
6. [inventory/repo-snapshot.md](inventory/repo-snapshot.md)
7. [inventory/file-coverage.tsv](inventory/file-coverage.tsv)
8. [07-remediation-plan.md](07-remediation-plan.md)

## Working Rules

- Every claim needs an evidence reference.
- Every exclusion must be written down explicitly.
- Every tracked file must move to a terminal status in
  [inventory/file-coverage.tsv](inventory/file-coverage.tsv).
- Every material cross-file seam must be logged in
  [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md).
- Findings go in [05-findings-register.md](05-findings-register.md),
  not in ad hoc notes.
- Prior audits are baselines to challenge, not sources of truth.

## Directory Layout

- [00-plan.md](00-plan.md): repo-specific audit plan.
- [01-tracker.md](01-tracker.md): master tracker and closure state.
- [02-protocol.md](02-protocol.md): execution rules and evidence discipline.
- [03-journeys.md](03-journeys.md): mandatory end-to-end journeys.
- [04-reconciliation.md](04-reconciliation.md): comparison passes.
- [05-findings-register.md](05-findings-register.md): findings ledger.
- [06-exclusions-register.md](06-exclusions-register.md): exclusions and blocked scope.
- [07-remediation-plan.md](07-remediation-plan.md): prioritized remediation sequence.
- [evidence/](evidence/): evidence log, tree observations, and seam map.
- [inventory/](inventory/): generated repo snapshot and file coverage inventory.

## Inventory Refresh

Refresh inventory from the current checkout with:

```sh
./docs/audit-2026-05-02/inventory/generate.sh
```

## Scope Reminder

This workspace audits the local repository snapshot. Hosted GitHub
controls, live third-party services, and remote infrastructure can only
be marked verified when the repo itself contains proof or a live probe
is executed and logged as evidence.
