'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import {
  KeyRound,
  BarChart3,
  Settings,
  LogOut,
  ShieldCheck,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { useAuth } from '@/lib/auth';
import { logout, type AccountMe } from '@/lib/api';
import { cn } from '@/lib/cn';

interface NavItem {
  href: string;
  label: string;
  icon: typeof KeyRound;
}

const nav: NavItem[] = [
  { href: '/keys/', label: 'API keys', icon: KeyRound },
  { href: '/usage/', label: 'Usage', icon: BarChart3 },
  { href: '/settings/', label: 'Settings', icon: Settings },
];

const staffNav: NavItem[] = [
  { href: '/admin/', label: 'Staff', icon: ShieldCheck },
];

export function AppShell({
  me,
  children,
}: {
  me: AccountMe;
  children: ReactNode;
}) {
  const pathname = usePathname();
  const router = useRouter();

  async function handleLogout() {
    try {
      await logout();
    } finally {
      // Either way, push to /signin and let the AuthProvider
      // re-resolve. Even a network failure here doesn't block
      // the user from leaving — the next /me call will 401 if
      // the cookie did get cleared, and if it didn't, /signin
      // will quietly bounce them back to /keys.
      router.replace('/signin/');
    }
  }

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-60 flex-col border-r border-surface-line bg-surface">
        <div className="border-b border-surface-line px-5 py-4">
          <Link href="/" className="text-base font-semibold text-ink">
            Rates Engine
          </Link>
          <p className="mt-0.5 text-xs text-ink-faint">Customer dashboard</p>
        </div>

        <nav className="flex-1 space-y-0.5 px-2 py-3">
          {nav.map((item) => (
            <NavLink key={item.href} item={item} pathname={pathname} />
          ))}
          {me.user.is_staff && (
            <>
              <div className="mt-4 px-3 pt-3 text-[11px] font-medium uppercase tracking-wide text-ink-faint">
                Staff
              </div>
              {staffNav.map((item) => (
                <NavLink key={item.href} item={item} pathname={pathname} />
              ))}
            </>
          )}
        </nav>

        <div className="border-t border-surface-line p-3">
          <div className="px-2 pb-2 text-xs text-ink-muted">
            <div className="truncate font-medium text-ink">
              {me.user.email}
            </div>
            <div className="truncate text-ink-faint">
              {me.account.name} · tier {me.account.tier}
            </div>
          </div>
          <button
            onClick={handleLogout}
            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-ink-muted hover:bg-surface-subtle hover:text-ink"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </button>
        </div>
      </aside>

      <main className="flex-1 px-8 py-8">{children}</main>
    </div>
  );
}

function NavLink({ item, pathname }: { item: NavItem; pathname: string }) {
  const active = pathname.startsWith(item.href);
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      className={cn(
        'flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors',
        active
          ? 'bg-brand-50 font-medium text-brand-900'
          : 'text-ink-muted hover:bg-surface-subtle hover:text-ink',
      )}
    >
      <Icon className="h-4 w-4" />
      {item.label}
    </Link>
  );
}

export function useAuthGate(): AccountMe | null {
  const { state } = useAuth();
  if (state.kind !== 'authed') return null;
  return state.me;
}
