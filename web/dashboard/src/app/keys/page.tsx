'use client';

import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Copy, Trash2, AlertCircle, KeyRound } from 'lucide-react';
import { AuthedRoute } from '@/components/AuthedRoute';
import {
  ApiError,
  type APIKey,
  type CreateKeyResponse,
  createKey,
  listKeys,
  revokeKey,
} from '@/lib/api';
import { cn } from '@/lib/cn';

export default function KeysPage() {
  return (
    <AuthedRoute>
      <KeysBody />
    </AuthedRoute>
  );
}

function KeysBody() {
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [newKey, setNewKey] = useState<CreateKeyResponse | null>(null);
  const [creating, setCreating] = useState(false);
  const [showForm, setShowForm] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setError(null);
      setKeys(await listKeys());
    } catch (err) {
      setError(err instanceof ApiError ? (err.detail ?? err.message) : 'Failed to load keys');
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function handleRevoke(id: string) {
    if (!confirm('Revoke this key? Apps using it will stop authenticating immediately.')) {
      return;
    }
    try {
      await revokeKey(id);
      toast.success('Key revoked');
      await refresh();
    } catch (err) {
      toast.error(err instanceof ApiError ? (err.detail ?? err.message) : 'Revoke failed');
    }
  }

  return (
    <div className="space-y-6">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-ink">
            API keys
          </h1>
          <p className="mt-1 text-sm text-ink-muted">
            Mint and manage the keys your apps use to authenticate against
            api.ratesengine.net.
          </p>
        </div>
        {!showForm && !newKey && (
          <button
            onClick={() => setShowForm(true)}
            className="rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-brand-900"
          >
            New key
          </button>
        )}
      </header>

      {newKey && <NewKeyBanner created={newKey} onDismiss={() => { setNewKey(null); void refresh(); }} />}

      {showForm && (
        <CreateKeyForm
          onCreated={(resp) => {
            setNewKey(resp);
            setShowForm(false);
          }}
          onCancel={() => setShowForm(false)}
          creating={creating}
          setCreating={setCreating}
        />
      )}

      {error && (
        <div className="flex items-start gap-3 rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-900">
          <AlertCircle className="mt-0.5 h-4 w-4 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {keys === null && !error && (
        <div className="rounded-md border border-surface-line bg-surface p-8 text-center text-sm text-ink-faint">
          Loading…
        </div>
      )}

      {keys && keys.length === 0 && !showForm && !newKey && (
        <div className="rounded-md border border-dashed border-surface-line bg-surface p-12 text-center">
          <KeyRound className="mx-auto h-8 w-8 text-ink-faint" />
          <p className="mt-3 text-sm text-ink-muted">
            You haven&apos;t minted a key yet.
          </p>
        </div>
      )}

      {keys && keys.length > 0 && <KeysTable keys={keys} onRevoke={handleRevoke} />}

      <p className="text-xs text-ink-faint">
        Keys minted here authenticate against the API once the platform-store
        cutover ships in the next slice. Operators on Phase&nbsp;1 dev should
        continue to mint via{' '}
        <code className="rounded bg-surface-subtle px-1 py-0.5">
          POST /v1/signup
        </code>{' '}
        until then.
      </p>
    </div>
  );
}

function NewKeyBanner({
  created,
  onDismiss,
}: {
  created: CreateKeyResponse;
  onDismiss: () => void;
}) {
  async function copy() {
    try {
      await navigator.clipboard.writeText(created.plaintext);
      toast.success('Copied');
    } catch {
      toast.error('Copy failed — select and copy manually');
    }
  }
  return (
    <div className="rounded-md border border-brand-500/40 bg-brand-50 p-5">
      <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-brand-900">
        <KeyRound className="h-4 w-4" />
        Save this key now — you won&apos;t see it again.
      </div>
      <div className="flex items-center gap-2">
        <code className="flex-1 break-all rounded bg-white px-3 py-2 font-mono text-sm">
          {created.plaintext}
        </code>
        <button
          onClick={copy}
          className="flex items-center gap-1.5 rounded-md border border-surface-line bg-white px-3 py-2 text-sm hover:bg-surface-subtle"
          title="Copy to clipboard"
        >
          <Copy className="h-4 w-4" />
          Copy
        </button>
      </div>
      <p className="mt-3 text-xs text-brand-900/80">
        Stored as <code>{created.key.key_prefix}…</code>. The dashboard never
        re-displays the full plaintext after this. If you lose it, revoke this
        key and mint a new one.
      </p>
      <div className="mt-4 flex justify-end">
        <button
          onClick={onDismiss}
          className="text-sm font-medium text-brand-600 hover:text-brand-900"
        >
          Done
        </button>
      </div>
    </div>
  );
}

function CreateKeyForm({
  onCreated,
  onCancel,
  creating,
  setCreating,
}: {
  onCreated: (r: CreateKeyResponse) => void;
  onCancel: () => void;
  creating: boolean;
  setCreating: (b: boolean) => void;
}) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [rateLimit, setRateLimit] = useState(1000);
  const [ipAllowlist, setIpAllowlist] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim() || creating) return;
    setCreating(true);
    try {
      const ipList = ipAllowlist
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      const resp = await createKey({
        name: name.trim(),
        description: description.trim() || undefined,
        rate_limit_per_min: rateLimit,
        ip_allowlist: ipList.length ? ipList : undefined,
      });
      onCreated(resp);
    } catch (err) {
      toast.error(err instanceof ApiError ? (err.detail ?? err.message) : 'Create failed');
    } finally {
      setCreating(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      className="space-y-4 rounded-md border border-surface-line bg-surface p-6"
    >
      <h2 className="text-base font-semibold text-ink">New API key</h2>
      <Field label="Name" hint="Helps you identify this key in the list later.">
        <input
          autoFocus
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="input"
          placeholder="production-web"
        />
      </Field>
      <Field label="Description (optional)">
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="input"
          placeholder="Used by the production web app at example.com"
        />
      </Field>
      <Field
        label="Rate limit (requests / minute)"
        hint="Capped to your account tier — Free 60, Starter 1000, Pro 10000, Business 60000, Enterprise 100000. Higher values silently clamp to the tier ceiling on save."
      >
        <input
          type="number"
          min={1}
          max={100000}
          value={rateLimit}
          onChange={(e) => setRateLimit(Number(e.target.value) || 1000)}
          className="input"
        />
      </Field>
      <Field
        label="IP allowlist (optional)"
        hint="One per line. CIDR (203.0.113.0/24) or bare IP."
      >
        <textarea
          rows={3}
          value={ipAllowlist}
          onChange={(e) => setIpAllowlist(e.target.value)}
          className="input font-mono"
        />
      </Field>
      <div className="flex justify-end gap-2 pt-2">
        <button
          type="button"
          onClick={onCancel}
          disabled={creating}
          className="rounded-md border border-surface-line bg-surface px-4 py-2 text-sm hover:bg-surface-subtle"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={creating}
          className="rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-900 disabled:opacity-60"
        >
          {creating ? 'Creating…' : 'Create key'}
        </button>
      </div>
      <style jsx>{`
        :global(.input) {
          width: 100%;
          border-radius: 0.375rem;
          border: 1px solid #e2e8f0;
          padding: 0.5rem 0.75rem;
          font-size: 0.875rem;
          background: white;
        }
        :global(.input:focus) {
          outline: none;
          border-color: #0ea5e9;
          box-shadow: 0 0 0 3px rgba(14, 165, 233, 0.15);
        }
      `}</style>
    </form>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-sm font-medium text-ink">
        {label}
      </span>
      {children}
      {hint && <span className="mt-1 block text-xs text-ink-faint">{hint}</span>}
    </label>
  );
}

function KeysTable({
  keys,
  onRevoke,
}: {
  keys: APIKey[];
  onRevoke: (id: string) => void;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-surface-line bg-surface">
      <table className="w-full text-sm">
        <thead className="bg-surface-subtle text-left text-xs uppercase tracking-wide text-ink-faint">
          <tr>
            <th className="px-5 py-3 font-medium">Name</th>
            <th className="px-5 py-3 font-medium">Prefix</th>
            <th className="px-5 py-3 font-medium">Rate limit</th>
            <th className="px-5 py-3 font-medium">Status</th>
            <th className="px-5 py-3 font-medium">Last used</th>
            <th className="px-5 py-3" />
          </tr>
        </thead>
        <tbody className="divide-y divide-surface-line">
          {keys.map((k) => (
            <tr key={k.id} className={cn(k.revoked_at && 'opacity-50')}>
              <td className="px-5 py-3">
                <div className="font-medium text-ink">{k.name}</div>
                {k.description && (
                  <div className="text-xs text-ink-muted">{k.description}</div>
                )}
              </td>
              <td className="px-5 py-3 font-mono text-xs text-ink-muted">
                {k.key_prefix}…
              </td>
              <td className="px-5 py-3 text-ink-muted">
                {k.rate_limit_per_min}/min
              </td>
              <td className="px-5 py-3">
                {k.revoked_at ? (
                  <span className="rounded bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700">
                    Revoked
                  </span>
                ) : (
                  <span className="rounded bg-emerald-50 px-2 py-0.5 text-xs font-medium text-emerald-700">
                    Active
                  </span>
                )}
              </td>
              <td className="px-5 py-3 text-xs text-ink-muted">
                {k.last_used_at
                  ? new Date(k.last_used_at).toLocaleString()
                  : '—'}
              </td>
              <td className="px-5 py-3 text-right">
                {!k.revoked_at && (
                  <button
                    onClick={() => onRevoke(k.id)}
                    className="inline-flex items-center gap-1 rounded-md border border-surface-line px-2 py-1 text-xs text-ink-muted hover:bg-red-50 hover:text-red-700"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    Revoke
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
