# Reconciliation And Multi-Pass Checks

This file is the audit quality gate. It must be updated after each
major pass and again at closure.

## Initial Scope Reconciliation

Generated from `git ls-files` before this audit directory was added.

| Top-Level Area | Tracked Files | Owned Workstreams |
| --- | ---: | --- |
| `internal` | 651 | W06-W15, W17-W20, W24 |
| `docs` | 421 | W01-W02, W17-W23 |
| `web` | 199 | W15-W17, W19, W22-W23 |
| `configs` | 163 | W03-W05, W17-W18 |
| `test` | 89 | W08-W12, W20 |
| `migrations` | 57 | W12 |
| `deploy` | 41 | W03, W05, W17-W18 |
| `cmd` | 36 | W03, W07, W14-W15, W18-W20 |
| `scripts` | 27 | W03, W11, W18, W20, W23 |
| `examples` | 14 | W15, W21, W23 |
| `.github` | 13 | W01, W03-W04, W20 |
| `pkg` | 10 | W15 |
| `docker` | 7 | W03, W05, W18 |
| root files | 24 | W01-W05, W20-W21 |
| `openapi` | 1 | W15, W21, W23 |

## Second-Pass Checklist

| Check | Status | Notes |
| --- | --- | --- |
| Every top-level area has at least one workstream owner | complete | See table above |
| Every tracked file is represented in inventory | complete | `EV-0078` refresh/merge restored 1,869 tracked rows for 1,869 current files |
| Every Go package is represented in a workstream | todo | Compare `go list ./...` to W06-W20 |
| Every binary has a journey or operator flow | todo | `cmd/*` to J45-J56 and API/data journeys |
| Every migration has a store/API/test review path | todo | W12 and J45 |
| Every workflow has script/artifact/deploy review path | todo | W03, W20, W23 |
| Every frontend route class has API/data review path | todo | W16 and J37-J44 |
| Every monitoring rule has metric and runbook review path | todo | W17 |
| Every public docs family has docs-truth review path | todo | W21 |
| Every competitive product surface has owner | todo | W22 |

## Third-Pass Checklist

| Check | Status | Notes |
| --- | --- | --- |
| No file inventory rows remain `todo` | todo | Closure gate |
| No file inventory rows remain `in_progress` | todo | Closure gate |
| Every `blocked` row has exclusion entry | todo | Closure gate |
| Every `excluded` row has exclusion entry | todo | Closure gate |
| Every finding has evidence refs | todo | Closure gate |
| Every evidence ref has primary source anchors | todo | Closure gate |
| Every journey has evidence or explicit exclusion | todo | Closure gate |
| Every severity was challenged once | todo | Adversarial review |
| Docs-only claims were removed or marked unverified | todo | Cold-audit rule |
| R1 checks are complete or explicitly excluded | todo | Runtime gate |
| Prior audit findings were not imported | complete | Plan contains no prior findings |
| Plan covers generic market-data parity | complete | W11, W15, W16, W22 |
| Plan covers Stellar-native superiority | complete | W06, W08-W10, W14, W18, W22 |

## Drift Searches To Run

Record results in [evidence/commands.md](evidence/commands.md).

```sh
git ls-files | sort
git status --short
go list ./...
go test ./...
make verify
rg -n "TODO|FIXME|panic\\(|t\\.Skip|Skip\\(|nolint|int64|Horizon|horizon|TODO\\(" .
rg -n "RATE|REDIS|DATABASE|JWT|SECRET|TOKEN|KEY|CORS|TRUST|PROXY" internal cmd configs deploy web
rg -n "Name:" internal/obs deploy/monitoring configs/prometheus
rg -n "CREATE TABLE|CREATE INDEX|CREATE MATERIALIZED|ALTER TABLE" migrations
```

## Reconciliation Notes

Add dated notes here during execution. Do not mark closure gates complete
without evidence IDs.

### 2026-05-11 Plan Second Pass

- `EV-0003` verifies the generated inventory row count matches the
  tracked file count: `1,747` rows for `1,747` tracked files in scope.
- All top-level tracked areas have workstream ownership in the initial
  scope table.
- The execution checklist remains open because the audit itself has not
  been performed.

### 2026-05-11 Plan Third Pass

- `EV-0004` verifies every inventory row remains `todo`, preventing the
  plan from falsely claiming completed file review.
- The findings register is intentionally empty and imports no prior
  findings.
- The plan adds explicit gates for generic market-data parity,
  Stellar-native depth, R1 runtime checks, frontend/product surfaces,
  generated artifact drift, and cross-file interactions.

### 2026-05-12 Execution Reconciliation

- Cold execution evidence now spans `CMD-0007` through `CMD-0100`,
  `EV-0005` through `EV-0098`, `R1-0001` through `R1-0018`, and
  `XFI-0001` through `XFI-0051`.
- Findings `F-1201` through `F-1259` remain evidence-backed and are
  mapped to remediation rows `R-1201` through `R-1257`; `F-1202`,
  `F-1212`, `F-1217`, `F-1235`, `F-1238`, `F-1245`, `F-1246`,
  `F-1247`, `F-1253`, and `F-1254` are now marked `fixed` on the
  current shared `HEAD`.
- Live R1 checks covered process state, timers, firewall/listeners,
  external reachability, host capacity, Prometheus alerts, config
  snippets, Caddy drift, API/history/SSE behavior, and stablecoin
  price parity.
- The audit found additional areas beyond the initial plan while
  executing: Docker migration-image bootstrapping, SSE server
  timeouts, CDN verification script drift, backfill depth, PR-only CI
  under unprotected main, stablecoin price cross-surface mismatch,
  SDEX legacy-claim backfill loss, oracle partial-coverage drift,
  external streamer parse-error observability, supply component
  freshness, CMC identity ambiguity, API Redis optionality,
  ops WASM progress-counter crash behavior, Docker Go toolchain
  drift, migration-index drift between shipped SQL and the
  operator-facing schema inventory, contribution-history rows that
  persist without their advertised USD-volume field, and classic
  asset registry metadata that freezes after the first same-process
  observation. The moving `HEAD` also surfaced newly landed dashboard
  webhook security/correctness seams: stored signing-key material is
  misdescribed as hash-only, URL validation leaves an SSRF-capable
  outbound worker path, quota enforcement races under concurrent create
  requests, and the callback queue currently has no production event
  producer at all. The follow-on new-runtime pass also found raceable
  freeze-event open-row dedupe, a verified failing integration path in
  FX-derived `usd_volume` freshness handling, a Redis ACL lockdown seam
  where both the username handoff and the actual key-pattern allow-list
  are out of sync with current binaries, a dashboard first-login
  provisioning race that can strand orphan accounts when multiple valid
  callbacks land for one new email, and two dashboard-key follow-ons:
  stale customer-facing/UI/OpenAPI budget semantics after the security
  fix, plus a still-raceable 25-active-key/account quota. The Wave 5
  remediation commit then closed the CEX parse-metric gap, the
  duplicate webhook-delivery claim race, and both Redis ACL lockdown
  drift findings. It also attempted to close the ten-webhook/account
  quota race, but the new count-then-insert CTE is still not serializing
  concurrent creates under normal PostgreSQL MVCC, so `F-1248` remains
  open.
- `CMD-0100` revalidated the settled `7c9e79ae...` workspace against
  those Wave 5 changes, ran the targeted webhook/external-source test
  set, and recorded that `F-1248` survives the attempted remediation.
- Closure caveat: the TSV remains the per-file coverage control. Rows
  with `todo` still require terminal file-level review before claiming
  literal every-file closure. `EV-0063` documented the scope drift when
  the repository advanced from the original `80c57e...` anchor to
  current `7c9e79ae...`; `EV-0078` resolves the first count mismatch,
  and `EV-0097` preserves the later refresh back to `1,870` tracked rows.
  Current findings remain source/R1 verified and not imported from prior
  audits, but final whole-repo closure still requires terminal review
  status across the refreshed TSV. The current inventory roll-up is
  `done=99`, `in_progress=41`, `todo=1730`.
