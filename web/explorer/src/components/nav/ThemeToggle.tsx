'use client';

import { Monitor, Moon, Sun } from 'lucide-react';
import { useEffect, useState } from 'react';

type Mode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 're.theme';

/**
 * ThemeToggle — three-state cycle button (light → dark → system →
 * light). Hidden until mount because static-export pre-renders
 * the page with whatever icon you put first; flashing icons on
 * hydration is jarring. The actual `html.dark` class is set by
 * the inline init script in layout.tsx so the page paints with
 * the right theme before this component even mounts.
 */
export function ThemeToggle() {
  const [mode, setMode] = useState<Mode>('system');
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    const stored = readStored();
    setMode(stored);
    setMounted(true);
  }, []);

  function cycle() {
    const next: Mode =
      mode === 'light' ? 'dark' : mode === 'dark' ? 'system' : 'light';
    setMode(next);
    writeStored(next);
    applyMode(next);
  }

  if (!mounted) {
    // Prevent hydration mismatch — server-rendered HTML doesn't
    // know what the user picked. Render a transparent placeholder
    // the same size as the real button so the navbar doesn't shift.
    return <span className="inline-block h-7 w-7" aria-hidden />;
  }

  const Icon = mode === 'light' ? Sun : mode === 'dark' ? Moon : Monitor;
  return (
    <button
      type="button"
      onClick={cycle}
      aria-label={`Theme: ${mode}. Click to cycle.`}
      title={`Theme: ${mode}. Click to cycle.`}
      className="inline-flex h-7 w-7 items-center justify-center rounded-md text-slate-500 hover:bg-slate-100 hover:text-brand-600 dark:text-slate-400 dark:hover:bg-slate-800"
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

function readStored(): Mode {
  if (typeof window === 'undefined') return 'system';
  const v = window.localStorage.getItem(STORAGE_KEY);
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system';
}

function writeStored(mode: Mode): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(STORAGE_KEY, mode);
}

function applyMode(mode: Mode): void {
  if (typeof document === 'undefined') return;
  const wantsDark =
    mode === 'dark' ||
    (mode === 'system' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', wantsDark);
}
