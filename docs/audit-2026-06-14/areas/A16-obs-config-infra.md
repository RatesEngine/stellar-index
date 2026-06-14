# A16 — obs / config / notify / incidents / misc infra

Read-only audit. Area scope: `internal/obs/`, `internal/config/`,
`internal/notify/`, `internal/incidents/`, `internal/customerwebhook/`,
`internal/obstest/`, `internal/stellarrpc/`, `internal/version/` (all
`.go`, incl. tests).

Audited against: D1 (correctness — config loading/schema/validation/
defaults; metric naming + `init()` registration; paired `*Total` /
`*DurationSeconds` pattern; notify `Sender` Resend+Noop; customerwebhook
HMAC + backoff/retry drain), D2 (stellarrpc is diagnostics-only, not prod
ingest; defaults match docs), D3 (secret fields not logged; Noop magic-link
dev gating), D4 (webhook drain + metric-vec concurrency), D9 (config doc-tag
accuracy / generated docs in sync; config-field orphan flags).

**Files read: 42** (every `.go` in scope) + 6 cross-reference files
(`internal/pipeline/datastore.go`, `internal/pipeline/sink.go`,
`internal/platform/webhook.go`, `cmd/stellarindex-ops/trim_galexie_archive.go`,
`internal/events/event.go`, `docs/reference/config/README.md`).

Verification run: `go test` over all eight packages → **all pass**;
`go run ./cmd/stellarindex-ops docs-config` diff vs checked-in reference →
**IN SYNC**; all 76 metric vars confirmed registered in `init()`; env-tag
vs `ApplyEnvOverrides` coverage cross-checked.

---

## Findings

| ID | Sev | Dim | File:line | Title |
|----|-----|-----|-----------|-------|
| A16-01 | High (latent) | D1/D3 | internal/config/load.go:75-79 ↔ cmd/stellarindex-ops/trim_galexie_archive.go:305-319 | S3 access/secret env fields have two contradictory consumers (name-vs-value); env override silently breaks explicit S3 creds |
| A16-02 | Medium | D1/D9 | internal/config/config.go:436-437 ↔ internal/pipeline/datastore.go:75-86 | `s3_cold_access_key_env` / `s3_cold_secret_key_env` are orphan config fields — never read; doc implies they gate private-bucket access |
| A16-03 | Low | D9 | internal/config/config.go:636 | Stale prose in `SignupRequireEmailVerification` doc comment ("Default false…") contradicts the actual default (`true`) |
| A16-04 | Low | D9 | internal/obs/metrics.go:701 | `SourceInsertErrorsTotal` `Help:` lists only `trade/oracle/panic`; emitted `kind` set also includes `unhandled`, `blend_*`, `comet_liquidity` (Help feeds `make docs-all`) |
| A16-05 | Low | D9 | internal/obs/metrics.go:1016 | `CustomerWebhookDeliveryAttemptsTotal` `Help:` is "labelled by outcome" only; the 10-value outcome enum lives only in the Go doc-comment, not the generated-docs Help string |
| A16-06 | Low | D1 | internal/config/validate.go:341-371 | `aggregate.interval_seconds` / `divergence_min_interval_seconds` / `max_trades_per_window` have no range validation; a negative value passes Validate (consumer-time `0→default` fallback only catches zero) |
| A16-07 | Info | D1 | internal/customerwebhook/worker.go:274-279 | 3xx responses are classed as transient-retry; combined with `CheckRedirect: ErrUseLastResponse`, a permanent 301/308 to a customer's new URL retries to exhaustion rather than following or terminating |
| A16-08 | Info | D9 | internal/config/config.go:924-925 | `obs.trace_exporter` / `obs.trace_sample` are documented-as-reserved orphans (no consumer); intentional, Validate() rejects `otlp` so operators aren't misled — noted for completeness |

No Critical findings.

---

## Critical / High detail

### A16-01 (High, latent) — S3 cred env field has two contradictory readers

`StorageConfig.S3AccessKeyEnv` / `S3SecretKeyEnv`
(`config.go:419-420`) are documented + defaulted as env-var **NAMES**:

```go
S3AccessKeyEnv string `... doc:"Env var holding S3 access key ID." env:"STELLARINDEX_S3_ACCESS_KEY" default:"STELLARINDEX_S3_ACCESS_KEY"`
```

The only direct consumer, `buildS3Client`
(`cmd/stellarindex-ops/trim_galexie_archive.go:305-319`), correctly treats
the field as a **name** and resolves it at call time:

```go
ak := os.Getenv(accessKeyEnv)   // accessKeyEnv == "STELLARINDEX_S3_ACCESS_KEY"
sk := os.Getenv(secretKeyEnv)
if ak != "" && sk != "" { awsCfg.Credentials = ...StaticCredentials(ak, sk, "") }
```

But `ApplyEnvOverrides` (`load.go:75-79`) treats the field as a slot for
the **value** — the same pattern as `STELLARINDEX_POSTGRES_DSN`:

```go
if v := os.Getenv("STELLARINDEX_S3_ACCESS_KEY"); v != "" {
    c.Storage.S3AccessKeyEnv = v   // overwrites the NAME with the SECRET VALUE
}
```

Failure path: the `trim-galexie-archive` / `rehydrate-galexie-archive` ops
commands all load via `config.LoadWithEnv` (which runs `ApplyEnvOverrides`,
confirmed at `cmd/stellarindex-ops/main.go` — 8 `LoadWithEnv` call sites).
If an operator follows the secret-by-value convention used everywhere else
and sets `STELLARINDEX_S3_ACCESS_KEY=AKIA…`:

1. `ApplyEnvOverrides` sets `S3AccessKeyEnv = "AKIA…"`.
2. `buildS3Client` then calls `os.Getenv("AKIA…")` → `""`.
3. `ak == ""` → static-credential branch is skipped → the S3 client
   silently falls back to the AWS default chain / anonymous.

The cold-tier archive S3 client then either fails opaquely or reads
nothing, depending on bucket ACL. Latent today only because (a) the indexer
hot-path datastore (`internal/pipeline/datastore.go:40-44`) does **not**
pass these fields at all — it relies on the SDK default chain via
`AWS_ACCESS_KEY_ID` — so live ingest is unaffected; and (b) the
trim/rehydrate commands are rare operator actions. No test exercises the
env-override path for these fields (`validate_test.go` only sets them to
`""`). Recommend: decide on one semantics. Either drop the two
`ApplyEnvOverrides` arms (keep field-as-name, the documented contract), or
rename/redoc the fields as value-slots and have `buildS3Client` use them
directly.

---

## Medium detail

### A16-02 (Medium) — cold-tier S3 cred env fields are orphans

`S3ColdAccessKeyEnv` / `S3ColdSecretKeyEnv` (`config.go:436-437`,
`env:""`) have **zero** consumers (grep across `cmd/ internal/ pkg/`,
excluding tests + config = 0 hits). The cold `DataStoreConfig` built in
`internal/pipeline/datastore.go:76-85` sets only `destination_bucket_path`
/ `region` / `endpoint_url` — never access/secret keys. The field docs say
"Empty = anonymous reads (correct for public buckets)", which is true today
because the canonical cold target is the public `aws-public-blockchain`
bucket. But an operator who points the cold tier at a **private** bucket
and dutifully sets these env-name fields gets no effect — the cold reads
stay anonymous and fail with no config-level signal. Either wire the fields
into the cold `Params` map or document them explicitly as currently
inert/reserved (they read as live knobs in the generated config reference).

---

## Lower-severity notes

- **A16-03 (Low, D9):** `config.go:636` doc comment opens "Default false to
  preserve the pre-F-1218 wire contract" then says "Default true
  (2026-05-13)". The `default:"true"` tag and `Default()` (line 952) both
  set `true`, and `TestDefault_MatchesStructTags` enforces that lockstep —
  so the runtime is correct; only the leading prose sentence is stale.

- **A16-04 / A16-05 (Low, D9):** Two metric `Help:` strings undercount
  their label vocabulary. `SourceInsertErrorsTotal.Help` says
  "trade/oracle/panic" but `internal/pipeline/sink.go` emits `panic`,
  `unhandled`, `blend_auction`, `blend_position`, `blend_emission`,
  `comet_liquidity` as `kind` values. `CustomerWebhookDeliveryAttemptsTotal.Help`
  is just "labelled by outcome" while the 10-value enum lives only in the
  Go doc-comment. Both `Help` fields feed `make docs-all` metric reference;
  the richer doc-comment block does not. Cosmetic.

- **A16-06 (Low, D1):** `AggregateConfig.validate` checks `VWAP/TWAP > 0`
  and `MinUSDVolume >= 0` / `OutlierSigma > 0`, but `IntervalSeconds`,
  `DivergenceMinIntervalSeconds`, and `MaxTradesPerWindow` are unchecked.
  Their docs promise "0 falls back to the library default", so the
  consumer-side guard catches zero — but a negative value would slip
  through Validate and reach the consumer (e.g. `time.NewTicker` with a
  negative duration panics). Narrow window; operator would have to type a
  negative on purpose.

---

## Verified-correct list (CORRECT)

- **Metric registration completeness (D1):** all 76 declared `*Vec` /
  scalar metric vars in `metrics.go` are registered in
  `registerAppMetrics()`'s `MustRegister` call (init split out for funlen).
  No orphan-declared metric. Confirmed by set-diff of declared-vs-registered.
- **Non-default Registry (D1/D4):** `obs.Registry` is a private
  `prometheus.NewRegistry()` (not the global default), so tests get isolated
  registries; Go + process collectors are explicit opt-in. Metric vecs are
  the prometheus client's own concurrency-safe types — no data races at
  observation sites.
- **F-0033 zero-seeding (D1):** bounded-cardinality counters whose alert
  rules use `rate()`/`increase()` (`AggregatorTriangulationsTotal`,
  `StripePlatformSyncErrorsTotal`, `ChLiveSinkLedgersTotal`) are pre-seeded
  with their full label set at init; `AggregatorFXSnapFallbackTotal` is
  correctly left emit-on-error (unbounded `leg` label). Pinned by
  `TestZeroSeed_F0033`.
- **Paired `*Total` / `*DurationSeconds` pattern (D1):** every duration
  histogram (`CustomerWebhookDeliveryDurationSeconds`,
  `DivergenceRefreshDurationSeconds`, `AggregatorSupplyRefreshDurationSeconds`,
  `AnomalyFreezeRecoverySweepDurationSeconds`,
  `IngestGapDetectorDurationSeconds`, `ProjectorCycleDurationSeconds`) is
  labelled by the same `outcome` axis as its counter twin; buckets sized to
  the real worst-case (e.g. detector extended to 600s for r1's ~300s
  soroban_events scan). Naming follows `stellarindex_` prefix except the
  intentionally-portable `http_*` and language-native `go_*`/`process_*`.
- **HTTP middleware correctness (D1):** route-pattern capture survives the
  `WithContext` shadow-copy chain via the planted `*routeCapture` pointer
  (`CaptureRoute` innermost); 499 client-abort labelling; synthetic-UA skip
  (`stellarindex-{smoke,probe,prewarm}/`); streaming-route duration skip;
  success-only histogram excludes 5xx + 499 (F-0105). All five behaviours
  pinned by dedicated tests.
- **Config loader (D1):** `Default()` → TOML decode → undecoded-key hard
  error (typo guard) → `Validate()`; `LoadWithEnv` re-validates after env
  overrides so a malformed env value fails fast with `ErrInvalidConfig`.
  `TestValidate_DefaultPasses` + `TestLoadReader_*` cover the path.
- **Default ↔ doc-tag lockstep (D9):** `TestDefault_MatchesStructTags`
  (F-1327 backstop) walks every `default:` tag and asserts `Default()`
  produces exactly that value — this is what would have caught the
  `persist_per_source` projector foot-gun. Generated config reference is in
  sync with the struct tags (verified by regenerate-and-diff).
- **Env-override coverage (D3):** all eight non-empty `env:` tags
  (`STELLARINDEX_POSTGRES_DSN`, `STELLARINDEX_REDIS_PASSWORD`,
  `STELLARINDEX_STRIPE_WEBHOOK_SECRET`, the 5 vendor keys) have matching
  `ApplyEnvOverrides` arms. Secrets are env-name-referenced in the TOML
  schema, never inlined; `schema.go`/`EmitMarkdown` surfaces only the env
  var *name* column, never a value.
- **Validation depth (D1):** per-section validators cover region-id pattern,
  network passphrase, DSN scheme, host:port, RPC duplicate/URL, S3
  all-or-nothing + DNS bucket pattern, source-name known/duplicate/empty,
  C-strkey oracle addresses, Phase-2 freeze bounds, CIDR parsing, trace
  exporter reserved-reject, and the cross-section "enabled source needs its
  contract address". `Supply.Validate()` is wired (G19-02 closed). Rich
  negative-case table in `validate_test.go`.
- **notify Sender (D1/D3):** `Sender` iface documented concurrency-safe;
  `ResendSender` constructor rejects empty key (fail-loud), maps 4xx→
  `ErrProviderRejected` / 5xx+network→`ErrTransient`, drains+closes the
  response body, body-peek bounded by `io.LimitReader(8KiB)`, honours
  `Idempotency-Key` header. `NoopSender` runs the same `validate()` and is
  mutex-guarded. Magic-link-in-logs leakage is gated correctly: the Noop
  path is selected only when `resend_api_key_env` resolves empty (dev), and
  the magic-link plaintext lands in the `NoopSender.Sent` slice, not in any
  log line — the package doc-comment ("magic-link tokens viewable via the
  in-memory Sent slice") matches the implementation.
- **customerwebhook HMAC + drain (D1/D4):** `signHMACSHA256` is correct
  HMAC-SHA-256 over the payload; the `wh.SecretHash` field is documented in
  `platform/webhook.go:46-81` as holding the raw signing key (mis-named for
  a column-rename-avoidance reason — verified, not a bug). SSRF guard
  re-resolves the host at dial time (DNS-rebinding defence, F-1245) and
  disables redirect-following. Backoff is `30s << (n-1)` capped at 1h,
  bounded by `MaxAttempts=15` (max shift 14 → no int64 overflow; clamp
  catches it regardless). Single poll-loop concurrency model is documented;
  idempotent `MarkDelivered`/`MarkAttemptFailed` make a second worker
  cosmetically harmless. `Fanout.Publish` is best-effort + `json.Valid`
  guards the payload + nil-Fanout short-circuits. Metric emission covers
  every branch incl. `list_error` / `mark_error` / `build_error` (with a
  zero-duration histogram sample so the bucket exists).
- **incidents (D1):** read-only embed (`//go:embed data/*.md`), no admin
  write path; per-file parse failures are logged + skipped (one bad post
  can't break the feed); `_`-prefixed files skipped; frontmatter split is
  state-machine-correct with a 1 MiB scanner buffer; RFC3339-then-date
  timestamp fallback.
- **stellarrpc NOT in prod ingest (D2):** package doc enumerates the exact
  allow-list (rpc-probe diagnostic, soroswap factory-seed, fixture scripts);
  no `BackfillRange` / `StreamLive` methods exist; `internal/events/event.go`
  references stellarrpc only in comments (the type moved *out* of stellarrpc
  — stellarrpc depends on events, not the reverse). `scripts/ci/lint-imports.sh`
  rule `A/no-rpc-in-ingest` is the structural guardrail.
- **version (D1):** ldflags-injected `Version`/`BuildDate` with safe
  `"dev"`/`"unknown"` fallbacks; `Commit`/`Dirty` from
  `debug.ReadBuildInfo()` VCS settings; no secrets.
- **obstest (D1):** `HistogramSampleCount` correctly sums `_count` across
  matching-label child series via `Collect` + DTO write — exists because
  `HistogramVec.WithLabelValues` returns an `Observer`, not a `Collector`.
