import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DateText } from './DateText';

describe('DateText', () => {
  it('formats calendar dates, months, and UTC timestamps without timezone shift', () => {
    render(
      <div>
        <DateText value="2026-11-02" />
        <DateText value="2026-11" format="month" />
        <DateText value="2026-11-02T16:05:12Z" format="datetime" />
        <DateText value={null} fallback="Unknown date" />
      </div>,
    );

    expect(screen.getByText('Nov 2, 2026')).toHaveAttribute(
      'dateTime',
      '2026-11-02',
    );
    expect(screen.getByText('Nov 2026')).toHaveAttribute('dateTime', '2026-11');
    expect(screen.getByText(/Nov 2, 2026.*16:05/)).toHaveAttribute(
      'dateTime',
      '2026-11-02T16:05:12Z',
    );
    expect(screen.getByText('Unknown date')).toBeInTheDocument();
  });
});
