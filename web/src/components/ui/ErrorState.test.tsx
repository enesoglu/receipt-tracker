import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ErrorState } from './ErrorState';

describe('ErrorState', () => {
  it('renders alert with retry button supporting click and keyboard focus', () => {
    const onRetry = vi.fn();
    render(
      <ErrorState
        title="Could not load receipts"
        message="Service unavailable."
        onRetry={onRetry}
      />,
    );

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Could not load receipts');
    expect(alert).toHaveTextContent('Service unavailable.');

    const retry = screen.getByRole('button', { name: 'Try again' });
    retry.focus();
    expect(retry).toHaveFocus();
    fireEvent.click(retry);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('omits retry button when onRetry is not provided', () => {
    render(<ErrorState message="Unexpected failure." />);
    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong');
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
