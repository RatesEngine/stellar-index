// Thin fetch wrapper for the Rates Engine API.
//
// Resolves the base URL from `NEXT_PUBLIC_API_BASE_URL` (set in
// `next.config.mjs`). Use this everywhere instead of constructing
// URLs by hand so the `<>` reveal can introspect every request.

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

export type RequestExample = {
  method: 'GET' | 'POST';
  url: string;
  headers?: Record<string, string>;
};

export function buildUrl(path: string, params?: Record<string, string | number | undefined>): string {
  const url = new URL(path.startsWith('/') ? path : `/${path}`, API_BASE_URL);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
  }
  return url.toString();
}

export async function apiGet<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
): Promise<T> {
  const res = await fetch(buildUrl(path, params), {
    headers: { Accept: 'application/json' },
    next: { revalidate: 60 },
  });
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText} on ${path}`);
  }
  return (await res.json()) as T;
}

// Helper for the <> reveal. Every panel exports a getRequestExample()
// that returns this shape; the reveal renders it as cURL + clickable URL.
export function asExample(
  path: string,
  params?: Record<string, string | number | undefined>,
): RequestExample {
  return { method: 'GET', url: buildUrl(path, params) };
}
