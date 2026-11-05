/**
 * Invoice rules shared with staff-portal's per-account dashboard
 * (app/features/billing/lib/invoice-summary.ts and utils.ts there), so a
 * fleet total is the sum of what each account page shows.
 */
import { parseAmount } from './money';
import type { Invoice, InvoicePhase } from './types';

export const DAY_MS = 24 * 60 * 60 * 1000;

const AMBERFLO_INVOICE_KEY = 'amberflo.billing.miloapis.com/invoiceKey';
const OPENMETER_INVOICE_ID = 'openmeter.miloapis.com/invoiceId';
export const STRIPE_INVOICE_NUMBER = 'openmeter.miloapis.com/stripeInvoiceNumber';

const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * Leftovers nothing updates any more, which stay Open or Past due forever
 * with made-up totals: Amberflo's invoices, and the ones an older OpenMeter
 * provider named `<account>-YYYY-MM` (the current one adds a hash). Every
 * fleet sum must skip them.
 */
export function isStaleInvoice(invoice: Invoice): boolean {
  const annotations = invoice.metadata?.annotations ?? {};
  if (AMBERFLO_INVOICE_KEY in annotations) return true;
  const account = invoice.spec?.billingAccountRef?.name;
  const name = invoice.metadata?.name ?? '';
  return (
    OPENMETER_INVOICE_ID in annotations &&
    !!account &&
    new RegExp(`^${escapeRegExp(account)}-\\d{4}-\\d{2}$`).test(name)
  );
}

/** One with no phase yet is still being projected by the provider: Open. */
export function getInvoicePhase(invoice: Invoice): InvoicePhase {
  return invoice.status?.phase ?? 'Open';
}

export function isUnpaid(invoice: Invoice): boolean {
  const phase = getInvoicePhase(invoice);
  return phase === 'Open' || phase === 'PastDue';
}

/** What is still owed: `amountDue`, or the total before the provider sets it. */
export function getAmountOwed(invoice: Invoice): number {
  return invoice.status?.amountDue !== undefined
    ? parseAmount(invoice.status.amountDue)
    : parseAmount(invoice.status?.total);
}

/** Whole days since `dueDate`; 0 before it or without one. */
export function getDaysOverdue(invoice: Invoice, now: Date): number {
  const due = Date.parse(invoice.status?.dueDate ?? '');
  if (Number.isNaN(due)) return 0;
  return Math.max(0, Math.floor((now.getTime() - due) / DAY_MS));
}

/**
 * When the invoice was issued. Invoices carry no issue date; the provider
 * issues each one as its period closes, so the period's end stands in.
 */
export function getIssuedAt(invoice: Invoice): number | undefined {
  const end = Date.parse(invoice.spec?.period?.end ?? '');
  return Number.isNaN(end) ? undefined : end;
}

export function getPaidAt(invoice: Invoice): number | undefined {
  const paid = Date.parse(invoice.status?.paidAt ?? '');
  return Number.isNaN(paid) ? undefined : paid;
}

export function getDueAt(invoice: Invoice): number | undefined {
  const due = Date.parse(invoice.status?.dueDate ?? '');
  return Number.isNaN(due) ? undefined : due;
}

export type AgingBucket = 'notDue' | 'days1to30' | 'days31to60' | 'days61to90' | 'days90plus';

export const AGING_BUCKETS: { id: AgingBucket; label: string }[] = [
  { id: 'notDue', label: 'Not yet due' },
  { id: 'days1to30', label: '1–30 days' },
  { id: 'days31to60', label: '31–60 days' },
  { id: 'days61to90', label: '61–90 days' },
  { id: 'days90plus', label: 'Over 90 days' },
];

/** Past-due invoices count as at least a day overdue, as on the account page. */
export function getAgingBucket(invoice: Invoice, now: Date): AgingBucket {
  const days =
    getInvoicePhase(invoice) === 'PastDue' ? Math.max(1, getDaysOverdue(invoice, now)) : 0;
  if (days === 0) return 'notDue';
  if (days <= 30) return 'days1to30';
  if (days <= 60) return 'days31to60';
  if (days <= 90) return 'days61to90';
  return 'days90plus';
}
