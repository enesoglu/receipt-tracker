import { render, screen, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import { resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import type { components } from '../../api/schema.gen';
import { ProductPriceHistoryCard } from './ProductPriceHistoryCard';

const productId = '0193a1b2-0000-7000-8000-000000000001';

function renderPriceHistoryCard() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <QueryProvider client={client}>
      <MemoryRouter>
        <ProductPriceHistoryCard productId={productId} />
      </MemoryRouter>
    </QueryProvider>,
  );
}

// A kg product bought as 1 kg at 100.00 and as 2 x 500 g at 50.00 each:
// both purchases cost 100.00 per kilogram.
const kilogramHistory: components['schemas']['PriceHistory'] = {
  productId,
  points: [
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      lineItemId: '0193a1b2-0000-7000-8000-000000000201',
      purchasedOn: '2026-11-03',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Synthetic Market',
      },
      unitPrice: '100.00',
      quantity: '1',
      quantityUnit: 'KG',
      currency: 'TRY',
      basis: 'kg',
      basisPrice: '100.00',
    },
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000101',
      lineItemId: '0193a1b2-0000-7000-8000-000000000202',
      purchasedOn: '2026-11-04',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Synthetic Market',
      },
      unitPrice: '50.00',
      quantity: '2',
      quantityUnit: 'AD',
      currency: 'TRY',
      basis: 'kg',
      basisPrice: '100.00',
    },
  ],
  stores: [
    {
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Synthetic Market',
      },
      currency: 'TRY',
      min: '50.00',
      max: '100.00',
      latest: '100.00',
      latestOn: '2026-11-04',
      purchaseCount: 2,
    },
  ],
};

const itemHistory: components['schemas']['PriceHistory'] = {
  productId,
  points: [
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      lineItemId: '0193a1b2-0000-7000-8000-000000000201',
      purchasedOn: '2026-11-03',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Synthetic Market',
      },
      unitPrice: '32.50',
      quantity: '1',
      quantityUnit: 'AD',
      currency: 'TRY',
      basis: 'item',
      basisPrice: null,
    },
  ],
  stores: [
    {
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Synthetic Market',
      },
      currency: 'TRY',
      min: '32.50',
      max: '32.50',
      latest: '32.50',
      latestOn: '2026-11-03',
      purchaseCount: 1,
    },
  ],
};

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' });
});

afterEach(() => {
  server.resetHandlers();
  resetMockAuth();
});

afterAll(() => {
  server.close();
});

describe('ProductPriceHistoryCard', () => {
  it('shows the comparable per-kilogram price of a kg product', async () => {
    server.use(
      http.get('/api/v1/products/:productId/price-history', () =>
        HttpResponse.json(kilogramHistory),
      ),
    );

    renderPriceHistoryCard();

    const table = await screen.findByRole('table', {
      name: 'TRY purchase points',
    });
    expect(
      within(table).getByRole('columnheader', { name: 'Per kg' }),
    ).toBeInTheDocument();

    const rows = within(table).getAllByRole('row');
    expect(rows).toHaveLength(3);
    for (const row of rows.slice(1)) {
      const cells = within(row).getAllByRole('cell');
      expect(cells[4]).toHaveTextContent('100.00');
    }

    expect(screen.getByText(/basis prices per kilogram/i)).toBeInTheDocument();
  });

  it('hides the basis column of an item product', async () => {
    server.use(
      http.get('/api/v1/products/:productId/price-history', () =>
        HttpResponse.json(itemHistory),
      ),
    );

    renderPriceHistoryCard();

    const table = await screen.findByRole('table', {
      name: 'TRY purchase points',
    });
    expect(
      within(table).queryByRole('columnheader', { name: 'Per kg' }),
    ).not.toBeInTheDocument();
    expect(
      within(table).queryByRole('columnheader', { name: 'Per litre' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/basis prices per kilogram/i),
    ).not.toBeInTheDocument();
  });
});
