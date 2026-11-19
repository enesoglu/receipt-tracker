import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import {
  MemoryUploadStore,
  UploadQueue,
  isSettled,
  type UploadItem,
  type UploadSender,
  type UploadStore,
} from './upload-queue';

type ScriptStep =
  | { kind: 'ok'; outcome: 'created' | 'duplicate'; receiptId: string }
  | { kind: 'rejected'; message: string }
  | { kind: 'error'; status: number };

/** A sender that answers scripted steps, recording the files it saw. */
function scriptedSender(script: readonly ScriptStep[]): {
  sender: UploadSender;
  sent: File[];
} {
  let calls = 0;
  const sent: File[] = [];
  const sender: UploadSender = async (file) => {
    sent.push(file);
    const step = script[Math.min(calls, script.length - 1)];
    calls += 1;
    if (step.kind === 'ok') {
      return {
        outcome: step.outcome,
        receiptId: step.receiptId,
        error: null,
      };
    }
    if (step.kind === 'rejected') {
      return {
        outcome: 'rejected',
        receiptId: null,
        error: { code: 'too_large', message: step.message },
      };
    }
    throw new ApiError({
      type: 'about:blank',
      title: `Scripted failure ${step.status}.`,
      status: step.status,
    });
  };
  return { sender, sent };
}

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

describe('upload queue', () => {
  it('stores each file as pending before sending, then completes it', async () => {
    const backing = new MemoryUploadStore();
    const putStates: string[] = [];
    const store: UploadStore = {
      list: () => backing.list(),
      put: async (item: UploadItem) => {
        putStates.push(item.state);
        await backing.put(item);
      },
      remove: (id: string) => backing.remove(id),
    };
    const { sender } = scriptedSender([
      { kind: 'ok', outcome: 'created', receiptId: 'rec-1' },
    ]);
    const queue = new UploadQueue(store, sender);

    await queue.enqueue([photo('a.jpg')]);

    // The pending write precedes every send; uploading and done follow.
    expect(putStates).toEqual(['pending', 'uploading', 'done']);
    const items = await queue.list();
    expect(items[0]).toMatchObject({
      state: 'done',
      receiptId: 'rec-1',
      outcome: 'created',
      error: null,
    });
  });

  it('sends files one per request in selection order', async () => {
    const { sender, sent } = scriptedSender([
      { kind: 'ok', outcome: 'created', receiptId: 'rec-1' },
      { kind: 'ok', outcome: 'created', receiptId: 'rec-2' },
      { kind: 'ok', outcome: 'created', receiptId: 'rec-3' },
    ]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('a.jpg'), photo('b.jpg'), photo('c.jpg')]);

    expect(sent.map((file) => file.name)).toEqual(['a.jpg', 'b.jpg', 'c.jpg']);
  });

  it('completes a duplicate response with the existing receipt id', async () => {
    const { sender } = scriptedSender([
      { kind: 'ok', outcome: 'duplicate', receiptId: 'rec-existing' },
    ]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('a.jpg')]);

    const items = await queue.list();
    expect(items[0]).toMatchObject({
      state: 'done',
      receiptId: 'rec-existing',
      outcome: 'duplicate',
      error: null,
    });
  });

  it('marks a rejected file failed without touching the rest', async () => {
    const { sender } = scriptedSender([
      { kind: 'rejected', message: 'The file is larger than 15 MB.' },
      { kind: 'ok', outcome: 'created', receiptId: 'rec-2' },
    ]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('large.jpg'), photo('fine.jpg')]);

    const items = await queue.list();
    expect(items).toMatchObject([
      {
        fileName: 'large.jpg',
        state: 'failed',
        error: 'The file is larger than 15 MB.',
      },
      { fileName: 'fine.jpg', state: 'done', receiptId: 'rec-2' },
    ]);
  });

  it('stops the whole drain when the session expired', async () => {
    const { sender, sent } = scriptedSender([{ kind: 'error', status: 401 }]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('a.jpg'), photo('b.jpg')]);

    const items = await queue.list();
    expect(items[0]).toMatchObject({ state: 'needs_signin' });
    expect(items[1]).toMatchObject({ state: 'pending' });
    // The second file was never attempted.
    expect(sent).toHaveLength(1);
  });

  it('marks one file failed on a 4xx answer and still sends the next', async () => {
    const { sender, sent } = scriptedSender([
      { kind: 'error', status: 413 },
      { kind: 'ok', outcome: 'created', receiptId: 'rec-2' },
    ]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('a.jpg'), photo('b.jpg')]);

    const items = await queue.list();
    expect(items[0]).toMatchObject({ state: 'failed' });
    expect(items[1]).toMatchObject({ state: 'done', receiptId: 'rec-2' });
    expect(sent).toHaveLength(2);
  });

  it('keeps a network failure pending with its message and finishes it later', async () => {
    const { sender, sent } = scriptedSender([
      { kind: 'error', status: 0 },
      { kind: 'ok', outcome: 'created', receiptId: 'rec-1' },
    ]);
    const queue = new UploadQueue(new MemoryUploadStore(), sender);

    await queue.enqueue([photo('a.jpg')]);

    let items = await queue.list();
    expect(items[0]).toMatchObject({
      state: 'pending',
      error: 'Scripted failure 0.',
    });

    // The retry that a later open, an online event or a sync would run.
    await queue.drain();

    items = await queue.list();
    expect(items[0]).toMatchObject({ state: 'done', receiptId: 'rec-1' });
    expect(sent).toHaveLength(2);
  });

  it('sends a file even when the store refuses to keep it', async () => {
    const refusing: UploadStore = {
      list: () => Promise.resolve([]),
      put: () =>
        Promise.reject(
          new DOMException('Quota exceeded', 'QuotaExceededError'),
        ),
      remove: () => Promise.resolve(),
    };
    const { sender } = scriptedSender([
      { kind: 'ok', outcome: 'created', receiptId: 'rec-1' },
    ]);
    const queue = new UploadQueue(refusing, sender);

    await queue.enqueue([photo('a.jpg')]);

    const items = await queue.list();
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ state: 'done', receiptId: 'rec-1' });
  });

  it('clearSettled keeps pending work and drops the rest', async () => {
    const store = new MemoryUploadStore();
    await store.put(
      seededItem({ id: 'done-1', state: 'done', receiptId: 'rec-1' }),
    );
    await store.put(
      seededItem({ id: 'failed-1', state: 'failed', error: 'no' }),
    );
    await store.put(seededItem({ id: 'pending-1', state: 'pending' }));
    const queue = new UploadQueue(store, () =>
      Promise.reject(new Error('never called')),
    );

    await queue.clearSettled();

    const items = await queue.list();
    expect(items).toHaveLength(1);
    expect(items[0]?.id).toBe('pending-1');
  });

  it('resume retries an interrupted upload and prunes week-old settled items', async () => {
    const week = 7 * 24 * 60 * 60 * 1000;
    const now = 1_000_000_000_000;
    const store = new MemoryUploadStore();
    await store.put(
      seededItem({ id: 'stuck', state: 'uploading', createdAt: now - 2000 }),
    );
    await store.put(
      seededItem({
        id: 'blocked',
        state: 'needs_signin',
        createdAt: now - 1000,
      }),
    );
    await store.put(
      seededItem({
        id: 'ancient',
        state: 'done',
        receiptId: 'rec-old',
        createdAt: now - week - 1,
      }),
    );
    await store.put(
      seededItem({
        id: 'fresh',
        state: 'done',
        receiptId: 'rec-new',
        createdAt: now - week + 1,
      }),
    );
    const { sender, sent } = scriptedSender([
      { kind: 'ok', outcome: 'created', receiptId: 'rec-stuck' },
      { kind: 'error', status: 401 },
    ]);
    const queue = new UploadQueue(store, sender);

    await queue.resume(now);

    const items = await queue.list();
    expect(items).toHaveLength(3);
    expect(items.find((item) => item.id === 'stuck')).toMatchObject({
      state: 'done',
      receiptId: 'rec-stuck',
    });
    // Still signed out: the blocked item gets its probe and lands back.
    expect(items.find((item) => item.id === 'blocked')).toMatchObject({
      state: 'needs_signin',
    });
    expect(items.find((item) => item.id === 'fresh')).toMatchObject({
      state: 'done',
    });
    expect(sent).toHaveLength(2);
  });

  it('tells its subscriber about changes and progress', async () => {
    const changed = vi.fn();
    const progress = vi.fn();
    const { sender } = scriptedSender([
      { kind: 'ok', outcome: 'created', receiptId: 'rec-1' },
    ]);
    const queue = new UploadQueue(
      new MemoryUploadStore(),
      async (file, options) => {
        options.onProgress?.(2, 3);
        return sender(file, options);
      },
    );
    queue.subscribe({ changed, progress });

    await queue.enqueue([photo('a.jpg')]);

    expect(changed).toHaveBeenCalled();
    expect(progress).toHaveBeenCalledWith(expect.any(String), 2, 3);
  });

  it('recognizes settled items', () => {
    expect(isSettled(seededItem({ state: 'done' }))).toBe(true);
    expect(isSettled(seededItem({ state: 'failed' }))).toBe(true);
    expect(isSettled(seededItem({ state: 'pending' }))).toBe(false);
    expect(isSettled(seededItem({ state: 'needs_signin' }))).toBe(false);
  });
});
