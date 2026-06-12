# Phoenix — contract & event verification

> **For the Phoenix team:** this is the complete set of Phoenix contracts
> and events Rates Engine ingests. Please confirm the factory, multihop,
> and pool list are correct and complete — **especially any pool not
> listed here**, since we can't enumerate them from on-chain events (see
> below) and rely on this list being complete.
>
> - **Enumeration method:** RPC view — the factory's `query_pools()` plus
>   the multihop contract (Phoenix pools were created before our lake's
>   earliest ledger, 50.46M, so there are **no `create` events in the lake**
>   to enumerate from).
> - **Last verified:** 2026-06-12 (r1 lake event activity; pool list from
>   the 2026-05-01 WASM-history walk).
> - **Gate status:** 🔎 enumerated; decoder gate pending (the curated pool
>   list below is the intended gated set).

## Factory & multihop

| Role | Contract | Lake events | Notes |
|---|---|---|---|
| Factory | `CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI` | none | Emits `("create","liquidity_pool")`, but only before our lake. Pools enumerated via its `query_pools()` view. |
| Multihop | `CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G` | `initialize` ×1 | **Emits no `swap` events** — it relays to pools, so a pool-only gate loses no trades. |

## Pools (11)

Pools that have emitted `swap` in the lake are marked **active** with their
swap count; the rest are in the factory's `query_pools()` but have no swap
activity in our window.

| Pool | Lake activity |
|---|---|
| `CBHCRSVX3ZZ7EGTSYMKPEFGZNWRVCSESQR3UABET4MIW52N4EVU6BIZX` | **active** — 43,918 swap (+ provide/withdraw_liquidity) |
| `CBCZGGNOEUZG4CAAE7TGTQQHETZMKUT4OIPFHHPKEUX46U4KXBBZ3GLH` | **active** — 4,233 swap (+ provide/withdraw_liquidity) |
| `CD5XNKK3B6BEF2N7ULNHHGAMOKZ7P6456BFNIHRF4WNTEDKBRWAE7IAA` | **active** — 2,872 swap |
| `CBISULYO5ZGS32WTNCBMEFCNKNSLFXCQ4Z3XHVDP4X4FLPSEALGSY3PS` | **active** — 1,736 swap |
| `CDMXKSLG5GITGFYERUW2MRYOBUQCMRT2QE5Y4PU3QZ53EBFWUXAXUTBC` | **active** — 48 swap |
| `CB5QUVK5GS3IU23TMFZQ3P5J24YBBZP5PHUQAEJ2SP5K55PFTJRUQG2L` | **active** — 25 swap |
| `CC6MJZN3HFOJKXN42ANTSCLRFOMHLFXHWPNAX64DQNUEBDMUYMPHASAV` | **active** — 8 swap |
| `CBW5G5SO5SDYUGQVU7RMZ2KJ34POM3AMODOBIV2RQYG4KJDUUBVC3P2T` | no lake events |
| `CCKOC2LJTPDBKDHTL3M5UO7HFZ2WFIHSOKCELMKQP3TLCIVUBKOQL4HB` | no lake events |
| `CCUCE5H5CKW3S7JBESGCES6ZGDMWLNRY3HOFET3OH33MXZWKXNJTKSM3` | no lake events |
| `CDQLKNH3725BUP4HPKQKMM7OO62FDVXVTO7RCYPID527MZHJG2F3QBJW` | no lake events |

**Note on completeness:** the `swap` topic is emitted by 49 distinct
contracts in our lake (most are other AMMs), and `withdraw_liquidity` by
75 — so we can't reverse-derive the Phoenix pool set from event topics
alone. This list is the factory's `query_pools()` snapshot; **if Phoenix
has deployed pools since 2026-05-01, please send the additions.**

## Events decoded

Verified against `phoenix-contracts` `pool/src/contract.rs`. Each Phoenix
action emits **multiple field-named events** (e.g. a swap emits 8) that we
correlate by `(ledger, tx_hash, op_index)` into one trade.

| Action (topic[0]) | Where it lands |
|---|---|
| `swap` | `trades` (source=phoenix) |
| `provide_liquidity`, `withdraw_liquidity` | `phoenix_liquidity` |
| `bond`, `unbond` | `phoenix_stake_events` |
