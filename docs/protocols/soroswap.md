# Soroswap — contract & event verification

> **For the Soroswap team:** this is the set of Soroswap factories and the
> method we use to enumerate pairs. Please confirm the four factories are
> correct and complete (any factory **not** listed here means we'd miss its
> pairs).
>
> - **Enumeration method:** lake deploy-graph (every
>   `SoroswapFactory:new_pair` event) + the factory's `all_pairs()` view for
>   the live registry, which also carries each pair's token_0 / token_1.
> - **Last verified:** 2026-06-12 (r1 lake).
> - **Gate status:** ✅ enforced (`internal/sources/soroswap`, ADR-0035).

## Factories (trust roots)

A `new_pair` event registers a pair only when emitted by one of these. The
primary factory carries the live pair registry; the three early factories
are launch-era deployments.

| Factory | `new_pair` count | Notes |
|---|---:|---|
| `CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2` | 194 (`SoroswapFactory` events) | Primary — all pairs with swap activity. |
| `CCIQM2O3YJQEKS7I77AS5IO3CU6UCBAUWHLWRBWVV336ZCSTKRNBKPHW` | 11 pairs | Early/launch-era. **The 21 pairs from these three have zero swaps (defunct).** |
| `CDBRTEJMOUJQHFZCAW4JPXZ75HCRHZAQXG75ZMGQ2LMNXA5ID7RQIFSX` | 6 pairs | Early/launch-era. |
| `CCDATRT2EY6Y2KAZ7HM7BRZVZCB6RHL56PQUBWGBS2ML2JAK7VXFLCJY` | 4 pairs | Early/launch-era. |

Also tracked: **Router** `CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH`
(orchestration only — its swap calls delegate to the pair contracts, which
emit the events; observed via the router's InvokeContract op for the census).

## Pairs

Pairs are **not** hard-coded — they're enumerated from the factories'
`new_pair` events (which carry `pair`, `token_0`, `token_1`) and the
primary factory's `all_pairs()` view, kept in the `soroswap_pairs`
registry. A pair's `swap`/`sync`/`skim` events are attributed to Soroswap
only when the pair is in that registry. The three early factories' 21 pairs
are registered for completeness but have no swap activity.

## Events decoded

Verified against `soroswap-core` `pair/src/event.rs` /
`factory/src/event.rs`.

| Event (topic) | Where it lands |
|---|---|
| `("SoroswapFactory","new_pair")` | registers the pair (token_0/token_1) |
| `("SoroswapPair","swap")` + `("SoroswapPair","sync")` | `trades` (source=soroswap) |
| `("SoroswapPair","skim")` | `soroswap_skim_events` |
| `("SoroswapPair","deposit"/"withdraw")` | matched (registered pair) — no trade output |
