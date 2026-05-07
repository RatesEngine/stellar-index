'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';
import { ChevronDown, TrendingUp } from 'lucide-react';

import { useStatus } from '@/api/hooks';
import { SearchModal } from './SearchModal';
import { ThemeToggle } from './ThemeToggle';

export function Navbar() {
  const pathname = usePathname();
  if (pathname?.startsWith('/embed/')) return null;
  return (
    <nav className="border-b border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-950">
      <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-3">
        <Link
          href="/"
          className="flex items-center gap-2 text-sm font-semibold tracking-tight"
        >
          <TrendingUp className="h-5 w-5 text-brand-500" />
          <span>Rates Engine</span>
        </Link>
        <div className="hidden items-center gap-1 text-sm md:flex">
          <NavLink href="/currencies" label="Currencies" />
          <Dropdown label="Blockchain" items={BLOCKCHAIN_ITEMS} />
          <NavLink href="https://docs.ratesengine.net" label="API Docs" external />
          <Dropdown label="About" items={ABOUT_ITEMS} />
          <SearchModal />
          <ThemeToggle />
          <StatusPill />
          <Link
            href="/signin"
            className="ml-2 rounded-md px-3 py-1.5 text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Sign in
          </Link>
          <Link
            href="/signup"
            className="ml-1 rounded-md bg-brand-600 px-3 py-1.5 font-medium text-white hover:bg-brand-700"
          >
            Create account
          </Link>
        </div>
      </div>
    </nav>
  );
}

type Item = { label: string; href: string; external?: boolean; description?: string };

const BLOCKCHAIN_ITEMS: Item[] = [
  { label: 'Assets', href: '/assets', description: 'Every asset across every connected network.' },
  { label: 'Exchanges', href: '/exchanges', description: 'Connected CEXes — order-book depth, 24h volume, pair coverage.' },
  { label: 'Dexes', href: '/dexes', description: 'On-chain DEXes + every (venue, base, quote) pool we observe.' },
  { label: 'Lending', href: '/lending', description: 'Lending pools across every connected protocol.' },
  { label: 'Aggregators', href: '/aggregators', description: 'Liquidity aggregators routing through the venues above.' },
  { label: 'Oracles', href: '/oracles', description: 'On-chain price oracles + the streams they publish.' },
  { label: 'Networks', href: '/networks', description: 'Per-network macro pulse — ingest tip, totals, contributors.' },
];

const ABOUT_ITEMS: Item[] = [
  { label: 'Pricing', href: '/pricing', description: 'Plans, quotas, SLAs.' },
  { label: 'Blog', href: '/blog', description: 'Engineering notes + product updates.' },
  { label: 'API status', href: 'https://status.ratesengine.net', external: true, description: 'Live service status.' },
  { label: 'Company', href: '/company', description: 'Who we are.' },
  { label: 'Careers', href: '/careers', description: 'Roles open at Rates Engine.' },
  { label: 'Contact', href: '/contact', description: 'How to reach us.' },
];

function NavLink({ href, label, external }: { href: string; label: string; external?: boolean }) {
  const cls =
    'rounded-md px-3 py-1.5 text-slate-600 hover:bg-slate-100 hover:text-brand-600 dark:text-slate-300 dark:hover:bg-slate-800';
  if (external) {
    return (
      <a href={href} className={cls}>
        {label}
      </a>
    );
  }
  return (
    <Link href={href} className={cls}>
      {label}
    </Link>
  );
}

function Dropdown({ label, items }: { label: string; items: Item[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    function onDocClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onEsc(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onEsc);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onEsc);
    };
  }, [open]);
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-haspopup="menu"
        className="inline-flex items-center gap-1 rounded-md px-3 py-1.5 text-slate-600 hover:bg-slate-100 hover:text-brand-600 dark:text-slate-300 dark:hover:bg-slate-800"
      >
        {label}
        <ChevronDown
          className={`h-3.5 w-3.5 transition-transform ${open ? 'rotate-180' : ''}`}
          aria-hidden
        />
      </button>
      {open && (
        <div
          role="menu"
          className="absolute left-0 top-full z-50 mt-1 w-72 rounded-lg border border-slate-200 bg-white p-2 shadow-lg dark:border-slate-700 dark:bg-slate-900"
        >
          {items.map((it) =>
            it.external ? (
              <a
                key={it.href}
                href={it.href}
                role="menuitem"
                onClick={() => setOpen(false)}
                className="block rounded-md px-3 py-2 text-sm hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <div className="font-medium text-slate-900 dark:text-slate-100">{it.label}</div>
                {it.description && (
                  <div className="text-xs text-slate-500 dark:text-slate-400">{it.description}</div>
                )}
              </a>
            ) : (
              <Link
                key={it.href}
                href={it.href}
                role="menuitem"
                onClick={() => setOpen(false)}
                className="block rounded-md px-3 py-2 text-sm hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <div className="font-medium text-slate-900 dark:text-slate-100">{it.label}</div>
                {it.description && (
                  <div className="text-xs text-slate-500 dark:text-slate-400">{it.description}</div>
                )}
              </Link>
            ),
          )}
        </div>
      )}
    </div>
  );
}

function StatusPill() {
  const status = useStatus();
  const overall = status.data?.overall ?? 'unknown';
  const tone =
    overall === 'ok'
      ? 'bg-emerald-500'
      : overall === 'degraded'
        ? 'bg-amber-500'
        : overall === 'down'
          ? 'bg-rose-500'
          : 'bg-slate-400';
  const title =
    overall === 'ok'
      ? 'All systems operational'
      : overall === 'degraded'
        ? 'Degraded performance — see status.ratesengine.net'
        : overall === 'down'
          ? 'Major outage — see status.ratesengine.net'
          : 'Status unknown';
  return (
    <a
      href="https://status.ratesengine.net"
      title={title}
      aria-label={`API status: ${overall}`}
      className="ml-2 inline-flex items-center rounded-md p-2 text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
    >
      <span
        className={`h-2 w-2 rounded-full ${tone} ${overall === 'ok' ? 'animate-pulse' : ''}`}
        aria-hidden
      />
    </a>
  );
}
