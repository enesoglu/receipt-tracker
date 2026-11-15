import { http, HttpResponse } from 'msw';
import type { components } from '../api/schema.gen';
import {
  getMockAuthMode,
  getMockProductById,
  getMockProductPriceHistory,
  getMockReceiptById,
  mergeMockProducts,
  mockCategory,
  mockCategoryPage,
  mockImageBytes,
  mockMonthBreakdown,
  mockMonthlySpending,
  mockProblemForbidden,
  mockProblemNotFound,
  mockProblemUnauthorized,
  mockReceipt,
  mockSettings,
  mockStorageStatus,
  mockStore,
  mockStorePage,
  mockUploadResponse,
  mockUsage,
  mockUser,
  queryMockProducts,
  queryMockReceipts,
  queryMockReviewFlags,
  registerMockUploads,
  resolveMockReviewFlag,
  setMockAuthMode,
  splitMockProduct,
  updateMockProduct,
} from './data';
import { readUploadedFiles } from './multipart';

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

  http.post('/api/v1/receipts', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const files = await readUploadedFiles(request);
    if (files.length === 0) {
      return HttpResponse.json(mockUploadResponse, { status: 200 });
    }
    return HttpResponse.json(registerMockUploads(files), { status: 200 });
  }),

  http.get('/api/v1/receipts', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockReceipts(url.searchParams));
  }),

  http.get('/api/v1/receipts/:receiptId', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const receipt = getMockReceiptById(String(params.receiptId));
    if (!receipt) {
      return problemJson(404, mockProblemNotFound);
    }
    return HttpResponse.json(receipt);
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
      { status: 200 },
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
    return HttpResponse.json(queryMockReviewFlags());
  }),

  http.post('/api/v1/review-flags/:flagId/resolve', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(resolveMockReviewFlag(String(params.flagId)));
  }),

  http.get('/api/v1/products', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockProducts(url.searchParams));
  }),

  http.get('/api/v1/products/:productId', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const product = getMockProductById(String(params.productId));
    if (!product) {
      return problemJson(404, mockProblemNotFound);
    }
    return HttpResponse.json(product);
  }),

  http.put('/api/v1/products/:productId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['ProductUpdate'];
    const updated = updateMockProduct(String(params.productId), body);
    if (!updated) {
      return problemJson(404, mockProblemNotFound);
    }
    return HttpResponse.json(updated);
  }),

  http.post(
    '/api/v1/products/:productId/merge',
    async ({ request, params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      const body =
        (await request.json()) as components['schemas']['MergeProductsRequest'];
      const result = mergeMockProducts(String(params.productId), body);
      if (!result.ok) {
        if (result.reason === 'self_merge') {
          return problemJson(400, {
            type: 'about:blank',
            title: 'Bad Request',
            status: 400,
            detail: 'Cannot merge a product into itself',
          });
        }
        return problemJson(404, mockProblemNotFound);
      }
      return HttpResponse.json(result.product);
    },
  ),

  http.post(
    '/api/v1/products/:productId/split',
    async ({ request, params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      const body =
        (await request.json()) as components['schemas']['SplitProductRequest'];
      const result = splitMockProduct(String(params.productId), body);
      if (!result.ok) {
        if (result.reason === 'invalid') {
          return problemJson(400, {
            type: 'about:blank',
            title: 'Bad Request',
            status: 400,
            detail: 'Select at least one alias and provide a product name',
          });
        }
        return problemJson(404, mockProblemNotFound);
      }
      return HttpResponse.json(result.product, { status: 201 });
    },
  ),

  http.get(
    '/api/v1/products/:productId/price-history',
    ({ request, params }) => {
      const blocked = guardSignedIn();
      if (blocked) return blocked;
      const url = new URL(request.url);
      const history = getMockProductPriceHistory(
        String(params.productId),
        url.searchParams,
      );
      if (!history) {
        return problemJson(404, mockProblemNotFound);
      }
      return HttpResponse.json(history);
    },
  ),

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
