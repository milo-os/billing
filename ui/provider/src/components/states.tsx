/**
 * Error and access-denied states, as in compute's provider plugin. Loading
 * is drawn by each section itself (a skeleton of its own content).
 */
import { ApiError } from '../lib/api';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { LockIcon, ServerCrashIcon } from 'lucide-react';

function StateCard({
  icon,
  title,
  message,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  message: string;
  children?: React.ReactNode;
}) {
  return (
    <Card size="sm" className="max-w-md">
      <CardContent className="flex flex-col items-start gap-3">
        {icon}
        <div>
          <p className="font-semibold">{title}</p>
          <p className="text-muted-foreground mt-1 text-sm">{message}</p>
        </div>
        {children}
      </CardContent>
    </Card>
  );
}

/** A 403 reads as "Access restricted"; anything else as a failure with Retry. */
export function ErrorOrRestrictedState({
  error,
  restrictedMessage,
  onRetry,
}: {
  error: unknown;
  restrictedMessage: string;
  onRetry: () => void;
}) {
  if (error instanceof ApiError && error.status === 403) {
    return (
      <StateCard
        icon={<LockIcon className="text-muted-foreground size-6" />}
        title="Access restricted"
        message={restrictedMessage}
      />
    );
  }
  return (
    <StateCard
      icon={<ServerCrashIcon className="text-destructive size-6" />}
      title="Failed to load"
      message={error instanceof Error ? error.message : 'An unknown error occurred'}>
      <button
        type="button"
        onClick={onRetry}
        className="border-border hover:bg-muted mt-1 rounded-md border px-3 py-1.5 text-sm font-medium transition-colors">
        Retry
      </button>
    </StateCard>
  );
}
