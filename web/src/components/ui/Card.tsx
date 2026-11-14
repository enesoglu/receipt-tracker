import { useId, type ReactNode } from 'react';

export interface CardProps {
  title?: string;
  description?: string;
  actions?: ReactNode;
  padding?: 'none' | 'md';
  children: ReactNode;
  className?: string;
}

/** Surface container with optional header, description, and action slot. */
export function Card({
  title,
  description,
  actions,
  padding = 'md',
  children,
  className = '',
}: CardProps) {
  const titleId = useId();
  const hasHeader = Boolean(title || description || actions);

  return (
    <section
      aria-labelledby={title ? titleId : undefined}
      className={`rounded-card border border-border bg-surface ${className}`.trim()}
    >
      {hasHeader ? (
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-6 py-4">
          <div className="flex flex-col gap-1">
            {title ? (
              <h2 id={titleId} className="text-heading font-semibold text-ink">
                {title}
              </h2>
            ) : null}
            {description ? (
              <p className="text-small text-muted">{description}</p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex items-center gap-2">{actions}</div>
          ) : null}
        </div>
      ) : null}
      <div className={padding === 'md' ? 'p-6' : ''}>{children}</div>
    </section>
  );
}
