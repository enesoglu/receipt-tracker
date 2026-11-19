import {
  getBackgroundUploadQueue,
  type UploadQueue,
} from '../api/upload-queue';
import { UPLOAD_SYNC_TAG } from './upload-sync';

/**
 * The service worker entry: it finishes uploads in the background,
 * through Background Sync where the browser supports it. Built on its
 * own (vite.sw.config.ts) into a classic /sw.js.
 */

/**
 * The worker scope, hand-declared because the project's type library
 * set is DOM; the worker build carries no webworker types of its own.
 */
interface UploadWorkerGlobal {
  addEventListener(
    type: 'sync',
    listener: (event: UploadSyncEvent) => void,
  ): void;
}

/** The Background Sync event, as far as this worker uses it. */
interface UploadSyncEvent {
  readonly tag: string;
  waitUntil(promise: Promise<unknown>): void;
}

declare const self: UploadWorkerGlobal;

/**
 * Drains the durable queue with the fetch sender. The worker and the
 * page may race the same item; the server's content-hash dedupe makes the
 * loser a duplicate with the same receipt id. An expired session answers
 * 401: the item is marked needs_signin and the drain stops — it never
 * loops.
 */
export async function drainUploads(
  getQueue: () => Promise<UploadQueue> = getBackgroundUploadQueue,
): Promise<void> {
  const queue = await getQueue();
  await queue.resume();
}

self.addEventListener('sync', (event) => {
  if (event.tag !== UPLOAD_SYNC_TAG) return;
  // Background Sync is best-effort: the browser may kill a long sync, so
  // the queue must always be able to finish on the next app open. The
  // waitUntil keeps this worker alive for the drain; if it rejects, the
  // browser retries the sync later.
  event.waitUntil(drainUploads());
});
