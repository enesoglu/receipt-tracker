import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { resetMockState, setMockAuthMode } from '../../mocks/data';
import { server } from '../../mocks/server';
import { CategoriesPage } from './CategoriesPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderCategoriesPage() {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/categories']}>
        <CategoriesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('CategoriesPage', () => {
  beforeAll(() => {
    server.listen({ onUnhandledRequest: 'error' });
  });

  afterEach(() => {
    server.resetHandlers();
    resetMockState();
    setMockAuthMode('signed_in');
  });

  afterAll(() => {
    server.close();
  });

  it('renders loading, error retry, and empty category states', async () => {
    server.use(
      http.get('/api/v1/categories', () => {
        return HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'category store unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000099',
          },
          { status: 500 },
        );
      }),
    );

    renderCategoriesPage();
    expect(screen.getByText('Loading categories…')).toBeInTheDocument();

    expect(
      await screen.findByRole('heading', { name: 'Could not load categories' }),
    ).toBeInTheDocument();
    expect(screen.getByText('category store unavailable')).toBeInTheDocument();

    server.use(
      http.get('/api/v1/categories', () => {
        return HttpResponse.json({ items: [], nextCursor: null });
      }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('No categories yet')).toBeInTheDocument();
  });

  it('creates categories, validates blank names, and surfaces 409 duplicate conflicts', async () => {
    renderCategoriesPage();

    expect(
      await screen.findByRole('cell', { name: 'Dairy' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '9 products' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Create category' }));
    expect(
      await screen.findByText('Category name is required.'),
    ).toBeInTheDocument();

    const nameInput = screen.getByLabelText('Category name');
    fireEvent.change(nameInput, { target: { value: 'dairy' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create category' }));

    expect(
      await screen.findByRole('heading', { name: 'Could not create category' }),
    ).toBeInTheDocument();
    expect(
      screen.getByText('a category with this name already exists'),
    ).toBeInTheDocument();

    fireEvent.change(nameInput, { target: { value: 'Beverages' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create category' }));

    expect(
      await screen.findByText('Category "Beverages" created.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Beverages' })).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '0 products' }),
    ).toBeInTheDocument();
  });

  it('renames categories and surfaces 409 conflicts inside the rename dialog', async () => {
    renderCategoriesPage();

    expect(
      await screen.findByRole('cell', { name: 'Bakery' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Rename Bakery' }));

    const dialog = await screen.findByRole('dialog');
    const renameInput = within(dialog).getByLabelText('New name');
    fireEvent.change(renameInput, { target: { value: 'DAIRY' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save name' }));

    expect(
      await within(dialog).findByRole('heading', {
        name: 'Could not rename category',
      }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText('a category with this name already exists'),
    ).toBeInTheDocument();

    fireEvent.change(renameInput, { target: { value: 'Bread & Bakery' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save name' }));

    expect(
      await screen.findByText('Category renamed to "Bread & Bakery".'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: 'Bread & Bakery' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('cell', { name: 'Bakery' }),
    ).not.toBeInTheDocument();
  });

  it('shows an explicit in-use warning when deleting a category with products and deletes cleanly', async () => {
    renderCategoriesPage();

    expect(
      await screen.findByRole('cell', { name: 'Dairy' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Delete Dairy' }));

    const dialog = await screen.findByRole('dialog');
    const alert = within(dialog).getByRole('alert');
    expect(alert).toHaveTextContent('Category currently in use by 9 products');
    expect(alert).toHaveTextContent(
      '9 products will stay in your catalog as uncategorised.',
    );

    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Delete category' }),
    );

    expect(
      await screen.findByText('Category "Dairy" deleted.'),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(
        screen.queryByRole('cell', { name: 'Dairy' }),
      ).not.toBeInTheDocument();
    });

    fireEvent.change(screen.getByLabelText('Category name'), {
      target: { value: 'Snacks' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create category' }));
    expect(
      await screen.findByRole('cell', { name: 'Snacks' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Delete Snacks' }));
    const unusedDialog = await screen.findByRole('dialog');
    expect(within(unusedDialog).queryByRole('alert')).not.toBeInTheDocument();
    expect(
      within(unusedDialog).getByText(/No products currently use/i),
    ).toBeInTheDocument();

    fireEvent.click(
      within(unusedDialog).getByRole('button', { name: 'Delete category' }),
    );
    expect(
      await screen.findByText('Category "Snacks" deleted.'),
    ).toBeInTheDocument();
  });
});
