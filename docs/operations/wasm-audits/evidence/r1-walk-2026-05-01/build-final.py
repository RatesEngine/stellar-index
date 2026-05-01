"""Final synthesis. Combines:
  - wasm-history-full.json (transitions)
  - current-state.json (current hash for ranges:null contracts)
  - classification (hash → source/role)
Produces per-source comprehensive picture, total assets in scope,
upgrade chronology."""
import json
from collections import defaultdict, Counter
from pathlib import Path

DATA = Path('/tmp/r1-wasm-walk')
OUT = DATA / 'final'
OUT.mkdir(exist_ok=True)

# Load everything
walk = json.loads((DATA / 'wasm-history-full.json').read_text())
current = json.loads((DATA / 'current-state.json').read_text())
classif = json.loads((DATA / 'classification-v2.json').read_text())

# Re-classify the 3 new hashes (all Aquarius pools)
classif_dict = {c['hash']: (c['source'], c['role']) for c in classif}
classif_dict.update({
    'ae0da5a84b15805c5c7931ac567a8d1b34be3f26b483993d9ff80cb2c3de9852': ('aquarius', 'aquarius-pool'),
    'f1077e0b77da5e62d596e13aeae4160104cad99e7ef7f3183a6c9b6ec9e747cd': ('aquarius', 'aquarius-pool'),
    '8875f0c770fb26d3053648856732a649936aed5db246845fa209f9032001b208': ('aquarius', 'aquarius-pool'),
})

# Build (contract → list of (ledger, hash) transitions OR ('current', hash) marker)
contract_history = {}
for c in walk:
    cid = c['contract']
    ranges = c.get('ranges') or []
    if ranges:
        contract_history[cid] = [
            {'kind': 'transition', 'from_ledger': r['from_ledger'], 'to_ledger': r['to_ledger'],
             'wasm_hash': r['wasm_hash']}
            for r in ranges
        ]
    else:
        # ranges:null → use current-state RPC
        cs = current.get(cid, {})
        h = cs.get('hash')
        if h and h != 'stellar-asset':
            contract_history[cid] = [{'kind': 'current_state', 'wasm_hash': h}]
        else:
            contract_history[cid] = []

# Tag each contract with source via its hashes
def tag_contract(cid):
    h = contract_history[cid]
    if not h:
        return ('unknown', None)
    sources = set()
    roles = []
    for entry in h:
        s, r = classif_dict.get(entry['wasm_hash'], (None, None))
        if s:
            sources.add(s)
            roles.append(r)
    if not sources:
        return ('unknown', None)
    if len(sources) == 1:
        return (sources.pop(), roles[-1])
    return (','.join(sorted(sources)), roles[-1])

contract_source = {cid: tag_contract(cid) for cid in contract_history}

# Per-source aggregation
per_source = defaultdict(lambda: {'contracts': [], 'unique_hashes': set(), 'pool_count': 0,
                                   'factory_count': 0, 'router_count': 0, 'multihop_count': 0,
                                   'roles': Counter()})
for cid, (src, role) in contract_source.items():
    info = per_source[src]
    info['contracts'].append((cid, role))
    info['roles'][role] += 1
    for entry in contract_history[cid]:
        info['unique_hashes'].add(entry['wasm_hash'])

# Print summary
print(f"\n{'source':12s}  {'contracts':>9s}  {'unique_wasms':>12s}  {'role_breakdown'}")
for src in sorted(per_source):
    info = per_source[src]
    role_summary = ', '.join(f'{r}:{n}' for r, n in info['roles'].most_common())
    print(f"  {src:12s}  {len(info['contracts']):>9d}  {len(info['unique_hashes']):>12d}  {role_summary}")

total_attributed = sum(len(per_source[s]['contracts']) for s in per_source if s != 'unknown')
total = len(contract_history)
print(f"\nTotal: {total_attributed} / {total} contracts attributed ({total_attributed/total*100:.1f}%)")

# Emit per-source detailed JSONs
for src, info in per_source.items():
    out = {
        'source': src,
        'contract_count': len(info['contracts']),
        'unique_wasm_count': len(info['unique_hashes']),
        'role_breakdown': dict(info['roles']),
        'contracts': [],
    }
    for cid, role in sorted(info['contracts']):
        out['contracts'].append({
            'contract': cid,
            'final_role': role,
            'wasm_history': contract_history[cid],
        })
    (OUT / f'{src}.json').write_text(json.dumps(out, indent=2))

# Hash inventory: for each WASM, list contracts using it
hash_to_contracts = defaultdict(list)
for cid, history in contract_history.items():
    seen = set()
    for entry in history:
        if entry['wasm_hash'] not in seen:
            hash_to_contracts[entry['wasm_hash']].append(cid)
            seen.add(entry['wasm_hash'])

hashes_out = {}
for h, contracts in hash_to_contracts.items():
    src, role = classif_dict.get(h, ('unknown', None))
    hashes_out[h] = {
        'source': src,
        'role': role,
        'contract_count': len(contracts),
    }
(OUT / 'hashes.json').write_text(json.dumps(hashes_out, indent=2))

print(f"\n{len(hashes_out)} unique WASMs total. Per source:")
for src in sorted(per_source):
    src_hashes = [h for h, info in hashes_out.items() if info['source'] == src]
    print(f"  {src:12s}  {len(src_hashes)} unique WASMs")
