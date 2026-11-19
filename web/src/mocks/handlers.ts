import { http, HttpResponse } from 'msw';
import type { components } from '../api/schema.gen';
import {
  createMockCategory,
  deleteMockCategory,
  disconnectMockStorage,
  getMockAuthMode,
  getMockProductById,
  getMockProductPriceHistory,
  getMockReceiptById,
  getMockSettings,
  getMockStorageStatus,
  mergeMockProducts,
  mockImageBytes,
  mockProblemForbidden,
  mockProblemNotFound,
  mockProblemUnauthorized,
  mockReceipt,
  mockStore,
  mockStorePage,
  mockUploadResponse,
  mockUsage,
  mockUser,
  queryMockCategories,
  queryMockMonthBreakdown,
  queryMockMonthlySpending,
  queryMockPipeline,
  queryMockProducts,
  queryMockReceipts,
  queryMockReviewFlags,
  registerMockUploads,
  resolveMockReviewFlag,
  setMockAuthMode,
  splitMockProduct,
  updateMockCategory,
  updateMockProduct,
  updateMockSettings,
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

  http.get('/api/v1/pipeline', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockPipeline(url.searchParams));
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
      // The API replaces every identity field, so an explicit null clears it.
      invoiceNumber: body.invoiceNumber ?? null,
      ettn: body.ettn ?? null,
      sellerTaxNumber: body.sellerTaxNumber ?? null,
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

  http.get('/api/v1/categories', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockCategories(url.searchParams));
  }),

  http.post('/api/v1/categories', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['CategoryInput'];
    const result = createMockCategory(body);
    if (!result.ok) {
      if (result.reason === 'conflict') {
        return problemJson(409, {
          type: 'about:blank',
          title: 'Conflict',
          status: 409,
          detail: 'a category with this name already exists',
          requestId: mockProblemNotFound.requestId,
        });
      }
      return problemJson(400, {
        type: 'about:blank',
        title: 'Bad Request',
        status: 400,
        detail: 'category name is invalid',
        requestId: mockProblemNotFound.requestId,
        errors: [
          {
            field: 'name',
            message: 'must be between 1 and 100 characters',
          },
        ],
      });
    }
    return HttpResponse.json(result.category, { status: 201 });
  }),

  http.put('/api/v1/categories/:categoryId', async ({ request, params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body =
      (await request.json()) as components['schemas']['CategoryInput'];
    const result = updateMockCategory(String(params.categoryId), body);
    if (!result.ok) {
      if (result.reason === 'not_found') {
        return problemJson(404, {
          ...mockProblemNotFound,
          detail: 'category not found',
        });
      }
      if (result.reason === 'conflict') {
        return problemJson(409, {
          type: 'about:blank',
          title: 'Conflict',
          status: 409,
          detail: 'a category with this name already exists',
          requestId: mockProblemNotFound.requestId,
        });
      }
      return problemJson(400, {
        type: 'about:blank',
        title: 'Bad Request',
        status: 400,
        detail: 'category name is invalid',
        requestId: mockProblemNotFound.requestId,
        errors: [
          {
            field: 'name',
            message: 'must be between 1 and 100 characters',
          },
        ],
      });
    }
    return HttpResponse.json(result.category);
  }),

  http.delete('/api/v1/categories/:categoryId', ({ params }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    if (!deleteMockCategory(String(params.categoryId))) {
      return problemJson(404, {
        ...mockProblemNotFound,
        detail: 'category not found',
      });
    }
    return new HttpResponse(null, { status: 204 });
  }),

  http.get('/api/v1/reports/monthly-spending', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockMonthlySpending(url.searchParams));
  }),

  http.get('/api/v1/reports/month-breakdown', ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const url = new URL(request.url);
    return HttpResponse.json(queryMockMonthBreakdown(url.searchParams));
  }),

  http.get('/api/v1/settings', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(getMockSettings());
  }),

  http.put('/api/v1/settings', async ({ request }) => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    const body = (await request.json()) as components['schemas']['Settings'];
    const result = updateMockSettings(body);
    if (!result.ok) {
      return problemJson(400, {
        type: 'about:blank',
        title: 'Bad Request',
        status: 400,
        detail: 'settings are invalid',
        requestId: mockProblemNotFound.requestId,
        errors: [{ field: result.field, message: result.message }],
      });
    }
    return HttpResponse.json(result.settings);
  }),

  http.get('/api/v1/usage', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(mockUsage);
  }),

  http.get('/api/v1/storage', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    return HttpResponse.json(getMockStorageStatus());
  }),

  http.delete('/api/v1/storage/connection', () => {
    const blocked = guardSignedIn();
    if (blocked) return blocked;
    if (!disconnectMockStorage()) {
      return problemJson(404, {
        ...mockProblemNotFound,
        detail: 'storage connection not found',
      });
    }
    return new HttpResponse(null, { status: 204 });
  }),
];
