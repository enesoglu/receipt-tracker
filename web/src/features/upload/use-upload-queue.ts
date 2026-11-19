import { useCallback, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { queryKeys } from '../../api/query-keys';
import {
  getUploadQueue,
  type UploadItem,
  type UploadQueue,
} from '../../api/upload-queue';
import { requestUploadSync } from '../../sw/upload-sync';

/** Bytes sent of one file, for the progress bar while it uploads. */
export interface UploadProgress {
  uploaded: number;
  total: number;
}

export interface UseUploadQueueResult {
  /** Every item in the queue, oldest first, settled ones included. */
  items: UploadItem[];
  /** Upload progress by item id; in memory only, never persisted. */
  progress: ReadonlyMap<string, UploadProgress>;
  /** True once the store is open and the queue has resumed. */
  ready: boolean;
  /** Adds files as pending items and starts sending them. */
  enqueue: (files: readonly File[]) => Promise<void>;
  /** Drops settled items from the queue; pending work always stays. */
  clearSettled: () => Promise<void>;
}

/** Sorts items the way the upload screen shows them: oldest first. */
function oldestFirst(items: readonly UploadItem[]): UploadItem[] {
  return [...items].sort(
    (a, b) => a.createdAt - b.createdAt || (a.id < b.id ? -1 : 1),
  );
}

/**
 * React binding for the durable upload queue: it opens (and resumes) the
 * queue on mount, mirrors the queue into state on every change, and
 * invalidates the receipt caches once the queue lands new receipts. The
 * queue itself lives in `api/` so the service worker can share it without
 * React.
 */
export function useUploadQueue(
  getQueue: () => Promise<UploadQueue> = getUploadQueue,
): UseUploadQueueResult {
  const queryClient = useQueryClient();
  const [items, setItems] = useState<UploadItem[]>([]);
  const [progress, setProgress] = useState<ReadonlyMap<string, UploadProgress>>(
    new Map(),
  );
  const [ready, setReady] = useState(false);

  const queueRef = useRef<UploadQueue | null>(null);
  const getQueueRef = useRef(getQueue);
  useEffect(() => {
    getQueueRef.current = getQueue;
  }, [getQueue]);

  // Receipt ids already known to be done when the queue was last observed;
  // a receipt that lands between two observations refreshes the caches.
  const doneIdsRef = useRef<ReadonlySet<string> | null>(null);

  const refresh = useCallback(
    async (queue: UploadQueue) => {
      const list = oldestFirst(await queue.list());
      setItems(list);
      const doneIds = new Set<string>();
      for (const item of list) {
        if (item.state === 'done' && item.receiptId) {
          doneIds.add(item.receiptId);
        }
      }
      if (doneIdsRef.current === null) {
        doneIdsRef.current = doneIds;
        return;
      }
      const landed = [...doneIds].some((id) => !doneIdsRef.current?.has(id));
      doneIdsRef.current = doneIds;
      if (landed) {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.receipts.all,
        });
        void queryClient.invalidateQueries({
          queryKey: queryKeys.settings.usage,
        });
      }
    },
    [queryClient],
  );

  useEffect(() => {
    let active = true;
    let unsubscribe: (() => void) | null = null;
    let stopOnlineRetry: (() => void) | null = null;
    void getQueueRef.current().then((queue) => {
      if (!active) return;
      queueRef.current = queue;
      unsubscribe = queue.subscribe({
        changed: () => void refresh(queue),
        progress: (id, uploaded, total) => {
          setProgress((current) => {
            const next = new Map(current);
            next.set(id, { uploaded, total });
            return next;
          });
        },
      });
      // The browser reports connectivity is back: try the pending files
      // again now instead of waiting for the next app open.
      const handleOnline = () => {
        void queue.resume();
      };
      window.addEventListener('online', handleOnline);
      stopOnlineRetry = () =>
        window.removeEventListener('online', handleOnline);
      // Resume-on-open: interrupted attempts are retried, week-old settled
      // items are pruned, and whatever is pending is sent.
      void queue.resume().then(async () => {
        if (!active) return;
        setReady(true);
        await refresh(queue);
        const unfinished = (await queue.list()).some(
          (item) => item.state === 'pending',
        );
        if (unfinished) {
          // The drain could not finish: the service worker keeps trying.
          requestUploadSync();
        }
      });
    });
    return () => {
      active = false;
      unsubscribe?.();
      stopOnlineRetry?.();
      queueRef.current = null;
    };
  }, [refresh]);

  const enqueue = useCallback(async (files: readonly File[]) => {
    const queue = queueRef.current ?? (await getQueueRef.current());
    await queue.enqueue(files);
    // New work: ask the browser to keep finishing it in the background.
    requestUploadSync();
  }, []);

  const clearSettled = useCallback(async () => {
    const queue = queueRef.current;
    if (!queue) return;
    await queue.clearSettled();
    setProgress(new Map());
    await refresh(queue);
  }, [refresh]);

  return { items, progress, ready, enqueue, clearSettled };
}
