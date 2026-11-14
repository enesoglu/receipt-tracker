import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Tabs } from './Tabs';

const items = [
  { id: 'lines', label: 'Line items', badge: 4, content: <p>Lines panel</p> },
  { id: 'flags', label: 'Flags', badge: 2, content: <p>Flags panel</p> },
  {
    id: 'disabled',
    label: 'History',
    disabled: true,
    content: <p>History panel</p>,
  },
  { id: 'image', label: 'Image', content: <p>Image panel</p> },
];

describe('Tabs', () => {
  it('switches panels on click and supports controlled mode', () => {
    const onChange = vi.fn();
    render(<Tabs label="Receipt views" items={items} onChange={onChange} />);

    expect(screen.getByRole('tabpanel')).toHaveTextContent('Lines panel');
    fireEvent.click(screen.getByRole('tab', { name: /Flags/ }));
    expect(onChange).toHaveBeenCalledWith('flags');
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Flags panel');
  });

  it('supports ArrowRight, ArrowLeft, Home, and End skipping disabled tabs', () => {
    render(<Tabs label="Receipt views" items={items} />);

    const linesTab = screen.getByRole('tab', { name: /Line items/ });
    const flagsTab = screen.getByRole('tab', { name: /Flags/ });
    const imageTab = screen.getByRole('tab', { name: 'Image' });

    linesTab.focus();
    fireEvent.keyDown(linesTab, { key: 'ArrowRight' });
    expect(flagsTab).toHaveFocus();
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Flags panel');

    // Skips disabled "History" tab and moves to "Image".
    fireEvent.keyDown(flagsTab, { key: 'ArrowRight' });
    expect(imageTab).toHaveFocus();
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Image panel');

    fireEvent.keyDown(imageTab, { key: 'ArrowLeft' });
    expect(flagsTab).toHaveFocus();

    fireEvent.keyDown(flagsTab, { key: 'Home' });
    expect(linesTab).toHaveFocus();

    fireEvent.keyDown(linesTab, { key: 'End' });
    expect(imageTab).toHaveFocus();
  });
});
