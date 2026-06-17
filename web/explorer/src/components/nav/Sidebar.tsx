'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
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
  LogOut,
  Radio,
  Share2,
  Tag,
  TrendingUp,
  User,
  Wallet,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { useMe, useStatus } from '@/api/hooks';
import { cn } from '@/lib/cn';
import { SearchModal } from './SearchModal';

type NavItem = { href: string; label: string; icon: LucideIcon; external?: boolean };
type NavGroup = { title?: string; items: NavItem[] };

// The console IA — grouped so a data-heavy site stays navigable.
const NAV: NavGroup[] = [
  { items: [{ href: '/', label: 'Overview', icon: LayoutDashboard }] },
  {
    title: 'Markets',
    items: [
      { href: '/assets', label: 'Assets', icon: Coins },
      { href: '/markets', label: 'Markets', icon: BarChart3 },
      { href: '/exchanges', label: 'Exchanges', icon: Building2 },
      { href: '/dexes', label: 'DEXes', icon: ArrowLeftRight },
      { href: '/aggregators', label: 'Aggregators', icon: Share2 },
      { href: '/oracles', label: 'Oracles', icon: Radio },
    ],
  },
  {
    title: 'Protocols',
    items: [
      { href: '/protocols', label: 'Protocols', icon: Boxes },
      { href: '/lending', label: 'Lending', icon: Landmark },
      { href: '/sources', label: 'Sources', icon: Activity },
    ],
  },
  {
    title: 'Network',
    items: [
      { href: '/ledgers', label: 'Ledgers', icon: Blocks },
      { href: '/accounts', label: 'Accounts', icon: Wallet },
      { href: '/issuers', label: 'Issuers', icon: BadgeCheck },
    ],
  },
  {
    title: 'Analytics',
    items: [
      { href: '/anomalies', label: 'Anomalies', icon: Zap },
      { href: '/divergences', label: 'Divergences', icon: GitCompare },
      { href: '/mev', label: 'MEV', icon: Activity },
    ],
  },
  {
    title: 'Developers',
    items: [
      { href: 'https://docs.stellarindex.io', label: 'API docs', icon: BookOpen, external: true },
      { href: '/sdk', label: 'SDK', icon: Code2 },
      { href: '/pricing', label: 'Pricing', icon: Tag },
      { href: '/methodology', label: 'Methodology', icon: BookOpen },
      { href: '/diagnostics', label: 'Diagnostics', icon: Activity },
    ],
  },
];

function isActive(pathname: string | null, href: string): boolean {
  if (!pathname) return false;
  if (href === '/') return pathname === '/';
  return pathname === href || pathname.startsWith(href + '/');
}

function Row({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const pathname = usePathname();
  const active = !item.external && isActive(pathname, item.href);
  const Icon = item.icon;
  const cls = cn(
    'group flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-sm font-medium transition-colors',
    active
      ? 'bg-surface text-ink shadow-xs ring-1 ring-line'
      : 'text-ink-body hover:bg-surface-subtle hover:text-ink',
  );
  const inner = (
    <>
      <Icon className={cn('h-4 w-4 shrink-0', active ? 'text-brand-600' : 'text-ink-faint group-hover:text-ink-muted')} />
      <span className="truncate">{item.label}</span>
      {item.external && <ExternalLink className="ml-auto h-3 w-3 text-ink-faint" />}
    </>
  );
  if (item.external) {
    return (
      <a href={item.href} className={cls} onClick={onNavigate}>
        {inner}
      </a>
    );
  }
  return (
    <Link href={item.href} className={cls} onClick={onNavigate} aria-current={active ? 'page' : undefined}>
      {inner}
    </Link>
  );
}

/** The console nav body — shared by the desktop rail + the mobile drawer. */
export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col bg-surface-muted">
      {/* Logo */}
      <div className="flex h-14 shrink-0 items-center px-4">
        <Link
          href="/"
          onClick={onNavigate}
          className="flex items-center gap-2 text-sm font-semibold tracking-tight text-ink"
        >
          <span className="flex h-6 w-6 items-center justify-center rounded-md bg-brand-600 text-white">
            <TrendingUp className="h-3.5 w-3.5" />
          </span>
          Stellar Index
        </Link>
      </div>

      {/* Search — directly below the logo */}
      <div className="px-3 pb-3">
        <SearchModal />
      </div>

      {/* Nav */}
      <nav className="flex-1 space-y-5 overflow-y-auto px-3 pb-4">
        {NAV.map((group, gi) => (
          <div key={group.title ?? `g${gi}`} className="space-y-0.5">
            {group.title && (
              <div className="px-2.5 pb-1 text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
                {group.title}
              </div>
            )}
            {group.items.map((it) => (
              <Row key={it.href} item={it} onNavigate={onNavigate} />
            ))}
          </div>
        ))}
      </nav>

      {/* Account — bottom-left */}
      <div className="shrink-0 border-t border-line p-3">
        <AccountCard onNavigate={onNavigate} />
      </div>
    </div>
  );
}

/** The persistent desktop left rail (hidden on mobile; drawer handles small screens). */
export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-screen w-64 shrink-0 border-r border-line lg:block">
      <SidebarNav />
    </aside>
  );
}

// ─── Account (bottom-left) ─────────────────────────────────────────────────

function AccountCard({ onNavigate }: { onNavigate?: () => void }) {
  const me = useMe();
  const status = useStatus();
  const overall = status.data?.overall ?? 'unknown';
  const statusTone =
    overall === 'ok' ? 'bg-up' : overall === 'degraded' ? 'bg-warn-500' : overall === 'down' ? 'bg-down' : 'bg-ink-faint';

  const signedIn = !!(me.data && (me.data.user?.email || me.data.key_id));
  const email = me.data?.user?.email;

  return (
    <div className="space-y-2">
      {/* Status line */}
      <a
        href="https://status.stellarindex.io"
        className="flex items-center gap-2 rounded-md px-2 py-1 text-xs text-ink-muted hover:bg-surface-subtle"
      >
        <span className={cn('h-1.5 w-1.5 rounded-full', statusTone, overall === 'ok' && 'animate-pulse')} aria-hidden />
        {overall === 'ok' ? 'All systems operational' : overall === 'degraded' ? 'Degraded performance' : overall === 'down' ? 'Major outage' : 'Status'}
      </a>

      {signedIn ? (
        <AccountMenu email={email} />
      ) : (
        <div className="grid grid-cols-2 gap-2">
          <Link
            href="/signin"
            onClick={onNavigate}
            className="rounded-lg border border-line bg-surface px-3 py-1.5 text-center text-sm font-medium text-ink-body shadow-xs hover:bg-surface-subtle"
          >
            Sign in
          </Link>
          <Link
            href="/signup"
            onClick={onNavigate}
            className="rounded-lg bg-brand-600 px-3 py-1.5 text-center text-sm font-medium text-white hover:bg-brand-700"
          >
            Sign up
          </Link>
        </div>
      )}
    </div>
  );
}

function AccountMenu({ email }: { email?: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onEsc(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onEsc);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onEsc);
    };
  }, [open]);

  async function signOut() {
    try {
      const base = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.stellarindex.io';
      await fetch(`${base}/v1/auth/logout`, { method: 'POST', credentials: 'include' });
    } catch {
      /* best-effort */
    }
    window.location.href = '/';
  }

  const initials = (email ?? 'A').slice(0, 1).toUpperCase();

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-haspopup="menu"
        className="flex w-full items-center gap-2.5 rounded-lg border border-line bg-surface px-2.5 py-2 text-left shadow-xs hover:bg-surface-subtle"
      >
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-brand-600 text-xs font-semibold text-white">
          {initials}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium text-ink">{email ?? 'Account'}</span>
          <span className="block truncate text-[11px] text-ink-muted">Signed in</span>
        </span>
      </button>
      {open && (
        <div
          role="menu"
          className="absolute bottom-full left-0 z-50 mb-1 w-full rounded-lg border border-line bg-surface p-2 shadow-elevated"
        >
          <Link
            href="/account"
            role="menuitem"
            onClick={() => setOpen(false)}
            className="flex items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-surface-subtle"
          >
            <User className="h-3.5 w-3.5 text-ink-faint" />
            Your account
          </Link>
          <button
            type="button"
            onClick={signOut}
            role="menuitem"
            className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-ink-body hover:bg-surface-subtle"
          >
            <LogOut className="h-3.5 w-3.5 text-ink-faint" />
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
