# Rates Engine — local dev stack

Docker Compose bundle that brings up Postgres+TimescaleDB, Redis,
and MinIO on your workstation. Everything a developer needs to
run the binaries locally and apply migrations end-to-end.

**Not production-shaped.** No HA, no TLS, no backup/restore.
Production topology lives in
[docs/architecture/ha-plan.md](../../docs/architecture/ha-plan.md).

## Prerequisites

- Docker Desktop (Mac/Windows) or Docker Engine 24+ (Linux).
- Docker Compose v2 (bundled with Docker Desktop; standalone via
  `docker-compose-plugin` on Linux).

## First-run

From the repo root:

```sh
# Copy the env defaults and adjust if you hit port collisions:
cp deploy/docker-compose/.env.example deploy/docker-compose/.env
$EDITOR deploy/docker-compose/.env       # optional

# Bring up the stack:
make dev

# Wait ~15s for TimescaleDB to come ready, then apply migrations:
export RATESENGINE_POSTGRES_DSN="postgres://ratesengine:ratesengine-dev@localhost:5432/ratesengine?sslmode=disable"
make db-migrate-up

# Verify state:
make db-migrate-status
```

Expected output after `db-migrate-up`:

```
migrated to version 2 (dirty=false)
```

## Services

| Service     | Purpose                        | Host port | Inside container |
| ----------- | ------------------------------ | --------- | ---------------- |
| timescale   | Postgres 15 + TimescaleDB      | 5432      | 5432             |
| redis       | Redis 7, AOF on, maxmem 512 MB | 6379      | 6379             |
| minio       | S3-compatible object store     | 9000      | 9000 (API)       |
| minio       | console                        | 9001      | 9001             |
| minio-init  | one-shot bucket creator        | —         | —                |

Default buckets created on first bring-up:
- `galexie-live` — live Galexie exports (writable)
- `galexie-archive` — historical bucket (write-mostly)
- `backups` — pgBackRest target in dev

## Teardown

```sh
make dev-teardown           # stops containers + removes named volumes
```

Or more surgically:

```sh
docker compose -f deploy/docker-compose/dev.yaml down            # stop, keep data
docker compose -f deploy/docker-compose/dev.yaml down -v         # stop + wipe
```

## Common operations

Apply schema from scratch:

```sh
make db-migrate-up
```

Roll back last migration:

```sh
make db-migrate-down
```

Connect with `psql`:

```sh
PGPASSWORD=ratesengine-dev psql -h localhost -U ratesengine ratesengine
```

Connect to Redis:

```sh
redis-cli -h localhost -p 6379
```

Browse MinIO:

```sh
open http://localhost:9001
# login with MINIO_ROOT_USER / MINIO_ROOT_PASSWORD from .env
```

Inspect running logs:

```sh
docker compose -f deploy/docker-compose/dev.yaml logs -f timescale
docker compose -f deploy/docker-compose/dev.yaml logs -f redis
docker compose -f deploy/docker-compose/dev.yaml logs -f minio
```

## Troubleshooting

- **`Error: port 5432 already in use`** — Postgres running on your
  host. Either stop it, or override `POSTGRES_PORT=5433` in `.env`
  and update `RATESENGINE_POSTGRES_DSN` accordingly.
- **`FATAL: could not access file "timescaledb"`** — you used a
  vanilla `postgres:15` image instead of `timescale/timescaledb`.
  Check `TIMESCALE_IMAGE_TAG` in `.env`.
- **`minio: no such file or directory`** — MinIO needs a data
  directory with write permission; the compose file mounts a named
  volume, so this usually means Docker Desktop lost its volume
  store. Run `make dev-teardown` then `make dev`.
- **`dirty=true` after a migration** — a migration started but
  didn't finish. Inspect the log, manually fix, and use
  `ratesengine-migrate -dsn ... force $VERSION` (unimplemented
  today; track against TODO(#0)) or clean slate with teardown.

## References

- Migrations lives in [`migrations/`](../../migrations).
- Production HA shape: [`docs/architecture/ha-plan.md`](../../docs/architecture/ha-plan.md).
- Archival node for stellar-core + Galexie (NOT in this dev
  stack — runs natively, see `configs/ansible/`):
  [`docs/architecture/infrastructure/archival-node-spec.md`](../../docs/architecture/infrastructure/archival-node-spec.md).
