import { formatMoney } from './format';

export interface MoneyProps {
  value: string | null | undefined;
  currency: string;
  locale?: string;
  fallback?: string;
  className?: string;
}

/** Formats a decimal money string with its ISO 4217 currency via Intl.NumberFormat. */
export function Money({
  value,
  currency,
  locale = 'en-US',
  fallback = '—',
  className = '',
}: MoneyProps) {
  const formatted = formatMoney(value, currency, locale, fallback);
  const isEmpty = formatted === fallback;

  return (
    <span
      className={`font-mono tabular-nums ${isEmpty ? 'text-muted' : ''} ${className}`.trim()}
    >
      {formatted}
    </span>
  );
}
