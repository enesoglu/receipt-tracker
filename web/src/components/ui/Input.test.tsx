import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Input } from './Input';

describe('Input', () => {
  it('associates label, hint, and error text with the input', () => {
    render(
      <Input
        label="Store name"
        hint="Printed at the top of the receipt."
        error="Store name is required."
        defaultValue=""
      />,
    );

    const input = screen.getByLabelText('Store name');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription(
      'Printed at the top of the receipt. Store name is required.',
    );
  });

  it('handles disabled state, keyboard input, and focus', () => {
    const onChange = vi.fn();
    const onKeyDown = vi.fn();
    render(
      <div>
        <Input label="Search" onChange={onChange} onKeyDown={onKeyDown} />
        <Input label="Locked" disabled value="TRY" readOnly />
      </div>,
    );

    const search = screen.getByLabelText('Search');
    search.focus();
    expect(search).toHaveFocus();
    fireEvent.change(search, { target: { value: 'ayran' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onKeyDown).toHaveBeenCalledTimes(1);

    expect(screen.getByLabelText('Locked')).toBeDisabled();
  });
});
