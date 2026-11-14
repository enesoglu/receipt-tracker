import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Button } from './Button';

describe('Button', () => {
  it('renders variants, sizes, and triggers click', () => {
    const onClick = vi.fn();
    render(
      <div>
        <Button onClick={onClick}>Save</Button>
        <Button variant="secondary" size="sm">
          Cancel
        </Button>
        <Button variant="ghost">Reset</Button>
        <Button variant="danger">Delete</Button>
      </div>,
    );

    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toHaveAttribute('type', 'button');
    fireEvent.click(save);
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('disables interaction when disabled or loading', () => {
    const onClick = vi.fn();
    render(
      <div>
        <Button disabled onClick={onClick}>
          Disabled
        </Button>
        <Button loading onClick={onClick}>
          Submitting
        </Button>
      </div>,
    );

    const disabledBtn = screen.getByRole('button', { name: 'Disabled' });
    const loadingBtn = screen.getByRole('button', { name: /Submitting/ });

    expect(disabledBtn).toBeDisabled();
    expect(loadingBtn).toBeDisabled();
    expect(loadingBtn).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('status')).toHaveTextContent('Loading');

    fireEvent.click(disabledBtn);
    fireEvent.click(loadingBtn);
    expect(onClick).not.toHaveBeenCalled();
  });

  it('supports keyboard focus and key presses', () => {
    const onKeyDown = vi.fn();
    render(<Button onKeyDown={onKeyDown}>Submit</Button>);

    const button = screen.getByRole('button', { name: 'Submit' });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.keyDown(button, { key: 'Enter' });
    fireEvent.keyDown(button, { key: ' ' });
    expect(onKeyDown).toHaveBeenCalledTimes(2);
  });
});
