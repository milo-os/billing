/** Amounts arrive as decimal strings; display is en-US with the ISO currency. */

/** A decimal string as a number, `0` when missing or unreadable. */
export function parseAmount(value: string | undefined): number {
  const amount = Number.parseFloat(value ?? '');
  return Number.isFinite(amount) ? amount : 0;
}

/** `$1,250.00`. */
export function formatMoney(amount: number, currency: string): string {
  try {
    return new Intl.NumberFormat('en-US', { style: 'currency', currency }).format(amount);
  } catch {
    return `${amount.toFixed(2)} ${currency}`;
  }
}

/**
 * `$1.3M`, `$48.2K`, `$912`, `$0`: whole currency units, compact from a
 * thousand up, so tiles side by side read in one style. For headline tiles
 * and chart axes; tables use `formatMoney`.
 */
export function formatMoneyCompact(amount: number, currency: string): string {
  try {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      notation: Math.abs(amount) < 1000 ? 'standard' : 'compact',
      minimumFractionDigits: 0,
      maximumFractionDigits: Math.abs(amount) < 1000 ? 0 : 1,
    }).format(amount);
  } catch {
    return formatMoney(amount, currency);
  }
}

/** `+12%`, `−4%`, or undefined when there is nothing to compare against. */
export function formatChange(current: number, previous: number): string | undefined {
  if (previous <= 0) return undefined;
  const pct = Math.round(((current - previous) / previous) * 100);
  if (pct === 0) return 'flat';
  return `${pct > 0 ? '+' : '−'}${Math.abs(pct)}%`;
}

export function formatPercent(ratio: number): string {
  return `${Math.round(ratio * 1000) / 10}%`;
}
