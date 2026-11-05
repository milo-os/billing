/**
 * Fleet usage spend by service, this month so far against the same point
 * last month, from `UsageQuery` (usage.billing.miloapis.com).
 *
 * A platform-scope query has to name its billing accounts, at most 50 at a
 * time, and merges them: spend comes back per meter, not per account. So the
 * fleet is read in batches of 50, totals only (no granularity), and meters
 * are rolled up to their service by the meter name's domain, the way
 * internal/usagereport's ServiceName does.
 *
 * Spend is what the provider prices from each account's plan: after the
 * offer's discounts, before tax and credits. A 100% discount prices real
 * usage at $0, so each service also says whether it recorded any usage at
 * all; "$0" and "no usage" are different answers. Only accounts that have
 * projects are queried: an account with none has no usage to read.
 */
import { ApiError, mapWithConcurrency, proxyPost } from './api';
import { accountKey } from './fleet';
import { parseAmount } from './money';
import type { BillingAccount, UsageQueryResult } from './types';

export const USAGE_BATCH_SIZE = 50;
const USAGE_CONCURRENCY = 6;
/** 40 batches is 2,000 accounts with projects; beyond that the totals say they're partial. */
export const MAX_USAGE_BATCHES = 40;

const USAGE_QUERIES_PATH = '/apis/usage.billing.miloapis.com/v1alpha1/usagequeries';

export interface ServiceSpend {
  service: string;
  current: number;
  previous: number;
  /** Whether any of the service's meters recorded usage this month, priced or not. */
  hasUsage: boolean;
}

export interface ServiceRollup {
  spend: number;
  hasUsage: boolean;
}

export interface FleetUsage {
  currency: string;
  window: { start: Date; end: Date; previousStart: Date; previousEnd: Date };
  total: number;
  previousTotal: number;
  /** Any meter recorded usage this month, whatever it was priced at. */
  hasUsage: boolean;
  services: ServiceSpend[];
  accountsQueried: number;
  accountsWithProjects: number;
  /** Batches whose query failed or reported a spend error: the totals miss those accounts. */
  failedBatches: number;
}

const ACRONYMS: Record<string, string> = { dns: 'DNS', ai: 'AI', alb: 'ALB', iam: 'IAM' };

/** "compute.datumapis.com/instance/cpu-seconds" → "Compute". */
export function serviceName(meterName: string): string {
  const domain = meterName.split('/')[0] ?? '';
  const label = domain.split('.')[0] ?? '';
  if (!label) return 'Other';
  if (ACRONYMS[label]) return ACRONYMS[label];
  return label
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => ACRONYMS[w] ?? w[0].toUpperCase() + w.slice(1))
    .join(' ');
}

/**
 * This month so far (UTC), and last month up to the same elapsed point,
 * clamped to last month's end so the 31st compares with the 30th.
 */
export function usageWindows(now: Date) {
  const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1));
  const previousStart = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - 1, 1));
  const elapsed = now.getTime() - start.getTime();
  const previousEnd = new Date(Math.min(previousStart.getTime() + elapsed, start.getTime()));
  return { start, end: now, previousStart, previousEnd };
}

export function usageAccounts(accounts: BillingAccount[]): BillingAccount[] {
  return accounts.filter(
    (a) => (a.status?.linkedProjectsCount ?? 0) > 0 && a.status?.phase !== 'Archived'
  );
}

function chunk<T>(items: T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

/** A meter's total usage over the window, from its ungrouped series. */
function meterQuantity(meter: NonNullable<NonNullable<UsageQueryResult['status']>['meters']>[number]) {
  return (meter.groupings ?? []).reduce(
    (sum, g) => sum + (g.series ?? []).reduce((s, series) => s + parseAmount(series.total), 0),
    0
  );
}

/**
 * Spend per service across query results, and whether the service recorded
 * any usage. Meters without spend (not priced by any plan) count as $0.
 */
export function rollUpByService(results: UsageQueryResult[]): Map<string, ServiceRollup> {
  const byService = new Map<string, ServiceRollup>();
  for (const result of results) {
    for (const meter of result.status?.meters ?? []) {
      const service = serviceName(meter.meterName || meter.meterDefinition || '');
      const entry = byService.get(service) ?? { spend: 0, hasUsage: false };
      entry.spend += parseAmount(meter.spend?.amount);
      entry.hasUsage ||= meterQuantity(meter) > 0;
      byService.set(service, entry);
    }
  }
  return byService;
}

const totalSpend = (results: UsageQueryResult[]) =>
  results.reduce((sum, r) => sum + parseAmount(r.status?.spend?.amount), 0);

async function queryBatch(
  batch: BillingAccount[],
  start: Date,
  end: Date
): Promise<UsageQueryResult | undefined> {
  try {
    const result = await proxyPost<UsageQueryResult>(USAGE_QUERIES_PATH, {
      apiVersion: 'usage.billing.miloapis.com/v1alpha1',
      kind: 'UsageQuery',
      spec: {
        startTime: start.toISOString(),
        endTime: end.toISOString(),
        timeZone: 'UTC',
        billingAccounts: batch.map((a) => ({
          namespace: a.metadata?.namespace,
          name: a.metadata?.name,
        })),
        groupings: [{ groupBy: [] }],
        includeSpend: true,
      },
    });
    return result.status?.spendError ? undefined : result;
  } catch (error) {
    // No access is the whole answer, not one failed batch: let it reach the
    // page, which says so instead of showing $0 as a partial result.
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) throw error;
    return undefined;
  }
}

/**
 * A short, stable signature of which accounts usage reads (those with
 * projects, in `currency`), for the query key: it changes when that set
 * changes, not only when its size does.
 */
export function usageAccountsSignature(accounts: BillingAccount[], currency: string): string {
  let hash = 0;
  const keys = usageAccounts(accounts)
    .filter((a) => (a.spec?.currencyCode || 'USD') === currency)
    .map((a) => accountKey(a.metadata?.namespace, a.metadata?.name))
    .sort();
  for (const ch of keys.join('|')) hash = (Math.imul(hash, 31) + ch.charCodeAt(0)) | 0;
  return `${keys.length}:${(hash >>> 0).toString(36)}`;
}

export async function fetchFleetUsage(
  accounts: BillingAccount[],
  currency: string,
  now: Date
): Promise<FleetUsage> {
  const window = usageWindows(now);
  const withProjects = usageAccounts(accounts).filter(
    (a) => (a.spec?.currencyCode || 'USD') === currency
  );
  // Stable order, so a truncated fleet always reads the same accounts.
  withProjects.sort((a, b) =>
    accountKey(a.metadata?.namespace, a.metadata?.name).localeCompare(
      accountKey(b.metadata?.namespace, b.metadata?.name)
    )
  );
  const batches = chunk(withProjects, USAGE_BATCH_SIZE).slice(0, MAX_USAGE_BATCHES);

  const jobs = batches.flatMap((batch) => [
    { batch, start: window.start, end: window.end, previous: false },
    { batch, start: window.previousStart, end: window.previousEnd, previous: true },
  ]);
  const results = await mapWithConcurrency(jobs, USAGE_CONCURRENCY, (job) =>
    queryBatch(job.batch, job.start, job.end)
  );

  const current: UsageQueryResult[] = [];
  const previous: UsageQueryResult[] = [];
  let failedBatches = 0;
  results.forEach((result, i) => {
    if (!result) {
      // Count a batch once even when both of its windows failed.
      if (!jobs[i].previous || results[i - 1]) failedBatches++;
      return;
    }
    (jobs[i].previous ? previous : current).push(result);
  });

  const currentByService = rollUpByService(current);
  const previousByService = rollUpByService(previous);
  const names = new Set([...currentByService.keys(), ...previousByService.keys()]);
  const services = [...names]
    .map((service) => ({
      service,
      current: currentByService.get(service)?.spend ?? 0,
      previous: previousByService.get(service)?.spend ?? 0,
      hasUsage: currentByService.get(service)?.hasUsage ?? false,
    }))
    .filter((s) => s.current > 0 || s.previous > 0 || s.hasUsage)
    .sort(
      (a, b) =>
        b.current - a.current || b.previous - a.previous || Number(b.hasUsage) - Number(a.hasUsage)
    );

  return {
    currency,
    window,
    total: totalSpend(current),
    previousTotal: totalSpend(previous),
    hasUsage: services.some((s) => s.hasUsage),
    services,
    accountsQueried: batches.reduce((n, b) => n + b.length, 0),
    accountsWithProjects: withProjects.length,
    failedBatches,
  };
}
