import { ChevronDown, ChevronUp, Minus, Plus } from 'lucide-react';
import { twMerge } from 'tailwind-merge';

export type RankBadgeProps = {
  /** Positive = moved UP this many spots, negative = moved DOWN, 0 = unchanged. */
  delta: number;
  /** Mark a brand-new entry. Overrides delta. */
  isNew?: boolean;
  className?: string;
};

/**
 * Rank-change badge — `▲ 2` / `▼ 1` / `—` / `NEW` per
 * design-inventory §6.5. Used on every directory leaderboard.
 */
export function RankBadge({ delta, isNew, className }: RankBadgeProps) {
  if (isNew) {
    return (
      <span
        className={twMerge(
          'inline-flex items-center gap-0.5 rounded-full bg-brand-100 px-1.5 py-0 text-[10px] font-bold text-brand-900 dark:bg-brand-900/40 dark:text-brand-100',
          className,
        )}
      >
        <Plus className="h-2.5 w-2.5" aria-hidden />
        NEW
      </span>
    );
  }
  if (delta === 0) {
    return (
      <span
        className={twMerge(
          'inline-flex items-center gap-0.5 text-xs text-slate-400 dark:text-slate-600',
          className,
        )}
        aria-label="no rank change"
      >
        <Minus className="h-3 w-3" aria-hidden />
      </span>
    );
  }
  const Icon = delta > 0 ? ChevronUp : ChevronDown;
  const tone =
    delta > 0
      ? 'text-up-strong dark:text-up-subtle'
      : 'text-down-strong dark:text-down-subtle';
  return (
    <span
      className={twMerge('inline-flex items-center gap-0.5 text-xs font-medium', tone, className)}
      aria-label={`moved ${delta > 0 ? 'up' : 'down'} ${Math.abs(delta)}`}
    >
      <Icon className="h-3 w-3" aria-hidden />
      {Math.abs(delta)}
    </span>
  );
}
