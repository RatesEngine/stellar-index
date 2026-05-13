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
- `CMD-0135` corrects stale `F-1236` wording against current source: native
  XLM now has a genuine freshness producer, so the open defect is specifically
  the remaining zero-value escape hatch on static fallback and freshness-query
  failure paths, not total absence of XLM producer coverage.
- `CMD-0136` strengthens `F-1243`: the classic-asset registry still freezes
  same-process freshness/count updates, and the trade insert path can also
  overcount on duplicate replay because registry mutation is not conditioned
  on whether `INSERT ... ON CONFLICT DO NOTHING` actually inserted a row.
- `CMD-0138` re-runs the migration/bootstrap proof on current head and
  confirms `F-1261` is still live: Timescale rejects `0030` with `0A000`,
  so the platform-store integration remains blocked before it can prove the
  webhook/API-key advisory-lock quota remediations.
- `CMD-0139` rechecks `F-1244` on current source and leaves it open unchanged:
  the persisted webhook signing-key model and the surrounding prose still
  disagree in security-significant ways even though the focused package tests
  remain green.
- `CMD-0140` reconciles `F-1251` against current source and evidence state:
  the FX resolver fixes are landed, but the migration `0030` blocker prevents
  the tagged DB-backed integration proof from running, so the finding belongs
  in `needs_evidence` rather than `fixed`.
- `CMD-0141` refreshes `F-1225` against current live R1. The source fallback
  and its regression test remain present, but deployed R1 still serves empty
  `native/fiat:USD` since-inception history while direct Circle-USDC history is
  populated under a peg-enabled config.
- `CMD-0142` refreshes `F-1228` against current live R1. The SSE deadline fix
  still exists in code and the focused stream packages pass, yet the R1
  loopback tip-stream probe again terminates at elapsed `30.0` seconds under a
  68-second client ceiling.
- `CMD-0143` refreshes `F-1219` on current head. The platform bridge remains
  wired and useful, but Stripe still lifts only Redis-backed legacy key budgets,
  leaves Postgres dashboard-key budgets untouched, and lacks closure-grade
  tests for the bridge/subscription/invoice side effects.
- `CMD-0144` refreshes `F-1218` against the current signup-verification state.
  Wave 44 now emits tokens/emails, but the key is still usable before proof of
  ownership; the wave-45 verified-state marker is not production-wired, its
  Redis implementation is still untracked workspace code, and no actual
  request-path `RequireEmailVerified` middleware exists in-tree.
- `CMD-0145` immediately supersedes that narrower workspace snapshot: the
  wave-45 gate/marker wiring now exists in the moving checkout and its focused
  tests pass, but the new files are still untracked, the gate defaults off, and
  docs lint currently fails on the unreconciled config-reference key.
- `CMD-0146` removes only that transient docs-sync objection. The generated
  config reference now includes `api.signup_require_email_verification` and
  docs lint passes again; `F-1218` still stays open because the new gate/store
  files are workspace-only and the hardening posture remains opt-in-off.
- `CMD-0147` settles the moving-workspace phase: wave 45 is now committed at
  `HEAD=93594529...`, but `F-1218` remains open because the new gate defaults
  off and live R1 has no explicit enabling config line.
- `CMD-0151` reconciles `F-1256` after wave 48: the dashboard key form and
  OpenAPI now describe the same tier-clamped persisted budget semantics, so
  the cross-file docs/product finding is genuinely fixed.
- `CMD-0152` restores audit-control parity after wave 47/48 tracked-scope
  growth. The Codex TSV now matches the current non-audit tree at `1,897`
  rows, the roll-up is `done=111`, `in_progress=87`, `todo=1699`, and docs
  lint passes on the current moving workspace.
- `CMD-0159` through `CMD-0163` falsify and then settle the moving wave-52/54
  closure claims. `F-1259` genuinely closes once the tracked Postman artifact
  is checked instead of the deleted local byproduct; `F-1244` remains open
  until the platform spec drops its false sealed-box webhook-secret claim, then
  closes after the wave-54 spec rewrite; `F-1263` broadens from malformed-ID to
  malformed-ID-plus-prefix fixture drift, then closes after the corrected full
  `TestPlatformPostgresStores` proof runs green, which also clears the remaining
  `F-1257` evidence gate.
- `CMD-0164` restores control parity on settled `HEAD=a783b5ed...`: tracked
  non-audit scope and TSV rows both equal `1,898`; status roll-up is
  `done=112`, `in_progress=88`, `todo=1698`; `internal` area count is `693`;
  docs lint passes.
- `CMD-0167` closes the remaining evidence gate on `F-1243`: the new
  asset-registry replay integration test runs green on current head and proves
  duplicate replays no longer mutate `classic_assets` counters after the
  dedupe cache is cleared between attempts.
- `CMD-0168` reconciles `F-1219` to genuinely fixed on current source rather
  than merely "narrowed": the Stripe bridge now fans paid upgrades into
  Postgres dashboard keys in production wiring, targeted platform-webhook
  tests pass, and the new sync-error metric/runbook makes best-effort platform
  write failures operator-visible.
- `CMD-0169` restores literal control parity after the latest tracked-scope
  growth. The Codex TSV now matches the repo at `tracked=1901`, `rows=1901`;
  the roll-up is `done=115`, `in_progress=88`, `todo=1698`; area counts and the
  repo snapshot are refreshed to `HEAD=34511e50...`; docs lint passes.
- `CMD-0170` through `CMD-0173` refresh the live/open tranche rather than
  carrying forward stale snapshots: launch-readiness is still red on the same
  cross-region/security/failover rows, GitHub hosted alerts remain disabled,
  R1 still exposes captive-core `11726/tcp` / leaves `sla-probe.timer`
  disabled / leaves signup verification unset, and the public history/SSE
  probes still reproduce `F-1225`, `F-1228`, and `F-1230`.
- `CMD-0174` falsifies the register itself: after the live refreshes, a
  summary-table-versus-detail-section status comparator found 19 stale detail
  labels, those are now reconciled to the already-settled table statuses, the
  comparator returns no mismatches, and docs lint stays green.
- `CMD-0175` refines the remaining R1 operations findings against current live
  truth. The old "many stopped sources" picture has narrowed, but launch-state
  remains degraded: status reports `12/17` active sources and eight incidents,
  ECB is still stale, Redstone is pending source-stopped, memory remains above
  94%, swap is effectively exhausted, and MinIO is still 78% full.
- `CMD-0176` refreshes the hosted governance tranche directly from GitHub:
  branch protection is still unavailable on the private-plan posture,
  production environments still have no reviewer/branch protection, and
  repository Actions policy is still all-actions/no-SHA-pinning.
- `CMD-0177` captured the pre-reopen wave-97 count reconciliation:
  findings `50/13`, XFI `45/10`, remediation `47/14`.
- `CMD-0178` then falsifies the wave-57 `F-1211` closure claim during the
  breadth pass. The primary status-page setup/runbook path is current, but
  `CLAUDE.md`, `launch-readiness-backlog.md`, `launch-task-list.md`, and the
  two `deploy/comms/*` templates still retain active Upptime/cstate/status-repo
  instructions that can misdirect an operator during an incident. `F-1211`,
  `XFI-0007`, and `R-1209` are reopened.
- `CMD-0179` verifies that the post-reopen wave-98 ledgers reconcile again:
  findings `49/14`, XFI `44/11`, remediation `46/15`; file coverage now rolls
  up to `done=115`, `in_progress=91`, `todo=1695`; docs lint still passes.
- `CMD-0180` closes the first root/control-doc breadth tranche without
  manufacturing findings from clearly historical documents. The dated review
  is explicitly unratified, the status-page hosting comparison is explicitly
  superseded, and the root README/VERSIONS/agent-orientation surfaces add no
  new defect beyond already-tracked live findings.
- `CMD-0181` surfaces new medium-severity `F-1264`: the Prometheus/Loki R1
  docs still describe a no-firewall/publicly reachable observability posture
  even though live R1 now has nftables `policy drop`, external `9090` / `3100`
  probes time out, and only the captive-core port set is explicitly accepted.
- `CMD-0182` surfaces new low-severity `F-1265`: Alertmanager migration docs
  and inline config comments still claim the Ansible template uses
  `critical/warning/info`, but the actual template already matches the
  standalone `page/ticket/informational` ladder.
- `CMD-0183` verifies that the wave-99 tables reconcile after both additions:
  findings `49/16`, XFI `44/13`, remediation `46/17`; coverage now rolls up
  to `done=125`, `in_progress=95`, `todo=1681`; docs lint remains green.
- `CMD-0184` broadens the Ansible/doc review. It expands `F-1265` to include
  the Prometheus role README's stale `critical/warning/info` prose and opens
  new medium-severity `F-1266`: the top-level Ansible bootstrap README still
  says archival-node is the only role while sibling HA roles/playbooks have
  landed, and it advertises Promtail/Loki wiring that the archival-node
  observability task still marks TODO.
- `CMD-0185` restores post-Ansible-pass control parity: findings `49/17`, XFI
  `44/14`, remediation `46/18`; file coverage now rolls up to `done=126`,
  `in_progress=98`, `todo=1677`; docs lint remains green.
- `CMD-0186` opens new medium-severity `F-1267`: Healthchecks setup docs and
  installer comments still say "four" external checks / URLs after the SLA
  probe timer/env slot expanded the contract to five; the pre-launch hardening
  guide also still omits `HEALTHCHECKS_URL_SLA_PROBE`.
- `CMD-0187` restores post-Healthchecks control parity: findings `49/18`, XFI
  `44/15`, remediation `46/19`; file coverage now rolls up to `done=126`,
  `in_progress=101`, `todo=1674`; docs lint remains green.
- `CMD-0188` opens new medium-severity `F-1268`: the R1 rules README still
  copies alert overlays into `/etc/prometheus/rules.d/`, while the actual R1
  Prometheus config loads `/etc/prometheus/rules.r1/*.yml`.
- `CMD-0189` restores post-rule-overlay control parity: findings `49/19`, XFI
  `44/16`, remediation `46/20`; file coverage now rolls up to `done=126`,
  `in_progress=103`, `todo=1672`; docs lint remains green.
- `CMD-0190` opens new low-severity `F-1269`: the WASM audit-input README
  still promises an `_unattributed` bucket that the curated YAML intentionally
  removed after the 2026-05-01 Reflector-testnet correction.
- `CMD-0191` falsifies the parallel remediation slice rather than trusting it
  wholesale. `F-1264`, `F-1265`, `F-1266`, and `F-1267` genuinely close in the
  workspace; `F-1268` remains open because the R1 rules overlay README still
  points at `/etc/prometheus/rules.d/` while the active config loads
  `/etc/prometheus/rules.r1/*.yml`. The same pass also reviewed the newly
  tracked `internal/obstest/histogram.go` helper and added it to the inventory.
- `CMD-0192` restores literal control-plane consistency after that recheck:
  findings `54/15`, XFI `49/12`, remediation `51/16`; file coverage now rolls
  up to `done=137`, `in_progress=95`, `todo=1670`; tracked-file parity is exact
  at `1902/1902`; docs lint passes; and the table/detail finding-status
  mismatch detector returns no rows.
- `CMD-0193` opens new medium-severity `F-1270`: active Caddy/operator docs
  contradict ADR-0025 by instructing operators to add Cloudflare edge CIDRs to
  API `trusted_proxy_cidrs`, even though the implemented architecture pins
  Cloudflare trust at Caddy and leaves the API trusting only its immediate
  proxy peer.
- `CMD-0194` confirms the moving workspace has now genuinely closed
  `F-1268` and `F-1269`: the R1 rules README points at the loaded
  `/etc/prometheus/rules.r1/` directory, and the WASM audit README now
  accurately describes the retired `_unattributed` concept as historical.
- `CMD-0196` opens high-severity `F-1271`: the Redis Sentinel role/runbook/client
  stack assumes Sentinel listener authentication, but the rendered
  `sentinel.conf.j2` only sets `sentinel auth-pass` for master authentication
  and never configures client-facing Sentinel auth.
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
  and migration-0030 files entered scope, `EV-0131` advances the same
  control to `1,884` rows after the monthly-quota middleware became tracked,
  and `EV-0136` restores parity at `1,888` rows after committed wave 39 added
  the touch-usage middleware/debouncer pair plus tests. `EV-0151` advances
  that same control to `1,897` rows after wave 47/48 scope growth and the
  missing `asset_registry_test.go` ledger row was added. `EV-0164` advances
  parity again at `1,898` tracked non-audit rows after the wave-50
  `apikey_store_test.go` scope growth and the wave-54 reconciliation refresh.
  `EV-0169` advances the same control to `1,901` tracked rows after the
  replay-integration test, Stripe sync runbook, and status README entered the
  tracked tree.
  Current findings remain source/R1 verified and not imported from prior
  audits, but final whole-repo closure still requires terminal review
  status across the refreshed TSV. The current inventory roll-up is
  `done=115`, `in_progress=88`, `todo=1698`, with tracked-file parity
  restored at `1901` rows and preserved through `CMD-0169`.
- `CMD-0197` records the initial Redis exporter ACL-lockdown defect; `CMD-0200`
  falsifies the first remediation attempt and preserves `F-1272` as open on the
  default non-lockdown branch.
- `CMD-0198` closes `F-1270` and `F-1271` on current source after the Caddy trust
  docs and Sentinel listener-auth render were corrected.
- `CMD-0199` reopens `F-1266` because the improved Ansible docs now advertise
  cluster playbooks that are not present in the tracked tree.
- `CMD-0201` adds `F-1273` for the shipped Sentinel design-note/tabletop drift.
- `CMD-0202` restores audit-control parity after that tranche:
  findings `59 fixed / 14 open`, XFI `52 fixed / 13 open`, remediation
  `54 fixed / 17 open`, file coverage `done=143 / in_progress=99 / todo=1660`,
  tracked-file parity `1902 / 1902`, docs lint green, and no finding-status
  mismatches between the register table and detailed sections.
- `CMD-0203` narrows but preserves `F-1266`: the top-level Ansible README and
  Redis role README now admit the missing cluster playbooks, while the
  HAProxy/Loki/Patroni/Prometheus role READMEs still present absent playbooks as
  runnable commands.
- `CMD-0204` reopens `F-1265` on the shipped Prometheus role design note, which
  still documents the retired `critical/warning/info` ladder after the actual
  configs and operator docs converged.
- `CMD-0205` restores audit-control parity after those refinements:
  findings `57 fixed / 16 open`, XFI `51 fixed / 14 open`, remediation
  `53 fixed / 18 open`, file coverage `done=143 / in_progress=103 / todo=1656`,
  tracked-file parity `1902 / 1902`, docs lint green, and no finding-status
  mismatches between the register table and detailed sections.
- `CMD-0206` closes the residual `F-1265` Prometheus design-note drift after
  the example routes were updated to `page / ticket / informational`.
- `CMD-0207` adds `F-1274` for the HAProxy role/design-note references to a
  missing `api-pod-down` runbook.
- `CMD-0208` narrows but preserves `F-1273`: the drill command is now
  authenticated, while the shipped design note still describes future
  `internal/cachekeys` Sentinel-client work that already landed elsewhere.
- `CMD-0209` restores audit-control parity after those changes:
  findings `58 fixed / 16 open`, XFI `52 fixed / 14 open`, remediation
  `54 fixed / 18 open`, file coverage `done=145 / in_progress=102 / todo=1655`,
  tracked-file parity `1902 / 1902`, docs lint green, and no finding-status
  mismatches between the register table and detailed sections.
- `CMD-0210` closes `F-1266`: the remaining HAProxy/Loki/Patroni role docs now
  mark absent playbooks as backlog-only, and Prometheus points at tracked
  `monitoring.yml`.
- `CMD-0211` closes `F-1272`: exporter username selection now follows the same
  ACL-lockdown branch that controls ACL-file emission.
- `CMD-0212` widens but preserves `F-1273`: the Sentinel role README still
  repeats the already-landed `internal/cachekeys` future-state claim alongside
  the shipped design note.
- `CMD-0213` closes `F-1274`: HAProxy docs now redirect to tracked
  `api-down.md` guidance and record that `api-pod-down.md` was the incorrect
  historical reference.
- `CMD-0214` adds high-severity `F-1275`: the shipped HAProxy `/v1/readyz`
  routing path and API Redis readiness checker can turn a Redis failover into
  a public routing outage despite maintained fail-open/degraded-serving docs.
- `CMD-0215` adds `F-1276`: alert/runbook/query docs still publish
  `job="api"` PromQL even though supported deployments use
  `ratesengine_api` or `ratesengine-api`.
- `CMD-0216` adds `F-1277`: `api-down.md` points at nonexistent
  `internal/api/v1/healthz.go` instead of the live readiness handler in
  `internal/api/v1/server.go`.
- `CMD-0217` adds high-severity `F-1278`: the HA-role nftables
  drop-ins do not compose deterministically with the repo's default-drop
  firewall baseline and are not real deny policies on their own.
- `CMD-0218` adds `F-1279`: Patroni writes its firewall drop-in before
  creating `/etc/nftables.d/`, so a clean-host first run can fail.
- `CMD-0219` restores audit-control parity after the moving HA/deployment
  tranche: findings `59 fixed / 20 open`, XFI `55 fixed / 16 open`,
  remediation `57 fixed / 20 open`, file coverage
  `done=152 / in_progress=111 / todo=1663`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0220` adds `F-1280`: Patroni's etcd download path defaults to a
  placeholder checksum that neither defaults nor README document how to replace.
- `CMD-0221` adds `F-1281`: Patroni's textfile scraper invokes `jq`, but the
  role never installs or documents that dependency.
- `CMD-0222` adds `F-1282`: `patroni_pgbackrest_restore_target` is documented
  but unused, so DR point-in-time restore requests render as immediate restores.
- `CMD-0223` restores audit-control parity after the Patroni tranche:
  findings `59 fixed / 23 open`, XFI `55 fixed / 19 open`, remediation
  `57 fixed / 23 open`, file coverage
  `done=151 / in_progress=118 / todo=1657`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0224` rechecks the moving HA/Patroni remediation state: `F-1279`,
  `F-1281`, and `F-1282` now close; `F-1280` narrows to missing README
  prerequisite/inventory guidance; and `F-1278` remains open because changing
  the drop-ins to `priority -100` still does not make an early base-chain
  `accept` final against the later default-drop chain.
- `CMD-0225` adds `F-1283`: the Timescale primary-down runbook's etcd
  commands use HTTPS, a stale top-level leader key, and a five-node quorum
  expectation that do not match the shipped Patroni role's HTTP etcd config,
  Patroni namespace/scope, and exactly three-node cluster assertion.
- `CMD-0226` restores audit-control parity after those updates:
  findings `62 fixed / 21 open`, XFI `58 fixed / 17 open`, remediation
  `60 fixed / 21 open`, file coverage
  `done=153 / in_progress=121 / todo=1652`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0227` closes the runtime part of `F-1275`: Redis-only ready-check
  failure now returns 200/degraded and is regression-tested. The pass adds
  `F-1284` for the narrower residual docs drift in HAProxy role comments,
  HAProxy README, and HA plan prose that still describe Redis as
  readiness-critical.
- `CMD-0228` restores audit-control parity after that split:
  findings `63 fixed / 21 open`, XFI `59 fixed / 17 open`, remediation
  `61 fixed / 21 open`, file coverage
  `done=152 / in_progress=123 / todo=1651`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0229` closes `F-1277` and narrows `F-1276`: `api-down.md` now points
  at `internal/api/v1/server.go::handleReadyz`, and maintained API runbooks
  now use the current selector family; the remaining `F-1276` drift is the
  stale `job="api"` source comment in `internal/obs/metrics.go`.
- `CMD-0230` restores audit-control parity after that pass:
  findings `64 fixed / 20 open`, XFI `60 fixed / 16 open`, remediation
  `62 fixed / 20 open`, file coverage
  `done=157 / in_progress=118 / todo=1651`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0231` adds high-severity `F-1285`: Loki and Promtail install upstream
  release zips without any checksum, while Prometheus and Alertmanager
  checksums are optional and undocumented.
- `CMD-0232` adds high-severity `F-1286`: Loki's systemd unit renders S3
  credentials through literal `${...}` indirection even though systemd does
  not shell-expand `Environment=` values from `EnvironmentFile`.
- `CMD-0233` restores audit-control parity after the monitoring-role pass:
  findings `64 fixed / 22 open`, XFI `60 fixed / 18 open`, remediation
  `62 fixed / 22 open`, file coverage
  `done=155 / in_progress=127 / todo=1644`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0234` adds high-severity `F-1287`: Prometheus and Alertmanager bind
  their HTTP ports to loopback by default, while the generated Prometheus
  config targets each prom host's private IP for self-scrape and
  Alertmanager delivery.
- `CMD-0235` restores audit-control parity after that pass:
  findings `64 fixed / 23 open`, XFI `60 fixed / 19 open`, remediation
  `62 fixed / 23 open`, file coverage
  `done=155 / in_progress=129 / todo=1642`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0236` adds `F-1288`: Prometheus's 20 GB TSDB capacity prerequisite is
  documented as a preflight assertion, but the implementation explicitly
  ignores failures from that assertion.
- `CMD-0237` restores audit-control parity after that pass:
  findings `64 fixed / 24 open`, XFI `60 fixed / 20 open`, remediation
  `62 fixed / 24 open`, file coverage
  `done=155 / in_progress=130 / todo=1641`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0238` closes `F-1286`: the Loki service template no longer renders
  literal `${RATESENGINE_S3_*}` credential assignments, and the README/defaults
  now require direct `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` entries in
  `/etc/default/loki`.
- `CMD-0239` adds `F-1289`: Loki defaults/docs/design note promise MinIO
  bucket/access and local capacity preflight coverage, but the server preflight
  only checks OS, inventory, time sync, and user creation.
- `CMD-0240` restores audit-control parity after the Loki credential closure
  and storage-preflight finding:
  findings `65 fixed / 24 open`, XFI `61 fixed / 20 open`, remediation
  `63 fixed / 24 open`, file coverage
  `done=155 / in_progress=132 / todo=1639`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0241` rechecks the moving monitoring-role fixes: `F-1287` and
  `F-1288` close in source, while `F-1285` narrows to missing checksum
  variable documentation after all four install tasks gained checksum asserts.
- `CMD-0242` adds `F-1290`: the Prometheus README still describes loopback-only
  UI access and a 9094-only firewall after the role moved 9090/9093 to
  firewall-gated internal network listeners.
- `CMD-0243` restores audit-control parity after the Prometheus moving-fix
  reconciliation:
  findings `67 fixed / 23 open`, XFI `63 fixed / 19 open`, remediation
  `65 fixed / 23 open`, file coverage
  `done=161 / in_progress=126 / todo=1639`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0244` adds two adversarial invariant findings: `F-1291` for
  unauthenticated archival-node MinIO/`mc`/node_exporter executable downloads,
  and `F-1292` for the active core-lag runbook's Horizon cross-check despite
  ADR-0001's runbook boundary.
- `CMD-0245` restores audit-control parity after that pass:
  findings `67 fixed / 25 open`, XFI `63 fixed / 21 open`, remediation
  `65 fixed / 25 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0246` rechecks concurrent fixes for the two fresh findings: `F-1292`
  is source-closed because `core-lag.md` now uses stellar.expert/stellar-rpc
  instead of Horizon, and `F-1291` narrows because archival-node install tasks
  now enforce checksums while defaults/operator docs still omit the required
  digest variables.
- `CMD-0247` restores audit-control parity after that moving-fix recheck:
  findings `68 fixed / 24 open`, XFI `64 fixed / 20 open`, remediation
  `66 fixed / 24 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0248` adds `F-1293` for the Redis Sentinel role's unchecked
  `redis_exporter` release download and `F-1294` for CI promtool/gitleaks
  curl-to-tar installers without digest verification.
- `CMD-0249` restores audit-control parity after that pass:
  findings `68 fixed / 26 open`, XFI `64 fixed / 22 open`, remediation
  `66 fixed / 26 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0250` narrows `F-1294` because the promtool installer now verifies a
  SHA-256 while gitleaks remains unchecked, and adds `F-1295` for the Redis
  exporter unit rendering the Redis password into a 0644 systemd unit file.
- `CMD-0251` restores audit-control parity after that pass:
  findings `68 fixed / 27 open`, XFI `64 fixed / 23 open`, remediation
  `66 fixed / 27 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0252` rechecks moving fixes for the latest supply-chain findings:
  `F-1294` closes because both CI tool downloads now verify SHA-256 before
  extraction, while `F-1293` narrows to missing Redis Sentinel role
  defaults/README guidance after the task-level `redis_exporter` checksum
  enforcement landed.
- `CMD-0253` restores audit-control parity after that moving-fix
  reconciliation:
  findings `69 fixed / 26 open`, XFI `65 fixed / 22 open`, remediation
  `67 fixed / 26 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0254` adds `F-1296`: `release.yml` and `deploy.yml` grant
  `id-token: write` only for future keyless signing/attestation, and no
  current step consumes GitHub OIDC. `api-docs.yml` remains the reviewed
  valid counterexample because GitHub Pages deploy requires the token.
- `CMD-0255` restores audit-control parity after the workflow OIDC finding:
  findings `69 fixed / 27 open`, XFI `65 fixed / 23 open`, remediation
  `67 fixed / 27 open`, file coverage
  `done=164 / in_progress=126 / todo=1636`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0256` adds `F-1297`: the deploy docs require pinned
  `<REGION>_SSH_KNOWN_HOSTS`, but `deploy.yml` merely warns when the R1
  secret is absent and falls back to live `ssh-keyscan`, so the production
  root-SSH deploy trust anchor is collected during the same run.
- `CMD-0257` restores audit-control parity after the deploy SSH finding and
  table-status drift correction:
  findings `69 fixed / 28 open`, XFI `65 fixed / 24 open`, remediation
  `67 fixed / 28 open`, file coverage
  `done=166 / in_progress=127 / todo=1633`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0258` reopens `F-1221`: prior closure checked the fixed
  `release-process.md` step 4 and `deploy-workflow.md`, but active
  `CLAUDE.md` still claims arm64 + GHCR release artifacts, the
  release-process pipeline summary still says amd64 + arm64, and
  R1 state still references the old 12-binary release shape.
- `CMD-0259` restores audit-control parity after reopening `F-1221`:
  findings `68 fixed / 29 open`, XFI `64 fixed / 25 open`, remediation
  `66 fixed / 29 open`, file coverage
  `done=166 / in_progress=128 / todo=1632`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0260` adds `F-1298`: `deploy.yml` and `release.yml` interpolate
  free-form manual inputs directly into `run:` shell scripts before
  validation, while the static-site deploy workflows mostly use choice
  inputs or `env:` values and do not show the same shell-source injection
  shape.
- `CMD-0261` restores audit-control parity after the workflow input-injection
  finding:
  findings `68 fixed / 30 open`, XFI `64 fixed / 26 open`, remediation
  `66 fixed / 30 open`, file coverage
  `done=166 / in_progress=128 / todo=1632`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0262` adds `F-1299`: production deploy resolves mutable Ansible
  tooling at runtime (`ansible-core==2.18.*` plus lower-bound Galaxy
  collection ranges), so the same repo revision can execute different
  deploy code without a reviewed dependency diff.
- `CMD-0263` restores audit-control parity after the mutable deploy-tooling
  finding:
  findings `68 fixed / 31 open`, XFI `64 fixed / 27 open`, remediation
  `66 fixed / 31 open`, file coverage
  `done=167 / in_progress=128 / todo=1631`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0264` adds `F-1300`: the Healthchecks SLA wrapper defaults to a
  node_exporter textfile path, but its DynamicUser/ProtectSystem unit lacks
  a write allowance for that directory, unlike the dedicated
  `deploy/systemd/sla-probe.service`.
- `CMD-0265` restores audit-control parity after the Healthchecks SLA
  sandbox finding:
  findings `68 fixed / 32 open`, XFI `64 fixed / 28 open`, remediation
  `66 fixed / 32 open`, file coverage
  `done=170 / in_progress=131 / todo=1625`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0266` adds `F-1301`: the aggregator-silent P1 runbook tells R1/default
  single-host operators to inspect `localhost:9464`, but the aggregator code
  shifts its default listener to `9465` when co-located with the indexer, and
  R1 Prometheus plus Healthchecks already scrape that shifted endpoint.
- `CMD-0267` restores audit-control parity after the aggregator metrics-port
  runbook finding:
  findings `68 fixed / 33 open`, XFI `64 fixed / 29 open`, remediation
  `66 fixed / 33 open`, file coverage
  `done=172 / in_progress=131 / todo=1623`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0268` adds `F-1302`: the Healthchecks smoke wrapper exits `0` before
  sending `${HEALTHCHECKS_URL_SMOKE}/fail` when the copied `r1-smoke.sh`
  executable is missing or not executable, so a broken smoke install can
  silently remove the API-surface evidence path.
- `CMD-0269` restores audit-control parity after the Healthchecks smoke wrapper
  finding:
  findings `68 fixed / 34 open`, XFI `64 fixed / 30 open`, remediation
  `66 fixed / 34 open`, file coverage
  `done=175 / in_progress=131 / todo=1620`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0270` closes `F-1300`: the current Healthchecks SLA service now grants
  the node_exporter textfile collector path through `ReadWritePaths` and joins
  the `ratesengine` group that owns the directory, so the original sandbox
  write conflict no longer reproduces in source.
- `CMD-0271` adds `F-1303`: the SLA Healthchecks wrapper exits `0` before
  sending `${HEALTHCHECKS_URL_SLA_PROBE}/fail` when the probe binary itself is
  missing or not executable, leaving a binary-deploy break invisible to the
  external SLA heartbeat.
- `CMD-0272` restores audit-control parity after the SLA Healthchecks
  moving-fix/missing-binary pass:
  findings `70 fixed / 33 open`, XFI `65 fixed / 30 open`, remediation
  `67 fixed / 34 open`, file coverage
  `done=178 / in_progress=131 / todo=1617`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
- `CMD-0273` adds `F-1304`: `pre-launch-hardening.md` lists the
  SLA-probe Healthchecks URL but omits `ratesengine-sla-probe.timer` from the
  apply/restart command, unlike the canonical Healthchecks README and installer
  guidance.
- `CMD-0274` restores audit-control parity after the Healthchecks hardening
  guide restart finding:
  findings `70 fixed / 34 open`, XFI `65 fixed / 31 open`, remediation
  `67 fixed / 35 open`, file coverage
  `done=178 / in_progress=131 / todo=1617`, tracked-file parity
  `1926 / 1926`, docs lint green, and no finding-status mismatches between
  the register table and detailed sections.
