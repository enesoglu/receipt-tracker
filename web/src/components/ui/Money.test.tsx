import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Money } from './Money';

describe('Money', () => {
  it('formats positive, negative, and null decimal strings via Intl.NumberFormat', () => {
    render(
      <div>
        <div data-testid="pos">
          <Money value="84.50" currency="TRY" />
        </div>
        <div data-testid="neg">
          <Money value="-5.00" currency="EUR" />
        </div>
        <div data-testid="empty">
          <Money value={null} currency="TRY" />
        </div>
      </div>,
    );

    expect(screen.getByTestId('pos')).toHaveTextContent('84.50');
    expect(screen.getByTestId('neg')).toHaveTextContent('-€5.00');
    expect(screen.getByTestId('empty')).toHaveTextContent('—');
  });

  it('handles unknown currency codes and malformed values safely', () => {
    render(
      <div>
        <div data-testid="unknown">
          <Money value="12.00" currency="XYZ" />
        </div>
        <div data-testid="bad">
          <Money value="not-a-number" currency="USD" fallback="N/A" />
        </div>
      </div>,
    );

    expect(screen.getByTestId('unknown')).toHaveTextContent('12.00');
    expect(screen.getByTestId('bad')).toHaveTextContent('N/A');
  });
});
