# Container packaging

One Dockerfile per binary. The release workflow
(`.github/workflows/release.yml`) builds each on every `v*.*.*` tag
and pushes to `ghcr.io/RatesEngine/<binary>:<tag>` plus `:latest`
on non-prerelease tags.

Local build (any one binary):

```sh
docker build -t ratesengine/ratesengine-api:local -f docker/ratesengine-api.Dockerfile .
```

All binaries (matches `make build-docker`):

```sh
make build-docker
```

## Image shape

- **Builder stage** uses `golang:1.25-alpine` and runs the same
  `go build -trimpath -buildvcs=true -ldflags=...` invocation the
  release workflow does so the locally-built image and the
  CI-released one are byte-equivalent at the binary level.
- **Runtime stage** uses `gcr.io/distroless/static-debian12:nonroot`
  — no shell, no package manager, runs as uid 65532. CA certs are
  baked in (needed for outbound HTTPS to CEX/FX vendors).
- Listening ports: API on 3000, indexer/aggregator metrics on 9464
  / 9465. Ops + migrate + sla-probe don't bind a port.

## Why distroless static (not alpine)

The Go binaries are statically linked (`CGO_ENABLED=0`), so the
runtime image needs nothing from the OS. Distroless's `static`
variant is ~2 MB vs Alpine's ~5 MB, has no shell (no
`exec`-into-prod attack surface), and gets the same OS-CVE
trickle from Google's distroless team that Alpine gets from
Alpine's security team.

The trade-off: no `apk add` / `bash` for "I want to debug this
container live". Acceptable because we have systemd-on-bare-metal
as the primary deploy target — containers are for portable
operator-side use (compose stacks, k8s if/when), not for live
debugging.

## Operator note: this is not the production deploy path

R1 today runs the binaries directly via systemd unit files (see
`/etc/systemd/system/ratesengine-*.service`). These container
images are for:

- Self-hosted operators wanting a docker-compose drop-in
- Future k8s deploys (post-multi-region)
- CI smoke tests of the full stack on tagged builds
