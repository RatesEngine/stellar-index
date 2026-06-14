# A17 — Explorer Web UI (Next.js static-export, network-explorer pages)

READ-ONLY audit. Scope: `web/explorer/src/**` (.ts/.tsx/.css), with
focus on the new ADR-0038 Phase-D explorer pages — `app/ledgers/`,
`app/ledger/`, `app/tx/`, `app/contract/`, `app/explorer-shared.tsx`,
and the modified `components/nav/Navbar.tsx` + `components/nav/SearchModal.tsx`.

Audited vs: **D1** correctness (query-param-page choice under
`output:'export'`, React Query usage, API wire-shape match, pagination,
loading/error/empty states, stroops→XLM, bigint precision), **D2**
brand-clean + default base URL, **D3** security (XSS, baked secrets,
open-redirect), **D7** search wiring.

Cross-references **A11-api-explorer.md** (the backend side of the same
endpoints). A11 already documents the server-side within-ledger
pagination loss + `total_coins`/`bump_to` precision. This area focuses
on how the **frontend consumes** those wire shapes; overlaps are noted.

**Files read: 24** — `app/explorer-shared.tsx`, `api/client.ts`,
`api/types.ts` (partial — generated OpenAPI types, 5775 lines),
`api/hooks.ts`, `app/ledgers/page.tsx`, `app/ledgers/LedgersTable.tsx`,
`app/ledger/page.tsx`, `app/ledger/LedgerView.tsx`, `app/tx/page.tsx`,
`app/tx/TxView.tsx`, `app/contract/page.tsx`,
`app/contract/ContractView.tsx`, `components/nav/SearchModal.tsx`,
`components/nav/Navbar.tsx`, `components/reveal/Panel.tsx`,
`components/reveal/RequestReveal.tsx`, `next.config.mjs`,
`app/layout.tsx`, `lib/format.ts`, `lib/safe-domain.ts`; plus 4
backend cross-check files (`explorer_ledgers.go`, `explorer_tx.go`,
`explorer_contracts.go`, `explorer_operations.go`, `explorer_search.go`,
`envelope.go`, `explorer_reader.go`).

---

## Findings

| Severity | File / location | Lens | Finding | Impact | Fix |
| --- | --- | --- | --- | --- | --- |
| **High** | `explorer-shared.tsx:55-59,87-95` (types) + `tx/TxView.tsx:148,264-274,398-411`, `ledger/LedgerView.tsx:328,359-371` | D1 | **`result_code` wire-type mismatch.** Frontend types `LedgerTransaction.result_code`, `TxSummary.result_code`, `TxOperation.result_code` as **`string`**, but the backend (`explorer_ledgers.go:62` `ResultCode int32`, `explorer_operations.go:27` `*int32`, set from `int32(tx.Result.Result.Result.Code)`) sends a **numeric** XDR code (e.g. `0` = txSUCCESS). The UI renders it as text: `SuccessBadge` shows `{code ?? 'failed'}` and op cards do `/success/i.test(op.result_code)`. With a number `0`, `/success/i.test(0)` coerces to `"0"` → false → a **successful op renders a red "0" badge**; the tx-level badge shows the raw integer instead of a label. | Every operation result badge and any non-success tx code is mislabelled on the new tx/ledger pages — a correctness-promising explorer shows wrong success/fail state. | Decide the contract: either backend maps the int code → string label (`txSUCCESS`, `op_inner`…) before serializing, or frontend types it `number` and maps codes to labels. Don't `regex.test()` a number. |
| **High** | `tx/TxView.tsx:175-181`, `ledger/LedgerView.tsx:314-320`, `contract/ContractView.tsx` (via search), `SearchModal.tsx:80` | D1 | **Source-account / account links point at a static-export 404.** The new pages link every tx `source_account` and op `source_account` to `/issuers/{account}`, and SearchModal routes a classified `account` kind to `/issuers/{canonical}`. But `app/issuers/[g_strkey]/page.tsx` is a server component whose `generateStaticParams` pre-renders only the **top ~100 issuers** with no `dynamicParams`/client fallback. Under `output:'export'`, any g-strkey not in that set (i.e. almost every ordinary source account) **hard-404s**. | The dominant link target on the new tx/ledger pages is broken for the common case (non-issuer accounts). Search "jump to account" 404s for any account that isn't a top issuer. | Either build a query-param account page (`/account?id=` like ledger/tx/contract — the consistent ADR-0038 pattern for unbounded entities), or make `issuers/[g_strkey]` a client-fallback shell that fetches at runtime, or don't link non-issuer accounts. |
| **Med** | `explorer-shared.tsx:143-159` (`stroopsToXlm`) consumed by `ledger/LedgerView.tsx:137` (total_coins), `:142` (fee_pool) | D1 / ADR-0003 | **`total_coins` loses precision through JS `Number()`.** Backend correctly sends `total_coins`/`fee_pool` as decimal **strings** (`explorer_ledgers.go:24-25`, ADR-0003), but `stroopsToXlm` does `Number(raw)` then `/1e7`. `total_coins` ≈ 1.05e18 stroops, ~117× past `2^53` (9.0e15) → the exact stroop remainder is lost. The whole-XLM part survives (we show ≤7dp after /1e7), but the displayed value is no longer faithful to the wire string — the same ADR-0003 failure class the API layer is careful to avoid. (`fee_pool`/`fee_charged`/`max_fee` are < 2^53, safe.) | Display-only fidelity loss on the ledger header's headline number, on the one page whose pitch is "straight from the certified raw lake". | Format large stroop strings without `Number`: BigInt-divide by 10^7 (quotient + 7-dp remainder), or use a decimal-string formatter. Reserve the `Number()` fast-path for values provably < 2^53. |
| **Med** | `contract/ContractView.tsx:170-177` (Load older) ← backend `explorer_reader.go:337-365` | D1 | **"Load older" can silently skip events within a saturated ledger.** The contract events cursor is `next_before = rows[n-1].Seq` (ledger only) with backend predicate `ledger_seq < before`, while ORDER BY is `(ledger_seq DESC, op_index DESC, event_index DESC)`. When one ledger holds more than `PAGE_SIZE` (50) matching events for a hot contract (an AMM router/pool routinely emits >100/ledger), paging past that boundary **drops the rest of that ledger's events**. Frontend-visible as missing rows in the contract activity table with no error. (Root cause is server-side — already filed in A11 as High; recorded here as the user-facing symptom.) | Contract pages for high-traffic Soroban contracts under-report events; "complete per-protocol data" claim is violated on exactly the busiest contracts. | Backend: composite-tuple keyset cursor. Frontend can't fix it but the page should not advertise completeness until it's a full cursor. |
| Low | `explorer-shared.tsx:178-188` (`relativeAge`) | D1 | `relativeAge` uses `Date.now()` in render without any re-render trigger; a long-lived ledgers/contract table shows "Xs ago" frozen at first paint until React Query refetch re-renders (10s/30s). Minor staleness, not wrong. | "12s ago" can sit stale between refetches. | Acceptable given refetch cadence; optionally tick a timer. Note only. |
| Low | `LedgersTable.tsx:171`, `contract/ContractView.tsx:175-177` (`next_before`) ← backend | D1 | `next_before` is emitted on every non-empty page incl. the final short page (A11 Low), so "Load older →" stays enabled one click past the real end and that click returns an empty page. The empty-state then renders "No ledgers/events" replacing the rows. Minor: a dead-end click that blanks the view rather than disabling at the true end. | One confusing wasted click at the end of pagination; momentary empty view. | Backend: only set `next_before` when `n == limit`. Frontend: could also stop advancing when a page returns `< PAGE_SIZE`. |
| Low | `app/layout.tsx:99` (`re.theme`), `:113-118` (`re-build-sha`/`re-build-time` meta), `next.config.mjs:32` comment | D2 | Residual **`re.` (Rates-Engine) brand prefix** in the localStorage theme key + the machine-readable build meta tags (`re-build-sha`, `re-build-time`). Not user-visible, but the meta names are documented for `curl … | grep re-build` and the comment still says "Rates-Engine". | Cosmetic brand drift post-rebrand (ADR-0036/0037); no functional impact. Changing the localStorage key would reset users' theme once. | Optional: rename meta to `si-build-*`; leave the storage key or migrate with a fallback read. |
| Low | `app/research/architecture/[slug]/page.tsx:16`, `lib/architecture.ts:30` | D2 | Two stale **"Rates-Engine"** mentions in code comments (out of new-page scope but found in the full src sweep). | Comment-only brand drift. | Trivial rename. |
| Info | `SearchModal.tsx:81-88` (`explorerHref` asset case) vs `explorer_search.go:65` | D7 | The `asset` branch does `if (c.href && c.href.startsWith('/')) return c.href;` — but the backend returns `href: "/v1/assets/<id>"` (an **API** path), which would route the SPA to a non-existent `/v1/assets/...` page. **Not currently reachable**: `looksLikeExplorerEntity` (`SearchModal.tsx:55-64`) gates `/v1/search` to tx-hash / ledger-seq / G-strkey / C-strkey shapes only — never a bare asset id — and a C-strkey classifies as `contract`, not `asset`. Latent only if the gate widens or the backend reclassifies. | None today; fragile coupling. | Frontend should build the asset href from `canonical` (`/assets/<canonical>`), not trust the backend's `/v1/...` href; or backend should return explorer-relative hrefs. |
| Info | `SearchModal.tsx:54` (`/^\d{1,12}$/` gate) vs `explorer_search.go:77` (`len>10` reject) | D7 | Frontend fires `/v1/search` for 1–12 digit inputs; backend `isLedgerSeq` rejects >10 digits (returns `unknown`). 11–12-digit numeric inputs round-trip to an `unknown` classification → harmless no-op (filtered by `explorerResult` returning null). | None; just a wasted request for 11–12-digit numbers. | Align frontend gate to `{1,10}` to match the uint32 ledger range. |

---

## CORRECT — verified good (do not regress)

**D1 — static-export architecture (the central design question):**
- The three unbounded-entity pages (`/ledger`, `/tx`, `/contract`)
  correctly use **query-param pages** that read `?seq`/`?hash`/`?id`
  via `useSearchParams()` inside a `<Suspense>` boundary
  (`ledger/page.tsx:23-26`, `tx/page.tsx:21-25`, `contract/page.tsx:20-25`),
  **not** `[seq]`/`[hash]`/`[id]` dynamic routes. This is exactly right
  under `output:'export'` (a dynamic route 404s on any param not in
  `generateStaticParams`). The rationale is documented in
  `explorer-shared.tsx:5-11` and each page header. `useSearchParams`
  under `Suspense` is required by Next 15 for static export — present
  in all three.
- `/ledgers` is correctly a **bounded list** page (not a query-param
  page) — the right call.
- React Query usage is sound: stable, param-keyed `queryKey`s; `enabled`
  gates on param presence (`seq != null`, `hash.length>0`,
  `looksValid`); `retry: false` on detail lookups so a 404 surfaces
  fast; `keepPreviousData`/`placeholderData` on the paged tables for
  fl&#8203;icker-free "Load older"; `staleTime` tuned per surface.
- **Envelope shape matches.** Frontend `Envelope<T> = {data, as_of,
  flags}` (`explorer-shared.tsx:20-24`) matches the backend
  `writeJSON`→`Envelope{Data, AsOf, Sources, Flags}`
  (`envelope.go:90-97`). All explorer hooks unwrap `env.data`.
- **Wire shapes match field-for-field** (verified against the Go view
  structs): `Ledger`/`LedgersPage`/`next_before`, `LedgerTransaction`/
  `LedgerTransactionsResp`, `TxSummary`/`TxOperation`/`TxEvent`,
  `ContractEvent`/`ContractResp`, `SearchResult`/`SearchKind` — names
  and optionality align. The **only** wire-type mismatch is
  `result_code` (string vs int — High above).
- **bigint precision (amounts as strings):** `fee_charged`/`max_fee`/
  `base_fee` are kept as numbers only where the backend caps them
  &#8203;≪ 2^53 (correct). `stroopsToXlm` accepts `string` and only the
  large `total_coins` case loses precision (Med above) — everything
  else is in-range. No amount is parsed to JS number and re-stringified
  in a way that round-trips through `trades`/price data.
- Loading / error / empty states are present and distinct on every new
  panel: ledgers table (`isError`/`isLoading`/`length===0`), ledger
  view (no-seq / not-found / loading / empty-tx), tx view (no-hash /
  invalid-hash-regex / 400 vs 404 via `errorStatus()` / loading / empty
  ops / empty events), contract view (no-id / invalid-id-regex / error /
  loading / empty events). The 400-vs-404 disambiguation in tx is a
  nice touch.
- Client-side input validation mirrors the backend regexes before
  fetching: `HASH_RE` (`tx`), `CONTRACT_RE` (`contract`), `^\d+$`
  (`ledger`) — avoids a pointless round-trip on obviously-bad input.

**D2 — brand + base URL:**
- `API_BASE_URL` defaults to `https://api.stellarindex.io`
  (`client.ts:7-8`); `useMe`'s inline fallback and `Navbar` sign-out
  fallback both use the same default. `next.config.mjs:48-49` injects
  the same. No `ratesengine.io` / `api.rates*` domain anywhere in src.
- User-visible branding is "Stellar Index" throughout (Navbar,
  layout metadata, status/docs links to `*.stellarindex.io`).

**D3 — security:**
- **No secrets/keys baked in.** Grep for `sk_live`/`AKIA`/`-----BEGIN`/
  `bearer`/`private_key`/`api_key=` returns only OpenAPI type
  definitions and doc placeholders (`rek_…`) — nothing live.
- **No XSS in the new pages.** All chain-controlled data (memos,
  topics, event types, op fields, raw_xdr, account/contract ids) is
  rendered through JSX text interpolation (`{...}`) or `title=`
  attributes — auto-escaped. `renderFieldValue` (`tx/TxView.tsx:314-328`)
  funnels arbitrary op-field values through `String()`/`JSON.stringify`
  into a text node. The only `dangerouslySetInnerHTML` in the whole src
  tree are (a) the static theme-init script and (b) JSON-LD built from
  `JSON.stringify` of **static constants** — none embed user/chain
  data. The new explorer pages add **zero** dangerous sinks.
- `target="_blank"` links in `contract/ContractView.tsx:147,157`
  (stellar.expert, API transfers) both carry `rel="noreferrer noopener"`.
  The transfers link `encodeURIComponent`s the contract id.
- No open-redirect surface: no `window.location`/`location.assign`
  driven by user input in any new page. `lib/safe-domain.ts` correctly
  guards attacker-controlled issuer `home_domain` (not used by the new
  pages, but verified intact).
- `CopyValue`/`CopyHash` clipboard writes are guarded with try/catch
  for insecure-context no-op.

**D7 — search wiring (additive):**
- `/v1/search` is fired **only** for explorer-entity shapes
  (`looksLikeExplorerEntity`, `SearchModal.tsx:55-64`) and the result is
  **prepended** to the existing asset/protocol/page index, de-duped on
  `href` (`:352-354`) — strictly additive, the legacy client-side
  search is untouched.
- Routing by kind is correct for the reachable kinds: `transaction`→
  `/tx?hash=`, `ledger`→`/ledger?seq=`, `contract`→`/contract?id=`
  (`explorerHref`, `:70-89`), each `encodeURIComponent`-ed. Kinds map to
  the right query-param pages.
- `enabled: open && looksLikeExplorerEntity(debouncedQ)` + 200ms debounce
  keeps the classification call cheap; `unknown` classifications return
  null and disappear cleanly.

---

## Summary

- **Critical: 0**
- **High: 2** — `result_code` string-vs-number type mismatch
  (mislabelled success/fail badges on tx + ledger pages); source-account
  links to `/issuers/{account}` 404 under static export for non-issuer
  accounts.
- **Med: 2** — `total_coins` precision loss through `Number()` in
  `stroopsToXlm` (ADR-0003 display-side); contract "Load older" can skip
  events within a saturated ledger (server cursor root cause, dup of
  A11 High — recorded as the UI symptom).
- **Low: 4** — frozen relative-age between refetches; `next_before`
  dead-end click at end of pagination; residual `re.`/`re-build-*`
  brand prefix; two "Rates-Engine" code comments.
- **Info: 2** — latent `asset`-kind href trusts backend `/v1/...` path
  (not currently reachable); frontend ledger-seq gate (12 digits) wider
  than backend (10).

The static-export architecture for the new explorer pages is **done
correctly** — query-param pages for unbounded entities, Suspense-wrapped
`useSearchParams`, sound React Query keys/states, matching envelope +
wire shapes, no XSS, no secrets, clean branding and base URL. The two
Highs are a wire-contract type bug and a routing/404 gap, both fixable
without architectural change.
