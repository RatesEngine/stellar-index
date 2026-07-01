'use client';

import { useEffect, useMemo, useRef, useState } from 'react';

/**
 * CurrencyCombobox — searchable picker over a list of tickers.
 * Replaces a plain `<select>` for converters where the user
 * may have 100+ currencies to pick from.
 *
 * Keyboard-friendly: arrow keys navigate the filtered list,
 * Enter selects, Escape closes. Click-outside also closes.
 * No external dependencies.
 *
 * Visual modes:
 *   - mode="chip" (default) — compact uppercase pill that shows
 *     the current ticker and a ▾ caret. Suits inline use inside
 *     a converter input row.
 *   - mode="select" — wider control with a left-side border that
 *     mirrors a native `<select>` look. Suits standalone "pick a
 *     currency" UX.
 */
export function CurrencyCombobox({
  tickers,
  value,
  onChange,
  mode = 'chip',
  placeholder = 'Search currency…',
}: {
  tickers: string[];
  value: string;
  onChange: (v: string) => void;
  mode?: 'chip' | 'select';
  placeholder?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [highlight, setHighlight] = useState(0);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toUpperCase();
    if (!q) return tickers;
    return tickers.filter((t) => t.includes(q));
  }, [tickers, query]);

  useEffect(() => {
    setHighlight(0);
  }, [query, open]);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
        setQuery('');
      }
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, [open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  function commit(t: string) {
    onChange(t);
    setOpen(false);
    setQuery('');
  }

  const triggerCls =
    mode === 'select'
      ? 'rounded-md border border-line bg-surface px-2 py-1 font-mono text-xs uppercase tracking-wider text-ink-body hover:border-brand-500'
      : 'rounded-sm bg-surface-subtle px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-ink-body hover:bg-line';

  return (
    <div ref={wrapRef} className="relative">
      <button type="button" onClick={() => setOpen((v) => !v)} className={triggerCls}>
        {value} ▾
      </button>
      {open && (
        <div className="absolute right-0 top-full z-20 mt-1 w-56 overflow-hidden rounded-md border border-line bg-surface shadow-lg">
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setHighlight((h) => Math.min(h + 1, filtered.length - 1));
              } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setHighlight((h) => Math.max(h - 1, 0));
              } else if (e.key === 'Enter') {
                e.preventDefault();
                if (filtered[highlight]) commit(filtered[highlight]);
              } else if (e.key === 'Escape') {
                e.preventDefault();
                setOpen(false);
                setQuery('');
              }
            }}
            placeholder={placeholder}
            className="w-full border-b border-line bg-surface px-3 py-2 text-sm focus:outline-hidden"
          />
          <ul className="max-h-64 overflow-y-auto py-1 text-sm">
            {filtered.length === 0 && (
              <li className="px-3 py-2 text-xs text-ink-muted">No matches</li>
            )}
            {filtered.map((t, i) => (
              <li key={t}>
                <button
                  type="button"
                  onClick={() => commit(t)}
                  onMouseEnter={() => setHighlight(i)}
                  className={`flex w-full items-center justify-between px-3 py-1.5 font-mono text-xs uppercase tracking-wider ${
                    i === highlight
                      ? 'bg-brand-50 text-brand-900'
                      : 'text-ink-body'
                  }`}
                >
                  <span>{t}</span>
                  {t === value && (
                    <span className="text-[10px] text-ink-faint">current</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
