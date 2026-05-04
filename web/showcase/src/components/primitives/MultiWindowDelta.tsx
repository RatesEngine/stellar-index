import { twMerge } from 'tailwind-merge';
import { DirectionPill } from './DirectionPill';

export type DeltaWindow = {
  /** Display label, e.g. '1h', '24h', '7d', '30d' */
  label: string;
  /** Percent change as a decimal — 5.2 means +5.2%. Null = no data. */
  deltaPct: number | null;
};

export type MultiWindowDeltaProps = {
  windows: DeltaWindow[];
  /** Compact = inline strip, tighter spacing for dense tables. */
  compact?: boolean;
  className?: string;
};

/**
 * Multi-window delta strip — renders the
 * `1h: +0.5% · 24h: +3.2% · 7d: −1.1% · 30d: +18.4%` pattern
 * from the data-inventory doc §6.1.
 *
 * Pass any number of windows; the canonical four are h1/h24/d7/d30
 * but the worker can emit fewer (sparse history → some are null)
 * or more (e.g. 1y once we backfill far enough).
 */
export function MultiWindowDelta({
  windows,
  compact,
  className,
}: MultiWindowDeltaProps) {
  return (
    <div
      className={twMerge(
        'inline-flex items-center text-xs',
        compact ? 'gap-1' : 'gap-2',
        className,
      )}
    >
      {windows.map((w, i) => (
        <span key={w.label} className="inline-flex items-center gap-1">
          <span className="text-slate-500 dark:text-slate-400">{w.label}:</span>
          <DirectionPill deltaPct={w.deltaPct} compact={compact} />
          {i < windows.length - 1 && (
            <span
              className="text-slate-300 dark:text-slate-700"
              aria-hidden
            >
              ·
            </span>
          )}
        </span>
      ))}
    </div>
  );
}
