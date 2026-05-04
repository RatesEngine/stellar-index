// Number / date / currency formatters — Intl-aware, locale-respecting.

const PRICE_FORMATTER = new Intl.NumberFormat('en-US', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 8,
});

const COMPACT_FORMATTER = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 2,
});

const PCT_FORMATTER = new Intl.NumberFormat('en-US', {
  style: 'percent',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
  signDisplay: 'exceptZero',
});

export function formatPrice(value: number | string): string {
  const n = typeof value === 'string' ? parseFloat(value) : value;
  if (!Number.isFinite(n)) return '—';
  return PRICE_FORMATTER.format(n);
}

export function formatCompact(value: number | string): string {
  const n = typeof value === 'string' ? parseFloat(value) : value;
  if (!Number.isFinite(n)) return '—';
  return COMPACT_FORMATTER.format(n);
}

// Pass a fraction (0.0123 → "+1.23%"). Pass a percentage point if you
// already divided.
export function formatPctChange(fraction: number): string {
  if (!Number.isFinite(fraction)) return '—';
  return PCT_FORMATTER.format(fraction);
}

export function formatLedger(ledger: number): string {
  return `#${ledger.toLocaleString('en-US')}`;
}
