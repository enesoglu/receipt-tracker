import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import { mockReceipt, resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import { ReceiptLineItemsCard } from './ReceiptLineItemsCard';

function renderLineItemsCard() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <QueryProvider client={client}>
      <ReceiptLineItemsCard receipt={mockReceipt} />
    </QueryProvider>,
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

describe('ReceiptLineItemsCard', () => {
  it('sends normalised comma values when adding a line', async () => {
    const created: unknown[] = [];

    server.use(
      http.post(
        '/api/v1/receipts/:receiptId/line-items',
        async ({ request }) => {
          created.push(await request.json());
          return HttpResponse.json(mockReceipt, { status: 201 });
        },
      ),
    );

    renderLineItemsCard();

    fireEvent.click(screen.getByRole('button', { name: 'Add line item' }));
    fireEvent.change(screen.getByLabelText(/Item name/i), {
      target: { value: 'Olive oil' },
    });
    fireEvent.change(screen.getByLabelText(/Quantity/i), {
      target: { value: '2,5' },
    });
    fireEvent.change(screen.getByLabelText(/Unit price/i), {
      target: { value: '5,5' },
    });
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '13,75' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add line' }));

    await waitFor(() => expect(created).toHaveLength(1));
    expect(created[0]).toMatchObject({
      name: 'Olive oil',
      quantity: '2.5',
      unitPrice: '5.5',
      lineTotal: '13.75',
    });
  });

  it('shows the field message instead of sending a malformed total', async () => {
    const created: unknown[] = [];

    server.use(
      http.post(
        '/api/v1/receipts/:receiptId/line-items',
        async ({ request }) => {
          created.push(await request.json());
          return HttpResponse.json(mockReceipt, { status: 201 });
        },
      ),
    );

    renderLineItemsCard();

    fireEvent.click(screen.getByRole('button', { name: 'Add line item' }));
    fireEvent.change(screen.getByLabelText(/Item name/i), {
      target: { value: 'Olive oil' },
    });
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '1.234,56' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add line' }));

    expect(
      await screen.findByText(
        'enter a number with up to two decimal places, using . or , — such as 351.90',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Check the highlighted fields.'),
    ).toBeInTheDocument();
    expect(created).toHaveLength(0);
    expect(screen.getByLabelText(/Line total/i)).toHaveValue('1.234,56');
  });

  it('rejects a blank line total and a non-positive quantity', async () => {
    const created: unknown[] = [];

    server.use(
      http.post(
        '/api/v1/receipts/:receiptId/line-items',
        async ({ request }) => {
          created.push(await request.json());
          return HttpResponse.json(mockReceipt, { status: 201 });
        },
      ),
    );

    renderLineItemsCard();

    fireEvent.click(screen.getByRole('button', { name: 'Add line item' }));
    fireEvent.change(screen.getByLabelText(/Item name/i), {
      target: { value: 'Olive oil' },
    });
    // Spaces pass the browser's required check but still carry no number.
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '   ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add line' }));

    expect(
      await screen.findByText(
        'enter a number with up to two decimal places, using . or , — such as 351.90',
      ),
    ).toBeInTheDocument();
    expect(created).toHaveLength(0);

    fireEvent.change(screen.getByLabelText(/Quantity/i), {
      target: { value: '-2' },
    });
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '13.75' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add line' }));

    expect(
      await screen.findByText(
        'must be positive with at most three decimal places',
      ),
    ).toBeInTheDocument();
    expect(created).toHaveLength(0);
    expect(screen.getByLabelText(/Quantity/i)).toHaveValue('-2');
  });

  it('sends normalised comma values when editing a line', async () => {
    const updated: unknown[] = [];

    server.use(
      http.put(
        '/api/v1/receipts/:receiptId/line-items/:lineItemId',
        async ({ request }) => {
          updated.push(await request.json());
          return HttpResponse.json(mockReceipt);
        },
      ),
    );

    renderLineItemsCard();

    fireEvent.click(screen.getByRole('button', { name: 'Edit Ayran 1 l' }));
    fireEvent.change(screen.getByLabelText(/Line total/i), {
      target: { value: '84,5' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }));

    await waitFor(() => expect(updated).toHaveLength(1));
    expect(updated[0]).toMatchObject({ lineTotal: '84.5' });
  });
});
