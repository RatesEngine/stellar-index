# Contributing checklists

Copy-followable checklists for the recurring "add an X" tasks. Each names the exact
files to touch, the existing helper to **reuse** (so you don't rebuild what exists —
see `/CAPABILITY-INVENTORY.md`), the guard that catches mistakes, and the "done when."

CLAUDE.md's "Common task recipes" section points here rather than duplicating steps.

| Task | Checklist |
|------|-----------|
| Add an on-chain Soroban source | [add-onchain-source.md](add-onchain-source.md) |
| Add a CEX / FX connector | [add-cex-connector.md](add-cex-connector.md) |
| Add an API endpoint | [add-api-endpoint.md](add-api-endpoint.md) |
| Add a Prometheus metric | [add-metric.md](add-metric.md) |
| Add a migration | [add-migration.md](add-migration.md) |
| Add a supply observer | [add-supply-observer.md](add-supply-observer.md) |

**Before writing any new utility, check `/CAPABILITY-INVENTORY.md`** — most primitives
(SSRF guard, HMAC sign, rate limit, cache key, SCVal/i128 decode, VWAP, XDR decode,
email, API-key hash) already exist. Rebuilding them is the #1 source of maintenance debt.
