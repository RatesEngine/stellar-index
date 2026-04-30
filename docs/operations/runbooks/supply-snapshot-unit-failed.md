---
title: Runbook — supply-snapshot-unit-failed
last_verified: 2026-04-30
status: ratified
severity: P3
---

# Runbook — `ratesengine_supply_snapshot_unit_failed_alert`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_supply_snapshot_unit_failed_alert` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/supply-snapshot.yml` |
| Typical MTTR | 15–30 min |
| Impact | `/v1/assets/{id}` F2 fields (total / circulating / max / market_cap_usd / fdv_usd) keep serving the previous good value, so bounded — but they go stale until the writer recovers. |

## Symptoms

- `ratesengine_supply_snapshot_unit_failed{asset_key=…} > 0` for ≥
  30 min.
- The most-recent `supply-snapshot.service` invocation in journald
  exited non-zero.
- `last_success_timestamp` for the named asset is older than the
  daily cadence target (24 h).

## Quick diagnosis (≤ 5 min)

```sh
# 1. Last run output.
sudo journalctl -u supply-snapshot.service -n 100 --output=cat

# 2. Dry-run the writer to reproduce.
sudo -u ratesengine /usr/local/bin/ratesengine-ops supply snapshot \
  -config /etc/ratesengine.toml -dry-run

# 3. Validate config.
sudo -u ratesengine /usr/local/bin/ratesengine-ops docs-config | \
  head  # confirm parses cleanly
grep -E "sdf_reserve_accounts|reserve_balances_stroops" /etc/ratesengine.toml
```

## Typical root causes (roughly in frequency order)

1. **Missing balance entry in `reserve_balances_stroops`.** Operator
   added a new account to `sdf_reserve_accounts` but forgot the
   matching balance entry. The writer-start validator catches this
   with a clear error.
   - Signal: log line `supply: reserve_balances_stroops missing
     balance for account "G..."`.
   - Mitigation: add the missing balance entry, reload the timer.

2. **Postgres unavailable.** The writer's `timescale.Open` or
   `InsertSupply` call failed. Same diagnostic flow as
   `pg-conns-saturated.md` — confirm reachability and pool depth.

3. **No ingestion cursors yet.** On a fresh box without indexed
   ledgers, `resolveSnapshotLedger` errors with "no ingestion
   cursors yet — pass -ledger explicitly until the indexer has
   produced a cursor."
   - Mitigation: ride out the indexer's first cursor, or set
     `EXTRA_FLAGS="-ledger <known-good>"` in
     `/etc/default/supply-snapshot` until it lands.

4. **Operator config edit broke parsing.** Trailing-comma in the
   TOML, mistyped key, etc. The writer-start config-load fails.
   - Signal: `config:` prefix in the error message.
   - Mitigation: fix the TOML, reload.

## Mitigation

- [ ] Step 1 — Walk Quick diagnosis to reproduce the failure mode.
- [ ] Step 2 — Apply the matching root-cause fix.
- [ ] Step 3 — Force a manual run: `sudo systemctl start supply-snapshot.service`.
- [ ] Verification: `unit_failed` returns to 0 and `last_success_timestamp` updates.

## Known false-positive patterns

- **First run after a fresh deploy**, before the first daily cron
  fire. The `for: 30m` window typically absorbs this.

## Related

- `supply-snapshot-stale.md` — when no recent successful run exists.
- `supply-cross-check-divergence.md` — when the value itself looks wrong.
- `pg-conns-saturated.md` — Postgres reachability.

## Changelog

- 2026-04-30 — initial draft alongside #295 (textfile + alerts).
