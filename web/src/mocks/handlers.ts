import { http, HttpResponse } from 'msw';
import type { components } from '../api/schema.gen';
import {
  getMockAuthMode,
  mockCategory,
  mockCategoryPage,
  mockImageBytes,
  mockMonthBreakdown,
  mockMonthlySpending,
  mockPriceHistory,
  mockProblemForbidden,
  mockProblemNotFound,
  mockProblemUnauthorized,
  mockProduct,
  mockProductPage,
  mockReceipt,
  mockReceiptPage,
  mockReviewFlagPage,
  mockSettings,
  mockStorageStatus,
  mockStore,
  mockStorePage,
  mockUploadResponse,
  mockUsage,
  mockUser,
  setMockAuthMode,
} from './data';

function problemJson(
  status: number,
  problem: components['schemas']['Problem'],
) {
  return HttpResponse.json(problem, {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });
}

function guardSignedIn() {
  const mode = getMockAuthMode();
  if (mode === 'signed_out') {
    return problemJson(401, mockProblemUnauthorized);
  }
  if (mode === 'access_closed') {
    return problemJson(403, mockProblemForbidden);
  }
  return null;
}

/** MSW handlers covering every OpenAPI operation using specification examples. */
export const handlers = [
  http.get('/auth/google/login', () => {
    setMockAuthMode('signed_in');
    return HttpResponse.redirect('/receipts', 302);
  }),

  http.get('/api/v1/me', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockUser);
  }),

  http.post('/api/v1/auth/logout', () => {
    setMockAuthMode('signed_out');
    return new HttpResponse(null, { status: 204 });
  }),

  http.post('/api/v1/receipts', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockUploadResponse, { status: 207 });
  }),

  http.get('/api/v1/receipts', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockReceiptPage);
  }),

  http.get('/api/v1/receipts/:receiptId', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    if (params.receiptId === 'missing') {
      return problemJson(404, mockProblemNotFound);
    }
    return HttpResponse.json({
      ...mockReceipt,
      id: String(params.receiptId),
    });
  }),

  http.put('/api/v1/receipts/:receiptId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['ReceiptUpdate'];
    return HttpResponse.json({
      ...mockReceipt,
      id: String(params.receiptId),
      title: body.title,
      currency: body.currency,
      purchasedOn: body.purchasedOn ?? mockReceipt.purchasedOn,
      purchasedTime: body.purchasedTime ?? mockReceipt.purchasedTime,
      subtotal: body.subtotal ?? mockReceipt.subtotal,
      taxTotal: body.taxTotal ?? mockReceipt.taxTotal,
      total: body.total ?? mockReceipt.total,
    });
  }),

  http.delete('/api/v1/receipts/:receiptId', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return new HttpResponse(null, { status: 204 });
  }),

  http.post('/api/v1/receipts/:receiptId/reprocess', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(
      {
        ...mockReceipt,
        id: String(params.receiptId),
        status: 'queued',
      },
      { status: 202 },
    );
  }),

  http.get('/api/v1/receipts/:receiptId/images/:imageId', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.arrayBuffer(mockImageBytes.buffer, {
      status: 200,
      headers: {
        'Content-Type': 'image/jpeg',
        'Cache-Control': 'private, max-age=3600',
      },
    });
  }),

  http.post(
    '/api/v1/receipts/:receiptId/line-items',
    async ({ request, params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      const body =
        (await request.json()) as components['schemas']['LineItemInput'];
      const added: components['schemas']['LineItem'] = {
        id: '0193a1b2-0000-7000-8000-000000000299',
        position: mockReceipt.lineItems.length,
        rawName: body.name,
        name: body.name,
        quantity: body.quantity ?? null,
        quantityUnit: body.quantityUnit ?? null,
        unitPrice: body.unitPrice ?? null,
        lineTotal: body.lineTotal,
        productCode: body.productCode ?? null,
        confidence: null,
        product: null,
        matchStatus: 'unmatched',
        matchScore: null,
        matchMethod: null,
      };
      return HttpResponse.json(
        {
          ...mockReceipt,
          id: String(params.receiptId),
          lineItems: [...mockReceipt.lineItems, added],
        },
        { status: 201 },
      );
    },
  ),

  http.put(
    '/api/v1/receipts/:receiptId/line-items/:lineItemId',
    async ({ request, params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      const body =
        (await request.json()) as components['schemas']['LineItemInput'];
      return HttpResponse.json({
        ...mockReceipt,
        id: String(params.receiptId),
        lineItems: mockReceipt.lineItems.map((item) =>
          item.id === String(params.lineItemId)
            ? {
                ...item,
                name: body.name,
                quantity: body.quantity ?? null,
                quantityUnit: body.quantityUnit ?? null,
                unitPrice: body.unitPrice ?? null,
                lineTotal: body.lineTotal,
                productCode: body.productCode ?? null,
              }
            : item,
        ),
      });
    },
  ),

  http.delete(
    '/api/v1/receipts/:receiptId/line-items/:lineItemId',
    ({ params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      return HttpResponse.json({
        ...mockReceipt,
        id: String(params.receiptId),
        lineItems: mockReceipt.lineItems.filter(
          (item) => item.id !== String(params.lineItemId),
        ),
      });
    },
  ),

  http.get('/api/v1/review-flags', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockReviewFlagPage);
  }),

  http.post('/api/v1/review-flags/:flagId/resolve', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json({
      ...mockReceipt,
      openFlagCount: 1,
    });
  }),

  http.get('/api/v1/products', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockProductPage);
  }),

  http.get('/api/v1/products/:productId', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json({
      ...mockProduct,
      id: String(params.productId),
    });
  }),

  http.put('/api/v1/products/:productId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['ProductUpdate'];
    return HttpResponse.json({
      ...mockProduct,
      id: String(params.productId),
      canonicalName: body.canonicalName,
      sizeValue: body.sizeValue ?? null,
      sizeUnit: body.sizeUnit ?? null,
    });
  }),

  http.post('/api/v1/products/:productId/merge', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json({
      ...mockProduct,
      id: String(params.productId),
    });
  }),

  http.post('/api/v1/products/:productId/split', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['SplitProductRequest'];
    return HttpResponse.json(
      {
        ...mockProduct,
        id: '0193a1b2-0000-7000-8000-000000000499',
        canonicalName: body.canonicalName,
      },
      { status: 201 },
    );
  }),

  http.get('/api/v1/products/:productId/price-history', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json({
      ...mockPriceHistory,
      productId: String(params.productId),
    });
  }),

  http.get('/api/v1/stores', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockStorePage);
  }),

  http.put('/api/v1/stores/:storeId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body = (await request.json()) as components['schemas']['StoreUpdate'];
    return HttpResponse.json({
      ...mockStore,
      id: String(params.storeId),
      name: body.name,
    });
  }),

  http.get('/api/v1/categories', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockCategoryPage);
  }),

  http.post('/api/v1/categories', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['CategoryInput'];
    return HttpResponse.json(
      {
        ...mockCategory,
        id: '0193a1b2-0000-7000-8000-000000000029',
        name: body.name,
        productCount: 0,
      },
      { status: 201 },
    );
  }),

  http.put('/api/v1/categories/:categoryId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['CategoryInput'];
    return HttpResponse.json({
      ...mockCategory,
      id: String(params.categoryId),
      name: body.name,
    });
  }),

  http.delete('/api/v1/categories/:categoryId', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return new HttpResponse(null, { status: 204 });
  }),

  http.get('/api/v1/reports/monthly-spending', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockMonthlySpending);
  }),

  http.get('/api/v1/reports/month-breakdown', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    const month = url.searchParams.get('month') ?? mockMonthBreakdown.month;
    const groupBy =
      (url.searchParams.get('groupBy') as 'category' | 'store' | null) ??
      mockMonthBreakdown.groupBy;
    return HttpResponse.json({
      ...mockMonthBreakdown,
      month,
      groupBy,
    });
  }),

  http.get('/api/v1/settings', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockSettings);
  }),

  http.put('/api/v1/settings', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body = (await request.json()) as components['schemas']['Settings'];
    return HttpResponse.json(body);
  }),

  http.get('/api/v1/usage', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockUsage);
  }),

  http.get('/api/v1/storage', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockStorageStatus);
  }),

  http.delete('/api/v1/storage/connection', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return new HttpResponse(null, { status: 204 });
  }),
];
