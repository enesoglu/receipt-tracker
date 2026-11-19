import { render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { Receipt } from '../../api/receipts';
import { mockReceipt, resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import { ReceiptDetailContent } from './ReceiptDetailContent';
import { chainReceipt } from './e-archive-fixture';

function renderDetail(receipt: Receipt) {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <MemoryRouter>
      <QueryProvider client={client}>
        <ReceiptDetailContent
          receipt={receipt}
          isRefreshing={false}
          onRefresh={() => {}}
        />
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

describe('e-archive panel in receipt detail', () => {
  it('renders the panel under the Receipt details card in the right column', async () => {
    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(chainReceipt),
      ),
    );

    renderDetail(chainReceipt);

    const details = await screen.findByRole('heading', {
      name: 'Receipt details',
    });
    const panel = await screen.findByRole('heading', { name: 'BİM e-archive' });
    const gallery = screen.getByRole('heading', { name: 'Receipt image' });

    // The details card and the panel share one flex-column child of the
    // grid, so the panel lands below the card in column 2, not in a new row.
    const column = details.closest('div.flex.flex-col.gap-6');
    expect(column).not.toBeNull();
    expect(column).toContainElement(panel);
    expect(column).not.toContainElement(gallery);
    expect(
      screen.getByRole('link', { name: 'Open arsiv.bim.com.tr' }),
    ).toBeInTheDocument();
  });

  it('renders no panel for the default chainless receipt', () => {
    server.use(
      http.get('/api/v1/receipts/:receiptId', () =>
        HttpResponse.json(mockReceipt),
      ),
    );

    renderDetail(mockReceipt);

    expect(
      screen.queryByRole('heading', { name: /e-archive$/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: /^Open / }),
    ).not.toBeInTheDocument();
  });
});
