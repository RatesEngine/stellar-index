import { Award, Flame, TrendingDown, TrendingUp } from 'lucide-react';
import { twMerge } from 'tailwind-merge';

export type StreakIndicatorProps =
  | {
      kind: 'streak';
      direction: 'up' | 'down' | 'flat';
      days: number;
    }
  | {
      kind: 'ath';
      /** ISO time the ATH was hit. Used for the "2h ago" relative label. */
      at: string;
    }
  | {
      kind: 'atl';
      at: string;
    }
  | {
      kind: 'new';
      /** Anything within the last 24h gets the badge per design-inventory §6.4. */
      since: string;
    };

/**
 * Streak / ATH / ATL / new-listing chip — the punchy badges from
 * design-inventory §6.4. Each variant uses a distinct colour +
 * icon so they're spottable in dense lists.
 */
export function StreakIndicator(props: StreakIndicatorProps & { className?: string }) {
  const className = props.className ?? '';
  switch (props.kind) {
    case 'streak': {
      if (props.direction === 'flat' || props.days === 0) {
        return null;
      }
      const Icon = props.direction === 'up' ? TrendingUp : TrendingDown;
      const tone =
        props.direction === 'up'
          ? 'bg-up-subtle/50 text-up-strong'
          : 'bg-down-subtle/50 text-down-strong';
      return (
        <span
          className={twMerge(
            'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
            tone,
            className,
          )}
        >
          <Icon className="h-3 w-3" aria-hidden />
          {props.days} {props.days === 1 ? 'day' : 'days'} {props.direction}
        </span>
      );
    }
    case 'ath':
      return (
        <span
          className={twMerge(
            'inline-flex items-center gap-1 rounded-full bg-warn-50 px-2 py-0.5 text-xs font-medium text-warn-700',
            className,
          )}
          title={`All-time high reached ${props.at}`}
        >
          <Award className="h-3 w-3" aria-hidden />
          ATH {relativeTime(props.at)}
        </span>
      );
    case 'atl':
      return (
        <span
          className={twMerge(
            'inline-flex items-center gap-1 rounded-full bg-down-subtle px-2 py-0.5 text-xs font-medium text-down-strong',
            className,
          )}
          title={`All-time low reached ${props.at}`}
        >
          <TrendingDown className="h-3 w-3" aria-hidden />
          ATL {relativeTime(props.at)}
        </span>
      );
    case 'new':
      return (
        <span
          className={twMerge(
            'inline-flex items-center gap-1 rounded-full bg-purple-100 px-2 py-0.5 text-xs font-medium text-purple-800',
            className,
          )}
          title={`First seen ${props.since}`}
        >
          <Flame className="h-3 w-3" aria-hidden />
          new
        </span>
      );
  }
}

/**
 * Relative-time formatter — "2h ago", "3 days ago". Intentionally
 * lo-fi (no Intl.RelativeTimeFormat); good enough for design and
 * easy to swap for a richer version later.
 */
function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return '';
  const diffSec = Math.max(0, (Date.now() - then) / 1000);
  if (diffSec < 60) return `${Math.round(diffSec)}s ago`;
  if (diffSec < 3600) return `${Math.round(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.round(diffSec / 3600)}h ago`;
  return `${Math.round(diffSec / 86400)} day${
    diffSec >= 172800 ? 's' : ''
  } ago`;
}
