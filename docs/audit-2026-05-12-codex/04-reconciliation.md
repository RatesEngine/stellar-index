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

- Cold execution evidence now spans `CMD-0007` through `CMD-0103`,
  `EV-0005` through `EV-0101`, `R1-0001` through `R1-0018`, and
  `XFI-0001` through `XFI-0051`.
- Findings `F-1201` through `F-1259` remain evidence-backed and are
  mapped to remediation rows `R-1201` through `R-1257`; `F-1202`,
  `F-1212`, `F-1217`, `F-1235`, `F-1238`, `F-1245`, `F-1246`,
  `F-1247`, `F-1253`, `F-1254`, and `F-1231` are now marked `fixed`
  on the current shared `HEAD`.
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
  open. Wave 9 then narrowed three more findings without closing them:
  the indexer now passes CoinMarketCap IDs, but ops verification still
  does not and the poller has no committed ID-mode response fixture;
  FX freshness is now anchored to trade time, but the targeted
  integration still fails on NUMERIC text-shape drift and zero-value
  freshness semantics remain contradictory; first-login callbacks now
  reload the winning user instead of preserving the old conflict path,
  but the speculative loser account can still remain orphaned.
- `CMD-0100` revalidated the settled `7c9e79ae...` workspace against
  those Wave 5 changes, ran the targeted webhook/external-source test
  set, and recorded that `F-1248` survives the attempted remediation.
- `CMD-0101` revalidated the later `27343a46...` workspace after the
  Wave 7 CI/signup commit. It closes `F-1231`, records that `F-1207`
  narrowed but remains open, and records that `F-1218` narrowed but
  remains open because unverified plaintext-key issuance and
  tracker-nil duplicate minting still exist.
- `CMD-0102` revalidated the Wave 8 key-policy work and preserved
  `F-1226` as open because cache-hit requests still lose policy fields,
  monthly quotas remain unenforced, and `TouchUsage` still has no
  production caller.
- `CMD-0103` reran docs lint after the committed Wave 8 middleware/test
  files entered tracked scope and restored literal inventory parity at
  `tracked=1872`, `rows=1872`.
- `CMD-0104` reconciled the settled `82a6052c...` Wave 9 commit,
  verified package-level CMC/timescale/dashboard-auth tests, reran the
  targeted FX integration, and recorded why `F-1237`, `F-1251`, and
  `F-1255` remain open.
- `CMD-0105` audited the later Wave 10 shared-workspace remediation
  slice. It records that deploy-migration coupling is improved but not
  yet proven runnable with the workflow's `ansible-core` install,
  contribution `volume_usd` writes still mismatch the filtered trade
  set, the dashboard-key cap CTE repeats the prior MVCC race class, the
  Redis-less usage reader can still nil-deref on `/v1/account/usage`,
  and usage docs remain internally inconsistent despite source OpenAPI
  edits.
- `CMD-0106` reviewed the follow-on CI action-pinning lint and the
  revised contribution-volume implementation. The new PR-diff lint
  narrows `F-1216` but still reports twelve existing mutable third-party
  action tags; the contribution rewrite now carries per-trade USD
  attribution through the filter chain and should be re-checked for
  `F-1242` once that workspace settles.
- `CMD-0107` reconciled the settled Wave 11 state, marked `F-1233`
  and `F-1242` fixed on current committed code, narrowed `F-1249` after
  the first webhook producer landed, and surfaced new high-severity
  aggregator finding `F-1260` for stale pre-filter USD-volume gating.
- `CMD-0108` audited Wave 13's divergence webhook producer and the
  later uncommitted supply-ledger attempt. `F-1249` now narrows to the
  two incident event families only, while `F-1236` remains open because
  the workspace fix does not compile against `timescale.Cursor`.
- Closure caveat: the TSV remains the per-file coverage control. Rows
  with `todo` still require terminal file-level review before claiming
  literal every-file closure. `EV-0063` documented the scope drift when
  the repository advanced from the original `80c57e...` anchor to
  current `60c8a2e4...`; `EV-0078` resolves the first count mismatch,
  `EV-0097` preserves the refresh back to `1,870` tracked rows,
  `EV-0101` restores parity after the two committed key-policy files
  increased tracked scope to `1,872`, and the latest inventory refresh
  now lands at `1,874` tracked non-audit rows after
  `scripts/ci/lint-actions-pinning.sh` and
  `internal/customerwebhook/fanout.go` entered scope.
  Current findings remain source/R1 verified and not imported from prior
  audits, but final whole-repo closure still requires terminal review
  status across the refreshed TSV. The current inventory roll-up is
  `done=102`, `in_progress=51`, `todo=1721`.
