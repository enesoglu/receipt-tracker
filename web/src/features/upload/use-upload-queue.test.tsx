import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import { createAppQueryClient } from '../../api/query-client';
import { queryKeys } from '../../api/query-keys';
import { QueryProvider } from '../../api/QueryProvider';
import {
  MemoryUploadStore,
  UploadQueue,
  type UploadItem,
  type UploadSender,
} from '../../api/upload-queue';
import { useUploadQueue } from './use-upload-queue';

const photo = (name: string) =>
  new File([new Uint8Array([1, 2, 3])], name, { type: 'image/jpeg' });

/** A sender that reports progress, then takes the file. */
const reportingSender: UploadSender = async (_file, options) => {
  options.onProgress?.(2, 3);
  return { outcome: 'created', receiptId: 'rec-1', error: null };
};

function createWrapper() {
  const client = createAppQueryClient({ staleTime: 0 });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryProvider client={client}>{children}</QueryProvider>;
  }
  return { client, Wrapper };
}

function seededItem(item: Partial<UploadItem>): UploadItem {
  return {
    id: item.id ?? crypto.randomUUID(),
    fileName: item.fileName ?? 'receipt.jpg',
    sizeBytes: item.sizeBytes ?? 3,
    blob: item.blob ?? photo(item.fileName ?? 'receipt.jpg'),
    createdAt: item.createdAt ?? 0,
    state: item.state ?? 'pending',
    receiptId: item.receiptId ?? null,
    outcome: item.outcome ?? null,
    error: item.error ?? null,
  };
}

describe('useUploadQueue', () => {
  it('mirrors queue changes, records progress, and sorts oldest first', async () => {
    const store = new MemoryUploadStore();
    await store.put(seededItem({ id: 'old', createdAt: 2 }));
    await store.put(seededItem({ id: 'new', createdAt: 1 }));
    const queue = new UploadQueue(store, reportingSender);
    const { Wrapper } = createWrapper();
    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );

    await waitFor(() => expect(result.current.ready).toBe(true));
    expect(result.current.items.map((item) => item.id)).toEqual(['new', 'old']);

    await act(async () => {
      await result.current.enqueue([photo('fresh.jpg')]);
    });

    const uploaded = result.current.items.find(
      (item) => item.fileName === 'fresh.jpg',
    );
    expect(uploaded).toMatchObject({ state: 'done', receiptId: 'rec-1' });
    expect(result.current.progress.get(uploaded?.id ?? '')).toEqual({
      uploaded: 2,
      total: 3,
    });
  });

  it('resumes the queue once on mount', async () => {
    const queue = new UploadQueue(new MemoryUploadStore(), reportingSender);
    const resumeSpy = vi.spyOn(queue, 'resume');
    const { Wrapper } = createWrapper();

    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.ready).toBe(true));

    expect(resumeSpy).toHaveBeenCalledTimes(1);
  });

  it('retries pending uploads when connectivity returns', async () => {
    const store = new MemoryUploadStore();
    await store.put(seededItem({ id: 'waiting', state: 'pending' }));
    const queue = new UploadQueue(store, reportingSender);
    const resumeSpy = vi.spyOn(queue, 'resume');
    const { Wrapper } = createWrapper();

    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.ready).toBe(true));
    expect(resumeSpy).toHaveBeenCalledTimes(1);

    await act(async () => {
      window.dispatchEvent(new Event('online'));
    });

    // The mount resume plus the connectivity-triggered retry.
    await waitFor(() => expect(resumeSpy).toHaveBeenCalledTimes(2));
  });

  it('invalidates receipt caches when a new receipt lands', async () => {
    const store = new MemoryUploadStore();
    const queue = new UploadQueue(store, reportingSender);
    const { client, Wrapper } = createWrapper();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');
    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.ready).toBe(true));
    // Observing the queue at mount must not count as a landed receipt.
    expect(invalidateSpy).not.toHaveBeenCalledWith({
      queryKey: queryKeys.receipts.all,
    });

    await act(async () => {
      await result.current.enqueue([photo('a.jpg')]);
    });

    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: queryKeys.receipts.all,
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: queryKeys.settings.usage,
    });
  });

  it('keeps a failed session marker without invalidating caches', async () => {
    const store = new MemoryUploadStore();
    await store.put(
      seededItem({ id: 'blocked', state: 'needs_signin', error: 'expired' }),
    );
    const queue = new UploadQueue(store, () => {
      throw new ApiError({
        type: 'about:blank',
        title: 'Authentication is required.',
        status: 401,
      });
    });
    const { client, Wrapper } = createWrapper();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');
    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );

    await waitFor(() =>
      expect(
        result.current.items.some((item) => item.state === 'needs_signin'),
      ).toBe(true),
    );
    expect(invalidateSpy).not.toHaveBeenCalledWith({
      queryKey: queryKeys.receipts.all,
    });
  });

  it('clearSettled removes settled items from the mirrored list', async () => {
    const store = new MemoryUploadStore();
    await store.put(
      seededItem({ id: 'done', state: 'done', receiptId: 'rec-1' }),
    );
    await store.put(seededItem({ id: 'failed', state: 'failed' }));
    await store.put(seededItem({ id: 'waiting', state: 'pending' }));
    const queue = new UploadQueue(store, () => {
      throw new Error('never called');
    });
    const { Wrapper } = createWrapper();
    const { result } = renderHook(
      () => useUploadQueue(() => Promise.resolve(queue)),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.ready).toBe(true));

    await act(async () => {
      await result.current.clearSettled();
    });

    expect(result.current.items.map((item) => item.id)).toEqual(['waiting']);
  });
});
