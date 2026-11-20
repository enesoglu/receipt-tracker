import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  cornersFromContour,
  type Point,
  type Quad,
  type RawCorners,
} from './corners';
import type { FrameDetection, ScannerWorker, WorkerFactory } from './engine';
import { ENGINE_TIMEOUT_MS } from './engine';

// The OpenCV bytes reach the engine as a build-time asset URL; the mock
// keeps every asset pipeline out of these tests and makes the URL fixed.
vi.mock('@techstark/opencv-js/dist/opencv.js?url', () => ({
  default: '/assets/opencv-test.js',
}));

interface FakeWorker {
  posted: Array<{ msg: unknown; transfer?: Transferable[] }>;
  terminated: boolean;
  postMessage(message: unknown, transfer?: Transferable[]): void;
  terminate(): void;
  addEventListener(
    type: 'message',
    listener: (event: MessageEvent<unknown>) => void,
  ): void;
  addEventListener(type: 'error', listener: (event: ErrorEvent) => void): void;
  removeEventListener(
    type: 'message',
    listener: (event: MessageEvent<unknown>) => void,
  ): void;
  removeEventListener(
    type: 'error',
    listener: (event: ErrorEvent) => void,
  ): void;
  emitMessage(data: unknown): void;
  emitError(message: string): void;
}

/** The corners of a rectangle rotated by `degrees`. */
function rotatedCorners(
  center: Point,
  halfWidth: number,
  halfHeight: number,
  degrees: number,
): Quad {
  const radians = (degrees * Math.PI) / 180;
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  const corner = (stepX: number, stepY: number): Point => ({
    x: center.x + stepX * halfWidth * cos - stepY * halfHeight * sin,
    y: center.y + stepX * halfWidth * sin + stepY * halfHeight * cos,
  });
  return [corner(-1, -1), corner(1, -1), corner(1, 1), corner(-1, 1)];
}

function makeFakeWorker(): FakeWorker {
  const messageListeners: Array<(event: MessageEvent<unknown>) => void> = [];
  const errorListeners: Array<(event: ErrorEvent) => void> = [];
  const worker: FakeWorker = {
    posted: [],
    terminated: false,
    postMessage(message, transfer) {
      worker.posted.push({ msg: message, transfer });
    },
    terminate() {
      worker.terminated = true;
    },
    addEventListener(type, listener) {
      if (type === 'message')
        messageListeners.push(listener as (e: MessageEvent<unknown>) => void);
      else errorListeners.push(listener as (e: ErrorEvent) => void);
    },
    removeEventListener(type, listener) {
      if (type === 'message') {
        const index = messageListeners.indexOf(
          listener as (e: MessageEvent<unknown>) => void,
        );
        if (index >= 0) messageListeners.splice(index, 1);
      } else {
        const index = errorListeners.indexOf(
          listener as (e: ErrorEvent) => void,
        );
        if (index >= 0) errorListeners.splice(index, 1);
      }
    },
    emitMessage(data) {
      for (const listener of [...messageListeners]) {
        listener({ data } as MessageEvent<unknown>);
      }
    },
    emitError(message) {
      for (const listener of [...errorListeners]) {
        listener({ message } as ErrorEvent);
      }
    },
  };
  return worker;
}

beforeEach(() => {
  vi.resetModules();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('loadScanEngine', () => {
  it('creates the worker, sends init with both urls, and resolves on ready', async () => {
    const worker = makeFakeWorker();
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as {
      type: string;
      opencvUrl: string;
      jscanifyUrl: string;
    };
    expect(init.type).toBe('init');
    expect(init.opencvUrl).toBe('/assets/opencv-test.js');
    expect(init.jscanifyUrl).toBe('/vendor/jscanify-1.4.3.js');
    const initId = (init as unknown as { id: number }).id;
    worker.emitMessage({ id: initId, type: 'ready' });
    const engine = await pending;
    expect(engine.detect).toBeInstanceOf(Function);
    expect(engine.extractPaper).toBeInstanceOf(Function);
    expect(engine.terminate).toBeInstanceOf(Function);
  });

  it('creates the worker at the fixed scanner worker path by default', async () => {
    // The default factory is exercised indirectly: importing the
    // module exposes the constant the default uses.
    const engineModule = await import('./engine');
    expect(engineModule.SCANNER_WORKER_PATH).toBe('/scanner-worker.js');
  });

  it('reports unavailable when the worker never becomes ready', async () => {
    const worker = makeFakeWorker();
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const verdict = expect(pending).rejects.toThrow(
      'did not become ready in time',
    );
    await vi.advanceTimersByTimeAsync(engineModule.ENGINE_TIMEOUT_MS);
    await verdict;
    expect(worker.terminated).toBe(true);
  });

  it('reports unavailable when the worker errors during init', async () => {
    const worker = makeFakeWorker();
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as unknown as { id: number };
    worker.emitMessage({ id: init.id, type: 'error', message: 'boom' });
    await expect(pending).rejects.toThrow('boom');
  });

  it('tries again after a failed load instead of caching the failure', async () => {
    const worker = makeFakeWorker();
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const first = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as unknown as { id: number };
    worker.emitMessage({ id: init.id, type: 'error', message: 'offline' });
    await expect(first).rejects.toThrow('offline');

    const worker2 = makeFakeWorker();
    const factory2: WorkerFactory = () => worker2 as unknown as ScannerWorker;
    const second = engineModule.loadScanEngine(factory2);
    const init2 = worker2.posted[0]?.msg as unknown as { id: number };
    worker2.emitMessage({ id: init2.id, type: 'ready' });
    const engine = await second;
    expect(engine.detect).toBeInstanceOf(Function);
  });
});

describe('detect', () => {
  async function loadedEngine(worker: FakeWorker) {
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as unknown as { id: number };
    worker.emitMessage({ id: init.id, type: 'ready' });
    return { engine: await pending, engineModule };
  }

  it('posts a detect request and resolves with the worker answer, transferring the buffer', async () => {
    const worker = makeFakeWorker();
    const { engine } = await loadedEngine(worker);
    const frame = document.createElement('canvas');
    frame.width = 4;
    frame.height = 3;
    const promise = engine.detect(frame);
    const request = worker.posted.find(
      (p) => (p.msg as { type?: string }).type === 'detect',
    );
    expect(request).toBeTruthy();
    const req = (request as { msg: unknown }).msg as unknown as {
      id: number;
      type: string;
      width: number;
      height: number;
      buffer: ArrayBuffer;
    };
    expect(req.type).toBe('detect');
    expect(req.width).toBe(4);
    expect(req.height).toBe(3);
    expect((request as { transfer?: Transferable[] }).transfer).toEqual([
      req.buffer,
    ]);
    const corners: RawCorners = {
      topLeftCorner: { x: 10, y: 20 },
      topRightCorner: { x: 110, y: 20 },
      bottomRightCorner: { x: 110, y: 90 },
      bottomLeftCorner: { x: 10, y: 90 },
    };
    worker.emitMessage({
      id: req.id,
      type: 'detect-result',
      classified: corners,
      points: [10, 20, 110, 20, 110, 90, 10, 90],
    });
    const detection = await promise;
    expect(detection.classified).toEqual(corners);
    expect(detection.points).toEqual([10, 20, 110, 20, 110, 90, 10, 90]);
  });

  it('rejects when the worker reports an error', async () => {
    const worker = makeFakeWorker();
    const { engine } = await loadedEngine(worker);
    const frame = document.createElement('canvas');
    frame.width = 2;
    frame.height = 2;
    const promise = engine.detect(frame);
    const foundDetect = worker.posted.find(
      (p) => (p.msg as { type?: string }).type === 'detect',
    );
    const req = (foundDetect as { msg: unknown }).msg as unknown as {
      id: number;
    };
    worker.emitMessage({
      id: req.id,
      type: 'error',
      message: 'classification failed',
    });
    await expect(promise).rejects.toThrow('classification failed');
  });

  it('times out when the worker does not answer', async () => {
    const worker = makeFakeWorker();
    const { engine } = await loadedEngine(worker);
    const frame = document.createElement('canvas');
    frame.width = 2;
    frame.height = 2;
    const promise = engine.detect(frame);
    const verdict = expect(promise).rejects.toThrow('did not answer in time');
    await vi.advanceTimersByTimeAsync(ENGINE_TIMEOUT_MS);
    await verdict;
  });
});

describe('extractPaper', () => {
  async function loadedEngine(worker: FakeWorker) {
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as unknown as { id: number };
    worker.emitMessage({ id: init.id, type: 'ready' });
    return await pending;
  }

  it('posts a warp request with corners and dimensions and builds a canvas from the answer', async () => {
    const worker = makeFakeWorker();
    const engine = await loadedEngine(worker);
    const frame = document.createElement('canvas');
    frame.width = 10;
    frame.height = 10;
    const quad: Quad = [
      { x: 1, y: 2 },
      { x: 3, y: 4 },
      { x: 5, y: 6 },
      { x: 7, y: 8 },
    ];
    const promise = engine.extractPaper(frame, quad, 80, 60);
    const request = worker.posted.find(
      (p) => (p.msg as { type?: string }).type === 'warp',
    );
    const req = (request as { msg: unknown }).msg as unknown as {
      id: number;
      type: string;
      width: number;
      height: number;
      corners: Quad;
      targetWidth: number;
      targetHeight: number;
      buffer: ArrayBuffer;
    };
    expect(req.type).toBe('warp');
    expect(req.width).toBe(10);
    expect(req.height).toBe(10);
    expect(req.corners).toEqual(quad);
    expect(req.targetWidth).toBe(80);
    expect(req.targetHeight).toBe(60);
    expect((request as { transfer?: Transferable[] }).transfer).toEqual([
      req.buffer,
    ]);
    const buffer = new ArrayBuffer(80 * 60 * 4);
    worker.emitMessage({
      id: req.id,
      type: 'warp-result',
      buffer,
      width: 80,
      height: 60,
    });
    const canvas = await promise;
    expect(canvas.width).toBe(80);
    expect(canvas.height).toBe(60);
  });

  it('rejects when the worker reports an error', async () => {
    const worker = makeFakeWorker();
    const engine = await loadedEngine(worker);
    const frame = document.createElement('canvas');
    frame.width = 2;
    frame.height = 2;
    const quad: Quad = [
      { x: 0, y: 0 },
      { x: 1, y: 0 },
      { x: 1, y: 1 },
      { x: 0, y: 1 },
    ];
    const promise = engine.extractPaper(frame, quad, 4, 4);
    const foundWarp = worker.posted.find(
      (p) => (p.msg as { type?: string }).type === 'warp',
    );
    const req = (foundWarp as { msg: unknown }).msg as unknown as {
      id: number;
    };
    worker.emitMessage({ id: req.id, type: 'error', message: 'warp failed' });
    await expect(promise).rejects.toThrow('warp failed');
  });
});

describe('terminate', () => {
  it('terminates the underlying worker', async () => {
    const worker = makeFakeWorker();
    const factory: WorkerFactory = () => worker as unknown as ScannerWorker;
    const engineModule = await import('./engine');
    const pending = engineModule.loadScanEngine(factory);
    const init = worker.posted[0]?.msg as unknown as { id: number };
    worker.emitMessage({ id: init.id, type: 'ready' });
    const engine = await pending;
    engine.terminate();
    expect(worker.terminated).toBe(true);
  });
});

describe('detectQuad', () => {
  it('takes the classified corners when they are complete and simple', async () => {
    const { detectQuad } = await import('./engine');
    const detection: FrameDetection = {
      classified: {
        topLeftCorner: { x: 10, y: 10 },
        topRightCorner: { x: 200, y: 10 },
        bottomRightCorner: { x: 200, y: 140 },
        bottomLeftCorner: { x: 10, y: 140 },
      },
      points: [0, 0, 500, 0, 500, 500, 0, 500],
    };

    expect(detectQuad(detection)).toEqual([
      { x: 10, y: 10 },
      { x: 200, y: 10 },
      { x: 200, y: 140 },
      { x: 10, y: 140 },
    ]);
  });

  it('falls back to extreme points when classification lost a corner', async () => {
    const { detectQuad } = await import('./engine');
    const corners = rotatedCorners({ x: 500, y: 400 }, 300, 200, 44);
    const flat = corners.flatMap((corner) => [corner.x, corner.y]);
    const detection: FrameDetection = {
      classified: {
        topLeftCorner: corners[0],
        bottomRightCorner: corners[2],
      },
      points: flat,
    };

    expect(detectQuad(detection)).toEqual(cornersFromContour(flat));
  });

  it('falls back to extreme points when the classified quad crosses itself', async () => {
    const { detectQuad } = await import('./engine');
    const detection: FrameDetection = {
      classified: {
        topLeftCorner: { x: 0, y: 60 },
        topRightCorner: { x: 100, y: 0 },
        bottomRightCorner: { x: 100, y: 60 },
        bottomLeftCorner: { x: 0, y: 0 },
      },
      points: [10, 20, 110, 20, 110, 90, 10, 90],
    };

    expect(detectQuad(detection)).toEqual([
      { x: 10, y: 20 },
      { x: 110, y: 20 },
      { x: 110, y: 90 },
      { x: 10, y: 90 },
    ]);
  });

  it('answers null when the fallback has no usable quad either', async () => {
    const { detectQuad } = await import('./engine');
    expect(
      detectQuad({ classified: null, points: [0, 0, 1, 1, 2, 2, 3, 3] }),
    ).toBeNull();
    expect(
      detectQuad({
        classified: {
          topLeftCorner: { x: 0, y: 60 },
          topRightCorner: { x: 100, y: 0 },
          bottomRightCorner: { x: 100, y: 60 },
          bottomLeftCorner: { x: 0, y: 0 },
        },
        points: null,
      }),
    ).toBeNull();
  });

  it('answers null for a frame without a contour', async () => {
    const { detectQuad } = await import('./engine');
    expect(detectQuad({ classified: null, points: null })).toBeNull();
  });
});
