import type { ComponentProps, ReactNode } from 'react';

import { cn } from '@/lib/cn';

export type BadgeTone =
  | 'neutral'
  | 'brand'
  | 'up'
  | 'down'
  | 'warn'
  | 'bad'
  | 'ok';

const tones: Record<BadgeTone, string> = {
  neutral: 'bg-surface-subtle text-ink-body ring-line',
  brand: 'bg-brand-50 text-brand-700 ring-brand-100',
  up: 'bg-up-subtle text-up-strong ring-up/20',
  down: 'bg-down-subtle text-down-strong ring-down/20',
  warn: 'bg-warn-50 text-warn-700 ring-warn-300/60',
  bad: 'bg-bad-50 text-bad-700 ring-bad-300/60',
  ok: 'bg-ok-50 text-ok-700 ring-ok-300/60',
};

type BadgeProps = ComponentProps<'span'> & {
  tone?: BadgeTone;
  /** A small leading dot — useful for status pills. */
  dot?: boolean;
  children: ReactNode;
};

/**
 * Badge is the small status / tag pill. Tones map to the semantic palette
 * (brand, up/down deltas, warn/bad/ok severities). `dot` prepends a status
 * dot in the tone colour.
 */
export function Badge({ tone = 'neutral', dot, className, children, ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        tones[tone],
        className,
      )}
      {...props}
    >
      {dot && <span className="h-1.5 w-1.5 rounded-full bg-current opacity-80" />}
      {children}
    </span>
  );
}
