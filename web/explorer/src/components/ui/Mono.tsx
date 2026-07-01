'use client';

import { Check, Copy } from 'lucide-react';
import { useState } from 'react';

import { cn } from '@/lib/cn';

/** Truncate a long identifier (G-strkey, C-id, tx hash) to head…tail. */
export function truncateMiddle(s: string, head = 6, tail = 4): string {
  if (!s || s.length <= head + tail + 1) return s;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
}

/**
 * Mono renders a monospace identifier (address / hash / contract id) with an
 * optional inline copy button. Use `truncate` to shorten long strkeys.
 */
export function Mono({
  value,
  truncate,
  copy = true,
  className,
}: {
  value: string;
  truncate?: boolean | { head: number; tail: number };
  copy?: boolean;
  className?: string;
}) {
  const display = truncate
    ? typeof truncate === 'object'
      ? truncateMiddle(value, truncate.head, truncate.tail)
      : truncateMiddle(value)
    : value;
  return (
    <span className={cn('inline-flex items-center gap-1.5 font-mono text-[13px]', className)}>
      <span className="break-all">{display}</span>
      {copy && <CopyButton value={value} />}
    </span>
  );
}

export function CopyButton({ value, className }: { value: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      aria-label="Copy to clipboard"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1400);
        } catch {
          /* clipboard unavailable — no-op */
        }
      }}
      className={cn(
        'inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-sm text-ink-faint transition-colors hover:bg-surface-subtle hover:text-ink-body',
        className,
      )}
    >
      {copied ? <Check className="h-3.5 w-3.5 text-up" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
}
