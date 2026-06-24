'use client';

import { KeyRound, Loader2, Plus, Trash2 } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';

import {
  ApiError,
  createKey,
  listKeys,
  revokeKey,
  type APIKey,
  type CreateKeyResponse,
} from '@/api/account';
import type { MeResponse } from '@/api/hooks';
import {
  Badge,
  Button,
  Callout,
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  Container,
  CopyButton,
  EmptyState,
  Field,
  Input,
  PageHeader,
  Section,
  Skeleton,
  Table,
  TableWrap,
  TBody,
  Td,
  Textarea,
  Th,
  THead,
  TR,
} from '@/components/ui';
import {
  fmtDate,
  fmtInt,
  fmtRelative,
  tierCeiling,
} from '@/lib/account-format';

import { AccountGate } from '../AccountGate';

/**
 * /dashboard/keys — API-key management. Ported from the standalone
 * dashboard: a table of keys, a create flow that reveals the secret
 * once, and revoke-with-confirm. Reads/writes `/v1/dashboard/keys`
 * with the session cookie (credentials: include) via @/api/account.
 */
export default function KeysPage() {
  return <AccountGate>{(me) => <KeysBody me={me} />}</AccountGate>;
}

function KeysBody({ me }: { me: MeResponse }) {
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [newKey, setNewKey] = useState<CreateKeyResponse | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [creating, setCreating] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setError(null);
      setKeys(await listKeys());
    } catch (err) {
      setError(
        err instanceof ApiError
          ? (err.detail ?? err.message)
          : 'Failed to load keys',
      );
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function handleRevoke(key: APIKey) {
    if (
      !confirm(
        `Revoke "${key.name}"? Apps using it will stop authenticating immediately. This cannot be undone.`,
      )
    ) {
      return;
    }
    try {
      await revokeKey(key.id);
      setNotice(`Key "${key.name}" revoked.`);
      await refresh();
    } catch (err) {
      setError(
        err instanceof ApiError ? (err.detail ?? err.message) : 'Revoke failed',
      );
    }
  }

  const tier = me.account?.tier ?? me.tier;
  const active = keys?.filter((k) => !k.revoked_at) ?? [];

  return (
    <Container>
      <Section className="space-y-6">
        <PageHeader
          eyebrow="Credentials"
          title="API keys"
          description="Mint and manage the keys your apps use to authenticate against api.stellarindex.io."
          actions={
            !showForm && (
              <Button onClick={() => setShowForm(true)}>
                <Plus className="h-4 w-4" />
                New key
              </Button>
            )
          }
        />

        {notice && (
          <Callout tone="ok" title="Done">
            {notice}
          </Callout>
        )}

        {newKey && (
          <NewKeyReveal
            created={newKey}
            onDismiss={() => {
              setNewKey(null);
              void refresh();
            }}
          />
        )}

        {showForm && (
          <CreateKeyForm
            tier={tier}
            creating={creating}
            setCreating={setCreating}
            onError={setError}
            onCreated={(resp) => {
              setNewKey(resp);
              setNotice(null);
              setShowForm(false);
              void refresh();
            }}
            onCancel={() => setShowForm(false)}
          />
        )}

        {error && (
          <Callout tone="bad" title="Something went wrong">
            {error}
          </Callout>
        )}

        {keys === null && !error ? (
          <Card>
            <CardBody className="space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </CardBody>
          </Card>
        ) : keys && keys.length === 0 && !showForm ? (
          <EmptyState
            icon={<KeyRound className="h-5 w-5" />}
            title="No API keys yet"
            description="Mint your first key to start authenticating requests to the Stellar Index API."
            action={
              <Button onClick={() => setShowForm(true)}>
                <Plus className="h-4 w-4" />
                Create your first key
              </Button>
            }
          />
        ) : keys && keys.length > 0 ? (
          <KeysTable keys={keys} onRevoke={handleRevoke} />
        ) : null}

        {keys && keys.length > 0 && (
          <p className="text-xs text-ink-faint">
            {fmtInt(active.length)} active{' '}
            {active.length === 1 ? 'key' : 'keys'}
            {keys.length > active.length &&
              `, ${fmtInt(keys.length - active.length)} revoked`}
            . Revoked keys are kept for your audit trail and stop working
            immediately.
          </p>
        )}
      </Section>
    </Container>
  );
}

// ─── Reveal banner (secret shown once) ─────────────────────────────

function NewKeyReveal({
  created,
  onDismiss,
}: {
  created: CreateKeyResponse;
  onDismiss: () => void;
}) {
  return (
    <Card className="border-brand-200 bg-brand-50/60">
      <CardHeader
        className="border-brand-100"
        eyebrow="New key created"
        title="Save this key now — you won't see it again"
        description="We store only a hash. If you lose the plaintext, revoke this key and mint a new one."
      />
      <CardBody className="space-y-3">
        <div className="flex items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2.5">
          <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-ink">
            {created.plaintext}
          </code>
          <CopyButton value={created.plaintext} className="h-7 w-7" />
        </div>
        <p className="text-xs text-ink-muted">
          Stored as{' '}
          <code className="rounded bg-surface-subtle px-1 py-0.5 font-mono">
            {created.key.key_prefix}…
          </code>
          {' · '}
          Send it as the{' '}
          <code className="rounded bg-surface-subtle px-1 py-0.5 font-mono">
            X-API-Key
          </code>{' '}
          header on every request.
        </p>
      </CardBody>
      <CardFooter className="justify-end">
        <Button variant="primary" size="sm" onClick={onDismiss}>
          I&apos;ve saved it
        </Button>
      </CardFooter>
    </Card>
  );
}

// ─── Create form ───────────────────────────────────────────────────

function CreateKeyForm({
  tier,
  creating,
  setCreating,
  onCreated,
  onCancel,
  onError,
}: {
  tier: string | undefined;
  creating: boolean;
  setCreating: (b: boolean) => void;
  onCreated: (r: CreateKeyResponse) => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const ceiling = tierCeiling(tier);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [rateLimit, setRateLimit] = useState(ceiling ?? 1000);
  const [ipAllowlist, setIpAllowlist] = useState('');
  const [nameError, setNameError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (creating) return;
    if (!name.trim()) {
      setNameError('Give the key a name so you can find it later.');
      return;
    }
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
      onError(
        err instanceof ApiError ? (err.detail ?? err.message) : 'Create failed',
      );
    } finally {
      setCreating(false);
    }
  }

  return (
    <Card>
      <CardHeader
        title="New API key"
        description="Generate a credential for one app or environment."
      />
      <form onSubmit={submit}>
        <CardBody className="space-y-5">
          <Field
            label="Name"
            htmlFor="key-name"
            required
            hint="Helps you identify this key in the list later."
            error={nameError ?? undefined}
          >
            <Input
              id="key-name"
              autoFocus
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (nameError) setNameError(null);
              }}
              placeholder="production-web"
            />
          </Field>

          <Field
            label="Description"
            htmlFor="key-desc"
            hint="Optional — what uses this key."
          >
            <Input
              id="key-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Production web app at example.com"
            />
          </Field>

          <Field
            label="Rate limit (requests / minute)"
            htmlFor="key-rate"
            hint={
              ceiling !== null
                ? `Capped to your ${tier} tier ceiling of ${fmtInt(ceiling)}/min. Higher values clamp on save.`
                : 'Capped to your account tier ceiling. Higher values clamp on save.'
            }
          >
            <Input
              id="key-rate"
              type="number"
              min={1}
              max={ceiling ?? 100000}
              value={rateLimit}
              onChange={(e) => setRateLimit(Number(e.target.value) || 1)}
              className="tnum max-w-[12rem]"
            />
          </Field>

          <Field
            label="IP allowlist"
            htmlFor="key-ips"
            hint="Optional — one per line. CIDR (203.0.113.0/24) or bare IP. Leave empty to allow any source."
          >
            <Textarea
              id="key-ips"
              rows={3}
              value={ipAllowlist}
              onChange={(e) => setIpAllowlist(e.target.value)}
              className="font-mono text-[13px]"
              placeholder={'203.0.113.0/24\n198.51.100.7'}
            />
          </Field>
        </CardBody>
        <CardFooter className="justify-end gap-2">
          <Button
            type="button"
            variant="secondary"
            onClick={onCancel}
            disabled={creating}
          >
            Cancel
          </Button>
          <Button type="submit" disabled={creating}>
            {creating && <Loader2 className="h-4 w-4 animate-spin" />}
            {creating ? 'Creating…' : 'Create key'}
          </Button>
        </CardFooter>
      </form>
    </Card>
  );
}

// ─── Keys table ────────────────────────────────────────────────────

function KeysTable({
  keys,
  onRevoke,
}: {
  keys: APIKey[];
  onRevoke: (k: APIKey) => void;
}) {
  return (
    <TableWrap>
      <Table>
        <THead>
          <tr>
            <Th>Name</Th>
            <Th>Key</Th>
            <Th align="right">Rate limit</Th>
            <Th>Created</Th>
            <Th>Last used</Th>
            <Th>Status</Th>
            <Th align="right">Actions</Th>
          </tr>
        </THead>
        <TBody>
          {keys.map((k) => {
            const revoked = Boolean(k.revoked_at);
            return (
              <TR key={k.id} className={revoked ? 'opacity-60' : undefined}>
                <Td>
                  <div className="font-medium text-ink">{k.name}</div>
                  {k.description && (
                    <div className="mt-0.5 text-xs text-ink-muted">
                      {k.description}
                    </div>
                  )}
                </Td>
                <Td>
                  <span className="inline-flex items-center gap-1.5">
                    <code className="font-mono text-[13px] text-ink-body">
                      {k.key_prefix}…
                    </code>
                    <CopyButton value={k.key_prefix} />
                  </span>
                </Td>
                <Td align="right">{fmtInt(k.rate_limit_per_min)}/min</Td>
                <Td>{fmtDate(k.created_at)}</Td>
                <Td>
                  <span title={k.last_used_at ?? undefined}>
                    {k.last_used_at ? fmtRelative(k.last_used_at) : '—'}
                  </span>
                </Td>
                <Td>
                  {revoked ? (
                    <Badge tone="down" dot>
                      Revoked
                    </Badge>
                  ) : k.expires_at && new Date(k.expires_at) < new Date() ? (
                    <Badge tone="warn" dot>
                      Expired
                    </Badge>
                  ) : (
                    <Badge tone="ok" dot>
                      Active
                    </Badge>
                  )}
                </Td>
                <Td align="right">
                  {!revoked && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-ink-muted hover:bg-bad-50 hover:text-bad-700"
                      onClick={() => onRevoke(k)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      Revoke
                    </Button>
                  )}
                </Td>
              </TR>
            );
          })}
        </TBody>
      </Table>
    </TableWrap>
  );
}
