/**
 * Small layout pieces shared by the overview's sections.
 *
 * The plugin has no Tailwind build: a class only takes effect if the host's
 * compiled CSS already has it. Common utilities are safe; anything sized or
 * colored precisely uses an inline style or a CSS variable instead.
 */
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import type { CSSProperties, ReactNode } from 'react';

export function Section({
  id,
  title,
  description,
  action,
  children,
}: {
  id?: string;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card size="sm" id={id} className="min-w-0">
      <CardContent className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 flex-col gap-0.5">
            <h2 className="text-base font-semibold">{title}</h2>
            {description && <p className="text-muted-foreground text-sm">{description}</p>}
          </div>
          {action}
        </div>
        {children}
      </CardContent>
    </Card>
  );
}

export interface Stat {
  label: string;
  /** Undefined while loading: the label stays, the figure is a skeleton. */
  value?: string;
  /** A second line under the value: a comparison, a count, a definition. */
  hint?: string;
  valueStyle?: CSSProperties;
}

/** The headline money row. Scrolls sideways on narrow screens rather than squashing. */
export function StatStrip({ stats }: { stats: Stat[] }) {
  return (
    <div
      className="min-w-0 overflow-x-auto"
      data-testid="billing-money-strip"
      aria-busy={stats.some((s) => s.value === undefined) || undefined}>
      <div
        style={{ minWidth: '48rem' }}
        className="divide-card-border border-card-border bg-card flex divide-x rounded-xl border">
        {stats.map((s) => (
          <div key={s.label} className="flex min-w-0 flex-1 flex-col gap-1.5 px-5 py-4">
            <span className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
              {s.label}
            </span>
            {s.value === undefined ? (
              <>
                <Skeleton className="h-7 w-20 rounded" />
                <Skeleton className="h-4 w-28 rounded" />
              </>
            ) : (
              <>
                <span className="text-xl font-semibold tabular-nums" style={s.valueStyle}>
                  {s.value}
                </span>
                {s.hint && <span className="text-muted-foreground text-xs">{s.hint}</span>}
              </>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export type BadgeTone = 'success' | 'warning' | 'danger' | 'info' | 'muted';

/**
 * staff-portal's status chip size (STATUS_BADGE_CLASS in its
 * app/components/badge/badge-tokens.ts). Its exact class string, so the host's
 * CSS already has every class in it.
 */
const STATUS_BADGE_CLASS = 'h-4 px-[5px] text-4xs leading-4 font-medium tracking-[0.3px] uppercase';

/**
 * A light status chip with no border. `Badge` is the host's copy: datum-ui
 * before 3.5 drew a border on light badges, and staff-portal ships 3.4, so
 * `border-transparent` (what datum-ui 3.5+ does itself) keeps it borderless
 * on either version.
 */
export function StatusBadge({ tone, children }: { tone: BadgeTone; children: ReactNode }) {
  return (
    <div className="w-fit">
      <Badge
        type={tone}
        theme="light"
        className={`${STATUS_BADGE_CLASS} inline-flex cursor-default items-center gap-1 border-transparent whitespace-nowrap`}>
        {children}
      </Badge>
    </div>
  );
}

/** A thin horizontal share bar, for "this much of the whole"; staff-portal's own ShareBar. */
export function ShareBar({ share, color }: { share: number; color?: string }) {
  return (
    <div className="bg-foreground/10 h-1.5 w-full overflow-hidden rounded-full">
      <div
        className="h-full rounded-full"
        style={{
          width: `${Math.max(0, Math.min(1, share)) * 100}%`,
          background: color ?? 'var(--primary)',
        }}
      />
    </div>
  );
}

/** Table rows still loading, as staff-portal's TableRowsSkeleton draws them. */
export function TableRowsSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-2 py-1" aria-busy="true">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-8 w-full rounded" />
      ))}
    </div>
  );
}

/** Labelled share-bar rows still loading: the label when known, a skeleton figure and bar. */
export function BarRowsSkeleton({ labels, rows }: { labels?: string[]; rows?: number }) {
  const count = labels?.length ?? rows ?? 4;
  return (
    <div className="flex flex-col gap-3" aria-busy="true">
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="flex flex-col gap-1">
          <div className="flex items-center justify-between gap-3 text-sm">
            {labels ? <span>{labels[i]}</span> : <Skeleton className="h-4 w-24 rounded" />}
            <Skeleton className="h-4 w-16 rounded" />
          </div>
          <Skeleton className="h-1.5 w-full rounded-full" />
        </div>
      ))}
    </div>
  );
}

export function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <div className="border-border text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-sm">
      {children}
    </div>
  );
}

/** Two-to-four-way segmented control, kept local: the host shares no tabs component. */
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
  label: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="border-border flex rounded-md border p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          onClick={() => onChange(o.value)}
          className={
            o.value === value
              ? 'bg-muted rounded px-2.5 py-1 text-xs font-medium'
              : 'text-muted-foreground hover:text-foreground rounded px-2.5 py-1 text-xs font-medium'
          }>
          {o.label}
        </button>
      ))}
    </div>
  );
}

