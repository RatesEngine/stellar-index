import { twMerge } from 'tailwind-merge';

export type AccelerationArrowProps = {
  /**
   * First derivative of recent values: 'up' / 'down' / 'flat'.
   * The base direction is always rendered with the existing
   * direction styling.
   */
  direction: 'up' | 'down' | 'flat';
  /**
   * Second derivative: 'increasing' (momentum building),
   * 'flat' (steady), or 'decreasing' (slowing). Combines with
   * direction for the six glyph variants per design-inventory §6.6.
   */
  acceleration: 'increasing' | 'flat' | 'decreasing';
  className?: string;
};

/**
 * Acceleration arrow — combines first + second derivative into
 * one of six glyphs per design-inventory §6.6:
 *
 *   ↗↗   accelerating up      (+ +)
 *   ↗→   steady up            (+ flat)
 *   ↗↘   decelerating up      (+ −)
 *   ↘↘   accelerating down    (− −)
 *   ↘→   steady down          (− flat)
 *   ↘↗   recovering           (− +)
 *
 * Use sparingly on hero metrics; over-use makes pages noisy.
 */
export function AccelerationArrow({
  direction,
  acceleration,
  className,
}: AccelerationArrowProps) {
  if (direction === 'flat') {
    return (
      <span className={twMerge('text-xs text-slate-400', className)} aria-label="flat">
        →
      </span>
    );
  }

  const glyph = pickGlyph(direction, acceleration);
  const tone =
    direction === 'up' ? 'text-up-strong' : 'text-down-strong';

  return (
    <span
      className={twMerge('text-xs font-bold tabular-nums', tone, className)}
      aria-label={describe(direction, acceleration)}
      title={describe(direction, acceleration)}
    >
      {glyph}
    </span>
  );
}

function pickGlyph(
  direction: 'up' | 'down',
  acceleration: 'increasing' | 'flat' | 'decreasing',
): string {
  if (direction === 'up') {
    return acceleration === 'increasing'
      ? '↗↗'
      : acceleration === 'flat'
        ? '↗→'
        : '↗↘';
  }
  return acceleration === 'increasing'
    ? '↘↘' // accelerating down
    : acceleration === 'flat'
      ? '↘→'
      : '↘↗'; // recovering
}

function describe(
  direction: 'up' | 'down',
  acceleration: 'increasing' | 'flat' | 'decreasing',
): string {
  const map: Record<string, string> = {
    'up:increasing': 'accelerating up',
    'up:flat': 'steady up',
    'up:decreasing': 'decelerating up',
    'down:increasing': 'accelerating down',
    'down:flat': 'steady down',
    'down:decreasing': 'recovering',
  };
  return map[`${direction}:${acceleration}`] ?? '';
}
