/**
 * React Query hooks for the fleet overview. Each list is read across every
 * organization in one paginated call on the platform API, the way
 * staff-portal's own billing-account list reads them.
 *
 * Accounts, invoices and payment methods are required; the page can't say
 * anything without them. The rest are optional: a staff member without
 * access to, say, credit grants still gets the page, minus that section.
 */
import { ApiError, listAll, PLUGIN_ID, proxyGet } from './api';
import type { FleetInput } from './fleet';
import type {
  BillingAccount,
  BillingAccountBalance,
  BillingArrangementApplication,
  BillingEntitlement,
  CreditGrant,
  Invoice,
  Offer,
  Organization,
  PaymentMethod,
} from './types';
import { fetchFleetUsage, usageAccountsSignature, type FleetUsage } from './usage';
import { useQueries, useQuery } from '@tanstack/react-query';

const BILLING = '/apis/billing.miloapis.com/v1alpha1';
const USAGE = '/apis/usage.billing.miloapis.com/v1alpha1';

/** Fleet lists are heavy and billing moves slowly: refresh every few minutes. */
const STALE_MS = 2 * 60_000;
const REFETCH_MS = 5 * 60_000;

export interface FleetData {
  input: FleetInput;
  /** Lists that stopped at the page cap: their totals are a lower bound. */
  truncated: string[];
}

/** Undefined when staff can't read the list (or the API isn't served here). */
async function optionalList<T>(path: string): Promise<{ items?: T[]; truncated: boolean }> {
  try {
    return await listAll<T>(path);
  } catch (error) {
    if (error instanceof ApiError && [401, 403, 404].includes(error.status)) {
      return { items: undefined, truncated: false };
    }
    throw error;
  }
}

async function fetchFleet(): Promise<FleetData> {
  const [
    accounts,
    invoices,
    paymentMethods,
    applications,
    entitlements,
    offers,
    creditGrants,
    organizations,
  ] = await Promise.all([
    listAll<BillingAccount>(`${BILLING}/billingaccounts`),
    // ~20k invoices before the walk stops; see FleetData.truncated.
    listAll<Invoice>(`${BILLING}/invoices`),
    listAll<PaymentMethod>(`${BILLING}/paymentmethods`),
    optionalList<BillingArrangementApplication>(`${BILLING}/billingarrangementapplications`),
    optionalList<BillingEntitlement>(`${BILLING}/billingentitlements`),
    optionalList<Offer>(`${BILLING}/offers`),
    optionalList<CreditGrant>(`${BILLING}/creditgrants`),
    optionalList<Organization>('/apis/resourcemanager.miloapis.com/v1alpha1/organizations'),
  ]);

  const truncated = Object.entries({
    'billing accounts': accounts,
    invoices,
    'payment methods': paymentMethods,
    'payment terms applications': applications,
    entitlements,
    'credit grants': creditGrants,
  })
    .filter(([, list]) => list.truncated)
    .map(([name]) => name);

  return {
    input: {
      accounts: accounts.items,
      invoices: invoices.items,
      paymentMethods: paymentMethods.items,
      applications: applications.items,
      entitlements: entitlements.items,
      offers: offers.items,
      creditGrants: creditGrants.items,
      organizations: organizations.items,
    },
    truncated,
  };
}

export function useFleet() {
  return useQuery({
    queryKey: [PLUGIN_ID, 'fleet'],
    queryFn: fetchFleet,
    staleTime: STALE_MS,
    refetchInterval: REFETCH_MS,
    retry: false,
  });
}

export function useFleetUsage(
  accounts: BillingAccount[] | undefined,
  currency: string | undefined,
  now: Date
) {
  return useQuery<FleetUsage>({
    // Keyed on which accounts are read and on the page's clock, which moves
    // each time the fleet refreshes: a refresh re-reads usage up to the new
    // moment, and a change in the accounts with projects re-reads it too.
    queryKey: [
      PLUGIN_ID,
      'fleet-usage',
      currency,
      accounts && currency ? usageAccountsSignature(accounts, currency) : undefined,
      now.getTime(),
    ],
    enabled: !!accounts && !!currency,
    queryFn: () => fetchFleetUsage(accounts ?? [], currency ?? 'USD', now),
    // Keep the last answer on screen while the next one loads.
    placeholderData: (previous) => previous,
    staleTime: STALE_MS,
    retry: false,
  });
}

/**
 * The provider's forecast for each account's current cycle. Read one account
 * at a time (the balance has no cross-namespace list), so only for the
 * handful of rows on screen.
 */
export function useAccountBalances(
  accounts: { namespace: string; name: string }[],
  enabled = true
) {
  return useQueries({
    queries: accounts.map(({ namespace, name }) => ({
      enabled,
      queryKey: [PLUGIN_ID, 'balance', namespace, name],
      queryFn: () =>
        proxyGet<BillingAccountBalance>(
          `${USAGE}/namespaces/${encodeURIComponent(namespace)}/billingaccountbalances/${encodeURIComponent(name)}`
        ),
      staleTime: STALE_MS,
      retry: false,
    })),
  });
}
