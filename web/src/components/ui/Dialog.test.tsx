import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Dialog } from './Dialog';

function DialogHarness() {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button type="button" onClick={() => setOpen(true)}>
        Open modal
      </button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title="Merge products"
        description="Moves aliases and purchases into this product."
        footer={
          <button type="button" onClick={() => setOpen(false)}>
            Confirm
          </button>
        }
      >
        <input aria-label="Target name" />
      </Dialog>
    </div>
  );
}

describe('Dialog', () => {
  it('renders modal content, closes on Escape, and restores focus to trigger', () => {
    render(<DialogHarness />);

    const trigger = screen.getByRole('button', { name: 'Open modal' });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = screen.getByRole('dialog', { name: 'Merge products' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleDescription(
      'Moves aliases and purchases into this product.',
    );
    expect(screen.getByRole('button', { name: 'Close dialog' })).toHaveFocus();

    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('closes on backdrop click and wraps Tab focus inside dialog', () => {
    const onClose = vi.fn();
    render(
      <Dialog
        open
        onClose={onClose}
        title="Delete receipt"
        footer={<button type="button">Delete</button>}
      >
        <p>This cannot be undone.</p>
      </Dialog>,
    );

    const closeBtn = screen.getByRole('button', { name: 'Close dialog' });
    const deleteBtn = screen.getByRole('button', { name: 'Delete' });

    closeBtn.focus();
    fireEvent.keyDown(closeBtn, { key: 'Tab', shiftKey: true });
    expect(deleteBtn).toHaveFocus();

    fireEvent.keyDown(deleteBtn, { key: 'Tab' });
    expect(closeBtn).toHaveFocus();

    const dialog = screen.getByRole('dialog');
    const backdrop = dialog.parentElement as HTMLElement;
    fireEvent.click(backdrop);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
