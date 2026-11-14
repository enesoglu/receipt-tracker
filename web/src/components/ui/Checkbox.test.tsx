import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Checkbox } from './Checkbox';

describe('Checkbox', () => {
  it('renders labelled checkbox with description and error', () => {
    render(
      <Checkbox
        label="Keep original image"
        description="Retains the untouched HEIC upload."
        error="Confirmation is required."
      />,
    );

    const checkbox = screen.getByRole('checkbox', {
      name: 'Keep original image',
    });
    expect(checkbox).toHaveAttribute('aria-invalid', 'true');
    expect(checkbox).toHaveAccessibleDescription(
      'Retains the untouched HEIC upload. Confirmation is required.',
    );
  });

  it('toggles via click, supports keyboard focus, and respects disabled', () => {
    const onChange = vi.fn();
    const onKeyDown = vi.fn();
    render(
      <div>
        <Checkbox
          label="Accept total"
          onChange={onChange}
          onKeyDown={onKeyDown}
        />
        <Checkbox label="Locked option" disabled checked readOnly />
      </div>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Accept total' });
    checkbox.focus();
    expect(checkbox).toHaveFocus();
    fireEvent.keyDown(checkbox, { key: ' ' });
    fireEvent.click(checkbox);
    expect(onKeyDown).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(checkbox).toBeChecked();

    const locked = screen.getByRole('checkbox', { name: 'Locked option' });
    expect(locked).toBeDisabled();
    expect(locked).toBeChecked();
  });
});
