# Rates Engine

**Status:** Pre-v1. Core system shipped end-to-end (ingestion +
storage + REST API + aggregator: VWAP/TWAP, triangulation,
anomaly response, multi-factor confidence, freeze policy, supply
pipeline). r1 (Hetzner) live with all three application services
running against the historical galexie archive (2026-05-03
first-bringup). Remaining launch blockers are operational
(R2/R3 multi-region provisioning, status page, external
security review, customer demo, production cutover).
**License:** Apache-2.0.
**Tested against:** Stellar pubnet protocol 23 (post-P23 / CAP-67 unified events).

A publicly-accessible, aggregated, real-time and historical price API
for every Stellar asset — classic and SEP-41 Soroban token.

Aggregates on-chain trades from **SDEX, Soroswap, Aquarius, Phoenix,
Comet**, oracle feeds from **Reflector, Redstone, Band**, plus
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
cmd/                   binaries (indexer / aggregator / api / ops / migrate / sla-probe)
internal/              private packages (Go-enforced non-importable)
pkg/                   public surface (client SDK + stable types)
migrations/            TimescaleDB schema migrations
configs/               example.toml + Ansible roles (configs/ansible/)
openapi/               API spec — source of truth for reference docs
deploy/                docker-compose (dev) / systemd (production) / monitoring (Prometheus rules) / status-page
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
- ✅ Phase 2 ingestion scaffold: SDEX / Soroswap / Aquarius /
  Phoenix / Comet / Reflector / Redstone / Band plus the external
  CEX + FX fleet, TimescaleDB hypertables + continuous
  aggregates, and the `ledgerstream -> dispatcher` indexer binary
  with Prometheus scrape target.
- ✅ REST + SSE API v1 surface (full list at
  [`docs/reference/api/index.html`](docs/reference/api/index.html)):
  pricing (`/price`, `/price/batch`, `/price/tip`, `/vwap`, `/twap`,
  `/observations`), historical (`/history`, `/history/since-inception`,
  `/ohlc`, `/chart`), catalogue (`/assets`, `/assets/{id}`, `/markets`,
  `/pairs`, `/sources`), oracle passthrough (`/oracle/latest`,
  `/oracle/prices`, `/oracle/lastprice`, `/oracle/x_last_price`),
  account self-service (`/account/me`, `/account/usage`,
  `/account/keys`), SEP-10 web auth (`/auth/sep10/challenge`,
  `/auth/sep10/token`), SSE streams (`/price/stream`,
  `/price/tip/stream`, `/observations/stream`), plus operator
  endpoints (`/healthz`, `/readyz`, `/version`, `/metrics`).
  Behind CORS, subject-aware rate limit (anon-IP + key-tier),
  trusted-proxy CIDR allow-list, and per-route Cache-Control with
  CDN-tier `s-maxage` gated on `api.cdn_enabled`.
- ✅ Aggregation engine: VWAP/TWAP orchestrator with closed-bucket
  Redis cache, cross-pair triangulation (X2.5 forex-snap), Phase 1+2
  anomaly response, multi-factor confidence score, freeze policy.
- ✅ Cross-source divergence detection (CoinGecko on by default,
  Chainlink HTTP cross-check opt-in via FeedMap).
- ✅ Three-algorithm supply pipeline (XLM via LCM AccountEntry
  observer; classic via trustlines + claimable + LP + SAC observers;
  SEP-41 via Soroban event observer) populating F2 fields on
  `/v1/assets/{id}`.
- ✅ Archive-completeness daemon (check / fix / verify modes) +
  multi-tier archive verification (Tier A chain-link / Tier B
  checkpoint / Tier D peer cross-compare / Tier E archivist).
- ⏳ Production hardening: public status page (L4.11), SEV-1/2
  dry-run record (L5.7), operational p95 ≤ 200ms proof
  (L5.* + Wk 9–10 launch tasks).

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
