import {
  Activity,
  ArrowLeftRight,
  BadgeCheck,
  BarChart3,
  Blocks,
  BookOpen,
  Boxes,
  Building2,
  Code2,
  Coins,
  ExternalLink,
  GitCompare,
  Landmark,
  LayoutDashboard,
  Radio,
  Search,
  Share2,
  Tag,
  TrendingUp,
  Wallet,
  Zap,
  type LucideIcon,
} from 'lucide-react';

import { cn } from '@/lib/cn';

// The status page lives on a subdomain but wears the main console shell so it
// reads as part of the product. Nav links are absolute to the main site;
// "Status" is the current page (active). MAIN can be overridden for local dev.
const MAIN = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://stellarindex.io';

type NavItem = {
  href: string;
  label: string;
  icon: LucideIcon;
  external?: boolean;
  active?: boolean;
};
type NavGroup = { title?: string; items: NavItem[] };

const NAV: NavGroup[] = [
  { items: [{ href: MAIN + '/', label: 'Overview', icon: LayoutDashboard }] },
  {
    title: 'Markets',
    items: [
      { href: MAIN + '/assets', label: 'Assets', icon: Coins },
      { href: MAIN + '/markets', label: 'Markets', icon: BarChart3 },
      { href: MAIN + '/exchanges', label: 'Exchanges', icon: Building2 },
      { href: MAIN + '/dexes', label: 'DEXes', icon: ArrowLeftRight },
      { href: MAIN + '/aggregators', label: 'Aggregators', icon: Share2 },
      { href: MAIN + '/oracles', label: 'Oracles', icon: Radio },
    ],
  },
  {
    title: 'Protocols',
    items: [
      { href: MAIN + '/protocols', label: 'Protocols', icon: Boxes },
      { href: MAIN + '/lending', label: 'Lending', icon: Landmark },
      { href: MAIN + '/sources', label: 'Sources', icon: Activity },
    ],
  },
  {
    title: 'Network',
    items: [
      { href: MAIN + '/ledgers', label: 'Ledgers', icon: Blocks },
      { href: MAIN + '/accounts', label: 'Accounts', icon: Wallet },
      { href: MAIN + '/issuers', label: 'Issuers', icon: BadgeCheck },
    ],
  },
  {
    title: 'Analytics',
    items: [
      { href: MAIN + '/anomalies', label: 'Anomalies', icon: Zap },
      { href: MAIN + '/divergences', label: 'Divergences', icon: GitCompare },
      { href: MAIN + '/mev', label: 'MEV', icon: Activity },
    ],
  },
  {
    title: 'Developers',
    items: [
      {
        href: 'https://docs.stellarindex.io',
        label: 'API docs',
        icon: BookOpen,
        external: true,
      },
      { href: MAIN + '/sdk', label: 'SDK', icon: Code2 },
      { href: MAIN + '/pricing', label: 'Pricing', icon: Tag },
      { href: MAIN + '/methodology', label: 'Methodology', icon: BookOpen },
      { href: MAIN + '/diagnostics', label: 'Diagnostics', icon: Activity },
      { href: '/', label: 'Status', icon: Activity, active: true },
    ],
  },
];

function Row({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const Icon = item.icon;
  const cls = cn(
    'group flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-sm font-medium transition-colors',
    item.active
      ? 'bg-surface text-ink shadow-xs ring-1 ring-line'
      : 'text-ink-body hover:bg-surface-subtle hover:text-ink',
  );
  return (
    <a
      href={item.href}
      className={cls}
      onClick={onNavigate}
      aria-current={item.active ? 'page' : undefined}
    >
      <Icon
        className={cn(
          'h-4 w-4 shrink-0',
          item.active
            ? 'text-brand-600'
            : 'text-ink-faint group-hover:text-ink-muted',
        )}
      />
      <span className="truncate">{item.label}</span>
      {item.external && (
        <ExternalLink className="ml-auto h-3 w-3 text-ink-faint" />
      )}
    </a>
  );
}

/** The console nav body — identical to the explorer's, links to the main site. */
export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col bg-surface-muted">
      <div className="flex h-14 shrink-0 items-center px-4">
        <a
          href={MAIN}
          className="flex items-center gap-2 text-sm font-semibold tracking-tight text-ink"
        >
          <span className="flex h-6 w-6 items-center justify-center rounded-md bg-brand-600 text-white">
            <TrendingUp className="h-3.5 w-3.5" />
          </span>
          Stellar Index
        </a>
      </div>

      {/* Search — links to the main site (search lives there) */}
      <div className="px-3 pb-3">
        <a
          href={MAIN}
          className="flex w-full items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-sm text-ink-muted shadow-xs transition-colors hover:border-line-strong hover:text-ink-body"
        >
          <Search className="h-4 w-4 shrink-0" />
          <span>Search</span>
          <kbd className="ml-auto rounded border border-line bg-surface-muted px-1.5 py-0.5 text-[10px] font-medium text-ink-faint">
            ⌘K
          </kbd>
        </a>
      </div>

      <nav className="flex-1 space-y-5 overflow-y-auto px-3 pb-4">
        {NAV.map((group, gi) => (
          <div key={group.title ?? `g${gi}`} className="space-y-0.5">
            {group.title && (
              <div className="px-2.5 pb-1 text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
                {group.title}
              </div>
            )}
            {group.items.map((it) => (
              <Row key={it.href + it.label} item={it} onNavigate={onNavigate} />
            ))}
          </div>
        ))}
      </nav>

      <div className="shrink-0 border-t border-line p-3">
        <div className="grid grid-cols-2 gap-2">
          <a
            href={MAIN + '/signin'}
            className="rounded-lg border border-line bg-surface px-3 py-1.5 text-center text-sm font-medium text-ink-body shadow-xs hover:bg-surface-subtle"
          >
            Sign in
          </a>
          <a
            href={MAIN + '/signup'}
            className="rounded-lg bg-brand-600 px-3 py-1.5 text-center text-sm font-medium text-white hover:bg-brand-700"
          >
            Sign up
          </a>
        </div>
      </div>
    </div>
  );
}

export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-screen w-64 shrink-0 border-r border-line lg:block">
      <SidebarNav />
    </aside>
  );
}
