export interface SpinnerProps {
  size?: 'sm' | 'md' | 'lg';
  label?: string;
  showLabel?: boolean;
  className?: string;
}

const sizeClasses: Record<NonNullable<SpinnerProps['size']>, string> = {
  sm: 'h-4 w-4',
  md: 'h-5 w-5',
  lg: 'h-8 w-8',
};

/** Static loading indicator (motion-free per UI policy). */
export function Spinner({
  size = 'md',
  label = 'Loading',
  showLabel = false,
  className = '',
}: SpinnerProps) {
  return (
    <span
      role="status"
      className={`inline-flex items-center gap-2 text-accent ${className}`.trim()}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        aria-hidden="true"
        className={sizeClasses[size]}
      >
        <circle
          cx="12"
          cy="12"
          r="9"
          stroke="currentColor"
          strokeOpacity="0.25"
          strokeWidth="3"
        />
        <path
          d="M21 12a9 9 0 0 0-9-9"
          stroke="currentColor"
          strokeWidth="3"
          strokeLinecap="round"
        />
      </svg>
      <span
        className={showLabel ? 'text-small font-medium text-muted' : 'sr-only'}
      >
        {label}
      </span>
    </span>
  );
}
