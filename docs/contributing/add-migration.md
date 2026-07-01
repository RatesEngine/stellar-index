# Checklist — add a migration

Reference + full rules: `migrations/README.md`.

- [ ] Create `migrations/NNNN_<desc>.up.sql` + `.down.sql` — **dense sequential** numbering
      (next after the current head), matching pair. `down.sql` reverses `up.sql` (or a banner
      comment explains the asymmetry — e.g. the deliberate NO-OP retention-removal downs).
- [ ] Amounts → **`NUMERIC`** (never `bigint` / `double precision`, ADR-0003). Asset IDs →
      canonical text (`<code>-<issuer>` / `C…` / `native`). Timestamps → `timestamptz` UTC.
- [ ] Hypertable/CAGG changes go through the Timescale API. **A CAGG without a refresh policy is
      a silent bug** — add the refresh policy in the same file. **Do not add `drop_after` to
      `trades`/`prices_1m`/`prices_15m`/`oracle_updates`** (ADR-0034: kept forever; retention was
      removed in 0031/0040).
- [ ] Event-hypertable PK **leads with `ledger_close_time`** (TS103 lesson) and carries a
      per-event discriminator (`event_index` or equivalent) — the `lint-pk-discriminators` CI gate
      requires it for protocol-row tables.
- [ ] Header comment explains the *why*. `CREATE … IF NOT EXISTS` where idempotent.
- [ ] Add a row to the **"Current migrations"** table in `migrations/README.md`.
- [ ] Apply as the **`stellarindex` app role** via `make db-migrate-up` — never as the `postgres`
      superuser (ownership trap).

**Done when:** `make db-migrate-up` then `make db-migrate-down` both succeed locally; README updated.
