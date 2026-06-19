// Authenticated account API for the in-site customer dashboard
// (/account/*). Every request sends `credentials: 'include'` so the
// magic-link session cookie set by GET /v1/auth/callback rides along
// — the same cookie `useMe()` relies on. These hit the dashboard
// key-management surface (`/v1/dashboard/keys`), the richer
// Postgres-backed store that exposes name / description / revoked_at /
// last_used_at, which the ported pages render.
//
// Kept separate from `src/api/client.ts` (the public, non-credentialed
// `apiGet`) precisely because account calls MUST be credentialed and
// the public CORS path deliberately is not — see the long note in
// `src/api/hooks.ts::useMe`.

import { API_BASE_URL } from './client';

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

async function accountFetch<T>(
  path: string,
  opts: FetchOptions = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  let body: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(`${API_BASE_URL}/v1${path}`, {
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

  if (res.status === 204) return undefined as unknown as T;
  return (await res.json()) as T;
}

// ─── Auth ──────────────────────────────────────────────────────────

/** POST /v1/auth/logout — clears the magic-link session cookie. */
export async function logout(): Promise<void> {
  await accountFetch<void>('/auth/logout', { method: 'POST' });
}

/**
 * POST /v1/auth/verify-code — exchange the 6-digit email code for a
 * session. Credentialed (via accountFetch) so the Set-Cookie sticks;
 * the caller does a full-page navigation afterwards so the cookie-
 * authed dashboard loads. Throws ApiError on a wrong/expired code
 * (status 400) — callers surface `.detail`.
 */
export async function verifyCode(email: string, code: string): Promise<void> {
  await accountFetch<{ status: string }>('/auth/verify-code', {
    method: 'POST',
    body: { email, code },
  });
}

// ─── Keys ──────────────────────────────────────────────────────────

// APIKey mirrors the `/v1/dashboard/keys` keyDTO wire shape
// (internal/api/v1/dashboardkeys/handlers.go). Optional fields are
// omitted by the server when zero-valued.
export interface APIKey {
  id: string;
  name: string;
  description?: string;
  key_prefix: string;
  tier: string;
  rate_limit_per_min: number;
  monthly_quota?: number;
  usage_alert_threshold_pct?: number;
  ip_allowlist?: string[];
  referer_allowlist?: string[];
  expires_at?: string;
  revoked_at?: string;
  revoked_reason?: string;
  last_used_at?: string;
  created_at: string;
}

interface KeyListResponse {
  keys: APIKey[];
}

export interface CreateKeyRequest {
  name: string;
  description?: string;
  rate_limit_per_min?: number;
  monthly_quota?: number;
  ip_allowlist?: string[];
  referer_allowlist?: string[];
  expires_at?: string;
  usage_alert_threshold_pct?: number;
}

export interface CreateKeyResponse {
  plaintext: string;
  key: APIKey;
}

/** GET /v1/dashboard/keys — every key on the session's account. */
export async function listKeys(signal?: AbortSignal): Promise<APIKey[]> {
  const r = await accountFetch<KeyListResponse>('/dashboard/keys', { signal });
  return r.keys ?? [];
}

/** POST /v1/dashboard/keys — mint a key; plaintext returned once. */
export async function createKey(
  body: CreateKeyRequest,
): Promise<CreateKeyResponse> {
  return accountFetch<CreateKeyResponse>('/dashboard/keys', {
    method: 'POST',
    body,
  });
}

/** DELETE /v1/dashboard/keys/{id} — soft-revoke (idempotent). */
export async function revokeKey(id: string): Promise<void> {
  await accountFetch<void>(`/dashboard/keys/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ─── Usage ─────────────────────────────────────────────────────────

// UsageRow mirrors the /v1/account/usage wire shape: per-day request
// counts written by the UsageTracker middleware. `errors` / `throttled`
// are reserved server-side and stay zero today, so we surface only the
// fields the API populates.
export interface UsageRow {
  date: string; // YYYY-MM-DD
  requests: number;
}

/**
 * GET /v1/account/usage — trailing 30-day per-day request counts for
 * the authenticated account. Returns an empty list when the usage
 * backend isn't wired (Redis-less deployment) rather than erroring,
 * so callers can treat [] as "no usage reported".
 */
export async function fetchUsage(signal?: AbortSignal): Promise<UsageRow[]> {
  const env = await accountFetch<{ data: UsageRow[] }>('/account/usage', {
    signal,
  });
  return env.data ?? [];
}

// ─── Staff: customer look-up ───────────────────────────────────────

// Mirrors AdminLookupResponse (internal/api/v1/dashboardauth/handlers_admin.go).
export interface AdminAccountView {
  id: string;
  name: string;
  slug: string;
  tier: string;
  status: string;
  billing_email?: string;
  created_at?: string;
  suspended_reason?: string;
  rate_limit_per_min_override?: number;
  monthly_request_quota_override?: number;
}
export interface AdminUserView {
  id: string;
  email: string;
  display_name?: string;
  role: string;
  is_staff: boolean;
  email_verified: boolean;
  last_login_at?: string;
}
export interface AdminLookupResult {
  account: AdminAccountView;
  users: AdminUserView[];
}

/**
 * GET /v1/account/admin/lookup?email=|slug= — staff customer look-up.
 * Resolves an account by a user's email or by account slug and returns
 * its tier/status plus the users on it. Staff-only (403 for non-staff);
 * throws ApiError (404 when nothing matches, surfaced via `.detail`).
 */
export async function adminLookup(
  query: { email?: string; slug?: string },
  signal?: AbortSignal,
): Promise<AdminLookupResult> {
  const params = new URLSearchParams();
  if (query.email) params.set('email', query.email);
  if (query.slug) params.set('slug', query.slug);
  return accountFetch<AdminLookupResult>(
    `/account/admin/lookup?${params.toString()}`,
    { signal },
  );
}
