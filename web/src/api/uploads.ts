import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type UploadResponse = components['schemas']['UploadResponse'];
export type UploadResult = components['schemas']['UploadResult'];

/** Uploads up to 20 receipt photos as multipart/form-data. */
export function useUploadReceipts() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (files: readonly File[]) =>
      unwrap(
        api.POST('/receipts', {
          body: { files: files as unknown as string[] },
          bodySerializer: () => {
            const form = new FormData();
            for (const file of files) {
              form.append('files', file);
            }
            return form;
          },
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.settings.usage,
      });
    },
  });
}

/*
 * One file per request. The queue sends each file on its own so progress
 * and failure are per file; the batch mutation above is the fallback for
 * callers that do not track progress.
 */

/** The upload operation of the contract, with the versioned base path. */
const uploadUrl = '/api/v1/receipts';

/** What one uploaded file reports back: one entry of an UploadResponse. */
export interface UploadFileOutcome {
  outcome: components['schemas']['UploadOutcome'];
  receiptId: string | null;
  error: components['schemas']['UploadError'] | null;
}

/** Options for the single-file upload senders. */
export interface UploadFileOptions {
  /** Called with the bytes sent so far and the total bytes of this file. */
  onProgress?: (uploaded: number, total: number) => void;
  /** Aborts the upload; the promise rejects with an ApiError. */
  signal?: AbortSignal;
}

/** Reads the one result the server owes for one uploaded file. */
function singleResult(body: unknown): UploadFileOutcome {
  const results = (body as UploadResponse | null)?.results;
  const result = results?.[0];
  if (!result) {
    throw new ApiError({
      type: 'about:blank',
      title: 'The upload response named no result for this file.',
      status: 0,
    });
  }
  return {
    outcome: result.outcome,
    receiptId: result.receiptId ?? null,
    error: result.error ?? null,
  };
}

/** Turns a non-2xx response body into the one error shape the app handles. */
function uploadProblem(status: number, body: unknown): ApiError {
  const candidate = body as Partial<components['schemas']['Problem']> | null;
  if (
    candidate &&
    typeof candidate.status === 'number' &&
    typeof candidate.title === 'string' &&
    typeof candidate.type === 'string'
  ) {
    return new ApiError(candidate as components['schemas']['Problem']);
  }
  return new ApiError({
    type: 'about:blank',
    title:
      status > 0 ? 'The upload failed.' : 'The server could not be reached.',
    status,
  });
}

/**
 * Uploads one file with XMLHttpRequest — the only browser API whose upload
 * events report real progress. This is the one deliberate request outside
 * the generated client: it stays typed (same path, same `files` field,
 * same `UploadResponse` schema) and lives here in `api/` so no component
 * ever builds a request itself.
 */
export function uploadReceiptFile(
  file: File,
  options: UploadFileOptions = {},
): Promise<UploadFileOutcome> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append('files', file);

    const request = new XMLHttpRequest();
    request.open('POST', uploadUrl);
    request.responseType = 'json';
    request.upload.onprogress = (event) => {
      options.onProgress?.(event.loaded, event.total || file.size);
    };
    request.onload = () => {
      const body = request.responseType === 'json' ? request.response : null;
      if (request.status >= 200 && request.status < 300) {
        try {
          resolve(singleResult(body));
        } catch (error) {
          reject(error);
        }
        return;
      }
      reject(uploadProblem(request.status, body));
    };
    request.onerror = () => reject(uploadProblem(0, null));
    request.onabort = () =>
      reject(
        new ApiError({
          type: 'about:blank',
          title: 'The upload was canceled.',
          status: 0,
        }),
      );
    const signal = options.signal;
    if (signal) {
      if (signal.aborted) {
        request.abort();
      } else {
        signal.addEventListener('abort', () => request.abort(), {
          once: true,
        });
      }
    }
    request.send(form);
  });
}

/**
 * Uploads one file with fetch, for the service worker, which has no
 * XMLHttpRequest and no generated-client runtime. Same path, same field
 * and the same response parsing as the XHR sender; the credentials option
 * is spelled out because the worker's requests carry the session cookie.
 */
export async function uploadReceiptFileWithFetch(
  file: File,
  options: UploadFileOptions = {},
): Promise<UploadFileOutcome> {
  const form = new FormData();
  form.append('files', file);
  // The fetch ban exists to keep components on the generated client; this
  // api-layer sender is the deliberate exception the worker needs.
  // eslint-disable-next-line no-restricted-globals
  const response = await fetch(uploadUrl, {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    signal: options.signal,
  });
  const body: unknown = await response.json().catch(() => null as unknown);
  if (!response.ok) {
    throw uploadProblem(response.status, body);
  }
  return singleResult(body);
}
