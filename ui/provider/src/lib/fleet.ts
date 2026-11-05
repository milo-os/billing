/**
 * The fleet billing overview's numbers, computed from billing's resources
 * listed across every organization. Pure: `summarizeFleet` takes the lists
 * and a fixed `now`, so the page and the tests agree on every figure.
 *
 * Money is reported in one currency, the one most accounts bill in.
 * Invoices in any other currency are counted and left out rather than
 * summed across currencies.
 */
import {
  AGING_BUCKETS,
  DAY_MS,
  getAgingBucket,
  getAmountOwed,
  getDaysOverdue,
  getDueAt,
  getInvoicePhase,
  getIssuedAt,
  getPaidAt,
  isStaleInvoice,
  isUnpaid,
  STRIPE_INVOICE_NUMBER,
  type AgingBucket,
} from './invoices';
import { formatMoney, parseAmount } from './money';
import type {
  BillingAccount,
  BillingArrangementApplication,
  BillingEntitlement,
  CreditGrant,
  Invoice,
  Offer,
  Organization,
  PaymentMethod,
} from './types';

export interface FleetInput {
  accounts: BillingAccount[];
  invoices: Invoice[];
  paymentMethods: PaymentMethod[];
  /** Each optional list is undefined when staff can't read it; its sections hide. */
  applications?: BillingArrangementApplication[];
  entitlements?: BillingEntitlement[];
  offers?: Offer[];
  creditGrants?: CreditGrant[];
  organizations?: Organization[];
}

export type BillingMode = 'invoice' | 'card';

export interface AccountRow {
  /** `<namespace>/<name>`: account names are only unique per organization. */
  key: string;
  name: string;
  namespace: string;
  orgName: string;
  orgDisplayName: string;
  displayName: string;
  phase: string;
  billingMode: BillingMode;
  netDays?: number;
  linkedProjects: number;
  offerName?: string;
  currency: string;
  /** Invoiced in the last 365 days, before credits. */
  billed12m: number;
  lifetimePaid: number;
  outstanding: number;
  pastDue: number;
  maxDaysOverdue: number;
  /**
   * The account is gone but still has invoices. Its money stays in every sum
   * (it's still owed or was paid), but it isn't counted as an account.
   */
  deleted?: boolean;
}

export interface CollectionRow {
  key: string;
  account: AccountRow;
  invoiceName: string;
  stripeNumber?: string;
  owed: number;
  daysOverdue: number;
  dueAt?: number;
}

export interface MonthPoint {
  /** `YYYY-MM`, UTC. */
  month: string;
  label: string;
  invoiced: number;
  collected: number;
}

export interface AgingRow {
  id: AgingBucket;
  label: string;
  card: number;
  invoice: number;
  total: number;
}

export type AttentionSeverity = 'error' | 'warning' | 'info';

/** One account (or application) behind an attention item. */
export interface AttentionEntry {
  key: string;
  /** Who: the account's display name. */
  label: string;
  /** Org display name and account name, to tell similar names apart. */
  sublabel?: string;
  /** Why this one is on the list, e.g. "Default card failed". */
  reason: string;
  /** Where to fix it: the account page, or the application to review. */
  href: string;
}

export interface AttentionItem {
  id: string;
  severity: AttentionSeverity;
  count: number;
  title: string;
  detail?: string;
  /** Every account behind the count, worst first; the panel expands into them. */
  entries: AttentionEntry[];
}

export interface Money {
  collected30d: number;
  collectedPrev30d: number;
  invoiced30d: number;
  invoicedPrev30d: number;
  /** Owed on every unpaid invoice. */
  outstanding: number;
  /** Owed on Open invoices, not yet past their due date. */
  notYetDue: number;
  pastDue: number;
  pastDueAccounts: number;
  pastDueInvoices: number;
  oldestDaysOverdue: number;
  /**
   * Of the invoices issued in the last 180 days that are settled or overdue,
   * the share of money paid: paid ÷ (paid + still owed past due). Open
   * invoices not yet due are left out, since nobody has failed to pay them,
   * and an invoice paid before its due date counts as paid. Undefined with
   * nothing to measure.
   */
  collectionRate?: number;
  /** Days sales outstanding: outstanding ÷ invoiced in the last 90 days × 90. */
  dso?: number;
}

export interface Composition {
  total: number;
  byPhase: { phase: string; count: number }[];
  byMode: Record<BillingMode, number>;
  withProjects: number;
  /** Undefined when entitlements couldn't be read. */
  byOffer?: { name: string; displayName: string; count: number }[];
  credits?: { active: number; granted: number; expiringSoon: number };
}

export interface FleetSummary {
  currency: string;
  /** Invoices left out because they bill in another currency. */
  otherCurrencyInvoices: number;
  money: Money;
  monthly: MonthPoint[];
  aging: AgingRow[];
  accounts: AccountRow[];
  collections: CollectionRow[];
  attention: AttentionItem[];
  composition: Composition;
}

const ORG_NAMESPACE_PREFIX = 'organization-';
const DISPLAY_NAME = 'kubernetes.io/display-name';
const SOON_DAYS = 30;

export const accountKey = (namespace: string | undefined, name: string | undefined) =>
  `${namespace ?? ''}/${name ?? ''}`;

const orgFromNamespace = (namespace: string) =>
  namespace.startsWith(ORG_NAMESPACE_PREFIX)
    ? namespace.slice(ORG_NAMESPACE_PREFIX.length)
    : namespace;

/** Billed by invoice when staff granted terms that haven't ended. */
export function getBillingMode(account: BillingAccount, now: Date): BillingMode {
  const arrangement = account.status?.paymentArrangement;
  if (!arrangement || arrangement.type !== 'Invoice') return 'card';
  const ends = Date.parse(arrangement.endsAt ?? '');
  return Number.isNaN(ends) || ends > now.getTime() ? 'invoice' : 'card';
}

/** The card charges go to: the default one, or the only one. */
function getChargedMethod(account: BillingAccount, methods: PaymentMethod[]) {
  const defaultName = account.spec?.defaultPaymentMethodRef?.name;
  if (defaultName) return methods.find((m) => m.metadata?.name === defaultName);
  return methods.length === 1 ? methods[0] : undefined;
}

/** Days until the card's expiry month ends; negative once expired. */
function daysUntilCardExpiry(method: PaymentMethod, now: Date): number | undefined {
  const card = method.status?.details?.card;
  if (!card?.expiryMonth || !card.expiryYear) return undefined;
  // The first instant after the expiry month.
  const expires = Date.UTC(card.expiryYear, card.expiryMonth, 1);
  return Math.floor((expires - now.getTime()) / DAY_MS);
}

const monthKey = (ms: number) => new Date(ms).toISOString().slice(0, 7);

function lastTwelveMonths(now: Date): MonthPoint[] {
  const months: MonthPoint[] = [];
  for (let i = 11; i >= 0; i--) {
    const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - i, 1));
    months.push({
      month: monthKey(d.getTime()),
      label: d.toLocaleDateString('en-US', { month: 'short', timeZone: 'UTC' }),
      invoiced: 0,
      collected: 0,
    });
  }
  return months;
}

function mostCommon(values: string[], fallback: string): string {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  let best = fallback;
  let bestCount = 0;
  for (const [v, c] of counts) {
    if (c > bestCount) {
      best = v;
      bestCount = c;
    }
  }
  return best;
}

export function summarizeFleet(input: FleetInput, now: Date): FleetSummary {
  const nowMs = now.getTime();
  const soonMs = nowMs + SOON_DAYS * DAY_MS;

  const orgDisplayNames = new Map(
    (input.organizations ?? []).map((o) => [
      o.metadata?.name ?? '',
      o.metadata?.annotations?.[DISPLAY_NAME] ?? o.metadata?.name ?? '',
    ])
  );
  const offerDisplayNames = new Map(
    (input.offers ?? []).map((o) => [o.metadata?.name ?? '', o.spec?.displayName ?? ''])
  );
  const offerByAccount = new Map(
    (input.entitlements ?? []).map((e) => [
      accountKey(e.metadata?.namespace, e.spec?.billingAccountRef?.name),
      e.spec?.offerRef?.name,
    ])
  );
  const methodsByAccount = new Map<string, PaymentMethod[]>();
  for (const m of input.paymentMethods) {
    const key = accountKey(m.metadata?.namespace, m.spec?.billingAccountRef?.name);
    methodsByAccount.set(key, [...(methodsByAccount.get(key) ?? []), m]);
  }

  const currency = mostCommon(
    input.accounts.map((a) => a.spec?.currencyCode || 'USD'),
    'USD'
  );

  const rows = new Map<string, AccountRow>();
  for (const account of input.accounts) {
    const namespace = account.metadata?.namespace ?? '';
    const name = account.metadata?.name ?? '';
    const orgName = orgFromNamespace(namespace);
    const orgDisplayName = orgDisplayNames.get(orgName) || orgName;
    const key = accountKey(namespace, name);
    const offerName = offerByAccount.get(key);
    rows.set(key, {
      key,
      name,
      namespace,
      orgName,
      orgDisplayName,
      displayName: account.spec?.contactInfo?.businessName || orgDisplayName || name,
      phase: account.status?.phase ?? 'Provisioning',
      billingMode: getBillingMode(account, now),
      netDays: account.status?.paymentArrangement?.invoice?.netDays ?? account.spec?.paymentTerms?.netDays,
      linkedProjects: account.status?.linkedProjectsCount ?? 0,
      offerName: offerName ? offerDisplayNames.get(offerName) || offerName : undefined,
      currency: account.spec?.currencyCode || 'USD',
      billed12m: 0,
      lifetimePaid: 0,
      outstanding: 0,
      pastDue: 0,
      maxDaysOverdue: 0,
    });
  }

  const money: Money = {
    collected30d: 0,
    collectedPrev30d: 0,
    invoiced30d: 0,
    invoicedPrev30d: 0,
    outstanding: 0,
    notYetDue: 0,
    pastDue: 0,
    pastDueAccounts: 0,
    pastDueInvoices: 0,
    oldestDaysOverdue: 0,
  };
  const monthly = lastTwelveMonths(now);
  const monthIndex = new Map(monthly.map((m, i) => [m.month, i]));
  const aging = new Map<AgingBucket, AgingRow>(
    AGING_BUCKETS.map((b) => [b.id, { ...b, card: 0, invoice: 0, total: 0 }])
  );
  const collections: CollectionRow[] = [];
  let otherCurrencyInvoices = 0;
  let dueCollected = 0;
  let dueOwed = 0;
  let invoiced90d = 0;

  for (const invoice of input.invoices) {
    if (isStaleInvoice(invoice)) continue;
    const key = accountKey(invoice.metadata?.namespace, invoice.spec?.billingAccountRef?.name);
    const row = rows.get(key) ?? addDeletedAccountRow(rows, invoice, orgDisplayNames);
    const invoiceCurrency = invoice.status?.currencyCode || row?.currency || 'USD';
    if (invoiceCurrency !== currency) {
      otherCurrencyInvoices++;
      continue;
    }
    const phase = getInvoicePhase(invoice);
    if (phase === 'Void') continue;

    const total = parseAmount(invoice.status?.total);
    const paid = parseAmount(invoice.status?.amountPaid);
    const owed = isUnpaid(invoice) ? getAmountOwed(invoice) : 0;
    const issuedAt = getIssuedAt(invoice);
    const paidAt = getPaidAt(invoice);
    const dueAt = getDueAt(invoice);

    if (issuedAt !== undefined) {
      const age = nowMs - issuedAt;
      if (age >= 0 && age < 30 * DAY_MS) money.invoiced30d += total;
      else if (age >= 30 * DAY_MS && age < 60 * DAY_MS) money.invoicedPrev30d += total;
      if (age >= 0 && age < 90 * DAY_MS) invoiced90d += total;
      if (age >= 0 && age < 365 * DAY_MS && row) row.billed12m += total;
      const i = monthIndex.get(monthKey(issuedAt));
      if (i !== undefined) monthly[i].invoiced += total;
    }
    if (paidAt !== undefined && paid > 0) {
      const age = nowMs - paidAt;
      if (age >= 0 && age < 30 * DAY_MS) money.collected30d += paid;
      else if (age >= 30 * DAY_MS && age < 60 * DAY_MS) money.collectedPrev30d += paid;
      const i = monthIndex.get(monthKey(paidAt));
      if (i !== undefined) monthly[i].collected += paid;
    }
    if (
      issuedAt !== undefined &&
      nowMs - issuedAt < 180 * DAY_MS &&
      (phase === 'Paid' || phase === 'PastDue')
    ) {
      dueCollected += paid;
      dueOwed += owed;
    }
    if (row) row.lifetimePaid += paid;

    if (owed > 0) {
      money.outstanding += owed;
      const bucket = aging.get(getAgingBucket(invoice, now))!;
      bucket[row?.billingMode ?? 'card'] += owed;
      bucket.total += owed;
      if (row) row.outstanding += owed;
      if (phase === 'PastDue') {
        const days = getDaysOverdue(invoice, now);
        money.pastDue += owed;
        money.pastDueInvoices++;
        money.oldestDaysOverdue = Math.max(money.oldestDaysOverdue, days);
        if (row) {
          row.pastDue += owed;
          row.maxDaysOverdue = Math.max(row.maxDaysOverdue, days);
          collections.push({
            key: `${key}/${invoice.metadata?.name ?? ''}`,
            account: row,
            invoiceName: invoice.metadata?.name ?? '',
            stripeNumber: invoice.metadata?.annotations?.[STRIPE_INVOICE_NUMBER],
            owed,
            daysOverdue: days,
            dueAt,
          });
        }
      } else {
        money.notYetDue += owed;
      }
    }
  }

  const allRows = [...rows.values()];
  // Every account with money past due, deleted ones too, so the count agrees
  // with the past-due total.
  money.pastDueAccounts = allRows.filter((a) => a.pastDue > 0).length;
  const accounts = allRows.filter((a) => !a.deleted);
  if (dueCollected + dueOwed > 0) money.collectionRate = dueCollected / (dueCollected + dueOwed);
  if (invoiced90d > 0) money.dso = (money.outstanding / invoiced90d) * 90;

  // Biggest exposure first: what's owed, weighted by how late it is.
  collections.sort(
    (a, b) => b.owed * Math.max(1, b.daysOverdue) - a.owed * Math.max(1, a.daysOverdue)
  );

  return {
    currency,
    otherCurrencyInvoices,
    money,
    monthly,
    aging: [...aging.values()],
    accounts,
    collections,
    attention: buildAttention(input, rows, methodsByAccount, money, currency, now, soonMs),
    composition: buildComposition(input, accounts, currency, nowMs, soonMs),
  };
}

/** staff-portal's page for one billing account. */
export const accountHref = (orgName: string, accountName: string) =>
  `/customers/billing-accounts/${encodeURIComponent(orgName)}/${encodeURIComponent(accountName)}`;

/**
 * A row for an account that no longer exists but still has invoices, so
 * their money is counted and attributed rather than dropped.
 */
function addDeletedAccountRow(
  rows: Map<string, AccountRow>,
  invoice: Invoice,
  orgDisplayNames: Map<string, string>
): AccountRow {
  const namespace = invoice.metadata?.namespace ?? '';
  const name = invoice.spec?.billingAccountRef?.name ?? '';
  const orgName = orgFromNamespace(namespace);
  const row: AccountRow = {
    key: accountKey(namespace, name),
    name,
    namespace,
    orgName,
    orgDisplayName: orgDisplayNames.get(orgName) || orgName,
    displayName: name,
    phase: 'Deleted',
    billingMode: 'card',
    linkedProjects: 0,
    currency: invoice.status?.currencyCode || 'USD',
    billed12m: 0,
    lifetimePaid: 0,
    outstanding: 0,
    pastDue: 0,
    maxDaysOverdue: 0,
    deleted: true,
  };
  rows.set(row.key, row);
  return row;
}

function entryFor(row: AccountRow, reason: string): AttentionEntry {
  return {
    key: row.key,
    label: row.displayName,
    sublabel:
      row.orgDisplayName !== row.displayName ? `${row.orgDisplayName} · ${row.name}` : row.name,
    reason,
    href: accountHref(row.orgName, row.name),
  };
}

const fmtDate = (ms: number) =>
  new Date(ms).toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });

function buildAttention(
  input: FleetInput,
  rows: Map<string, AccountRow>,
  methodsByAccount: Map<string, PaymentMethod[]>,
  money: Money,
  currency: string,
  now: Date,
  soonMs: number
): AttentionItem[] {
  const nowMs = now.getTime();
  const items: AttentionItem[] = [];

  const pastDue = [...rows.values()]
    .filter((r) => r.pastDue > 0)
    .sort((a, b) => b.pastDue * Math.max(1, b.maxDaysOverdue) - a.pastDue * Math.max(1, a.maxDaysOverdue));
  if (pastDue.length > 0) {
    items.push({
      id: 'past-due',
      severity: 'error',
      count: pastDue.length,
      title: `${plural(pastDue.length, 'account')} past due`,
      detail: `${plural(money.pastDueInvoices, 'invoice')}, oldest ${plural(money.oldestDaysOverdue, 'day')} overdue`,
      entries: pastDue.map((r) =>
        entryFor(
          r,
          `${r.deleted ? 'Account deleted · ' : ''}${formatMoney(r.pastDue, currency)} past due · ${plural(r.maxDaysOverdue, 'day')} late`
        )
      ),
    });
  }

  // Card-paying accounts that run projects but have nothing to charge, and
  // the cards about to stop working.
  const unchargeable: AttentionEntry[] = [];
  const expiring: { entry: AttentionEntry; days: number }[] = [];
  for (const account of input.accounts) {
    const key = accountKey(account.metadata?.namespace, account.metadata?.name);
    const row = rows.get(key);
    if (!row || row.billingMode !== 'card' || row.linkedProjects === 0) continue;
    if (row.phase === 'Archived') continue;
    const methods = methodsByAccount.get(key) ?? [];
    const charged = getChargedMethod(account, methods);
    const projects = plural(row.linkedProjects, 'project');
    if (methods.length === 0) {
      unchargeable.push(entryFor(row, `No payment method · ${projects}`));
      continue;
    }
    if (charged?.status?.phase === 'Failed') {
      const why = charged.status.failureReason ? ` (${charged.status.failureReason})` : '';
      unchargeable.push(entryFor(row, `Default card failed${why} · ${projects}`));
      continue;
    }
    if (!methods.some((m) => m.status?.phase === 'Active')) {
      unchargeable.push(entryFor(row, `No active card (${plural(methods.length, 'pending card')}) · ${projects}`));
      continue;
    }
    if (!charged) {
      unchargeable.push(entryFor(row, `No default card among ${methods.length} · ${projects}`));
      continue;
    }
    const days = daysUntilCardExpiry(charged, now);
    if (days === undefined || days > SOON_DAYS) continue;
    const card = charged.status?.details?.card;
    const which = card?.last4 ? `Card ending ${card.last4}` : 'Default card';
    const expiry = card ? `${String(card.expiryMonth).padStart(2, '0')}/${card.expiryYear}` : '';
    expiring.push({
      entry: entryFor(row, days < 0 ? `${which} expired ${expiry}` : `${which} expires ${expiry}`),
      days,
    });
  }
  if (unchargeable.length > 0) {
    items.push({
      id: 'no-payment-method',
      severity: 'error',
      count: unchargeable.length,
      title: `${plural(unchargeable.length, 'account')} with projects can't be charged`,
      detail: 'Pays by card, but has no card that works',
      entries: unchargeable,
    });
  }
  if (expiring.length > 0) {
    const expired = expiring.filter((e) => e.days < 0).length;
    items.push({
      id: 'card-expiry',
      severity: expired > 0 ? 'error' : 'warning',
      count: expiring.length,
      title: `${plural(expiring.length, 'card')} expired or expiring within ${SOON_DAYS} days`,
      detail: expired > 0 ? `${expired} already expired` : undefined,
      entries: expiring.sort((a, b) => a.days - b.days).map((e) => e.entry),
    });
  }

  if (input.applications) {
    const pending = input.applications.filter((a) => (a.status?.phase ?? 'Pending') === 'Pending');
    if (pending.length > 0) {
      items.push({
        id: 'applications',
        severity: 'warning',
        count: pending.length,
        title: `${plural(pending.length, 'payment terms application')} waiting for review`,
        entries: pending
          .sort((a, b) =>
            (a.metadata?.creationTimestamp ?? '').localeCompare(b.metadata?.creationTimestamp ?? '')
          )
          .map((a) => {
            const namespace = a.metadata?.namespace ?? '';
            const org = orgFromNamespace(namespace);
            const row = rows.get(accountKey(namespace, a.spec?.billingAccountRef?.name));
            const created = Date.parse(a.metadata?.creationTimestamp ?? '');
            return {
              key: `${namespace}/${a.metadata?.name ?? ''}`,
              label: a.spec?.organization?.legalName || row?.displayName || org,
              sublabel: row ? `${row.orgDisplayName} · ${row.name}` : org,
              reason: Number.isNaN(created) ? 'Waiting for review' : `Applied ${fmtDate(created)}`,
              href: `/customers/billing-accounts/applications/${encodeURIComponent(org)}/${encodeURIComponent(a.metadata?.name ?? '')}`,
            };
          }),
      });
    }
  }

  const endingTerms = input.accounts
    .map((a) => ({
      row: rows.get(accountKey(a.metadata?.namespace, a.metadata?.name)),
      ends: Date.parse(a.status?.paymentArrangement?.endsAt ?? ''),
    }))
    .filter((t): t is { row: AccountRow; ends: number } =>
      !!t.row && !Number.isNaN(t.ends) && t.ends > nowMs && t.ends <= soonMs
    )
    .sort((a, b) => a.ends - b.ends);
  if (endingTerms.length > 0) {
    items.push({
      id: 'terms-ending',
      severity: 'info',
      count: endingTerms.length,
      title: `${plural(endingTerms.length, 'invoice arrangement')} ending within ${SOON_DAYS} days`,
      detail: 'These accounts go back to paying by card',
      entries: endingTerms.map((t) => entryFor(t.row, `Terms end ${fmtDate(t.ends)}`)),
    });
  }

  if (input.creditGrants) {
    const expiringGrants = input.creditGrants
      .map((g) => ({ g, expires: Date.parse(g.status?.expiresAt ?? '') }))
      .filter(
        ({ g, expires }) =>
          g.status?.phase === 'Active' && !Number.isNaN(expires) && expires > nowMs && expires <= soonMs
      )
      .sort((a, b) => a.expires - b.expires);
    if (expiringGrants.length > 0) {
      items.push({
        id: 'credits-expiring',
        severity: 'info',
        count: expiringGrants.length,
        title: `${plural(expiringGrants.length, 'credit grant')} expiring within ${SOON_DAYS} days`,
        entries: expiringGrants.flatMap(({ g, expires }) => {
          const row = rows.get(accountKey(g.metadata?.namespace, g.spec?.billingAccountRef?.name));
          if (!row) return [];
          const amount = formatMoney(parseAmount(g.spec?.amount), g.spec?.currency || currency);
          return [{ ...entryFor(row, `${amount} credit expires ${fmtDate(expires)}`), key: `${row.key}/${g.metadata?.name}` }];
        }),
      });
    }
  }

  return items;
}

function buildComposition(
  input: FleetInput,
  accounts: AccountRow[],
  currency: string,
  nowMs: number,
  soonMs: number
): Composition {
  const phases = ['Ready', 'Provisioning', 'Suspended', 'Archived'];
  const phaseCounts = new Map<string, number>(phases.map((p) => [p, 0]));
  for (const a of accounts) phaseCounts.set(a.phase, (phaseCounts.get(a.phase) ?? 0) + 1);

  const live = accounts.filter((a) => a.phase !== 'Archived');
  const composition: Composition = {
    total: accounts.length,
    byPhase: [...phaseCounts].map(([phase, count]) => ({ phase, count })),
    byMode: {
      card: live.filter((a) => a.billingMode === 'card').length,
      invoice: live.filter((a) => a.billingMode === 'invoice').length,
    },
    withProjects: live.filter((a) => a.linkedProjects > 0).length,
  };

  if (input.entitlements) {
    const offerCounts = new Map<string, { name: string; displayName: string; count: number }>();
    for (const a of live) {
      const label = a.offerName ?? 'No offer';
      const entry = offerCounts.get(label) ?? { name: label, displayName: label, count: 0 };
      entry.count++;
      offerCounts.set(label, entry);
    }
    composition.byOffer = [...offerCounts.values()].sort((a, b) => b.count - a.count);
  }

  if (input.creditGrants) {
    const active = input.creditGrants.filter(
      (g) => g.status?.phase === 'Active' && !g.spec?.voided && (g.spec?.currency || 'USD') === currency
    );
    composition.credits = {
      active: active.length,
      granted: active.reduce((sum, g) => sum + parseAmount(g.spec?.amount), 0),
      expiringSoon: active.filter((g) => {
        const expires = Date.parse(g.status?.expiresAt ?? '');
        return !Number.isNaN(expires) && expires > nowMs && expires <= soonMs;
      }).length,
    };
  }

  return composition;
}

/** `1 account`, `3 accounts`; pass `many` for words that don't just take an s. */
export function plural(n: number, word: string, many = `${word}s`): string {
  return `${n.toLocaleString('en-US')} ${n === 1 ? word : many}`;
}
