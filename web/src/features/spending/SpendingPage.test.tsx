import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { resetMockState, setMockAuthMode } from '../../mocks/data';
import { server } from '../../mocks/server';
import {
  buildBreakdownReceiptsHref,
  formatSharePercentage,
  getDefaultMonthRange,
  validateMonthRange,
} from './spending-helpers';
import { SpendingPage } from './SpendingPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderSpendingPage() {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/spending']}>
        <SpendingPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('spending helpers', () => {
  it('builds one filtered receipts link per breakdown group kind', () => {
    type Group = Parameters<typeof buildBreakdownReceiptsHref>[1];
    const id = '0193a1b2-0000-7000-8000-000000000020';
    const group = (
      kind: Group['kind'],
      groupId: string | null = null,
    ): Group => ({
      kind,
      id: groupId,
      name: 'Group',
      amount: '10.00',
      share: '0.1000',
      itemCount: 1,
    });

    expect(
      buildBreakdownReceiptsHref('2026-11', group('category', id), 'category'),
    ).toBe(`/receipts?month=2026-11&categoryId=${id}`);
    expect(
      buildBreakdownReceiptsHref('2026-11', group('store', id), 'store'),
    ).toBe(`/receipts?month=2026-11&storeId=${id}`);
    expect(
      buildBreakdownReceiptsHref('2026-11', group('uncategorised'), 'category'),
    ).toBe('/receipts?month=2026-11&group=uncategorised');
    expect(
      buildBreakdownReceiptsHref('2026-11', group('uncategorised'), 'store'),
    ).toBe('/receipts?month=2026-11&group=no-store');
    expect(
      buildBreakdownReceiptsHref('2026-11', group('discounts'), 'category'),
    ).toBe('/receipts?month=2026-11&group=discounts');
    expect(
      buildBreakdownReceiptsHref('2026-11', group('unallocated'), 'category'),
    ).toBe('/receipts?month=2026-11&group=unallocated');
  });

  it('defaults to the local current month during the first hours of a month', () => {
    const originalTz = process.env.TZ;
    process.env.TZ = 'Europe/Istanbul';
    try {
      expect(
        getDefaultMonthRange(new Date('2026-12-01T00:30:00+03:00')),
      ).toEqual({ from: '2026-01', to: '2026-12' });
    } finally {
      if (originalTz === undefined) {
        delete process.env.TZ;
      } else {
        process.env.TZ = originalTz;
      }
    }
  });

  it('computes a 12-month default range across year boundaries and validates spans', () => {
    expect(getDefaultMonthRange(new Date('2026-11-15T00:00:00Z'))).toEqual({
      from: '2025-12',
      to: '2026-11',
    });
    expect(getDefaultMonthRange(new Date('2026-03-01T00:00:00Z'))).toEqual({
      from: '2025-04',
      to: '2026-03',
    });
    expect(validateMonthRange('2026-11', '2026-10')).toBe(
      'From month must be on or before To month.',
    );
    expect(validateMonthRange('2020-01', '2026-01')).toBe(
      'Date range cannot exceed 60 months.',
    );
    expect(validateMonthRange('2026-01', '2026-11')).toBeNull();
    expect(formatSharePercentage('0.6530')).toBe('65.3%');
  });
});

describe('SpendingPage', () => {
  beforeAll(() => {
    server.listen({ onUnhandledRequest: 'error' });
  });

  afterEach(() => {
    server.resetHandlers();
    resetMockState();
    setMockAuthMode('signed_in');
  });

  afterAll(() => {
    server.close();
  });

  it('renders loading, error retry, and empty spending states', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let attempts = 0;

    server.use(
      http.get('/api/v1/reports/monthly-spending', async ({ request }) => {
        attempts += 1;
        const url = new URL(request.url);
        if (attempts === 1) {
          await gate;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Internal Server Error',
              status: 500,
              detail: 'Aggregation unavailable',
            },
            { status: 500 },
          );
        }
        return HttpResponse.json({
          from: url.searchParams.get('from') ?? '2025-12',
          to: url.searchParams.get('to') ?? '2026-11',
          months: [],
          excludedReceipts: 0,
        });
      }),
    );

    renderSpendingPage();
    expect(screen.getByText('Loading monthly spending…')).toBeInTheDocument();

    release();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Could not load spending report');
    expect(alert).toHaveTextContent('Aggregation unavailable');

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() =>
      expect(
        screen.getByText('No spending data in this range'),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Upload receipts' }),
    ).toHaveAttribute('href', '/upload');
  });

  it('charts monthly totals per currency, drills into month breakdown by category and store, and links to receipts', async () => {
    const requestedSpendingQueries: {
      from: string | null;
      to: string | null;
    }[] = [];

    server.use(
      http.get('/api/v1/reports/monthly-spending', ({ request }) => {
        const url = new URL(request.url);
        requestedSpendingQueries.push({
          from: url.searchParams.get('from'),
          to: url.searchParams.get('to'),
        });
        return HttpResponse.json({
          from: url.searchParams.get('from') ?? '2025-12',
          to: url.searchParams.get('to') ?? '2026-11',
          months: [
            {
              month: '2026-09',
              totals: [{ currency: 'TRY', amount: '980.50', receiptCount: 6 }],
            },
            { month: '2026-10', totals: [] },
            {
              month: '2026-11',
              totals: [
                { currency: 'TRY', amount: '1294.00', receiptCount: 9 },
                { currency: 'EUR', amount: '23.80', receiptCount: 1 },
              ],
            },
          ],
          excludedReceipts: 1,
        });
      }),
    );

    const { container } = renderSpendingPage();

    // Excluded receipts notice and both currency sections render.
    expect(
      await screen.findByText(/1 receipt is excluded from these totals/),
    ).toBeInTheDocument();
    expect(screen.getByText('TRY monthly spending')).toBeInTheDocument();
    expect(screen.getByText('EUR monthly spending')).toBeInTheDocument();

    const expectedDefault = getDefaultMonthRange();
    expect(requestedSpendingQueries[0]).toEqual({
      from: expectedDefault.from,
      to: expectedDefault.to,
    });

    // Breakdown defaults to Nov 2026 grouped by category.
    expect(
      await screen.findByText('Breakdown for Nov 2026'),
    ).toBeInTheDocument();
    expect(await screen.findByText('65.3%')).toBeInTheDocument();
    expect(screen.getAllByText('Uncategorised').length).toBeGreaterThanOrEqual(
      1,
    );
    expect(screen.getAllByText('Unallocated').length).toBeGreaterThanOrEqual(1);

    const categoryReceiptLinks = screen.getAllByRole('link', {
      name: 'View receipts',
    });
    expect(categoryReceiptLinks[0]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&categoryId=0193a1b2-0000-7000-8000-000000000020',
    );
    expect(categoryReceiptLinks[1]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&group=uncategorised',
    );
    expect(categoryReceiptLinks[2]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&group=unallocated',
    );

    // Recharts bars render cleanly with token fill and zero hex colours.
    await waitFor(() => {
      const bars = container.querySelectorAll('.recharts-bar-rectangle path');
      expect(bars.length).toBeGreaterThanOrEqual(2);
      for (const bar of bars) {
        expect(bar.getAttribute('fill')).toBe('var(--color-accent)');
        expect(bar.getAttribute('d')).toBeTruthy();
      }
    });

    // Switching breakdown tab to "By store" adds storeId to receipt links.
    fireEvent.click(screen.getByRole('tab', { name: 'By store' }));
    expect(await screen.findByText('68.8%')).toBeInTheDocument();

    const storeSection = screen.getByLabelText('TRY store breakdown');
    expect(
      within(storeSection).getByRole('cell', { name: 'Example Market' }),
    ).toBeInTheDocument();
    const storeLinks = within(storeSection).getAllByRole('link', {
      name: 'View receipts',
    });
    expect(storeLinks[0]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&storeId=0193a1b2-0000-7000-8000-000000000010',
    );
    expect(storeLinks[1]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&storeId=0193a1b2-0000-7000-8000-000000000011',
    );
    expect(storeLinks[2]).toHaveAttribute(
      'href',
      '/receipts?month=2026-11&group=unallocated',
    );

    // Clicking Sep 2026 in the monthly table drills into Sep 2026 store breakdown.
    fireEvent.click(screen.getAllByRole('button', { name: 'Sep 2026' })[0]);
    expect(
      await screen.findByText('Breakdown for Sep 2026'),
    ).toBeInTheDocument();
    expect(await screen.findByText('63.2%')).toBeInTheDocument();

    // Clicking empty Oct 2026 displays breakdown empty state with link to receipts.
    fireEvent.click(screen.getAllByRole('button', { name: 'Oct 2026' })[0]);
    expect(
      await screen.findByText('No recorded spending in Oct 2026'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Inspect receipts for Oct 2026' }),
    ).toHaveAttribute('href', '/receipts?month=2026-10');
  });

  it('handles breakdown API errors with retry and validates custom month ranges', async () => {
    let failBreakdown = true;

    server.use(
      http.get('/api/v1/reports/monthly-spending', ({ request }) => {
        const url = new URL(request.url);
        return HttpResponse.json({
          from: url.searchParams.get('from') ?? '2026-09',
          to: url.searchParams.get('to') ?? '2026-11',
          months: [
            {
              month: '2026-11',
              totals: [{ currency: 'TRY', amount: '1294.00', receiptCount: 9 }],
            },
          ],
          excludedReceipts: 0,
        });
      }),
      http.get('/api/v1/reports/month-breakdown', () => {
        if (failBreakdown) {
          failBreakdown = false;
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Internal Server Error',
              status: 500,
              detail: 'Breakdown query timed out',
            },
            { status: 500 },
          );
        }
        return HttpResponse.json({
          month: '2026-11',
          groupBy: 'category',
          currencies: [
            {
              currency: 'TRY',
              total: '1294.00',
              groups: [
                {
                  kind: 'category',
                  id: '0193a1b2-0000-7000-8000-000000000020',
                  name: 'Dairy',
                  amount: '1294.00',
                  share: '1.0000',
                  itemCount: 15,
                },
              ],
            },
          ],
          excludedReceipts: 0,
        });
      }),
    );

    renderSpendingPage();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Could not load month breakdown');
    expect(alert).toHaveTextContent('Breakdown query timed out');

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('100.0%')).toBeInTheDocument();

    // Setting From month after To month blocks invalid query and shows inline alert.
    fireEvent.change(screen.getByLabelText('From month'), {
      target: { value: '2026-12' },
    });
    fireEvent.change(screen.getByLabelText('To month'), {
      target: { value: '2026-06' },
    });
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'From month must be on or before To month.',
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reset to last 12 months' }),
    );
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
    );
  });
});
