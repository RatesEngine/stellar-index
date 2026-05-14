-- 0033 up — pre-seed `routers` with the three Phase-A DeFindex
-- autocompound vaults.
--
-- Sourced from web/explorer/src/app/aggregators/page.tsx (last
-- verified 2026-05-08; see also paltalabs/defindex's source repo).
-- Multi-vault discovery via factory `("DeFindexFactory","create")`
-- events is a Phase-B follow-up; until then, hand-curated.
--
-- kind = 'aggregator-vault' (vs. soroswap-router's 'router')
-- because vaults hold persistent capital — the per-tx routed_via
-- attribution path tags individual flows, but the
-- aggregator_exposures hypertable holds the period-over-period
-- vault-state snapshot.
--
-- Idempotent: ON CONFLICT DO NOTHING so re-running is a no-op.

BEGIN;

INSERT INTO routers (contract_id, name, kind, protocol_slug, auto_discovered, notes) VALUES
(
    'CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP',
    'defindex-vault-usdc-autocompound',
    'aggregator-vault',
    'defindex',
    false,
    'Manual seed. USDC autocompound; deposits into Blend USDC pool. ' ||
    'WASM hash 0f3073517cbfacbfd482bc166cff38a0e7abeab9b7ee77334abab45880fb8f3a ' ||
    '(paltalabs/defindex tag 1.0.0). Decoder: internal/sources/defindex/. ' ||
    'WASM audit: docs/operations/wasm-audits/defindex.md (in_progress).'
),
(
    'CC5CE6MWISDXT3MLNQ7R3FVILFVFEIH3COWGH45GJKL6BD2ZHF7F7JVI',
    'defindex-vault-eurc-autocompound',
    'aggregator-vault',
    'defindex',
    false,
    'Manual seed. EURC autocompound; same WASM hash + audit reference as USDC vault.'
),
(
    'CDPWNUW7UMCSVO36VAJSQHQECISPJLCVPDASKHRC5SEROAAZDUQ5DG2Z',
    'defindex-vault-xlm-autocompound',
    'aggregator-vault',
    'defindex',
    false,
    'Manual seed. XLM autocompound; same WASM hash + audit reference as USDC vault.'
)
ON CONFLICT (contract_id) DO NOTHING;

COMMIT;
