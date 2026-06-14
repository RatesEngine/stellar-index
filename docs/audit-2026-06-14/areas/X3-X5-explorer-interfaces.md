# X3 + X5 — Explorer dataflow & interface conformance (READ-ONLY cross-file audit)

Two cross-cutting seams audited together because they overlap heavily on the
explorer read path:

- **X3 — Explorer dataflow:** trace the read path end-to-end, lake → reader →
  handler → xdrjson → wire JSON → OpenAPI → web UI. Verify (a) index coverage
  vs every query predicate, (b) wire-shape consistency at EVERY hop, (c) the
  bloom-skip-index-vs-FINAL discipline.
- **X5 — Interface conformance:** enumerate the key interface seams and verify
  each interface ↔ all its implementations + test stubs are in sync.

Audit date: 2026-06-14. Method: full read of every file on the explorer read
path + the dispatcher decoder seams + the external connector seam, plus the
lake DDL, the OpenAPI spec sections, and the web/explorer TypeScript types that
consume the wire. SDK `MemoType()` and the lake-writer (`extract.go`) consulted
to confirm what form columns actually hold.

Dimension legend (shared with the rest of this audit): D1 correctness,
D2 ADR invariants, D3 wire-shape/contract, D4 concurrency/lifecycle,
D5 robustness/coverage.

**Files read: 26 / 26.**
Go (15): `internal/storage/clickhouse/explorer_reader.go`, `extract.go`,
`extract_entry_changes.go` (skim), `internal/api/v1/explorer.go`,
`explorer_ledgers.go`, `explorer_operations.go`, `explorer_tx.go`,
`explorer_contracts.go`, `explorer_accounts.go`, `explorer_search.go`,
`explorer_ledgers_test.go` (the stub), `internal/xdrjson/operation.go`,
`helpers.go`, `participants.go`, `internal/dispatcher/dispatcher.go` (the 4
decoder seams). DDL (1): `deploy/clickhouse/tier1_schema.sql`. OpenAPI (1):
`openapi/stellar-index.v1.yaml` (explorer paths + schemas + servers). UI (3):
`web/explorer/src/app/explorer-shared.tsx`, `ledger/LedgerView.tsx`,
`tx/TxView.tsx` (+ `contract/ContractView.tsx` for the search-href check).
External seam (1): `internal/sources/external/framework.go`. Plus SDK (1):
`ingest/ledger_transaction.go` (`MemoType()`), and `internal/api/v1/server.go`
(route table + Options wiring) consulted.

---

## Findings

| severity | file:line | dim | issue | why it matters | suggested fix | conf |
|---|---|---|---|---|---|---|
| **high** | `web/explorer/src/app/tx/TxView.tsx:264-272` + `explorer-shared.tsx:56,76,95` | D3 | **`result_code` int-vs-string contract break.** The API emits `result_code` as the raw XDR enum **integer** (`extract.go:98` `int32(tx.Result...Code)`; `OpView.ResultCode *int32`; OpenAPI `result_code: { type: integer }`). The UI types it `result_code?: string` and the per-op badge does `op.result_code && /success/i.test(op.result_code) ... {op.result_code}`. Three concrete breakages: (1) success code `0` is **falsy** in JS → the result badge is HIDDEN for every successful op; (2) `/success/i.test(0)` coerces to `"0"` → never matches → every *shown* op result renders in the failure (rose) colour; (3) the badge prints the bare integer (`-1`, `12`…) not a human label. | The tx-detail op-result UI is wrong for the common case (success shows nothing; failures mis-colour). It's the textbook int32-vs-string mismatch this audit was told to hunt. The `SuccessBadge` for the tx/ledger row (`code={t.result_code}`) is less broken — it only renders `code` when `!ok`, and shows the int as a title — but still typed `string`. | Decide one representation. Cheapest: keep the int wire, fix the UI — type `result_code?: number`, gate the badge on `op.result_code != null` (not truthiness), and map the int to a label (or just show `tx success` from the `successful` bool). Better long-term: have the API emit a string code name (`opSuccess` / `opNoTrust`) so `/success/i` works and the wire is self-describing — but that's a wire change touching OpenAPI + `extract.go` + the reader scan. | high |
| medium | `internal/storage/clickhouse/explorer_reader.go:266-282` (`TransactionByHash`) | D1/D5 | The hash lookup is `... WHERE tx_hash=? ORDER BY ingested_at DESC LIMIT 1` with **no `FINAL`** (correct — FINAL defeats the bloom skip-index). But `ingested_at` is `DateTime` (1-second resolution). On a re-ingest / ch-rebuild that re-writes the same tx within the same second as the original, two rows share an `ingested_at`; `ORDER BY ... DESC LIMIT 1` then picks an arbitrary one. Same-content rows → harmless; but if a rewrite legitimately *changed* a field, the "latest wins" intent isn't guaranteed at 1 s granularity. | Niche (requires a same-second rewrite with a changed value). The lake is ReplacingMergeTree so the duplicate eventually merges away, but until the merge settles a point lookup can return the stale row. The doc comment claims "takes the latest-ingested row" — true only to 1 s resolution. | Either accept + document the 1 s tie (the values are identical in the normal re-ingest case), or make `ingested_at` `DateTime64(3)` in the DDL for sub-second tie-breaking on hash/contract point lookups. Low urgency. | med |
| medium | `openapi/.../ContractEvent` (3826-3836) vs `TxEventView` (`explorer_tx.go:14-21`) vs `ContractEventView` (`explorer_contracts.go:11-20`) | D3 | **One OpenAPI schema (`ContractEvent`) documents two structurally different wire shapes.** `TxDetail.events[]` uses `TxEventView` which has `contract_id` (and NO `ledger`/`close_time`/`tx_hash`). `GET /contracts/{id}.events[]` uses `ContractEventView` which has `ledger`/`close_time`/`tx_hash` but NO `contract_id`. The single `ContractEvent` schema lists `ledger`/`close_time`/`tx_hash`/`op_index`/`event_index`/`event_type`/`topic_0` — i.e. it documents the contract-activity shape and **omits `contract_id`**, so the tx-detail `events[].contract_id` field is undocumented, and the documented `ledger`/`close_time`/`tx_hash` fields are absent from the tx-detail variant. | A generated client (or `pkg/client`) built from the spec will have a `ContractEvent` type missing `contract_id` (present on the tx-detail wire) and carrying `ledger`/`tx_hash` that are always empty on the tx-detail wire. The UI dodges this by hand-typing two interfaces (`TxEvent` has `contract_id`; `ContractEvent` doesn't — `explorer-shared.tsx:79-110`), but the spec is the contract of record and it's wrong for one of the two. | Split into two schemas: `TxContractEvent` (`op_index, event_index, contract_id, event_type, topic_0`) for `TxDetail.events`, and keep `ContractEvent` (with `contract_id` ADDED) for the contract-activity list. Regenerate `docs/reference/`. | med |
| low | `internal/api/v1/explorer_search.go:51-54` vs `web/explorer/.../ContractView.tsx:43` | D3/D5 | The search classifier routes a contract query to `href = /v1/contracts/{q}/transfers` (the SEP-41 trail), but the explorer's own contract page fetches `/v1/contracts/{id}` (the event-activity detail, `handleContractDetail`). Both endpoints exist and 200, so nothing is broken — but the canonical `href` the search API hands back points at a *different* surface than the UI uses for the same entity. | Inconsistent guidance: an API consumer following `search.href` lands on transfers; the first-party UI shows event activity. Cosmetic today (the UI navigates to its own `/contract?id=` route, not the API href), but it's a latent surprise for third-party clients. | Point the contract `href` at `/v1/contracts/{q}` (event activity is the general detail; transfers is the SEP-41 sub-view), or document the deliberate choice in the handler comment. | low |
| low | `internal/storage/clickhouse/explorer_reader.go:218-236` (`AccountTransactions`) + `241-259` (`AccountOperations`) | D5 | Keyset pagination is `WHERE source_account=? [AND ledger_seq<?] ORDER BY ledger_seq DESC, tx_index DESC`, cursor returned as `next_before = last row's Seq` (handler). The cursor is **ledger-granular**, but a single ledger can hold more rows for one account than `limit`. If account X has >limit txs in ledger N, paging with `before=N` re-fetches from the TOP of ledger N (`ledger_seq < N` excludes N entirely → the remaining rows IN ledger N are **skipped**), OR (if the boundary were inclusive) would loop. With strict `<` the tail of a hot ledger is silently dropped across the page boundary. | Edge case (an account with more than `limit` (≤200) txs/ops in one ledger), but on a busy market-maker account it's reachable and produces silent gaps in the paged history. | Make the cursor a composite `(ledger_seq, tx_index[, op_index])` and use a tuple comparison `WHERE (ledger_seq, tx_index) < (?, ?)`, mirroring how the ORDER BY is already composite. Same applies to `ContractEventsRecent` (`op_index/event_index` within a ledger). | low |
| low | `internal/storage/clickhouse/explorer_reader.go:300-318` (`OperationResultsByTx`) + `extract.go:184-196` | D5 | `operation_results` stores `result_xdr` (full base64) but the reader only ever projects `op_index, result_code`; nothing reads `result_xdr`, and the op-result wire is just the int code. So the inner-result detail (e.g. *which* path-payment offers filled, the `ManageOfferSuccess` claim atoms) is captured in the lake but never surfaced. | Not a bug — Phase-A scope is the code only. Noted because it's a known coverage stub: the data is there for a richer op-result view whenever the UI wants it. | None needed now; when op-result detail is wanted, decode `result_xdr` in a new `xdrjson` helper rather than re-deriving from MinIO. | low |

---

## X3 — written conclusions

### Index coverage vs every query predicate — SOUND (one keyset caveat)

Every WHERE clause maps to a usable sort-key prefix or a declared bloom
skip-index. Walked predicate-by-predicate against `tier1_schema.sql`:

| query | predicate | table ORDER BY / index | verdict |
|---|---|---|---|
| `RecentLedgers` | `ledger_seq < ?` ORDER BY seq DESC | PK `ledger_seq` | PK prefix — fast |
| `LedgerBySeq` | `ledger_seq = ?` | PK `ledger_seq` | PK point — fast |
| `LedgerTransactions` | `ledger_seq = ?` ORDER BY tx_index | PK `(ledger_seq, tx_index)` | PK prefix + partition-pruned |
| `OperationsByLedger` | `ledger_seq = ?` ORDER BY (tx_index, op_index) | PK `(ledger_seq, tx_index, op_index)` | PK prefix |
| `TransactionByHash` | `tx_hash = ?` | bloom `idx_tx_hash` | skip-index (needs MATERIALIZE on history) |
| `OperationsByTx` | `ledger_seq = ? AND tx_hash = ?` | PK `(ledger_seq, …)` | partition-pruned by ledger; tx_hash is a within-partition filter — fast (caller passes the ledger) |
| `OperationResultsByTx` | `ledger_seq = ? AND tx_hash = ?` | PK `(ledger_seq, tx_hash, op_index)` | PK prefix point |
| `EventsByTx` | `ledger_seq = ? AND tx_hash = ?` | PK `(ledger_seq, tx_hash, op_index, event_index)` | PK prefix |
| `ContractEventsRecent` | `contract_id = ? [AND ledger_seq<?]` | bloom `idx_contract_id` | skip-index |
| `AccountTransactions` | `source_account = ? [AND ledger_seq<?]` | bloom `idx_tx_source` | skip-index |
| `AccountOperations` | `source_account = ? [AND ledger_seq<?]` | bloom `idx_op_source` | skip-index |

**No full-scan risk found** for the predicate set — *provided* the three bloom
indexes are MATERIALIZEd over historical parts (the DDL comments flag this as a
required one-time `ALTER TABLE … MATERIALIZE INDEX` per index; new parts are
indexed on insert). That MATERIALIZE step is an operational precondition, not a
code defect — worth a deploy-checklist line so a fresh region doesn't full-scan
`tx_hash`/`contract_id`/`source_account` against pre-existing history. The only
correctness caveat is the ledger-granular keyset cursor (low finding above),
which is a *gap*, not a scan-cost issue.

### Wire-shape consistency lake → reader → handler → OpenAPI → UI

Walked every field across all five hops. Mostly clean; the load-bearing
amount/precision discipline (ADR-0003) is correct end-to-end. The exceptions
are the findings above. Highlights:

- **`result_code` (THE int-vs-string finding):** lake `Int32` → reader `int32`
  → handler `int32` JSON number → OpenAPI `integer` → **UI `string` (wrong)**.
  All hops *except the UI* agree on integer; the UI is the broken end. This is
  the high finding. Note the prompt hypothesised a possible "int32 vs UI string"
  mismatch — confirmed real, and it does break rendering, not just typing.
- **Amounts are strings throughout (correct, ADR-0003):** `total_coins` /
  `fee_pool` are `Int64` in the lake, `strconv.FormatInt` to STRING in
  `ledgerView`, `type: string` in OpenAPI, `string` in the UI (`Ledger.total_coins`),
  and `stroopsToXlm` parses the string. Op-body amounts (`amount`, `starting_balance`,
  path-payment legs, `limit`, clawback amount) are all rendered via
  `xdrjson.amount()` → `strconv.FormatInt` → decimal STRING inside the
  free-form `fields` map. Verified no `int64(parts.Lo)`-style truncation and no
  JSON-number amount anywhere on this path.
- **`fee_charged` / `max_fee` are JSON numbers (acceptable):** capped well below
  2^53 (fees are stroops, bounded), `int64` → JSON number → OpenAPI `integer` →
  UI `number`. Consistent and safe.
- **`memo_type` normalisation is correct + idempotent:** the lake stores the SDK
  enum string `MemoTypeMemoText` (verified: SDK `MemoType()` returns
  `memoObject.Type.String()`), the handler runs `xdrjson.MemoTypeName` →
  `text`/`none`/…; `MemoTypeName("text")` is idempotent (prefix-strip misses,
  lower-cases a no-op) so re-normalisation is safe. OpenAPI documents the
  normalised vocabulary. The stub test pins both raw and normalised inputs.
- **`op_type` fallback path is consistent:** lake stores `op.Body.Type.String()`
  (`OperationTypePayment`); the happy path uses `xdrjson.OpTypeName` (controlled
  snake_case); the decode-error fallback uses `normalizeLakeOpType` which
  strips exactly the `OperationType` prefix the writer produced. Aligned.
- **`ContractEvent` schema serves two divergent shapes** (medium finding) —
  the only structural wire/spec divergence besides `result_code`.

### bloom-index-vs-FINAL discipline — CORRECT

The split is applied exactly right and is the subtlest part of this code:

- **FINAL is used** on the by-PK reads where it's free of skip-index cost:
  `RecentLedgers`, `LedgerBySeq`, `LedgerTransactions`, `OperationsByLedger`
  (all keyed on the `ledger_seq` PK prefix). FINAL here gives correct read-time
  dedup of un-merged ReplacingMergeTree parts.
- **FINAL is deliberately OMITTED** on the three bloom-skip-index reads —
  `TransactionByHash`, `ContractEventsRecent`, `AccountTransactions`/
  `AccountOperations` — because (as the DDL + reader comments both state) FINAL
  forces a full merge that defeats the skip-index. `TransactionByHash`
  compensates with `ORDER BY ingested_at DESC LIMIT 1` (latest-wins by hand);
  the others tolerate transient pre-merge dupes (acceptable for an
  append-mostly lake; dupes converge on merge). The only residual nit is the
  1 s `ingested_at` tie on `TransactionByHash` (medium finding).
- `OperationsByTx` / `OperationResultsByTx` / `EventsByTx` are ledger-scoped PK
  reads (the caller threads the ledger from `TransactionByHash`), so they are
  partition-pruned and fast without either FINAL or a skip-index — the right
  call.

### xdrjson decode coverage (informational, not a defect)

15 of 28 op types are field-decoded; the other 12 (`set_options`, claimable-
balance pair, sponsorship trio, `revoke_sponsorship`, `clawback_claimable_balance`,
LP deposit/withdraw, `inflation`, footprint TTL/restore) degrade to `raw_xdr`
— **lossless** (the base64 body is preserved) and matches the documented
Phase-A scope. `DecodeOperationBody` never panics on a bad body (returns an
error → handler falls back to `normalizeLakeOpType` + raw). The `price` helper
renders `{n,d}` as a rational (correct — no float). `ParticipantAccounts` is a
generic G-strkey harvester over decoded fields for the Phase-B participant
index — sound, but note it ONLY finds participants in the 15 field-decoded op
types; the 12 raw-XDR types contribute no participants until decoded (a
forward coverage gap for Phase B, not a current bug).

---

## X5 — written conclusions

### v1.ExplorerReader ↔ impl ↔ stub — IN SYNC

The interface (`explorer.go:16-28`, 11 methods) has exactly two implementers:
- `*clickhouse.ExplorerReader` (production) — all 11 methods present with
  matching signatures; verified method-by-method against the interface.
- `*stubExplorerReader` (`explorer_ledgers_test.go:13-81`) — all 11 methods
  present.

**The stub does NOT mask real behaviour in a dangerous way**, but two stub
simplifications are worth recording: (1) `LedgerTransactions` / `OperationsByLedger`
/ `OperationsByTx` / `AccountTransactions` / `AccountOperations` ignore their
ledger/account/limit args and return the same backing slice — so the tests
exercise the handler's view-mapping + pagination-cursor logic but NOT the
reader's predicate/sort correctness (that's only provable against a real CH,
i.e. integration). (2) The stub's `OperationResultsByTx` returns a fixed map
regardless of tx — fine for the view test. No method returns a *wrong-shaped*
or *over-permissive* result that would let a handler bug pass. Compile-time
conformance is guaranteed (the stub is passed as `v1.ExplorerReader` in
`explorerTestServer`), so a future interface method addition fails the build
until the stub catches up — good.

One robustness note carried from X3: there is no compile-time assertion
`var _ v1.ExplorerReader = (*clickhouse.ExplorerReader)(nil)` in the
clickhouse package (the binding is only proven where `Options.Explorer` is
assigned in the binary). Not a defect — the assignment proves it — but a
one-line `var _` guard in the clickhouse package would localise the failure if
a signature drifts.

### Dispatcher's 4 decoder interfaces ↔ implementations — ALL PRESENT & SYMMETRIC

| interface | impls found | shape check |
|---|---|---|
| `Decoder` (event) | 12 (`cctp, reflector, sep41_supply, aquarius, sep41_transfers, phoenix, soroswap, redstone, blend, comet, defindex, rozo`) | `Name()/Matches(events.Event)/Decode(events.Event)` |
| `OpDecoder` | 1 (`sdex`) | `Name()/Matches(xdr.Operation)/Decode(OpContext)` |
| `ContractCallDecoder` | 2 (`band, soroswap_router`) | `Name()/Matches(contractID,functionName)/Decode(ContractCallContext)` |
| `LedgerEntryChangeDecoder` | 5 (`trustlines, liquidity_pools, claimable_balances, accounts, sac_balances`) | `Name()/Matches(xdr.LedgerEntryChange)/Decode(LedgerEntryChangeContext)` |

All four share the **same documented non-fatal-error contract** ("Decode error
= skip + count, never stop dispatching"), and the dispatcher honours it
uniformly (the per-source `eventsSeen`/`decodeErrors` counters are bumped
pre-Decode under `statsMu`). The implementations were not individually
behaviour-diffed here (that's the per-source areas A05/A08), but the seam
shapes are consistent and every interface has ≥1 impl. The contract-call seam's
`CallPath` and the entry-change seam's `OpIndex == -1` fee-meta convention are
documented on the context structs and matched by the lake writer
(`extract.go` / `extract_entry_changes.go` use the same `op_index -1`
fee-meta tagging — cross-checked in X3).

### external.Connector sub-interfaces — COHERENT

`Connector` (root: `Name()/Class()`) + three optional sub-interfaces each
embedding `Connector`: `Streamer` (Start→channel), `Poller`
(PollOnce/PollInterval), `Backfiller` (Backfill). The framework's `TradeEvent`
/ `UpdateEvent` wrappers implement `consumer.Event` and mirror the on-chain
per-source wrappers so the sink type-switch stays uniform (consistent with the
A01 finding about `IsProjectedEvent` lockstep). The split is clean — a venue
implements `Connector` + whichever of the three it supports. Per-impl
conformance is the A06 area's job; the seam itself is sound.

### HistoryReader / FXHistoryReader (adjacent, partial) — no divergence found

`HistoryReader` is wrapped by `CachedHistoryReader` (embedded interface → every
method pass-through unless overridden), so the cache decorator can't silently
drop a method — additions are inherited. `FXHistoryReader` is a separate read
seam (currencies). Neither is on the explorer path; checked only for
embedding/decorator soundness — clean. (The historical "entries 0" delegate
bug in MEMORY.md was a *missing optional delegate*, not an interface
divergence; not re-found here.)

---

## Verified CORRECT (recorded so the next pass doesn't re-litigate)

- **ADR-0003 (i128/Int64 never truncates):** every amount on the explorer wire
  is a decimal string (`total_coins`, `fee_pool`, all op-body amounts) — no
  JSON-number amount, no `int64(parts.Lo)` pattern. (`result_code` is an
  enum int, not a token amount — correctly an integer.)
- **ADR-0034 (CH is the lake, served from CH for the explorer):** the explorer
  reader reads ClickHouse directly, never Postgres — correct per ADR-0038.
- **503 fallback:** every explorer handler guards `s.explorer == nil` →
  `explorerUnavailable` (503); pinned by `TestExplorer_Unavailable503`.
- **Input validation:** tx-hash (64-hex, case-normalised), ledger seq (uint32),
  contract id (C-strkey via `canonical.IsContractID`), account (G-strkey via
  `canonical.IsAccountID`), limit (bounded per-endpoint), `before` (uint32) —
  all validated before any lake read, each with a problem+json 400.
- **Decode never aborts a response:** `opView` degrades a bad/unknown op body to
  type + raw_xdr; `OperationResultsByTx` / `EventsByTx` errors are non-fatal in
  the tx-detail handler (op list still served).
- **OpenAPI servers base = `/v1`** so spec paths (`/ledgers`, `/tx/{hash}`)
  correctly map to the registered `/v1/...` routes; route table in
  `server.go:1025-1033` matches the spec's explorer path set 1:1.
- **`opArgsByIndex` ↔ event `op_args_xdr` parity:** the lake writer stamps each
  event's producing-op InvokeContract args (same MarshalBinary + base64.Std as
  the dispatcher) so Redstone/Band-class decoders read identical bytes from CH
  — cross-checked in `extract.go:109-144`.

---

## Severity counts

- high: 1 (`result_code` int-vs-string UI break)
- medium: 2 (`TransactionByHash` 1 s `ingested_at` tie; `ContractEvent` OpenAPI
  schema serves two divergent shapes / omits `contract_id`)
- low: 3 (contract search href→transfers vs UI→detail; ledger-granular keyset
  cursor drops the tail of a hot ledger; `result_xdr` captured-but-unread)

No Critical. The single High is a UI-side contract bug (the API/spec/lake all
agree `result_code` is an integer; only the web explorer mistypes it as a
string and renders incorrectly).
