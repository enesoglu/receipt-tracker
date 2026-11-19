import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
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
import type { UploadItem } from '../../api/upload-queue';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import { mockUsage, resetMockAuth } from '../../mocks/data';
import { server } from '../../mocks/server';
import { UploadPage } from './UploadPage';

// The durable queue is replaced by a hand-written fake that exposes the
// same surface the hook consumes. The real queue logic has its own tests;
// this file tests what the screen does with the queue's reports.
const fake = vi.hoisted(() => {
  interface QueueEvents {
    changed?: () => void;
    progress?: (id: string, uploaded: number, total: number) => void;
  }

  class FakeUploadQueue {
    items: Array<{
      id: string;
      fileName: string;
      sizeBytes: number;
      blob: File;
      createdAt: number;
      state: string;
      receiptId: string | null;
      outcome: string | null;
      error: string | null;
    }> = [];
    enqueueCalls: File[][] = [];
    resumeCalls = 0;
    clearSettledCalls = 0;
    events: QueueEvents = {};

    subscribe(events: QueueEvents): () => void {
      this.events = events;
      return () => {
        this.events = {};
      };
    }

    async list() {
      return this.items.map((item) => ({ ...item }));
    }

    async resume() {
      this.resumeCalls += 1;
      this.events.changed?.();
    }

    async enqueue(files: readonly File[]) {
      this.enqueueCalls.push([...files]);
      for (const file of files) {
        this.items.push({
          id: `q-${this.items.length + 1}`,
          fileName: file.name,
          sizeBytes: file.size,
          blob: file,
          createdAt: Date.now() + this.items.length,
          state: 'pending',
          receiptId: null,
          outcome: null,
          error: null,
        });
      }
      this.events.changed?.();
    }

    async clearSettled() {
      this.clearSettledCalls += 1;
      this.items = this.items.filter(
        (item) =>
          item.state === 'pending' ||
          item.state === 'uploading' ||
          item.state === 'needs_signin',
      );
      this.events.changed?.();
    }

    emitProgress(id: string, uploaded: number, total: number) {
      this.events.progress?.(id, uploaded, total);
    }

    seed(items: UploadItem[]) {
      this.items.push(...items);
    }

    reset() {
      this.items = [];
      this.enqueueCalls = [];
      this.resumeCalls = 0;
      this.clearSettledCalls = 0;
      this.events = {};
    }
  }

  return { queue: new FakeUploadQueue() };
});

vi.mock('../../api/upload-queue', () => ({
  getUploadQueue: () => Promise.resolve(fake.queue),
}));

function makeFile(name: string, type: string, sizeBytes = 2048): File {
  const file = new File(['x'], name, { type });
  if (sizeBytes !== 1) {
    Object.defineProperty(file, 'size', { value: sizeBytes });
  }
  return file;
}

function makeItem(item: Partial<UploadItem>): UploadItem {
  return {
    id: item.id ?? 'q-seeded',
    fileName: item.fileName ?? 'receipt.jpg',
    sizeBytes: item.sizeBytes ?? 2048,
    blob: item.blob ?? makeFile(item.fileName ?? 'receipt.jpg', 'image/jpeg'),
    createdAt: item.createdAt ?? 0,
    state: item.state ?? 'pending',
    receiptId: item.receiptId ?? null,
    outcome: item.outcome ?? null,
    error: item.error ?? null,
  };
}

function pipelineItem(
  receiptId: string,
  stage: string,
  queuePosition: number | null,
  failureReason: string | null = null,
) {
  return { receiptId, stage, queuePosition, failureReason };
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
  fake.queue.reset();
});

afterAll(() => {
  server.close();
});

describe('UploadPage', () => {
  it('renders usage loading, empty, and error states with retry recovery', async () => {
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
  });

  it('resumes the durable queue on mount and lists every item state', async () => {
    server.use(
      http.get('/api/v1/pipeline', () =>
        HttpResponse.json({
          items: [
            pipelineItem('rec-created', 'ready', null),
            pipelineItem('rec-quota', 'waiting_quota', null),
          ],
        }),
      ),
    );
    fake.queue.seed([
      makeItem({
        id: 'q-1',
        fileName: 'stuck.jpg',
        state: 'pending',
        error: 'The server could not be reached.',
      }),
      makeItem({
        id: 'q-2',
        fileName: 'landed.jpg',
        state: 'done',
        receiptId: 'rec-created',
        outcome: 'created',
      }),
      makeItem({
        id: 'q-3',
        fileName: 'copy.jpg',
        state: 'done',
        receiptId: 'rec-dup',
        outcome: 'duplicate',
      }),
      makeItem({
        id: 'q-4',
        fileName: 'rejected.jpg',
        state: 'failed',
        error: 'The file is larger than 15 MB.',
      }),
      makeItem({
        id: 'q-5',
        fileName: 'signed-out.jpg',
        state: 'needs_signin',
      }),
      makeItem({
        id: 'q-6',
        fileName: 'next-month.jpg',
        state: 'done',
        receiptId: 'rec-quota',
        outcome: 'created',
      }),
    ]);

    renderUploadPage();

    await waitFor(() =>
      expect(screen.getByText('landed.jpg')).toBeInTheDocument(),
    );
    expect(fake.queue.resumeCalls).toBe(1);

    expect(screen.getByText('stuck.jpg')).toBeInTheDocument();
    expect(
      screen.getByText('The server could not be reached.'),
    ).toBeInTheDocument();

    // Stage labels arrive with the first pipeline response.
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument());
    const viewLink = screen.getByRole('link', { name: 'View receipt' });
    expect(viewLink).toHaveAttribute('href', '/receipts/rec-created');

    const duplicateLink = screen.getByRole('link', {
      name: 'View existing receipt',
    });
    expect(duplicateLink).toHaveAttribute('href', '/receipts/rec-dup');

    expect(screen.getByText('Waiting for quota')).toBeInTheDocument();
    // Once in the page-level alert title, once on the item's badge.
    expect(screen.getAllByText('Sign in required')).toHaveLength(2);
    expect(
      screen.getByText('The file is larger than 15 MB.'),
    ).toBeInTheDocument();
  });

  it('validates a selection, enqueues only valid files, and lists rejected ones', async () => {
    renderUploadPage();

    const dropArea = screen.getByRole('region', {
      name: 'Receipt photo drop area',
    });
    fireEvent.drop(dropArea, {
      dataTransfer: {
        files: [
          makeFile('ok.jpg', 'image/jpeg'),
          makeFile('ok.png', 'image/png'),
          makeFile('notes.pdf', 'application/pdf'),
          makeFile('huge.jpg', 'image/jpeg', 16 * 1024 * 1024),
        ],
      },
    });

    await waitFor(() => expect(fake.queue.enqueueCalls).toHaveLength(1));
    expect(fake.queue.enqueueCalls[0]?.map((file) => file.name)).toEqual([
      'ok.jpg',
      'ok.png',
    ]);

    expect(
      screen.getByText('Only JPEG, PNG, and HEIC/HEIF photos are accepted.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('File exceeds the 15 MB size limit.'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('Failed')).toHaveLength(2);
    expect(screen.getAllByText('Waiting')).toHaveLength(2);
  });

  it('shows per-file upload progress with a static determinate bar', async () => {
    fake.queue.seed([
      makeItem({ id: 'q-1', fileName: 'sending.jpg', state: 'uploading' }),
    ]);

    renderUploadPage();
    await waitFor(() =>
      expect(screen.getByText('sending.jpg')).toBeInTheDocument(),
    );

    expect(screen.getByText('Uploading')).toBeInTheDocument();
    expect(
      screen.getByText('Sending photo to the server...'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();

    act(() => {
      fake.queue.emitProgress('q-1', 30, 100);
    });

    expect(
      screen.getByText('Sending photo to the server (30% of 100 B)...'),
    ).toBeInTheDocument();
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('aria-valuenow', '30');
    expect(bar).toHaveAttribute('aria-valuemax', '100');
    expect(bar).toHaveAttribute(
      'aria-label',
      'Upload progress for sending.jpg',
    );
    const filled = bar.firstElementChild;
    expect(filled).not.toBeNull();
    expect(filled).toHaveStyle({ width: '30%' });
  });

  it('polls pipeline stages every 2 seconds and stops once settled', async () => {
    vi.useFakeTimers();
    fake.queue.seed([
      makeItem({
        id: 'q-1',
        fileName: 'polling.jpg',
        state: 'done',
        receiptId: 'rec-a',
        outcome: 'created',
      }),
    ]);

    let pollCount = 0;
    server.use(
      http.get('/api/v1/pipeline', ({ request }) => {
        expect(new URL(request.url).searchParams.get('ids')).toBe('rec-a');
        pollCount += 1;
        if (pollCount === 1) {
          return HttpResponse.json({
            items: [pipelineItem('rec-a', 'queued', 2)],
          });
        }
        if (pollCount === 2) {
          return HttpResponse.json({
            items: [pipelineItem('rec-a', 'extracting', null)],
          });
        }
        return HttpResponse.json({
          items: [pipelineItem('rec-a', 'ready', null)],
        });
      }),
    );

    renderUploadPage();
    // The 100ms async advance lets the first pipeline fetch resolve and
    // commit, the same cadence the old list-polling test used.
    await vi.advanceTimersByTimeAsync(100);

    expect(pollCount).toBe(1);
    expect(screen.getByText('Queued')).toBeInTheDocument();
    expect(
      screen.getByText('Position 2 in the extraction queue.'),
    ).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(2000);
    expect(pollCount).toBe(2);
    expect(screen.getByText('Extracting')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(2000);
    expect(pollCount).toBe(3);
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View receipt' })).toHaveAttribute(
      'href',
      '/receipts/rec-a',
    );

    // No further polling once every tracked receipt has settled.
    await vi.advanceTimersByTimeAsync(10000);
    expect(pollCount).toBe(3);
  });

  it('stops polling when a created receipt is missing from the pipeline response', async () => {
    vi.useFakeTimers();
    fake.queue.seed([
      makeItem({
        id: 'q-1',
        fileName: 'vanished.jpg',
        state: 'done',
        receiptId: 'rec-gone',
        outcome: 'created',
      }),
    ]);

    let pollCount = 0;
    server.use(
      http.get('/api/v1/pipeline', ({ request }) => {
        expect(new URL(request.url).searchParams.get('ids')).toBe('rec-gone');
        pollCount += 1;
        return HttpResponse.json({ items: [] });
      }),
    );

    renderUploadPage();
    await vi.advanceTimersByTimeAsync(100);
    expect(pollCount).toBe(1);
    expect(screen.getByText('vanished.jpg')).toBeInTheDocument();

    // A receipt that cannot be found must not hold the poller open forever.
    await vi.advanceTimersByTimeAsync(10000);
    expect(pollCount).toBe(1);
  });

  it('tracks at most the most recent hundred receipts because the endpoint caps ids', async () => {
    const seeded = Array.from({ length: 101 }, (_, index) =>
      makeItem({
        id: `q-${index + 1}`,
        fileName: `receipt-${index}.jpg`,
        createdAt: index,
        state: 'done',
        receiptId: `rec-${index}`,
        outcome: 'created',
      }),
    );
    fake.queue.seed(seeded);

    let receivedIds: string[] = [];
    server.use(
      http.get('/api/v1/pipeline', ({ request }) => {
        const raw = new URL(request.url).searchParams.get('ids') ?? '';
        receivedIds = raw.split(',').filter(Boolean);
        return HttpResponse.json({ items: [] });
      }),
    );

    renderUploadPage();

    await waitFor(() => expect(receivedIds).toHaveLength(100));
    expect(receivedIds[0]).toBe('rec-1');
    expect(receivedIds[99]).toBe('rec-100');
  });

  it('explains when uploads wait for a fresh sign-in', async () => {
    fake.queue.seed([
      makeItem({
        id: 'q-1',
        fileName: 'signed-out.jpg',
        state: 'needs_signin',
      }),
    ]);

    renderUploadPage();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Sign in required');
    expect(alert).toHaveTextContent(
      'Your session expired. Sign in again; queued photos resume when you return to this page.',
    );
    expect(screen.getByText('signed-out.jpg')).toBeInTheDocument();
  });

  it('clears settled items and rejected selections, keeping pending work', async () => {
    fake.queue.seed([
      makeItem({
        id: 'q-1',
        fileName: 'done.jpg',
        state: 'done',
        receiptId: 'rec-1',
        outcome: 'created',
      }),
      makeItem({
        id: 'q-2',
        fileName: 'broken.jpg',
        state: 'failed',
        error: 'The photo could not be uploaded.',
      }),
      makeItem({ id: 'q-3', fileName: 'waiting.jpg', state: 'pending' }),
    ]);

    renderUploadPage();
    fireEvent.drop(
      screen.getByRole('region', { name: 'Receipt photo drop area' }),
      {
        dataTransfer: { files: [makeFile('notes.pdf', 'application/pdf')] },
      },
    );

    await waitFor(() =>
      expect(
        screen.getByText('Only JPEG, PNG, and HEIC/HEIF photos are accepted.'),
      ).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Clear list' }));

    await waitFor(() => expect(fake.queue.clearSettledCalls).toBe(1));
    await waitFor(() =>
      expect(screen.getByText('waiting.jpg')).toBeInTheDocument(),
    );
    expect(screen.queryByText('done.jpg')).not.toBeInTheDocument();
    expect(screen.queryByText('broken.jpg')).not.toBeInTheDocument();
    expect(screen.queryByText('notes.pdf')).not.toBeInTheDocument();
  });

  it('disables the drop zone, file input and select button while a batch is pending', async () => {
    fake.queue.seed([makeItem({ id: 'q-1', state: 'pending' })]);

    renderUploadPage();

    await waitFor(() =>
      expect(screen.getByText('Waiting')).toBeInTheDocument(),
    );

    expect(screen.getByLabelText('Choose receipt photos')).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Select photos' }),
    ).toBeDisabled();
    expect(
      screen.getByRole('region', { name: 'Receipt photo drop area' }),
    ).toHaveAttribute('aria-disabled', 'true');
  });

  it('ignores a selection made while a batch is in flight', async () => {
    fake.queue.seed([makeItem({ id: 'q-1', state: 'uploading' })]);

    renderUploadPage();

    await waitFor(() =>
      expect(screen.getByText('Uploading')).toBeInTheDocument(),
    );

    fireEvent.drop(
      screen.getByRole('region', { name: 'Receipt photo drop area' }),
      { dataTransfer: { files: [makeFile('second.jpg', 'image/jpeg')] } },
    );

    expect(fake.queue.enqueueCalls).toHaveLength(0);
  });

  it('rejects a selection larger than the batch limit without enqueuing', async () => {
    renderUploadPage();

    const tooMany = Array.from({ length: 21 }, (_, index) =>
      makeFile(`bulk-${index}.jpg`, 'image/jpeg'),
    );
    fireEvent.drop(
      screen.getByRole('region', { name: 'Receipt photo drop area' }),
      { dataTransfer: { files: tooMany } },
    );

    expect(
      screen.getByText(
        'You can upload at most 20 files in one selection (21 chosen).',
      ),
    ).toBeInTheDocument();
    expect(fake.queue.enqueueCalls).toHaveLength(0);
    expect(screen.getByText('No files uploaded yet')).toBeInTheDocument();
  });
});
