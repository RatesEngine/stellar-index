---
adr: 0002
title: Self-hosted storage is S3-compatible (MinIO), not local filesystem
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0002: Self-hosted storage is S3-compatible (MinIO), not local filesystem

## Context

Galexie exports `LedgerCloseMeta` batches to a datastore backend.
Three implementations exist in
`go-stellar-sdk/support/datastore/`:

- `gcs.go` — Google Cloud Storage.
- `s3.go` — AWS S3 (any S3-compatible service via `endpoint_url`).
- `filesystem.go` — local directory.

The filesystem backend looks like the obvious default for a
self-hosted deployment. **It is not.** An audit of
`filesystem.go` revealed:

1. Galexie attaches **nine metadata keys** to every uploaded
   object (`start-ledger`, `end-ledger`, `start-ledger-close-time`,
   `end-ledger-close-time`, `protocol-version`, `core-version`,
   `network-passphrase`, `compression-type`, `version`). The
   filesystem backend **silently drops them all**.
2. The implementation itself warns in its docstring:

   > "Concurrent writes to the same file path are not safe and may
   > result in data corruption. Callers must ensure proper
   > synchronization when writing to the same path from multiple
   > processes."

3. SDF's own Galexie `config.example.toml` explicitly says the
   filesystem backend is intended for development/testing and is
   "not recommended for production use."

We also have a real production incident in adjacent tooling
(Stellar Expert) caused by a different DB-schema-level correctness
bug; the broader lesson is "don't silently truncate or drop
metadata."

## Decision

**Self-hosted Stellar Index deployments use an S3-compatible object
store — MinIO by default — not the local filesystem backend.**

- MinIO runs on our own colo hardware (erasure-coded multi-drive)
  for the primary tier.
- Async replication from MinIO → a cloud S3 bucket handles
  backup + disaster recovery.
- Consumers point at MinIO via the Galexie S3 backend with
  `endpoint_url` overridden. SDF's own config example explicitly
  lists Cloudflare R2 as an S3-compatible example; MinIO works
  the same way.
- Third-party operators running their own Stellar Index instance
  are free to use any S3-compatible backend (AWS S3, GCS,
  Cloudflare R2, Backblaze B2, Wasabi) — all are supported by
  the SDK code path we rely on.

The filesystem backend is allowed **only in local development** —
explicitly documented as dev-only in our
`deploy/docker-compose/` config.

## Consequences

**Positive**

- All nine metadata keys preserved on every uploaded object.
  Downstream consumers can filter / audit / version without
  opening the zstd-XDR body.
- Atomic conditional writes (S3's `If-None-Match`-equivalent) —
  multi-writer-safe.
- Network-accessible from any consumer machine, unlike local FS
  which requires NFS/SMB (and re-introduces concurrency risk).

**Negative**

- Slightly higher deployment complexity — operators stand up a
  MinIO (or equivalent) alongside Postgres + Redis.
- Resource cost — MinIO consumes a little memory and CPU on the
  colo box.

**Operational impact**

- Our backup + DR story depends on S3-level semantics (bucket
  versioning, lifecycle rules, replication). Simpler to reason
  about than filesystem snapshots.
- Monitoring includes MinIO cluster health (capacity, erasure-code
  heal state, replication lag).

**Downstream design impact**

- Our ingest consumer reads the Galexie-produced objects via the
  same SDK `datastore` abstraction — the backend is swappable at
  config time, not at code time.
- `rs-stellar-archivist`'s Rust rewrite does not write to S3
  (only reads); we mirror archives to local filesystem then
  `aws s3 sync` / `mc mirror` into MinIO.

## Alternatives considered

1. **Local filesystem.** Rejected: silent metadata loss +
   multi-writer unsafety + SDF's own dev-only marking.
2. **Ceph RGW self-hosted.** Rejected: operational complexity
   dwarfs MinIO for our scale; same S3 API compatibility but
   much heavier footprint.
3. **AWS S3 only (no self-hosted option).** Rejected: our OSS
   commitment requires a fully self-hostable path that doesn't
   require a cloud account.
4. **Force every operator to use GCS** (matching SDF's public
   bucket). Rejected: cloud vendor lock-in, plus disadvantages
   our colo-based deployment.

## References

- Datastore filesystem backend source:
  `go-stellar-sdk/support/datastore/filesystem.go`.
