import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { components } from '../../api/schema.gen';
import {
  mockReceipt,
  mockReceiptPage,
  resetMockAuth,
  resetMockState,
} from '../../mocks/data';
import { server } from '../../mocks/server';
import { ReviewQueuePage } from './ReviewQueuePage';

type ReviewFlagEntry = components['schemas']['ReviewFlagEntry'];

const BASE_RECEIPT_SUMMARY = mockReceiptPage.items[1];
const BASE_LINE_ITEM = mockReceipt.lineItems[1];

function makeEntry(
  id: string,
  flag: components['schemas']['ReviewFlag'],
  lineItem: components['schemas']['LineItem'] | null = BASE_LINE_ITEM,
): ReviewFlagEntry {
  return {
    flag: { ...flag, id },
    receipt: BASE_RECEIPT_SUMMARY,
    lineItem,
  };
}

function renderReviewPage() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <MemoryRouter initialEntries={['/review']}>
      <QueryProvider client={client}>
        <Routes>
          <Route path="/review" element={<ReviewQueuePage />} />
          <Route path="/receipts" element={<h1>Receipts page target</h1>} />
          <Route
            path="/receipts/:receiptId"
            element={<h1>Receipt detail target</h1>}
          />
        </Routes>
      </QueryProvider>
    </MemoryRouter>,
  );
}

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' });
});

afterEach(() => {
  server.resetHandlers();
  resetMockState();
  resetMockAuth();
});

afterAll(() => {
  server.close();
});

describe('ReviewQueuePage', () => {
  it('renders loading, error retry, and empty state linking to /receipts', async () => {
    let attempts = 0;
    let resolvePending!: (response: Response) => void;
    let markStarted!: () => void;
    const started = new Promise<void>((resolve) => {
      markStarted = resolve;
    });

    server.use(
      http.get('/api/v1/review-flags', () => {
        attempts += 1;
        if (attempts === 1) {
          markStarted();
          return new Promise<Response>((resolve) => {
            resolvePending = resolve;
          });
        }
        if (attempts === 2) {
          return HttpResponse.json(
            {
              type: 'https://receipt-tracker.example/problems/unavailable',
              title: 'Service unavailable',
              status: 503,
              detail: 'Queue service unavailable',
            },
            {
              status: 503,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          );
        }
        return HttpResponse.json({ items: [], nextCursor: null, totalOpen: 0 });
      }),
    );

    renderReviewPage();

    await started;
    expect(screen.getByText('Loading review queue...')).toBeInTheDocument();

    resolvePending(
      HttpResponse.json(
        {
          type: 'https://receipt-tracker.example/problems/unavailable',
          title: 'Service unavailable',
          status: 503,
          detail: 'Queue service unavailable',
        },
        {
          status: 503,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    );

    await waitFor(
      () =>
        expect(
          screen.getByText('Could not load review queue'),
        ).toBeInTheDocument(),
      { timeout: 3000 },
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() =>
      expect(screen.getByText('Review queue is clear')).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Browse receipts' }),
    ).toHaveAttribute('href', '/receipts');
  });

  it('resolves all seven flag kinds with tailored controls, live previews, and ambiguous_code copy', async () => {
    const receiptId = BASE_RECEIPT_SUMMARY.id;
    const queue: ReviewFlagEntry[] = [
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000901',
        {
          id: '0193a1b2-0000-7000-8000-000000000901',
          receiptId,
          lineItemId: null,
          kind: 'uncertain_match',
          detail: {
            reason:
              'ambiguous_code' as components['schemas']['UncertainMatchReason'],
            candidates: [
              {
                productId: '0193a1b2-0000-7000-8000-000000000403',
                canonicalName: 'Ekmek 350 g',
                sizeValue: '350',
                sizeUnit: 'g',
                score: 0.88,
              },
              {
                productId: '0193a1b2-0000-7000-8000-000000000404',
                canonicalName: 'Kepekli ekmek 400 g',
                sizeValue: '400',
                sizeUnit: 'g',
                score: 0.65,
              },
            ],
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        BASE_LINE_ITEM,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000902',
        {
          id: '0193a1b2-0000-7000-8000-000000000902',
          receiptId,
          lineItemId: null,
          kind: 'low_confidence',
          detail: { field: 'store', confidence: 0.61 },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        null,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000903',
        {
          id: '0193a1b2-0000-7000-8000-000000000903',
          receiptId,
          lineItemId: null,
          kind: 'missing_field',
          detail: { field: 'purchased_on' },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        null,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000904',
        {
          id: '0193a1b2-0000-7000-8000-000000000904',
          receiptId,
          lineItemId: BASE_LINE_ITEM.id,
          kind: 'line_math',
          detail: {
            quantity: '2',
            unitPrice: '15.00',
            lineTotal: '28.00',
            expectedTotal: '30.00',
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        BASE_LINE_ITEM,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000905',
        {
          id: '0193a1b2-0000-7000-8000-000000000905',
          receiptId,
          lineItemId: null,
          kind: 'sum_mismatch',
          detail: {
            lineSum: '125.00',
            total: '129.40',
            difference: '4.40',
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        null,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000906',
        {
          id: '0193a1b2-0000-7000-8000-000000000906',
          receiptId,
          lineItemId: BASE_LINE_ITEM.id,
          kind: 'code_conflict',
          detail: {
            code: '8690001234',
            codeProduct: {
              productId: '0193a1b2-0000-7000-8000-000000000408',
              canonicalName: 'Wholewheat bread 400 g',
              sizeValue: '400',
              sizeUnit: 'g',
              score: 0.72,
            },
            nameProduct: {
              productId: '0193a1b2-0000-7000-8000-000000000409',
              canonicalName: 'White loaf 350 g',
              sizeValue: '350',
              sizeUnit: 'g',
              score: 0.91,
            },
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        BASE_LINE_ITEM,
      ),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000907',
        {
          id: '0193a1b2-0000-7000-8000-000000000907',
          receiptId,
          lineItemId: null,
          kind: 'possible_duplicate',
          detail: {
            otherReceiptId: '0193a1b2-0000-7000-8000-000000000199',
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        null,
      ),
    ];

    let activeQueue = [...queue];
    const submittedPayloads: Array<{ flagId: string; body: unknown }> = [];

    server.use(
      http.get('/api/v1/review-flags', () =>
        HttpResponse.json({
          items: activeQueue,
          nextCursor: null,
          totalOpen: activeQueue.length,
        }),
      ),
      http.post(
        '/api/v1/review-flags/:flagId/resolve',
        async ({ request, params }) => {
          const body = await request.json();
          const flagId = String(params.flagId);
          submittedPayloads.push({ flagId, body });
          activeQueue = activeQueue.filter((item) => item.flag.id !== flagId);
          return HttpResponse.json({
            ...mockReceipt,
            openFlagCount: activeQueue.length,
          });
        },
      ),
    );

    renderReviewPage();

    // 1. uncertain_match (ambiguous_code) + live preview when switching to new product
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 7')).toBeInTheDocument(),
    );
    expect(
      screen.getByText(
        /product code is shared by more than one existing product/i,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(
      /Links "Ekmek" to existing product "Ekmek 350 g"/i,
    );

    fireEvent.click(screen.getByLabelText(/None of these/i));
    const nameInput = screen.getByLabelText(/New product name/i);
    fireEvent.change(nameInput, { target: { value: 'Village loaf 500 g' } });
    expect(screen.getByRole('status')).toHaveTextContent(
      /Creates a new product named "Village loaf 500 g"/i,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Apply choice' }));

    // 2. low_confidence
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 6')).toBeInTheDocument(),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Confirm as extracted' }),
    );

    // 3. missing_field
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 5')).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText(/Enter purchase date/i), {
      target: { value: '2026-11-05' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save field' }));

    // 4. line_math
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 4')).toBeInTheDocument(),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Accept printed total' }),
    );

    // 5. sum_mismatch
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 3')).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Open receipt to edit lines or total' }),
    ).toHaveAttribute('href', `/receipts/${receiptId}`);
    fireEvent.click(screen.getByRole('button', { name: 'Accept difference' }));

    // 6. code_conflict (choose second option nameProduct)
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 2')).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByLabelText(/White loaf 350 g/i));
    fireEvent.click(screen.getByRole('button', { name: 'Apply choice' }));

    // 7. possible_duplicate
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 1')).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Inspect matching receipt' }),
    ).toHaveAttribute('href', '/receipts/0193a1b2-0000-7000-8000-000000000199');
    fireEvent.click(screen.getByRole('button', { name: 'Keep both receipts' }));

    await waitFor(() =>
      expect(screen.getByText('Review queue is clear')).toBeInTheDocument(),
    );

    expect(submittedPayloads).toEqual([
      {
        flagId: '0193a1b2-0000-7000-8000-000000000901',
        body: {
          action: 'different_product',
          canonicalName: 'Village loaf 500 g',
        },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000902',
        body: { action: 'confirm' },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000903',
        body: { action: 'correct', purchasedOn: '2026-11-05' },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000904',
        body: { action: 'accept' },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000905',
        body: { action: 'accept' },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000906',
        body: {
          action: 'same_product',
          productId: '0193a1b2-0000-7000-8000-000000000409',
        },
      },
      {
        flagId: '0193a1b2-0000-7000-8000-000000000907',
        body: { action: 'accept' },
      },
    ]);
  });

  it('supports n and c keyboard shortcuts when unfocused and ignores them while typing in an input', async () => {
    const resolved: string[] = [];
    let queue: ReviewFlagEntry[] = [
      makeEntry('0193a1b2-0000-7000-8000-000000000911', {
        id: '0193a1b2-0000-7000-8000-000000000911',
        receiptId: BASE_RECEIPT_SUMMARY.id,
        lineItemId: BASE_LINE_ITEM.id,
        kind: 'uncertain_match',
        detail: {
          reason: 'similar_name',
          candidates: [
            {
              productId: '0193a1b2-0000-7000-8000-000000000403',
              canonicalName: 'Ekmek 350 g',
              sizeValue: '350',
              sizeUnit: 'g',
              score: 0.78,
            },
          ],
        },
        resolvedAt: null,
        resolution: null,
        createdAt: '2026-11-02T16:05:31Z',
      }),
      makeEntry(
        '0193a1b2-0000-7000-8000-000000000912',
        {
          id: '0193a1b2-0000-7000-8000-000000000912',
          receiptId: BASE_RECEIPT_SUMMARY.id,
          lineItemId: null,
          kind: 'low_confidence',
          detail: { field: 'store', confidence: 0.68 },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        null,
      ),
    ];

    server.use(
      http.get('/api/v1/review-flags', () =>
        HttpResponse.json({
          items: queue,
          nextCursor: null,
          totalOpen: queue.length,
        }),
      ),
      http.post('/api/v1/review-flags/:flagId/resolve', ({ params }) => {
        const flagId = String(params.flagId);
        resolved.push(flagId);
        queue = queue.filter((entry) => entry.flag.id !== flagId);
        return HttpResponse.json({
          ...mockReceipt,
          openFlagCount: queue.length,
        });
      }),
    );

    renderReviewPage();

    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 2')).toBeInTheDocument(),
    );

    // Pressing "n" advances to Flag 2 of 2.
    fireEvent.keyDown(document.body, { key: 'n' });
    expect(screen.getByText('Flag 2 of 2')).toBeInTheDocument();

    // Typing "c" or "n" inside an input must NOT resolve or skip.
    const storeInput = screen.getByLabelText(/Correct store/i);
    storeInput.focus();
    fireEvent.keyDown(storeInput, { key: 'c' });
    fireEvent.keyDown(storeInput, { key: 'n' });
    expect(resolved).toHaveLength(0);
    expect(screen.getByText('Flag 2 of 2')).toBeInTheDocument();

    // Pressing "c" outside inputs confirms the active low_confidence flag.
    storeInput.blur();
    fireEvent.keyDown(document.body, { key: 'c' });
    await waitFor(() =>
      expect(resolved).toEqual(['0193a1b2-0000-7000-8000-000000000912']),
    );
    await waitFor(() =>
      expect(screen.getByText('Flag 1 of 1')).toBeInTheDocument(),
    );
  });
});
