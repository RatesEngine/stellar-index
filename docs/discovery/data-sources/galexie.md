# Galexie

**Status:** ✅ Recommended for historical backfill and bounded re-exports.
⚠️ Unsuitable on its own for 30-second hot-path freshness (file-batched
object-store writes add latency; see *Latency profile* below).

**Repo:** <https://github.com/stellar/stellar-galexie>
**Release used for audit:** checked against `main` at clone time
(2026-04-22). Latest release noted on GitHub UI: `v26.0.0` (2026-04-01).
**Verified against:** `main.go`, `cmd/main.go`, `config/config.example.toml`,
`internal/config.go`, `internal/uploader.go`, `go.mod`.

## What it is

> "Converts ledger meta data from Stellar network into static data and
> exports it [to] remote data storage."
> — `cmd/main.go:33`

Galexie runs a **captive-core** sub-process, reads `LedgerCloseMeta`
records out of it, serialises and zstd-compresses them, and uploads
ledger-range-sized objects to an external data store (GCS, S3, S3-compat,
or a local filesystem). It is the Stellar-Foundation-maintained producer
side of the Composable Data Platform (CDP).

## Subcommands (verified from `cmd/main.go`)

| Subcommand       | Range       | Purpose                                                                                 |
| ---------------- | ----------- | --------------------------------------------------------------------------------------- |
| `scan-and-fill`  | bounded     | Scan the `[start, end]` range and export **only missing** ledger objects.              |
| `append`         | bounded OR unbounded | Start from first missing ledger ≥ `start` and keep exporting. Unbounded = live-follow (runs forever). |
| `replace`        | bounded     | Re-export every ledger in `[start, end]`, overwriting existing objects.                |
| `detect-gaps`    | bounded     | Report missing ranges only (no export). Optional `--output-file` JSON report.           |
| `load-test`      | bounded OR unbounded | Synthetic ingestion load test over a fixture `ledgers-path` file.              |
| `version`        | —           | Prints `stellar-galexie <version>`.                                                     |

All export subcommands share flags:

```
-s, --start       uint32  Starting ledger (inclusive), must be > 1.
-e, --end         uint32  Ending ledger (inclusive), 0 = unbounded for `append`.
    --config-file string  Path to TOML config (default "config.toml").
```

`append` is the only mode where `Resumable() == true`
(`internal/config.go:58-60`).

## Config file schema (verified)

Source: `config/config.example.toml` and the `Config`/`StellarCoreConfig`
structs in `internal/config.go:95-124`.

```toml
admin_port = 6061                           # Prometheus metrics endpoint; default 6061

[datastore_config]
type = "GCS"                                # "GCS" | "S3" | "Filesystem"

[datastore_config.params]
# For GCS:
destination_bucket_path = "bucket/subpath"
# For S3-compatible (AWS S3, Cloudflare R2, MinIO, ...):
# destination_bucket_path = "bucket/subpath"
# region                  = "us-west-1"
# endpoint_url            = "https://<account-id>.r2.cloudflarestorage.com"   # OR MinIO endpoint
# For Filesystem (DEV ONLY — see warning below):
# destination_path        = "/mnt/data/galexie"

[datastore_config.schema]
ledgers_per_file    = 1                     # example default; we should tune
files_per_partition = 64000

[stellar_core_config]
network                 = "pubnet"          # "pubnet" | "testnet" | "futurenet" (or omit & set all 3 below)
# network_passphrase        = "Public Global Stellar Network ; September 2015"
# history_archive_urls      = ["https://history.stellar.org/prd/core-live/core_live_001"]
# captive_core_toml_path    = "captive-core.cfg"
# stellar_core_binary_path  = "/usr/bin/stellar-core"
# checkpoint_frequency      = 64           # defaults to historyarchive.DefaultCheckpointFrequency
# storage_path              = "/var/lib/galexie/core"

# Optional:
# user_agent = "ratesengine-galexie/1.0"
```

Env-var mapping: every CLI flag is bound via Viper with
`KebabToConstantCase`, so `--start` → `START`, `--config-file` →
`CONFIG_FILE`, etc. (`cmd/main.go:205-217`.)

### Filesystem backend — explicitly dev-only

Config example comments verbatim:

> "Filesystem storage is not recommended for production use. It is
> intended for development and testing purposes only. This implementation
> does not support storing metadata."
> — `config/config.example.toml:29-31`

Implication for us: if we want to keep data local on our own colo
storage, we run MinIO (or Ceph RGW) and use the S3 backend with
`endpoint_url`, not the Filesystem backend.

### S3-compatible backends confirmed

The example file explicitly shows **Cloudflare R2** as an example of an
S3-compatible target by setting `endpoint_url`. MinIO and Backblaze B2's
S3 endpoint work the same way. This lets us keep data 100% on our own
infrastructure without touching AWS/GCP.

## Captive-core dependency

Galexie **embeds** a captive-core run (`internal/config.go:237-278`). In
Docker images this is fine because the image bundles `stellar-core`.
Outside Docker we need `stellar-core` on `PATH` or set
`stellar_core_binary_path`.

When `network = pubnet|testnet|futurenet` is set, Galexie uses the
baked-in default captive-core toml, passphrase, and history archive URLs
from the SDK (`ledgerbackend.PubnetDefaultConfig` etc.). Overrides via the
other three keys take precedence (`internal/config.go:317-351`).

`EmitVerboseMeta: true` is hardcoded (`internal/config.go:256`), so
exported `LedgerCloseMeta` includes full verbose data — required for
downstream consumers doing anything non-trivial.

## Compression

Hardcoded `const compressionType = "zstd"` (`internal/config.go:79`) with
a `// user-configurable in the future.` comment. So right now:

- Files are zstd-compressed XDR.
- The comment tells us SDF plan to make it configurable; we won't bet on
  that for us but we should watch.

## Output object format

The upload path (`internal/uploader.go:91-141`) calls
`compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, &metaArchive.Data)`
and pushes via `dataStore.PutFile` or `PutFileIfNotExists`. So each
object is:

- A zstd-compressed, `xdr`-serialised `datastore.LedgerMetaArchive` struct
  (which wraps `[]LedgerCloseMeta`).
- Key layout is determined by `ledgers_per_file` / `files_per_partition`
  via the shared `datastore` helper in `go-stellar-sdk`.

## Prometheus metrics

Exposed on `admin_port` (default `6061`):

- `galexie_uploader_put_duration_seconds{ledgers,already_exists}`
- `galexie_uploader_object_size_bytes{ledgers,already_exists,compression}`
- `galexie_uploader_latest_ledger`

These are the key SLO signals for us — `latest_ledger` lag plus upload
latency percentiles are exactly what we need for monitoring.

## Dependency on `go-stellar-sdk`

`go.mod` pins `github.com/stellar/go-stellar-sdk v0.4.0`. This is the
**new** SDK that replaced the archived `stellar/go` monorepo. All the
heavy lifting (datastore backends, compressxdr, ledgerbackend,
historyarchive) lives there.

Go 1.25 is required (`go.mod:3`).

## Latency profile (what limits us)

Galexie writes one object per `ledgers_per_file` batch. With pubnet's ~5
second ledger close time and `ledgers_per_file = 1`, each ledger creates
an object roughly 5 s after close — plus network upload to S3/GCS, which
is typically 100 ms to a few seconds.

So **best-case end-to-end freshness via Galexie is ~6–10 s** from ledger
close to object availability, assuming we also consume via a polling
reader. That **is** inside the 30-second RFP freshness SLA, but only
barely, and only if `ledgers_per_file = 1`. Any batching (e.g. the
example's `ledgers_per_file = 1` is just an example value — we'd need to
confirm SDF's public data lake setting) trades freshness for object-count
efficiency.

**Decision heuristic:** for the hot path we'll prefer a direct live feed
(stellar-rpc subscription or captive-core → memory) and use Galexie for
durable historical storage + backfill. We never want our current-price
API to block on object-store reads.

## Fit for Rates Engine

**Use it for:**
- Since-inception historical backfill (bounded `scan-and-fill` or
  `append --start N --end M`).
- Immutable, replayable audit trail of ledger meta for reprocessing when
  we change aggregation rules.
- Feeding our reference Postgres / Parquet indexer (see
  [stellar-ledger-data-indexer.md](stellar-ledger-data-indexer.md)).
- Disaster-recovery re-seed: if our live indexer loses state, we rebuild
  from the data lake.

**Do not use it for:**
- Primary 30-second freshness path. That runs off our own stellar-rpc
  and/or a direct captive-core stream (see
  [archival-nodes.md](archival-nodes.md)).

## Open items still requiring verification

- [ ] Benchmark export throughput from our colo captive-core on the R640.
      Target: match pubnet ledger close cadence indefinitely.
- [ ] Determine optimal `ledgers_per_file` / `files_per_partition` given
      S3 request-per-second pricing and downstream consumer read patterns.
      Likely `ledgers_per_file = 1` for freshness, `files_per_partition`
      tuned to keep partitions < ~5k objects.
- [ ] Confirm MinIO works with an `endpoint_url` and path-style bucket
      addressing (Cloudflare R2 is confirmed; MinIO should be the same
      AWS SDK path). Do a local MinIO smoke test.
- [ ] Read `go-stellar-sdk/support/datastore` to capture the **object
      key naming scheme** verbatim (needed for downstream readers).
- [ ] Decide retention for the live data lake — indefinite (required for
      "all-time" OHLC regeneration) or 1yr rolling with separate archival
      cold storage.

## Related docs

- [stellar-archivist.md](stellar-archivist.md) — seed the captive-core
  local history archive from SDF's public archive, before Galexie starts.
- [composable-data-platform.md](composable-data-platform.md) — CDP
  architecture.
- [stellar-ledger-data-indexer.md](stellar-ledger-data-indexer.md) —
  reference downstream consumer.
- [withobsrvr-cdp-pipeline-workflow.md](withobsrvr-cdp-pipeline-workflow.md) — withObsrvr's
  multi-source, multi-sink pipeline built on top of Galexie data.
