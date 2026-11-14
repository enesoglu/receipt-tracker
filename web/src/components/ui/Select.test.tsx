import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Select } from './Select';

describe('Select', () => {
  it('renders labelled options, placeholder, hint, and error', () => {
    render(
      <Select
        label="Currency"
        placeholder="Choose currency"
        hint="Default currency for new uploads."
        error="Select a currency."
        defaultValue=""
        options={[
          { value: 'TRY', label: 'Turkish Lira (TRY)' },
          { value: 'EUR', label: 'Euro (EUR)' },
        ]}
      />,
    );

    const select = screen.getByLabelText('Currency');
    expect(select).toHaveAttribute('aria-invalid', 'true');
    expect(select).toHaveAccessibleDescription(
      'Default currency for new uploads. Select a currency.',
    );
    expect(
      screen.getByRole('option', { name: 'Choose currency' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('option', { name: 'Turkish Lira (TRY)' }),
    ).toBeInTheDocument();
  });

  it('supports selection changes, disabled state, and keyboard events', () => {
    const onChange = vi.fn();
    const onKeyDown = vi.fn();
    render(
      <div>
        <Select
          label="Category"
          defaultValue="dairy"
          onChange={onChange}
          onKeyDown={onKeyDown}
          options={[
            { value: 'dairy', label: 'Dairy' },
            { value: 'bakery', label: 'Bakery' },
          ]}
        />
        <Select
          label="Readonly"
          disabled
          options={[{ value: 'a', label: 'A' }]}
        />
      </div>,
    );

    const select = screen.getByLabelText('Category');
    select.focus();
    expect(select).toHaveFocus();
    fireEvent.keyDown(select, { key: 'ArrowDown' });
    fireEvent.change(select, { target: { value: 'bakery' } });
    expect(onKeyDown).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(select).toHaveValue('bakery');
    expect(screen.getByLabelText('Readonly')).toBeDisabled();
  });
});
