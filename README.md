# Stellar Index

**Stellar Index is a protocol explorer and pricing API for the Stellar network** —
complete, verified, per-protocol on-chain data: every contract, every
event, every trade, for every major protocol (SDEX, Soroswap, Aquarius,
Phoenix, Comet, Blend, DeFindex, CCTP, Rozo, and the Reflector / Redstone
/ Band oracles), captured from a certified raw ledger lake and
attributed by contract identity (ADR-0035), with per-protocol
verification pages at [docs/protocols/](docs/protocols/). It is evolving
toward a comprehensive blockchain explorer for Stellar — classic/native
and Soroban.

Its flagship product is the **pricing API**: publicly-accessible,
aggregated, real-time and historical prices for every Stellar asset —
classic and SEP-41 Soroban token. On-chain trades + oracle feeds +
CEX/FX/reference aggregators fused into one VWAP-first pricing layer
served at p95 ≤ 200 ms. Full since-inception OHLC. Self-hostable.

**Status:** Pre-v1. The core system runs end-to-end — ingestion, raw
ledger lake, served tier, REST + SSE API, and the aggregator (VWAP/TWAP,
triangulation, anomaly response, multi-factor confidence, freeze policy,
supply pipeline). A live deployment serves [stellarindex.io](https://stellarindex.io),
[docs.stellarindex.io](https://docs.stellarindex.io), and a public
[status page](https://status.stellarindex.io).
**License:** Apache-2.0.
**Tested against:** Stellar pubnet protocol 23 (post-P23 / CAP-67 unified events).

---

## If you are an AI agent reading this for the first time

See **[CLAUDE.md](CLAUDE.md)**. It's your orientation map.

---

## Start here

- **Hosted UI / explorer:** the explorer (`web/explorer/`) is a
  static-export Next.js site rendered at
  <https://stellarindex.io>. Browse assets, markets, issuers,
  sources, diagnostics; every panel reveals the exact API call
  that produced it. Build locally via
  `cd web/explorer && pnpm build` (output: `web/explorer/out/`).
  Operator runbook: [`docs/operations/explorer-deployment.md`](docs/operations/explorer-deployment.md).
- **Users of the hosted API:** [`docs/getting-started.md`](docs/getting-started.md)
  walks from zero to your first authenticated request in five
  minutes. Rendered at <https://docs.stellarindex.io>.
- **API examples:** [`examples/curl/`](examples/curl/) — ten runnable
  shell scripts covering signup, account info, price, OHLC, history,
  oracle latest, markets, and the SSE price stream.
  [`examples/postman/`](examples/postman/) ships a Postman v2.1
  collection auto-generated from the OpenAPI spec (imports cleanly
  into Postman, Insomnia, and Bruno).
- **Reference docs:** generated Scalar output at
  [`docs/reference/api/index.html`](docs/reference/api/index.html)
  (regenerate via `make docs-api`); also published to
  <https://docs.stellarindex.io> (Cloudflare Pages) by the
  [`docs-deploy` workflow](.github/workflows/docs-deploy.yml).
- **Self-hosting:** `make dev` boots the full local stack
  (TimescaleDB + Redis + MinIO). See
  [deploy/docker-compose/dev.yaml](deploy/docker-compose/dev.yaml).
- **Contributors:** [CONTRIBUTING.md](CONTRIBUTING.md).
- **Architecture / design:** [docs/architecture/](docs/architecture/)
  (narrative designs) and [docs/adr/](docs/adr/) (Architecture Decision
  Records). [docs/engineering-standards.md](docs/engineering-standards.md)
  is the non-negotiable policy layer every change is held to.

---

## Layout

```
cmd/                   binaries (indexer / aggregator / api / ops / migrate / sla-probe)
internal/              private packages (Go-enforced non-importable)
pkg/                   public surface (client SDK + stable types)
migrations/            TimescaleDB schema migrations
configs/               example.toml + Ansible roles (configs/ansible/)
openapi/               API spec — source of truth for reference docs
examples/              curl scripts + Postman collection for the public API
deploy/                docker-compose (dev) / systemd (production) / monitoring (Prometheus rules) / status-page
web/explorer/          Next.js static-export explorer rendered at stellarindex.io
test/                  integration + load + chaos + fixtures
docs/                  architecture / ADR / operations / reference / methodology
```

---

## Core invariants (never violated)

These are the architectural commitments that bind every PR. See
[docs/adr/](docs/adr/) for the long-form rationale per decision.

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
6. **ClickHouse is the raw lake; Postgres is the served tier.** ADR-0034.

---

## What's shipped

- **Ingestion** — Galexie → dispatcher → per-source decoders for SDEX,
  Soroswap, Aquarius, Phoenix, Comet, Blend, DeFindex, CCTP, Rozo, plus
  the Reflector / Redstone / Band oracles and the external CEX + FX fleet.
  Dual-sink into the ClickHouse raw lake (full history) and the
  TimescaleDB served tier.
- **REST + SSE API v1** (full list at
  [`docs/reference/api/index.html`](docs/reference/api/index.html)):
  pricing (`/price`, `/price/batch`, `/price/tip`, `/vwap`, `/twap`,
  `/observations`), historical (`/history`, `/history/since-inception`,
  `/ohlc`, `/chart`), catalogue (`/assets`, `/assets/{id}`, `/markets`,
  `/pairs`, `/sources`), oracle passthrough, account self-service,
  SEP-10 web auth, SSE streams, plus operator endpoints
  (`/healthz`, `/readyz`, `/version`, `/metrics`). Behind CORS, a
  subject-aware rate limit (anon-IP + key-tier), a trusted-proxy CIDR
  allow-list, and per-route Cache-Control with CDN-tier `s-maxage`.
- **Aggregation engine** — VWAP/TWAP orchestrator with closed-bucket
  Redis cache, cross-pair triangulation, anomaly response, a
  multi-factor confidence score, and a freeze policy.
- **Cross-source divergence detection** — CoinGecko on by default,
  Chainlink HTTP cross-check opt-in.
- **Three-algorithm supply pipeline** — XLM via LCM AccountEntry
  observer; classic via trustlines + claimable + LP + SAC observers;
  SEP-41 via Soroban event observer.
- **Archive-completeness verification** — a check/fix/verify daemon plus
  multi-tier archive verification (chain-link / checkpoint / peer
  cross-compare / archivist).
- **Public explorer + status page** — static-export Next.js, deployed to
  Cloudflare Pages.

---

## Contact

- Security: <security@stellarindex.io> — see [SECURITY.md](SECURITY.md)
  for the disclosure process.
- Code: [CONTRIBUTING.md](CONTRIBUTING.md)
- General: <hello@stellarindex.io>

---

## License

Apache-2.0. See [LICENSE](LICENSE).
