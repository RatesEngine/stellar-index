"""Extract function exports + interesting strings from each WASM
hash. Produces a per-hash markdown summary that's the seed for
'what does this WASM expose / what topic strings does it emit'."""
import json, subprocess, re
from pathlib import Path

DATA_DIR = Path('/tmp/r1-wasm-walk')
WASM_DIR = DATA_DIR / 'wasm-bytes'
OUT_DIR = DATA_DIR / 'disasm'
OUT_DIR.mkdir(exist_ok=True)

def objdump_section(wasm_path: Path, section: str) -> str:
    """Run wasm-objdump for a specific section."""
    return subprocess.run(
        ['wasm-objdump', '-j', section, '-x', str(wasm_path)],
        capture_output=True, text=True, timeout=30,
    ).stdout

def extract_exports(wasm_path: Path) -> list[str]:
    out = objdump_section(wasm_path, 'Export')
    return sorted({m.group(1) for m in re.finditer(r'-> "([^"]+)"', out)})

def extract_imports(wasm_path: Path) -> list[str]:
    out = objdump_section(wasm_path, 'Import')
    return sorted({f"{m.group(1)}.{m.group(2)}" for m in re.finditer(
        r'<- (\w+)\.(\w+)', out)})

def extract_strings(wasm_path: Path, min_len=3) -> list[str]:
    """Pull printable ASCII strings from the data section. Filters
    to ones that look like topic / function / error names (snake-
    case, length 3+, not pure punctuation)."""
    raw = wasm_path.read_bytes()
    runs = re.findall(rb'[\x20-\x7e]{%d,}' % min_len, raw)
    out = set()
    for r in runs:
        s = r.decode('ascii', errors='ignore')
        # filter to identifier-like or symbol-like
        if re.match(r'^[a-zA-Z_][a-zA-Z0-9_]*$', s):
            out.add(s)
    return sorted(out)

# Per-hash summary
summary = {}
for wasm_file in sorted(WASM_DIR.glob('*.wasm')):
    h = wasm_file.stem
    size = wasm_file.stat().st_size
    exports = extract_exports(wasm_file)
    imports = extract_imports(wasm_file)
    strings = extract_strings(wasm_file)
    # Heuristic: identify "topic-like" strings vs noise
    # Topic strings are typically: short (< 20 chars), lowercase, possibly with underscores
    topic_candidates = [s for s in strings if 2 < len(s) <= 30 and re.match(r'^[a-z][a-z0-9_]*$', s)]
    summary[h] = {
        'hash': h,
        'size_bytes': size,
        'export_count': len(exports),
        'import_count': len(imports),
        'exports': exports,
        'imports': imports,
        'all_strings_count': len(strings),
        'topic_like_strings': topic_candidates,
    }
    out = OUT_DIR / f'{h}.json'
    out.write_text(json.dumps(summary[h], indent=2))

# Top-level summary index
print(f"{'hash':16s}  {'size':>8s}  {'exports':>7s}  {'topic-strings':>13s}  example_exports")
for h in sorted(summary):
    s = summary[h]
    sample = ','.join(s['exports'][:3])
    print(f"{h[:16]}  {s['size_bytes']:>8d}  {s['export_count']:>7d}  {len(s['topic_like_strings']):>13d}  {sample[:60]}")
