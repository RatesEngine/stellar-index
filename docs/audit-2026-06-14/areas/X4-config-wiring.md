# Seam X4 — Config → Wiring cross-file audit

READ-ONLY. Goal: verify every `internal/config` field is correctly
consumed, and every consumer reads a real, set field — no orphan config
fields, no consumer reading an unset/misnamed field. Plus cross-check the
schema against the deployed r1 example (`configs/example.toml`) and the
Ansible templates.

- **Date:** 2026-06-14
- **Method:** read `internal/config/{config.go,load.go,schema.go,validate.go}`
  in full, enumerate every leaf field + toml tag + default + `env:` tag,
  then grep the whole tree (`cmd/`, `internal/`) for each field's consumer.
  Cross-check `configs/example.toml`,
  `configs/ansible/roles/archival-node/templates/{stellarindex.toml.j2,stellarindex.env.j2}`.
- **Files read (count): 14**
  - `internal/config/config.go`, `load.go`, `schema.go`, `validate.go`
  - `configs/example.toml`
  - `configs/ansible/roles/archival-node/templates/stellarindex.toml.j2`
  - `configs/ansible/roles/archival-node/templates/stellarindex.env.j2`
  - `cmd/stellarindex-ops/trim_galexie_archive.go`
  - `cmd/stellarindex-sla-probe/main.go`, `cmd/stellarindex-migrate/main.go`
  - plus grep-confirmed consumer sites in `cmd/stellarindex-{indexer,aggregator,api,ops}/main.go`,
    `internal/storage/redisclient/`, `internal/pipeline/`, `internal/aggregate/orchestrator/`,
    `internal/divergence/`, `internal/supply/`, `internal/sources/forex/`
  - (consumer tracing fanned out across 4 sub-agents; spot-checked the
    load-bearing claims directly — see "Verification" notes inline)

---

## Severity summary

| Severity | Count | Items |
|---|---|---|
| Critical | 0 | — |
| High | 0 | — |
| Medium | 3 | X4-M1 S3 cred name-vs-value (A16 class), X4-M2 `ExternalBaseURL` dead, X4-M3 example.toml schema drift (15 fields undocumented + stale `auth_mode` comment) |
| Low | 6 | X4-L1 `BackfillBatchSize` orphan, X4-L2 `CursorStoreScheme` orphan, X4-L3 `VWAP/TWAPWindowSeconds` orphans, X4-L4 ECB/CoinGecko `PollInterval` not wired, X4-L5 out-of-schema env vars (invisible to docs-config), X4-L6 `S3Cold*KeyEnv` + `Stellar`/`Region` decorative orphans |
| Info | — | env-name-vs-value semantics all correct except X4-M1; SEP10/Dashboard/Stripe verified; trace fields orphan-by-design |

**Orphan/mismatched field count:** **13 fields flagged** — 8 true orphans
(no runtime consumer), 2 PollInterval mismatches, 1 cred name-vs-value
mismatch (X4-M1), 2 effectively-dead (`ExternalBaseURL` log-only,
`Stellar.Network` boot-log-only). Plus 15 schema fields undocumented in
example.toml (X4-M3, drift not orphan) and 3 out-of-schema env vars
(X4-L5).

Everything load-bearing (Postgres DSN, Redis HA, S3 buckets, ClickHouse,
all source/oracle/external `.Enabled` gates, all auth/SEP10/Dashboard/
Stripe, supply watched-sets, aggregate VWAP knobs, anomaly, divergence,
`usd_pegged_classic_assets`, `min_usd_volume`,
`signup_require_email_verification`, `allowed_origins`/CORS) is **WIRED**
and correct.

---

## Critical / High inline

None. The S3 cred bug (X4-M1) would be High on its own (silent auth
fallback) but is **masked on every Ansible-deployed host** by parallel
`AWS_*` env vars (see below), so it is filed Medium as a latent foot-gun.

---

## Medium findings

### X4-M1 — S3 cred fields: name-vs-value confusion (A16 class)

`Storage.S3AccessKeyEnv` / `S3SecretKeyEnv` are read with **two opposite
semantics**:

- **`internal/config/load.go:75-79` `ApplyEnvOverrides`** — when the env
  var `STELLARINDEX_S3_ACCESS_KEY` (resp. `_SECRET_KEY`) is set, it does
  `c.Storage.S3AccessKeyEnv = v`, stuffing the **secret VALUE** into the
  field. (Same pattern as the `env:` tag column — but here the field's
  whole purpose elsewhere is to hold a NAME.)
- **`cmd/stellarindex-ops/trim_galexie_archive.go:143 → buildS3Client:305-318`**
  — reads the same field as an **env-var NAME**: `ak := os.Getenv(accessKeyEnv)`.

Conflict: the default value of `S3AccessKeyEnv` is the *string*
`"STELLARINDEX_S3_ACCESS_KEY"` (a name). As long as the override has NOT
fired, `os.Getenv("STELLARINDEX_S3_ACCESS_KEY")` resolves the real key —
correct. But once `ApplyEnvOverrides` fires (the documented production
path), the field becomes the secret value, and `buildS3Client` does
`os.Getenv("<the-actual-access-key-string>")` → `""` → static creds are
NOT set → silent fallback to the AWS SDK default credential chain.

**Why Medium, not High — masked on r1:** the Ansible EnvironmentFile
`stellarindex.env.j2:23-30` sets BOTH `STELLARINDEX_S3_ACCESS_KEY`
(the value, triggers the override) AND the canonical
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (+ `AWS_ENDPOINT_URL`,
`AWS_REGION`). So `buildS3Client`'s fall-through lands on the SDK default
chain, which finds `AWS_ACCESS_KEY_ID` and authenticates correctly. The
bug only bites a host that sets `STELLARINDEX_S3_ACCESS_KEY` WITHOUT the
parallel `AWS_*` vars — e.g. a config following `example.toml`/docs only,
or any non-Ansible deployment of the `trim-galexie-archive` ops command.

**Blast radius is narrow:** `buildS3Client` has exactly one caller
(`trim-galexie-archive`, a maintenance ops subcommand). Production ingest/
backfill (`internal/pipeline/datastore.go`) never reads the S3 cred fields
at all — it passes only endpoint/region/bucket into the go-stellar-sdk
DataStore params and relies on the SDK's own credential resolution.

Files: `internal/config/load.go:75-79`,
`cmd/stellarindex-ops/trim_galexie_archive.go:143,305-318`,
`configs/ansible/roles/archival-node/templates/stellarindex.env.j2:23-30`.

### X4-M2 — `API.ExternalBaseURL` is effectively dead (log-only)

`api.external_base_url` (default `https://api.stellarindex.io/v1`) has
exactly one non-config consumer: `cmd/stellarindex-api/main.go:131`, a
startup `logger.Info` field. Nothing constructs links, redirects,
pagination `next` URLs, magic-link callbacks, or SEP-10 challenge URLs
from it (those use `Dashboard.BaseURL`, `SEP10.WebAuthDomain` etc.). The
doc string ("Public-facing base URL") implies it should be load-bearing;
it does no work. Either wire it (e.g. into pagination/HATEOAS links) or
demote the doc to "logging/diagnostics only."
File: `cmd/stellarindex-api/main.go:131`.

### X4-M3 — example.toml drifted from the schema (15 fields undocumented + 1 stale comment)

`configs/example.toml` predates a number of schema additions. Fields
present in the schema but with NO mention (not even a commented stanza) in
example.toml:

`auth_backend`, `allow_credentials`, `tls_cert_probe_hosts`,
`prometheus_url`, `divergence_min_interval_seconds`, `live_seam_ledger`,
`s3_cold_*` (whole cold-tier block), `clickhouse_addr`,
`clickhouse_live_sink`, `clickhouse_projector_source`,
`[ingestion.projector]`, `[anomaly]` (thresholds / classifications /
phase2), `[metadata]` (`issuer_home_domains`),
`[oracle.{redstone,band,soroswap}]` (only `[oracle.reflector]` is shown),
`[api.{sep10,stripe,dashboard}]`.

Notable because the Ansible template *does* render several of these
(`s3_cold_*`, `[anomaly.phase2] z_score_min_freeze=100`,
`[oracle.{redstone,band,soroswap}]`, `auth_backend` via `auth_mode`,
`prometheus_url`), so an operator copying example.toml gets a materially
different config than r1. Reverse direction is clean — no key in the
Ansible template or example.toml is absent from the schema (the strict
`Undecoded()` check in `load.go:39-48` would reject that at boot anyway).

**Stale comment:** example.toml:235 documents
`auth_mode = "none" # none | apikey | sep10` — omits `apikey_optional`,
which is the value the validator accepts AND the one r1 actually runs
(`stellarindex.toml.j2:132 auth_mode = "apikey_optional"`).

Files: `configs/example.toml` (whole file),
`configs/ansible/roles/archival-node/templates/stellarindex.toml.j2`.

---

## Low findings

### X4-L1 — `Ingestion.BackfillBatchSize` orphan
toml `backfill_batch_size`, default 64. Only readers: `validate.go:285`
(rejects 0). Zero runtime fetch/backfill loop consumes it (grep
`BackfillBatchSize` over `cmd/`+`internal/` minus config/tests = empty).
The only struct that ever used it is the dead `consumer.Orchestrator`
(no callers per CLAUDE.md). Validate-only orphan.

### X4-L2 — `Ingestion.CursorStoreScheme` orphan
toml `cursor_store_scheme`, default `postgres`. Only reader:
`validate.go:278` (postgres/redis enum). No runtime branch selects a
cursor store from it — cursors are postgres-only in practice.
Validate-only orphan.

### X4-L3 — `Aggregate.VWAPWindowSeconds` + `TWAPWindowSeconds` orphans
toml `vwap_window_seconds` / `twap_window_seconds`, default 300 each.
Only readers: `validate.go:342,346` (`>0` checks). No runtime code reads
either; the real window durations come entirely from `Aggregate.Windows`
(`AggregatorWindows()`). An operator tuning these two scalars gets zero
behavioural change. Validate-only orphans. (Documented in example.toml at
lines 204-205, compounding the confusion — they look live.)

### X4-L4 — ECB + CoinGecko `PollInterval` not wired (mismatch)
- **`External.ECB.PollInterval`** — the ECB poller exposes a settable
  `Interval` field (`internal/sources/external/ecb/poller.go`), but
  neither the indexer (`startExternalConnectors` → `externalecb.NewPoller()`)
  nor ops ever sets it from `cfg.ECB.PollInterval`. The knob is inert
  despite a real consumer existing on the poller. MISMATCH.
- **`External.CoinGecko.PollInterval`** — WIRED in the indexer
  (`cmd/stellarindex-indexer/main.go` `p.Interval = …`), DROPPED in the
  ops `buildVerifyExternal` parallel block (calls `NewPoller()` ignoring
  it). Diagnostic-path divergence; minor.
- The WebSocket streamer venues (Binance/Kraken/Bitstamp/Coinbase) share
  the `ExternalVenueConfig.PollInterval` field but have no poll concept —
  inert **by design** (not flagged as a defect, but the shared struct
  makes the field look applicable when it isn't).

### X4-L5 — out-of-schema env vars (invisible to `make docs-config`)
Three secrets are read via raw `os.Getenv`, NOT declared as any `env:`
struct tag, so they never appear in the generated config reference:
- `COINGECKO_API_KEY` (pro) + `COINGECKO_DEMO_API_KEY` — read at
  `cmd/stellarindex-indexer/main.go:835,838` (CoinGecko uses
  `ExternalVenueConfig`, which has no `APIKey` field, by deliberate choice
  to avoid a schema change for the late-2024 CG auth tightening).
- `MASSIVE_API_KEY` — read at `cmd/stellarindex-api/main.go:605` for the
  forex shim (`internal/sources/forex/`), source for the
  `/v1/currencies` FX surface (`massive.com`). No config field exists for
  it at all.
- `STELLARINDEX_PROBE_API_KEY` — read by `stellarindex-sla-probe`
  (`cmd/stellarindex-sla-probe/main.go:198`), which intentionally does NOT
  load `internal/config` at all (flags + env only). Same for
  `stellarindex-migrate` (DSN via `-dsn` flag or `STELLARINDEX_POSTGRES_DSN`).
All three (CG/Massive) are rendered in `stellarindex.env.j2` so they DO
reach r1 — the gap is purely doc-discoverability for operators reading the
generated reference.

### X4-L6 — decorative / by-design orphans
No action needed, listed for completeness:
- `Storage.S3ColdAccessKeyEnv` / `S3ColdSecretKeyEnv` — zero consumers.
  The cold-tier datastore (`internal/pipeline/datastore.go:79-81`)
  intentionally passes no creds (anonymous reads of the public
  `aws-public-blockchain` bucket). If a *private* cold bucket were ever
  configured, auth would silently fail — latent, but matches ADR-0027's
  public-bucket assumption.
- `Region.Name`, `Region.HomeDomain` — no runtime consumer (Region.ID is
  wired into boot-log tags + `RegionName`). Decorative.
- `Stellar.CoreHTTPEndpoint`, `Stellar.HistoryArchiveURL` — validated for
  URL shape (`validate.go:195,201`) but never probed/fetched by any
  binary. The doc strings imply a liveness probe / backfill catchup that
  doesn't read them.
- `Stellar.Network` — only a boot-log field; the *real* network-identity
  consumer is `StellarConfig.Passphrase()` (heavily used:
  indexer/pipeline/ops). Not an orphan, just narrow.
- `Obs.TraceExporter` / `Obs.TraceSample` — validate-only (no OTel
  TracerProvider anywhere); orphan **by design**, self-documented
  ("reserved … ignored in this build"); `validate.go:551-564` rejects
  `otlp` so an operator can't think tracing is on.

---

## Field → consumer matrix (highlights)

Status legend: WIRED = read by runtime code; ORPHAN = no runtime consumer
(validate/Default only); MISMATCH = consumer exists but config value is
dropped or semantics diverge.

### Storage (env-override + ClickHouse focus)

| Field (toml) | Consumer | Status |
|---|---|---|
| `postgres_dsn` (env `STELLARINDEX_POSTGRES_DSN`) | `timescale.Open` callers (indexer/api/~25 ops) | WIRED |
| `redis_addr` | `redisclient.go:57` | WIRED |
| `redis_sentinel_addrs` | `redisclient.go:40-43` (FailoverClient) | WIRED |
| `redis_master_name` | `redisclient.go:42` | WIRED |
| `redis_password_env` (env `STELLARINDEX_REDIS_PASSWORD`) | `redisclient.go:49-50,59` | WIRED |
| `redis_username` | `redisclient.go:48,58` | WIRED |
| `s3_endpoint`/`s3_region`/`s3_bucket_archive`/`s3_bucket_live` | `pipeline/datastore.go:42-43,66,75`, indexer | WIRED |
| `s3_access_key_env` / `s3_secret_key_env` | `trim_galexie_archive.go:143,315-316` | **WIRED but X4-M1 name-vs-value** |
| `s3_cold_endpoint`/`_region`/`_bucket_archive` | `pipeline/datastore.go:79-81` + `ColdTieringEnabled()` | WIRED |
| `s3_cold_access_key_env` / `s3_cold_secret_key_env` | none | **ORPHAN (X4-L6)** |
| `clickhouse_addr` | indexer `:390,484`, api `:809,824` | WIRED |
| `clickhouse_live_sink` | indexer `:386` | WIRED |
| `clickhouse_projector_source` | indexer `:483` (`proj.SetClickHouseSource`) | WIRED |

### Ingestion / Oracle / External (Enabled-gate focus)

| Field | Consumer | Status |
|---|---|---|
| `enabled_sources` | indexer `BuildDispatcher`/`BuildRegistry`, ops `backfill.go` | WIRED |
| `backfill_from_ledger` | indexer `resolveStartLedger` `:426` | WIRED |
| `backfill_batch_size` | validate only | **ORPHAN (X4-L1)** |
| `cursor_store_scheme` | validate only | **ORPHAN (X4-L2)** |
| `live_seam_ledger` | indexer `:534` (archive/live seam) | WIRED |
| `projector.enabled` / `projector.persist_per_source` | indexer `:451,468` | WIRED |
| `oracle.reflector.{dex,cex,fx}_contract` | `pipeline/dispatcher.go`, `projector/registry.go`, ops | WIRED |
| `oracle.redstone.adapter_contract` | `dispatcher.go:129`, `registry.go:220`, ops | WIRED |
| `oracle.band.standard_reference_contract` | `dispatcher.go:138` (ContractCallDecoder), ops | WIRED |
| `oracle.soroswap.factory_contract` / `seed_rpc_endpoint` | ops seed/verify | WIRED |
| `external.<venue>.enabled` (all 11) | indexer `startExternalConnectors`, ops `buildVerifyExternal` | WIRED |
| `external.{exchangeratesapi,polygon_forex,coinmarketcap,cryptocompare}.api_key` (+ env tags) | indexer + ops | WIRED |
| `external.chainlink.{rpc_url(env),feed_map,poll_interval}` | indexer; ops via `backfill_chainlink.go` | WIRED (PollInterval indexer-only) |
| `external.ecb.poll_interval` | poller has `Interval`, never set from config | **MISMATCH (X4-L4)** |
| `external.coingecko.poll_interval` | indexer WIRED, ops verify drops it | **MISMATCH (X4-L4)** |

### Aggregate / Anomaly / Trades / Divergence

| Field | Consumer | Status |
|---|---|---|
| `min_usd_volume` | aggregator `:406` → `orchestrator.dropForMinUSDVolume` | WIRED |
| `vwap_window_seconds` / `twap_window_seconds` | validate only | **ORPHAN (X4-L3)** |
| `outlier_sigma_threshold` | `orchestrator.FilterOutliers` | WIRED |
| `triangulation_enabled` / `triangulations` | aggregator `buildTriangulations` | WIRED |
| `interval_seconds` / `divergence_min_interval_seconds` / `max_trades_per_window` | aggregator → orchestrator | WIRED |
| `disable_class_filter` / `enable_stablecoin_fiat_proxy` | aggregator `:402-403` → orchestrator | WIRED |
| `pairs` / `windows` (`AggregatorPairs/Windows`) | aggregator `:198,212` → orchestrator | WIRED |
| `anomaly.{enabled,thresholds,classifications}` | aggregator `buildAnomalyChecker` | WIRED |
| `anomaly.phase2.{confidence_max_freeze,z_score_min_freeze,source_count_max_freeze}` | aggregator `:398-400` → `phase2_freeze.go` | WIRED |
| `trades.usd_pegged_classic_assets` | indexer (insert USD-vol), aggregator (fiat-proxy), **API (`change24h`)** — 3 domains | WIRED |
| `divergence.*` (threshold/min_sources/timeout/coingecko/chainlink + feed Address/Decimals/Invert) | aggregator + api `buildDivergenceReferences` → `internal/divergence/` | WIRED |

### API / Supply / Metadata / Obs

| Field | Consumer | Status |
|---|---|---|
| `listen_addr`/`auth_mode`/`auth_backend`/`anon_rate_limit_per_min`/`key_rate_limit_per_min` | api main (`:981,640,641,313-317`) | WIRED |
| `external_base_url` | api `:131` (log only) | **DEAD/log-only (X4-M2)** |
| `tls_cert_probe_hosts` | api `:746` (`RunTLSCertProbe`) | WIRED |
| `signup_require_email_verification` | api `:863` (`RequireEmailVerified`) | WIRED |
| `cdn_enabled` / `allowed_origins` / `allow_credentials` / `trusted_proxy_cidrs` | api `:925,298,299,284` | WIRED |
| `prometheus_url` | api `:575-577` (status backend) | WIRED |
| `sep10.{seed_env,jwt_secret_env}` | api `:1349,1353` `os.Getenv(cfg.X)` (read as NAME ✓) | WIRED |
| `sep10.{web_auth_domain,home_domain,challenge_ttl,jwt_ttl}` | api → `auth/sep10/validator.go` | WIRED |
| `api.streaming.{pairs,poll_interval}` | api `:956,961` | WIRED |
| `stripe.signing_secret` (env `STELLARINDEX_STRIPE_WEBHOOK_SECRET`) | api `:402` → `stripe_webhook.go:403` (used as VALUE ✓) | WIRED |
| `dashboard.resend_api_key_env` | api `:1246` `os.Getenv(cfg.X)` (read as NAME ✓) | WIRED |
| `dashboard.{base_url,email_from,magic_link_ttl_minutes,session_ttl_days,cookie_secure,cookie_domain}` | api → `dashboardauth/handlers.go` | WIRED |
| `supply.sdf_reserve_accounts` / `reserve_balances_stroops` | aggregator + ops `supply.go`; dispatcher accounts.Observer | WIRED |
| `supply.aggregator_refresh_{enabled,cadence}` | aggregator `:482,491,510` | WIRED |
| `supply.watched_classic_assets` / `sac_wrappers` / `watched_sep41_contracts` | dispatcher observers + aggregator + api | WIRED |
| `supply.strict_freshness_required` / `stale_component_ledgers_by_asset` | aggregator `:656,658` → `refresher.go` | WIRED |
| `metadata.issuer_home_domains` (via `HomeDomainFor`) | api `:487` (static fallback behind LCM resolver) | WIRED |
| `obs.{metrics_listen,log_level,log_format}` | indexer/aggregator/api + `obs/log.go` | WIRED |
| `obs.trace_exporter` / `obs.trace_sample` | validate only | **ORPHAN by design (X4-L6)** |

---

## Cross-check: example.toml + Ansible vs schema

- **No unknown keys:** every key in example.toml AND the Ansible
  `stellarindex.toml.j2` maps onto a schema field. `load.go:39-48` hard-
  fails on undecoded keys, so this is enforced at boot.
- **Schema → example.toml gap:** 15 schema fields/blocks undocumented in
  example.toml (X4-M3). Several ARE rendered by the Ansible template,
  creating a copy-the-example-and-diverge trap.
- **Stale `auth_mode` comment** in example.toml omits `apikey_optional`
  (the value r1 runs) (X4-M3).
- **Ansible-only overrides** confirmed wired: `s3_cold_*`,
  `[anomaly.phase2] z_score_min_freeze=100`, `[oracle.{redstone,band,
  soroswap}]`, `auth_backend`/`prometheus_url`/`anon|key_rate_limit=6000`,
  `aggregator_refresh_enabled=true`, the full `[supply.sac_wrappers]` +
  `watched_classic_assets` registries, `[divergence.chainlink]` feeds.
- **env file (`stellarindex.env.j2`)** sets `STELLARINDEX_*` overrides
  (DSN, S3 creds, Stripe N/A) AND parallel `AWS_*` (the mask for X4-M1)
  AND the out-of-schema `MASSIVE_API_KEY` / `COINGECKO_DEMO_API_KEY` /
  `CHAINLINK_RPC_URL` (X4-L5).

---

## Verification notes (claims spot-checked directly, not agent-only)

- X4-M1: read `trim_galexie_archive.go:300-327` + `load.go:75-79` +
  `stellarindex.env.j2` directly — override-fires-and-masks chain confirmed.
- X4-L1/L2/L3: `grep -rn <field> --include=*.go cmd/ internal/ | grep -v
  config/ | grep -v _test.go` returned EMPTY for `BackfillBatchSize`,
  `CursorStoreScheme`, `VWAPWindowSeconds`, `TWAPWindowSeconds` — confirmed
  zero runtime consumers.
- X4-M3: programmatic `grep -q` of each schema field against example.toml —
  the 15-field MISSING list is verified, not inferred.
- X4-L5: `MASSIVE_API_KEY` confirmed live at `forex/client.go` +
  `api/main.go:605`; CG keys at indexer `:835,838`.
