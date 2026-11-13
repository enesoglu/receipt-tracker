import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { HomePage } from './HomePage';

describe('HomePage', () => {
  it('explains the empty state without offering unavailable actions', () => {
    render(<HomePage />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Your groceries, clearly recorded.',
    );
    expect(screen.getByRole('status')).toHaveTextContent('No receipts yet.');
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
