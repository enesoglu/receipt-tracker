import { formatDateValue, type DateDisplayFormat } from './format';

export interface DateTextProps {
  value: string | null | undefined;
  format?: DateDisplayFormat;
  locale?: string;
  fallback?: string;
  className?: string;
}

/** Formats YYYY-MM-DD dates, YYYY-MM months, and RFC 3339 timestamps in UTC. */
export function DateText({
  value,
  format = 'date',
  locale = 'en-US',
  fallback = '—',
  className = '',
}: DateTextProps) {
  const formatted = formatDateValue(value, format, locale, fallback);
  if (formatted === fallback || value == null) {
    return <span className={`text-muted ${className}`.trim()}>{fallback}</span>;
  }

  return (
    <time dateTime={value} className={`tabular-nums ${className}`.trim()}>
      {formatted}
    </time>
  );
}
