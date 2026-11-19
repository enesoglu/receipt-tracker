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
import type { components } from '../../api/schema.gen';
import { resetMockState, setMockAuthMode } from '../../mocks/data';
import { server } from '../../mocks/server';
import { ProductsListPage } from './ProductsListPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderProductsList() {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/products']}>
        <ProductsListPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('ProductsListPage', () => {
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

  it('shows a loading indicator while products are loading', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });

    server.use(
      http.get('/api/v1/products', async () => {
        await gate;
        return HttpResponse.json({ items: [], nextCursor: null });
      }),
    );

    renderProductsList();
    expect(screen.getByText('Loading products…')).toBeInTheDocument();

    release();
    await waitFor(() =>
      expect(screen.getByText('No products yet')).toBeInTheDocument(),
    );
  });

  it('renders an error alert and retries loading products', async () => {
    let failNext = true;

    server.use(
      http.get('/api/v1/products', () => {
        if (failNext) {
          failNext = false;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Internal Server Error',
              status: 500,
              detail: 'Product index unavailable',
            },
            { status: 500 },
          );
        }
        return HttpResponse.json({
          items: [
            {
              id: '0193a1b2-0000-7000-8000-000000000401',
              canonicalName: 'Ayran 1 l',
              category: {
                id: '0193a1b2-0000-7000-8000-000000000020',
                name: 'Dairy',
              },
              sizeValue: '1',
              sizeUnit: 'l',
              purchaseCount: 6,
              lastPurchase: null,
            },
          ],
          nextCursor: null,
        });
      }),
    );

    renderProductsList();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Could not load products');
    expect(alert).toHaveTextContent('Product index unavailable');

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Ayran 1 l' }),
      ).toBeInTheDocument(),
    );
  });

  it('shows the initial empty state linking to upload when there are no products', async () => {
    server.use(
      http.get('/api/v1/products', () =>
        HttpResponse.json({ items: [], nextCursor: null }),
      ),
    );

    renderProductsList();

    await waitFor(() =>
      expect(screen.getByText('No products yet')).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Upload receipts' }),
    ).toHaveAttribute('href', '/upload');
  });

  it('renders package sizes without trailing zeros', async () => {
    server.use(
      http.get('/api/v1/products', () =>
        HttpResponse.json({
          items: [
            {
              id: '0193a1b2-0000-7000-8000-000000000409',
              canonicalName: 'Flour',
              category: null,
              sizeValue: '500.000',
              sizeUnit: 'g',
              purchaseCount: 1,
              lastPurchase: null,
            },
          ],
          nextCursor: null,
        }),
      ),
    );

    renderProductsList();

    expect(await screen.findByText('500 g')).toBeInTheDocument();
    expect(screen.queryByText('500.000 g')).not.toBeInTheDocument();
  });

  it('lists products, searches by query and alias, and filters by category', async () => {
    renderProductsList();

    const ayranLink = await screen.findByRole('link', { name: 'Ayran 1 l' });
    expect(ayranLink).toHaveAttribute(
      'href',
      '/products/0193a1b2-0000-7000-8000-000000000401',
    );
    expect(
      screen.getByRole('link', { name: 'Ekmek 350 g' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/Example Market/)).toBeInTheDocument();
    expect(screen.getByText('Nov 2, 2026')).toBeInTheDocument();

    // Filter by search query matching alias text.
    fireEvent.change(screen.getByLabelText('Search'), {
      target: { value: 'ekmek' },
    });
    await waitFor(() => {
      expect(
        screen.getByRole('link', { name: 'Ekmek 350 g' }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole('link', { name: 'Ayran 1 l' }),
      ).not.toBeInTheDocument();
    });

    // Non-matching search displays filtered empty state.
    fireEvent.change(screen.getByLabelText('Search'), {
      target: { value: 'nonexistent' },
    });
    await waitFor(() =>
      expect(screen.getByText('No matching products')).toBeInTheDocument(),
    );

    fireEvent.click(
      within(screen.getByRole('status')).getByRole('button', {
        name: 'Clear filters',
      }),
    );
    await waitFor(() => {
      expect(
        screen.getByRole('link', { name: 'Ayran 1 l' }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole('link', { name: 'Ekmek 350 g' }),
      ).toBeInTheDocument();
    });

    // Filter by category.
    await waitFor(() =>
      expect(screen.getByRole('option', { name: 'Dairy' })).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText('Category'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000020' },
    });
    await waitFor(() => {
      expect(
        screen.getByRole('link', { name: 'Ayran 1 l' }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole('link', { name: 'Ekmek 350 g' }),
      ).not.toBeInTheDocument();
    });
  });

  it('appends pages when clicking Load more and resets when filters change', async () => {
    const page1: components['schemas']['ProductSummary'] = {
      id: '0193a1b2-0000-7000-8000-000000000401',
      canonicalName: 'Ayran 1 l',
      category: {
        id: '0193a1b2-0000-7000-8000-000000000020',
        name: 'Dairy',
      },
      sizeValue: '1',
      sizeUnit: 'l',
      priceBasis: 'item',
      purchaseCount: 6,
      lastPurchase: null,
    };
    const page2: components['schemas']['ProductSummary'] = {
      id: '0193a1b2-0000-7000-8000-000000000402',
      canonicalName: 'Whole Milk 1 l',
      category: {
        id: '0193a1b2-0000-7000-8000-000000000020',
        name: 'Dairy',
      },
      sizeValue: '1',
      sizeUnit: 'l',
      priceBasis: 'item',
      purchaseCount: 3,
      lastPurchase: null,
    };

    server.use(
      http.get('/api/v1/products', ({ request }) => {
        const url = new URL(request.url);
        const cursor = url.searchParams.get('cursor');
        const q = url.searchParams.get('q');
        if (q) {
          return HttpResponse.json({
            items: [page1, page2].filter((item) =>
              item.canonicalName.toLowerCase().includes(q.toLowerCase()),
            ),
            nextCursor: null,
          });
        }
        if (cursor === 'page-2') {
          return HttpResponse.json({
            items: [page2],
            nextCursor: null,
          });
        }
        return HttpResponse.json({
          items: [page1],
          nextCursor: 'page-2',
        });
      }),
    );

    renderProductsList();

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Ayran 1 l' }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.queryByRole('link', { name: 'Whole Milk 1 l' }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Whole Milk 1 l' }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole('link', { name: 'Ayran 1 l' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Load more' }),
    ).not.toBeInTheDocument();

    // Changing search resets cursor so it does not concatenate prior page items.
    fireEvent.change(screen.getByLabelText('Search'), {
      target: { value: 'milk' },
    });
    expect(
      await screen.findByRole('link', { name: 'Whole Milk 1 l' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Ayran 1 l' }),
    ).not.toBeInTheDocument();
  });
});
