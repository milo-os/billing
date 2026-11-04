import { buildFixtures } from './fixtures';
import { plural, summarizeFleet, type FleetInput } from './fleet';
import { formatMoneyCompact } from './money';
import { DAY_MS, getAgingBucket, isStaleInvoice } from './invoices';
import type { BillingAccount, Invoice, PaymentMethod } from './types';
import { describe, expect, test } from 'bun:test';

const NOW = new Date('2026-11-15T12:00:00Z');
const ago = (days: number) => new Date(NOW.getTime() - days * DAY_MS).toISOString();
const ahead = (days: number) => new Date(NOW.getTime() + days * DAY_MS).toISOString();

const account = (org: string, extra: Partial<BillingAccount['status']> = {}): BillingAccount => ({
  metadata: { name: `${org}-main`, namespace: `organization-${org}` },
  spec: { currencyCode: 'USD', defaultPaymentMethodRef: { name: `${org}-card` } },
  status: { phase: 'Ready', linkedProjectsCount: 1, ...extra },
});

const card = (org: string, extra: Partial<PaymentMethod['status']> = {}): PaymentMethod => ({
  metadata: { name: `${org}-card`, namespace: `organization-${org}` },
  spec: { billingAccountRef: { name: `${org}-main` } },
  status: { phase: 'Active', details: { type: 'card', card: { expiryMonth: 12, expiryYear: 2030 } }, ...extra },
});

const invoice = (org: string, name: string, status: Invoice['status'], issued = ago(10)): Invoice => ({
  metadata: { name: `${org}-main-${name}`, namespace: `organization-${org}`, annotations: { 'openmeter.miloapis.com/invoiceId': name } },
  spec: { billingAccountRef: { name: `${org}-main` }, period: { start: ago(40), end: issued } },
  status: { currencyCode: 'USD', ...status },
});

const fleet = (input: Partial<FleetInput>): FleetInput => ({
  accounts: [],
  invoices: [],
  paymentMethods: [],
  ...input,
});

describe('summarizeFleet money', () => {
  test('splits outstanding into not-yet-due and past due, and ranks collections by exposure', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a'), account('b'), account('c')],
        paymentMethods: [card('a'), card('b'), card('c')],
        invoices: [
          invoice('a', 'open', { phase: 'Open', total: '100', amountDue: '100', dueDate: ahead(5) }),
          invoice('b', 'late', { phase: 'PastDue', total: '200', amountDue: '200', dueDate: ago(10) }, ago(40)),
          invoice('c', 'later', { phase: 'PastDue', total: '50', amountDue: '50', dueDate: ago(70) }, ago(100)),
        ],
      }),
      NOW
    );
    expect(s.money.outstanding).toBe(350);
    expect(s.money.notYetDue).toBe(100);
    expect(s.money.pastDue).toBe(250);
    expect(s.money.pastDueAccounts).toBe(2);
    expect(s.money.oldestDaysOverdue).toBe(70);
    // c: $50 × 70 days late outranks b: $200 × 10.
    expect(s.collections.map((c) => c.account.name)).toEqual(['c-main', 'b-main']);
    expect(s.aging.find((r) => r.id === 'days61to90')?.total).toBe(50);
    expect(s.attention.find((a) => a.id === 'past-due')?.count).toBe(2);
  });

  test('counts collected and invoiced in rolling 30-day windows', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a')],
        invoices: [
          invoice('a', 'recent', { phase: 'Paid', total: '80', amountPaid: '80', amountDue: '0', paidAt: ago(3), dueDate: ago(4) }, ago(5)),
          invoice('a', 'older', { phase: 'Paid', total: '40', amountPaid: '40', amountDue: '0', paidAt: ago(40), dueDate: ago(41) }, ago(45)),
        ],
      }),
      NOW
    );
    expect(s.money.collected30d).toBe(80);
    expect(s.money.collectedPrev30d).toBe(40);
    expect(s.money.invoiced30d).toBe(80);
    expect(s.money.invoicedPrev30d).toBe(40);
    expect(s.money.collectionRate).toBe(1);
    expect(s.accounts[0].lifetimePaid).toBe(120);
  });

  test('collection rate counts invoices paid before they fall due', () => {
    // Issued Nov 1, due Dec 1, paid Nov 2: paid, so it counts, though not yet due.
    const s = summarizeFleet(
      fleet({
        accounts: [account('a'), account('b')],
        invoices: [
          invoice('a', 'early', { phase: 'Paid', total: '90', amountPaid: '90', amountDue: '0', paidAt: ago(13), dueDate: ahead(16) }, ago(14)),
          invoice('b', 'late', { phase: 'PastDue', total: '10', amountDue: '10', dueDate: ago(3) }, ago(33)),
          invoice('b', 'open', { phase: 'Open', total: '500', amountDue: '500', dueDate: ahead(20) }, ago(10)),
        ],
      }),
      NOW
    );
    // 90 paid of 90 + 10 overdue; the open, not-yet-due 500 is left out.
    expect(s.money.collectionRate).toBe(0.9);
  });

  test('skips stale and void invoices, and other currencies', () => {
    const stale = invoice('a', 'x', { phase: 'PastDue', total: '1000', amountDue: '1000' });
    stale.metadata!.annotations = { 'amberflo.billing.miloapis.com/invoiceKey': 'k' };
    const oldOpenMeter = invoice('a', '2025-01', { phase: 'PastDue', total: '1000', amountDue: '1000' });
    const s = summarizeFleet(
      fleet({
        accounts: [account('a')],
        invoices: [
          stale,
          oldOpenMeter,
          invoice('a', 'void', { phase: 'Void', total: '500' }),
          invoice('a', 'eur', { phase: 'Open', total: '70', amountDue: '70', currencyCode: 'EUR' }),
        ],
      }),
      NOW
    );
    expect(isStaleInvoice(stale)).toBe(true);
    expect(isStaleInvoice(oldOpenMeter)).toBe(true);
    expect(s.money.outstanding).toBe(0);
    expect(s.otherCurrencyInvoices).toBe(1);
  });
});

describe('deleted accounts', () => {
  test('a deleted account\'s past-due invoice counts as an account and is listed', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [],
        invoices: [invoice('gone', 'x', { phase: 'PastDue', total: '25', amountDue: '25', dueDate: ago(9) })],
      }),
      NOW
    );
    expect(s.money.pastDue).toBe(25);
    expect(s.money.pastDueAccounts).toBe(1);
    expect(s.collections).toHaveLength(1);
    expect(s.attention.find((a) => a.id === 'past-due')?.entries[0].reason).toBe(
      'Account deleted · $25.00 past due · 9 days late'
    );
    // Not an account any more: not in Top accounts or the fleet's make-up.
    expect(s.accounts).toHaveLength(0);
    expect(s.composition.total).toBe(0);
  });
});

describe('summarizeFleet attention', () => {
  test('flags card accounts with projects that have nothing to charge', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a'), account('b'), account('idle', { linkedProjectsCount: 0 })],
        paymentMethods: [card('a', { phase: 'Failed' })],
      }),
      NOW
    );
    // a: default card failed; b: no card at all. idle has no projects.
    const item = s.attention.find((a) => a.id === 'no-payment-method');
    expect(item?.count).toBe(2);
    // Each account behind the count, linking to its own page, with why.
    expect(item?.entries.map((e) => [e.href, e.reason])).toEqual([
      ['/customers/billing-accounts/a/a-main', 'Default card failed · 1 project'],
      ['/customers/billing-accounts/b/b-main', 'No payment method · 1 project'],
    ]);
  });

  test('past-due entries say how much and how late, worst first', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a'), account('b')],
        paymentMethods: [card('a'), card('b')],
        invoices: [
          invoice('a', 'x', { phase: 'PastDue', total: '100', amountDue: '100', dueDate: ago(5) }),
          invoice('b', 'y', { phase: 'PastDue', total: '40', amountDue: '40', dueDate: ago(40) }),
        ],
      }),
      NOW
    );
    expect(s.attention.find((a) => a.id === 'past-due')?.entries.map((e) => e.reason)).toEqual([
      '$40.00 past due · 40 days late',
      '$100.00 past due · 5 days late',
    ]);
  });

  test('pending applications link to their review page', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a')],
        paymentMethods: [card('a')],
        applications: [
          {
            metadata: { name: 'a-app', namespace: 'organization-a', creationTimestamp: ago(2) },
            spec: { billingAccountRef: { name: 'a-main' }, organization: { legalName: 'A Ltd' } },
            status: { phase: 'Pending' },
          },
        ],
      }),
      NOW
    );
    const entry = s.attention.find((a) => a.id === 'applications')?.entries[0];
    expect(entry?.label).toBe('A Ltd');
    expect(entry?.href).toBe('/customers/billing-accounts/applications/a/a-app');
  });

  test('flags cards that expire this month', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a')],
        paymentMethods: [card('a', { details: { type: 'card', card: { expiryMonth: 11, expiryYear: 2026 } } })],
      }),
      NOW
    );
    expect(s.attention.find((a) => a.id === 'card-expiry')?.count).toBe(1);
  });

  test('invoice-terms accounts are not expected to have a card', () => {
    const s = summarizeFleet(
      fleet({
        accounts: [account('a', { paymentArrangement: { type: 'Invoice', invoice: { netDays: 45 } } })],
      }),
      NOW
    );
    expect(s.attention.find((a) => a.id === 'no-payment-method')).toBeUndefined();
    expect(s.accounts[0].billingMode).toBe('invoice');
    expect(s.accounts[0].netDays).toBe(45);
  });
});

describe('aging', () => {
  test('a past-due invoice is at least a day overdue', () => {
    const inv = invoice('a', 'x', { phase: 'PastDue', dueDate: ahead(1) });
    expect(getAgingBucket(inv, NOW)).toBe('days1to30');
  });
});

describe('fixtures', () => {
  test('build a fleet whose totals are internally consistent', () => {
    const f = buildFixtures(NOW);
    const s = summarizeFleet(f, NOW);
    const agingTotal = s.aging.reduce((sum, r) => sum + r.total, 0);
    expect(Math.round(agingTotal * 100)).toBe(Math.round(s.money.outstanding * 100));
    expect(s.money.pastDue).toBeLessThan(999999); // the stale Amberflo invoice is skipped
    expect(s.monthly).toHaveLength(12);
    expect(s.composition.total).toBe(f.accounts.length);
  });
});

describe('formatting', () => {
  test('plural takes an irregular form', () => {
    expect(plural(1, 'batch', 'batches')).toBe('1 batch');
    expect(plural(2, 'batch', 'batches')).toBe('2 batches');
    expect(plural(2, 'account')).toBe('2 accounts');
  });

  test('compact money reads in one style across magnitudes', () => {
    expect(formatMoneyCompact(0, 'USD')).toBe('$0');
    expect(formatMoneyCompact(912.4, 'USD')).toBe('$912');
    expect(formatMoneyCompact(34_120, 'USD')).toBe('$34.1K');
    expect(formatMoneyCompact(1_250_000, 'USD')).toBe('$1.3M');
  });
});
