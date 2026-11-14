import { useId, type InputHTMLAttributes, type Ref } from 'react';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  error?: string;
  hint?: string;
  ref?: Ref<HTMLInputElement>;
}

/** Labelled text/number/date input with hint, error, and focus styling. */
export function Input({
  label,
  error,
  hint,
  id,
  className = '',
  disabled = false,
  ref,
  ...rest
}: InputProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={inputId} className="text-small font-medium text-ink">
        {label}
      </label>
      <input
        ref={ref}
        id={inputId}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={`w-full rounded-control border bg-surface px-3 py-2 text-body text-ink placeholder:text-muted hover:border-border-strong disabled:cursor-not-allowed disabled:bg-surface-subtle disabled:text-muted ${error ? 'border-danger' : 'border-border'} ${className}`.trim()}
        {...rest}
      />
      {hint ? (
        <p id={hintId} className="text-caption text-muted">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errorId} className="text-caption text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
