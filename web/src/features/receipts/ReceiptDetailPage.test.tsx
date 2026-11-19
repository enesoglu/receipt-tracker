import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { components } from '../../api/schema.gen';
import { mockReceipt, resetMockAuth, resetMockState } from '../../mocks/data';
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

// The receipt header renders the creation day in the viewer's local zone.
process.env.TZ = 'Etc/GMT-3';

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' });
});

afterEach(() => {
  server.resetHandlers();
  resetMockAuth();
  resetMockState();
});

afterAll(() => {
  server.close();
});

describe('ReceiptDetailPage', () => {
  it('shows the receipt creation day from its timestamp, not a string slice', async () => {
    const receipt: Receipt = {
      ...mockReceipt,
      id: '0193a1b2-0000-7000-8000-000000000151',
      // 21:30Z on the 19th is 00:30 on the 20th at UTC+3.
      createdAt: '2026-11-19T21:30:00Z',
    };
    server.use(
      http.get('/api/v1/receipts/:receiptId', () => HttpResponse.json(receipt)),
    );

    renderDetailPage(receipt.id);

    await waitFor(() =>
      expect(screen.getByText(/Created Nov 20, 2026/)).toBeInTheDocument(),
    );
  });

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
    const timeInput = screen.getByLabelText(/Purchase time/i);
    const totalInput = screen.getByLabelText(/^Total/i);

    expect(dateInput).toHaveAttribute('type', 'date');
    expect(timeInput).toHaveAttribute('type', 'time');

    fireEvent.change(titleInput, { target: { value: 'Weekend market run' } });
    fireEvent.change(dateInput, { target: { value: '2026-11-10' } });
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
    expect(dateInput).toHaveValue('2026-11-10');
    expect(totalInput).toHaveValue('-10.00');

    failNextSave = false;
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

  it('normalises decimal commas before saving totals', async () => {
    let savedPayload: Record<string, unknown> | null = null;

    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(mockReceipt),
      ),
      http.put('/api/v1/receipts/:receiptId', async ({ request }) => {
        savedPayload = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(mockReceipt);
      }),
    );

    renderDetailPage(mockReceipt.id);

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Home shopping' }),
      ).toBeInTheDocument(),
    );

    fireEvent.change(screen.getByLabelText('Subtotal'), {
      target: { value: '84,5' },
    });
    fireEvent.change(screen.getByLabelText('Tax total'), {
      target: { value: '10,' },
    });
    fireEvent.change(screen.getByLabelText(/^Total/i), {
      target: { value: '351,9' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(screen.getByText('Receipt details saved.')).toBeInTheDocument(),
    );
    expect(savedPayload).toMatchObject({
      subtotal: '84.5',
      taxTotal: '10.',
      total: '351.9',
    });
  });

  it('shows the field message instead of sending a malformed amount', async () => {
    let saves = 0;

    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(mockReceipt),
      ),
      http.put('/api/v1/receipts/:receiptId', () => {
        saves += 1;
        return HttpResponse.json(mockReceipt);
      }),
    );

    renderDetailPage(mockReceipt.id);

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: 'Home shopping' }),
      ).toBeInTheDocument(),
    );

    const totalInput = screen.getByLabelText(/^Total/i);
    fireEvent.change(totalInput, { target: { value: '1.234,56' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    expect(
      await screen.findByText(
        'enter a number with up to two decimal places, using . or , — such as 351.90',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Check the highlighted fields.'),
    ).toBeInTheDocument();
    expect(saves).toBe(0);
    expect(totalInput).toHaveValue('1.234,56');
  });

  it('round-trips identity fields with server field errors and clearing', async () => {
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
                  field: 'invoiceNumber',
                  message:
                    'The invoice number must be up to 32 letters or digits.',
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
          invoiceNumber: (body.invoiceNumber as string | null) ?? null,
          ettn: (body.ettn as string | null) ?? null,
          sellerTaxNumber: (body.sellerTaxNumber as string | null) ?? null,
        };
        return HttpResponse.json(receiptState);
      }),
    );

    renderDetailPage(mockReceipt.id);

    const invoiceInput = await screen.findByLabelText(/Invoice number/i);
    expect(invoiceInput).toHaveValue('T112026000000000');
    expect(screen.getByLabelText(/^ETTN/i)).toHaveValue(
      '1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d',
    );
    expect(screen.getByLabelText(/Seller tax number/i)).toHaveValue(
      '1234567890',
    );

    // The server rejects the typed value and names the field.
    fireEvent.change(invoiceInput, { target: { value: 'not a printed no!' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(
        screen.getByText(
          'The invoice number must be up to 32 letters or digits.',
        ),
      ).toBeInTheDocument(),
    );
    expect(invoiceInput).toHaveValue('not a printed no!');

    // A corrected save round-trips the typed values into the request body.
    failNextSave = false;
    fireEvent.change(invoiceInput, { target: { value: ' t112026000000002 ' } });
    fireEvent.change(screen.getByLabelText(/^ETTN/i), {
      target: { value: '0a1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5e' },
    });
    fireEvent.change(screen.getByLabelText(/Seller tax number/i), {
      target: { value: '1234567891' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(screen.getByText('Receipt details saved.')).toBeInTheDocument(),
    );
    expect(savedPayload).toMatchObject({
      invoiceNumber: 't112026000000002',
      ettn: '0a1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5e',
      sellerTaxNumber: '1234567891',
    });

    // Emptying a box clears the column: the update carries null.
    fireEvent.change(invoiceInput, { target: { value: '' } });
    fireEvent.change(screen.getByLabelText(/^ETTN/i), {
      target: { value: '' },
    });
    fireEvent.change(screen.getByLabelText(/Seller tax number/i), {
      target: { value: '' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() =>
      expect(screen.getByText('Receipt details saved.')).toBeInTheDocument(),
    );
    expect(savedPayload).toMatchObject({
      invoiceNumber: null,
      ettn: null,
      sellerTaxNumber: null,
    });
    expect(invoiceInput).toHaveValue('');
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

    // Inline edit shows the message before submit and saves on valid input.
    fireEvent.click(screen.getByRole('button', { name: 'Edit Olive oil' }));
    const editTotalInput = screen.getByLabelText(/Line total/i);
    fireEvent.change(editTotalInput, { target: { value: 'bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }));

    expect(
      await screen.findByText(
        'enter a number with up to two decimal places, using . or , — such as 351.90',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Check the highlighted fields.'),
    ).toBeInTheDocument();
    expect(editTotalInput).toHaveValue('bad');
    expect(
      screen.getByRole('button', { name: 'Save line' }),
    ).toBeInTheDocument();

    fireEvent.change(editTotalInput, { target: { value: '255,50' } });
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

  it('resets the edit form when a refresh brings fresh extraction values', async () => {
    const base: Receipt = {
      ...mockReceipt,
      id: '0193a1b2-0000-7000-8000-000000000170',
      status: 'queued',
      updatedAt: '2026-11-19T09:00:00Z',
    };
    let current = base;
    let savedPayload: Record<string, unknown> | null = null;

    server.use(
      http.get('/api/v1/receipts/:receiptId', () => HttpResponse.json(current)),
      http.put('/api/v1/receipts/:receiptId', async ({ request }) => {
        savedPayload = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(current);
      }),
    );

    renderDetailPage(base.id);

    await waitFor(() =>
      expect(screen.getByLabelText(/^Total/i)).toHaveValue('129.40'),
    );

    current = {
      ...base,
      store: {
        id: '0193a1b2-0000-7000-8000-000000000019',
        name: 'Fresh Market',
      },
      purchasedOn: '2026-11-15',
      total: '8.33',
      status: 'needs_review',
      updatedAt: '2026-11-19T09:05:00Z',
    };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

    await waitFor(() =>
      expect(screen.getByLabelText(/^Total/i)).toHaveValue('8.33'),
    );
    expect(screen.getByLabelText('Store')).toHaveValue('Fresh Market');

    fireEvent.click(screen.getByRole('button', { name: 'Save details' }));

    await waitFor(() => expect(savedPayload).not.toBeNull());
    expect(savedPayload).toMatchObject({
      storeName: 'Fresh Market',
      purchasedOn: '2026-11-15',
      total: '8.33',
    });
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

  it('offers pending suggestions on eligible lines with accept and ignore', async () => {
    renderDetailPage(mockReceipt.id);

    // The needs_review Ekmek line and the new_product Süt line have
    // suggestions; the auto-linked Ayran line and the negative İndirim
    // line are not eligible.
    expect(
      await screen.findByText('Model suggestion for "EKMEK"'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Model suggestion for "SUT 1LT TAM YAGLI"'),
    ).toBeInTheDocument();
    expect(
      screen.queryByText('Model suggestion for "AYRAN 1LT"'),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText('Model suggestion for "INDIRIM"'),
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', {
        name: 'Accept model suggestion for EKMEK',
      }),
    );

    await waitFor(() =>
      expect(
        screen.queryByText('Model suggestion for "EKMEK"'),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.getByText('Model suggestion for "SUT 1LT TAM YAGLI"'),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', {
        name: 'Ignore model suggestion for SUT 1LT TAM YAGLI',
      }),
    );

    await waitFor(() =>
      expect(
        screen.queryByText('Model suggestion for "SUT 1LT TAM YAGLI"'),
      ).not.toBeInTheDocument(),
    );
  });
});
