# Command Evidence

Shell command transcripts captured during audit execution. ID
prefix: `CMD-####`. Long outputs go in attached files; this
ledger holds the command + summarised result + cross-references.

| ID | Date | Command | Result summary | Evidence produced | Notes |
| --- | --- | --- | --- | --- | --- |
| CMD-1201 | 2026-05-12 | `git rev-parse HEAD` | `80c57e38eeee729ec2d879d54286419206cee864` | EV-1201 | Snapshot anchor |
| CMD-1202 | 2026-05-12 | `git ls-files \| wc -l` | `1747` | EV-1202 | Tracked-file count |
| CMD-1203 | 2026-05-12 | `bash docs/audit-2026-05-12/inventory/generate.sh` | regenerates 14 inventory files | EV-1202, EV-1218 | Run before each session |
| CMD-1204 | 2026-05-12 | `tail -n +2 docs/audit-2026-05-12/inventory/file-coverage.tsv \| awk -F'\t' '{c[$4]++} END {for (k in c) print c[k],k}' \| sort -rn` | runtime 384, test 327, documentation 315, config 299, frontend 173, unknown 83, migration 58, deploy 47, script 35, workflow 10, asset 7, policy 5, generated 4 | inventory file_kind distribution | Sanity-check for unknowns |
| CMD-1205 | 2026-05-12 | `tail -n +2 docs/audit-2026-05-12/inventory/file-coverage.tsv \| awk -F'\t' '{c[$5]++} END {for (k in c) print c[k],k}' \| sort -rn` | every W## has a non-zero file count; no orphan files | inventory workstream-coverage distribution | R00 ownership proof |

## Drift-search results (R0a)

Run the drift block in `04-reconciliation.md` R0a and capture
each result here. Suggested template:

| CMD-#### | Drift target | Hits | Sample paths | Notes |
| --- | --- | ---: | --- | --- |
| CMD-1210 | `grep -RnE 'TODO\\(|FIXME|XXX:|panic\\(|t\.Skip|Skip\\(|nolint' --include='*.go' .` | 149 | various | per EV-1240; not all findings — triage needed |
| CMD-1211 | `grep -RnE 'int64\\([a-zA-Z_]+\\.Lo\\)' --include='*.go' internal cmd` (ADR-0003) | 4 | only `internal/scval/scval.go:231,242` + 2 doc-comment refs | **clean** — only correct Hi/Lo destructure |
| CMD-1212 | `grep -RniE '\\bhorizon\\b\|horizonclient' --include='*.go' internal cmd` (ADR-0001) | 2 | `aggregates.go:288` time horizon; `sdex/events.go:12` ADR-0001 comment | **clean** — no Horizon import |
| CMD-1213 | `grep -RnE 'github\\.com/stellar/go/' --include='*.go' internal cmd` (archived monorepo leak) | 0 | — | **clean** |
| CMD-1214 | cachekeys sole-builder grep | 1 + comments | `internal/storage/redisclient/redisclient.go:51` (canonical) | **clean** |
| CMD-1215 | `.discovery-repos/` import leak grep | 0 imports | 4 doc-comment refs in band/redstone/blend/comet | **clean** |
| CMD-1216 | `grep -RnE '/v1/coins\|/v1/currencies' internal cmd web examples openapi docs/reference docs/operations` | 114 total: 41 internal, 7 cmd, 47 web, 3 examples, 3 openapi, 2 docs/reference, 11 docs/operations | various | most cmd/ refs are in `cmd/ratesengine-sla-probe/main.go` (F-1223); web hits = F-1201; internal hits = dead handler files (F-1210) |
| CMD-1217 | `grep -RnE '//go:embed' --include='*.go' internal cmd` | 3 | `sources/forex/circulation.go:17`, `currency/verified.go:11`, `incidents/incidents.go:31` | all known |
| CMD-1218 | metric `Name:` registrations | 63 | `internal/{obs,api,aggregate,divergence,ledgerstream,dispatcher}` | per W14 walk |

## Gap-closure command runs (2026-05-12 follow-up session)

| CMD-#### | Command | Result | Notes |
| --- | --- | --- | --- |
| CMD-1219 | `go test -timeout 5m ./...` | ALL PASS across every package (incl. internal/sources/*, storage/timescale, storage/redisclient, supply, aggregate, dispatcher, ledgerstream, archivecompleteness, divergence, pkg/client, scripts/ci/{lint-openapi-urls,verify-launch-ready}); 9 packages reported `[no test files]` (usage, decode-scval, encode-topics, circulation-fetch, fx-history-backfill, test/{chaos,integration,load}) | **Repo is green.** Race detector default off in this invocation; rerun with `-race` if needed |
| CMD-1220 | `go mod verify` | `all modules verified` | clean |
| CMD-1221 | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | `Your code is affected by 0 vulnerabilities. … 0 in packages you import and 2 in modules you require, but your code doesn't appear to call these vulnerabilities.` | clean |
| CMD-1222 | `gitleaks detect --redact --no-banner` (v8.30.1) | `2067 commits scanned. … no leaks found` (24.97 MB / 9.44 s) | clean |
| CMD-1223 | `make docs-api` | `✓ docs/reference/api regenerated (Scalar 1.55.3)` | **DRIFT** — `docs/reference/api/rates-engine.v1.yaml` modified: 561 insertions / 429 deletions. Finding F-1246. |
| CMD-1224 | `make docs-postman` | `Generated docs/reference/api/postman-collection.json (656299 bytes)` | output is `docs/reference/api/postman-collection.json`, not `examples/postman/*` — verify which is the source of truth |
| CMD-1225 | spot-verify SEP-10 replay (`grep -rnE 'nonce\|onceonly\|replay\|seen.tx\|consumed' internal/auth/sep10/`) | 1 match — `doc.go:9` "structured nonce" (comment only) | **F-1224 verified**: no seen-tx-hash store |
| CMD-1226 | spot-verify cache-control switch (`grep -nE '^\s*case ' internal/api/v1/middleware/cachecontrol.go`) | switch covers: healthz/account/auth/sep10/price.tip/diagnostics/status/price/history…; **no case for `/v1/auth/callback`** | **F-1225 verified** |
| CMD-1227 | spot-verify SLA probe `-textfile-output` flag (`grep -nE 'textfile-output' cmd/ratesengine-sla-probe/*.go configs/healthchecks/sla-probe.sh`) | flag defined at `main.go:168`; wrapper at `sla-probe.sh:34` calls `"$PROBE_BIN" \\` with NO `-textfile-output` arg | **F-1221 verified** |
| CMD-1228 | spot-verify `MarkRecovered` callers (`grep -RnE 'MarkRecovered' --include='*.go' .`) | 4 matches, all inside `internal/storage/timescale/freeze_events.go` itself | **F-1229 verified** — no external callers |
| CMD-1229 | spot-verify `AppendStripeEvent`/`MarkStripeEventProcessed` callers (`grep -RnE` ditto) | 0 callers outside `internal/platform/{billing.go,errors.go}` definitions | **F-1227 verified** |
| CMD-1230 | spot-verify F-1219/F-1220 (prometheus.r1.yml scrape jobs) | 7 jobs: prometheus, node_exporter, ratesengine-{indexer,api,aggregator}, caddy, galexie. NO redis_exporter, postgres_exporter, alertmanager, pgbackrest_exporter, minio. | **F-1220 verified** |
| CMD-1231 | spot-verify F-1226 oracle/streams test absence | `handleOracleStreams` defined at `internal/api/v1/oracle.go:203`; zero matches in `internal/api/v1/*_test.go` | **F-1226 verified** |
| CMD-1232 | spot-verify F-1242 SEP-41 transfer dual shape | `internal/sources/sep41_supply/dispatcher_adapter_test.go:151 TestDecoder_SkipsTransfer` confirms we **deliberately skip** transfer events for supply purposes | **F-1242 SEP-41 sub-claim refuted** — we don't decode this surface, so dual-shape risk doesn't apply. **Downgrade to invalid.** |
| CMD-1233 | spot-verify F-1242 Comet | `comet/adapter_test.go:41,47` tests topic-order match/reject; **no test asserts non-Comet contract emitting (POOL,*) shape would mis-attribute** | F-1242 Comet sub-claim **verified** |
