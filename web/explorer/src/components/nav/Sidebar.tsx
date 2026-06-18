'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  Activity,
  ArrowLeftRight,
  BadgeCheck,
  Blocks,
  BookOpen,
  Boxes,
  Building2,
  Code2,
  Coins,
  ExternalLink,
  GitCompare,
  Gauge,
  KeyRound,
  Landmark,
  LayoutDashboard,
  LogOut,
  Network,
  Radio,
  Settings,
  Share2,
  ShieldCheck,
  TrendingUp,
  User,
  Wallet,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { useMe } from '@/api/hooks';
import { cn } from '@/lib/cn';
import { SearchModal } from './SearchModal';

type NavItem = { href: string; label: string; icon: LucideIcon; external?: boolean; exact?: boolean };
type NavGroup = { title?: string; items: NavItem[] };

// The console IA — an entity-centric explorer. Grouped so a data-heavy
// site stays navigable. Secondary/marketing pages (Pricing, Methodology,
// Diagnostics, Sources, the CEX board) live in the footer + search, not
// the primary rail. Transactions / Contracts / SDEX Markets land here as
// their pages ship (kept out until then so there are no dead links).
const NAV: NavGroup[] = [
  {
    items: [
      { href: '/', label: 'Home', icon: LayoutDashboard, exact: true },
      { href: '/ledgers', label: 'Ledgers', icon: Blocks },
      { href: '/accounts', label: 'Accounts', icon: Wallet },
      { href: '/issuers', label: 'Issuers', icon: BadgeCheck },
      { href: '/assets', label: 'Assets', icon: Coins },
      { href: '/dexes', label: 'AMM Pools', icon: Boxes },
    ],
  },
  {
    title: 'Protocols',
    items: [
      { href: '/protocols', label: 'DEX / AMM', icon: ArrowLeftRight },
      { href: '/lending', label: 'Lending', icon: Landmark },
      { href: '/aggregators', label: 'Aggregators', icon: Share2 },
      { href: '/bridges', label: 'Bridges', icon: Network },
      { href: '/oracles', label: 'Oracles', icon: Radio },
      { href: '/protocols/soroswap-router', label: 'Soroswap Router', icon: Boxes },
    ],
  },
  {
    items: [{ href: '/exchanges', label: 'External Markets', icon: Building2 }],
  },
  {
    title: 'Analytics',
    items: [
      { href: '/anomalies', label: 'Anomalies', icon: Zap },
      { href: '/divergences', label: 'Divergence', icon: GitCompare },
      { href: '/mev', label: 'MEV', icon: Activity },
    ],
  },
  {
    title: 'Developers',
    items: [
      { href: 'https://docs.stellarindex.io', label: 'API docs', icon: BookOpen, external: true },
      { href: '/sdk', label: 'SDK', icon: Code2 },
      { href: 'https://status.stellarindex.io', label: 'Status', icon: Activity, external: true },
    ],
  },
];

// Shown only when signed in — the logged-in "Account" section (the former
// standalone dashboard, now part of the site). The Admin row is appended
// only for staff sessions (see SidebarNav).
const ACCOUNT_GROUP: NavGroup = {
  title: 'Account',
  items: [
    { href: '/account', label: 'Dashboard', icon: LayoutDashboard, exact: true },
    { href: '/account/keys', label: 'API keys', icon: KeyRound },
    { href: '/account/usage', label: 'Usage', icon: Gauge },
    { href: '/account/settings', label: 'Settings', icon: Settings },
  ],
};

const ADMIN_ITEM: NavItem = {
  href: '/account/admin',
  label: 'Admin',
  icon: ShieldCheck,
};

function isActive(pathname: string | null, href: string): boolean {
  if (!pathname) return false;
  if (href === '/') return pathname === '/';
  return pathname === href || pathname.startsWith(href + '/');
}

function Row({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const pathname = usePathname();
  const active =
    !item.external && (item.exact ? pathname === item.href : isActive(pathname, item.href));
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
  const me = useMe();
  const signedIn = !!(me.data && (me.data.user?.email || me.data.key_id));
  const isStaff = !!me.data?.user?.is_staff;
  // Logged-in users get the Account section right after Overview; staff
  // sessions also get the Admin cockpit row.
  const accountGroup: NavGroup = isStaff
    ? { ...ACCOUNT_GROUP, items: [...ACCOUNT_GROUP.items, ADMIN_ITEM] }
    : ACCOUNT_GROUP;
  const groups = signedIn ? [NAV[0], accountGroup, ...NAV.slice(1)] : NAV;
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
        {groups.map((group, gi) => (
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
  const signedIn = !!(me.data && (me.data.user?.email || me.data.key_id));
  const email = me.data?.user?.email;

  return (
    <div className="space-y-2">
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
        aria-controls="sidebar-account-menu"
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
        // Disclosure, not an APG menu: we don't implement the menu
        // keyboard model (Arrow/Home/End), so declaring role="menu"/
        // "menuitem" would promise a behaviour we don't provide and
        // mislead AT. The trigger's aria-expanded + aria-controls
        // describe it correctly; items are plain links/buttons (Tab-
        // reachable), and Escape closes it (handled above).
        <div
          id="sidebar-account-menu"
          className="absolute bottom-full left-0 z-50 mb-1 w-full rounded-lg border border-line bg-surface p-2 shadow-elevated"
        >
          <Link
            href="/account"
            onClick={() => setOpen(false)}
            className="flex items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-surface-subtle"
          >
            <User className="h-3.5 w-3.5 text-ink-faint" />
            Your account
          </Link>
          <button
            type="button"
            onClick={signOut}
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
