// Re-export every design-system primitive so callers import from one
// barrel rather than the per-file paths.
//
// Per docs/architecture/explorer-data-inventory.md §6 these
// primitives compose throughout the site. Keep them small,
// presentational, and free of API/data-fetch concerns — every
// primitive takes its data via props and renders deterministically.

export { DirectionPill } from './DirectionPill';
export type { DirectionPillProps } from './DirectionPill';

export { MultiWindowDelta } from './MultiWindowDelta';
export type { MultiWindowDeltaProps, DeltaWindow } from './MultiWindowDelta';

export { Sparkline } from './Sparkline';
export type { SparklineProps } from './Sparkline';

export { StreakIndicator } from './StreakIndicator';
export type { StreakIndicatorProps } from './StreakIndicator';

export { RankBadge } from './RankBadge';
export type { RankBadgeProps } from './RankBadge';

export { AccelerationArrow } from './AccelerationArrow';
export type { AccelerationArrowProps } from './AccelerationArrow';
