# `configs/audit/` — auditor inputs

Curated source-of-truth lists feeding the `ratesengine-ops wasm-history`
and related audit tooling. Maintained in repo (rather than ad-hoc on
r1) so audits are reproducible.

## Files

### `wasm-walk-contracts.yaml`

The input list for `ratesengine-ops wasm-history` walks. One entry per
Soroban source we ingest, plus an `_unattributed` block for contracts
known to be operational but whose ContractInstance entries are
TTL-evicted from RPC.

**Total: 540 contracts across 8 sources** (derived from the 2026-04-30
r1 walk + 2026-05-01 cross-check).

To re-run a walk for a single source:

```sh
# Build the contract list from the YAML
yq '.aquarius.contracts | join(",")' configs/audit/wasm-walk-contracts.yaml \
  | ssh root@r1 "cat > /tmp/aquarius-input.txt"

# Run the walk on r1 (off the curated list)
ssh root@r1 "set -a; . /etc/default/ratesengine-ops; set +a; \
  ratesengine-ops wasm-history \
    -config /etc/ratesengine.toml \
    -from 50457424 -to \$(date +%s)-derived-current-tip \
    -contracts \$(cat /tmp/aquarius-input.txt) \
    -parallel 8 \
    > /var/log/wasm-history-aquarius.json"
```

Or against all sources at once: concatenate the contract lists across
sources and pass as one `-contracts` flag (this is what the 2026-04-30
walk did, producing the `wasm-history-full.json` shipped under
`docs/operations/wasm-audits/evidence/r1-walk-2026-05-01/`).

### Refreshing the YAML

Re-derive the contract set whenever:

- A factory deploys a new pool (Soroswap / Aquarius / Phoenix / Blend).
- A new SEP-40 oracle goes live (Reflector additions).
- A new venue is onboarded (Comet, Band, Redstone deployments).

Each source's `provenance:` field documents how to enumerate. Minimum
cadence: alongside every quarterly WASM-history walk.

### Schema

```yaml
<source>:
  provenance: |
    How to refresh the contract list (factory call, RPC query, etc.).
  notes: |
    Source-specific quirks (TTL eviction, role splits, etc.).
  contracts:  # N total
    - C... (1 line per contract address)
```

## Why this lives in repo

Per the audit's "Tooling gaps surfaced":

> The 532-contract input list isn't curated in repo. Future audits
> would have to reconstruct it. Curate
> `configs/audit/wasm-walk-contracts.yaml` per source with provenance.

This file is the answer. The 2026-04-30 r1 walk used an ad-hoc list
the operator assembled at run time; this YAML captures that list as
the durable source-of-truth, with provenance per source so anyone can
re-derive it.
