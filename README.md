# Rates Engine

**Status:** Pre-v1. Ingestion + storage + REST API shipped;
aggregator + hardening in progress.
**License:** Apache-2.0.
**Tested against:** Stellar protocol 25.x.

A publicly-accessible, aggregated, real-time and historical price API
for every Stellar asset — classic and SEP-41 Soroban token.

Aggregates on-chain trades from **SDEX, Soroswap, Aquarius, Phoenix,
Comet, Blend**, oracle feeds from **Reflector, Redstone, Band**, plus
CEX + FX + reference aggregators, into one VWAP-first pricing layer
served at p95 ≤ 200 ms. Full since-inception OHLC. Self-hostable.

Built against the [Stellar Prices API RFP](docs/stellar-rfp.md) and
the [Freighter asset-detail RFP](docs/freighter-rfp.md).

---

## If you are an AI agent reading this for the first time

See **[CLAUDE.md](CLAUDE.md)**. It's your orientation map.

---

## Start here

- **Users of the hosted API:** [`docs/getting-started.md`](docs/getting-started.md)
  walks from zero to your first authenticated request in five
  minutes. Rendered at <https://docs.ratesengine.net> post-launch.
- **Reference docs:** generated Redocly output at
  [`docs/reference/api/index.html`](docs/reference/api/index.html)
  (regenerate via `make docs-api`); also published to
  <https://docs.ratesengine.net> by the
  [`api-docs` workflow](.github/workflows/api-docs.yml).
- **Self-hosting:** `make dev` boots the full local stack
  (TimescaleDB + Redis + MinIO). See
  [deploy/docker-compose/dev.yaml](deploy/docker-compose/dev.yaml).
- **Contributors:** [CONTRIBUTING.md](CONTRIBUTING.md).
- **Architects / reviewers:** [docs/discovery/](docs/discovery/) —
  Phase-1 audit artefacts.
  [docs/discovery/engineering-standards.md](docs/discovery/engineering-standards.md)
  is the non-negotiable policy layer.

---

## Layout

See [docs/discovery/repo-structure-plan.md](docs/discovery/repo-structure-plan.md)
for the rationale. Summary:

```
cmd/                   binaries (indexer / aggregator / api / ops / migrate)
internal/              private packages (Go-enforced non-importable)
pkg/                   public surface (client SDK + stable types)
migrations/            TimescaleDB schema migrations
configs/               default + example configs
openapi/               API spec — source of truth for reference docs
deploy/                docker-compose / k8s / baremetal deploy kits
test/                  integration + load + chaos + fixtures
docs/                  architecture / ADR / operations / reference / discovery
```

---

## Core invariants (never violated)

These are the architectural commitments that bind every PR. See
[docs/discovery/decisions.md](docs/discovery/decisions.md) for the
long-form rationale; each becomes a numbered ADR.

1. **i128 amounts never truncate to int64.** Token balances,
   reserves, prices from Soroban are `*big.Int` in Go, `NUMERIC` in
   Postgres, strings in JSON. ADR-0003.
2. **Horizon is not in our architecture.** We don't ingest, proxy,
   or depend on Horizon. ADR-0001.
3. **Self-hosted storage is MinIO (or any S3-compat with
   `endpoint_url`), not local filesystem.** ADR-0002.
4. **Monorepo with one `go.mod`.** ADR-0005.
5. **Validator track post-launch targets three Tier-1 full
   validators with independent history archives.** ADR-0004.

---

## Status

- ✅ Phase 1 discovery complete. 45 audit docs in
  [`docs/discovery/`](docs/discovery/).
- ✅ Repo structure plan, engineering standards, 10-week delivery
  plan locked.
- ✅ Phase 2 ingestion scaffold: Soroswap / Aquarius / Phoenix /
  Reflector sources, TimescaleDB hypertables + continuous
  aggregates, stellar-rpc client, indexer binary with
  Prometheus scrape target.
- ✅ REST API v1 serving `/healthz`, `/readyz`, `/version`,
  `/assets`, `/price`, `/history`, `/ohlc`, `/vwap`, `/twap`,
  `/markets`, `/oracle/latest` behind CORS + per-IP rate limit.
- ⏳ Aggregation engine (VWAP/TWAP cache refresh, cross-source
  divergence detection).
- ⏳ Historical backfill, remaining sources (SDEX / Comet / Blend /
  Redstone / Band / CEX / FX), hardening, launch.

**Production deadline:** 2026-06-30 per
[docs/discovery/delivery-plan.md](docs/discovery/delivery-plan.md).

---

## Contact

- Security: <security@ratesengine.net> (GPG key in
  [SECURITY.md](SECURITY.md))
- Code: [CONTRIBUTING.md](CONTRIBUTING.md)
- General: <hello@ratesengine.net>

---

## License

Apache-2.0. See [LICENSE](LICENSE).
