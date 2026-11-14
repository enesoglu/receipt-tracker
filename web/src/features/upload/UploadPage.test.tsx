import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from 'vitest';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import type { components } from '../../api/schema.gen';
import { mockReceiptPage, mockUsage, resetMockAuth } from '../../mocks/data';
import { readUploadedFiles } from '../../mocks/multipart';
import { server } from '../../mocks/server';
import { UploadPage } from './UploadPage';

type ReceiptSummary = components['schemas']['ReceiptSummary'];

function makeSummary(
  id: string,
  status: ReceiptSummary['status'],
  overrides: Partial<ReceiptSummary> = {},
): ReceiptSummary {
  return {
    id,
    title: 'Home shopping',
    store: null,
    purchasedOn: null,
    currency: 'TRY',
    total: null,
    status,
    failureReason: null,
    source: 'web',
    openFlagCount: 0,
    createdAt: '2026-11-14T18:00:00Z',
    ...overrides,
  };
}

function makeFile(name: string, type: string, sizeBytes = 2048): File {
  const file = new File(['x'], name, { type });
  if (sizeBytes !== 1) {
    Object.defineProperty(file, 'size', { value: sizeBytes });
  }
  return file;
}

function renderUploadPage() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <MemoryRouter initialEntries={['/upload']}>
      <QueryProvider client={client}>
        <UploadPage />
      </QueryProvider>
    </MemoryRouter>,
  );
}

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' });
});

afterEach(() => {
  vi.useRealTimers();
  server.resetHandlers();
  resetMockAuth();
});

afterAll(() => {
  server.close();
});

describe('UploadPage', () => {
  it('renders loading, empty, and error states with retry recovery', async () => {
    let usageAttempts = 0;
    let resolvePendingUsage!: (value: Response) => void;
    let markUsageStarted!: () => void;
    const usageStarted = new Promise<void>((resolve) => {
      markUsageStarted = resolve;
    });
    server.use(
      http.get('/api/v1/usage', () => {
        usageAttempts += 1;
        if (usageAttempts === 1) {
          markUsageStarted();
          return new Promise<Response>((resolve) => {
            resolvePendingUsage = resolve;
          });
        }
        if (usageAttempts === 2) {
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'usage counter unavailable',
            },
            { status: 503 },
          );
        }
        return HttpResponse.json(mockUsage);
      }),
    );

    renderUploadPage();

    expect(screen.getByText('Loading extraction usage...')).toBeInTheDocument();
    expect(screen.getByText('No files uploaded yet')).toBeInTheDocument();

    await usageStarted;

    resolvePendingUsage(
      HttpResponse.json(
        {
          type: 'about:blank',
          title: 'Service Unavailable',
          status: 503,
          detail: 'usage counter unavailable',
        },
        { status: 503 },
      ),
    );

    // Retry path after query error (client retries 5xx once before failing)
    await waitFor(
      () =>
        expect(
          screen.getByText('Could not load extraction usage'),
        ).toBeInTheDocument(),
      { timeout: 3000 },
    );
    expect(screen.getByText('usage counter unavailable')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() =>
      expect(screen.getByText(/pages used/i)).toBeInTheDocument(),
    );
    expect(screen.getByText('363 pages remaining')).toBeInTheDocument();

    // Batch upload error and retry
    let uploadCalls = 0;
    server.use(
      http.post('/api/v1/receipts', () => {
        uploadCalls += 1;
        if (uploadCalls === 1) {
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'image storage is offline',
            },
            { status: 503 },
          );
        }
        return HttpResponse.json({
          results: [
            {
              index: 0,
              fileName: 'receipt.jpg',
              outcome: 'duplicate',
              receiptId: mockReceiptPage.items[1].id,
              error: null,
            },
          ],
        });
      }),
    );

    const input = screen.getByLabelText('Choose receipt photos');
    fireEvent.change(input, {
      target: { files: [makeFile('receipt.jpg', 'image/jpeg')] },
    });

    await waitFor(() =>
      expect(screen.getByText('Upload failed')).toBeInTheDocument(),
    );
    expect(
      screen.getAllByText('image storage is offline').length,
    ).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole('button', { name: 'Retry batch' }));

    await waitFor(() =>
      expect(screen.getByText('Duplicate')).toBeInTheDocument(),
    );
    expect(uploadCalls).toBe(2);
  });

  it('validates file types, 15 MB size limit, and 20-file batch limit client-side', async () => {
    const sentNames: string[] = [];
    server.use(
      http.post('/api/v1/receipts', async ({ request }) => {
        const files = await readUploadedFiles(request);
        for (const file of files) {
          sentNames.push(file.name);
        }
        return HttpResponse.json({
          results: files.map((file, index) => ({
            index,
            fileName: file.name,
            outcome: 'duplicate',
            receiptId: mockReceiptPage.items[1].id,
            error: null,
          })),
        });
      }),
    );

    renderUploadPage();

    const input = screen.getByLabelText('Choose receipt photos');

    const tooMany = Array.from({ length: 21 }, (_, idx) =>
      makeFile(`photo-${idx}.jpg`, 'image/jpeg'),
    );
    fireEvent.change(input, { target: { files: tooMany } });
    expect(
      screen.getByText(
        'You can upload at most 20 files in one batch (21 selected).',
      ),
    ).toBeInTheDocument();
    expect(sentNames).toHaveLength(0);

    const badType = makeFile('notes.pdf', 'application/pdf', 1024);
    const tooLarge = makeFile('huge.jpg', 'image/jpeg', 15 * 1024 * 1024 + 1);
    const emptyPng = makeFile('blank.png', 'image/png', 0);
    const validHeic = makeFile('market.heic', '', 2048);

    fireEvent.change(input, {
      target: { files: [badType, tooLarge, emptyPng, validHeic] },
    });

    await waitFor(() =>
      expect(screen.getByText('Duplicate')).toBeInTheDocument(),
    );
    expect(sentNames).toEqual(['market.heic']);
    expect(
      screen.getByText('Only JPEG, PNG, and HEIC/HEIF photos are accepted.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('File exceeds the 15 MB size limit.'),
    ).toBeInTheDocument();
    expect(screen.getByText('File is empty.')).toBeInTheDocument();
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });

  it('handles a ten-file batch upload with mixed outcomes in one request', async () => {
    let postRequests = 0;
    let resolveUpload!: (response: Response) => void;

    const idQueued = '0193a1b2-0000-7000-8000-000000000801';
    const idProcessing = '0193a1b2-0000-7000-8000-000000000802';
    const idReview = '0193a1b2-0000-7000-8000-000000000803';
    const idReady = '0193a1b2-0000-7000-8000-000000000804';
    const idFailed = '0193a1b2-0000-7000-8000-000000000805';
    const idQuota = '0193a1b2-0000-7000-8000-000000000806';
    const existingDuplicateId = '0193a1b2-0000-7000-8000-000000000100';

    server.use(
      http.post('/api/v1/receipts', async ({ request }) => {
        postRequests += 1;
        const files = await readUploadedFiles(request);
        expect(files).toHaveLength(8);
        return new Promise<Response>((resolve) => {
          resolveUpload = resolve;
        });
      }),
      http.get('/api/v1/receipts', () =>
        HttpResponse.json({
          items: [
            makeSummary(idQueued, 'queued'),
            makeSummary(idProcessing, 'processing'),
            makeSummary(idReview, 'needs_review', { openFlagCount: 2 }),
            makeSummary(idReady, 'ready', { total: '92.10' }),
            makeSummary(idFailed, 'failed', {
              failureReason: 'Receipt image was too blurry to read.',
            }),
            makeSummary(idQuota, 'waiting_quota'),
          ],
          nextCursor: null,
        }),
      ),
    );

    renderUploadPage();

    const dropArea = screen.getByRole('region', {
      name: 'Receipt photo drop area',
    });

    const tenFiles = [
      makeFile('01-queued.jpg', 'image/jpeg'),
      makeFile('02-processing.png', 'image/png'),
      makeFile('03-review.heic', 'image/heic'),
      makeFile('04-ready.heif', 'image/heif'),
      makeFile('05-failed.jpg', 'image/jpeg'),
      makeFile('06-quota.png', 'image/png'),
      makeFile('07-duplicate.jpg', 'image/jpeg'),
      makeFile('08-unreadable.jpg', 'image/jpeg'),
      makeFile('09-notes.pdf', 'application/pdf'),
      makeFile('10-oversized.jpg', 'image/jpeg', 16 * 1024 * 1024),
    ];

    fireEvent.drop(dropArea, {
      dataTransfer: { files: tenFiles },
    });

    expect(screen.getAllByText('Uploading')).toHaveLength(8);
    expect(screen.getAllByText('Failed')).toHaveLength(2);
    await waitFor(() => expect(postRequests).toBe(1));

    resolveUpload(
      HttpResponse.json({
        results: [
          {
            index: 0,
            fileName: '01-queued.jpg',
            outcome: 'created',
            receiptId: idQueued,
            error: null,
          },
          {
            index: 1,
            fileName: '02-processing.png',
            outcome: 'created',
            receiptId: idProcessing,
            error: null,
          },
          {
            index: 2,
            fileName: '03-review.heic',
            outcome: 'created',
            receiptId: idReview,
            error: null,
          },
          {
            index: 3,
            fileName: '04-ready.heif',
            outcome: 'created',
            receiptId: idReady,
            error: null,
          },
          {
            index: 4,
            fileName: '05-failed.jpg',
            outcome: 'created',
            receiptId: idFailed,
            error: null,
          },
          {
            index: 5,
            fileName: '06-quota.png',
            outcome: 'created',
            receiptId: idQuota,
            error: null,
          },
          {
            index: 6,
            fileName: '07-duplicate.jpg',
            outcome: 'duplicate',
            receiptId: existingDuplicateId,
            error: null,
          },
          {
            index: 7,
            fileName: '08-unreadable.jpg',
            outcome: 'rejected',
            receiptId: null,
            error: {
              code: 'unreadable',
              message: 'Image header is corrupted.',
            },
          },
        ],
      }),
    );

    await waitFor(() =>
      expect(screen.getByText('Needs review')).toBeInTheDocument(),
    );

    expect(screen.getByText('Queued')).toBeInTheDocument();
    expect(screen.getByText('Processing')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Waiting for quota')).toBeInTheDocument();
    expect(screen.getByText('Duplicate')).toBeInTheDocument();
    expect(screen.getAllByText('Failed')).toHaveLength(4);
    expect(
      screen.getByText('Receipt image was too blurry to read.'),
    ).toBeInTheDocument();
    expect(screen.getByText('Image header is corrupted.')).toBeInTheDocument();

    const duplicateLink = screen.getByRole('link', {
      name: 'View existing receipt',
    });
    expect(duplicateLink).toHaveAttribute(
      'href',
      `/receipts/${existingDuplicateId}`,
    );

    const viewLinks = screen.getAllByRole('link', { name: 'View receipt' });
    expect(viewLinks).toHaveLength(2);
    expect(viewLinks.map((link) => link.getAttribute('href')).sort()).toEqual(
      [`/receipts/${idReview}`, `/receipts/${idReady}`].sort(),
    );
  });

  it('polls listReceipts every 2 seconds while queued or processing and stops once settled', async () => {
    vi.useFakeTimers();

    const idA = '0193a1b2-0000-7000-8000-000000000901';
    const idB = '0193a1b2-0000-7000-8000-000000000902';
    let pollCount = 0;

    server.use(
      http.post('/api/v1/receipts', () =>
        HttpResponse.json({
          results: [
            {
              index: 0,
              fileName: 'a.jpg',
              outcome: 'created',
              receiptId: idA,
              error: null,
            },
            {
              index: 1,
              fileName: 'b.png',
              outcome: 'created',
              receiptId: idB,
              error: null,
            },
          ],
        }),
      ),
      http.get('/api/v1/receipts', ({ request }) => {
        const idsParam = new URL(request.url).searchParams.get('ids');
        expect(idsParam).toBe(`${idA},${idB}`);
        pollCount += 1;

        if (pollCount === 1) {
          return HttpResponse.json({
            items: [makeSummary(idA, 'queued'), makeSummary(idB, 'processing')],
            nextCursor: null,
          });
        }
        if (pollCount === 2) {
          return HttpResponse.json({
            items: [makeSummary(idA, 'processing'), makeSummary(idB, 'ready')],
            nextCursor: null,
          });
        }
        return HttpResponse.json({
          items: [
            makeSummary(idA, 'needs_review', { openFlagCount: 1 }),
            makeSummary(idB, 'ready'),
          ],
          nextCursor: null,
        });
      }),
    );

    renderUploadPage();
    await vi.advanceTimersByTimeAsync(0);

    const input = screen.getByLabelText('Choose receipt photos');
    fireEvent.change(input, {
      target: {
        files: [
          makeFile('a.jpg', 'image/jpeg'),
          makeFile('b.png', 'image/png'),
        ],
      },
    });

    await vi.advanceTimersByTimeAsync(100);
    expect(pollCount).toBe(1);
    expect(screen.getByText('Queued')).toBeInTheDocument();
    expect(screen.getByText('Processing')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(1899);
    expect(pollCount).toBe(1);

    await vi.advanceTimersByTimeAsync(100);
    expect(pollCount).toBe(2);
    expect(screen.getByText('Ready')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(2000);
    expect(pollCount).toBe(3);
    expect(screen.getByText('Needs review')).toBeInTheDocument();

    // No further polling once every receipt reaches a non-active state.
    await vi.advanceTimersByTimeAsync(10000);
    expect(pollCount).toBe(3);
  });
});
