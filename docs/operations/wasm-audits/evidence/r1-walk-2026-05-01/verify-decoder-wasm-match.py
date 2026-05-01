"""Role-aware decoder ↔ WASM topic verification."""
import json
from pathlib import Path
from collections import defaultdict

WASM_DIR = Path('/tmp/r1-wasm-walk/wasm-bytes')

# Per (source, role): the topics that role's contract is expected
# to emit in events the decoder watches for. Empty set means
# "this role doesn't emit decoder-relevant events" (factories that
# only emit `deploy`, routers that orchestrate, oracles read via
# methods, etc.)
ROLE_TOPICS = {
    ('soroswap', 'soroswap-pair'):     {'swap', 'sync', 'skim', 'deposit', 'withdraw'},
    ('soroswap', 'soroswap-factory'):  {'new_pair'},
    ('soroswap', 'soroswap-router'):   set(),
    ('aquarius', 'aquarius-pool'):     {'trade', 'deposit', 'withdraw', 'claim'},
    ('aquarius', 'aquarius-router'):   set(),
    ('phoenix',  'phoenix-pool'):      {'swap'},  # ('swap', '<field>') 2-tuple
    ('phoenix',  'phoenix-factory'):   set(),
    ('phoenix',  'phoenix-multihop'):  set(),
    ('reflector','reflector'):         set(),  # SEP-40 read via methods
    ('comet',    'comet'):             {'swap', 'join_pool'},
    ('redstone', 'redstone'):          {'REDSTONE'},
    ('band',     'band'):              set(),  # Band emits zero events
    ('blend',    'blend-pool'):        {'new_auction', 'fill_auction', 'delete_auction',
                                         'defaulted_debt', 'bad_debt', 'gulp_emissions',
                                         'reserve_emission_update', 'set_status', 'set_admin',
                                         'set_reserve', 'queue_set_reserve', 'cancel_set_reserve',
                                         'update_pool', 'update_reserves',
                                         'borrow', 'repay', 'supply', 'supply_collateral',
                                         'withdraw_collateral', 'flash_loan'},
    ('blend',    'blend-backstop'):    {'gulp_emissions'},
    ('blend',    'blend-pool-factory'):{'deploy'},
}

# hash → (source, role)
classif = json.loads(Path('/tmp/r1-wasm-walk/classification-v2.json').read_text())
hash_role = {c['hash']: (c['source'], c['role']) for c in classif}
for h in ['ae0da5a84b15805c5c7931ac567a8d1b34be3f26b483993d9ff80cb2c3de9852',
          'f1077e0b77da5e62d596e13aeae4160104cad99e7ef7f3183a6c9b6ec9e747cd',
          '8875f0c770fb26d3053648856732a649936aed5db246845fa209f9032001b208']:
    hash_role[h] = ('aquarius', 'aquarius-pool')
hash_role['c1f4502a757e25c611f5a159bc1ab0eef64085adac6c68123dca66e87faffbc2'] = ('blend', 'blend-backstop')
hash_role['31328050548831f63d2b72e37bcfd0bb7371b7907135755dbe09ed434d755ca9'] = ('blend', 'blend-pool-factory')

def has_string(wasm_path: Path, needle: str) -> bool:
    return needle.encode() in wasm_path.read_bytes()

results = []
for wasm in sorted(WASM_DIR.glob('*.wasm')):
    h = wasm.stem
    src, role = hash_role.get(h, (None, None))
    if not src or src == 'unknown':
        continue
    expected = ROLE_TOPICS.get((src, role), None)
    if expected is None:
        results.append({'hash': h[:16], 'source': src, 'role': role,
                        'verdict': 'no expected-topics rule',
                        'expected': [], 'present': [], 'missing': []})
        continue
    found = {t: has_string(wasm, t) for t in expected}
    missing = sorted(t for t, ok in found.items() if not ok)
    present = sorted(t for t, ok in found.items() if ok)
    if not expected:
        verdict = 'OK (no decoder-relevant events expected for this role)'
    elif not missing:
        verdict = 'OK'
    else:
        verdict = f'MISSING {len(missing)}/{len(expected)}'
    results.append({'hash': h[:16], 'source': src, 'role': role,
                    'verdict': verdict, 'expected': sorted(expected),
                    'present': present, 'missing': missing})

# Summary
print(f"\n{'source':10s}  {'role':>26s}  {'WASMs':>5s}  {'OK':>4s}  {'partial':>7s}")
groups = defaultdict(lambda: {'all':0, 'ok':0, 'partial':0})
for r in results:
    k = (r['source'], r['role'])
    groups[k]['all'] += 1
    if r['verdict'].startswith('OK'):
        groups[k]['ok'] += 1
    elif r['verdict'].startswith('MISSING'):
        groups[k]['partial'] += 1

for (src, role), s in sorted(groups.items()):
    print(f"  {src:10s}  {role:>26s}  {s['all']:>5d}  {s['ok']:>4d}  {s['partial']:>7d}")

# Issues
issues = [r for r in results if r['verdict'].startswith('MISSING')]
print(f'\nIssues found: {len(issues)}')
for r in issues:
    print(f"  {r['source']:10s} {r['role']:>26s} {r['hash']} verdict={r['verdict']}")
    print(f"    expected: {r['expected']}")
    print(f"    missing:  {r['missing']}")

with open('/tmp/r1-wasm-walk/decoder-wasm-match-v2.json', 'w') as f:
    json.dump(results, f, indent=2)
