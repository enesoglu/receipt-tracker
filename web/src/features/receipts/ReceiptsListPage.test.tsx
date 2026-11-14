import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { components } from '../../api/schema.gen';
import { mockReceipt, mockReceiptPage, resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import {
  formatReviewFlagSummary,
  formatUncertainMatchReason,
} from './flag-reasons';
import { ReceiptsListPage } from './ReceiptsListPage';

type ReceiptSummary = components['schemas']['ReceiptSummary'];

function makeSummary(
  id: string,
  overrides: Partial<ReceiptSummary> = {},
): ReceiptSummary {
  return {
    id,
    title: 'Home shopping',
    store: {
      id: '0193a1b2-0000-7000-8000-000000000010',
      name: 'Example Market',
    },
    purchasedOn: '2026-11-12',
    currency: 'TRY',
    total: '84.50',
    status: 'ready',
    failureReason: null,
    source: 'web',
    openFlagCount: 0,
    createdAt: '2026-11-12T10:00:00Z',
    ...overrides,
  };
}

function renderListPage() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <MemoryRouter initialEntries={['/receipts']}>
      <QueryProvider client={client}>
        <ReceiptsListPage />
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

describe('flag-reasons', () => {
  it('formats uncertain_match reasons including ambiguous_code and unknown fallback strings', () => {
    expect(formatUncertainMatchReason('size_conflict')).toMatch(/size/i);
    expect(formatUncertainMatchReason('code_name_mismatch')).toMatch(/code/i);
    expect(formatUncertainMatchReason('similar_name')).toMatch(/similar/i);
    expect(formatUncertainMatchReason('ambiguous_code')).toMatch(
      /more than one/i,
    );
    expect(formatUncertainMatchReason('future_reason_code')).toBe(
      'Automatic product matching could not decide cleanly; please confirm.',
    );

    const summary = formatReviewFlagSummary(mockReceipt.flags[0]);
    expect(summary.label).toBe('Low confidence');
    expect(summary.reason).toContain('71%');
  });
});

describe('ReceiptsListPage', () => {
  it('renders loading, empty linking to /upload, and error retry states', async () => {
    let attempts = 0;
    let resolvePending!: (response: Response) => void;
    let markStarted!: () => void;
    const started = new Promise<void>((resolve) => {
      markStarted = resolve;
    });

    server.use(
      http.get('/api/v1/receipts', () => {
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
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'database temporarily unavailable',
            },
            { status: 503 },
          );
        }
        return HttpResponse.json({ items: [], nextCursor: null });
      }),
    );

    renderListPage();

    expect(screen.getByText('Loading receipts...')).toBeInTheDocument();

    await started;
    resolvePending(
      HttpResponse.json(
        {
          type: 'about:blank',
          title: 'Service Unavailable',
          status: 503,
          detail: 'database temporarily unavailable',
        },
        { status: 503 },
      ),
    );

    await waitFor(
      () =>
        expect(screen.getByText('Could not load receipts')).toBeInTheDocument(),
      { timeout: 3000 },
    );
    expect(
      screen.getByText('database temporarily unavailable'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() =>
      expect(screen.getByText('No receipts yet')).toBeInTheDocument(),
    );
    const emptyUploadLink = screen.getAllByRole('link', {
      name: 'Upload receipts',
    })[1];
    expect(emptyUploadLink).toHaveAttribute('href', '/upload');
  });

  it('filters by month, status, store, search text and loads next cursor page', async () => {
    const receiptA = makeSummary('0193a1b2-0000-7000-8000-000000000101', {
      title: 'Weekly groceries',
      total: '124.90',
      status: 'needs_review',
      openFlagCount: 2,
    });
    const receiptB = makeSummary('0193a1b2-0000-7000-8000-000000000102', {
      title: 'Bakery stop',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000011',
        name: 'Corner Bakery',
      },
      purchasedOn: '2026-10-28',
      total: '36.00',
      status: 'ready',
      openFlagCount: 0,
    });

    const recordedQueries: URLSearchParams[] = [];

    server.use(
      http.get('/api/v1/stores', () =>
        HttpResponse.json({
          items: [
            {
              id: '0193a1b2-0000-7000-8000-000000000010',
              name: 'Example Market',
              receiptCount: 4,
            },
            {
              id: '0193a1b2-0000-7000-8000-000000000011',
              name: 'Corner Bakery',
              receiptCount: 2,
            },
          ],
          nextCursor: null,
        }),
      ),
      http.get('/api/v1/receipts', ({ request }) => {
        const params = new URL(request.url).searchParams;
        recordedQueries.push(params);
        if (params.get('cursor') === 'cursor-2') {
          return HttpResponse.json({
            items: [receiptB],
            nextCursor: null,
          });
        }
        if (
          params.get('month') === '2026-10' &&
          params.get('status') === 'ready' &&
          params.get('storeId') === '0193a1b2-0000-7000-8000-000000000011' &&
          params.get('q') === 'bakery'
        ) {
          return HttpResponse.json({
            items: [receiptB],
            nextCursor: null,
          });
        }
        if (params.get('month') || params.get('status') || params.get('q')) {
          return HttpResponse.json({ items: [], nextCursor: null });
        }
        return HttpResponse.json({
          items: [receiptA],
          nextCursor: 'cursor-2',
        });
      }),
    );

    renderListPage();

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Weekly groceries' }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Weekly groceries' }),
    ).toHaveAttribute('href', `/receipts/${receiptA.id}`);
    expect(screen.getByText('2 flags')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));

    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Bakery stop' }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Weekly groceries' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Load more' }),
    ).not.toBeInTheDocument();

    // Apply all filters together
    await waitFor(() =>
      expect(
        screen.getByRole('option', { name: 'Corner Bakery' }),
      ).toBeInTheDocument(),
    );

    fireEvent.change(screen.getByLabelText('Month'), {
      target: { value: '2026-10' },
    });
    fireEvent.change(screen.getByLabelText('Status'), {
      target: { value: 'ready' },
    });
    fireEvent.change(screen.getByLabelText('Store'), {
      target: { value: '0193a1b2-0000-7000-8000-000000000011' },
    });
    fireEvent.change(screen.getByLabelText('Search'), {
      target: { value: 'bakery' },
    });

    await waitFor(() => {
      const last = recordedQueries[recordedQueries.length - 1];
      expect(last?.get('month')).toBe('2026-10');
      expect(last?.get('status')).toBe('ready');
      expect(last?.get('storeId')).toBe('0193a1b2-0000-7000-8000-000000000011');
      expect(last?.get('q')).toBe('bakery');
    });
    expect(
      screen.queryByRole('link', { name: 'Weekly groceries' }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Bakery stop' }),
    ).toBeInTheDocument();

    // Clear filters shows initial receipts again
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: 'Weekly groceries' }),
      ).toBeInTheDocument(),
    );

    // Reference default mockReceiptPage import check stays valid
    expect(mockReceiptPage.items.length).toBeGreaterThan(0);
  });
});
