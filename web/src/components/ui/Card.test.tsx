import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Card } from './Card';

describe('Card', () => {
  it('renders titled section with description, actions, and body', () => {
    render(
      <Card
        title="Receipt summary"
        description="Totals from the store"
        actions={<button type="button">Edit</button>}
      >
        <p>Body content</p>
      </Card>,
    );

    const region = screen.getByRole('region', { name: 'Receipt summary' });
    expect(region).toHaveTextContent('Totals from the store');
    expect(region).toHaveTextContent('Body content');
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
  });

  it('renders body cleanly without a header', () => {
    render(
      <Card padding="none">
        <div>Compact body</div>
      </Card>,
    );
    expect(screen.getByText('Compact body')).toBeInTheDocument();
  });
});
