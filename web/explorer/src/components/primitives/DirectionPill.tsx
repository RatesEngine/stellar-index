import { ArrowDown, ArrowRight, ArrowUp } from 'lucide-react';
import { twMerge } from 'tailwind-merge';

export type DirectionPillProps = {
  /** Fraction; 0.05 = +5%. Pass null for "no data". */
  deltaPct: number | null;
  /** Tightens to a smaller chip when used in dense tables. */
  compact?: boolean;
  className?: string;
};

/**
 * Direction pill — arrow + colour-coded percent change.
 *
 * Bands per design-inventory §6.3:
 *   ↗  +1-5%      light green
 *   ↗↗ +5-20%    green
 *   ↗↗↗ >+20%    bright green
 *   ↘  ↘↘ ↘↘↘    symmetric red
 *   →  <±0.5%    grey
 */
export function DirectionPill({
  deltaPct,
  compact,
  className,
}: DirectionPillProps) {
  if (deltaPct === null || !Number.isFinite(deltaPct)) {
    return (
      <span
        className={twMerge(
          'inline-flex items-center gap-1 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500 dark:bg-slate-800',
          className,
        )}
        aria-label="no data"
      >
        —
      </span>
    );
  }
  const abs = Math.abs(deltaPct);
  const sign = deltaPct > 0 ? 'up' : deltaPct < 0 ? 'down' : 'flat';
  const Icon = sign === 'up' ? ArrowUp : sign === 'down' ? ArrowDown : ArrowRight;
  const bg =
    abs < 0.5
      ? 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
      : sign === 'up' && abs < 5
        ? 'bg-up-subtle/40 text-up-strong dark:bg-up-strong/20 dark:text-up'
        : sign === 'up' && abs < 20
          ? 'bg-up-subtle text-up-strong dark:bg-up/30 dark:text-up-subtle'
          : sign === 'up'
            ? 'bg-up text-white dark:bg-up dark:text-white'
            : sign === 'down' && abs < 5
              ? 'bg-down-subtle/40 text-down-strong dark:bg-down-strong/20 dark:text-down'
              : sign === 'down' && abs < 20
                ? 'bg-down-subtle text-down-strong dark:bg-down/30 dark:text-down-subtle'
                : 'bg-down text-white dark:bg-down dark:text-white';
  return (
    <span
      className={twMerge(
        'inline-flex items-center gap-0.5 rounded-full font-medium tabular-nums',
        compact ? 'px-1.5 py-0 text-[10px]' : 'px-2 py-0.5 text-xs',
        bg,
        className,
      )}
      aria-label={`${deltaPct > 0 ? '+' : ''}${deltaPct.toFixed(2)}% ${sign}`}
    >
      <Icon className={compact ? 'h-2.5 w-2.5' : 'h-3 w-3'} aria-hidden />
      {deltaPct > 0 ? '+' : ''}
      {deltaPct.toFixed(2)}%
    </span>
  );
}
