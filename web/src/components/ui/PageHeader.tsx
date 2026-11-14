import type { ReactNode } from 'react';

export interface PageHeaderProps {
  title: string;
  description?: string;
  eyebrow?: string;
  actions?: ReactNode;
  className?: string;
}

/** Top-of-page heading block with optional eyebrow, subtitle, and actions. */
export function PageHeader({
  title,
  description,
  eyebrow,
  actions,
  className = '',
}: PageHeaderProps) {
  return (
    <header
      className={`flex flex-col gap-4 border-b border-border pb-6 sm:flex-row sm:items-end sm:justify-between ${className}`.trim()}
    >
      <div className="flex flex-col gap-1.5">
        {eyebrow ? (
          <p className="text-caption font-semibold tracking-wider text-accent uppercase">
            {eyebrow}
          </p>
        ) : null}
        <h1 className="text-title font-semibold text-ink">{title}</h1>
        {description ? (
          <p className="text-body text-muted">{description}</p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {actions}
        </div>
      ) : null}
    </header>
  );
}
