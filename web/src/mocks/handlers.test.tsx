import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { ApiError, api, unwrap } from '../api/client';
import { createAppQueryClient } from '../api/query-client';
import { AppRouterContent } from '../app/App';
import { resetMockAuth, setMockAuthMode } from './data';
import { server } from './server';

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

describe('MSW API handlers', () => {
  it('handles every OpenAPI operation through the generated client', async () => {
    const me = await unwrap(api.GET('/me'));
    expect(me.email).toBe('ada@example.com');

    const upload = await unwrap(
      api.POST('/receipts', {
        body: { files: [] },
        bodySerializer: () => new FormData(),
      }),
    );
    expect(upload.results).toHaveLength(3);

    const receiptPage = await unwrap(api.GET('/receipts'));
    expect(receiptPage.items).toHaveLength(2);

    const receipt = await unwrap(
      api.GET('/receipts/{receiptId}', {
        params: { path: { receiptId: '0193a1b2-0000-7000-8000-000000000100' } },
      }),
    );
    expect(receipt.lineItems).toHaveLength(4);

    await expect(
      unwrap(
        api.GET('/receipts/{receiptId}', {
          params: { path: { receiptId: 'missing' } },
        }),
      ),
    ).rejects.toBeInstanceOf(ApiError);

    const updatedReceipt = await unwrap(
      api.PUT('/receipts/{receiptId}', {
        params: { path: { receiptId: receipt.id } },
        body: { title: 'Updated trip', currency: 'TRY' },
      }),
    );
    expect(updatedReceipt.title).toBe('Updated trip');

    const reprocessed = await unwrap(
      api.POST('/receipts/{receiptId}/reprocess', {
        params: { path: { receiptId: receipt.id } },
      }),
    );
    expect(reprocessed.status).toBe('queued');

    const imageRes = await api.GET('/receipts/{receiptId}/images/{imageId}', {
      params: {
        path: {
          receiptId: receipt.id,
          imageId: '0193a1b2-0000-7000-8000-000000000110',
        },
      },
      parseAs: 'arrayBuffer',
    });
    expect(imageRes.response.status).toBe(200);
    expect(imageRes.data).toBeInstanceOf(ArrayBuffer);

    const withLine = await unwrap(
      api.POST('/receipts/{receiptId}/line-items', {
        params: { path: { receiptId: receipt.id } },
        body: { name: 'Yogurt', lineTotal: '40.00' },
      }),
    );
    expect(withLine.lineItems).toHaveLength(5);

    const editedLine = await unwrap(
      api.PUT('/receipts/{receiptId}/line-items/{lineItemId}', {
        params: {
          path: {
            receiptId: receipt.id,
            lineItemId: receipt.lineItems[0].id,
          },
        },
        body: { name: 'Ayran 1 l', lineTotal: '84.50' },
      }),
    );
    expect(editedLine.lineItems[0].name).toBe('Ayran 1 l');

    const removedLine = await unwrap(
      api.DELETE('/receipts/{receiptId}/line-items/{lineItemId}', {
        params: {
          path: {
            receiptId: receipt.id,
            lineItemId: receipt.lineItems[0].id,
          },
        },
      }),
    );
    expect(removedLine.lineItems).toHaveLength(3);

    await unwrap(
      api.DELETE('/receipts/{receiptId}', {
        params: { path: { receiptId: receipt.id } },
      }),
    );

    const flags = await unwrap(api.GET('/review-flags'));
    expect(flags.totalOpen).toBe(2);

    const resolved = await unwrap(
      api.POST('/review-flags/{flagId}/resolve', {
        params: { path: { flagId: flags.items[0].flag.id } },
        body: { action: 'accept' },
      }),
    );
    expect(resolved.openFlagCount).toBe(1);

    const products = await unwrap(api.GET('/products'));
    expect(products.items).toHaveLength(2);

    const product = await unwrap(
      api.GET('/products/{productId}', {
        params: { path: { productId: products.items[0].id } },
      }),
    );
    expect(product.aliases).toHaveLength(2);

    const updatedProduct = await unwrap(
      api.PUT('/products/{productId}', {
        params: { path: { productId: product.id } },
        body: { canonicalName: 'Ayran 1 l' },
      }),
    );
    expect(updatedProduct.canonicalName).toBe('Ayran 1 l');

    const merged = await unwrap(
      api.POST('/products/{productId}/merge', {
        params: { path: { productId: product.id } },
        body: { sourceProductId: '0193a1b2-0000-7000-8000-000000000405' },
      }),
    );
    expect(merged.id).toBe(product.id);

    const split = await unwrap(
      api.POST('/products/{productId}/split', {
        params: { path: { productId: product.id } },
        body: {
          aliasIds: [product.aliases[1].id],
          canonicalName: 'Ayran 2 l',
        },
      }),
    );
    expect(split.canonicalName).toBe('Ayran 2 l');

    const history = await unwrap(
      api.GET('/products/{productId}/price-history', {
        params: { path: { productId: product.id } },
      }),
    );
    expect(history.points).toHaveLength(3);

    const stores = await unwrap(api.GET('/stores'));
    expect(stores.items).toHaveLength(2);

    const updatedStore = await unwrap(
      api.PUT('/stores/{storeId}', {
        params: { path: { storeId: stores.items[0].id } },
        body: { name: 'Corner Market' },
      }),
    );
    expect(updatedStore.name).toBe('Corner Market');

    const categories = await unwrap(api.GET('/categories'));
    expect(categories.items).toHaveLength(2);

    const createdCategory = await unwrap(
      api.POST('/categories', { body: { name: 'Beverages' } }),
    );
    expect(createdCategory.name).toBe('Beverages');

    const updatedCategory = await unwrap(
      api.PUT('/categories/{categoryId}', {
        params: { path: { categoryId: categories.items[0].id } },
        body: { name: 'Bread & Bakery' },
      }),
    );
    expect(updatedCategory.name).toBe('Bread & Bakery');

    await unwrap(
      api.DELETE('/categories/{categoryId}', {
        params: { path: { categoryId: categories.items[0].id } },
      }),
    );

    const spending = await unwrap(
      api.GET('/reports/monthly-spending', {
        params: { query: { from: '2026-10', to: '2026-11' } },
      }),
    );
    expect(spending.months).toHaveLength(2);

    const breakdown = await unwrap(
      api.GET('/reports/month-breakdown', {
        params: { query: { month: '2026-11', groupBy: 'store' } },
      }),
    );
    expect(breakdown.groupBy).toBe('store');

    const settings = await unwrap(api.GET('/settings'));
    expect(settings.defaultCurrency).toBe('TRY');

    const updatedSettings = await unwrap(
      api.PUT('/settings', {
        body: { defaultCurrency: 'EUR', defaultReceiptTitle: 'Groceries' },
      }),
    );
    expect(updatedSettings.defaultCurrency).toBe('EUR');

    const usage = await unwrap(api.GET('/usage'));
    expect(usage.pagesLimit).toBe(500);

    const storage = await unwrap(api.GET('/storage'));
    expect(storage.drive).toBe('ok');

    await unwrap(api.DELETE('/storage/connection'));

    await unwrap(api.POST('/auth/logout'));
    await expect(unwrap(api.GET('/me'))).rejects.toMatchObject({ status: 401 });
  });

  it('renders SignInPage when signed out and access is closed', async () => {
    setMockAuthMode('signed_out');
    const client = createAppQueryClient({ staleTime: 0 });
    const { unmount } = render(
      <MemoryRouter initialEntries={['/receipts']}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Sign in with Google' }),
      ).toHaveAttribute('href', '/auth/google/login'),
    );
    unmount();

    setMockAuthMode('access_closed');
    const closedClient = createAppQueryClient({ staleTime: 0 });
    render(
      <MemoryRouter initialEntries={['/receipts']}>
        <AppRouterContent queryClient={closedClient} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('Access is closed'),
    );
  });

  it('renders the shell, review badge, and signs out cleanly against MSW', async () => {
    setMockAuthMode('signed_in');
    const client = createAppQueryClient({ staleTime: 0 });
    render(
      <MemoryRouter initialEntries={['/receipts']}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Receipts' }),
      ).toBeInTheDocument(),
    );
    await waitFor(() =>
      expect(screen.getByLabelText('Open review items')).toHaveTextContent('2'),
    );

    fireEvent.click(screen.getByRole('link', { name: 'Settings' }));
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Settings' }),
      ).toBeInTheDocument(),
    );

    fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: 'Sign in to your account' }),
      ).toBeInTheDocument(),
    );
  });
});
