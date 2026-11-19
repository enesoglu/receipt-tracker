import { describe, expect, it } from 'vitest';
import { formatDateValue, formatQuantity } from './format';

// Audit timestamps render in the viewer's local zone. Pin UTC+3 so the
// midnight rollover is deterministic on any host.
process.env.TZ = 'Etc/GMT-3';

describe('formatDateValue', () => {
  it('renders a timestamp on its local calendar day', () => {
    // 21:30Z on the 19th is 00:30 on the 20th at UTC+3.
    expect(formatDateValue('2026-11-19T21:30:00Z', 'date')).toBe(
      'Nov 20, 2026',
    );
    expect(formatDateValue('2026-11-19T21:30:00Z', 'datetime')).toBe(
      'Nov 20, 2026, 00:30',
    );
  });

  it('renders a calendar-only date without shifting the day', () => {
    expect(formatDateValue('2026-11-20', 'date')).toBe('Nov 20, 2026');
  });
});

describe('formatQuantity', () => {
  it.each([
    ['2.000', '2'],
    ['500.000', '500'],
    ['1.250', '1.25'],
    ['0.500', '0.5'],
    ['0.000', '0'],
    ['7', '7'],
    ['-2.000', '-2'],
  ])('strips redundant trailing zeros from %s', (value, expected) => {
    expect(formatQuantity(value)).toBe(expected);
  });

  it('uses the fallback for absent or malformed values', () => {
    expect(formatQuantity(null)).toBe('—');
    expect(formatQuantity(undefined)).toBe('—');
    expect(formatQuantity('  ')).toBe('—');
    expect(formatQuantity('abc', 'N/A')).toBe('N/A');
  });
});
