// URL-state helpers. Per the design principle "URL state is config",
// every reactive UI selection reads from + writes to query params.
//
// Server components: pass `searchParams` straight in.
// Client components: use `useSearchParams` from `next/navigation`.

export function parseAsOfLedger(searchParams: URLSearchParams | Record<string, string | string[] | undefined>): number | undefined {
  const raw = readParam(searchParams, 'as_of_ledger');
  if (!raw) return undefined;
  const n = parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

export function readParam(
  source: URLSearchParams | Record<string, string | string[] | undefined>,
  name: string,
): string | undefined {
  if (source instanceof URLSearchParams) {
    return source.get(name) ?? undefined;
  }
  const v = source[name];
  if (Array.isArray(v)) return v[0];
  return v ?? undefined;
}

export function readCsvParam(
  source: URLSearchParams | Record<string, string | string[] | undefined>,
  name: string,
): string[] {
  const raw = readParam(source, name);
  if (!raw) return [];
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}
