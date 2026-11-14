import type { ReactNode } from 'react';

export type BadgeVariant =
  'neutral' | 'accent' | 'success' | 'warning' | 'danger';

export interface BadgeProps {
  variant?: BadgeVariant;
  children: ReactNode;
  className?: string;
}

const variantClasses: Record<BadgeVariant, string> = {
  neutral: 'border-border bg-surface-subtle text-muted',
  accent: 'border-accent/25 bg-accent-soft text-accent',
  success: 'border-success/25 bg-success-soft text-success',
  warning: 'border-warning/25 bg-warning-soft text-warning',
  danger: 'border-danger/25 bg-danger-soft text-danger',
};

/** Compact status pill using semantic colour tokens. */
export function Badge({
  variant = 'neutral',
  children,
  className = '',
}: BadgeProps) {
  return (
    <span
      className={`inline-flex items-center rounded-pill border px-2.5 py-0.5 text-caption font-medium ${variantClasses[variant]} ${className}`.trim()}
    >
      {children}
    </span>
  );
}
