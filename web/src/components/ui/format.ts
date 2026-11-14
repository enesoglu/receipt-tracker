export type DateDisplayFormat = 'date' | 'datetime' | 'month';

/** Formats a decimal string and ISO 4217 currency code without float arithmetic. */
export function formatMoney(
  value: string | null | undefined,
  currency: string,
  locale = 'en-US',
  fallback = '—',
): string {
  if (value == null) return fallback;
  const trimmed = value.trim();
  if (!trimmed || !/^-?\d+(?:\.\d+)?$/.test(trimmed)) return fallback;
  const numeric = Number(trimmed);
  if (!Number.isFinite(numeric)) return fallback;
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
    }).format(numeric);
  } catch {
    return `${trimmed} ${currency}`;
  }
}

/** Formats YYYY-MM-DD dates, YYYY-MM months, or RFC 3339 timestamps in UTC. */
export function formatDateValue(
  value: string | null | undefined,
  format: DateDisplayFormat = 'date',
  locale = 'en-US',
  fallback = '—',
): string {
  if (value == null) return fallback;
  const trimmed = value.trim();
  if (!trimmed) return fallback;

  if (format === 'month') {
    const match = /^(\d{4})-(0[1-9]|1[0-2])$/.exec(trimmed);
    if (!match) return fallback;
    const year = Number(match[1]);
    const month = Number(match[2]);
    return new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'short',
      timeZone: 'UTC',
    }).format(new Date(Date.UTC(year, month - 1, 1)));
  }

  if (format === 'date') {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(trimmed);
    if (match) {
      const year = Number(match[1]);
      const month = Number(match[2]);
      const day = Number(match[3]);
      const utcDate = new Date(Date.UTC(year, month - 1, day));
      if (
        utcDate.getUTCFullYear() !== year ||
        utcDate.getUTCMonth() !== month - 1 ||
        utcDate.getUTCDate() !== day
      ) {
        return fallback;
      }
      return new Intl.DateTimeFormat(locale, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        timeZone: 'UTC',
      }).format(utcDate);
    }
  }

  const parsed = new Date(trimmed);
  if (Number.isNaN(parsed.getTime())) return fallback;

  if (format === 'datetime') {
    return new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'UTC',
    }).format(parsed);
  }

  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    timeZone: 'UTC',
  }).format(parsed);
}
