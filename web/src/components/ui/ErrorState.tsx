import { Button } from './Button';

export interface ErrorStateProps {
  title?: string;
  message: string;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
}

/** Accessible alert box for API or screen errors with optional retry button. */
export function ErrorState({
  title = 'Something went wrong',
  message,
  onRetry,
  retryLabel = 'Try again',
  className = '',
}: ErrorStateProps) {
  return (
    <div
      role="alert"
      className={`flex flex-col items-start justify-between gap-4 rounded-card border border-danger/30 bg-danger-soft p-6 sm:flex-row sm:items-center ${className}`.trim()}
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-body font-semibold text-danger">{title}</h2>
        <p className="text-small text-ink">{message}</p>
      </div>
      {onRetry ? (
        <Button variant="secondary" size="sm" onClick={onRetry}>
          {retryLabel}
        </Button>
      ) : null}
    </div>
  );
}
