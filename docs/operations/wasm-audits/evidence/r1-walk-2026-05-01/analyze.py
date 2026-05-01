#!/usr/bin/env python3
"""Per-source comprehensive WASM history. Uses hash anchors from
audit docs + frequency clustering to attribute contracts."""
import json, sys
from collections import defaultdict
from pathlib import Path

DATA_DIR = Path('/tmp/r1-wasm-walk')
OUT_DIR = DATA_DIR / 'per-source-v2'
OUT_DIR.mkdir(exist_ok=True)

# Anchors: contract IDs we KNOW belong to specific sources (from audit docs + code).
ANCHOR_CONTRACTS = {
    'CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2': ('soroswap', 'factory'),
    'CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH': ('soroswap', 'router'),
    'CAVLP5DH2GJPZMVO7IJY4CVOD5MWEFTJFVPD2YY2FQXOQHRGHK4D6HLP': ('aquarius', 'router'),
    'CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI': ('phoenix', 'factory'),
    'CCSSOHTBL3LEWUCBBEB5NJFC2OKFRC74OWEIJIZLRJBGAAU4VMU5NV4W': ('phoenix', 'multihop'),
    'CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M': ('reflector', 'dex'),
    'CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN': ('reflector', 'cex'),
    'CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC': ('reflector', 'fx'),
    'CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M': ('band', 'standard-reference'),
    'CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG': ('redstone', 'adapter'),
    'CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM': ('comet', 'pool-blend-backstop'),
    # Other contracts in -all walk that are likely Aquarius infrastructure
    # (router, share token registries, swap orchestrators):
    'CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G': ('aquarius', 'admin-or-related'),
    'CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK': ('aquarius', 'admin-or-related'),
    'CCYOZJCOPG34LLQQ7N24YXBM7LL62R7ONMZ3G6WZAAYPB5OYKOMJRN63': ('aquarius', 'admin-or-related'),
    'CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI': ('phoenix', 'factory'),
}

# Anchor hashes from audit docs (full 64-char or longest available prefix).
# Each prefix maps to a (source, role) pair.
ANCHOR_HASHES = {
    # Soroswap (audit doc):
    '5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae': ('soroswap', 'factory-wasm'),
    '4c3db3ebd2d6a2ab23de1f622eaabb39501539b4611b68622ec4e47f76c4ba07': ('soroswap', 'router-wasm'),
    '18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e': ('soroswap', 'pair-wasm'),
    # Phoenix (audit doc — partial 16-char prefix; resolve to full from walk):
    # Reflector
    '4a64c8c8502df326f4ce06d98998dc7d8a61575a11d6c0fbd4c60d10dfe28ffa': ('reflector', 'v2-wasm'),
    'df88820e231ad8f3027871e5dd3cf45491d7b7735e785731466bfc2946008608': ('reflector', 'v3-wasm'),
    # Redstone
    'b400f7a8ac121022955be1bd2468fcb99f126d2aa2fcc185a6abba36e83a3ef2': ('redstone', 'hotfix-wasm'),
    '5e93d22c9e19b254dae5474aebbb65a39f2f53b3b1d4371c58281987e1e29945': ('redstone', 'production-wasm'),
    # Comet
    '8abc28913035c07411ed5d134e6bfeab4723d97ddd4d1a22a0605d35c94d1a36': ('comet', 'pool-wasm'),
    # Band
    '6cdb9a3cdeec01a113c50e311218eeb0991aff8f7b379f556badca2b49b1eb01': ('band', 'reference-wasm'),
}

# Audit-doc 16-char prefixes that we couldn't find as full hashes in the walk
# (likely outdated — protocols upgraded since audit-doc was written).
AUDIT_DOC_STALE_PREFIXES = {
    'aquarius': ['8875f0c770fb26d3', 'ae0da5a84b15805c', 'f1077e0b77da5e62'],
    'phoenix':  ['13b158655e403969', '167ab414a226427d'],
}

with open(DATA_DIR / 'wasm-history-full.json') as f:
    data = json.load(f)

contracts = {c['contract']: (c.get('ranges') or []) for c in data}
hash_to_contracts = defaultdict(set)
for cid, ranges in contracts.items():
    for r in ranges:
        hash_to_contracts[r['wasm_hash']].add(cid)

# Resolve audit-doc 16-char prefixes against full hashes in the walk
prefix_to_full = {}
for h in hash_to_contracts.keys():
    for src, prefixes in AUDIT_DOC_STALE_PREFIXES.items():
        for p in prefixes:
            if h.startswith(p):
                prefix_to_full[p] = h

# Resolve phoenix prefixes too
PHOENIX_PREFIXES = ['13b158655e403969', '167ab414a226427d']
for h in hash_to_contracts.keys():
    for p in PHOENIX_PREFIXES:
        if h.startswith(p):
            ANCHOR_HASHES[h] = ('phoenix', f'pool-wasm-{p[:8]}')

# Build (hash → source) from anchor hashes + anchor contracts' hashes
hash_source = {}
for h, (src, role) in ANCHOR_HASHES.items():
    hash_source[h] = (src, role)

for cid, (src, role) in ANCHOR_CONTRACTS.items():
    for r in contracts.get(cid, []):
        if r['wasm_hash'] not in hash_source:
            hash_source[r['wasm_hash']] = (src, f'used-by-{role}')

# Tag contracts by hashes they use; if all their hashes map to one source, tag.
contract_source = {}
for cid, (src, role) in ANCHOR_CONTRACTS.items():
    contract_source[cid] = (src, role)

for cid, ranges in contracts.items():
    if cid in contract_source:
        continue
    sources = set()
    for r in ranges:
        s = hash_source.get(r['wasm_hash'])
        if s:
            sources.add(s[0])
    if len(sources) == 1:
        contract_source[cid] = (sources.pop(), 'instance')

# Per-source aggregation
per_source = defaultdict(lambda: {
    'contracts': [], 'unique_hashes': set(), 'total_ranges': 0,
})
for cid, (src, role) in contract_source.items():
    per_source[src]['contracts'].append((cid, role))
    for r in contracts.get(cid, []):
        per_source[src]['unique_hashes'].add(r['wasm_hash'])
        per_source[src]['total_ranges'] += 1

# Untagged: contracts in walk we couldn't attribute
untagged = [cid for cid in contracts if cid not in contract_source]
untagged_with_ranges = [cid for cid in untagged if contracts[cid]]

# Print summary
print("Per-source breakdown (improved clustering):\n")
print(f"{'source':12s}  {'contracts':>9s}  {'unique_hashes':>13s}  {'total_ranges':>12s}  earliest_ledger")
for src in sorted(per_source):
    info = per_source[src]
    earliest = None
    for cid, _ in info['contracts']:
        for r in contracts.get(cid, []):
            if earliest is None or r['from_ledger'] < earliest:
                earliest = r['from_ledger']
    print(f"  {src:12s}  {len(info['contracts']):>9d}  {len(info['unique_hashes']):>13d}  {info['total_ranges']:>12d}  {earliest}")

print(f"\nTagged total: {len(contract_source)} / {len(contracts)}")
print(f"Untagged WITH ranges: {len(untagged_with_ranges)} (contracts that DID change state but we can't attribute)")
print(f"Untagged WITHOUT ranges: {len(untagged) - len(untagged_with_ranges)} (contracts that never changed — could be in any source or none)")

# Emit per-source detail JSONs
for src in per_source:
    out = {
        'source': src,
        'contract_count': len(per_source[src]['contracts']),
        'unique_wasm_hashes': sorted(per_source[src]['unique_hashes']),
        'unique_wasm_hash_count': len(per_source[src]['unique_hashes']),
        'total_range_count': per_source[src]['total_ranges'],
        'contracts': [],
    }
    for cid, role in sorted(per_source[src]['contracts']):
        ranges = contracts.get(cid, [])
        out['contracts'].append({
            'contract': cid,
            'role': role,
            'first_ledger': (ranges[0]['from_ledger'] if ranges else None),
            'last_ledger': (ranges[-1]['to_ledger'] if ranges else None),
            'range_count': len(ranges),
            'unique_hashes': sorted({r['wasm_hash'] for r in ranges}),
            'ranges': ranges,
        })
    out['contracts'].sort(key=lambda c: (c['first_ledger'] is None, c['first_ledger']))
    with open(OUT_DIR / f'{src}.json', 'w') as f:
        json.dump(out, f, indent=2)

# Untagged JSON for further investigation
with open(OUT_DIR / '_untagged.json', 'w') as f:
    json.dump({
        'count': len(untagged),
        'with_ranges': len(untagged_with_ranges),
        'contracts': sorted(untagged),
    }, f, indent=2)

# Emit hash-frequency dump for the cluster analysis
freq = sorted([(len(v), h) for h, v in hash_to_contracts.items()], reverse=True)
with open(OUT_DIR / '_hash-frequency.json', 'w') as f:
    json.dump([{'hash': h, 'contract_count': n, 'attributed_to': hash_source.get(h)} for n, h in freq], f, indent=2)

# Print stale audit-doc prefix audit
print("\nAudit-doc hash prefixes that DON'T match any walk hash (likely doc rot):")
walk_hashes = set(hash_to_contracts.keys())
for src, prefixes in AUDIT_DOC_STALE_PREFIXES.items():
    for p in prefixes:
        matches = [h for h in walk_hashes if h.startswith(p)]
        status = matches[0] if matches else "STALE — no match"
        print(f"  {src:10s}  {p}  →  {status}")
