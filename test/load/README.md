# `test/load/` — k6 load test suite

Per [Task #74 design note](../../docs/architecture/k6-load-tests-design-note.md).

Synthetic traffic against the Rates Engine API, asserting:

- **p95 ≤ 200 ms** across the realistic mix of `/v1/price`,
  `/v1/vwap`, `/v1/twap`, `/v1/history`, batch + stream
  surfaces (Freighter SLA target).
- **99.9 % success rate** under sustained load (per ADR-0009).

Tooling: [k6](https://k6.io). Each scenario is a `.js` file
under `scenarios/`. Shared helpers (env, pair fixtures, SLA
thresholds) live under `scenarios/lib/`.

## Scenarios

| File | Stresses | Pass criteria |
|---|---|---|
| `scenarios/01-price-hot-path.js` | Redis cache + handler routing | p95 < 200 ms @ 500 rps for 5 min |
| `scenarios/02-vwap-twap.js` | CAGG read path | p95 < 200 ms @ 100 rps for 5 min |
| `scenarios/03-history.js` | Since-inception query (heavy) | p95 < 1000 ms for `since-inception`; p95 < 200 ms for windowed |
| `scenarios/04-batch.js` | bulk fan-out cap | p95 < 500 ms @ batch-size 100, 50 rps |
| `scenarios/05-streaming.js` | SSE connection ramp + sustain | 99 % of clients receive their first event < 1 s after subscribe |
| `scenarios/06-mixed-realistic.js` | the canonical proof scenario | p95 < 200 ms across the weighted mix; error rate < 0.1 %; sustained 10 min |
| `scenarios/07-catalogue-browse.js` | showcase hot path (`/v1/coins`, `/v1/issuers`, `/v1/markets`, `/v1/diagnostics/cursors`) | p95 < 200 ms on lookups, p95 < 300 ms on `/v1/markets` (GROUP BY); error rate < 0.1 %; 5 min |
| `scenarios/99-spike.js` | brief 10× burst absorption | recovery to baseline p95 within 2 min of spike end |

## Running

```sh
# Set the target + auth (the Makefile target refuses production):
export K6_TARGET=https://api.staging.ratesengine.net/v1
export RATESENGINE_LOAD_API_KEY="<paste from vault>"

# Run a single scenario:
k6 run --out experimental-prometheus-rw test/load/scenarios/01-price-hot-path.js

# Run the canonical proof scenario:
make test-load-mixed

# Run everything (slow):
make test-load
```

The `make test-load*` targets enforce a non-production guard
(refuses to run if `K6_TARGET` mentions production hostnames).
Direct `k6 run` skips the guard — be careful.

## Output

k6's `--out experimental-prometheus-rw` writes scrape data into
the same Prometheus stack the production API uses. Grafana
dashboard ID `load-proof` (provisioned alongside the suite)
renders per-endpoint p95 / p99 / error rate for the run window.

For the SLA proof report (Task #77), the canonical artefact is
the markdown summary at
`docs/operations/sla-proof-<YYYY-MM-DD>.md` capturing the
mixed-realistic run's results + Grafana snapshot link. Use
`docs/operations/sla-proof-template.md` as the starting shape;
copy → fill in the run window, per-endpoint table, Grafana
snapshots, and concurrent-ingest activity → commit alongside the
CHANGELOG entry for the release.

## Local development

Install k6:

```sh
brew install k6   # macOS
# or
sudo apt install k6   # Debian/Ubuntu (after adding the k6 apt repo)
```

Compile-check a scenario without running:

```sh
k6 archive --quiet test/load/scenarios/01-price-hot-path.js
```

## See also

- [`docs/architecture/k6-load-tests-design-note.md`](../../docs/architecture/k6-load-tests-design-note.md) — full design (effort breakdown, edge cases, traffic-shape rationale).
- [`docs/operations/runbooks/api-latency.md`](../../docs/operations/runbooks/api-latency.md) — the alert this suite proves we don't trip.
- [`deploy/monitoring/rules/slo.yml`](../../deploy/monitoring/rules/slo.yml) — the multi-window SLO rules whose budget this proves we stay within.
- [Coverage matrix S9.2](../../docs/architecture/coverage-matrix.md) — Freighter SLA contract requirement.
