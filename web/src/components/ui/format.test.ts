import { describe, expect, it } from 'vitest';
import { formatDateValue } from './format';

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
