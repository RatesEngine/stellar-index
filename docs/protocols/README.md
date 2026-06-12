# Protocol verification pages

One page per on-chain protocol we index, listing **every contract we
attribute to that protocol** — factories, pools/vaults, and the events we
decode from each. These pages exist to be **sent to each protocol team for
verification**: "this is the complete set of your contracts and events we
ingest; please confirm it's correct and complete."

## Why this matters (ADR-0035)

Soroban topic symbols are not unique across protocols (`swap`, `supply`,
`deploy`, `create`, `claim` are all emitted by multiple protocols and by
SACs). We therefore gate every decoder on **contract identity** — a
contract's events are attributed to a protocol only if the contract is one
of that protocol's factories or a contract a factory created (fan out).
The correctness of that gate depends on having the **complete** factory +
contract set. Discovery docs proved incomplete (e.g. Blend has two pool
factories, only one was documented), so each set is verified **empirically
against the certified lake** and then confirmed by the protocol team via
these pages.

## How the sets were enumerated

The enumeration method differs per protocol because creation events aren't
always in our lake (which starts at ledger 50,457,424):

- **Lake deploy-graph** — decode every creation event (`deploy` /
  `new_pair`) in `contract_events`, build factory → children. Used where
  the factory's creation events fall inside the lake (Blend, Soroswap).
- **RPC view enumeration** — the factory's `query_pools()` / `all_pairs()`
  view returns the current child set. Used where pools predate the lake
  (Phoenix). Snapshot-in-time; re-run to refresh.
- **WASM-hash walk** — contracts sharing the protocol's pool/vault WASM
  hash (the `wasm-history` audit). The fallback discriminator.

Each page states which method produced its set and the `last_verified`
date, so a team can tell us if a contract is missing or mis-attributed.

## Status legend

- ✅ **Gated** — the decoder enforces this set (events from contracts
  outside it are not attributed to this protocol).
- 🔎 **Enumerated, pending gate** — set verified from the lake; decoder
  gate not yet shipped.
- ⏳ **Pending verification** — set not yet enumerated.

| Protocol | Method | Status | Page |
|---|---|---|---|
| Blend | lake deploy-graph | ✅ Gated (2 factories, 27 pools) | [blend.md](blend.md) |
| Soroswap | lake deploy-graph | ✅ Gated (4 factories) | [soroswap.md](soroswap.md) |
| Phoenix | RPC view (pre-lake) | 🔎 Enumerated (13 contracts) | [phoenix.md](phoenix.md) |
| Aquarius | TBD | ⏳ | — |
| DeFindex | TBD | ⏳ | — |
| Comet | WASM-hash | ⏳ | — |
