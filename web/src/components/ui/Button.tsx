import type { ButtonHTMLAttributes, Ref } from 'react';
import { Spinner } from './Spinner';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
export type ButtonSize = 'sm' | 'md';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  ref?: Ref<HTMLButtonElement>;
}

const variantClasses: Record<ButtonVariant, string> = {
  primary:
    'bg-accent text-on-accent border border-transparent hover:bg-accent-hover',
  secondary:
    'bg-surface text-ink border border-border hover:bg-surface-subtle hover:border-border-strong',
  ghost:
    'bg-transparent text-ink border border-transparent hover:bg-surface-subtle',
  danger:
    'bg-danger text-on-danger border border-transparent hover:bg-danger-hover',
};

const sizeClasses: Record<ButtonSize, string> = {
  sm: 'px-3 py-1.5 text-small',
  md: 'px-4 py-2 text-small',
};

/** Accessible button with variant, size, disabled, and static loading states. */
export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  disabled = false,
  type = 'button',
  className = '',
  children,
  ref,
  ...rest
}: ButtonProps) {
  const isDisabled = disabled || loading;

  return (
    <button
      ref={ref}
      type={type}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={`inline-flex items-center justify-center gap-2 rounded-control font-medium cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 ${variantClasses[variant]} ${sizeClasses[size]} ${className}`.trim()}
      {...rest}
    >
      {loading ? (
        <Spinner size="sm" className="text-current" label="Loading" />
      ) : null}
      {children}
    </button>
  );
}
