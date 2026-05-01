---
title: Protocol epoch timelines (2026-05-01 r1 walk)
last_verified: 2026-05-01
status: comprehensive (8 sources, 540/543 contracts, 52 WASMs)
related:
  - docs/operations/wasm-audits/r1-walk-2026-05-01.md
  - configs/audit/wasm-walk-contracts.yaml
---

# Protocol epoch timelines

> Per-protocol view of every contract we ingest, every WASM it has run, and which ledger ranges (epochs) each WASM was active. Built from the 2026-04-30 r1 wasm-history walk + 2026-05-01 cross-check via Soroban-RPC. The structured source data lives at [`evidence/r1-walk-2026-05-01/per-source-final/`](evidence/r1-walk-2026-05-01/per-source-final/); this document renders it as readable timelines.

## Coverage at a glance

| Source | Contracts | Unique WASMs | Stability |
|---|---|---|---|
| soroswap | 196 | 3 | 🟢 stable (no observable upgrades) |
| aquarius | 314 | 19 | 🟡 active upgrade chain (5 successive pool WASMs) |
| phoenix | 13 | 22 | 🟠 most-iterated (22 WASMs / 13 contracts) |
| reflector | 3 | 2 | 🟡 v2→v3 migration completed |
| comet | 1 | 1 | 🟢 stable |
| redstone | 1 | 2 | 🟡 35-min hotfix → production |
| band | 1 | 1 | 🟢 stable |
| blend | 11 | 3 | 🟡 walk pending |

**Total: 540 contracts, 53 unique WASMs (52 unique across all sources after de-dup)**

## Soroswap

All three Soroswap WASMs (factory + router + pair) have been completely stable since deployment. The factory's `5db738b0…` has had 6 self-touches in the walked window — these are TTL-extension restamps to the SAME hash, not upgrades. Net: zero observable upgrade events. The 194 pair instances all run the canonical pair WASM `18051456…`; none has ever transitioned.

### WASM inventory (3 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `18051456816b66f1…` | soroswap-pair | 321 | `evidence/r1-walk-2026-05-01/wasm-bytes/18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e.wasm` |
| `5db738b05d914812…` | soroswap-factory | 6 | `evidence/r1-walk-2026-05-01/wasm-bytes/5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae.wasm` |
| `4c3db3ebd2d6a2ab…` | soroswap-router | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/4c3db3ebd2d6a2ab23de1f622eaabb39501539b4611b68622ec4e47f76c4ba07.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 112 contracts | 112 | soroswap-pair (current state) |
| 28 contracts | 28 | soroswap-pair |
| 20 contracts | 20 | soroswap-pair → soroswap-pair |
| 14 contracts | 14 | soroswap-pair → soroswap-pair → soroswap-pair |
| 8 contracts | 8 | soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair |
| 7 contracts | 7 | soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair |
| 5 contracts | 5 | soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair → soroswap-pair |
| 1 contracts | CA4HEQTL2WPEUYKYKCDO… | soroswap-factory → soroswap-factory → soroswap-factory → soroswap-factory → soroswap-factory → soroswap-factory |
| 1 contracts | CAG5LRYQ5JVEUI5TEID7… | soroswap-router |

## Aquarius

Aquarius has the most WASMs (19) and a clear two-cohort structure:
- **Cohort A** (168 pools, never-upgraded since deployment) — runs one of three older variants (`ae0da5a84b15805c…` volatile / `f1077e0b77da5e62…` stableswap / `8875f0c770fb26d3…` rewards-enhanced).
- **Cohort B** (145 pools, upgraded through 5 successive WASMs) — went through the chain `b54ba37b → 2d770946 → 7cecf23b → a1629dcd → 4f080d24` over the 2-year window.

The router itself has had 6 distinct WASM versions tracking governance + protocol-fee admin features. Decoder is keyed off event-topic shape (not per-WASM behaviour) — same `Symbol("trade")` event family across all 19 WASMs.

### WASM inventory (19 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `a1629dcdf9192727…` | aquarius-pool/v5 | 219 | `evidence/r1-walk-2026-05-01/wasm-bytes/a1629dcdf9192727296124ca4ef0f5cc5829086073b4cf5b4f42e27331b22ce0.wasm` |
| `ae0da5a84b15805c…` | aquarius-pool/volatile-v1 | 149 | `evidence/r1-walk-2026-05-01/wasm-bytes/ae0da5a84b15805c5c7931ac567a8d1b34be3f26b483993d9ff80cb2c3de9852.wasm` |
| `2d770946d5429b09…` | aquarius-pool/v3 | 138 | `evidence/r1-walk-2026-05-01/wasm-bytes/2d770946d5429b0988fd33904cf19b5498823f668cb69a0a5028e988023d2040.wasm` |
| `b54ba37b7bb7dd69…` | aquarius-pool/v2 | 97 | `evidence/r1-walk-2026-05-01/wasm-bytes/b54ba37b7bb7dd69a7759caa9eec70e9e13615ba3b009fc23c4626ae9dffa27f.wasm` |
| `7cecf23b7c8cd6f7…` | aquarius-pool/v4 | 55 | `evidence/r1-walk-2026-05-01/wasm-bytes/7cecf23b7c8cd6f7768d272f89fd59bf71a8eff0eed07f251c5f47d9570ed435.wasm` |
| `bb79374086c981f6…` | aquarius-pool/variant-W | 52 | `evidence/r1-walk-2026-05-01/wasm-bytes/bb79374086c981f6937907516ba98af68aadf005bc34f0a18a863e7d02d37b48.wasm` |
| `64ef0bc6962f0654…` | aquarius-pool/variant-Y | 37 | `evidence/r1-walk-2026-05-01/wasm-bytes/64ef0bc6962f06546c0e00a11877c869d78c08043f79d50ee2c09cb1b837c477.wasm` |
| `2e6f1daed872881a…` | aquarius-pool/variant-Z | 25 | `evidence/r1-walk-2026-05-01/wasm-bytes/2e6f1daed872881ac52bf637328076cd81a3b5a70e46524453a354309c7a3786.wasm` |
| `4f080d249700b9a3…` | aquarius-pool/v6 (newest) | 18 | `evidence/r1-walk-2026-05-01/wasm-bytes/4f080d249700b9a30ca2e1bb828ecb29e47357cd36b122c7744593cef30b7a08.wasm` |
| `29eb1047312dc244…` | aquarius-pool/variant-X | 17 | `evidence/r1-walk-2026-05-01/wasm-bytes/29eb1047312dc244e06128910bf60aa71e4104083c5bf41af006177b65216ca0.wasm` |
| `f1077e0b77da5e62…` | aquarius-pool/stableswap-v1 | 13 | `evidence/r1-walk-2026-05-01/wasm-bytes/f1077e0b77da5e62d596e13aeae4160104cad99e7ef7f3183a6c9b6ec9e747cd.wasm` |
| `8875f0c770fb26d3…` | aquarius-pool/rewards-v1 | 6 | `evidence/r1-walk-2026-05-01/wasm-bytes/8875f0c770fb26d3053648856732a649936aed5db246845fa209f9032001b208.wasm` |
| `f5bdd7c4b58401cd…` | aquarius-pool/variant-V | 6 | `evidence/r1-walk-2026-05-01/wasm-bytes/f5bdd7c4b58401cd3c28bd4ba4162650fe1971f9b22c27b5be7c4d3664596a96.wasm` |
| `2c2b3c9756f37209…` | aquarius-router/v3 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/2c2b3c9756f372092f9120393b41b1b6a5aa5546de169d1100daf370e1c880d8.wasm` |
| `a67ad7715ecb3e17…` | aquarius-router/v6 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/a67ad7715ecb3e174a1afe731d9f534e616dac3d5b115781762ec531c2ce1d30.wasm` |
| `26c495019afb7448…` | aquarius-router/v2 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/26c495019afb7448f690a82d6e66d8fab1ad3fd3e7b4aec7d554209966c9d19d.wasm` |
| `b04880dfbe7630b1…` | aquarius-router/v5 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/b04880dfbe7630b17cec60403669123987fa0ef6b91e418b30fb68180ac4f93b.wasm` |
| `8cf10d1439a9ed1f…` | aquarius-router/v4 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/8cf10d1439a9ed1f8d27606cadcdae7555851c40ef0e6e4bb1ffe2f8ebcd7658.wasm` |
| `2491b9b663f647bd…` | aquarius-router/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/2491b9b663f647bdb5ff358c3bbe6ddd4e9a43bfbe8edebccd26ca71ad2e84b8.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 149 contracts | 149 | aquarius-pool/volatile-v1 (current state) |
| 32 contracts | 32 | aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 27 contracts | 27 | aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 17 contracts | 17 | aquarius-pool/v5 |
| 13 contracts | 13 | aquarius-pool/stableswap-v1 (current state) |
| 13 contracts | 13 | aquarius-pool/v4 → aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 13 contracts | 13 | aquarius-pool/v4 → aquarius-pool/v4 → aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 7 contracts | 7 | aquarius-pool/variant-Y → aquarius-pool/variant-Y → aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 6 contracts | 6 | aquarius-pool/rewards-v1 (current state) |
| 6 contracts | 6 | aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 5 contracts | 5 | aquarius-pool/variant-X → aquarius-pool/variant-Y → aquarius-pool/variant-Y → aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 4 contracts | 4 | aquarius-pool/v6 (newest) → aquarius-pool/v6 (newest) → aquarius-pool/v4 → aquarius-pool/v4 → aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 4 contracts | 4 | aquarius-pool/v6 (newest) → aquarius-pool/v6 (newest) → aquarius-pool/v4 → aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 4 contracts | 4 | aquarius-pool/v5 → aquarius-pool/v5 |
| 3 contracts | 3 | aquarius-pool/variant-X → aquarius-pool/variant-X → aquarius-pool/variant-Y → aquarius-pool/variant-Y → aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 3 contracts | 3 | aquarius-pool/variant-V → aquarius-pool/variant-V → aquarius-pool/variant-X → aquarius-pool/variant-X → aquarius-pool/variant-Y → aquarius-pool/variant-Y → aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 2 contracts | 2 | aquarius-pool/variant-W |
| 2 contracts | 2 | aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 2 contracts | 2 | aquarius-pool/v6 (newest) → aquarius-pool/v4 → aquarius-pool/v4 → aquarius-pool/v3 → aquarius-pool/v3 → aquarius-pool/v2 → aquarius-pool/v5 → aquarius-pool/v5 |
| 1 contracts | CAIOFLKJ3DQGZR2C2OF2… | aquarius-pool/variant-Y → aquarius-pool/variant-Z → aquarius-pool/variant-W → aquarius-pool/variant-W |
| 1 contracts | CBQDHNBFBZYE4MKPWBSJ… | aquarius-router/v3 → aquarius-router/v3 → aquarius-router/v6 → aquarius-router/v6 → aquarius-router/v2 → aquarius-router/v2 → aquarius-router/v4 → aquarius-router/v5 → aquarius-router/v5 → aquarius-router/v1 |

## Phoenix

Phoenix is the most-iterated source — 22 unique WASMs across 13 contracts. The factory has had 5 versions, multihop 3, and the 11 pool instances span 14 different pool-template versions (each pool has been upgraded multiple times).

### WASM inventory (22 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `e747f94bd6d54805…` | phoenix-pool/v14 | 19 | `evidence/r1-walk-2026-05-01/wasm-bytes/e747f94bd6d54805390f66f608311628b054e77c5062336aaf38b46492d622b8.wasm` |
| `167ab414a226427d…` | phoenix-pool/v2 | 12 | `evidence/r1-walk-2026-05-01/wasm-bytes/167ab414a226427de34c19947ef9c5cf38c6c0ed91ecf9392f7cef3278ff506c.wasm` |
| `7e834fa193deacd7…` | phoenix-pool/v8 | 5 | `evidence/r1-walk-2026-05-01/wasm-bytes/7e834fa193deacd7fdd28104a58c8a2684b6b6a4e8d2485d37195569d8810888.wasm` |
| `14ed2bb1a025c22b…` | phoenix-pool/v4 | 4 | `evidence/r1-walk-2026-05-01/wasm-bytes/14ed2bb1a025c22bcc730ffc9da3f5aa9f7c653f4be402812c32f249e20dee01.wasm` |
| `ac63334c62a48aa3…` | phoenix-pool/v11 | 3 | `evidence/r1-walk-2026-05-01/wasm-bytes/ac63334c62a48aa3ea5c2187e423ebe89bcf285848e25251aa839667323083c8.wasm` |
| `9f10e0527cd180e5…` | phoenix-pool/v10 | 3 | `evidence/r1-walk-2026-05-01/wasm-bytes/9f10e0527cd180e53ab3da55484eca00c004333309fbff81b5ba8a52d8909f26.wasm` |
| `b4e621fbdc990177…` | phoenix-pool/v12 | 3 | `evidence/r1-walk-2026-05-01/wasm-bytes/b4e621fbdc990177ef071c439100bc382c58d08e9d9710f82fbb993698e17931.wasm` |
| `0293730a1d9bdb23…` | phoenix-pool/v3 | 3 | `evidence/r1-walk-2026-05-01/wasm-bytes/0293730a1d9bdb23ad2b2d7f6d7ff86dc5fbe620efb29187f8210dc1d5b90bef.wasm` |
| `96c6a73863de6e33…` | phoenix-factory/v3 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/96c6a73863de6e331d8103898f421bb7af719b87bbd4ac14d5a16f6b2662546b.wasm` |
| `24f1456544ebc25a…` | phoenix-pool/v6 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/24f1456544ebc25a598792c4f64113711ff78ad6d9bda95e057ed2ade7c23800.wasm` |
| `27635b9a4439ac91…` | phoenix-pool/v7 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/27635b9a4439ac911b2a9b431627ad2e2545faded01da27e405003050ec91887.wasm` |
| `e1464afcf0c7c01e…` | phoenix-factory/v5 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/e1464afcf0c7c01e4306e3eb9d16f653500fbaa31c3cce0afd675449d58cea14.wasm` |
| `2bbb91c58cb8432f…` | phoenix-factory/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/2bbb91c58cb8432fd40446e80188a25e7c07dc2edc853e885d65cafbbbf19581.wasm` |
| `721badb85470a81d…` | phoenix-factory/v2 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/721badb85470a81d9d0a1c72dd0debb37f5f268419d4b94d125e546cc32d350e.wasm` |
| `c54ba54bd9e37503…` | phoenix-factory/v4 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/c54ba54bd9e37503a641fa661126cd858faf57738c1e903b84f72ac505016393.wasm` |
| `916315045be9861c…` | phoenix-pool/v9 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/916315045be9861c47b22a8b2502ea266e75659d54fdbd0303ba1b9091c9632f.wasm` |
| `60332ba12801eda6…` | phoenix-multihop/v2 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/60332ba12801eda65874e9afdbfdf8d114f56011fe7d1f902103cd7767c547d4.wasm` |
| `18336805466bbd05…` | phoenix-multihop/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/18336805466bbd05bc388610d36de73994f5b9061bb199c02418a073fcf4b281.wasm` |
| `77bdc0a993960faa…` | phoenix-multihop/v3 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/77bdc0a993960faa2b0dac7b43a5160d6ed745ee787573c2f0ad8d00d4806366.wasm` |
| `18e40185bbebefa0…` | phoenix-pool/v5 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/18e40185bbebefa02e358353699302c1c0266a5b3d048981b241eec8d22ffd76.wasm` |
| `13b158655e403969…` | phoenix-pool/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/13b158655e40396957537bf1c528c6542b315930c1c9e0df640f57293c8af2ca.wasm` |
| `e235ace43d2eaea9…` | phoenix-pool/v13 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/e235ace43d2eaea9a57d5717d6888e32d6bdd0fbf224ea4c83a602e94d3d8f6e.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 4 contracts | 4 | phoenix-pool/v14 → phoenix-pool/v14 → phoenix-pool/v2 |
| 2 contracts | 2 | phoenix-pool/v11 → phoenix-pool/v10 → phoenix-pool/v12 → phoenix-pool/v3 → phoenix-pool/v14 → phoenix-pool/v6 → phoenix-pool/v14 → phoenix-pool/v14 → phoenix-pool/v2 |
| 2 contracts | 2 | phoenix-pool/v14 → phoenix-pool/v7 → phoenix-pool/v8 → phoenix-pool/v8 → phoenix-pool/v2 → phoenix-pool/v2 |
| 1 contracts | CB4SVAWJA6TSRNOJZ7W2… | phoenix-factory/v5 → phoenix-factory/v3 → phoenix-factory/v3 → phoenix-factory/v1 → phoenix-factory/v2 → phoenix-factory/v4 |
| 1 contracts | CBHCRSVX3ZZ7EGTSYMKP… | phoenix-pool/v11 → phoenix-pool/v10 → phoenix-pool/v12 → phoenix-pool/v3 → phoenix-pool/v14 → phoenix-pool/v9 → phoenix-pool/v14 → phoenix-pool/v8 → phoenix-pool/v4 → phoenix-pool/v4 → phoenix-pool/v2 |
| 1 contracts | CCLZRD4E72T7JCZCN3P7… | phoenix-multihop/v2 → phoenix-multihop/v1 → phoenix-multihop/v3 |
| 1 contracts | CD5XNKK3B6BEF2N7ULNH… | phoenix-pool/v5 → phoenix-pool/v1 |
| 1 contracts | CDMXKSLG5GITGFYERUW2… | phoenix-pool/v14 → phoenix-pool/v13 → phoenix-pool/v4 → phoenix-pool/v4 → phoenix-pool/v2 |

## Reflector

All three SEP-40 oracles (DEX/CEX/FX) migrated v2 (`4a64c8c8…`) → v3 (`df88820e…`) coherently. v3 has been the active version since the migration; both versions expose the SEP-40 API (`lastprice`, `prices`, `price`, `base`, `assets`, `decimals`, `resolution`).

### WASM inventory (2 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `df88820e231ad8f3…` | reflector/v3 | 14 | `evidence/r1-walk-2026-05-01/wasm-bytes/df88820e231ad8f3027871e5dd3cf45491d7b7735e785731466bfc2946008608.wasm` |
| `4a64c8c8502df326…` | reflector/v2 | 2 | `evidence/r1-walk-2026-05-01/wasm-bytes/4a64c8c8502df326f4ce06d98998dc7d8a61575a11d6c0fbd4c60d10dfe28ffa.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 2 contracts | 2 | reflector/v2 → reflector/v3 → reflector/v3 → reflector/v3 → reflector/v3 → reflector/v3 → reflector/v3 |
| 1 contracts | CBKGPWGKSKZF52CFHMTR… | reflector/v3 → reflector/v3 |

## Comet

One contract, one WASM — the Blend backstop pool, stable since deployment.

### WASM inventory (1 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `8abc28913035c074…` | comet/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/8abc28913035c07411ed5d134e6bfeab4723d97ddd4d1a22a0605d35c94d1a36.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 1 contracts | CAS3FL6TLZKDGGSISDBW… | comet/v1 |

## Redstone

Two WASMs spanning a deliberate hotfix sequence: an initial deploy `b400f7a8…` lived for ledgers 58,758,722 → 58,759,141 (~35 minutes) before being replaced by the production hash `5e93d22c…`. Pre-backfill SQL guard reproduced in the synthesis report; required reading before any `ratesengine-ops backfill` overlapping the hotfix window.

### WASM inventory (2 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `b400f7a8ac121022…` | redstone/hotfix (35-min lifetime) | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/b400f7a8ac121022955be1bd2468fcb99f126d2aa2fcc185a6abba36e83a3ef2.wasm` |
| `5e93d22c9e19b254…` | redstone/production | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/5e93d22c9e19b254dae5474aebbb65a39f2f53b3b1d4371c58281987e1e29945.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 1 contracts | CA526Y2NQWGWVVQ7RFFP… | redstone/hotfix (35-min lifetime) → redstone/production |

## Band

One contract, one WASM — `CCQXWMZV…` running `6cdb9a3c…` since L50,842,736. No upgrades observed.

### WASM inventory (1 unique)

| WASM (first 16) | Role | Contracts using it | Bytes path |
|---|---|---|---|
| `6cdb9a3cdeec01a1…` | band/v1 | 1 | `evidence/r1-walk-2026-05-01/wasm-bytes/6cdb9a3cdeec01a113c50e311218eeb0991aff8f7b379f556badca2b49b1eb01.wasm` |

### Contract timelines

_Contracts sharing the same WASM history are grouped; rare singletons listed separately._

| Group | Contracts | WASM sequence (epoch order) |
|---|---|---|
| 1 contracts | CCQXWMZVM3KRTXTUPTN5… | band/v1 |

## How to refresh

Re-run the audit pipeline:

```sh
# 1) Run a fresh wasm-history walk on r1 against the curated list
yq '.[] | (select(.contracts) | .contracts) | .[]' \
  configs/audit/wasm-walk-contracts.yaml | sort -u > /tmp/all-contracts.txt
ssh r1 "set -a; . /etc/default/ratesengine-ops; set +a; \
  ratesengine-ops wasm-history -config /etc/ratesengine.toml \
    -from 50457424 -to <current-tip> -parallel 8 \
    -contracts \$(paste -sd, /tmp/all-contracts.txt) \
    > /var/log/wasm-history-full.json"

# 2) Pull WASMs (live + TTL-evicted) via Soroban-RPC
# 3) Re-run evidence/.../build-final.py + this renderer
```

Cadence: alongside every quarterly r1 walk, plus on detection of any new factory deployment or per-source contract upgrade.