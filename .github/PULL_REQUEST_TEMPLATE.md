<!--
Thanks for the PR. Please fill in every section below. If a section
is truly not applicable, say so explicitly — never delete a section.

The full policy lives in docs/engineering-standards.md.
-->

## Summary

<!-- One paragraph: what and why. -->

## Type of change

- [ ] Bug fix (regression test included?)
- [ ] New feature (ADR needed?)
- [ ] Refactor (no behaviour change — confirm with tests)
- [ ] Documentation only
- [ ] Operational / CI / tooling

## Related issues

<!-- Link any issues this closes or references. -->

- Closes #
- Related to #

## Definition of Done checklist

### Mechanical (CI enforces; tick when green locally)

- [ ] `make lint` passes.
- [ ] `make test` passes with `-race`.
- [ ] Coverage on changed packages did not decrease.
- [ ] No `TODO` / `FIXME` without a linked issue.
- [ ] Every new exported symbol has a Godoc comment.
- [ ] If OpenAPI changed, reference regenerated (`make docs-api`).
- [ ] If config changed, reference regenerated (`make docs-config`).

### Judgement (reviewer checks)

- [ ] Docstrings explain **why**, not **what**.
- [ ] Public API (`pkg/*`) free of `interface{}` / `any`.
- [ ] New goroutines take `context.Context` and honour `ctx.Done()`.
- [ ] `time.Now()` only via injected clock.
- [ ] If architectural, an ADR is proposed/updated.
- [ ] If new alert, a runbook exists in `docs/operations/runbooks/`.
- [ ] Docs touched by this change refreshed (or `last_verified`
      bumped if content still accurate).

## Invariant check

Does this PR touch any of these? (check if yes and explain below)

- [ ] i128 / `*big.Int` handling (ADR-0003)
- [ ] Horizon / ingestion architecture (ADR-0001)
- [ ] Storage backend (ADR-0002)
- [ ] Public `pkg/*` types (SemVer implications)
- [ ] Validator key material (ADR-0004)

Explanation:

<!-- If you ticked any box above, explain how you preserved the
invariant. -->

## How this was tested

<!-- Unit / integration / manual / fixture-based. Be specific. -->

## Risk assessment

- **Blast radius if this ships broken:**
- **Rollback plan:**

## Notes for reviewer

<!-- Anything that'll save them time. Points you want eyes on.
     Areas where you're uncertain. -->
