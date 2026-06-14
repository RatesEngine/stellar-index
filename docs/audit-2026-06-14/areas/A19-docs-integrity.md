# A19 — Docs integrity (D9 doc-drift)

**Audit date:** 2026-06-14
**Scope:** CLAUDE.md, docs/adr/** (40 ADRs incl. 0038), openapi/stellar-index.v1.yaml,
docs/reference/**, README.md, docs/architecture/** (key narrative docs),
docs/operations/runbooks/**.
**Method:** READ-ONLY. CLAUDE.md claims checked against actual code (file/function/flag
presence); ADR supersede-chain + index consistency; OpenAPI ↔ handler reconciliation;
generated-reference staleness; runbook↔alert coverage; brand consistency; `last_verified`
freshness.

**Files / artifacts read:** 41
(CLAUDE.md; README.md; openapi/stellar-index.v1.yaml; docs/reference/api/{stellar-index.v1.yaml,
README.md, index.html}; docs/adr/{README.md, 0029, 0034, 0035, 0036, 0037, 0038};
docs/architecture/{repo-hygiene-plan.md + 50 frontmatter scans}; scripts/ci/lint-docs.sh;
scripts/dev/docs-api.sh refs; Makefile; deploy/monitoring/rules/*.yml + configs/prometheus/rules.r1/*.yml;
docs/operations/runbooks/* inventory; docs/protocols/*; verified ~30 internal/ + cmd/ + pkg/ code
paths cited by CLAUDE.md.)

---

## Severity counts

| Severity | Count |
| -------- | ----- |
| Critical | 0 |
| High     | 3 |
| Medium   | 5 |
| Low      | 6 |
| CORRECT (verified-true claims) | see CORRECT list |

---

## Findings

| ID | Sev | Area | Finding | Evidence |
| -- | --- | ---- | ------- | -------- |
| A19-01 | High | docs/reference freshness | **Generated API reference is STALE — missing the entire ADR-0038 explorer section.** `openapi/stellar-index.v1.yaml` (source, 73 paths, last touched commit `b0e99c0a` 2026-06-14 09:31 "document the explorer endpoints") vs `docs/reference/api/stellar-index.v1.yaml` (generated copy, **66 paths**, last regenerated `24527aa3` 2026-06-12). The 7 missing paths are the explorer set: `/ledgers`, `/ledgers/{seq}`, `/ledgers/{seq}/transactions`, `/tx/{hash}`, `/operations`, `/contracts/{contract_id}`, `/search` (+ the `explorer` tag). `make docs-api` was not re-run after the source edit. | `diff openapi/stellar-index.v1.yaml docs/reference/api/stellar-index.v1.yaml` (removes lines 50-51 tag + 3376-3547 explorer block); path counts 73 vs 66. |
| A19-02 | High | docs/reference + CI claim | **The published rendered reference (`index.html`) serves the stale spec, and the "CI verifies in sync" claim is false in practice.** `docs/reference/api/index.html` loads the colocated `stellar-index.v1.yaml` (the 66-path stale copy) — so anyone reading docs.stellarindex.io sees 7 fewer endpoints than ship. `docs/reference/api/README.md` asserts "CI verifies the rendered output is in sync with the spec on every PR that touches either side" — but the out-of-sync state exists on `main`, so either the check did not run, is non-blocking, or only the source side was edited without the regen step. | `index.html` grep → loads `stellar-index.v1.yaml`; README.md banner lines 16-18; A19-01 proves the desync. |
| A19-03 | High | OpenAPI ↔ reality / ADR-0038 self-contradiction | **`/v1/accounts/{g_strkey}/transactions` and `/operations` are implemented but UNDOCUMENTED in OpenAPI — and ADR-0038 line 171 claims "OpenAPI (shipped): all explorer endpoints documented."** Handlers live in `internal/api/v1/explorer_accounts.go` (`handleAccountTransactions`/`handleAccountOperations`, registered `server.go:1032-1033`). Source spec has only `/account/*` (singular, API-key mgmt); no `accounts/{...}` path exists. The ADR's "Phase B v1 shipped" section (lines 153-158) documents the endpoints as live, but its own "OpenAPI (shipped)" line is then false for exactly these two. | `grep "accounts/{" openapi/...` → none; `explorer_accounts.go`; ADR-0038:153-158 vs :171. |
| A19-04 | Medium | docs/adr index | **ADR-0038 is missing from the ADR README index table.** The index in `docs/adr/README.md` ends at 0037; ADR-0038 (Accepted, 2026-06-14) exists as a file and is referenced by the OpenAPI spec + handlers but is not listed. The README rule #3 requires every ADR (even Planned) be in the index. | `grep 0038 docs/adr/README.md` → no match; file `docs/adr/0038-network-explorer.md` exists, status Accepted. |
| A19-05 | Medium | CLAUDE.md trap drift | **`MustI128()` cited in the SEP-41 trap does not exist as a symbol.** CLAUDE.md "Things that will surprise you" says "Type-test before `MustI128()`." No `MustI128` function exists in `internal/scval` or anywhere in the repo (only a comment echo in `internal/sources/sep41_transfers/decode.go:44`). The real helper is `scval.AsAmountFromI128` (`internal/scval/scval.go:309`); the type-test behaviour the trap describes IS correct (`decode.go:64+` tests `ScValTypeScvI128`), but the named symbol is fictional and will mislead a grep. | agent verify item 8; `scval/scval.go:309`. |
| A19-06 | Medium | runbook↔alert drift | **Five self-named runbooks are dead weight — a live alert by that name exists but its `runbook_url` points at a different (shared) runbook.** `aggregator-supply-refresh-never-initialized.md` (alert points to `supply-snapshot-never-initialized.md`); `anomaly-freeze-sustained.md` (→ `anomaly-freeze-engaged.md`); `external-poller-error-rate-high.md` (→ `external-poller-stale.md`); `node-root-disk-full.md` + `node-root-disk-warning.md` (both → `redis-write-blocked-disk-full.md`). CI §9 only checks the URL target exists, not that the alert's self-named runbook is the one referenced — so this drift is invisible to CI. | agent PART B Class-1; rules `*.yml` runbook_url fields; `scripts/ci/lint-docs.sh` §9. |
| A19-07 | Medium | runbook orphans | **~12 alert-shaped runbooks are referenced by no alert (orphans), incl. the 6 SLO-burn runbooks.** `slo-{availability,latency}-burn-{fast,medium,slow}.md` exist but the SLO burn alerts in `slo.yml` all `runbook_url` to `api-5xx.md`/`api-latency.md`; plus fully-orphaned `fx-history-missing.md`, `minio-metrics-403.md`, `prometheus-tsdb-corruption.md`, `sdex-gap-detected.md`, `ingestion-lag.md`. CLAUDE.md frames the contract one-directionally ("every alert references a runbook"); the reverse (orphan runbooks) is real and unchecked. | agent PART B Class-2; no `lint-docs.sh` orphan check. |
| A19-08 | Medium | brand consistency | **8 residual `rates-engine`/`RatesEngine`/`ratesengine-*` references outside the sanctioned provenance areas.** Most are legitimate-but-debatable (dev-repo path `RatesEngine/stellar-index`, GCP service-account filename); two are stale prose: `docs/architecture/coverage-matrix.md:32` ("`github.com/RatesEngine/rates-engine`, binaries `cmd/ratesengine-*`" — wrong module + binary names, doc `last_verified: 2026-06-13`), and `docs/architecture/infrastructure/archival-node-spec.md:38` ("Rates-engine indexer" label paired with the new `stellarindex-indexer` binary). `docs/blog/2026-05-07-*` uses `RatesEngine/rates-engine` GitHub link (dated post — borderline provenance). | `grep -inE "rates[ -]?engine"` over docs/README/CLAUDE/openapi minus provenance. |
| A19-09 | Low | CLAUDE.md repo map | **`internal/xdrjson/` is present but undocumented in the CLAUDE.md repo map.** New package (ADR-0038 XDR→JSON explorer decode: `helpers.go`, `operation.go`, `participants.go`). The repo-map `internal/` list omits it. | `ls internal/xdrjson`; CLAUDE.md repo map. |
| A19-10 | Low | CLAUDE.md flag form | **Catch-up command flag form drift:** CLAUDE.md writes `projector-replay -source <name> -from <ledger>` (single-dash); help text + `find_data_gaps.go:240` emit `--source`/`--from` (double-dash). Go's `flag` accepts both, so functionally harmless — cosmetic. | agent verify item 5. |
| A19-11 | Low | CLAUDE.md locality | **`completeness_snapshots` table-name does not live in the package CLAUDE.md implies.** Invariant 7 / ADR-0033 prose attributes the authoritative verdict to `internal/completeness`; the package computes the verdict logic but the literal table write is in `internal/storage/timescale/completeness_snapshots.go` + `cmd/stellarindex-ops/compute_completeness.go` (migration 0052). Architectural claim holds; locality wording is slightly off. | agent verify item 13. |
| A19-12 | Low | docs/reference freshness banner | **`docs/reference/api/README.md` `last_verified: 2026-05-06`** is >30 days old (under the 90-day warn threshold, so not yet CI-flagged), and predates the explorer regen that never happened — a soft signal corroborating A19-01. | README.md frontmatter; `lint-docs.sh` §6 thresholds (90 warn / 180 fail). |
| A19-13 | Low | ADR-0038 lake snapshot | ADR-0038 embeds a dated row-count table ("Rows 2026-06-14": ledgers 63M, operations 23.4B, `ledger_entry_changes` empty). It's labelled with its capture date so not drift today, but it's an inline data snapshot that will silently age (no `last_verified` on ADRs by design — ADRs are immutable, so this is acceptable but worth noting it will never be refreshed). | ADR-0038:25-32. |
| A19-14 | Low | CLAUDE.md cmd note | CLAUDE.md "six in total" binaries is exactly correct today, but it's a hard-coded count that will drift the moment a 7th binary lands — prefer "the binaries below" phrasing. Informational. | `ls cmd/` = 6, matches. |

---

## CORRECT — verified-true claims (no action)

These CLAUDE.md / ADR / OpenAPI claims were checked and hold:

- **cmd/ six binaries** — exact match, no extras (`stellarindex-{indexer,aggregator,api,ops,migrate,sla-probe}`).
- **All 32 documented `internal/` packages exist** (only undocumented extra is `xdrjson`, A19-09).
- **`pkg/` surface** — only `pkg/client/`; `pkg/client/types.go` exists; no `pkg/types` dir.
- **Invariant 7 anchors exist** — `internal/projector/registry.go::buildSource` (line 95) and `internal/pipeline/sink.go::IsProjectedEvent` (line 269).
- **No bespoke `<source>-backfill` subcommands remain** — `projector-replay` registered (`cmd/stellarindex-ops/main.go:166`); the surviving `*-backfill` commands are infra/lake (`ch-backfill`, `census-backfill`), not per-source.
- **Band ContractCallDecoder** — interface at `internal/dispatcher/dispatcher.go:143`, match-by-(contract_id, function_name); `internal/sources/band/dispatcher_adapter.go` implements it.
- **`events.Event.OpArgs`** — field present (`internal/events/event.go:83`); Redstone/Band tx-arg plumbing claim holds.
- **soroswap_router log-only, excluded from IsProjectedEvent** — dir exists; explicitly named in the false-branch comment `sink.go:282` per ADR-0032.
- **Stablecoin proxy map** — `internal/aggregate/stablecoin.go:26-36` matches CLAUDE.md (USDT/USDC/DAI/PYUSD/USDP→USD, EURC/EUROC/EUROB→EUR, MXNe→MXN).
- **currency seed** — `internal/currency/data/seed.yaml` + `//go:embed` at `internal/currency/verified.go:11`.
- **external Connector framework + Registry** — `internal/sources/external/framework.go:193` (`Connector`), `registry.go:31` (`Registry` map).
- **`childgate` is a real package** (ADR-0035 gating) — `internal/sources/childgate`, wired via `internal/pipeline/gated_registry.go` + `dispatcher.go`.
- **`/v1/assets/{slug}` dual-shape** — `handleAssetGet` (`assets.go:1187`) dispatches `GlobalAssetView` (`assets_global.go:29`) vs `AssetDetail` (`assets.go:55`); matches the trap doc.
- **All other explorer paths ARE documented + handled** — `/ledgers`, `/ledgers/{seq}`, `/operations`, `/tx/{hash}`, `/contracts/{c}`, `/search`, `/network/stats`, `/ledger/tip`, `/ledger/stream` all present in source spec AND registered (only the generated-reference copy is stale, A19-01).
- **Every Prometheus alert has a `runbook_url` and every referenced runbook file exists** — ~110 alerts across 21 rule files (both `deploy/monitoring/rules/` and `configs/prometheus/rules.r1/`); zero alerts with a missing runbook target. CLAUDE.md "every alert references a runbook or it's a CI failure" holds for the forward direction (A19-06/07 are reverse/quality issues, not the CI contract).
- **CI lint exists and enforces alert→runbook** — `scripts/ci/lint-docs.sh` §9 (runbook_url target exists), §10 (alert in catalog), §11 (metric freshness), §12 (catalog links resolve).
- **ADR supersede chain is correct** — 0029→superseded-by-0034 (both metadata + index agree); 0036→superseded-by-0037 (both agree); 0036/0037 dated 2026-06-12, 0035 2026-06-12, all Accepted/Superseded consistent. ADR README "Brand note" correctly carves out 0001-0035 immutability.
- **ADR-0038 matches shipped explorer code** — invariants (i128, explorer-reads-ClickHouse via `internal/storage/clickhouse`, `internal/xdrjson` centralised decode, no Horizon) all verified against code; "Status of build" reflects reality (Phase A read surface live, Phase B v1 shipped, Phase C substrate shipped). Only the embedded "OpenAPI all documented" line is wrong (A19-03).
- **README brand consistency** — title "Stellar Index", stellarindex.io throughout, single provenance mention ("formerly Rates Engine, renamed 2026-06-12").
- **`docs/protocols/` exists** (aquarius/blend/defindex/phoenix/soroswap + README) and CLAUDE.md cites it (line 16).
- **`last_verified` freshness CI is real** (`lint-docs.sh` §6, 90 warn / 180 fail); no architecture/operations doc is currently past the 180-day fail line — most-stale is `chaos-suite-design-note.md`/`contract-schema-evolution.md`/several ansible notes at 2026-05-02/03 (~42 days, under warn).
- **Comet open-case consistency** — ADR-0035 "Open: Comet" (lines 209+, checklist :235) matches CLAUDE.md's "one open case" framing; no contradiction.

---

## Recommended remediation order

1. **A19-01/A19-02** (run `make docs-api`, verify CI sync gate actually blocks) — the live published reference is missing 7 shipped endpoints.
2. **A19-03** (document `/v1/accounts/{g}/transactions|operations` in OpenAPI; then A19-01 fix regenerates correctly) — also corrects the ADR-0038 false claim by making it true.
3. **A19-04** (add ADR-0038 row to the index table).
4. **A19-05, A19-08** (fix the `MustI128` trap wording → `AsAmountFromI128`; fix the two stale brand prose lines in coverage-matrix.md + archival-node-spec.md).
5. **A19-06/A19-07** (reconcile self-named runbooks with their alerts, or delete the dead-weight files; optionally add an orphan-runbook CI check).
6. Low items as cleanup.
