import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { components } from '../../api/schema.gen';
import { mockReceipt, resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import { ReceiptDetailPage } from './ReceiptDetailPage';

type Receipt = components['schemas']['Receipt'];

function renderDetailPage(receiptId: string) {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <MemoryRouter initialEntries={[`/receipts/${receiptId}`]}>
      <QueryProvider client={client}>
        <Routes>
          <Route path="/receipts" element={<h1>Receipts list target</h1>} />
          <Route path="/receipts/:receiptId" element={<ReceiptDetailPage />} />
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
  resetMockAuth();
});

afterAll(() => {
  server.close();
});

describe('ReceiptDetailPage', () => {
  it('renders loading, error retry, image with Download original gating, and flagged fields', async () => {
    let attempts = 0;
    let resolvePending!: (response: Response) => void;
    let markStarted!: () => void;
    const started = new Promise<void>((resolve) => {
      markStarted = resolve;
    });

    const flaggedReceipt: Receipt = {
      ...mockReceipt,
      id: '0193a1b2-0000-7000-8000-000000000150',
      images: [
        {
          ...mockReceipt.images[0],
          original: null,
        },
      ],
      flags: [
        {
          id: '0193a1b2-0000-7000-8000-000000000391',
          receiptId: '0193a1b2-0000-7000-8000-000000000150',
          kind: 'low_confidence',
          lineItemId: null,
          detail: {
            field: 'store',
            confidence: 0.62,
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
        {
          id: '0193a1b2-0000-7000-8000-000000000392',
          receiptId: '0193a1b2-0000-7000-8000-000000000150',
          kind: 'possible_duplicate',
          lineItemId: null,
          detail: {
            otherReceiptId: '0193a1b2-0000-7000-8000-000000000199',
          },
          resolvedAt: null,
          resolution: null,
          createdAt: '2026-11-02T16:05:31Z',
        },
      ],
    };

    server.use(
      http.get('/api/v1/receipts/:receiptId', () => {
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
              detail: 'Database temporarily unreachable',
            },
            {
              status: 503,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          );
        }
        return HttpResponse.json(flaggedReceipt);
      }),
    );

    renderDetailPage(flaggedReceipt.id);

    await started;
    expect(screen.getByText('Loading receipt...')).toBeInTheDocument();

    resolvePending(
      HttpResponse.json(
        {
          type: 'https://receipt-tracker.example/problems/unavailable',
          title: 'Service unavailable',
          status: 503,
          detail: 'Database temporarily unreachable',
        },
        {
          status: 503,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    );

    await waitFor(
      () =>
        expect(screen.getByText('Could not load receipt')).toBeInTheDocument(),
      { timeout: 3000 },
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Home shopping' }),
      ).toBeInTheDocument(),
    );

    expect(
      screen.getByRole('img', { name: 'Home shopping image 1' }),
    ).toHaveAttribute(
      'src',
      `/api/v1/receipts/${flaggedReceipt.id}/images/${flaggedReceipt.images[0].id}`,
    );
    expect(
      screen.queryByRole('link', { name: 'Download original' }),
    ).not.toBeInTheDocument();

    expect(
      screen.getByText('Extractor confidence was 62% for store.'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'View other receipt' }),
    ).toHaveAttribute('href', '/receipts/0193a1b2-0000-7000-8000-000000000199');
  });

  it('shows Download original link when image has original and preserves edits on 422 fieldErrors', async () => {
    let failNextSave = true;
    let savedPayload: unknown = null;
    let receiptState = structuredClone(mockReceipt);

    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(receiptState),
      ),
      http.put('/api/v1/receipts/:receiptId', async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        if (failNextSave) {
          return HttpResponse.json(
            {
              type: 'https://receipt-tracker.example/problems/invalid',
              title: 'Invalid receipt details',
              status: 422,
              detail: 'Please check the highlighted fields.',
              errors: [
                {
                  field: 'purchasedOn',
                  message: 'Use YYYY-MM-DD date format.',
                },
                {
                  field: 'total',
                  message: 'Total must be non-negative.',
                },
              ],
            },
            {
              status: 422,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          );
        }
        savedPayload = body;
        receiptState = {
          ...receiptState,
          title: String(body.title),
          purchasedOn: String(body.purchasedOn),
          total: String(body.total),
        };
        return HttpResponse.json(receiptState);
      }),
    );

    renderDetailPage(mockReceipt.id);

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Download original' }),
      ).toHaveAttribute(
        'href',
        `/api/v1/receipts/${mockReceipt.id}/images/${mockReceipt.images[0].id}?variant=original`,
      ),
    );

    const titleInput = screen.getByLabelText(/Title/i);
    const dateInput = screen.getByLabelText(/Purchase date/i);
    const totalInput = screen.getByLabelText(/^Total/i);

    fireEvent.change(titleInput, { target: { value: 'Weekend market run' } });
    fireEvent.change(dateInput, { target: { value: 'invalid-date' } });
    fireEvent.change(totalInput, { target: { value: '-10.00' } });

    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(
        screen.getByText('Use YYYY-MM-DD date format.'),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText('Total must be non-negative.')).toBeInTheDocument();

    // User input stays intact after validation failure.
    expect(titleInput).toHaveValue('Weekend market run');
    expect(dateInput).toHaveValue('invalid-date');
    expect(totalInput).toHaveValue('-10.00');

    failNextSave = false;
    fireEvent.change(dateInput, { target: { value: '2026-11-10' } });
    fireEvent.change(totalInput, { target: { value: '142.50' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(screen.getByText('Receipt details saved.')).toBeInTheDocument(),
    );
    expect(savedPayload).toMatchObject({
      title: 'Weekend market run',
      purchasedOn: '2026-11-10',
      total: '142.50',
    });
  });

  it('supports adding, editing with validation recovery, and deleting line items', async () => {
    let receiptState = structuredClone(mockReceipt);

    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(receiptState),
      ),
      http.post(
        '/api/v1/receipts/:receiptId/line-items',
        async ({ request }) => {
          const body =
            (await request.json()) as components['schemas']['LineItemInput'];
          receiptState = {
            ...receiptState,
            lineItems: [
              ...receiptState.lineItems,
              {
                id: '0193a1b2-0000-7000-8000-000000000299',
                position: receiptState.lineItems.length,
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
              },
            ],
          };
          return HttpResponse.json(receiptState, { status: 201 });
        },
      ),
      http.put(
        '/api/v1/receipts/:receiptId/line-items/:lineItemId',
        async ({ request, params }) => {
          const body =
            (await request.json()) as components['schemas']['LineItemInput'];
          if (body.lineTotal === 'bad') {
            return HttpResponse.json(
              {
                type: 'https://receipt-tracker.example/problems/invalid',
                title: 'Invalid line item',
                status: 422,
                detail: 'Line total is not a valid decimal.',
                errors: [
                  {
                    field: 'lineTotal',
                    message: 'Enter a valid decimal amount.',
                  },
                ],
              },
              {
                status: 422,
                headers: { 'Content-Type': 'application/problem+json' },
              },
            );
          }
          receiptState = {
            ...receiptState,
            lineItems: receiptState.lineItems.map((item) =>
              item.id === String(params.lineItemId)
                ? { ...item, ...body }
                : item,
            ),
          };
          return HttpResponse.json(receiptState);
        },
      ),
      http.delete(
        '/api/v1/receipts/:receiptId/line-items/:lineItemId',
        ({ params }) => {
          receiptState = {
            ...receiptState,
            lineItems: receiptState.lineItems.filter(
              (item) => item.id !== String(params.lineItemId),
            ),
          };
          return HttpResponse.json(receiptState);
        },
      ),
    );

    renderDetailPage(mockReceipt.id);

    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Edit Ayran 1 l' }),
      ).toBeInTheDocument(),
    );

    // Add a new line item.
    fireEvent.click(screen.getByRole('button', { name: 'Add line item' }));
    fireEvent.change(screen.getByLabelText(/Item name/i), {
      target: { value: 'Olive oil' },
    });
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '240.00' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add line' }));

    await waitFor(() =>
      expect(screen.getByText('Olive oil')).toBeInTheDocument(),
    );

    // Inline edit preserves input on 422 and saves on valid input.
    fireEvent.click(screen.getByRole('button', { name: 'Edit Olive oil' }));
    const editTotalInput = screen.getByLabelText(/Line total/i);
    fireEvent.change(editTotalInput, { target: { value: 'bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }));

    await waitFor(() =>
      expect(
        screen.getByText('Enter a valid decimal amount.'),
      ).toBeInTheDocument(),
    );
    expect(editTotalInput).toHaveValue('bad');

    fireEvent.change(editTotalInput, { target: { value: '255.00' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }));

    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Save line' }),
      ).not.toBeInTheDocument(),
    );

    // Delete line item.
    fireEvent.click(screen.getByRole('button', { name: 'Delete Olive oil' }));
    await waitFor(() =>
      expect(screen.queryByText('Olive oil')).not.toBeInTheDocument(),
    );
  });

  it('confirms reprocess and delete inside dialogs and navigates to /receipts on delete', async () => {
    let reprocessed = false;
    let deleted = false;

    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(mockReceipt),
      ),
      http.post('/api/v1/receipts/:receiptId/reprocess', () => {
        reprocessed = true;
        return HttpResponse.json({ ...mockReceipt, status: 'queued' });
      }),
      http.delete('/api/v1/receipts/:receiptId', () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderDetailPage(mockReceipt.id);

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Home shopping' }),
      ).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Reprocess' }));
    expect(
      screen.getByRole('dialog', { name: 'Reprocess receipt?' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Confirm reprocess' }));

    await waitFor(() => expect(reprocessed).toBe(true));
    expect(screen.getByRole('status')).toHaveTextContent(
      /Reprocessing queued/i,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Delete receipt' }));
    expect(
      screen.getByRole('dialog', { name: 'Delete receipt?' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }));

    await waitFor(() => expect(deleted).toBe(true));
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Receipts list target' }),
      ).toBeInTheDocument(),
    );
  });
});
