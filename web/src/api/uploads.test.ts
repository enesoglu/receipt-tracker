import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import {
  uploadReceiptFile,
  uploadReceiptFileWithFetch,
  type UploadFileOutcome,
} from './uploads';

/**
 * Minimal XMLHttpRequest stand-in. The upload senders under test capture
 * one instance per request; the test drives it by hand.
 */
class FakeUploadXHR {
  static instances: FakeUploadXHR[] = [];

  upload: { onprogress: ((event: ProgressEvent) => void) | null } = {
    onprogress: null,
  };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  responseType = '';
  status = 0;
  response: unknown = null;
  sent: FormData | null = null;

  method = '';
  url = '';

  open(method: string, url: string): void {
    this.method = method;
    this.url = url;
  }

  send(form: FormData): void {
    this.sent = form;
    FakeUploadXHR.instances.push(this);
  }

  abort(): void {
    this.aborted = true;
  }

  aborted = false;
}

const syntheticFile = () =>
  new File([new Uint8Array([1, 2, 3])], 'receipt.jpg', {
    type: 'image/jpeg',
  });

function finishUpload(status: number, response: unknown): FakeUploadXHR {
  const request = FakeUploadXHR.instances.at(-1);
  if (!request) throw new Error('no request was sent');
  request.responseType = 'json';
  request.status = status;
  request.response = response;
  request.onload?.();
  return request;
}

const createdResponse = (receiptId: string, outcome: string) => ({
  results: [
    {
      index: 0,
      fileName: 'receipt.jpg',
      outcome,
      receiptId,
      error: null,
    },
  ],
});

const unauthorizedProblem = {
  type: 'about:blank',
  title: 'Authentication is required.',
  status: 401,
};

describe('single-file upload senders', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    FakeUploadXHR.instances = [];
  });

  it('sends one file per request and reports progress and the outcome', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeUploadXHR);
    const progress: Array<[number, number]> = [];
    const pending: Promise<UploadFileOutcome> = uploadReceiptFile(
      syntheticFile(),
      {
        onProgress: (uploaded, total) => progress.push([uploaded, total]),
      },
    );

    const request = FakeUploadXHR.instances[0];
    if (!request) throw new Error('no request was sent');
    request.upload.onprogress?.({ loaded: 2, total: 3 } as ProgressEvent);
    finishUpload(200, createdResponse('rec-1', 'created'));

    await expect(pending).resolves.toEqual({
      outcome: 'created',
      receiptId: 'rec-1',
      error: null,
    });
    expect(progress).toEqual([[2, 3]]);
    expect(request.method).toBe('POST');
    expect(request.url).toBe('/api/v1/receipts');
    expect(request.sent?.get('files')).toBeInstanceOf(File);
  });

  it('treats a duplicate response as an outcome with the existing id', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeUploadXHR);
    const pending = uploadReceiptFile(syntheticFile());
    finishUpload(200, createdResponse('rec-existing', 'duplicate'));

    await expect(pending).resolves.toEqual({
      outcome: 'duplicate',
      receiptId: 'rec-existing',
      error: null,
    });
  });

  it('rejects with an ApiError carrying the problem document', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeUploadXHR);
    const pending = uploadReceiptFile(syntheticFile());
    finishUpload(401, unauthorizedProblem);

    await expect(pending).rejects.toBeInstanceOf(ApiError);
    const error = (await pending.catch(
      (reason: unknown) => reason,
    )) as ApiError;
    expect(error.status).toBe(401);
    expect(error.problem.title).toBe('Authentication is required.');
  });

  it('rejects a network failure with a connection problem', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeUploadXHR);
    const pending = uploadReceiptFile(syntheticFile());
    FakeUploadXHR.instances[0]?.onerror?.();

    await expect(pending).rejects.toBeInstanceOf(ApiError);
    const error = (await pending.catch(
      (reason: unknown) => reason,
    )) as ApiError;
    expect(error.status).toBe(0);
  });

  it('uploads through fetch with the session credentials', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(createdResponse('rec-2', 'created')), {
        status: 200,
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const outcome = await uploadReceiptFileWithFetch(syntheticFile());
    expect(outcome).toEqual({
      outcome: 'created',
      receiptId: 'rec-2',
      error: null,
    });

    const [url, init] = fetchMock.mock.calls[0] as [
      string,
      RequestInit & { credentials?: RequestCredentials },
    ];
    expect(url).toBe('/api/v1/receipts');
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('same-origin');
    expect(init.body).toBeInstanceOf(FormData);
  });

  it('rejects a failed fetch upload with the problem document', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify(unauthorizedProblem), { status: 401 }),
      );
    vi.stubGlobal('fetch', fetchMock);

    const pending = uploadReceiptFileWithFetch(syntheticFile());
    await expect(pending).rejects.toBeInstanceOf(ApiError);
    const error = (await pending.catch(
      (reason: unknown) => reason,
    )) as ApiError;
    expect(error.status).toBe(401);
  });
});
