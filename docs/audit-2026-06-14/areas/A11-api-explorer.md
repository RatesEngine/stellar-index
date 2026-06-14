# A11 — Network-explorer API (ADR-0038) — read-only audit

Scope: the NEW, unreviewed network-explorer surface — handlers
(`internal/api/v1/explorer*.go`), the XDR→JSON decoder
(`internal/xdrjson/`), the ClickHouse read path
(`internal/storage/clickhouse/explorer_reader.go`), route registration
(`internal/api/v1/server.go`), binary wiring
(`cmd/stellarindex-api/main.go`), and the OpenAPI explorer paths.

Audited against D1 (correctness), D2 (ADR invariants), D3 (security),
D4 (shared state), D7 (API contract), D9 (degrade-not-panic).

Date: 2026-06-14. Method: full read of all 14 in-scope source/test
files + the ClickHouse reader + SDK type cross-checks (go-stellar-sdk
v0.5.0) + OpenAPI parity diff. No source edited, no git run.

---

## Findings

| severity | file:line | dim | issue | why it matters | fix | confidence |
|---|---|---|---|---|---|---|
| **High** | explorer_contracts.go:67-69, explorer_reader.go:337-365 | D1 | Keyset pagination cursor is `ledger_seq` ONLY (`next_before = rows[n-1].Seq`), but a contract can emit many events in one ledger. ORDER BY is `ledger_seq DESC, op_index DESC, event_index DESC` yet the next-page predicate is `ledger_seq < before`. When a single ledger holds > `limit` matching events, the next page skips the **rest of that ledger** → silent row loss across the page boundary. | A heavy contract (e.g. an AMM router) routinely emits >100 events in a busy ledger; default limit is 100. Clients paging "all events for this contract" lose data with no error. | Cursor must be the full composite `(ledger_seq, op_index, event_index)` and the predicate a tuple comparison (`ledger_seq < b OR (ledger_seq=b AND (op_index,event_index) < ...)`), or page within a ledger before descending. | High |
| **High** | explorer_accounts.go:77-79 & 116-118, explorer_reader.go:218-259 | D1 | Same `ledger_seq`-only cursor bug for `/v1/accounts/{g}/transactions` and `/operations`. ORDER BY `ledger_seq DESC, tx_index DESC[, op_index DESC]`; next-page predicate `ledger_seq < before`. An account that submits > `limit` txs/ops in one ledger loses the remainder on the page boundary. | Active accounts (market makers, sweepers) submit many ops per ledger. Default limit 50. Page boundary lands mid-ledger → dropped rows, no signal. | Same composite-cursor fix; the cursor needs `tx_index` (txs) / `tx_index,op_index` (ops) too. | High |
| **High** | openapi/stellar-index.v1.yaml (no entry) vs server.go:1032-1033 | D7 | The two account endpoints `GET /v1/accounts/{g_strkey}/transactions` and `/operations` are REGISTERED + shipped but **completely absent from OpenAPI**. The `AccountTransactionsView` / `AccountOperationsView` wire shapes (incl. the `scope` field) and the 503/400 codes are undocumented. | OpenAPI is the source-of-truth API contract (CLAUDE.md "Change the OpenAPI spec"); docs-gen + contract tests can't cover what isn't there. Clients have no spec for a live surface. | Add `/accounts/{g_strkey}/transactions` + `/operations` paths + `AccountTransactions`/`AccountOperations` schemas (with `scope`, `next_before`); bump API minor. | High |
| Med | xdrjson/operation.go:98,106,111,153 (Destination/dest via `MuxedAccount.Address()`); operation.go:152 (account_merge) | D9 | `PaymentOp.Destination`, both `PathPayment*.Destination`, and `account_merge`'s `MustDestination()` are `xdr.MuxedAccount`, whose `.Address()` **panics** on an unknown crypto-key type (SDK muxed_account.go:110 `panic(err)`). `DecodeOperationBody`/`fillOpFields` has no recover. A crafted/garbage body that unmarshals into a MuxedAccount with an unrecognised type panics the whole request. | Contradicts the stated design ("a single malformed/unknown op never fails the response", opView doc). The Recoverer middleware (server.go:866) catches it → clean 500, so it's not a crash — but it's a 500 on the WHOLE tx/ops response, not the intended per-op RawXDR degrade. | Use `MuxedAccount.GetAddress()` (returns err, no panic) and fall through to RawXDR / `"unknown_address"` on error; or wrap `fillOpFields` body in a recover that demotes to RawXDR. | Med |
| Med | xdrjson/operation.go:162 (`bump_to`), 123/130 (`offer_id`) | D2 | `bump_to` (`SequenceNumber` = Int64) and `offer_id` (Int64) are emitted as raw JSON **numbers** (`int64(...)`). Sequence numbers are `ledgerSeq<<32` so `bump_to` routinely exceeds 2^53 → IEEE-754 precision loss in the JSON. offer_id can also exceed 2^53 long-term. This is exactly the ADR-0003 failure class the rest of the file (amounts, total_coins) correctly stringifies. | A `bump_sequence` op's `bump_to` will be silently corrupted for any modern ledger (seq ~63M << 32 ≈ 2.7e17 ≫ 9e15). Wrong data on a correctness-promising explorer. | Render `bump_to` (and arguably `offer_id`) as decimal strings via `strconv.FormatInt`, consistent with `amount()`. | High |
| Med | xdrjson/participants.go:21-44 | D1 | `ParticipantAccounts` only collects field values that pass `canonical.IsAccountID` (G-strkeys). But payment/path-payment destinations decode to whatever `MuxedAccount.Address()` returns — an **M-address** for muxed destinations — which `IsAccountID` rejects. So a payment to a muxed account contributes NO participant. The doc claims it picks up "destination / to_address / etc." generically. | The Phase-B participant index (the whole point of this fn) will silently miss every muxed-destination payment — a real and growing share of traffic post-CAP-67. | Also accept M-addresses (resolve to underlying G via `strkey`/`MuxedAccount`), or decode destinations to their underlying G in the field map. At minimum document the muxed gap. | Med |
| Low | xdrjson/participants.go:33 + helpers.go:75-94 | D1 | The "generic G-strkey extraction" is value-shape based, so it is correct only because no decoded **non-account** field currently emits a valid G-strkey. `contractAddress()` can emit a G-strkey (the ScAddress account case) into `contract_id`; that field is only set inside `invoke_host_function` (no participant call there today), so no live false-positive — but the invariant "only real participants are valid G-strkeys among field values" is implicit and fragile. A future decoded field carrying an account string that is NOT a counterparty would be wrongly indexed. | Latent false-positive risk as the decoder grows; the design has no per-type allowlist to anchor it (acknowledged in the doc as "deliberately generic"). | Consider a small per-op participant-field allowlist, or tag participant fields explicitly, rather than shape-sniffing all field values. | Med |
| Low | explorer_ledgers.go:118-120, explorer_contracts.go:67-69, explorer_accounts.go:77-79,116-118 | D1 | `next_before` is emitted whenever `n>0`, including the final (short) page. A client always gets a cursor and must make one extra request that returns an empty list to learn it's done. No infinite-loop risk (strict `<`), just one wasted round-trip and no "last page" signal. | Minor inefficiency / ambiguous termination; common pattern but worth noting under the correctness lens. | Only set `next_before` when `n == limit` (more-pages heuristic), or add an explicit `has_more`. | High |
| Low | explorer_ledgers.go:187-201 (LedgerTransactions), explorer_operations.go:107 (OperationsByLedger), explorer_reader.go:148-161,196-208,286-295 | D1 | `LedgerTransactions`, `OperationsByLedger`, and `OperationsByTx` (the per-ledger/per-tx listings) have a `LIMIT` but NO pagination cursor. A ledger with more txs/ops than the cap (200/2000) silently truncates with no `next_*` and no indication rows were dropped. A pathological tx with >limit ops in OperationsByTx (no LIMIT there, OK) — but ledger-level lists can truncate. | Completeness gap on high-volume ledgers; the explorer claims full-chain fidelity. Caps are generous so rare in practice. | Add keyset paging (tx_index / op_index cursor) to the ledger-scoped lists, or document the cap as a hard truncation. | Med |
| Low | explorer_reader.go:266-282 (TransactionByHash), 337 (ContractEventsRecent) | D1 | Reads that drop `FINAL` to keep the skip-index (documented) take "latest-ingested" via `ORDER BY ingested_at DESC LIMIT 1` (tx) or no dedup (contract events). If a ledger was re-ingested (lake replay / ch-rebuild), a superseded duplicate row could surface, or contract-event lists could show dupes. | Edge case tied to re-ingest; the ReplacingMergeTree semantics mean pre-merge duplicates are possible. Low likelihood on steady state. | Acceptable trade-off if documented; otherwise dedup in-app on `(ledger,tx_index)` / `(ledger,op_index,event_index)`. | Low |
| Info | explorer_search.go:51-54 vs 62-65 | D7 | A C-strkey is classified `kind=contract` (href `/v1/contracts/{c}/transfers`) BEFORE the asset branch, even though `ParseAsset` would also accept it as a Soroban asset. Deliberate precedence, but means a SAC contract id never resolves to its asset view via search. | Documented intent; flagging for href-correctness review only. | None needed; confirm the explorer UI expects contract precedence. | High |
| Info | explorer_search.go:56-60 | D7 | Account search href points at `/v1/issuers/{g}` (issuer view), NOT the new `/v1/accounts/{g}/transactions`. The `note` field explains it, and `supported=true`. | UX choice; href is to a real endpoint, so no contract break. | Reconsider routing accounts to the new activity endpoint once Phase B is GA. | High |
| Info | explorer_operations.go:61-63 | D1 | `normalizeLakeOpType` (decode-error fallback) lowercases the lake enum without snake_case insertion → e.g. `manage_sell_offer` becomes `managesselloffer` (note: also a typo'd example in the comment, "managesselloffer"). Only hit on the decode-failure path; the happy path uses the controlled `OpTypeName` vocabulary. | Cosmetic wire inconsistency on the rare decode-failure fallback; the `type` won't match the snake_case vocabulary clients expect. | Map via `OpTypeName(parsedEnum)` if the enum can be recovered, or accept the degraded form as best-effort. | High |

---

## CORRECT — verified, no issue

- **D2 amounts as strings (ADR-0003):** Every classic Int64 amount in
  `xdrjson` goes through `amount()` → `strconv.FormatInt` decimal
  string: payment amount, create_account starting_balance, both
  path-payment send/dest legs, manage(sell/buy)/passive offer amounts,
  change_trust limit, clawback amount. `LedgerView.TotalCoins`/`FeePool`
  are stringified (`strconv.FormatInt`) and the struct fields are
  `string`. Test `TestExplorer_LedgersList` asserts the exact
  `5000000000000000000` string round-trips. `fee_charged`/`max_fee`
  correctly kept as JSON numbers (capped ≪ 2^53). (bump_to/offer_id are
  the two exceptions — see Med finding.)
- **D2 assets as canonical CODE-ISSUER:** `assetID` emits `native` /
  `CODE-ISSUER` (dash form) for alphanum4/12, trims NUL padding
  (`assetCode`), and `assetPath` maps the path. `changeTrustAsset`
  adds the `liquidity_pool_share` marker for pool-share lines. Matches
  the rest of the API's asset id form. Tests cover native + credit dash
  form.
- **D1 op-type vocabulary:** `opTypeName` is a controlled map (27
  entries) → stable snake_case; unknown enums → `unknown_<n>`
  (`TestOpTypeName_Unknown`). Not derived from the SDK CamelCase string.
- **D1 field decode for the field-decoded op types** (createAccount,
  payment, both path-payments, manageSell/Buy/passive offers,
  changeTrust, allowTrust, setTrustLineFlags, accountMerge, manageData,
  bumpSequence, clawback, invokeHostFunction) — field names + types
  cross-checked against go-stellar-sdk v0.5.0 struct definitions; the
  selected fields and `Must*()` accessors match the op variant. Op
  types in `opTypeName` but NOT field-decoded (set_options, create/claim/
  clawback claimable balance, sponsorship ops, LP deposit/withdraw,
  ttl/restore footprint) correctly degrade to `RawXDR` (Fields empty)
  — nothing lost.
- **D1 `price()`** renders `xdr.Price` as `{n,d}`; `Price.N/D` are
  already `Int32` so `int32(p.N)` is a lossless no-op (verified SDK
  type). Test asserts n=7,d=2.
- **D9 graceful decode degrade:** `opView` (explorer_operations.go:42-55)
  treats a `DecodeOperationBody` error by falling back to the lake op
  type + RawXDR — never errors the response. `TestExplorer_TxDetail`
  feeds `BodyXDR:"not-valid-xdr"` and asserts a 200 with the op present.
  (The MuxedAccount.Address panic path is the one gap — see Med finding;
  it is still contained by the Recoverer middleware to a 500, not a
  crash.)
- **D9 base decode safety:** `xdr.SafeUnmarshalBase64` is used (not the
  panicking unmarshal); errors are wrapped + returned, not panicked.
- **D3 SQL injection:** Every ClickHouse query in `explorer_reader.go`
  uses `?` placeholders with `conn.Query(ctx, q, args...)`; user input
  (seq, hash, account, contract, limit, beforeLedger) is ALWAYS a bound
  arg, never string-concatenated into SQL. The only string concat is
  static column-list / clause assembly (`ledgerCols`, ` WHERE ... < ?`)
  with no user data. Clean.
- **D3 input validation:** tx hash gated by `^[0-9a-f]{64}$` after
  lowercasing (400 on miss); contract id via `canonical.IsContractID`
  (SEP-23 CRC-checked, not regex); account via `canonical.IsAccountID`
  (CRC-checked G-strkey); ledger seq via `ParseUint(...,32)`;
  `limit` capped per-endpoint with `[1,maxN]` (400 out of range);
  `before` parsed as uint32 (400 on malformed). The reader ALSO
  defensively re-clamps limits. Search `q` trimmed + required (400 on
  empty).
- **D3 no internal-error leak:** all 500 paths call `writeProblem(...,
  "Internal error", 500, "")` with an EMPTY detail; the real error goes
  only to `s.logger.Error`. The Recoverer middleware likewise sends a
  generic problem+json and logs the panic/stack server-side. No stack /
  driver text reaches the client.
- **D3 client-abort handling:** every reader-error site checks
  `clientAborted(r, err)` first → returns silently (499) instead of a
  misleading 500. Correct per the documented decision rule.
- **D1 404/400/503:** ledger/tx not-found → 404; invalid seq/hash/
  strkey/contract/limit/before → 400; nil explorer reader → 503 on every
  explorer route (`TestExplorer_Unavailable503`). `handleSearch` is the
  one route that intentionally works without the reader (pure
  classification) — correct, it does no lake read.
- **D1 ledger pagination keyset:** for `/v1/ledgers`,
  `next_before = rows[n-1].Seq` with predicate `ledger_seq < before`
  (strict) is correct AND safe — exactly one row per ledger_seq, so the
  composite-cursor bug above does NOT apply here. No off-by-one, no
  infinite loop (strict `<` guarantees forward progress; empty page
  terminates). Test asserts `next_before==99`.
- **D1 MemoTypeName / OpTypeName normalization:** `MemoTypeName`
  strips `MemoTypeMemo` prefix, lowercases, maps "" → "none"
  (`TestMemoTypeName` covers none/text/id/hash + empty); produces
  none|text|id|hash|return matching the OpenAPI `memo_type` enum doc.
- **D1 combine/merge logic:** `buildTxOpViews` correctly joins ops with
  `OperationResultsByTx` by `op_index` and attaches a per-op
  `result_code` pointer (nil when absent); `OperationResultsByTx` /
  `EventsByTx` failures are non-fatal (ops still served) — test
  exercises the result-code attach. `buildTxEventViews` returns nil for
  empty (omitempty) rather than `[]`.
- **D4 shared state:** No package-level mutable state. `txHashRe` is a
  compiled `regexp.Regexp` (concurrency-safe, read-only). `opTypeName`
  is a read-only map. Every handler builds fresh per-request structs;
  decoders are pure functions over their args. The `ExplorerReader`
  conn pool is the only shared object and is the intended
  construct-once/reuse pattern (driver pool is concurrency-safe). No
  data races introduced.
- **D7 envelope consistency:** every explorer 200 goes through
  `writeJSON(...)` → the standard `{data, as_of, sources, flags}`
  envelope; tests decode `{ "data": ... }`. Errors go through
  `writeProblem` → RFC 9457 problem+json with `Cache-Control:
  no-store`. Consistent with the rest of v1.
- **D7 documented-path parity (ledgers/tx/operations/contracts/search):**
  the 7 documented explorer paths' params (limit min/max/default,
  before, ledger, seq, hash, contract_id, q), response shapes (data
  envelope, ledgers[]/next_before, transactions[], operations[],
  events[], next_before, search classification fields incl. enum), and
  status codes (200/400/404/503) all match the handlers. Component
  schemas Ledger/TxSummary/Operation/TxDetail/ContractEvent exist and
  align field-for-field (total_coins/fee_pool typed `string`; amounts
  noted as strings).
- **D7 search classifier hrefs:** tx → `/v1/tx/<lowerhash>`, ledger →
  `/v1/ledgers/<seq>`, contract → `/v1/contracts/<c>/transfers`,
  account → `/v1/issuers/<g>` (with explanatory note), asset →
  `/v1/assets/<canonical>`. All point at real, registered routes;
  `supported` set correctly; unknown → supported=false + note. Tests
  cover all 8 kinds incl. fiat:USD and the dash-form asset.

---

## Files read (14 in-scope + 6 cross-reference)

In-scope (14):
- internal/api/v1/explorer.go
- internal/api/v1/explorer_ledgers.go
- internal/api/v1/explorer_ledgers_test.go
- internal/api/v1/explorer_operations.go
- internal/api/v1/explorer_tx.go
- internal/api/v1/explorer_contracts.go
- internal/api/v1/explorer_accounts.go
- internal/api/v1/explorer_search.go
- internal/api/v1/explorer_search_test.go
- internal/xdrjson/operation.go
- internal/xdrjson/operation_test.go
- internal/xdrjson/helpers.go
- internal/xdrjson/participants.go
- internal/xdrjson/participants_test.go

Cross-reference (6):
- internal/storage/clickhouse/explorer_reader.go (SQL/field semantics)
- internal/api/v1/server.go (route registration + middleware chain)
- cmd/stellarindex-api/main.go (reader wiring)
- internal/api/v1/envelope.go (writeJSON/writeProblem/clientAborted)
- internal/api/v1/middleware/recoverer.go (panic containment)
- internal/canonical/strkey.go + asset.go (validation + ParseAsset)
- openapi/stellar-index.v1.yaml (explorer paths + schemas)
- go-stellar-sdk@v0.5.0 xdr types (MuxedAccount.Address, op structs,
  Price/Int64/SequenceNumber) — type cross-check.

---

## Severity counts

- Critical: 0
- High: 3
- Medium: 3
- Low: 4
- Info: 3
