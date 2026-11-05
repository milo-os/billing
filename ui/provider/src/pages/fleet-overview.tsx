/**
 * `portal.page/service` extension at path "" (`FleetOverview`): replaces
 * staff-portal's built-in Overview on /admin/service-catalog/<billing service>
 * with a fleet-wide view of billing, so it's the landing page. The built-in
 * page (pricing, meters, quota, conditions) moves to staff-portal's "Service
 * details" nav item. staff-portal's own shell renders the service header and
 * nav around this.
 *
 * Top to bottom, by how soon someone has to act on it:
 *   1. What needs attention (past due, can't charge, cards expiring, reviews)
 *   2. Money: collected, invoiced, outstanding, past due, collection rate, DSO
 *   3. Invoiced vs collected by month, and receivables by age
 *   4. Collections worklist and top accounts
 *   5. Usage spend by service this month, and what the fleet is made of
 *
 * All of it is read client-side through staff-portal's proxy with the
 * staff member's own access; see ../lib/queries.ts.
 */
import { CollectionsWorklist, TopAccounts } from '../components/accounts';
import { AttentionPanel } from '../components/attention';
import { AgingCard, MonthlyChart } from '../components/cash-flow';
import { CompositionCard, UsageByService } from '../components/fleet-mix';
import { StatStrip, type Stat } from '../components/parts';
import { ErrorOrRestrictedState } from '../components/states';
import { plural, summarizeFleet, type FleetSummary } from '../lib/fleet';
import { formatChange, formatMoneyCompact, formatPercent } from '../lib/money';
import { useFleet, useFleetUsage } from '../lib/queries';
import { useMemo } from 'react';

// A 2:1 pair that wraps to one column on narrow screens. Inline, because the
// host's CSS may not carry `lg:grid-cols-3`/`lg:col-span-2` (see ui/CLAUDE.md).
const WIDE = { flex: '2 1 34rem', minWidth: 0 } as const;
const NARROW = { flex: '1 1 20rem', minWidth: 0 } as const;

/** The money strip's labels, shown with skeleton figures while the fleet loads. */
const MONEY_LABELS = ['Collected', 'Invoiced', 'Outstanding', 'Past due', 'Collection rate', 'DSO'];

function moneyStats(summary: FleetSummary): Stat[] {
  const { money, currency } = summary;
  const fmt = (n: number) => formatMoneyCompact(n, currency);
  const vs = (current: number, previous: number) => {
    const change = formatChange(current, previous);
    return change ? `${change} vs prior 30 days` : 'Last 30 days';
  };
  return [
    {
      label: 'Collected',
      value: fmt(money.collected30d),
      hint: vs(money.collected30d, money.collectedPrev30d),
    },
    {
      label: 'Invoiced',
      value: fmt(money.invoiced30d),
      hint: vs(money.invoiced30d, money.invoicedPrev30d),
    },
    {
      label: 'Outstanding',
      value: fmt(money.notYetDue),
      hint: 'Open, not yet due',
    },
    {
      label: 'Past due',
      value: fmt(money.pastDue),
      hint:
        money.pastDueAccounts > 0
          ? `${plural(money.pastDueAccounts, 'account')} · oldest ${money.oldestDaysOverdue}d`
          : 'Nothing overdue',
      valueStyle: money.pastDue > 0 ? { color: 'var(--btn-danger)' } : undefined,
    },
    {
      label: 'Collection rate',
      value: money.collectionRate !== undefined ? formatPercent(money.collectionRate) : '—',
      hint: 'Paid vs overdue, last 180 days',
    },
    {
      label: 'DSO',
      value: money.dso !== undefined ? `${Math.round(money.dso)} days` : '—',
      hint: 'Days sales outstanding',
    },
  ];
}

export default function FleetOverview() {
  const { data, isLoading, error, refetch, dataUpdatedAt } = useFleet();
  // One clock for the whole page, so every figure measures from the same
  // moment: when the fleet was last read. It moves with each refresh, so a
  // page left open keeps counting new invoices and days overdue correctly.
  const now = useMemo(() => new Date(dataUpdatedAt || Date.now()), [dataUpdatedAt]);
  const summary = useMemo(() => (data ? summarizeFleet(data.input, now) : undefined), [data, now]);
  const usage = useFleetUsage(data?.input.accounts, summary?.currency, now);

  // A failed fleet read replaces the page; anything else renders the full
  // layout at once, each section a skeleton of itself until its data lands,
  // the way staff-portal's own billing dashboard loads.
  if (!isLoading && error) {
    return (
      <div className="flex min-w-0 flex-col gap-6 p-4 sm:p-6" data-testid="billing-fleet-overview">
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to read billing accounts and invoices across the platform."
          onRetry={() => void refetch()}
        />
      </div>
    );
  }

  const currency = summary?.currency;
  return (
    <div className="flex min-w-0 flex-col gap-6 p-4 sm:p-6" data-testid="billing-fleet-overview">
      {summary && data && (data.truncated.length > 0 || summary.otherCurrencyInvoices > 0) && (
        <p className="text-muted-foreground text-xs">
          {data.truncated.length > 0 &&
            `Partial: stopped reading ${data.truncated.join(', ')} at the page limit, so totals are a lower bound. `}
          {summary.otherCurrencyInvoices > 0 &&
            `Money is in ${summary.currency}; ${plural(summary.otherCurrencyInvoices, 'invoice')} in other currencies are left out.`}
        </p>
      )}

      <AttentionPanel items={summary?.attention} />

      <StatStrip stats={summary ? moneyStats(summary) : MONEY_LABELS.map((label) => ({ label }))} />

      <div className="flex flex-wrap gap-4">
        <div style={WIDE}>
          <MonthlyChart months={summary?.monthly} currency={currency} />
        </div>
        <div style={NARROW}>
          <AgingCard rows={summary?.aging} currency={currency} />
        </div>
      </div>

      <CollectionsWorklist rows={summary?.collections} currency={currency} />

      <TopAccounts accounts={summary?.accounts} currency={currency} />

      <div className="flex flex-wrap gap-4">
        <div style={WIDE}>
          <UsageByService usage={usage.data} isLoading={usage.isLoading} error={usage.error} />
        </div>
        <div style={NARROW}>
          <CompositionCard composition={summary?.composition} currency={currency} />
        </div>
      </div>
    </div>
  );
}
