/**
 * Invoiced vs collected by month, and receivables by age.
 *
 * Two series, so a legend is always shown; colors come from datum-ui's
 * --chart-1/--chart-2 tokens, which the host defines for light and dark.
 */
import type { AgingRow, MonthPoint } from '../lib/fleet';
import { formatMoney, formatMoneyCompact } from '../lib/money';
import { AGING_BUCKETS } from '../lib/invoices';
import { BarRowsSkeleton, EmptyNote, Section, ShareBar } from './parts';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';

const CHART_HEIGHT = 240;

const INVOICED = 'var(--chart-1)';
const COLLECTED = 'var(--chart-2)';

function LegendSwatch({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5 text-xs">
      <span className="rounded-sm" style={{ width: 10, height: 10, background: color }} />
      {label}
    </span>
  );
}

function ChartTooltip({
  active,
  payload,
  label,
  currency,
}: {
  active?: boolean;
  payload?: { dataKey?: string | number; value?: number; color?: string }[];
  label?: string;
  currency: string;
}) {
  if (!active || !payload?.length) return null;
  return (
    <div className="border-border bg-card rounded-md border px-3 py-2 text-xs shadow-sm">
      <div className="mb-1 font-medium">{label}</div>
      {payload.map((p) => (
        <div key={String(p.dataKey)} className="flex items-center justify-between gap-4">
          <LegendSwatch color={p.color ?? ''} label={p.dataKey === 'invoiced' ? 'Invoiced' : 'Collected'} />
          <span className="tabular-nums">{formatMoney(p.value ?? 0, currency)}</span>
        </div>
      ))}
    </div>
  );
}

export function MonthlyChart({ months, currency }: { months?: MonthPoint[]; currency?: string }) {
  const empty = months?.every((m) => m.invoiced === 0 && m.collected === 0);
  return (
    <Section
      title="Invoiced vs collected"
      description="Last 12 months, UTC. Invoiced by the month its period closed, collected by the month it was paid."
      action={
        <div className="flex items-center gap-3">
          <LegendSwatch color={INVOICED} label="Invoiced" />
          <LegendSwatch color={COLLECTED} label="Collected" />
        </div>
      }>
      {!months || !currency ? (
        <Skeleton className="w-full rounded" style={{ height: CHART_HEIGHT }} aria-busy="true" />
      ) : empty ? (
        <EmptyNote>No invoices in the last 12 months.</EmptyNote>
      ) : (
        <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
          <BarChart data={months} barGap={2} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="0" />
            <XAxis
              dataKey="label"
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
            />
            <YAxis
              width={56}
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              tickFormatter={(v: number) => formatMoneyCompact(v, currency)}
            />
            <Tooltip
              cursor={{ fill: 'var(--muted)', opacity: 0.5 }}
              content={<ChartTooltip currency={currency} />}
            />
            <Bar dataKey="invoiced" fill={INVOICED} radius={[4, 4, 0, 0]} maxBarSize={18} isAnimationActive={false} />
            <Bar dataKey="collected" fill={COLLECTED} radius={[4, 4, 0, 0]} maxBarSize={18} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </Section>
  );
}

const AGING_COLOR: Record<string, string> = {
  notDue: 'var(--muted-foreground)',
  days1to30: 'var(--btn-warning)',
  days31to60: 'var(--btn-warning)',
  days61to90: 'var(--btn-danger)',
  days90plus: 'var(--btn-danger)',
};

/**
 * Who owes what, by how late it is. Split by how the account pays: a card
 * account past due is a failed charge, an invoice account past due is a
 * customer who hasn't paid an invoice, and the follow-up differs.
 */
export function AgingCard({ rows, currency }: { rows?: AgingRow[]; currency?: string }) {
  if (!rows || !currency) {
    return (
      <Section title="Receivables by age" description="Unpaid invoices by days past the due date.">
        <BarRowsSkeleton labels={AGING_BUCKETS.map((b) => b.label)} />
      </Section>
    );
  }
  const total = rows.reduce((sum, r) => sum + r.total, 0);
  return (
    <Section
      title="Receivables by age"
      description={
        total > 0
          ? `${formatMoney(total, currency)} unpaid, by days past the due date.`
          : 'Unpaid invoices by days past the due date.'
      }>
      {total === 0 ? (
        <EmptyNote>No unpaid invoices.</EmptyNote>
      ) : (
        <div className="flex flex-col gap-3">
          {rows.map((r) => (
            <div key={r.id} className="flex flex-col gap-1">
              <div className="flex items-baseline justify-between gap-3 text-sm">
                <span>{r.label}</span>
                <span className="font-medium tabular-nums">{formatMoney(r.total, currency)}</span>
              </div>
              <ShareBar share={r.total / total} color={AGING_COLOR[r.id]} />
              {r.total > 0 && (
                <span className="text-muted-foreground text-xs">
                  Card {formatMoney(r.card, currency)} · Invoice terms {formatMoney(r.invoice, currency)}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
    </Section>
  );
}
