---
title: Runbook — completeness verdict incomplete
last_verified: 2026-06-30
status: ratified
severity: P3
---

# Runbook — `stellarindex_completeness_incomplete`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_completeness_incomplete` |
| Severity | **P3** (ticket) |
| Detected by | `stellarindex_completeness_incomplete{source} == 1` for > 1h |
| Emitted by | `data-freshness.sh` from the latest `completeness_snapshots` row per source |
| Typical MTTR | minutes to verify; a re-derive can be longer (chunked) |
| Impact | The served tier no longer reconciles to the certified ClickHouse lake for that source (ADR-0033 `complete=false`) — a real served<>lake gap, i.e. served data is incomplete vs the proven substrate. |

## Symptoms

`stellarindex_completeness_incomplete{source="X"} = 1`. The ADR-0033 verdict
(`compute-completeness`, daily) found served counts ≠ the lake re-derive for
source `X`. Since the verdict is trustworthy + self-maintaining as of rc.149
(it preseeds factory children), this is a **real gap**, not a checker artifact.

## Quick diagnosis (≤ 5 min)

```sh
# The exact Δ + window the verdict recorded:
sudo -u postgres psql -d stellarindex -c \
 "SELECT source, complete, watermark_ledger, detail FROM (SELECT DISTINCT ON (source) * \
  FROM completeness_snapshots ORDER BY source, computed_at DESC) s WHERE NOT complete;"
# Re-run that source to confirm it persists (off the serving DB, -ch):
stellarindex-ops compute-completeness -config /etc/stellarindex.toml -ch -source <X> -from <recent>
```

## Mitigation (≤ 15 min to start)

Re-derive the flagged source from the certified lake, then re-verify:

- **Soroban projected sources** (`trades`/protocol tables): `stellarindex-ops
  projector-replay -source <X> -from <F>`.
- **Non-projected** (`sdex`, soroban-events): `stellarindex-ops backfill -source <X> -from <F> -to <T>`, or the CH re-derive (`ch-rebuild`).
- Then re-run `compute-completeness -ch -source <X>`; the gauge clears when it
  returns `complete=true`. Run chunked + off-peak (the SDEX/heavy re-derives
  blow ClickHouse's per-query memory limit over large windows).

## Root cause analysis

A served<>lake divergence: dropped rows (a decoder bug fixed forward-only, e.g.
the SEP-41 CAP-67 loss), a missed projection window, or a retention/PK artifact.
The `detail` column names the per-target Δ and window.

## Known false-positive patterns

- Pre-rc.149: factory-gated sources (blend) false-fired because the verdict's
  childgate wasn't self-seeded — **fixed**; if a NEW factory-gated source
  false-fires, confirm its creation events are reachable in `soroban_events`.
- SEP-41 is currently EXCLUDED from the verdict (event_index PK-collapse), so it
  does not emit this gauge until that's resolved (launch-todo P1-7).

## Related

- `stellarindex_data_source_stale` — a source not ingesting at all.
- `docs/architecture/completeness-verification.md` (ADR-0033), `docs/operations/launch-todo.md` Phase C.

## Changelog

- 2026-06-30: created with the data-freshness watchdog.
