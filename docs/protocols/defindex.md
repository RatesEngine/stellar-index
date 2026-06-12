# DeFindex — contract & event verification

> **For the DeFindex team:** this is the set of DeFindex factories, vaults,
> and strategy contracts Rates Engine ingests. Please confirm the four
> factories and help us with the **open question below** about how vaults
> and strategies relate (so we attribute strategy events correctly).
>
> - **Enumeration method:** lake deploy-graph (`DeFindexFactory` /
>   `DeFindexVault` / `BlendStrategy` events; topics are namespaced, so
>   collision risk is low).
> - **Last verified:** 2026-06-12 (r1 lake).
> - **Gate status:** 🔎 enumerated; decoder gate pending.

## Factories (4)

`DeFindexFactory` `create` events announce new vaults. There is more than
one factory (like other protocols, DeFindex appears to have been
redeployed):

| Factory | events | first → last ledger |
|---|---:|---|
| `CDKFHFJIET3A73A2YN4KV7NSV32S6YGQMUFH3DNJXLBWL4SKEGVRNFKI` | 108 | 57,057,068 → 62,972,282 |
| `CDHPT7OBQKIUFHIJMLI4W7TNOQUHEVOOVMCW7HA4O5SPFNLDRCE6DQ5F` | 10 | 60,947,911 → 60,966,531 |
| `CAVP2QLPIG7FQNHI57KXF7KS6NIAAUQKHZZDM3AGVADE64WHFBC5YURX` | 3 | 55,484,403 → 55,511,450 |
| `CDOIC7245ONYVOTEDLGKUM263EQ7SEEQ74ZQCN4SSH4TSYXOCMU6254O` | 2 | 56,891,213 → 56,927,232 |

## Vaults & strategies (lake counts)

- **34 vault contracts** emit `DeFindexVault` events (deposit / withdraw /
  rebalance / fee / manager changes), 59.37M → 62.99M.
- **7 strategy contracts** emit `BlendStrategy` events (deposit / withdraw
  / harvest), 62.85M → 62.99M (recent).

The full vault + strategy address lists are derivable from the lake; we'll
attach them once the open question is settled. A hand-seeded vault list
already exists (`migrations/0033_seed_defindex_vaults`).

## ⚠️ Open question (please advise)

We verified the factory `create` events against the lake and found a
gating obstacle: **the `create` event does not carry the new vault's own
address.** The 4 factories emit 107 `create` events whose bodies hold the
vault's *configuration* (assets, strategy addresses, manager/role
addresses) — but **0 of the 34 vault-emitting contracts appear anywhere in
those bodies.** So unlike Blend (`deploy` → pool address) or Soroswap
(`new_pair` → pair address), we can't enumerate DeFindex vaults from the
creation event; the vault's address is the deterministically-deployed
contract, recorded in the transaction's `create_contract` op, not the
event.

To gate correctly we need one of:

1. A **factory view function** that lists deployed vault addresses (a
   `query_vaults()` / registry), OR
2. Confirmation that the vault address is recoverable from the `create`
   event another way (e.g. a salt/deployer derivation), OR
3. The **authoritative vault + strategy address list** directly.

And separately: are the **7 `BlendStrategy`** contracts **created by their
vaults** (fan-out), or **shared / independently deployed** (need their own
allowlist)?

> **Note:** DeFindex topics are namespaced (`DeFindexVault`,
> `BlendStrategy`), so collision risk is low and the urgency is lower than
> for Blend/Soroswap (whose generic `supply`/`swap` topics collide widely).

## Events decoded

| Layer (topic[0]) | topic[1] examples | Where it lands |
|---|---|---|
| `DeFindexFactory` | `create`, `n_fee` | registers the vault |
| `DeFindexVault` | `deposit`, `withdraw`, `rebalance`, … | `defindex_flows` (vault layer) |
| `BlendStrategy` | `deposit`, `withdraw`, `harvest` | `defindex_flows` (strategy layer) |
