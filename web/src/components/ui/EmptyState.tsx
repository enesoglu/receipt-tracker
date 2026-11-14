import type { ReactNode } from 'react';

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

/** Consistent empty-state panel with optional follow-up action. */
export function EmptyState({
  title,
  description,
  action,
  className = '',
}: EmptyStateProps) {
  return (
    <div
      role="status"
      className={`flex flex-col items-center justify-center gap-3 rounded-card border border-border bg-surface px-6 py-10 text-center ${className}`.trim()}
    >
      <div className="flex max-w-content flex-col gap-1">
        <h2 className="text-heading font-semibold text-ink">{title}</h2>
        {description ? (
          <p className="text-small text-muted">{description}</p>
        ) : null}
      </div>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
