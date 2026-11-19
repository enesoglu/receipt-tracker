import {
  uploadReceiptFile,
  type UploadFileOptions,
  type UploadFileOutcome,
} from './uploads';
import { formatApiError } from './errors';

/**
 * The durable upload queue. Files chosen for upload are stored in
 * IndexedDB before anything is sent, so a closed tab, a lost connection
 * or a drained battery never loses a photo: whatever is pending resumes
 * on the next app open, or in the service worker where the browser
 * supports Background Sync. The stored blob is the original file; bytes
 * are never downscaled or recompressed here.
 */

/** Where one queued file stands. */
export type UploadItemState =
  'pending' | 'uploading' | 'done' | 'failed' | 'needs_signin';

/** One queued file. `receiptId` is set once the server has the file. */
export interface UploadItem {
  id: string;
  fileName: string;
  sizeBytes: number;
  /** The original photo, stored exactly as selected. */
  blob: File;
  /**
   * A scanned version of the same photo, reserved for the document
   * scanner that will feed this queue: stored and carried, sent by
   * nobody yet.
   */
  scannedBlob?: File;
  createdAt: number;
  state: UploadItemState;
  receiptId: string | null;
  /** How the server took the file once done: a fresh receipt or an upload of
   * bytes it already had. `null` until the file is done. */
  outcome: 'created' | 'duplicate' | null;
  error: string | null;
}

/** The persistence the queue needs; the in-memory store is the fake and the fallback. */
export interface UploadStore {
  list(): Promise<UploadItem[]>;
  put(item: UploadItem): Promise<void>;
  remove(id: string): Promise<void>;
}

const DB_NAME = 'receipt-tracker-uploads';
const STORE = 'items';
const DB_VERSION = 1;

function request<T>(run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open(DB_NAME, DB_VERSION);
    open.onupgradeneeded = () => {
      const db = open.result;
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: 'id' });
      }
    };
    open.onsuccess = () => {
      const db = open.result;
      const transaction = db.transaction(STORE, 'readwrite');
      const request_ = run(transaction.objectStore(STORE));
      transaction.oncomplete = () => {
        db.close();
        resolve(request_.result);
      };
      transaction.onabort = () => {
        db.close();
        reject(transaction.error ?? new Error('IndexedDB is unavailable'));
      };
      transaction.onerror = () => {
        db.close();
        reject(transaction.error ?? new Error('IndexedDB is unavailable'));
      };
    };
    open.onerror = () =>
      reject(open.error ?? new Error('IndexedDB is unavailable'));
    open.onblocked = () =>
      reject(new Error('IndexedDB is blocked by another tab'));
  });
}

/** Opens the IndexedDB-backed store. Rejects when IndexedDB is unusable. */
export function openUploadStore(): Promise<UploadStore> {
  // Prove the database can be opened before handing out a working store.
  return request((store) => store.count()).then(() => ({
    list: () => request((store) => store.getAll()) as Promise<UploadItem[]>,
    put: (item: UploadItem) =>
      request((store) => store.put(item)).then(() => undefined),
    remove: (id: string) =>
      request((store) => store.delete(id)).then(() => undefined),
  }));
}

/** The in-memory store: the fake for tests and the fallback in private mode. */
export class MemoryUploadStore implements UploadStore {
  private readonly items = new Map<string, UploadItem>();

  async list(): Promise<UploadItem[]> {
    return [...this.items.values()].map((item) => ({ ...item }));
  }

  async put(item: UploadItem): Promise<void> {
    this.items.set(item.id, { ...item });
  }

  async remove(id: string): Promise<void> {
    this.items.delete(id);
  }
}

/** Sends one file; the page uses the XHR sender, the worker the fetch sender. */
export type UploadSender = (
  file: File,
  options: UploadFileOptions,
) => Promise<UploadFileOutcome>;

/** What a view listens to. */
export interface UploadQueueEvents {
  /** Called after every change to the queue's contents. */
  changed?: () => void;
  /** Called with upload bytes of one file; progress is never persisted. */
  progress?: (id: string, uploaded: number, total: number) => void;
}

/** A settled item will not be tried again. */
export function isSettled(item: UploadItem): boolean {
  return item.state === 'done' || item.state === 'failed';
}

const PRUNE_AFTER_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * UploadQueue drains FIFO: one file per request, pending items first,
 * until nothing is left or an attempt fails. The server dedupes by
 * content hash, so a resumed or doubly-sent file never creates a second
 * receipt; the duplicate outcome completes the item with the existing
 * receipt id.
 */
export class UploadQueue {
  private draining = false;
  /** Items the store refused (a full disk): kept in memory, still sent. */
  private readonly unstored = new Map<string, UploadItem>();

  constructor(
    private readonly store: UploadStore,
    private sender: UploadSender,
    private events: UploadQueueEvents = {},
  ) {}

  /** Subscribes a view; the returned function unsubscribes it. */
  subscribe(events: UploadQueueEvents): () => void {
    const previous = this.events;
    this.events = events;
    return () => {
      this.events = previous;
    };
  }

  /** All items, stored and unstored. */
  async list(): Promise<UploadItem[]> {
    const stored = await this.store.list();
    return [...stored, ...this.unstored.values()].map((item) => ({ ...item }));
  }

  /** Stores the files as pending items and starts draining. */
  async enqueue(files: readonly File[]): Promise<void> {
    const now = Date.now();
    // The index keeps a batch's order deterministic even when one
    // millisecond covers every file.
    const items = files.map((file, index) => this.newItem(file, now + index));
    for (const item of items) {
      await this.put(item);
    }
    await this.drain();
  }

  private newItem(file: File, createdAt: number): UploadItem {
    return {
      id: crypto.randomUUID(),
      fileName: file.name,
      sizeBytes: file.size,
      blob: file,
      createdAt,
      state: 'pending',
      receiptId: null,
      outcome: null,
      error: null,
    };
  }

  /**
   * Prepares the queue after an app open: an attempt that was interrupted
   * is tried again, items blocked on a sign-in that may have returned are
   * given a fresh chance, settled items older than a week are pruned, and
   * whatever is pending is sent.
   */
  async resume(now = Date.now()): Promise<void> {
    for (const item of await this.store.list()) {
      if (item.state === 'uploading' || item.state === 'needs_signin') {
        await this.put({ ...item, state: 'pending', error: null });
      } else if (isSettled(item) && now - item.createdAt > PRUNE_AFTER_MS) {
        await this.store.remove(item.id);
      }
    }
    this.events.changed?.();
    await this.drain();
  }

  /** Removes settled items; pending work is never dropped. */
  async clearSettled(): Promise<void> {
    for (const item of await this.list()) {
      if (isSettled(item)) {
        await this.store.remove(item.id);
        this.unstored.delete(item.id);
      }
    }
    this.events.changed?.();
  }

  /** Sends pending files one by one, FIFO, until the queue is empty or an attempt fails. */
  async drain(): Promise<void> {
    if (this.draining) return;
    this.draining = true;
    try {
      for (;;) {
        const item = firstPending(await this.list());
        if (!item) return;
        await this.put({ ...item, state: 'uploading', error: null });
        try {
          const outcome = await this.sender(item.blob, {
            onProgress: (uploaded, total) =>
              this.events.progress?.(item.id, uploaded, total),
          });
          await this.put(this.settled(item, outcome));
        } catch (error) {
          const problem = formatApiError(
            error,
            'The photo could not be uploaded.',
          );
          if (problem.status === 401) {
            // The session expired: everything pending needs the user back.
            await this.put({
              ...item,
              state: 'needs_signin',
              error: problem.message,
            });
            return;
          }
          if (
            problem.status !== undefined &&
            problem.status >= 400 &&
            problem.status < 500
          ) {
            // A 4xx answer is about this file; the rest still get a turn.
            await this.put({
              ...item,
              state: 'failed',
              error: problem.message,
            });
            continue;
          }
          // A network or server hiccup: keep the file pending, retry later.
          await this.put({ ...item, state: 'pending', error: problem.message });
          return;
        }
      }
    } finally {
      this.draining = false;
    }
  }

  private settled(item: UploadItem, outcome: UploadFileOutcome): UploadItem {
    if (outcome.outcome === 'rejected') {
      return {
        ...item,
        state: 'failed',
        error: outcome.error?.message ?? 'The file was rejected by the server.',
        outcome: null,
      };
    }
    return {
      ...item,
      state: 'done',
      receiptId: outcome.receiptId,
      error: null,
      outcome: outcome.outcome,
    };
  }

  /** Persists a state change, or keeps the item in memory when the store refuses it. */
  private async put(item: UploadItem): Promise<void> {
    if (this.unstored.has(item.id)) {
      // This item lives only in memory, settled ones included: it shows
      // for the rest of the session, then it is gone with the page.
      this.unstored.set(item.id, { ...item });
    } else {
      try {
        await this.store.put(item);
      } catch {
        // A full disk must not block the upload: keep the item in memory,
        // visibly noting that it will not survive a reload.
        this.unstored.set(item.id, {
          ...item,
          error:
            item.state === 'pending'
              ? 'This device is out of storage: the photo will upload now but will not survive a reload.'
              : item.error,
        });
      }
    }
    this.events.changed?.();
  }
}

function firstPending(items: UploadItem[]): UploadItem | undefined {
  return [...items]
    .filter((item) => item.state === 'pending')
    .sort((a, b) => a.createdAt - b.createdAt || (a.id < b.id ? -1 : 1))[0];
}

let queueSingleton: Promise<UploadQueue> | null = null;

/**
 * The queue of this page, one instance per browser realm: IndexedDB when
 * it works, memory when it does not, always the XHR sender for progress.
 */
export function getUploadQueue(): Promise<UploadQueue> {
  queueSingleton ??= openUploadStore()
    .catch(() => new MemoryUploadStore())
    .then((store) => new UploadQueue(store, uploadReceiptFile));
  return queueSingleton;
}
