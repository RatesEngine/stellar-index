'use client';

import { useQuery } from '@tanstack/react-query';
import { BarChart3, Gauge, KeyRound } from 'lucide-react';

import {
  ApiError,
  fetchUsage,
  listKeys,
  type APIKey,
  type UsageRow,
} from '@/api/account';
import type { MeResponse } from '@/api/hooks';
import {
  Badge,
  ButtonLink,
  Callout,
  Card,
  CardBody,
  CardHeader,
  Container,
  EmptyState,
  PageHeader,
  Section,
  Skeleton,
  Stat,
  StatCell,
  StatGrid,
  Table,
  TableWrap,
  TBody,
  Td,
  Th,
  THead,
  TR,
} from '@/components/ui';
import {
  fmtDate,
  fmtDateTime,
  fmtInt,
  fmtRelative,
  tierCeiling,
  tierLabel,
} from '@/lib/account-format';

import { AccountGate } from '../AccountGate';

/**
 * /dashboard/usage — per-key activity + rate-limit headroom + the daily
 * request counts the API exposes via /v1/account/usage. Detailed
 * per-endpoint analytics are honestly flagged as not-yet-shipped.
 */
export default function UsagePage() {
  return <AccountGate>{(me) => <UsageBody me={me} />}</AccountGate>;
}

function UsageBody({ me }: { me: MeResponse }) {
  const keysQuery = useQuery<APIKey[], Error>({
    queryKey: ['dashboard', 'keys'],
    queryFn: ({ signal }) => listKeys(signal),
  });
  // Daily request totals are best-effort — swallow failures to [] so the
  // page never blocks on the usage backend (matches prior behavior).
  const usageQuery = useQuery<UsageRow[], Error>({
    queryKey: ['dashboard', 'usage'],
    queryFn: async ({ signal }) => {
      try {
        return await fetchUsage(signal);
      } catch {
        return [];
      }
    },
  });

  const keys = keysQuery.data ?? null;
  const usage = usageQuery.data ?? null;
  const error = keysQuery.error
    ? keysQuery.error instanceof ApiError
      ? (keysQuery.error.detail ?? keysQuery.error.message)
      : 'Failed to load usage'
    : null;

  const active = keys?.filter((k) => !k.revoked_at) ?? [];

  return (
    <Container>
      <Section className="space-y-6">
        <PageHeader
          eyebrow="Activity"
          title="Usage"
          description="Your daily request volume, per-key activity, and rate-limit headroom."
        />

        {error && (
          <Callout tone="bad" title="Couldn't load usage">
            {error}
          </Callout>
        )}

        <HeadroomStrip me={me} keys={keys} />

        <DailyRequests usage={usage} />

        <Card>
          <CardHeader
            title="Per-key activity"
            description="The most recent request seen on each key, with its configured limits."
          />
          {keys === null && !error ? (
            <CardBody className="space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </CardBody>
          ) : active.length === 0 ? (
            <CardBody>
              <EmptyState
                icon={<KeyRound className="h-5 w-5" />}
                title="No active keys"
                description="Create an API key and start making requests to see activity here."
                action={
                  <ButtonLink href="/dashboard/keys">Go to API keys</ButtonLink>
                }
              />
            </CardBody>
          ) : (
            <PerKeyTable keys={active} />
          )}
        </Card>

        <Callout tone="info" title="Per-endpoint analytics are on the way">
          Error rates and per-endpoint breakdowns ship once the usage pipeline
          (Redis stream → Timescale worker) is fully wired through. Until then
          this page shows what the API exposes today: daily request totals, the
          last request seen per key, and your tier headroom.
        </Callout>
      </Section>
    </Container>
  );
}

function HeadroomStrip({
  me,
  keys,
}: {
  me: MeResponse;
  keys: APIKey[] | null;
}) {
  if (keys === null) {
    return (
      <StatGrid cols={3}>
        {Array.from({ length: 3 }).map((_, i) => (
          <StatCell key={i}>
            <Skeleton className="h-3 w-24" />
            <Skeleton className="mt-2 h-8 w-16" />
          </StatCell>
        ))}
      </StatGrid>
    );
  }

  const tier = me.account?.tier ?? me.tier;
  const active = keys.filter((k) => !k.revoked_at);
  const ceiling = tierCeiling(tier);
  const totalProvisioned = active.reduce(
    (sum, k) => sum + (k.rate_limit_per_min || 0),
    0,
  );

  return (
    <StatGrid cols={3}>
      <StatCell>
        <Stat
          icon={<Gauge className="h-3.5 w-3.5" />}
          label="Tier ceiling"
          value={ceiling !== null ? `${fmtInt(ceiling)}` : '—'}
          sub={`${tierLabel(tier)} · req/min`}
        />
      </StatCell>
      <StatCell>
        <Stat
          icon={<KeyRound className="h-3.5 w-3.5" />}
          label="Active keys"
          value={fmtInt(active.length)}
          sub="authenticating now"
        />
      </StatCell>
      <StatCell>
        <Stat
          icon={<BarChart3 className="h-3.5 w-3.5" />}
          label="Provisioned"
          value={`${fmtInt(totalProvisioned)}`}
          sub="req/min across keys"
        />
      </StatCell>
    </StatGrid>
  );
}

function DailyRequests({ usage }: { usage: UsageRow[] | null }) {
  const total = (usage ?? []).reduce((s, r) => s + (r.requests || 0), 0);
  return (
    <Card>
      <CardHeader
        title="Requests (last 30 days)"
        description="Per-account daily request counts recorded by the API."
        actions={
          usage && usage.length > 0 ? (
            <span className="tnum font-mono text-sm text-ink-muted">
              {fmtInt(total)} total
            </span>
          ) : undefined
        }
      />
      <CardBody>
        {usage === null ? (
          <Skeleton className="h-12 w-full" />
        ) : usage.length === 0 ? (
          <p className="text-sm text-ink-muted">
            No tracked requests yet for this account in the last 30 days.
            Requests count against the per-account daily window once you start
            calling the API with one of your keys.
          </p>
        ) : (
          <UsageBars rows={usage} />
        )}
      </CardBody>
    </Card>
  );
}

function UsageBars({ rows }: { rows: UsageRow[] }) {
  const max = Math.max(...rows.map((r) => r.requests || 0), 1);
  return (
    <div className="flex items-end gap-1">
      {rows.map((r) => {
        const h = Math.max(3, (r.requests / max) * 64);
        return (
          <div
            key={r.date}
            title={`${fmtDate(r.date)}: ${fmtInt(r.requests)} requests`}
            className="flex flex-1 flex-col items-center justify-end"
          >
            <div
              className="w-full rounded-xs bg-brand-500/70"
              style={{ height: `${h}px` }}
            />
          </div>
        );
      })}
    </div>
  );
}

function PerKeyTable({ keys }: { keys: APIKey[] }) {
  return (
    <TableWrap className="rounded-t-none border-0 border-t">
      <Table>
        <THead>
          <tr>
            <Th>Key</Th>
            <Th align="right">Rate limit</Th>
            <Th align="right">Monthly quota</Th>
            <Th>Last request</Th>
          </tr>
        </THead>
        <TBody>
          {keys.map((k) => (
            <TR key={k.id}>
              <Td>
                <div className="font-medium text-ink">{k.name}</div>
                <code className="mt-0.5 block font-mono text-xs text-ink-muted">
                  {k.key_prefix}…
                </code>
              </Td>
              <Td align="right">{fmtInt(k.rate_limit_per_min)}/min</Td>
              <Td align="right">
                {k.monthly_quota ? (
                  fmtInt(k.monthly_quota)
                ) : (
                  <Badge tone="neutral">Unlimited</Badge>
                )}
              </Td>
              <Td>
                {k.last_used_at ? (
                  <span title={fmtDateTime(k.last_used_at)}>
                    {fmtRelative(k.last_used_at)}
                  </span>
                ) : (
                  <span className="text-ink-faint">No traffic yet</span>
                )}
              </Td>
            </TR>
          ))}
        </TBody>
      </Table>
    </TableWrap>
  );
}
