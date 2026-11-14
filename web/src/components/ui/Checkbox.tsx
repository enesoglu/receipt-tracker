import { useId, type InputHTMLAttributes, type Ref } from 'react';

export interface CheckboxProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type'
> {
  label: string;
  description?: string;
  error?: string;
  ref?: Ref<HTMLInputElement>;
}

/** Labelled checkbox control styled with the accent token. */
export function Checkbox({
  label,
  description,
  error,
  id,
  className = '',
  disabled = false,
  ref,
  ...rest
}: CheckboxProps) {
  const generatedId = useId();
  const checkboxId = id ?? generatedId;
  const descriptionId = description ? `${checkboxId}-description` : undefined;
  const errorId = error ? `${checkboxId}-error` : undefined;
  const describedBy =
    [descriptionId, errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-start gap-2.5">
        <input
          ref={ref}
          id={checkboxId}
          type="checkbox"
          disabled={disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={`mt-1 h-4 w-4 rounded-control border border-border accent-accent cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 ${className}`.trim()}
          {...rest}
        />
        <div className="flex flex-col gap-0.5">
          <label
            htmlFor={checkboxId}
            className={`text-small font-medium ${disabled ? 'cursor-not-allowed text-muted' : 'cursor-pointer text-ink'}`}
          >
            {label}
          </label>
          {description ? (
            <p id={descriptionId} className="text-caption text-muted">
              {description}
            </p>
          ) : null}
        </div>
      </div>
      {error ? (
        <p id={errorId} className="text-caption text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
