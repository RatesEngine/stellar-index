// API client for the dashboard. Every request includes
// `credentials: 'include'` so the host-only / parent-domain
// session cookie set by GET /v1/auth/callback rides along on
// XHRs to api.ratesengine.net.

const BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

export class ApiError extends Error {
  status: number;
  detail: string | undefined;
  constructor(status: number, message: string, detail?: string) {
    super(message);
    this.status = status;
    this.detail = detail;
  }
}

interface FetchOptions {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
}

export async function apiFetch<T>(
  path: string,
  opts: FetchOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
  };
  let body: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(`${BASE_URL}/v1${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body,
    credentials: 'include',
    signal: opts.signal,
  });

  if (!res.ok) {
    let detail: string | undefined;
    try {
      const errBody = (await res.json()) as { detail?: string };
      detail = errBody.detail;
    } catch {
      // problem+json bodies are best-effort; some 5xx come without one.
    }
    throw new ApiError(res.status, res.statusText, detail);
  }

  // 200 with no body → caller gets undefined-like.
  if (res.status === 204) return undefined as unknown as T;
  return (await res.json()) as T;
}

// ─── Endpoints ───────────────────────────────────────────────────

export interface AccountMe {
  user: {
    id: string;
    email: string;
    display_name: string;
    role: string;
    is_staff: boolean;
  };
  account: {
    id: string;
    name: string;
    slug: string;
    tier: string;
    status: string;
  };
}

export async function fetchMe(signal?: AbortSignal): Promise<AccountMe> {
  return apiFetch<AccountMe>('/account/me', { signal });
}

export async function requestMagicLink(email: string): Promise<void> {
  await apiFetch<{ status: string }>('/auth/login', {
    method: 'POST',
    body: { email },
  });
}

export async function logout(): Promise<void> {
  await apiFetch<void>('/auth/logout', { method: 'POST' });
}
