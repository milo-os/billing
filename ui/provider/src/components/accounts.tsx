/** Top accounts and the collections worklist: the two "who" tables. */
import { ApiError } from '../lib/api';
import { accountHref, type AccountRow, type CollectionRow } from '../lib/fleet';
import { parseAmount, formatMoney } from '../lib/money';
import { useAccountBalances } from '../lib/queries';
import {
  EmptyNote,
  Section,
  Segmented,
  StatusBadge,
  TableRowsSkeleton,
  type BadgeTone,
} from './parts';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';

const TOP_COUNT = 10;
const COLLECTIONS_COUNT = 10;

type SortKey = 'billed12m' | 'lifetimePaid' | 'outstanding';

const SORTS: { value: SortKey; label: string }[] = [
  { value: 'billed12m', label: 'Invoiced (12 mo)' },
  { value: 'lifetimePaid', label: 'Lifetime paid' },
  { value: 'outstanding', label: 'Outstanding' },
];

function accountStatus(row: AccountRow): { tone: BadgeTone; label: string } {
  if (row.pastDue > 0) return { tone: 'danger', label: 'Past due' };
  if (row.deleted) return { tone: 'muted', label: 'Deleted' };
  if (row.phase === 'Ready') return { tone: 'success', label: 'Ready' };
  if (row.phase === 'Suspended') return { tone: 'danger', label: 'Suspended' };
  if (row.phase === 'Archived') return { tone: 'muted', label: 'Archived' };
  return { tone: 'warning', label: row.phase };
}

function AccountCell({ row }: { row: AccountRow }) {
  return (
    <div className="flex min-w-0 flex-col">
      <Link to={accountHref(row.orgName, row.name)} className="truncate font-medium hover:underline">
        {row.displayName}
      </Link>
      <span className="text-muted-foreground truncate text-xs">
        {row.orgDisplayName !== row.displayName ? `${row.orgDisplayName} · ` : ''}
        <span className="font-mono">{row.name}</span>
      </span>
    </div>
  );
}

function modeLabel(row: AccountRow) {
  return row.billingMode === 'invoice' ? `Invoice${row.netDays ? ` · net ${row.netDays}` : ''}` : 'Card';
}

const th = 'text-muted-foreground px-3 py-2 text-left text-xs font-medium';
const thRight = 'text-muted-foreground px-3 py-2 text-right text-xs font-medium';
const td = 'px-3 py-2 align-middle';
const tdRight = 'px-3 py-2 text-right align-middle tabular-nums';

/**
 * The biggest customers by money, with the provider's estimate of what each
 * has run up this cycle. That estimate is read per account, so only for the
 * rows shown.
 */
export function TopAccounts({ accounts, currency }: { accounts?: AccountRow[]; currency?: string }) {
  const [sort, setSort] = useState<SortKey>('billed12m');
  const top = useMemo(
    () =>
      (accounts ?? [])
        .filter((a) => a[sort] > 0)
        .sort((a, b) => b[sort] - a[sort])
        .slice(0, TOP_COUNT),
    [accounts, sort]
  );
  // BillingAccountBalance is served by the usage provider, and not every
  // provider serves it yet (staging's doesn't). Once every lookup 404s the API
  // isn't there: drop the column and stop asking, rather than show dashes and
  // re-send the lookups on every sort.
  const [balancesServed, setBalancesServed] = useState(true);
  const balances = useAccountBalances(
    top.map((a) => ({ namespace: a.namespace, name: a.name })),
    balancesServed
  );
  const allNotFound =
    balances.length > 0 &&
    balances.every((b) => b.error instanceof ApiError && b.error.status === 404);
  useEffect(() => {
    if (allNotFound) setBalancesServed(false);
  }, [allNotFound]);
  const showCycle = balancesServed && !allNotFound;

  return (
    <Section
      title="Top accounts"
      description={
        showCycle
          ? "Ranked by money, in the fleet's main currency. This cycle is the provider's estimate so far, before credits."
          : "Ranked by money, in the fleet's main currency."
      }
      action={<Segmented label="Rank top accounts by" value={sort} options={SORTS} onChange={setSort} />}>
      {!accounts || !currency ? (
        <TableRowsSkeleton rows={5} />
      ) : top.length === 0 ? (
        <EmptyNote>No account has any here yet.</EmptyNote>
      ) : (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full text-sm" style={{ minWidth: '44rem' }}>
            <thead className="border-border border-b">
              <tr>
                <th className={th} style={{ width: '2rem' }}>#</th>
                <th className={th}>Account</th>
                <th className={th}>Pays by</th>
                <th className={th}>Offer</th>
                <th className={thRight}>Invoiced (12 mo)</th>
                {showCycle && <th className={thRight}>This cycle</th>}
                <th className={thRight}>Outstanding</th>
                <th className={th}>Status</th>
              </tr>
            </thead>
            <tbody className="divide-border divide-y">
              {top.map((row, i) => {
                const balance = balances[i];
                const estimate = balance?.data?.status?.forecast?.estimatedSpend;
                const status = accountStatus(row);
                return (
                  <tr key={row.key}>
                    <td className={`${td} text-muted-foreground tabular-nums`}>{i + 1}</td>
                    <td className={td} style={{ maxWidth: '16rem' }}>
                      <AccountCell row={row} />
                    </td>
                    <td className={`${td} whitespace-nowrap`}>{modeLabel(row)}</td>
                    <td className={`${td} text-muted-foreground`}>{row.offerName ?? '—'}</td>
                    <td className={tdRight}>{formatMoney(row.billed12m, currency)}</td>
                    {showCycle && (
                      <td className={`${tdRight} text-muted-foreground`}>
                        {balance?.isLoading ? (
                          <Skeleton className="ml-auto h-4 w-16 rounded" />
                        ) : estimate !== undefined ? (
                          formatMoney(parseAmount(estimate), balance.data?.status?.currency || currency)
                        ) : (
                          '—'
                        )}
                      </td>
                    )}
                    <td className={tdRight}>
                      {row.outstanding > 0 ? formatMoney(row.outstanding, currency) : '—'}
                    </td>
                    <td className={td}>
                      <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

/** Past-due invoices, biggest exposure (amount × days late) first. */
export function CollectionsWorklist({
  rows,
  currency,
}: {
  rows?: CollectionRow[];
  currency?: string;
}) {
  if (!rows || !currency) {
    return (
      <Section
        id="collections"
        title="Collections"
        description="Past-due invoices, biggest amount × days late first.">
        <TableRowsSkeleton rows={3} />
      </Section>
    );
  }
  const shown = rows.slice(0, COLLECTIONS_COUNT);
  return (
    <Section
      id="collections"
      title="Collections"
      description={
        rows.length > 0
          ? `${rows.length.toLocaleString('en-US')} past-due ${rows.length === 1 ? 'invoice' : 'invoices'}, biggest amount × days late first.`
          : 'Past-due invoices, biggest amount × days late first.'
      }>
      {shown.length === 0 ? (
        <EmptyNote>No past-due invoices.</EmptyNote>
      ) : (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full text-sm" style={{ minWidth: '40rem' }}>
            <thead className="border-border border-b">
              <tr>
                <th className={th}>Account</th>
                <th className={th}>Invoice</th>
                <th className={th}>Pays by</th>
                <th className={thRight}>Owed</th>
                <th className={thRight}>Days late</th>
              </tr>
            </thead>
            <tbody className="divide-border divide-y">
              {shown.map((r) => (
                <tr key={r.key}>
                  <td className={td} style={{ maxWidth: '16rem' }}>
                    <AccountCell row={r.account} />
                  </td>
                  <td className={`${td} font-mono text-xs`}>{r.stripeNumber || r.invoiceName}</td>
                  <td className={`${td} whitespace-nowrap`}>{modeLabel(r.account)}</td>
                  <td className={tdRight}>{formatMoney(r.owed, currency)}</td>
                  <td className={tdRight} style={r.daysOverdue > 60 ? { color: 'var(--btn-danger)' } : undefined}>
                    {r.daysOverdue}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length > shown.length && (
            <p className="text-muted-foreground mt-2 text-xs">
              {rows.length - shown.length} more past-due invoices on the{' '}
              <Link to="/customers/billing-accounts" className="underline">
                billing accounts
              </Link>{' '}
              list.
            </p>
          )}
        </div>
      )}
    </Section>
  );
}
