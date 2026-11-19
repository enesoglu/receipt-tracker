import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/client';
import {
  MemoryUploadStore,
  UploadQueue,
  type UploadItem,
  type UploadSender,
} from '../api/upload-queue';
import { drainUploads } from './sw';

const photo = (name: string) =>
  new File([new Uint8Array([1, 2, 3])], name, { type: 'image/jpeg' });

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

describe('drainUploads', () => {
  it('drains the queue FIFO until every file is done', async () => {
    const store = new MemoryUploadStore();
    await store.put(seededItem({ id: 'a', fileName: 'a.jpg', createdAt: 1 }));
    await store.put(seededItem({ id: 'b', fileName: 'b.jpg', createdAt: 2 }));
    const sends: string[] = [];
    const sender: UploadSender = async (file) => {
      sends.push(file.name);
      return {
        outcome: 'created',
        receiptId: `rec-${sends.length}`,
        error: null,
      };
    };
    const queue = new UploadQueue(store, sender);

    await drainUploads(() => Promise.resolve(queue));

    expect(sends).toEqual(['a.jpg', 'b.jpg']);
    expect(await queue.list()).toMatchObject([
      { id: 'a', state: 'done', receiptId: 'rec-1' },
      { id: 'b', state: 'done', receiptId: 'rec-2' },
    ]);
  });

  it('marks the item needs_signin on an expired session and stops', async () => {
    const store = new MemoryUploadStore();
    await store.put(seededItem({ id: 'a', fileName: 'a.jpg', createdAt: 1 }));
    await store.put(seededItem({ id: 'b', fileName: 'b.jpg', createdAt: 2 }));
    let attempts = 0;
    const sender: UploadSender = async () => {
      attempts += 1;
      throw new ApiError({
        type: 'about:blank',
        title: 'Authentication is required.',
        status: 401,
      });
    };
    const queue = new UploadQueue(store, sender);

    await drainUploads(() => Promise.resolve(queue));

    // One attempt, then the drain stops: the worker never loops on 401.
    expect(attempts).toBe(1);
    expect(await queue.list()).toMatchObject([
      { id: 'a', state: 'needs_signin' },
      // The rest keep their turn for the next signed-in drain.
      { id: 'b', state: 'pending' },
    ]);
  });

  it('keeps unfinished work pending for a later drain', async () => {
    const store = new MemoryUploadStore();
    await store.put(seededItem({ id: 'a', fileName: 'a.jpg', createdAt: 1 }));
    const failures: string[] = [];
    let failing = true;
    const sender: UploadSender = async (file) => {
      if (failing) {
        failures.push(file.name);
        throw new ApiError({
          type: 'about:blank',
          title: 'The server could not be reached.',
          status: 0,
        });
      }
      return { outcome: 'created', receiptId: 'rec-1', error: null };
    };
    const queue = new UploadQueue(store, sender);

    await drainUploads(() => Promise.resolve(queue));
    expect(failures).toEqual(['a.jpg']);
    expect(await queue.list()).toMatchObject([
      { id: 'a', state: 'pending', error: 'The server could not be reached.' },
    ]);

    // The next sync finishes what the first could not.
    failing = false;
    await drainUploads(() => Promise.resolve(queue));
    expect(await queue.list()).toMatchObject([
      { id: 'a', state: 'done', receiptId: 'rec-1' },
    ]);
  });
});
