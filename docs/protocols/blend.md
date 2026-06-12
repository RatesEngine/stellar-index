# Blend — contract & event verification

> **For the Blend team:** this is the complete set of Blend contracts and
> events Rates Engine ingests and attributes to Blend. Please confirm the
> two pool factories and the pool list are correct and complete (especially
> any factory or pool **not** listed here).
>
> - **Enumeration method:** lake deploy-graph (decoded every `deploy`
>   event in our certified lake, ledgers 50.46M–62.99M).
> - **Last verified:** 2026-06-12 (r1 lake).
> - **Gate status:** ✅ enforced (`internal/sources/blend`, ADR-0035).

## Pool factories (trust roots)

A contract's events are attributed to Blend only if the contract is one of
these factories or a pool one of them deployed. Both emit only `deploy`
(pure factory signature), body = the new pool's `Address`.

| Factory | `deploy` count | first → last ledger | Notes |
|---|---:|---|---|
| `CCZD6ESMOGMPWH2KRO4O7RGTAPGTUPFWFQBELQSS7ZUK63V3TZWETGAG` | 17 | 51,499,915 → 55,857,910 | **Earlier factory — not in our discovery docs; found via lake verification.** |
| `CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU` | 10 | 56,615,475 → 62,967,177 | Pool Factory V2 (documented). |

Also tracked: **Backstop V2** `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7`.

## Pools (27 — every contract the factories deployed)

The 9 pools that have emitted liquidation auctions (`new_auction` /
`fill_auction`) are marked **active**; the rest were deployed but have no
auction activity in the lake (they may still have supply/borrow flow).

### Deployed by `CCZD6ESM…` (17)

```
CADP6E57HEJOAWHBSTEDJYFJSRU5C5D7YBHFEET23CAHD2KGD4XKCFMS
CAQF5KNOFIGRI24NQRRGUPD46Q45MGMXZMRTQFXS25Y4NZVNPT34GM6S   active
CB22FIF722FWWHKDX6URY2LHTOS6TWLPXL2IOGY5QS6YNQXTRBDCNPD3
CB7V7T52OLKMBC5QPL7GH2OKR4XV6YWDURUXSAAAFCPSNX7EPBYF5DJE
CBP7NO6F7FRDHSOFQBT2L2UWYIZ2PU76JKVRYAQTG3KZSQLYAOKIF2WB   active
CBQPFUWOMGTGC5X65J52Z2OHFWYWFCA3TMYCVY6G2T2SB326WW45HF2G
CBVOPI6QC6OWVCOEZDCFELAGQNAOHUS4CWOKAVADKQZXVSWR2R5IAKO7
CCTZXMW3DJIKDI3UVDUJR6PM4WFFEB5RIWDXJBGIEFBD5XFHI26LZ5BU
CDAKUFO3WOUG2DLY6XTNRKBSK53VJTJXMTOUEMPKOWN4R756OFICXWID
CDE65QK2ROZ32V2LVLBOKYPX47TYMYO37Z6ASQTBRTBNK53C7C6QF4Y7   active
CDIUMS2ZNGNGHDRBKFXS4QU23ATPYCTDBUHGZ6FS2MPAEY37FAC4JD3R
CDJ6Q3A2NUK3ANWFGXCHUBPQJXKAXBHNUVILGTEOTSEH2NDZC4FI632B
CDJD2PFCHD2R4SHP3WJ4C6JEF445ODSO74WOCKNFS25I4XI7HMLK3VYO
CDK4KXOYG332TO7VDARUJ66RMQTEADFSZY3RDJZQBS7ZFCD25RV52NXP
CDL3EQ4P3DQH5Q6BT3AINZCCJKUHSXPJAOF7YP3JE7MFJX7FGXHPT27B
CDU4RTOYFZERUD727WW6VRXH5IK35GLCXCPK5ILUYRLLYYMTYSCJXUEA
CDVQVKOY2YSXS2IC7KN6MNASSHPAO7UN2UR2ON4OI2SKMFJNVAMDX6DP   active
```

### Deployed by `CDSYOAVX…` (10)

```
CADR6Q2UOCDJAGXMAB2E6SRT35STLZ2IGLZUCXJQG7TC2LNKCU5RTQVY
CAE7QVOMBLZ53CDRGK3UNRRHG5EZ5NQA7HHTFASEMYBWHG6MDFZTYHXC   active
CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD   active
CALRF5I2OCJCU577R6MZBCY5IIXNMAAG6PNMN7GUKEYIXBJCJN2FJRVI
CB4OFHAY2TAEYUVPOJS36S657C6NYMSIFUNCCA5AHYT46Y5XUID3O2ED
CBNR7PYFY775UG7W37B4OJG2OBBUKLFW6VIBHFDKKLR2HECPRMRZMDK3
CBYOBT7ZCCLQCBUYYIABZLSEGDPEUWXCUXQTZYOG3YBDR7U357D5ZIRF   active
CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS   active
CDMAVJPFXPADND3YRL4BSM3AKZWCTFMX27GLLXCML3PD62HEQS5FPVAI   active
CDZVHCO7LDUJZSME3PJPJXAKT7F6W5IXSOXTJ2QEK3Y2X2CDUREBUMUY
```

## Events decoded (from pool contracts)

Verified against `blend-contracts-v2` `pool/src/events.rs` /
`pool-factory/src/events.rs`.

| Event topic[0] | Where it lands |
|---|---|
| `deploy` (factory) | registers the new pool (pool registry) |
| `new_auction`, `fill_auction`, `delete_auction` | `blend_auctions` |
| `supply`, `withdraw`, `supply_collateral`, `withdraw_collateral`, `borrow`, `repay`, `flash_loan` | `blend_positions` |
| `gulp`, `claim`, `reserve_emission_update`, `gulp_emissions`, `bad_debt`, `defaulted_debt` | `blend_emissions` |
| `set_admin`, `update_pool`, `queue_set_reserve`, `cancel_set_reserve`, `set_reserve`, `set_status` | `blend_admin` |
