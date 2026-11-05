import type { AttentionItem, AttentionSeverity } from '../lib/fleet';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import {
  AlertCircleIcon,
  AlertTriangleIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  ChevronRightIcon,
  InfoIcon,
} from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';

/** Entries shown before "Show all": enough to act on, short enough to scan. */
const ENTRIES_SHOWN = 8;

/**
 * staff-portal's list-row hover (LIST_TABLE_ROW_CLASS in its
 * features/milo/lib/card-chrome.ts): a faint full-width tint.
 */
const ROW_HOVER = 'hover:bg-muted/30 transition-colors';

/** Lines entries up under the item title: the row's padding plus the icon and its gap. */
const ENTRY_INDENT = { paddingLeft: '2.75rem' } as const;

const ICON: Record<AttentionSeverity, { icon: typeof InfoIcon; color: string; label: string }> = {
  error: { icon: AlertCircleIcon, color: 'var(--btn-danger)', label: 'Action needed' },
  warning: { icon: AlertTriangleIcon, color: 'var(--btn-warning)', label: 'Warning' },
  info: { icon: InfoIcon, color: 'var(--muted-foreground)', label: 'Heads up' },
};

/**
 * One item: a row that expands into the accounts behind its count, each
 * linking to the page where it gets fixed, with why it's on the list.
 */
function AttentionRow({ item }: { item: AttentionItem }) {
  const [open, setOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const { icon: Icon, color, label } = ICON[item.severity];
  const shown = showAll ? item.entries : item.entries.slice(0, ENTRIES_SHOWN);
  const Chevron = open ? ChevronDownIcon : ChevronRightIcon;

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className={`${ROW_HOVER} flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm`}>
        <Icon className="size-4 shrink-0" style={{ color }} aria-label={label} />
        <span className="font-medium">{item.title}</span>
        {item.detail && (
          <span className="text-muted-foreground truncate text-xs">{item.detail}</span>
        )}
        <Chevron className="text-muted-foreground ml-auto size-4 shrink-0" />
      </button>
      {open && (
        <div className="border-border border-t">
          <div className="divide-border divide-y">
            {shown.map((entry) => (
              <Link
                key={entry.key}
                to={entry.href}
                style={ENTRY_INDENT}
                className={`${ROW_HOVER} flex items-center gap-3 py-2 pr-4 text-sm`}>
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium">{entry.label}</span>
                  {entry.sublabel && (
                    <span className="text-muted-foreground truncate font-mono text-xs">
                      {entry.sublabel}
                    </span>
                  )}
                </span>
                <span className="text-muted-foreground ml-auto shrink-0 text-right text-xs">
                  {entry.reason}
                </span>
                <ChevronRightIcon className="text-muted-foreground size-4 shrink-0" />
              </Link>
            ))}
          </div>
          {item.entries.length > ENTRIES_SHOWN && (
            <button
              type="button"
              onClick={() => setShowAll((v) => !v)}
              style={ENTRY_INDENT}
              className="border-border text-muted-foreground hover:text-foreground w-full border-t py-2 text-left text-xs underline">
              {showAll ? 'Show fewer' : `Show all ${item.entries.length}`}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The fleet's to-do list: the per-account health checks on staff-portal's
 * account page, counted across every account. Sorted errors first; each row
 * expands into the accounts behind it.
 */
export function AttentionPanel({ items }: { items?: AttentionItem[] }) {
  if (!items) {
    return (
      <div
        className="border-border bg-card divide-border divide-y overflow-hidden rounded-xl border"
        aria-busy="true">
        {[56, 72, 48].map((width) => (
          <div key={width} className="flex items-center gap-3 px-4 py-2.5">
            <Skeleton className="size-4 shrink-0 rounded-full" />
            <Skeleton className="h-4 rounded" style={{ width: `${width / 4}rem` }} />
            <Skeleton className="h-3 w-32 rounded" />
          </div>
        ))}
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <div className="border-border bg-card flex items-center gap-2 rounded-xl border px-4 py-3 text-sm">
        <CheckCircle2Icon className="size-4 shrink-0" style={{ color: 'var(--btn-success)' }} />
        Nothing needs attention: no past-due accounts, payment problems or pending reviews.
      </div>
    );
  }
  const order: AttentionSeverity[] = ['error', 'warning', 'info'];
  const sorted = [...items].sort((a, b) => order.indexOf(a.severity) - order.indexOf(b.severity));

  return (
    <div
      className="border-border bg-card divide-border divide-y overflow-hidden rounded-xl border"
      data-testid="billing-attention">
      {sorted.map((item) => (
        <AttentionRow key={item.id} item={item} />
      ))}
    </div>
  );
}
