# Docker + systemd Inventory

## Dockerfiles

| File | Bytes | Base image (FROM) | USER | HEALTHCHECK | Status |
| --- | ---: | --- | --- | --- | --- |
| `docker/ratesengine-aggregator.Dockerfile` | 981 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |
| `docker/ratesengine-api.Dockerfile` | 939 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |
| `docker/ratesengine-indexer.Dockerfile` | 963 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |
| `docker/ratesengine-migrate.Dockerfile` | 971 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |
| `docker/ratesengine-ops.Dockerfile` | 947 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |
| `docker/ratesengine-sla-probe.Dockerfile` | 983 | `FROM golang:1.26-alpine AS builder|FROM gcr.io/distroless/static-debian12:nonroot` | `USER nonroot:nonroot` | `—` | todo |

## systemd units

| File | Type | Notes |
| --- | --- | --- |
| `deploy/systemd/archive-completeness.service` | service | todo |
| `deploy/systemd/archive-completeness.timer` | timer | todo |
| `deploy/systemd/ratesengine-aggregator.service` | service | todo |
| `deploy/systemd/ratesengine-api.service` | service | todo |
| `deploy/systemd/ratesengine-indexer.service` | service | todo |
| `deploy/systemd/sla-probe.service` | service | todo |
| `deploy/systemd/sla-probe.timer` | timer | todo |
| `deploy/systemd/supply-snapshot.service` | service | todo |
| `deploy/systemd/supply-snapshot.timer` | timer | todo |
| `deploy/systemd/verify-archive-tier-a.service` | service | todo |
| `deploy/systemd/verify-archive-tier-a.timer` | timer | todo |
