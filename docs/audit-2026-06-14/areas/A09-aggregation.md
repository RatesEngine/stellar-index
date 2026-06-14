# A09 — Aggregation / divergence / currency / metadata

Read-only audit, 2026-06-14. Scope: `internal/aggregate/` (+ subpackages
anomaly, baseline, changesummary, confidence, freeze, orchestrator),
`internal/divergence/`, `internal/currency/` (+ marketcap), `internal/metadata/`.
ALL `.go` including tests.

Dimensions exercised: **D1** correctness (VWAP/TWAP/outlier/triangulation
math; stablecoin→fiat proxy; min_usd_volume; divergence cross-checks);
**D2** ADR invariants (stablecoin proxy is aggregator-layer not decoder;
ADR-0014 allow-lists; i128/decimal precision via big.Rat not float);
**D3** security (currency seed.yaml trust surface; divergence/metadata
external HTTP — SSRF + key handling); **D5** resource/lifecycle (divergence
refresh worker; chainlink-HTTP divergence; backoff/timeouts).

## Files read

77 Go files total (43 non-test + 34 test) across the four packages and their
sub-packages. Every file in scope was read in full. Key supporting files read
outside the area for cross-checks: `internal/canonical/asset_crypto.go`
(`knownCryptoCodes` allow-list), `internal/currency/data/seed.yaml` (header +
spot entries).

## Severity counts

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 0 |
| Medium   | 1 |
| Low      | 5 |
| Info     | 9 |

**No Critical or High findings.** This is a mature, heavily-audited surface —
the prior codex-audit register (F-1213, F-1230, F-1242, F-1249, F-1260,
F-1306, F-1308, F-1336, F-1337, F-1340, F-1344, F-1345, G13/G14/G16) is
already fixed and verified inline. The value-serving math (VWAP / OHLC /
triangulation / stablecoin combine) is all exact-precision `big.Rat`/`big.Int`;
float64 is confined to heuristic/display layers (outlier σ, confidence factors,
baseline z-score, MinUSDVolume gate, divergence %) — each documented as such.
The SSRF guard on the SEP-1 fetcher is best-in-class. The single Medium is a
dead-but-tested production function whose stale doc-comment asserts a
caller-contract no caller honors.

## Findings

| Severity | File:line | Dim | Issue | Why it matters | Fix | Confidence |
|----------|-----------|-----|-------|----------------|-----|------------|
| Medium | orchestrator.go:1242 (`windowUSDVolume`) | D1 (dead code / doc-drift) | `windowUSDVolume` has **zero production callers** — the only references are `orchestrator_test.go:1125,1131`. The MinUSDVolume gate moved to `survivorUSDVolume` + `usdVolumeForPairPerTrade` (F-1260/F-1213), and `dropForMinUSDVolume` now takes a pre-computed survivor total. The function's doc still carries a "CALLER CONTRACT: only invoke when minUSDVolumeApplies returned true" warning describing a contract no caller honors. | Latent foot-gun: a future dev wiring the min-volume gate could pick this seemingly-purpose-built helper (it sums quote at a hard-coded 1e8 scale and ignores classic 7-decimal pegs) and re-introduce the exact 10× under/over-statement F-1213 fixed. The test keeps it compiling, masking that it's orphaned. Same pattern (intentionally-retained dead seam) exists for `usdVolumeForPair` at line 1098 — but that one is honestly labelled a doc-pointer and pinned via `var _ =`. | Delete `windowUSDVolume` + its two test cases, OR demote it to a `var _ = windowUSDVolume` doc-pointer like `usdVolumeForPair` and strip the misleading CALLER CONTRACT block. | high |
| Low | orchestrator.go:1297-1322 (`formatRatFixed`) | D1 | Fixed 12-dp **truncate-toward-zero** formatter. Verified behaviour: 1/3→`0.333333333333`, 2/3→`0.666666666666` (NOT banker's-rounded `…667`), and any value `< 10^-12` (e.g. 1e-15) renders `0.000000000000`. | The truncation is *intentional and correct* per the doc (the API spec mandates truncate-toward-zero, not Go's default round-half-even). The only edge is the sub-pico value → literal `0.000…`, which would mis-serve a genuinely tiny price as zero. 12dp is generous for any real crypto/fiat price, so unreachable in practice; flagged only for completeness. | Accept (documented). If a sub-1e-12 asset ever lists, bump `decimals` for that pair. | high |
| Low | global.go:362 (`averageAggregatorPrices`) | D1 (precision) | The aggregator-tier mean is `avgScaled := sum / contributed` via `big.Int.Quo` (integer division, truncates toward zero) at the common 14-dp scale before rendering. | A sub-1e-14 rounding on a multi-source aggregator-tier *fallback* price (only fires when VWAP tier missed AND aggregator rows exist AND fresh). Negligible — 14dp is below any served price granularity, and it's a tier-2 fallback, not the primary VWAP. Mentioned because it's the one integer-truncation in the global-price math. | None needed; could carry the remainder if perfect tie-rounding ever matters (it won't at 14dp). | high |
| Low | chainlink.go:84,145 + coingecko.go:78,123 + marketcap/refresher.go:51 | D3 (SSRF, operator-scoped) | The divergence `ChainlinkReference.rpcURL`, `CoinGeckoReference.baseURL`, and the marketcap `Refresher.Endpoint` are operator-configured outbound URLs with **no SSRF dialer guard** (unlike `metadata.Resolver`, which has a full private-IP/rebind block). They default to public endpoints (cloudflare-eth.com / api.coingecko.com). | These are operator-config, not end-user input, so the SSRF blast radius is "an operator points their own deployment at an internal host" — low real risk. But the asymmetry with the metadata fetcher (which assumes hostile input because home-domains come from on-chain issuer data) is worth a note: if a future feature ever lets a *user* influence a divergence/marketcap endpoint, these become live SSRF. | Document the trust assumption (operator-only). If endpoints ever become user/issuer-derived, reuse `metadata.ssrfDialer`. | med |
| Low | divergence/worker.go:339 + flushObservations | D1 | `deltaPct = (ourPrice - refPrice) / refPrice * 100` and the `firing := absFloat(deltaPct) > s.threshold` use the **per-reference price**, while the Redis `WarningFired` uses divergence vs the **median**. The durable `divergence_observations` "firing" column can therefore disagree with the cached `WarningFired` for the same tick (one ref far off, median fine). | Not a bug — the two columns answer different questions (per-ref delta history vs aggregate verdict) and the docstrings say so. Flagged because an operator reading `divergence_observations.firing` could mistake it for the API-surfaced flag. | Add a one-line column comment in the sink/migration clarifying per-reference-vs-aggregate semantics. | med |
| Low | divergence/worker.go:395-460 (`LookupCached`) | D5 | The by-asset divergence lookup does `SMembers(idx)` then one `Get` per quote member — an N+1 Redis round-trip on the `/v1/price` hot path (N = distinct quotes the base trades against). | Bounded by the operator's configured pair set (small — single-digit quotes per base in practice), and each Get is O(1). Not a scan. Would matter only if a base ever accreted dozens of quote members. The per-pair-key design (F-1344) is correct; this is just the read shape it implies. | Accept; if quote cardinality grows, pipeline the Gets (`MGET`/pipeline). | high |
| Info | vwap.go / twap.go / ohlc.go / triangulate.go (whole) | D1/D2 | All value-serving aggregation math verified exact: VWAP = Σquote/Σbase via `big.Int`+`big.Rat`; TWAP weights by integer nanoseconds (cancels in division) with non-positive-Δt skip; OHLC open/close ordering-correct, high≥open/close & low≤open/close, zero-base/zero-quote skipped; Triangulate/TriangulateChain reject nil/≤0 and return fresh copies. No `int64(parts.Lo)` truncation anywhere (ADR-0003 honored). All four refuse to sort internally (won't hide caller ordering bugs). | — | None — verified correct. | high |
| Info | stablecoin.go (whole) + canonical/asset_crypto.go | D2 | The stablecoin→fiat proxy is **aggregator-layer, applied as a quote-side pair rewrite at VWAP compute time**, never at decode (CLAUDE.md / ADR-0014/0026 honored — keeps depeg signal in raw feed). All 9 proxy codes (USDT/USDC/DAI/PYUSD/USDP/EURC/EUROC/EUROB/MXNe) confirmed present in `knownCryptoCodes`, so `ExpandTargetPairWithClassicPegs`'s `NewCryptoAsset(ticker)` never silent-`continue`s a backer; test-guarded by `TestFiatProxy_{USD,EUR,MXNe}Pegged`. Only the QUOTE is rewritten (base-rewrite explicitly rejected as wrong-axis). `FiatProxy` degrades to ok=false (not panic) if the fiat allow-list ever regresses. `ExpandTargetPairWithClassicPegs` defensively skips malformed/non-classic pegs without aborting the tick. | — | None — verified correct, and the depeg safety-net pairing (concealed-depeg price → divergence worker fires) is regression-tested in `depeg_test.go`. | high |
| Info | orchestrator.go:1204-1262 (MinUSDVolume gate) | D1/D2 | The min-USD-volume publish gate is correctly post-class + post-outlier (F-1260: evaluates the **survivor** USD sum via per-trade-ID map, not the pre-filter total), and correctly USD-quote-only (`minUSDVolumeApplies` — because only fiat:USD windows are uniformly off-chain 1e8-scaled; classic 7-dp pegs handled separately at decimals=7). float64 used for the threshold compare only, documented as non-precision-sensitive. | — | None — verified; the F-1213/F-1242/F-1260 chain is internally consistent. | high |
| Info | confidence/{factors,score}.go + baseline/baseline.go | D1 | Confidence factors all clamp to [0,1], handle NaN/Inf/negative defensively, and combine via a log-space weighted geometric mean (numerically stable; all-zero-weight → neutral 0.5; any zero factor → 0 dominates, as ADR-0019 wants). Bootstrap cap (≤0.5 under 30 days) applied. Baseline z-score = |x−median|/MAD with the MAD==0 split (delta==0→0, else +Inf) correct; Median/MAD on float64 (documented heuristic-layer choice, parallel to outliers.go). | — | None — verified correct. | high |
| Info | anomaly/threshold.go (`Evaluate`) | D1 | Phase-1 anomaly decision table correct: dev<warn→Allow; warn≤dev<freeze→Warn; dev≥freeze & sources>1→Warn (real move); dev≥freeze & sources≤1→Freeze (manipulation signature). nil PrevVWAP→Allow; nil CurrVWAP→Freeze (fail-safe on caller bug). `computeDeviationPct` guards prev==0 (returns 1e9 sentinel). big.Rat throughout the deviation calc. | — | None — verified. | high |
| Info | orchestrator/phase2_freeze.go | D1 | The 3-signal AND freeze fires iff `Confidence < ConfidenceMaxFreeze && ZScore > ZScoreMinFreeze && SourceCount <= SourceCountMaxFreeze` — operators (`<`/`>`/`<=`) match the documented thresholds; defaults fall back to package constants when zero. AND (not OR) is correct per ADR-0019 (all three must agree to suppress a publish). | — | None — verified. | high |
| Info | metadata/sep1.go (SSRF guard) | D3 | Best-in-class SSRF defence on the issuer-controlled stellar.toml fetch: `Proxy:nil` (F-1336 — prevents proxy-bypass exfil), custom `ssrfDialer` that re-resolves + blocks per-IP AFTER DNS (closes rebind races), blocks loopback/link-local (169.254.169.254)/multicast/RFC-1918/RFC-4193 + the cloud-metadata extras (100.64/10 Alibaba, 192.0.0/24 Oracle, 198.18/15), CheckRedirect rejecting >5 hops / scheme-downgrade / cross-host, 1 MiB body cap via LimitReader (+ ContentLength pre-check), strict hostname validator rejecting URLs/queries/fragments. `AllowPrivateIPs` is test-only. Verified test coverage (`ssrf_internal_test.go`, `domain_validator_test.go`) exercises every block range incl. IPv6 ULA + the proxy-bypass case. | — | None — exemplary. | high |
| Info | currency/verified.go + data/seed.yaml | D3 | The verified-currency catalogue is the hand-vetted trust surface as designed: `//go:embed data/seed.yaml` (compile-time, code-change-to-amend), header explicitly states "part of the trust surface … requires a code change + redeploy". Loader is read-only after construction, validates required fields, fails loudly on unknown class / duplicate slug-ticker-assetID / Stellar code collision. **NOT** auto-populated from CG/CMC — the marketcap refresher only *enriches price/mktcap into a separate Cache*, never mutates the catalogue. CG/CMC IDs in seed are operator-curated hints, not a population source. | — | None — verified the no-auto-populate invariant holds. | high |
| Info | marketcap/refresher.go:235-247 + chainlink/coingecko key handling | D3 | CoinGecko key passed via **request header** (`x-cg-pro-api-key` / `x-cg-demo-api-key`), NOT URL query (F-1337 — avoids secret leaking through `*url.Error` strings + access logs). Pro>Demo precedence. 429/403 → Retry-After-aware exponential backoff clamped [5m,30m]. Body capped at 5 MiB LimitReader. Error bodies truncated to 200 chars before logging. Chainlink/CoinGecko refs use bounded `io.LimitReader` + per-call timeouts + panic-recover per reference in `Compare`. No secrets in code. | — | None — verified. | high |
| Info | divergence/compare.go + worker.go (concurrency + lifecycle) | D4/D5 | `Compare` fans out one goroutine per reference, each with its own `context.WithTimeout` + a panic-recover that records the failure (a misbehaving operator-supplied reference can't crash the run); buffered channel sized to len(refs) so the recover's send never blocks; `wg.Wait()` then `close`. `Service` edge-triggered warning hook is mutex-guarded (`warningMu`). CoinGecko batch cache coalesces concurrent fetches under `batchMu`. Divergence refresh is best-effort per-pair with per-outcome metrics, gated by `DivergenceMinInterval` (F-0030 — CMC quota protection). | — | None — verified. | high |
| Info | metadata/cache.go (singleflight) + lcm_resolver.go | D1/D4/D5 | SEP-1 cache uses `DoChan` (per-caller ctx cancellation honored) with a **detached** `context.Background()`+10s fetch ctx so one caller's cancel doesn't truncate an in-flight fetch benefiting other waiters; re-checks cache inside the singleflight slot; negative results not cached (404 is a real transient signal). LCM home-domain resolver caps the "latest" sentinel at `math.MaxInt32` (the prior `MaxUint32` overflowed postgres int4 — fix verified) and chains to the static map on storage error with a 100ms timeout. | — | None — verified, incl. the int4-overflow fix. | high |

## CORRECT-verified list (no issues found)

Read in full and verified against D1/D2/D3/D5:

- **vwap.go** — `VWAP` (Σquote/Σbase, big.Int, skips non-positive base/quote,
  ErrNoTrades on zero sum), `TotalBaseVolume`/`TotalQuoteVolume`,
  `SourceContributions` (per-source quote-share weights; float only for the
  display weight, volumes stay big.Int).
- **twap.go** — integer-nanosecond time-weighting; final-slot windowEnd clamp;
  non-positive-Δt skip; ErrNoTrades on zero total duration.
- **outliers.go** — σ-filter on the float64 price projection (documented
  heuristic); sigma≤0 / n<3 / stdev==0 no-ops; sample stdev (Bessel) matches
  RFP; preserves surviving order.
- **ohlc.go** — `ComputeOHLC` open/high/low/close exact via big.Rat; ordering
  invariants; zero-base/quote skip; ErrNoTrades when nothing contributes.
- **triangulate.go** — `Triangulate` / `TriangulateChain` (nil/≤0 reject,
  fresh-copy result, identity for single price).
- **stablecoin.go** — full proxy surface (`FiatProxy`, `ProxyPair`,
  `ProxyTrade`, `FiatBackers`, `ExpandTargetPair[WithClassicPegs]`); quote-only
  rewrite; defensive non-abort skips; classic-USD-peg operator allow-list.
- **global.go** — `ComputeGlobalPrice` three-tier fallback (VWAP→aggregator-avg
  →triangulated); XLM dual-form alias loop (F-1340, in lock-step with v1's
  copy); error-vs-not-found tier semantics; `filterFreshAggregatorRows`
  defensive recopy; `averageAggregatorPrices` big.Int mean at 14dp.
- **orchestrator.go** — full Tick lifecycle: fetch→class-filter→outlier-filter
  →min-USD-gate→VWAP→Phase1 anomaly→Phase2 confidence-freeze→cache write
  →contribution sink→stream publish; serialized-tick single-runner invariant
  (prevVWAPs/lastWriteAt/lastDivergenceRefreshAt need no lock; Stats uses mu);
  `emitStalenessGauges` XLM dual-label MIN (F-1306/F-1308); freeze-LKG-TTL
  keepalive (F-1345); `formatRatFixed` truncate-toward-zero.
- **orchestrator/triangulate.go** — chain validation (≥2 legs, endpoint match,
  pivot continuity); X2.5 FX-snap rule (both-fiat legs → FXStore at bucketEnd,
  soft-fallback to cached VWAP on ErrNoFXQuote); provenance marker write;
  per-chain failures counted not fatal.
- **orchestrator/{confidence,phase2_freeze,divergence_refresh}.go** — baseline
  →confidence wiring (nil-Baselines + first-tick guards), 3-signal AND, the
  div-refresh outcome ladder + min-interval gate.
- **anomaly/{class,decision,threshold}.go** — class taxonomy, Decision shape,
  per-class threshold table + Evaluate decision matrix.
- **baseline/{baseline,multi,refresh}.go** — robust Median/MAD/ZScore;
  multi-window MaxZScore with all-nil-window handling; refresh worker.
- **confidence/{factors,score}.go** — six bounded factors + log-space weighted
  geometric mean + bootstrap cap.
- **freeze/{freeze,recovery}.go** — Redis freeze-marker writer + 60s recovery
  sweep.
- **changesummary/rollup.go** — multi-window delta strip; values carried as
  decimal strings, float only at display-grade % delta (documented);
  divide-by-zero guard on `deltaPct`.
- **divergence/{reference,compare,coingecko,chainlink,worker}.go** — Reference
  iface + sentinels; parallel Compare with per-ref timeout+panic-recover+median;
  CoinGecko batched fetch (header-key, 256KiB cap, default ID/quote maps);
  Chainlink eth_call int256 two's-complement decode + decimals scale + invert;
  per-pair Redis key layout (F-1344) + edge-triggered hook (F-1249) +
  observation sink.
- **currency/verified.go + marketcap/{cache,refresher}.go** — embedded
  catalogue loader/indexes + RWMutex-guarded price/mktcap enrichment cache +
  CG batch refresher with header-key + Retry-After backoff.
- **metadata/{sep1,cache,lcm_resolver,lcm_resolver_helpers}.go** — SSRF-guarded
  SEP-1 fetch/parse + singleflight Redis cache + LCM home-domain resolver
  (int4-safe sentinel) + chained-static fallback.
- All `doc.go` package docs and the test suites (`*_test.go`) — verified the
  known-trap regression tests exist (stablecoin depeg ↔ divergence pairing in
  `depeg_test.go`; SSRF range/rebind/proxy-bypass in `ssrf_internal_test.go`;
  hostname-validator negatives in `domain_validator_test.go`; FiatProxy
  allow-list assertions in `stablecoin_test.go`); no test rot observed.
