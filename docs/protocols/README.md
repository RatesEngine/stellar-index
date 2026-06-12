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
| Phoenix | RPC view (pre-lake) | ⏳ Enumerated (factory + multihop + 11 pools + 3 stake) — completeness needs team confirmation | [phoenix.md](phoenix.md) |
| DeFindex | tx create_contract | ⏳ Enumerated (4 factories, 34 vaults, 7 strategies) — open: `create` event lacks vault address | [defindex.md](defindex.md) |
| Aquarius | lake observation | ⏳ Enumerated (router + ~177 pools) — open: authoritative pool enumeration | [aquarius.md](aquarius.md) |

> **Why blend + soroswap are gated but the rest aren't (yet):** the
> factory-fan-out gate needs a way to enumerate the *complete* child set.
> That's clean only when the creation event **carries the child's
> address** — true for Blend (`deploy` → pool address) and Soroswap
> (`new_pair` → pair address), both lake-verified complete. For the rest
> the creation signal is absent or insufficient: Phoenix/Aquarius pools
> predate our lake (50.46M); DeFindex's `create` event carries the vault's
> *config* but not its address (0/34 vaults appear in the event bodies);
> Comet has no factory namespace at all. Those four need the protocol team
> to confirm the contract set (this page) or a view-function/WASM-hash
> enumeration — enforcing a gate on an unverified set would silently drop
> events, the one regression ADR-0035 forbids.
| Comet | WASM-hash | ⏳ Pending (shared Balancer-v1 WASM, no factory namespace) | — |

## External cross-checks — Dune dashboards

Dune has Stellar datasets and community/team dashboards that serve as an
independent cross-check for our contract enumerations and metrics
([discover: blockchain:Stellar](https://dune.com/discover/content/popular?q=blockchain%3A%27Stellar%27&timeframe=30d&resource-type=dashboards)).
Directly relevant:

| Dashboard | Author | Use for us |
|---|---|---|
| [Soroswap.Finance](https://dune.com/paltalabs/soroswap) | @paltalabs (the team) | pair set + volume cross-check |
| [DeFindex](https://dune.com/paltalabs/defindex) | @paltalabs (the team) | **vault enumeration** (our open Q) + TVL benchmark ($4.02M @ 2026-06-12) |
| [Blend 🧪](https://dune.com/fergmolina/blend) | @fergmolina | pool set cross-check vs our 27-pool/2-factory enumeration |
| [Aquarius ♒️](https://dune.com/fergmolina/aquarius) + [Aquarius Stellar](https://dune.com/claw) | community | **pool enumeration** (our open Q) |
| Soroban AMMs on Stellar | @paltalabs | cross-AMM pool lists (phoenix/comet) |
| Stellar Smart Contract Analysis | @stellar (SDF) | contract activity baseline |

The contract addresses live in each dashboard's query SQL — retrievable
via the Dune API (free tier key) or a logged-in query view, not from the
public page render. When cross-checking metrics, mind the
window-mismatch trap: Dune totals are often lifetime, ours are often
windowed.
