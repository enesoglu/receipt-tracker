import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { UiPreviewPage } from './UiPreviewPage';

describe('UiPreviewPage', () => {
  it('renders all UI primitives and supports dialog and table interactions', () => {
    render(<UiPreviewPage />);

    expect(
      screen.getByRole('heading', { level: 1, name: 'UI Primitives' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('table', { name: 'Sample receipts' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('tablist', { name: 'Preview tabs' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('row', { name: 'Select Example Market' }));
    expect(
      screen.getByText('Selected row: Example Market'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Open dialog' }));
    expect(
      screen.getByRole('dialog', { name: 'Confirm action' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
