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

- Cold execution evidence now spans `CMD-0007` through `CMD-0121`,
  `EV-0005` through `EV-0119`, `R1-0001` through `R1-0018`, and
  `XFI-0001` through `XFI-0052`.
- Findings `F-1201` through `F-1260` remain evidence-backed and are
  mapped to remediation rows `R-1201` through `R-1258`; `F-1202`,
  `F-1203`, `F-1212`, `F-1213`, `F-1217`, `F-1220`, `F-1223`, `F-1231`,
  `F-1233`, `F-1235`, `F-1238`, `F-1242`, `F-1245`, `F-1246`,
  `F-1247`, `F-1249`, `F-1253`, `F-1254`, and `F-1260` are now marked
  `fixed` on the current shared `HEAD` or verified live R1 state.
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
- `CMD-0109` audited the current Stripe remediation attempt. The new
  bridge helper is dormant in production wiring, untested in the
  webhook suite, and still leaves Postgres dashboard API-key limits
  untouched, so `F-1219` remains open.
- `CMD-0110` reconciled settled `HEAD=c82c1602...` plus the latest
  supply-worktree follow-up. Wave 15 adds Stripe subscription-event
  handling and a supply stale-component rejection gate, but neither
  closes the findings yet: production API wiring still never sets
  `StripeWebhookConfig.Platform`, and the supply freshness field is not
  populated by production storage readers. The current
  `ClassicComputer`/`SEP41Computer` worktree changes only thread a field
  that remains zero from real readers.
- `CMD-0111` reconciled settled `HEAD=65197ec0...`. Wave 16 commits the
  classic/SEP41 freshness-field threading plus a new GitHub Actions
  SHA-pinning operations guide, raising tracked non-audit scope to
  `1,875`. `F-1236` remains open because the committed storage-backed
  readers still never populate `MinComponentLedger`; the refresher gate
  can only reject stale snapshots when an upstream reader finally emits
  a non-zero freshness signal.
- `CMD-0112` reviewed the next live `F-1236` workspace slice. Classic
  supply now has an uncommitted `MinClassicComponentLedger` query and
  reader plumbing, and the targeted supply/timescale/command tests pass,
  but the remediation is still partial: SEP41 and XLM freshness producers
  remain absent, and the classic reader intentionally degrades query
  errors back to the zero-value bypass.
- `CMD-0113` reconciled settled `HEAD=6819e7dc...` plus the next live
  SEP41 producer attempt. Wave 17 commits the classic storage producer,
  but the current SEP41 workspace does not build in `./internal/supply`
  because the fake SEP41 test store no longer satisfies the expanded
  interface. XLM freshness production is still absent too, so `F-1236`
  remains open.
- `CMD-0114` reconciled settled `HEAD=fb0b3073...` plus the next live
  Stripe webhook workspace. Wave 18 commits the SEP41 freshness producer
  and restores the targeted supply/timescale/command test set to green,
  but the new `invoice.paid` Stripe handler remains a dormant side path
  because production API wiring still never sets `StripeWebhookConfig.Platform`.
  `F-1219` therefore remains open.
- `CMD-0115` materially changed the live R1 posture: the public metrics
  exposure is closed, internal-service public reachability is largely
  reduced under active nftables, three evidence timers are now enabled,
  and the residual open issues are narrower captive-core ingress drift on
  `11726/tcp` plus the still-disabled `sla-probe.timer`.
- `CMD-0116` narrowed `F-1207` to hosted GitHub control posture. The web
  apps now pin patched Next.js versions, npm Dependabot ecosystems exist,
  and high-severity `pnpm audit` runs are clear of high/critical advisories;
  repository vulnerability and Dependabot alerts remain disabled.
- `CMD-0118` split two market-data threads cleanly: `F-1213` is fixed in
  current source/tests, while `F-1225` remains open as a live R1
  source/runtime mismatch for since-inception USD history fallback.
- `CMD-0119` preserved the two highest-priority remaining integrity/security
  findings in that slice: classic-asset registry freshness/count semantics
  still freeze after first same-process observation, and webhook signing-key
  storage remains materially more recoverable than the docs/prose imply.
- `CMD-0120` narrowed `F-1226` by closing cache-hit policy shedding, but
  monthly quota enforcement and production `TouchUsage` propagation remain
  absent.
- `CMD-0127` deepened `F-1226`: neither per-key nor per-account monthly
  quota fields have a request-path consumer, `TouchUsage` still has no
  production caller, and `/v1/account/usage` currently reads credential-local
  counters rather than a real account aggregation.
- `CMD-0128` narrowed `F-1218`: the current main binary does not mint signup
  keys through a Redis-less tracker-nil path because the signup account store
  is absent too and the handler returns `503`. The unverified plaintext-key
  issuance finding remains high severity on the healthy Redis-backed path.
- `CMD-0129` rechecked `F-1226` against the new shared-workspace monthly
  quota patch. Positive per-key quota propagation/enforcement is now in
  flight, but default/inherited quota semantics, account override usage,
  admission atomicity near cap, `TouchUsage`, and account-level aggregation
  remain unresolved.
- `CMD-0130` folded the new highest-priority auth/quota/signup reviews back
  into the per-file control ledger and refreshed the snapshot/area metadata.
- `CMD-0131` resolved stale audit drift around `F-1219`: the platform bridge
  is now genuinely wired in production source, but the finding remains open
  because paid upgrades still do not mutate existing dashboard-created
  Postgres API keys and bridge failures remain best-effort.
- `CMD-0132` restores file-ledger parity after the monthly-quota middleware
  moved from workspace-only remediation into tracked scope. The refreshed
  audit TSV now covers `1,884` tracked non-audit files exactly and the current
  roll-up is `done=105`, `in_progress=67`, `todo=1712`.
- `CMD-0133` narrows the live `F-1226` touch-usage thread: current workspace
  code now wires a Redis-debounced Postgres touch path and the focused test set
  passes, but the files remain untracked workspace remediation and the inline
  post-handler latency model plus quota/account-aggregation gaps still block
  closure.
- `CMD-0134` closes the immediate cross-file ACL question for that touch path
  in the current workspace: the new `touch:apikey:*` Redis family is also
  present in the rendered Redis ACL template. That coherence check is useful,
  but both sides are still moving workspace edits rather than landed closure.
- `CMD-0121` preserved `F-1228` as a source/live drift issue: the SSE deadline
  fix exists in code, yet the public R1 stream still terminates around the
  former 30-second cutoff.
- `CMD-0122` surfaced new high-severity migration finding `F-1261`.
  Migration `0030_asset_supply_history_unique_constraint` fails against the
  compressed hypertable created by `0005`, fresh integration bootstrap dies
  before store-level scenarios run, and live R1 is still at schema version
  `28` with the old index shape. That same blocker downgrades apparent
  closure of `F-1248` and `F-1257` to `needs_evidence`: their new advisory-lock
  code paths and concurrent tests are present, but the closure proof cannot
  execute until `F-1261` is cleared.
- `CMD-0124` sharpened `F-1236` rather than changing its status. Classic and
  SEP41 freshness producers are now real, but native XLM still has no
  freshness producer and its live-observation miss path intentionally falls
  back to static reserve config while still stamping the snapshot at the
  freshest cursor ledger. Because that path emits `MinComponentLedger=0`, the
  stale-component gate is guaranteed to skip the least-proven provenance path.
- `CMD-0125` folds that supply pass back into the file-level control ledger;
  parity remains exact at `tracked=1882`, `rows=1882`.
- Closure caveat: the TSV remains the per-file coverage control. Rows
  with `todo` still require terminal file-level review before claiming
  literal every-file closure. `EV-0063` documented the scope drift when
  the repository advanced from the original `80c57e...` anchor to
  current `fb0b3073...`; `EV-0078` resolves the first count mismatch,
  `EV-0097` preserves the refresh back to `1,870` tracked rows,
  `EV-0101` restores parity after the two committed key-policy files
  increased tracked scope to `1,872`, `EV-0122` restores parity again at
  `1,882` tracked non-audit rows after the incident-emitter, signup-locker,
  and migration-0030 files entered scope, and `EV-0131` advances the same
  control to `1,884` rows after the monthly-quota middleware became tracked.
  Current findings remain source/R1 verified and not imported from prior
  audits, but final whole-repo closure still requires terminal review
  status across the refreshed TSV. The current inventory roll-up is
  `done=105`, `in_progress=67`, `todo=1712`, with tracked-file parity
  restored at `1884` rows and preserved through `CMD-0132`.
