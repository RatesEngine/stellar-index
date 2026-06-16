import type { ADRStatus } from '@/lib/adr';

const STYLES: Record<ADRStatus, string> = {
  Accepted: 'bg-up-subtle text-up',
  Proposed: 'bg-warn-50 text-warn-700',
  Superseded: 'bg-surface-subtle text-ink-body',
  Rejected: 'bg-down-subtle text-down',
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
