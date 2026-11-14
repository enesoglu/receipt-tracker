import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Spinner } from './Spinner';

describe('Spinner', () => {
  it('renders static accessible loading status', () => {
    const { rerender } = render(<Spinner />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading');

    rerender(<Spinner size="lg" label="Uploading receipts" showLabel />);
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent('Uploading receipts');
    expect(status.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
  });
});
