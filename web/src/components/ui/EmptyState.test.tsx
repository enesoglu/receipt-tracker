import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { EmptyState } from './EmptyState';

describe('EmptyState', () => {
  it('renders status region with title, description, and action', () => {
    const onUpload = vi.fn();
    render(
      <EmptyState
        title="No receipts yet"
        description="Upload a photo to begin."
        action={
          <button type="button" onClick={onUpload}>
            Upload
          </button>
        }
      />,
    );

    expect(screen.getByRole('status')).toHaveTextContent('No receipts yet');
    expect(screen.getByRole('status')).toHaveTextContent(
      'Upload a photo to begin.',
    );
    const button = screen.getByRole('button', { name: 'Upload' });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.click(button);
    expect(onUpload).toHaveBeenCalledTimes(1);
  });
});
