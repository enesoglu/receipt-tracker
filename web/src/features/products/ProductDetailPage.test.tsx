import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import type { components } from '../../api/schema.gen';
import { mockProduct, resetMockState, setMockAuthMode } from '../../mocks/data';
import { server } from '../../mocks/server';
import { ProductDetailPage } from './ProductDetailPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderProductDetail(productId = mockProduct.id) {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/products/${productId}`]}>
        <Routes>
          <Route path="/products/:productId" element={<ProductDetailPage />} />
          <Route path="/products" element={<h1>Products catalog</h1>} />
          <Route path="/receipts/:receiptId" element={<h1>Receipt view</h1>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('ProductDetailPage', () => {
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

  it('renders loading, error retry, and 404 not-found states', async () => {
    let gateRelease!: () => void;
    const gate = new Promise<void>((resolve) => {
      gateRelease = resolve;
    });
    let attempts = 0;

    server.use(
      http.get('/api/v1/products/:productId', async () => {
        attempts += 1;
        if (attempts === 1) {
          await gate;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Internal Server Error',
              status: 500,
              detail: 'Product service offline',
            },
            { status: 500 },
          );
        }
        return HttpResponse.json(mockProduct);
      }),
    );

    renderProductDetail();
    expect(screen.getByText('Loading product…')).toBeInTheDocument();

    gateRelease();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Could not load product');
    expect(alert).toHaveTextContent('Product service offline');

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Ayran 1 l' }),
      ).toBeInTheDocument(),
    );

    // 404 state renders missing-product callout with link back to catalog.
    server.resetHandlers();
    renderProductDetail('0193a1b2-0000-7000-8000-000000009999');
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: 'Product not found',
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Back to products' }),
    ).toHaveAttribute('href', '/products');
  });

  it('prefills the package size without trailing zeros', async () => {
    server.use(
      http.get('/api/v1/products/:productId', () =>
        HttpResponse.json({
          ...mockProduct,
          sizeValue: '500.000',
          sizeUnit: 'g',
        }),
      ),
    );

    renderProductDetail();

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Ayran 1 l' }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Package size')).toHaveValue('500');
  });

  it('validates and saves updated canonical name, category, and package size', async () => {
    renderProductDetail();

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Ayran 1 l' }),
    ).toBeInTheDocument();

    // Validation requires package size and unit together.
    fireEvent.change(screen.getByLabelText('Size unit'), {
      target: { value: '' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Provide both a package size and unit, or leave both blank.',
    );

    // Fix fields and submit valid update.
    fireEvent.change(screen.getByLabelText('Canonical name'), {
      target: { value: 'Ayran Traditional 1.5 l' },
    });
    await waitFor(() =>
      expect(
        screen.getByRole('option', { name: 'Bakery' }),
      ).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText('Category'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000021' },
    });
    fireEvent.change(screen.getByLabelText('Package size'), {
      target: { value: '1.5' },
    });
    fireEvent.change(screen.getByLabelText('Size unit'), {
      target: { value: 'l' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText('Product details saved.'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: 'Ayran Traditional 1.5 l',
      }),
    ).toBeInTheDocument();
  });

  it('normalises decimal commas in the package size and shows field-level errors', async () => {
    const updates: Array<Record<string, unknown>> = [];
    let failNextSave = false;

    server.use(
      http.put('/api/v1/products/:productId', async ({ request }) => {
        if (failNextSave) {
          failNextSave = false;
          return HttpResponse.json(
            {
              type: 'https://receipt-tracker.example/problems/invalid',
              title: 'Bad Request',
              status: 400,
              detail: 'invalid size',
              errors: [
                {
                  field: 'sizeValue',
                  message: 'must be positive with at most three decimal places',
                },
              ],
            },
            {
              status: 400,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          );
        }
        updates.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json(mockProduct);
      }),
    );

    renderProductDetail();

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Ayran 1 l' }),
      ).toBeInTheDocument(),
    );

    // A decimal comma is accepted and sent as a dot.
    fireEvent.change(screen.getByLabelText('Package size'), {
      target: { value: '1,5' },
    });
    fireEvent.change(screen.getByLabelText('Size unit'), {
      target: { value: 'l' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText('Product details saved.'),
    ).toBeInTheDocument();
    expect(updates).toMatchObject([
      { canonicalName: 'Ayran 1 l', sizeValue: '1.5', sizeUnit: 'l' },
    ]);

    // A zero size is rejected before it is sent.
    fireEvent.change(screen.getByLabelText('Package size'), {
      target: { value: '0' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText(
        'must be positive with at most three decimal places',
      ),
    ).toBeInTheDocument();
    expect(updates).toHaveLength(1);

    // A field error from the server is shown at the input, not discarded.
    fireEvent.change(screen.getByLabelText('Package size'), {
      target: { value: '2' },
    });
    failNextSave = true;
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(
        screen.getByText('must be positive with at most three decimal places'),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('heading', { name: 'Could not save product' }),
    ).toBeInTheDocument();
    expect(updates).toHaveLength(1);

    // The corrected value saves.
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText('Product details saved.'),
    ).toBeInTheDocument();
    expect(updates).toMatchObject([
      { canonicalName: 'Ayran 1 l', sizeValue: '1.5' },
      { canonicalName: 'Ayran 1 l', sizeValue: '2', sizeUnit: 'l' },
    ]);
  });

  it('keeps the price basis when saving other product details', async () => {
    const updates: Array<Record<string, unknown>> = [];
    const genericProduct: components['schemas']['Product'] = {
      ...mockProduct,
      canonicalName: 'Ayran',
      sizeValue: null,
      sizeUnit: null,
      priceBasis: 'kg',
    };

    server.use(
      http.get('/api/v1/products/:productId', () =>
        HttpResponse.json(genericProduct),
      ),
      http.put('/api/v1/products/:productId', async ({ request }) => {
        updates.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json(genericProduct);
      }),
    );

    renderProductDetail();

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Ayran' }),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Canonical name'), {
      target: { value: 'Ayran Klasik' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText('Product details saved.'),
    ).toBeInTheDocument();
    expect(updates).toMatchObject([
      { canonicalName: 'Ayran Klasik', priceBasis: 'kg' },
    ]);
  });

  it('supports merge picker with confirmation summary, server error recovery, and merge commit', async () => {
    let failMergeOnce = true;
    server.use(
      http.post('/api/v1/products/:productId/merge', async ({ request }) => {
        if (failMergeOnce) {
          failMergeOnce = false;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Conflict',
              status: 409,
              detail: 'Merge conflict from concurrent update',
            },
            { status: 409 },
          );
        }
        const body =
          (await request.json()) as components['schemas']['MergeProductsRequest'];
        expect(body.sourceProductId).toBe(
          '0193a1b2-0000-7000-8000-000000000403',
        );
        return HttpResponse.json({
          ...mockProduct,
          aliases: [
            ...mockProduct.aliases,
            {
              id: '0193a1b2-0000-7000-8000-000000000503',
              rawName: 'EKMEK 350G',
              normalizedName: 'ekmek 350 g',
              store: {
                id: '0193a1b2-0000-7000-8000-000000000010',
                name: 'Example Market',
              },
              seenCount: 2,
              lastSeenAt: '2026-11-01T14:20:00Z',
            },
          ],
        });
      }),
    );

    renderProductDetail();
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Ayran 1 l' }),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: 'Merge another product…' }),
    );
    const dialog = await screen.findByRole('dialog');

    await waitFor(() =>
      expect(
        within(dialog).getByRole('option', { name: 'Ekmek 350 g' }),
      ).toBeInTheDocument(),
    );

    fireEvent.change(within(dialog).getByLabelText('Product to merge'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000403' },
    });

    await waitFor(() =>
      expect(
        within(dialog).getByText('Summary of items moving from Ekmek 350 g'),
      ).toBeInTheDocument(),
    );
    expect(
      within(dialog).getByText(/1 alias \(EKMEK 350G\)/),
    ).toBeInTheDocument();
    expect(within(dialog).getByText('0 product codes')).toBeInTheDocument();

    // First attempt surfaces server error inside dialog.
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm merge' }),
    );
    await waitFor(() =>
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        'Merge conflict from concurrent update',
      ),
    );
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    // Retry merge succeeds and closes dialog.
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm merge' }),
    );
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    );
    expect(await screen.findByRole('status')).toHaveTextContent(
      'Merged Ekmek 350 g into Ayran 1 l.',
    );
  });

  it('warns about mixed sizes after a merge', async () => {
    server.use(
      http.post('/api/v1/products/:productId/merge', () =>
        HttpResponse.json({
          ...mockProduct,
          aliases: [
            ...mockProduct.aliases,
            {
              id: '0193a1b2-0000-7000-8000-000000000503',
              rawName: 'PEYNIR 1KG',
              normalizedName: 'peynir 1 kg',
              store: {
                id: '0193a1b2-0000-7000-8000-000000000010',
                name: 'Example Market',
              },
              seenCount: 1,
              lastSeenAt: '2026-11-01T14:20:00Z',
            },
          ],
          mixedSizes: { basis: 'kg', sizes: ['500 g', '1 kg'] },
        }),
      ),
    );

    renderProductDetail();
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Ayran 1 l' }),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: 'Merge another product…' }),
    );
    const dialog = await screen.findByRole('dialog');

    await waitFor(() =>
      expect(
        within(dialog).getByRole('option', { name: 'Ekmek 350 g' }),
      ).toBeInTheDocument(),
    );

    fireEvent.change(within(dialog).getByLabelText('Product to merge'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000403' },
    });

    await waitFor(() =>
      expect(
        within(dialog).getByText('Summary of items moving from Ekmek 350 g'),
      ).toBeInTheDocument(),
    );

    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm merge' }),
    );

    const warning = await screen.findByRole('alert');
    expect(warning).toHaveTextContent('500 g');
    expect(warning).toHaveTextContent('1 kg');
    expect(warning).toHaveTextContent(/price basis kg/i);
    expect(warning).toHaveTextContent(/per kilogram/i);
  });

  it('supports splitting selected aliases with confirmation summary and server error handling', async () => {
    let failSplitOnce = true;
    server.use(
      http.post('/api/v1/products/:productId/split', async ({ request }) => {
        if (failSplitOnce) {
          failSplitOnce = false;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Bad Request',
              status: 400,
              detail: 'Cannot leave product with conflicting alias',
            },
            { status: 400 },
          );
        }
        const body =
          (await request.json()) as components['schemas']['SplitProductRequest'];
        expect(body).toEqual({
          aliasIds: ['0193a1b2-0000-7000-8000-000000000502'],
          canonicalName: 'Ayran Corner 1 l',
        });
        return HttpResponse.json(
          {
            ...mockProduct,
            id: '0193a1b2-0000-7000-8000-000000000499',
            canonicalName: body.canonicalName,
            aliases: [mockProduct.aliases[1]],
            codes: [],
            purchaseCount: 1,
          },
          { status: 201 },
        );
      }),
    );

    renderProductDetail();
    expect(await screen.findByLabelText('AYRN 1 L')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('AYRN 1 L'));
    fireEvent.change(screen.getByLabelText('New product name'), {
      target: { value: 'Ayran Corner 1 l' },
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Split selected aliases…' }),
    );

    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText('New product: Ayran Corner 1 l'),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/AYRN 1 L \(Corner Shop · Seen 1\)/),
    ).toBeInTheDocument();

    // First attempt surfaces server error inside confirmation dialog.
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm split' }),
    );
    await waitFor(() =>
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        'Cannot leave product with conflicting alias',
      ),
    );

    // Retry succeeds and shows link to the created product.
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm split' }),
    );
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    );

    const status = await screen.findByRole('status');
    expect(status).toHaveTextContent('Moved 1 alias to new product');
    expect(
      within(status).getByRole('link', { name: 'Ayran Corner 1 l' }),
    ).toHaveAttribute('href', '/products/0193a1b2-0000-7000-8000-000000000499');
  });

  it('charts price history per currency and store using token strokes and receipt links', async () => {
    const capturedQueries: URLSearchParams[] = [];

    const multiCurrencyHistory: components['schemas']['PriceHistory'] = {
      productId: mockProduct.id,
      points: [
        {
          receiptId: '0193a1b2-0000-7000-8000-000000000098',
          lineItemId: '0193a1b2-0000-7000-8000-000000000291',
          purchasedOn: '2026-10-04',
          store: {
            id: '0193a1b2-0000-7000-8000-000000000010',
            name: 'Example Market',
          },
          unitPrice: '39.90',
          quantity: '1',
          quantityUnit: 'ad',
          currency: 'TRY',
          basis: 'item',
          basisPrice: null,
        },
        {
          receiptId: '0193a1b2-0000-7000-8000-000000000099',
          lineItemId: '0193a1b2-0000-7000-8000-000000000295',
          purchasedOn: '2026-10-18',
          store: {
            id: '0193a1b2-0000-7000-8000-000000000011',
            name: 'Corner Shop',
          },
          unitPrice: '44.00',
          quantity: '1',
          quantityUnit: 'ad',
          currency: 'TRY',
          basis: 'item',
          basisPrice: null,
        },
        {
          receiptId: '0193a1b2-0000-7000-8000-000000000100',
          lineItemId: '0193a1b2-0000-7000-8000-000000000201',
          purchasedOn: '2026-11-02',
          store: {
            id: '0193a1b2-0000-7000-8000-000000000010',
            name: 'Example Market',
          },
          unitPrice: '42.25',
          quantity: '2',
          quantityUnit: 'ad',
          currency: 'TRY',
          basis: 'item',
          basisPrice: null,
        },
        {
          receiptId: '0193a1b2-0000-7000-8000-000000000105',
          lineItemId: '0193a1b2-0000-7000-8000-000000000210',
          purchasedOn: '2026-11-05',
          store: {
            id: '0193a1b2-0000-7000-8000-000000000012',
            name: 'Airport Deli',
          },
          unitPrice: '1.95',
          quantity: '1',
          quantityUnit: 'ad',
          currency: 'EUR',
          basis: 'item',
          basisPrice: null,
        },
      ],
      stores: [
        {
          store: {
            id: '0193a1b2-0000-7000-8000-000000000010',
            name: 'Example Market',
          },
          currency: 'TRY',
          min: '39.90',
          max: '42.25',
          latest: '42.25',
          latestOn: '2026-11-02',
          purchaseCount: 2,
        },
        {
          store: {
            id: '0193a1b2-0000-7000-8000-000000000011',
            name: 'Corner Shop',
          },
          currency: 'TRY',
          min: '44.00',
          max: '44.00',
          latest: '44.00',
          latestOn: '2026-10-18',
          purchaseCount: 1,
        },
        {
          store: {
            id: '0193a1b2-0000-7000-8000-000000000012',
            name: 'Airport Deli',
          },
          currency: 'EUR',
          min: '1.95',
          max: '1.95',
          latest: '1.95',
          latestOn: '2026-11-05',
          purchaseCount: 1,
        },
      ],
    };

    server.use(
      http.get('/api/v1/products/:productId/price-history', ({ request }) => {
        const url = new URL(request.url);
        capturedQueries.push(url.searchParams);
        const storeFilter = url.searchParams.get('storeId');
        if (storeFilter) {
          return HttpResponse.json({
            productId: mockProduct.id,
            points: multiCurrencyHistory.points.filter(
              (point) => point.store?.id === storeFilter,
            ),
            stores: multiCurrencyHistory.stores.filter(
              (store) => store.store?.id === storeFilter,
            ),
          });
        }
        return HttpResponse.json(multiCurrencyHistory);
      }),
    );

    const { container } = renderProductDetail();

    expect(
      await screen.findByRole('heading', { name: 'TRY unit prices' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'EUR unit prices' }),
    ).toBeInTheDocument();

    // Receipt links from price history points table.
    expect(screen.getByRole('link', { name: 'Nov 2, 2026' })).toHaveAttribute(
      'href',
      '/receipts/0193a1b2-0000-7000-8000-000000000100',
    );
    expect(screen.getByRole('link', { name: 'Nov 5, 2026' })).toHaveAttribute(
      'href',
      '/receipts/0193a1b2-0000-7000-8000-000000000105',
    );

    // Separate Recharts charts render with CSS token strokes and zero raw hex colours.
    await waitFor(() => {
      const curves = container.querySelectorAll('.recharts-line-curve');
      expect(curves.length).toBeGreaterThanOrEqual(2);
      for (const curve of curves) {
        const stroke = curve.getAttribute('stroke') ?? '';
        expect(stroke).toMatch(/^var\(--color-/);
        expect(curve.getAttribute('d')).toBeTruthy();
      }
    });

    // Filtering by store updates API query parameters.
    await waitFor(() =>
      expect(
        screen.getByRole('option', { name: 'Corner Shop' }),
      ).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText('Store'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000011' },
    });

    await waitFor(() => {
      expect(
        screen.queryByRole('heading', { name: 'EUR unit prices' }),
      ).not.toBeInTheDocument();
      expect(
        capturedQueries.some(
          (params) =>
            params.get('storeId') === '0193a1b2-0000-7000-8000-000000000011',
        ),
      ).toBe(true);
    });
  });
});
