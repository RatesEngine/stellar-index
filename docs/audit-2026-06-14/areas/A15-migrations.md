# A15 — Migrations + Schema (READ-ONLY audit)

Scope: `migrations/*.sql` (122 files = 61 up/down pairs + `README.md`) and the
runner `cmd/stellarindex-migrate/main.go`.
Audited 2026-06-14. Dimensions: D6 (schema, primary), D2 (one-writer ownership),
D1 (mid-migration failure / non-idempotency / data-loss), X7 (Go↔column
cross-check).

---

## Headline

- **Numbering is dense and complete**: 0001–0061, no gaps, no duplicates.
- **Every `up` has a matching `down`** (61/61 stems pair exactly).
- **The trades-retention invariant (ADR-0034) HOLDS on `up`**: 0001 added a 90d
  retention policy on `trades`; 0031 removed it with `if_exists`; no later
  migration re-adds a `drop_after`/retention policy on `trades`. Same for
  `oracle_updates` (0003 added, 0040 removed). **But** the `0031.down.sql` /
  `0040.down.sql` files *re-add* the retention policies — the documented "rogue
  retention is drift" bug is one `migrate down` away (see H1).
- **ADR-0003 (NUMERIC) holds everywhere**: every amount/price/supply/reserve/
  volume/balance/shares/fee column is `numeric`. The only `double precision`
  columns are statistical baselines (median/MAD), oracle `confidence` (0..1) and
  coverage percentages — none are i128 quantities. No `bigint`/`float` amount
  columns exist.
- **Continuous-aggregate-in-transaction is CORRECT, not a bug**: all CAGGs
  (0002/0034/0036) use `WITH NO DATA` and never call
  `refresh_continuous_aggregate` inside the txn, so they are legal inside the
  file's `BEGIN/COMMIT` under the golang-migrate v4 postgres driver (which does
  NOT add its own transaction).
- **Largest real risks** are operational/non-atomic, not structural: the
  event_index recovery migrations (0053–0060) run multi-statement DDL with **no
  transaction wrapper** and a bare `DROP CONSTRAINT … pkey` (no `IF EXISTS`),
  so a mid-migration failure leaves a partially-applied, dirty schema that
  needs manual `force` recovery; and the `trades` **1-day chunk interval** that
  caused the lock-table-sizing incident is still baked into 0001 and never
  widened by any migration (fresh bring-up re-creates the problem).
- **README "Current migrations" table is badly stale/wrong** for 0016–0028 (it
  documents entirely different tables than the files that shipped) and stops at
  0045 (16 migrations undocumented).

Severity counts: **Critical 0 · High 4 · Medium 7 · Low 6 · Info 3**

---

## Findings

| severity | file:line | dimension | issue | why it matters | fix | confidence |
|---|---|---|---|---|---|---|
| High | `0031_remove_trades_retention.down.sql:7` (+ `0040_remove_oracle_updates_retention.down.sql:11`) | D6 / D1 | The `down` migration re-adds `add_retention_policy('trades', INTERVAL '90 days')` (and `prices_1m`/`prices_15m` 30d; 0040 re-adds oracle_updates 90d). | This is the literal mechanism of the known "rogue retention policy on `trades`" drift bug (MEMORY: SDEX-discrepancy + ADR-0034). A single `stellarindex-migrate down` (or `down N` crossing 31/40) silently re-arms the retention worker that deletes >90d raw trades — the exact data-loss ADR-0034 forbids. golang-migrate `down` is one keystroke. | Make the down a no-op with a loud comment ("forward-only; raw trades are kept forever per ADR-0034 — do NOT re-add retention"), OR keep the policy-restore but add a `\echo`/comment refusing it. At minimum document in README that `down` past 31/40 reintroduces retention and is forbidden on prod. | High |
| High | `0053`–`0060` `.up.sql` (all 8) | D1 | event_index/PK-recovery migrations run `decompress_chunk(...)` + `ALTER TABLE ADD COLUMN` + `DROP CONSTRAINT pkey` + `ADD CONSTRAINT pkey` with **no `BEGIN/COMMIT`**. golang-migrate v4 postgres driver runs the whole file as one `Exec` and does NOT add its own transaction. | If any statement after the first fails (e.g. `ADD CONSTRAINT` hits a duplicate on a populated host, or the connection drops), the migration is left half-applied: constraint dropped but not re-added (table has NO primary key → ON CONFLICT writes break), or column added but PK unchanged. golang-migrate marks the version *dirty*; recovery is a manual `force` + hand-finish. On a live r1-class table this is a write-path outage, not just a failed deploy. | Where the DDL allows a txn, wrap the `ALTER`s in `BEGIN/COMMIT` (the `decompress_chunk` can precede it; constraint swaps are transactional). 0030 already does exactly this (decompress outside txn, swap inside `BEGIN/COMMIT`) — mirror that pattern for 0053–0060. | High |
| High | `0053`–`0060` `.up.sql` / `.down.sql` (`DROP CONSTRAINT <t>_pkey` with no `IF EXISTS`) | D1 | The PK swap uses bare `DROP CONSTRAINT <table>_pkey` (no `IF EXISTS`). Re-running after a partial failure (constraint already dropped) errors with "constraint does not exist", blocking the obvious retry path. | Combined with the no-transaction issue above, the natural operator response (re-run `up`) fails instead of converging, forcing manual surgery exactly when the schema is already in a fragile state. | Use `ALTER TABLE … DROP CONSTRAINT IF EXISTS <t>_pkey;` so a retry is idempotent; pair with the txn wrapper so partial state can't arise in the first place. | High |
| High | `0001_create_trades_hypertable.up.sql:64` | D6 | `trades` hypertable is created with `chunk_time_interval => INTERVAL '1 day'` and **no migration ever widens it** (`set_chunk_time_interval` appears in zero migrations). | This 1-day interval is the documented root cause of the lock-table-sizing crisis (MEMORY: 3445 chunks → per-INSERT ON CONFLICT walks all chunks → ~6 inserts/s; `max_locks_per_transaction` bumped 64→256→4096). The r1 fix was operational (`merge_chunks()` out-of-band), so the *migration set still encodes the broken interval*: a fresh archival-node bring-up (R2/R3/DR) re-creates the 1-day interval and re-accrues the same lock pressure as the table fills. Migration-vs-deployed-schema drift. | Add a migration that `SELECT set_chunk_time_interval('trades', INTERVAL '<N> days')` (the agreed next step per MEMORY) so new chunks are wider, and update 0001's header / archival-node-bringup doc. Apply the same review to other 1-day hypertables that will get hot (`soroban_events`, `oracle_updates`, the blend/phoenix per-event tables). | High |
| Medium | `migrations/README.md:103-127` | D6 (doc drift) | The "Current migrations" table is wrong for 0016–0028: it lists `anomaly_freezes`/`archive_completeness`/`external_poller_state`/`market_observations`/`supply_state`/`change_summary`/`incidents`/`redstone_extras`/`divergence_runs` against numbers whose actual files are `soroswap_pairs`/`wasm_history`/`freeze_events`/`divergence_observations`/`decoder_stats_5m`/`tvl_and_mev`/`change_summary_5m`/`classic_asset_registry`/`classic_asset_stats_5m`. The table also stops at 0045 (0046–0061 undocumented). | The only human index of the migration set is actively misleading — an agent/operator trusting it will look for the wrong table or assume a migration adds something it doesn't. CLAUDE.md treats README freshness as load-bearing. | Regenerate the table from the actual filenames; add 0046–0061; consider a CI check that the table rows match `ls *.up.sql`. | Medium |
| Medium | `migrations/README.md:76-78` | D6 (doc drift) | "Conventions" says transactions can be disabled with `-- migrate:no-transaction`. That directive is **dbmate/goose syntax — golang-migrate v4 ignores it** (the v4 postgres driver has no per-file no-transaction comment; it just doesn't wrap files in a txn at all — the file's own `BEGIN/COMMIT` is the only transaction). | An author who writes a hypertable/CONCURRENTLY migration trusting `-- migrate:no-transaction` to "disable the wrapper" is reasoning about a wrapper that doesn't exist and a directive that does nothing. It works today only by accident (no wrapper to disable). Mis-teaches the next migration author. | Replace with the accurate statement: "golang-migrate v4 does NOT wrap a migration in a transaction; add your own `BEGIN/COMMIT` for atomicity, and OMIT it (run statements bare) for `CREATE INDEX CONCURRENTLY` / `ALTER TABLE … SET (compress=…)` which cannot run in a txn." | Medium |
| Medium | `0037_trades_pair_source_ts_index.up.sql:45` | D1 / D6 | Plain `CREATE INDEX … ON trades(...)` (not CONCURRENTLY) — correctly cannot be CONCURRENTLY inside the runner, and the header documents the "build by hand CONCURRENTLY first on a live node" step. But nothing *enforces* it; running `up` on a populated `trades` (2.7B rows) takes a full-table ACCESS EXCLUSIVE lock for the whole build. | Same hazard class as the chunk-interval drift: a DR/fresh-region bring-up that forgets the manual-CONCURRENTLY pre-step will stall ingest for minutes-to-hours. The `IF NOT EXISTS` guard makes the hand-build safe, but only if the operator remembers it. | Keep as-is for fresh (empty) bring-ups, but move the "build CONCURRENTLY by hand first" step into the archival-node-bringup runbook as a hard gate, not just an in-file comment. | Medium |
| Medium | `0053`–`0060` `.down.sql` | D6 | The `down` files narrow the PK back to the pre-event_index column set, but they note (0057 explicitly) that this will FAIL on duplicates if a re-derive added rows that are only distinct under the wider PK. So the down is "best-effort" and not reliably reversible on a populated host. | Honest and documented, but it means `down` is not a safe rollback on prod for these — and that asymmetry is only in 0057's comment, not the others. An operator running `down` expecting a clean revert may hit a hard error mid-rollback (compounded by the no-txn issue). | Add the same "best-effort; may fail on duplicates post-re-derive" caveat to 0053/0054/0055/0058/0059/0060 down headers; or have the down `DROP` the table-rebuild expectation explicitly. | Medium |
| Medium | `0002_create_price_aggregates.up.sql:69,102` vs audit-prompt expectation | D6 | The audit prompt states "prices_1m/15m have 30d retention". Reality: 0002 added 30d retention, **0031 removed it** (per ADR-0034 "prices_1m/15m retention was also removed"). So sub-hourly CAGGs are now INDEFINITE, not 30d. | Not a code bug — the code matches ADR-0034. Flagging because the prompt's stated invariant and CLAUDE.md's ADR-0034 paragraph disagree; the *running* schema (post-0031) is the indefinite one. Anyone re-asserting "30d retention on prices_1m" would be reintroducing the 0031-removed policy (= the H1 drift). | None to schema. Confirm ADR-0034's "removed prices_1m/15m retention" is the intended state (it is) and don't re-add. | Medium |
| Medium | `0030_asset_supply_history_unique_constraint.up.sql` / `0004` / `0053`–`0060` | D1 | The decompress→DDL→recompress pattern: `decompress_chunk(c,true)` + `ALTER TABLE … SET (compress=false/…)` cannot live in a txn, so these are inherently non-atomic. 0030 mitigates by doing only the *constraint swap* inside an inner `BEGIN/COMMIT`; 0004 and 0053–0060 do not. | A crash between decompress and the compression-restore leaves chunks uncompressed (storage blows up until the next policy run / a re-run) — recoverable but a silent disk-pressure footgun, and on r1 disk pressure has caused PG crashes (MEMORY: CH log root-fill). | Where feasible, follow 0030's shape: decompress + compress-toggle bare, wrap the pure constraint/PK swap in an inner `BEGIN/COMMIT`. Document that re-running converges (needs the IF EXISTS fix from H above). | Medium |
| Medium | `0048_source_coverage_snapshots.up.sql:3` | D6 (smell) | Column named `"table"` (a reserved word, quoted). | Forces every reader (Go + ad-hoc SQL) to quote it forever; one unquoted reference is a runtime error. Minor but avoidable. | Rename to `table_name`/`target_table` in a future migration if churn allows; otherwise leave + note. (Low-churn, so not High.) | Medium |
| Low | `0031.up.sql` / `0040.up.sql` | D6 | `up` removes retention with `remove_retention_policy(..., if_exists => true)` — correctly idempotent. Good. (Recorded as the positive counterpart to H1: the `up` direction is safe; only `down` reintroduces the policy.) | — | — | High |
| Low | `cmd/stellarindex-migrate/main.go:37` | D6 | `up` runs `m.Up()` which applies ALL pending migrations in one process. There is no `--to <version>`/`goto` subcommand, only `up`/`down N`/`force`. | Minor: no way to apply up-to-a-version (e.g. stop before a known-heavy migration). golang-migrate supports `Migrate(version)`; not exposed. Operationally fine today. | Optional: add a `goto <v>` subcommand. | High |
| Low | `cmd/stellarindex-migrate/main.go:88-101` | D1 | `force <V>` is exposed and documented as DANGEROUS but has no confirmation prompt / env-gate. | Combined with the dirty-state risk from the no-txn migrations (H2), an operator under pressure can `force` to the wrong version and mask a half-applied schema. | Optional: require `--yes` or an interactive confirm for `force`. | Medium |
| Low | `0009_create_blend_auctions.up.sql:116`, `0003:64`, `0027:242` | D6 | Several hypertables use a 1-day chunk interval (`blend_auctions`, `oracle_updates`, `api_usage_events`, plus the per-event Soroban tables in 0042–0045). | Same chunk-count growth dynamic as `trades` (H4) — lower volume today so lower priority, but `oracle_updates` and `soroban_events` (1-day) will accrue chunks at scale. | Track alongside the trades chunk-interval fix; widen the ones expected to get hot. | Medium |
| Low | `migrations/README.md:38-40` | D6 | README rule 4 says every CAGG migration "also adds its refresh policy + retention policy in the same file". 0002 deliberately does NOT add retention on 1h+ (indefinite by design), and 0034 adds none on sub-hourly. | The rule as written is contradicted by the (correct) files; a literal reader thinks 0002 is buggy. | Reword rule 4 to "refresh policy (always) + retention policy (where the grain is retained)". | Low |
| Low | `0046_cursors_add_first_ledger.up.sql` (UPDATE … ::integer) | D1 | The `UPDATE … SET first_ledger = split_part(sub_source,'-',1)::integer WHERE sub_source ~ '^[0-9]+-…'` is guarded by a regex so the cast can't fail on matched rows. Verified safe. | — (recorded as a checked-and-OK data migration). | — | High |
| Info | golang-migrate v4 driver behavior | D1 | Confirmed from the vendored source (`migrate/v4@v4.19.1/database/postgres/postgres.go` `Run()`): with `MultiStatementEnabled=false` (the default — no `x-multi-statement` in the DSN) the driver runs the **entire file as one `runStatement`** and adds **no transaction of its own**. Atomicity is solely whatever `BEGIN/COMMIT` the file contains. This is why the no-txn migrations (H2) are genuinely non-atomic. | — | — | High |
| Info | D2 ownership | D2 | Projected-source tables (`blend_*`, `phoenix_*`, `comet_liquidity`, `soroswap_skim_events`, `defindex_flows`, `sep41_supply_events`, `cctp_events`, `rozo_events`, `oracle_updates`) are created by migrations as plain tables owned by the applying role; the one-writer rule is enforced in Go (`internal/projector` + `pipeline/sink.go::IsProjectedEvent`), not in the schema. The migrations don't violate D2 (no second writer is granted), but the schema has no constraint *preventing* a second writer — ownership is a code/convention invariant only. Consistent with the architecture. | — | — | High |
| Info | README rule 7 / `source_entry_counts` ownership | D2 | README documents the 0035 r1 incident where applying as `postgres` superuser made `source_entry_counts` superuser-owned and the app lost access (42501). This is an *operational* (apply-as-app-role) invariant, correctly NOT fixed with a GRANT migration. No schema action needed; noted because it's a live foot-gun for DR/fresh-region applies. | — | Apply migrations only via `STELLARINDEX_POSTGRES_DSN` (the app role), never as `postgres`. | High |

---

## CORRECT — verified-good list

- **Numbering**: 0001–0061 dense, no gaps, no dupes (verified via `seq` diff).
- **Pairing**: 61/61 `up` have a matching `down`; 0/0 orphans either direction.
- **trades retention invariant (ADR-0034) on `up`**: 0001 add → 0031 remove
  (`if_exists`), never re-added on any later `up`. `trades` raw kept forever as
  required. (Caveat = H1: the `down` re-adds it.)
- **oracle_updates retention**: 0003 add → 0040 remove, never re-added on `up`.
- **CAGG retention tiers**: `prices_1m`/`prices_15m` retention REMOVED by 0031
  (now indefinite, per ADR-0034); 1h/4h/1d/1w/1mo never had retention
  (indefinite by design). Matches ADR-0034 (note the prompt's "30d" is the
  pre-0031 state — see Medium finding).
- **CAGG creation is transaction-safe**: 0002 (7 views), 0034 (7 views), 0036
  (1 view) all use `WITH NO DATA` and add the refresh policy in-file; no
  `refresh_continuous_aggregate` inside a txn. Legal under the golang-migrate v4
  driver. Each CAGG has a refresh policy (README rule 4 satisfied).
- **ADR-0003 NUMERIC**: every amount/price/supply/reserve/volume/balance/
  shares/fee/rate column is `numeric`. No `bigint`/`int8`/`float`/`real`/
  `double precision` is used for an i128 quantity. The `double precision`
  columns (volatility median/MAD in 0007/0008, oracle `confidence` 0..1 in
  0003, `coverage_pct`/`density_pct`/`gap_free_pct`) are all legitimate
  non-amount statistics.
- **0030 constraint swap**: textbook handling of a compressed hypertable —
  decompress + disable-compress outside the txn, DROP INDEX + ADD CONSTRAINT
  inside an inner `BEGIN/COMMIT` (race-safe), restore compression after.
- **event_index PK design (0053–0061)**: the discriminator additions correctly
  close the coarse-PK collapse class (blend positions add `(asset,user_address)`;
  emissions/admin/auctions/comet/phoenix/sep41 add `event_index`; defindex adds
  `event_index`; soroswap-router adds `call_sig`). `ADD COLUMN IF NOT EXISTS …
  DEFAULT 0` is idempotent and non-destructive; existing rows get a sane default.
- **X7 column cross-check (load-bearing)**: `call_sig` (0056) — Go INSERT +
  `ON CONFLICT (ledger_close_time, ledger, tx_hash, op_index, call_sig)` in
  `internal/storage/timescale/soroswap_router_swaps.go` matches the 0056 PK
  exactly. `protocol_contracts` (0061) — read/written by
  `internal/storage/timescale/protocol_contracts.go`,
  `internal/pipeline/gated_registry.go`, `internal/projector/registry.go`
  (UPSERT columns `source,contract_id,factory_id,first_ledger,observed_at` match
  the table). `event_index` — read/written across projector + clickhouse/
  timescale sinks. `first_ledger` (0046) — present and consumed by the
  density-coverage projection. No referenced load-bearing column is missing from
  the migrations.
- **FK/constraint integrity**: CHECK constraints are consistent and sane
  (`ledger>0` relaxed to `>=0` in 0004 for off-chain `ledger=0`; amounts
  `>0`/`>=0`; pct columns `BETWEEN 0 AND 1`). No dangling FK references found.
- **Runner safety rails**: no default-DSN fallback (fails closed); golang-migrate
  advisory lock serialises concurrent runners; `force` is gated behind explicit
  intent and documented as dangerous.
- **chunk intervals present**: every `create_hypertable` specifies an explicit
  `chunk_time_interval` (none fall back to the TS 7-day default unintentionally).

---

## Method / coverage

- **Files read in full**: runner `main.go`; `0001`, `0002` (up+down), `0030`,
  `0031` (up+down), `0037`, `0040.down`, `0046` (up+down), `0053` (up+down),
  `0057` (up+down), `0061` (up+down); README.md.
- **Files inspected by targeted extraction (statements/types/headers)**: all 61
  `up` + relevant `down` files for BEGIN/COMMIT presence, retention calls,
  NUMERIC/float typing, chunk intervals, `WITH NO DATA`, decompress/PK-swap
  bodies (0004, 0029, 0048, 0054, 0055, 0056, 0058, 0059, 0060 up + 0053–0061
  down).
- **Driver behavior**: confirmed against vendored
  `golang-migrate/migrate/v4@v4.19.1` postgres `Run()` source.
- **X7**: grepped `internal/`, `cmd/` for `call_sig`, `protocol_contracts`,
  `event_index`, `soroswap_router_swaps`, `first_ledger` and matched
  ON CONFLICT / INSERT column lists to the migration PKs/columns.

**Numbering complete (0001–0061, no gaps/dupes): CONFIRMED.**
**Up/down pairing complete (61/61): CONFIRMED.**
**Total migration files: 122 (.sql) + 1 README. Distinct migrations: 61.**
