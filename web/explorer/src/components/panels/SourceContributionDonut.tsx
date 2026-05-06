/**
 * SourceContributionDonut — the donut on every price card showing
 * which sources contributed to the VWAP. Per data-inventory §6.7.
 *
 * Pure SVG so it composes into static-exported pages. Hover surfaces
 * each source's name + weight via native title attributes (good
 * enough for v0; a richer tooltip layer comes later).
 */

export type SourceContributionDonutProps = {
  contributions: { name: string; weight: number; venue?: string }[];
  /** Hero size in px. Donut is rendered as a square. */
  size?: number;
  /** Stroke width of the ring. */
  thickness?: number;
};

export function SourceContributionDonut({
  contributions,
  size = 160,
  thickness = 24,
}: SourceContributionDonutProps) {
  const total = contributions.reduce((s, c) => s + c.weight, 0);
  if (total <= 0) return null;
  const r = (size - thickness) / 2;
  const cx = size / 2;
  const cy = size / 2;
  const circ = 2 * Math.PI * r;

  let offset = 0;
  const arcs = contributions.map((c, i) => {
    const fraction = c.weight / total;
    const length = fraction * circ;
    const arc = (
      <circle
        key={c.name}
        cx={cx}
        cy={cy}
        r={r}
        fill="none"
        stroke={pickColor(i)}
        strokeWidth={thickness}
        strokeDasharray={`${length} ${circ}`}
        strokeDashoffset={-offset}
        transform={`rotate(-90 ${cx} ${cy})`}
      >
        <title>
          {c.venue ?? c.name}: {(fraction * 100).toFixed(1)}%
        </title>
      </circle>
    );
    offset += length;
    return arc;
  });

  return (
    <div className="flex items-center gap-4">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label="Source contribution breakdown">
        <circle
          cx={cx}
          cy={cy}
          r={r}
          fill="none"
          stroke="currentColor"
          strokeOpacity={0.05}
          strokeWidth={thickness}
        />
        {arcs}
      </svg>
      <ul className="space-y-1 text-xs">
        {contributions
          .slice()
          .sort((a, b) => b.weight - a.weight)
          .map((c) => (
            <li key={c.name} className="flex items-center gap-2">
              <span
                className="inline-block h-2.5 w-2.5 rounded-sm"
                style={{ background: pickColor(originalIndex(c.name, contributions)) }}
                aria-hidden
              />
              <span className="font-medium">{c.venue ?? c.name}</span>
              <span className="font-mono tabular-nums text-slate-500">
                {((c.weight / total) * 100).toFixed(1)}%
              </span>
            </li>
          ))}
      </ul>
    </div>
  );
}

const PALETTE = [
  '#0ea5e9', // brand-500
  '#22c55e', // green-500
  '#a855f7', // purple-500
  '#f59e0b', // amber-500
  '#ef4444', // red-500
  '#14b8a6', // teal-500
  '#ec4899', // pink-500
  '#6366f1', // indigo-500
];

function pickColor(i: number): string {
  return PALETTE[i % PALETTE.length]!;
}

function originalIndex(name: string, all: { name: string }[]): number {
  return all.findIndex((c) => c.name === name);
}
