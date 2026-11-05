/** Usage spend by service this month, and what the fleet is made of. */
import { plural, type Composition } from '../lib/fleet';
import { formatChange, formatMoney } from '../lib/money';
import { ApiError } from '../lib/api';
import type { FleetUsage } from '../lib/usage';
import { BarRowsSkeleton, EmptyNote, Section, ShareBar } from './parts';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';

const fmtDay = (d: Date) =>
  d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });

export function UsageByService({
  usage,
  isLoading,
  error,
}: {
  usage?: FleetUsage;
  isLoading: boolean;
  error: unknown;
}) {
  if (isLoading || (!usage && !error)) {
    return (
      <Section
        title="Usage this month"
        description="This month so far (UTC), against the same point last month. Spend after offer discounts, before tax and credits.">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1" aria-busy="true">
          <Skeleton className="h-8 w-32 rounded" />
          <Skeleton className="h-4 w-64 rounded" />
        </div>
        <BarRowsSkeleton rows={5} />
      </Section>
    );
  }
  if (!usage) {
    return (
      <Section title="Usage this month">
        <EmptyNote>
          {error instanceof ApiError && (error.status === 401 || error.status === 403)
            ? "You don't have permission to read usage across billing accounts."
            : `Usage couldn't be read: ${error instanceof Error ? error.message : 'unknown error'}.`}
        </EmptyNote>
      </Section>
    );
  }

  const change = formatChange(usage.total, usage.previousTotal);
  const max = Math.max(...usage.services.map((s) => s.current), 0);
  const partial = usage.failedBatches > 0 || usage.accountsQueried < usage.accountsWithProjects;

  return (
    <Section
      title="Usage this month"
      description={`${fmtDay(usage.window.start)} – ${fmtDay(usage.window.end)} (UTC), against ${fmtDay(usage.window.previousStart)} – ${fmtDay(usage.window.previousEnd)}. Spend after offer discounts, before tax and credits.`}>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-2xl font-semibold tabular-nums">
          {formatMoney(usage.total, usage.currency)}
        </span>
        <span className="text-muted-foreground text-sm">
          {change ? `${change} vs same point last month` : 'nothing to compare with last month'} ·{' '}
          {plural(usage.accountsQueried, 'account')} with projects
        </span>
      </div>
      {usage.total === 0 && usage.hasUsage && (
        <p className="text-sm" style={{ color: 'var(--btn-warning)' }}>
          Usage is recorded this month, but it all prices at $0. The offers these accounts are on
          discount it, or a free allowance covers it.
        </p>
      )}
      {partial && (
        <p className="text-xs" style={{ color: 'var(--btn-warning)' }}>
          Partial: {usage.failedBatches > 0 ? `${plural(usage.failedBatches, 'batch', 'batches')} of 50 accounts failed to read` : ''}
          {usage.failedBatches > 0 && usage.accountsQueried < usage.accountsWithProjects ? '; ' : ''}
          {usage.accountsQueried < usage.accountsWithProjects
            ? `read ${usage.accountsQueried.toLocaleString('en-US')} of ${usage.accountsWithProjects.toLocaleString('en-US')} accounts`
            : ''}
          .
        </p>
      )}
      {usage.services.length === 0 ? (
        <EmptyNote>No usage recorded yet this month.</EmptyNote>
      ) : (
        <div className="flex flex-col gap-3">
          {usage.services.map((s) => {
            const c = formatChange(s.current, s.previous);
            return (
              <div key={s.service} className="flex flex-col gap-1">
                <div className="flex items-baseline justify-between gap-3 text-sm">
                  <span>{s.service}</span>
                  <span className="flex items-baseline gap-2">
                    <span className="text-muted-foreground text-xs">
                      {s.current === 0 && s.hasUsage
                        ? 'used, priced at $0'
                        : (c ?? (s.previous === 0 ? 'new' : ''))}
                    </span>
                    <span className="font-medium tabular-nums">{formatMoney(s.current, usage.currency)}</span>
                  </span>
                </div>
                <ShareBar share={max > 0 ? s.current / max : 0} />
              </div>
            );
          })}
        </div>
      )}
    </Section>
  );
}

function CountRow({ label, count, total }: { label: string; count: number; total: number }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-3 text-sm">
        <span>{label}</span>
        <span className="font-medium tabular-nums">{count.toLocaleString('en-US')}</span>
      </div>
      <ShareBar share={total > 0 ? count / total : 0} color="var(--muted-foreground)" />
    </div>
  );
}

const OFFERS_SHOWN = 5;

export function CompositionCard({
  composition,
  currency,
}: {
  composition?: Composition;
  currency?: string;
}) {
  if (!composition || !currency) {
    return (
      <Section title="Accounts" description="Billing accounts by phase, how they pay, and offer.">
        <BarRowsSkeleton rows={6} />
      </Section>
    );
  }
  const live = composition.byMode.card + composition.byMode.invoice;
  const offers = composition.byOffer?.slice(0, OFFERS_SHOWN);
  return (
    <Section
      title="Accounts"
      description={`${plural(composition.total, 'billing account')}; ${composition.withProjects.toLocaleString('en-US')} have projects.`}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-3">
          {composition.byPhase
            .filter((p) => p.count > 0)
            .map((p) => (
              <CountRow key={p.phase} label={p.phase} count={p.count} total={composition.total} />
            ))}
        </div>
        <div className="border-border flex flex-col gap-3 border-t pt-4">
          <CountRow label="Pays by card" count={composition.byMode.card} total={live} />
          <CountRow label="Invoice terms" count={composition.byMode.invoice} total={live} />
        </div>
        {offers && offers.length > 0 && (
          <div className="border-border flex flex-col gap-3 border-t pt-4">
            {offers.map((o) => (
              <CountRow key={o.name} label={o.displayName} count={o.count} total={live} />
            ))}
            {(composition.byOffer?.length ?? 0) > OFFERS_SHOWN && (
              <span className="text-muted-foreground text-xs">
                and {(composition.byOffer?.length ?? 0) - OFFERS_SHOWN} more offers
              </span>
            )}
          </div>
        )}
        {composition.credits && (
          <div className="border-border flex items-baseline justify-between gap-3 border-t pt-4 text-sm">
            <span>Active credit grants</span>
            <span className="text-right">
              <span className="font-medium tabular-nums">{composition.credits.active.toLocaleString('en-US')}</span>
              <span className="text-muted-foreground block text-xs">
                {formatMoney(composition.credits.granted, currency)} granted
                {composition.credits.expiringSoon > 0
                  ? ` · ${composition.credits.expiringSoon} expiring soon`
                  : ''}
              </span>
            </span>
          </div>
        )}
      </div>
    </Section>
  );
}
