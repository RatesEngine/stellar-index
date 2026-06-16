import type { Metadata } from 'next';
import { Activity, Boxes, Coins, Search } from 'lucide-react';

import {
  Badge,
  Button,
  ButtonLink,
  Callout,
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  Container,
  EmptyState,
  Field,
  Input,
  Mono,
  PageHeader,
  Section,
  SectionHeader,
  Select,
  Skeleton,
  Stat,
  StatCell,
  StatGrid,
  TBody,
  TR,
  Table,
  TableWrap,
  Td,
  Th,
  THead,
} from '@/components/ui';

export const metadata: Metadata = {
  title: 'Style guide — design system',
  robots: { index: false, follow: false },
};

const SWATCHES: { name: string; vars: { label: string; cls: string }[] }[] = [
  {
    name: 'Brand',
    // Literal class names — Tailwind's JIT can't see template-literal classes.
    vars: [
      { label: '50', cls: 'bg-brand-50' },
      { label: '100', cls: 'bg-brand-100' },
      { label: '200', cls: 'bg-brand-200' },
      { label: '300', cls: 'bg-brand-300' },
      { label: '400', cls: 'bg-brand-400' },
      { label: '500', cls: 'bg-brand-500' },
      { label: '600', cls: 'bg-brand-600' },
      { label: '700', cls: 'bg-brand-700' },
      { label: '800', cls: 'bg-brand-800' },
      { label: '900', cls: 'bg-brand-900' },
      { label: '950', cls: 'bg-brand-950' },
    ],
  },
  {
    name: 'Surface & line',
    vars: [
      { label: 'canvas', cls: 'bg-surface-canvas' },
      { label: 'surface', cls: 'bg-surface' },
      { label: 'muted', cls: 'bg-surface-muted' },
      { label: 'subtle', cls: 'bg-surface-subtle' },
      { label: 'line', cls: 'bg-line' },
      { label: 'line-strong', cls: 'bg-line-strong' },
    ],
  },
  {
    name: 'Ink',
    vars: [
      { label: 'ink', cls: 'bg-ink' },
      { label: 'body', cls: 'bg-ink-body' },
      { label: 'muted', cls: 'bg-ink-muted' },
      { label: 'faint', cls: 'bg-ink-faint' },
    ],
  },
  {
    name: 'Semantic',
    vars: [
      { label: 'up', cls: 'bg-up' },
      { label: 'down', cls: 'bg-down' },
      { label: 'ok', cls: 'bg-ok-500' },
      { label: 'warn', cls: 'bg-warn-500' },
      { label: 'bad', cls: 'bg-bad-500' },
    ],
  },
];

function Block({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-4">
      <SectionHeader title={title} />
      {children}
    </div>
  );
}

export default function StyleGuidePage() {
  return (
    <Container>
      <Section className="space-y-12">
        <PageHeader
          eyebrow="Design system"
          title="Style guide"
          description="The living reference for Stellar Index's light-mode design system — tokens, type, and every UI primitive. Build pages from these."
        />

        {/* Colour */}
        <Block title="Colour">
          <div className="grid gap-6 sm:grid-cols-2">
            {SWATCHES.map((g) => (
              <Card key={g.name}>
                <CardHeader title={g.name} />
                <CardBody className="flex flex-wrap gap-2">
                  {g.vars.map((v) => (
                    <div key={v.label} className="text-center">
                      <div className={`h-12 w-12 rounded-lg border border-line ${v.cls}`} />
                      <div className="mt-1 text-[11px] text-ink-muted">{v.label}</div>
                    </div>
                  ))}
                </CardBody>
              </Card>
            ))}
          </div>
        </Block>

        {/* Type */}
        <Block title="Typography">
          <Card>
            <CardBody className="space-y-3">
              <p className="text-display font-semibold text-ink">Display 56</p>
              <p className="text-h1 font-semibold text-ink">Heading 1 · 32</p>
              <p className="text-h2 font-semibold text-ink">Heading 2 · 24</p>
              <p className="text-h3 font-semibold text-ink">Heading 3 · 20</p>
              <p className="text-base text-ink-body">
                Body — Inter at 16px. The quick brown fox jumps over the lazy dog. 0123456789.
              </p>
              <p className="text-sm text-ink-muted">Muted small — secondary copy and labels.</p>
              <p className="font-mono text-sm tnum text-ink-body">
                Mono / tabular — 1,234,567.89 · GA5Z…KZVN · 0xdAC1…1ec7
              </p>
            </CardBody>
          </Card>
        </Block>

        {/* Buttons */}
        <Block title="Buttons">
          <Card>
            <CardBody className="flex flex-wrap items-center gap-3">
              <Button variant="primary">Primary</Button>
              <Button variant="secondary">Secondary</Button>
              <Button variant="subtle">Subtle</Button>
              <Button variant="ghost">Ghost</Button>
              <Button variant="danger">Danger</Button>
              <Button disabled>Disabled</Button>
              <ButtonLink href="#" variant="primary" size="sm">
                Small link
              </ButtonLink>
              <ButtonLink href="#" variant="secondary" size="lg">
                Large link
              </ButtonLink>
            </CardBody>
          </Card>
        </Block>

        {/* Badges */}
        <Block title="Badges">
          <Card>
            <CardBody className="flex flex-wrap items-center gap-2">
              <Badge>neutral</Badge>
              <Badge tone="brand">brand</Badge>
              <Badge tone="ok" dot>
                operational
              </Badge>
              <Badge tone="up">+2.41%</Badge>
              <Badge tone="down">−1.08%</Badge>
              <Badge tone="warn" dot>
                degraded
              </Badge>
              <Badge tone="bad" dot>
                outage
              </Badge>
            </CardBody>
          </Card>
        </Block>

        {/* Stats */}
        <Block title="Metrics">
          <StatGrid cols={4}>
            <StatCell>
              <Stat label="24h volume" value="$4.21M" sub="+12.4% vs prev" icon={<Coins className="h-3.5 w-3.5" />} />
            </StatCell>
            <StatCell>
              <Stat label="Active markets" value="318" icon={<Activity className="h-3.5 w-3.5" />} />
            </StatCell>
            <StatCell>
              <Stat label="Protocols" value="17" icon={<Boxes className="h-3.5 w-3.5" />} />
            </StatCell>
            <StatCell>
              <Stat label="Latest ledger" value="62,829,354" />
            </StatCell>
          </StatGrid>
        </Block>

        {/* Table */}
        <Block title="Table">
          <TableWrap>
            <Table>
              <THead>
                <tr>
                  <Th>Asset</Th>
                  <Th align="right">Price</Th>
                  <Th align="right">24h</Th>
                  <Th align="right">Volume</Th>
                </tr>
              </THead>
              <TBody>
                {[
                  ['XLM', '$0.1142', '+1.2%', '$1.91M'],
                  ['USDC', '$1.0001', '+0.0%', '$1.13M'],
                  ['AQUA', '$0.0038', '−3.4%', '$214K'],
                ].map((r) => (
                  <TR key={r[0]}>
                    <Td className="font-medium text-ink">{r[0]}</Td>
                    <Td align="right">{r[1]}</Td>
                    <Td align="right" className={r[2].startsWith('−') ? 'text-down' : 'text-up'}>
                      {r[2]}
                    </Td>
                    <Td align="right">{r[3]}</Td>
                  </TR>
                ))}
              </TBody>
            </Table>
          </TableWrap>
        </Block>

        {/* Forms */}
        <Block title="Forms">
          <Card>
            <CardBody className="grid max-w-xl gap-4">
              <Field label="API key name" htmlFor="sg-name" hint="A label to recognise this key.">
                <Input id="sg-name" placeholder="Production server" />
              </Field>
              <Field label="Plan" htmlFor="sg-plan">
                <Select id="sg-plan" defaultValue="pro">
                  <option value="free">Free</option>
                  <option value="pro">Pro</option>
                  <option value="scale">Scale</option>
                </Select>
              </Field>
              <Field label="Email" htmlFor="sg-email" error="Enter a valid email address." required>
                <Input id="sg-email" defaultValue="not-an-email" />
              </Field>
            </CardBody>
          </Card>
        </Block>

        {/* Cards / callouts / states */}
        <Block title="Cards, callouts & states">
          <div className="grid gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader
                eyebrow="Protocol"
                title="Soroswap"
                description="AMM · 4 factories"
                actions={<Badge tone="ok" dot>live</Badge>}
              />
              <CardBody>
                <Mono value="CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM" truncate />
              </CardBody>
              <CardFooter>
                <span>Updated 2m ago</span>
                <ButtonLink href="#" variant="ghost" size="sm">
                  View →
                </ButtonLink>
              </CardFooter>
            </Card>
            <div className="space-y-4">
              <Callout tone="info" title="Heads up">
                Reference prices cross-check on-chain quotes; they are not primary feeds.
              </Callout>
              <Callout tone="warn">A source is reporting degraded freshness.</Callout>
              <Skeleton className="h-10 w-full" />
              <EmptyState
                icon={<Search className="h-5 w-5" />}
                title="No results"
                description="Try a different asset code, issuer, or contract id."
              />
            </div>
          </div>
        </Block>
      </Section>
    </Container>
  );
}
