"""Fetch WASM bytes via Soroban-RPC. Skip XDR parsing — just find
the WASM magic header (00 61 73 6d 01 00 00 00) and read until the
end (XDR padding may add 0-3 trailing zeros)."""
import base64, json, os, struct, urllib.request, hashlib
from pathlib import Path

DATA_DIR = Path('/tmp/r1-wasm-walk')
HASHES_FILE = DATA_DIR / 'unique-hashes.txt'
OUT_DIR = DATA_DIR / 'wasm-bytes'
OUT_DIR.mkdir(exist_ok=True)
RPC = os.environ.get('SOROBAN_RPC', 'https://soroban-rpc.creit.tech')

WASM_MAGIC = bytes.fromhex('0061736d01000000')

def b64(x): return base64.b64encode(x).decode()

def encode_key(hash_hex: str) -> str:
    return b64(struct.pack('>I', 7) + bytes.fromhex(hash_hex))

def call(method, params):
    req = json.dumps({'jsonrpc':'2.0','id':1,'method':method,'params':params}).encode()
    r = urllib.request.Request(RPC, data=req, headers={'Content-Type':'application/json'})
    with urllib.request.urlopen(r, timeout=60) as resp:
        return json.loads(resp.read())

def extract_wasm(xdr_b64: str, expected_hash: str) -> bytes:
    raw = base64.b64decode(xdr_b64)
    idx = raw.find(WASM_MAGIC)
    if idx < 0:
        raise ValueError("WASM magic not found in XDR")
    # The opaque field is XDR length-prefixed. The 4 bytes
    # immediately before the magic header are the length.
    if idx < 4:
        raise ValueError("magic at start; can't read length prefix")
    code_len = struct.unpack('>I', raw[idx-4:idx])[0]
    code = raw[idx:idx+code_len]
    if len(code) != code_len:
        raise ValueError(f"truncated WASM: want {code_len}, got {len(code)}")
    # Verify SHA-256 == expected hash (Soroban ContractCode hash convention)
    actual = hashlib.sha256(code).hexdigest()
    if actual != expected_hash:
        raise ValueError(f"hash mismatch: file has {actual}, expected {expected_hash}")
    return code

hashes = HASHES_FILE.read_text().strip().split('\n')
keys = [encode_key(h) for h in hashes]
hash_for_key = {k: h for k, h in zip(keys, hashes)}

found, not_found, errors = [], [], []
for i in range(0, len(keys), 50):
    batch = keys[i:i+50]
    print(f"Batch keys {i}..{i+len(batch)}")
    try:
        resp = call('getLedgerEntries', {'keys': batch})
    except Exception as e:
        print(f"  RPC error: {e}")
        continue
    if 'error' in resp:
        print(f"  rpc error: {resp['error']}")
        continue
    entries = resp.get('result', {}).get('entries', [])
    returned = {e['key']: e for e in entries}
    for k in batch:
        h = hash_for_key[k]
        if k not in returned:
            not_found.append(h)
            continue
        try:
            wasm = extract_wasm(returned[k]['xdr'], h)
            (OUT_DIR / f'{h}.wasm').write_bytes(wasm)
            found.append((h, len(wasm)))
        except Exception as e:
            errors.append((h, str(e)))

print(f"\nFound {len(found)} / {len(hashes)}; not_found={len(not_found)}; parse_errors={len(errors)}")
for h, size in sorted(found):
    print(f"  OK    {h}  {size:>8d} bytes")
for h, err in errors:
    print(f"  ERR   {h}  {err}")
for h in not_found:
    print(f"  MISS  {h}")
