import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PageHeader } from './PageHeader';

describe('PageHeader', () => {
  it('renders eyebrow, title, description, and actions', () => {
    render(
      <PageHeader
        eyebrow="Catalogue"
        title="Products"
        description="Matched grocery products across stores."
        actions={<button type="button">Merge</button>}
      />,
    );

    expect(screen.getByText('Catalogue')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Products',
    );
    expect(
      screen.getByText('Matched grocery products across stores.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Merge' })).toBeInTheDocument();
  });
});
