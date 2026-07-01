'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { Menu, TrendingUp, X } from 'lucide-react';
import { useCallback, useEffect, useState, type ReactNode } from 'react';

import { useDialog } from '@/lib/useDialog';

import { DegradedBanner } from './DegradedBanner';
import { Sidebar, SidebarNav } from './Sidebar';

/**
 * ConsoleShell is the app frame: a single persistent left Sidebar (logo →
 * search → grouped nav → account) over a scrolling content column — no top
 * bar. On small screens the sidebar collapses; a minimal mobile header
 * (logo + menu) opens it as a drawer. Embed routes (/embed/*) render
 * chrome-free for iframing.
 */
export function ConsoleShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const [drawer, setDrawer] = useState(false);
  useEffect(() => {
    setDrawer(false);
  }, [pathname]);
  // LC-051: the mobile drawer is the primary mobile nav — give it the full
  // modal contract (Escape + focus trap + focus move-in/restore), not just
  // Escape. The shared hook handles all of it.
  const closeDrawer = useCallback(() => setDrawer(false), []);
  const drawerRef = useDialog<HTMLDivElement>(drawer, closeDrawer);

  if (pathname?.startsWith('/embed/')) return <>{children}</>;

  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Mobile-only header — the desktop layout has no top bar. */}
        <div className="flex h-14 items-center justify-between border-b border-line bg-surface px-4 lg:hidden">
          <Link href="/" className="flex items-center gap-2 text-sm font-semibold tracking-tight text-ink">
            <span className="flex h-6 w-6 items-center justify-center rounded-md bg-brand-600 text-white">
              <TrendingUp className="h-3.5 w-3.5" />
            </span>
            Stellar Index
          </Link>
          <button
            type="button"
            onClick={() => setDrawer(true)}
            aria-label="Open navigation"
            aria-expanded={drawer}
            aria-controls="mobile-nav-drawer"
            className="-mr-1 inline-flex items-center justify-center rounded-md p-2 text-ink-body hover:bg-surface-subtle"
          >
            <Menu className="h-5 w-5" />
          </button>
        </div>

        <DegradedBanner />
        <main id="main" className="flex-1">
          {children}
        </main>
      </div>

      {/* Mobile drawer */}
      {drawer && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            className="absolute inset-0 bg-ink/30 backdrop-blur-xs"
            onClick={() => setDrawer(false)}
            aria-hidden
          />
          <div
            ref={drawerRef}
            id="mobile-nav-drawer"
            tabIndex={-1}
            role="dialog"
            aria-modal="true"
            aria-label="Navigation"
            className="absolute left-0 top-0 h-full w-72 max-w-[85vw] border-r border-line bg-surface shadow-elevated outline-hidden"
          >
            <button
              type="button"
              onClick={() => setDrawer(false)}
              aria-label="Close navigation"
              className="absolute right-2 top-3 z-10 inline-flex items-center justify-center rounded-md p-2 text-ink-body hover:bg-surface-subtle"
            >
              <X className="h-5 w-5" />
            </button>
            <SidebarNav onNavigate={() => setDrawer(false)} />
          </div>
        </div>
      )}
    </div>
  );
}
