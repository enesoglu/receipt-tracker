import {
  useId,
  type ReactNode,
  type Ref,
  type SelectHTMLAttributes,
} from 'react';

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label: string;
  options?: readonly SelectOption[];
  placeholder?: string;
  error?: string;
  hint?: string;
  children?: ReactNode;
  ref?: Ref<HTMLSelectElement>;
}

/** Labelled native select control with option list, hint, and error state. */
export function Select({
  label,
  options,
  placeholder,
  error,
  hint,
  id,
  className = '',
  disabled = false,
  children,
  ref,
  ...rest
}: SelectProps) {
  const generatedId = useId();
  const selectId = id ?? generatedId;
  const hintId = hint ? `${selectId}-hint` : undefined;
  const errorId = error ? `${selectId}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={selectId} className="text-small font-medium text-ink">
        {label}
      </label>
      <select
        ref={ref}
        id={selectId}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={`w-full rounded-control border bg-surface px-3 py-2 text-body text-ink hover:border-border-strong disabled:cursor-not-allowed disabled:bg-surface-subtle disabled:text-muted ${error ? 'border-danger' : 'border-border'} ${className}`.trim()}
        {...rest}
      >
        {placeholder ? <option value="">{placeholder}</option> : null}
        {options?.map((option) => (
          <option
            key={option.value}
            value={option.value}
            disabled={option.disabled}
          >
            {option.label}
          </option>
        ))}
        {children}
      </select>
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
