/**
 * A made-up fleet for the standalone preview (`?fixtures`) and the tests.
 * Deterministic: the same `now` always builds the same fleet.
 */
import { DAY_MS } from './invoices';
import type {
  BillingAccount,
  BillingArrangementApplication,
  BillingEntitlement,
  CreditGrant,
  Invoice,
  Offer,
  Organization,
  PaymentMethod,
  UsageQueryResult,
} from './types';

/** Mulberry32: a tiny seeded PRNG, so fixtures don't change between runs. */
function rng(seed: number) {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ORGS = [
  ['acme', 'Acme Robotics'],
  ['globex', 'Globex'],
  ['initech', 'Initech'],
  ['umbrella', 'Umbrella Labs'],
  ['hooli', 'Hooli'],
  ['stark', 'Stark Industries'],
  ['wayne', 'Wayne Enterprises'],
  ['wonka', 'Wonka Industries'],
  ['tyrell', 'Tyrell Corp'],
  ['cyberdyne', 'Cyberdyne Systems'],
  ['soylent', 'Soylent'],
  ['massive', 'Massive Dynamic'],
  ['oscorp', 'Oscorp'],
  ['vandelay', 'Vandelay Industries'],
  ['pied-piper', 'Pied Piper'],
  ['aperture', 'Aperture Science'],
  ['black-mesa', 'Black Mesa'],
  ['gringotts', 'Gringotts'],
  ['dunder', 'Dunder Mifflin'],
  ['prestige', 'Prestige Worldwide'],
];

export interface Fixtures {
  accounts: BillingAccount[];
  invoices: Invoice[];
  paymentMethods: PaymentMethod[];
  applications: BillingArrangementApplication[];
  entitlements: BillingEntitlement[];
  offers: Offer[];
  creditGrants: CreditGrant[];
  organizations: Organization[];
}

const iso = (ms: number) => new Date(ms).toISOString();

export function buildFixtures(now: Date): Fixtures {
  const rand = rng(42);
  const f: Fixtures = {
    accounts: [],
    invoices: [],
    paymentMethods: [],
    applications: [],
    entitlements: [],
    offers: [
      { metadata: { name: 'standard-2026' }, spec: { displayName: 'Standard' } },
      { metadata: { name: 'startup-credits' }, spec: { displayName: 'Startup program' } },
      { metadata: { name: 'bare-metal-acme' }, spec: { displayName: 'Bare metal contract' } },
    ],
    creditGrants: [],
    organizations: ORGS.map(([name, display]) => ({
      metadata: { name, annotations: { 'kubernetes.io/display-name': display } },
    })),
  };

  ORGS.forEach(([org], i) => {
    const namespace = `organization-${org}`;
    const name = `${org}-main`;
    const invoiceTerms = i % 6 === 0;
    const size = Math.pow(10, 2 + rand() * 2.6); // $100 – $40k a month
    const phase = i === 17 ? 'Suspended' : i === 19 ? 'Provisioning' : 'Ready';
    f.accounts.push({
      metadata: { name, namespace, creationTimestamp: iso(now.getTime() - 400 * DAY_MS) },
      spec: {
        currencyCode: 'USD',
        paymentTerms: { netDays: 30, invoiceFrequency: 'Monthly', invoiceDayOfMonth: 1 },
        contactInfo: { email: `billing@${org}.example`, businessName: i % 3 === 0 ? `${ORGS[i][1]}, Inc.` : undefined },
        defaultPaymentMethodRef: invoiceTerms ? undefined : { name: `${name}-card` },
      },
      status: {
        phase,
        linkedProjectsCount: phase === 'Provisioning' ? 0 : 1 + Math.floor(rand() * 6),
        paymentArrangement: invoiceTerms
          ? {
              name: `${name}-terms`,
              type: 'Invoice',
              startsAt: iso(now.getTime() - 300 * DAY_MS),
              endsAt: i === 6 ? iso(now.getTime() + 12 * DAY_MS) : undefined,
              invoice: { netDays: i === 0 ? 45 : 30, creditLimit: '50000' },
            }
          : undefined,
      },
    });
    f.entitlements.push({
      metadata: { name, namespace },
      spec: {
        billingAccountRef: { name },
        offerRef: { name: i === 0 ? 'bare-metal-acme' : i % 4 === 1 ? 'startup-credits' : 'standard-2026' },
      },
    });
    if (!invoiceTerms) {
      // Two cards run out at the end of this month; the rest in two years.
      const expiringSoon = i === 4 || i === 9;
      const expiryYear = now.getUTCFullYear() + (expiringSoon ? 0 : 2);
      f.paymentMethods.push({
        metadata: { name: `${name}-card`, namespace },
        spec: { billingAccountRef: { name }, displayName: 'Visa' },
        status: {
          phase: i === 11 ? 'Failed' : 'Active',
          details: {
            type: 'card',
            card: {
              brand: 'visa',
              last4: String(1000 + i),
              expiryMonth: now.getUTCMonth() + 1,
              expiryYear,
            },
          },
        },
      });
    }

    // Twelve monthly invoices, issued on the 1st, due net 30.
    for (let m = 12; m >= 1; m--) {
      const start = Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - m, 1);
      const end = Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - m + 1, 1);
      if (end > now.getTime()) continue;
      const total = Math.round(size * (0.7 + rand() * 0.6) * (1 + (12 - m) * 0.02) * 100) / 100;
      const netDays = invoiceTerms ? (i === 0 ? 45 : 30) : 0;
      const due = end + netDays * DAY_MS;
      const lateRisk = i === 3 || i === 8 || i === 11 || i === 12;
      let phase: Invoice['status'] = {};
      if (due > now.getTime()) {
        phase = { phase: 'Open', amountDue: String(total), amountPaid: '0' };
      } else if (lateRisk && m <= (i === 12 ? 4 : 2)) {
        phase = { phase: 'PastDue', amountDue: String(total), amountPaid: '0' };
      } else {
        phase = {
          phase: 'Paid',
          amountDue: '0',
          amountPaid: String(total),
          paidAt: iso(due - (invoiceTerms ? 5 : netDays) * DAY_MS + rand() * 2 * DAY_MS),
        };
      }
      const month = new Date(start).toISOString().slice(0, 7);
      f.invoices.push({
        metadata: {
          name: `${name}-${month}-${(i * 97 + m).toString(16)}`,
          namespace,
          annotations: {
            'openmeter.miloapis.com/invoiceId': `inv_${i}_${m}`,
            'openmeter.miloapis.com/stripeInvoiceNumber': `DTM-${1000 + i}-${String(13 - m).padStart(4, '0')}`,
          },
        },
        spec: { billingAccountRef: { name }, period: { start: iso(start), end: iso(end) } },
        status: {
          currencyCode: 'USD',
          total: String(total),
          dueDate: iso(due),
          ...phase,
        },
      });
    }
  });

  // A stale Amberflo leftover the sums must ignore.
  f.invoices.push({
    metadata: {
      name: 'acme-main-legacy',
      namespace: 'organization-acme',
      annotations: { 'amberflo.billing.miloapis.com/invoiceKey': 'x' },
    },
    spec: { billingAccountRef: { name: 'acme-main' }, period: { end: iso(now.getTime() - 200 * DAY_MS) } },
    status: { phase: 'PastDue', total: '999999', amountDue: '999999', currencyCode: 'USD' },
  });

  f.applications.push(
    {
      metadata: { name: 'hooli-app', namespace: 'organization-hooli' },
      spec: { billingAccountRef: { name: 'hooli-main' }, organization: { legalName: 'Hooli, Inc.' } },
      status: { phase: 'Pending' },
    },
    {
      metadata: { name: 'wonka-app', namespace: 'organization-wonka' },
      spec: { billingAccountRef: { name: 'wonka-main' }, organization: { legalName: 'Wonka Industries' } },
      status: { phase: 'Approved' },
    }
  );

  f.creditGrants.push(
    {
      metadata: { name: 'pied-piper-startup', namespace: 'organization-pied-piper' },
      spec: { billingAccountRef: { name: 'pied-piper-main' }, amount: '5000', currency: 'USD', reason: 'Promotional' },
      status: { phase: 'Active', expiresAt: iso(now.getTime() + 20 * DAY_MS) },
    },
    {
      metadata: { name: 'globex-goodwill', namespace: 'organization-globex' },
      spec: { billingAccountRef: { name: 'globex-main' }, amount: '250', currency: 'USD', reason: 'Goodwill' },
      status: { phase: 'Active' },
    }
  );

  return f;
}

const SERVICES = [
  'compute.datumapis.com/instance/cpu-seconds',
  'compute.datumapis.com/instance/memory-gib-seconds',
  'networking.datumapis.com/egress/bytes',
  'alb.datumapis.com/requests',
  'dns.networking.miloapis.com/queries',
  'ai-gateway.datumapis.com/tokens',
];

/** A UsageQuery answer for a batch of accounts, scaled by the window's length. */
export function fixtureUsage(batchSize: number, days: number, previous: boolean): UsageQueryResult {
  const rand = rng(batchSize * 7 + (previous ? 1 : 0));
  const meters = SERVICES.map((meterName, i) => ({
    meterName,
    meterDefinition: meterName.replace(/[./]/g, '-'),
    spend: {
      amount: (batchSize * days * (60 / (i + 1)) * (0.8 + rand() * 0.4) * (previous ? 0.9 : 1)).toFixed(2),
      currency: 'USD',
    },
  }));
  const total = meters.reduce((sum, m) => sum + Number(m.spend.amount), 0);
  return { status: { meters, spend: { amount: total.toFixed(2), currency: 'USD' } } };
}
