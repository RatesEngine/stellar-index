# Runbook: drain a cascade window across every per-source table

**When to use this runbook:** after a back-pressure incident (e.g. F-0020-class) leaves a contiguous ledger window short in one or more per-source classifier hypertables (`blend_*`, `comet_*`, `phoenix_*`, `sep41_transfers`, `soroswap_skim_events`, `cctp_events`, `rozo_events`) while the raw `soroban_events` landing zone is whole. Typical signal: the [ingest-gap-detected](ingest-gap-detected.md) alert fires for one or more `source=` labels other than `soroban-events`, or a manual `ratesengine-ops find-data-gaps --source <name>` reports a contiguous window.

**Pre-flight:** confirm `soroban_events` is whole over the cascade range. If not, fill that first via `ratesengine-ops backfill --source soroban-events --from $FROM --to $TO --parallel 8`; the per-source decoders re-read from `soroban_events`, so a missing landing zone produces an apparent-success run that inserts nothing.

## TL;DR

```
ratesengine-ops drain-cascade-window \
  --config /etc/ratesengine.toml \
  --from $FROM --to $TO \
  --output text
```

Output:

```
drain-cascade-window: ledgers=[62642781, 62735517] dry_run=false halt_on_error=false total=312.0s failed=0/7
    OK  sep41-transfers       42.1s  sep41-transfers-backfill
    OK  cctp                   3.4s  cctp-backfill
    OK  rozo                   2.8s  rozo-backfill
    OK  soroswap-skim         18.7s  soroswap-skim-backfill
    OK  comet-liquidity       89.2s  comet-liquidity-backfill
    OK  blend                 91.5s  blend-backfill
    OK  phoenix               64.3s  phoenix-backfill
```

## What it does

Runs the seven existing per-source `*-backfill` subcommands in series over the same `[from, to]` range. Each subcommand streams events from `soroban_events` through its source's Go decoder and inserts into its dedicated per-source hypertable with `ON CONFLICT DO NOTHING`. The orchestrator captures per-source duration + ok/fail; idempotent re-runs over an already-drained range are clean no-ops (`rows_scanned > 0`, `rows_inserted = 0`).

The seven sources covered:

| Source | Subcommand | Target tables |
|---|---|---|
| sep41-transfers | `sep41-transfers-backfill` | `sep41_transfers` |
| cctp | `cctp-backfill` | `cctp_events` |
| rozo | `rozo-backfill` | `rozo_events` |
| soroswap-skim | `soroswap-skim-backfill` | `soroswap_skim_events` |
| comet-liquidity | `comet-liquidity-backfill` | `comet_liquidity` |
| blend | `blend-backfill` | `blend_positions`, `blend_emissions`, `blend_admin` |
| phoenix | `phoenix-backfill` | `phoenix_liquidity`, `phoenix_stake_events` |

## What it doesn't cover

The following sources have no dedicated `*-backfill` subcommand yet — for them, run the slower binary `ratesengine-ops backfill --source <name>` over the cascade window (re-walks MinIO via the dispatcher, several minutes per source at `--parallel 8`):

- **aquarius** (main swap event)
- **reflector-cex**, **reflector-dex**, **reflector-fx**
- **redstone**
- **soroswap** (main swap, not skim)
- **soroswap-router**
- **defindex**

These should be added as dedicated subcommands in a future PR following the `blend_backfill.go` pattern; tracked as part of ADR-0030 (per-source coverage invariant).

## Common flags

- `--dry-run` — decode without inserting; useful to validate the cascade window has live data before paying the insert cost.
- `--halt-on-error` — stop on first per-source failure. Default is "continue and report" so a single decoder bug doesn't strand the other six.
- `--sources blend,phoenix` — run only the named subset (useful when re-running after a specific failure).
- `--output json` — emit a structured report for piping to `jq` / CI assertions.

## Verification

After the drain completes, the per-source data-derived gap gauges should decay to 0:

```
curl -s http://localhost:9465/metrics | grep ratesengine_ingest_gap_max_size_ledgers
```

If a `source=...` row still reports non-zero, re-run the orchestrator with `--sources <that source>`. If a re-run produces no change, the issue is not a missing row in `soroban_events` — it's either a decoder gap (the source's classify() doesn't claim that topic) or a per-source table without ON CONFLICT idempotency; escalate via the EVERY-event policy review.

## Related

- [ingest-gap-detected.md](ingest-gap-detected.md) — what triggers this runbook
- ADR-0030 — per-source coverage invariant + lint guard
- ADR-0029 — soroban_events landing-zone contract
