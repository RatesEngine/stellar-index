import type { ADRStatus } from '@/lib/adr';

const STYLES: Record<ADRStatus, string> = {
  Accepted: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400',
  Proposed: 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-400',
  Superseded: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400',
  Rejected: 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-400',
};

export function StatusBadge({ status }: { status: ADRStatus }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider ${STYLES[status] ?? STYLES.Proposed}`}
    >
      {status}
    </span>
  );
}
