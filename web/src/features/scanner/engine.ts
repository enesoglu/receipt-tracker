/**
 * The scanning engine loader: OpenCV.js plus the vendored jscanify
 * client run inside a dedicated worker, so the app's strict document
 * CSP never has to admit 'unsafe-eval'. The worker is created only when
 * the scanner opens and terminated when it closes; its script is served
 * from one fixed path whose response carries the worker's own CSP.
 *
 * The worker receives the OpenCV bytes as a Vite-emitted hashed asset
 * URL and the jscanify client URL, loads both with importScripts, and
 * answers detect and warp requests over a small typed protocol.
 */
import opencvUrl from '@techstark/opencv-js/dist/opencv.js?url';
import {
  cornersFromContour,
  isSimpleQuad,
  orderCorners,
  type Quad,
  type RawCorners,
} from './corners';

/** The vendored jscanify client source, served verbatim from /vendor. */
const JSCANIFY_URL = '/vendor/jscanify-1.4.3.js';

/** The fixed path the dedicated worker script is served from. */
export const SCANNER_WORKER_PATH = '/scanner-worker.js';

/**
 * The whole load must answer within this budget or the scanner reports
 * the engine unavailable: twice the 9.3 s cold load measured in the
 * spike, still seconds rather than the minutes a wedged load hangs for.
 */
export const ENGINE_TIMEOUT_MS = 20_000;

/** The narrow slice of a worker the engine drives. */
export interface ScannerWorker {
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
}

/** Creates the worker; tests pass a fake. */
export type WorkerFactory = () => ScannerWorker;

/**
 * What one detection pass over a frame produced, before the pure
 * corner policy picks a quad: the contour's points, and jscanify's
 * classification of them. Both null when the frame held no paper
 * contour at all.
 */
export interface FrameDetection {
  classified: RawCorners | null;
  points: readonly number[] | null;
}

/** The loaded engine, as narrow as the scanner needs it. */
export interface ScanEngine {
  /** Finds the paper of one frame; runs in the worker. */
  detect(frame: HTMLCanvasElement): Promise<FrameDetection>;
  /** Warps the frame's paper to width × height between the corners; runs in the worker. */
  extractPaper(
    frame: HTMLCanvasElement,
    corners: Quad,
    width: number,
    height: number,
  ): Promise<HTMLCanvasElement>;
  /** Stops the worker. */
  terminate(): void;
}

/**
 * The quad of a detection: jscanify's classification when it is
 * complete and simple; the extreme-point fallback over the same
 * contour when it is not — its corners can be missing (a sheet rotated
 * toward 45° puts contour points in the wrong quadrant) or form a
 * crossed shape. Null when neither path gives four corners that make
 * a usable quad.
 */
export function detectQuad(detection: FrameDetection): Quad | null {
  if (detection.classified) {
    const classified = orderCorners(detection.classified);
    if (classified && isSimpleQuad(classified)) {
      return classified;
    }
  }
  if (!detection.points) {
    return null;
  }
  const fallback = cornersFromContour(detection.points);
  return fallback && isSimpleQuad(fallback) ? fallback : null;
}

/**
 * Loads the engine: creates the worker, waits for it to become ready
 * (or times out), and returns the handle. A failed load is not cached;
 * the next scanner open tries again.
 */
export function loadScanEngine(
  createWorker: WorkerFactory = () => new Worker(SCANNER_WORKER_PATH),
): Promise<ScanEngine> {
  return start(createWorker);
}

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
  timer: ReturnType<typeof globalThis.setTimeout>;
}

async function start(createWorker: WorkerFactory): Promise<ScanEngine> {
  const worker = createWorker();
  let nextId = 1;
  const pending = new Map<number, PendingRequest>();

  // Wait for the worker to become ready.
  const initId = nextId++;
  await new Promise<void>((resolve, reject) => {
    const onMessage = (event: MessageEvent<unknown>) => {
      const message = event.data as {
        id?: number;
        type?: string;
        message?: string;
      } | null;
      if (!message || message.id !== initId) return;
      cleanup();
      if (message.type === 'ready') resolve();
      else
        reject(
          new Error(
            message.message ?? 'The scanning engine did not become ready.',
          ),
        );
    };
    const onError = (event: ErrorEvent) => {
      cleanup();
      reject(
        new Error(event.message || 'The scanning engine could not be loaded.'),
      );
    };
    const timer = globalThis.setTimeout(() => {
      cleanup();
      worker.terminate();
      reject(new Error('The scanning engine did not become ready in time.'));
    }, ENGINE_TIMEOUT_MS);
    function cleanup() {
      globalThis.clearTimeout(timer);
      worker.removeEventListener('message', onMessage);
      worker.removeEventListener('error', onError);
    }
    worker.addEventListener('message', onMessage);
    worker.addEventListener('error', onError);
    worker.postMessage({
      id: initId,
      type: 'init',
      opencvUrl,
      jscanifyUrl: JSCANIFY_URL,
    });
  });

  // Wire the steady-state message router.
  const onMessage = (event: MessageEvent<unknown>) => {
    const message = event.data as {
      id?: number;
      type?: string;
      message?: string;
    } | null;
    if (!message || typeof message.id !== 'number') return;
    const request = pending.get(message.id);
    if (!request) return;
    globalThis.clearTimeout(request.timer);
    pending.delete(message.id);
    if (message.type === 'error') {
      request.reject(
        new Error(message.message ?? 'The scanning engine failed.'),
      );
    } else {
      request.resolve(message);
    }
  };
  const onError = (event: ErrorEvent) => {
    for (const request of pending.values()) {
      globalThis.clearTimeout(request.timer);
      request.reject(new Error(event.message || 'The scanning engine failed.'));
    }
    pending.clear();
  };
  worker.addEventListener('message', onMessage);
  worker.addEventListener('error', onError);

  function postRequest(
    payload: Record<string, unknown>,
    transfer?: Transferable[],
  ): Promise<unknown> {
    const id = nextId++;
    return new Promise((resolve, reject) => {
      const timer = globalThis.setTimeout(() => {
        if (pending.has(id)) {
          pending.delete(id);
          reject(new Error('The scanning engine did not answer in time.'));
        }
      }, ENGINE_TIMEOUT_MS);
      pending.set(id, { resolve, reject, timer });
      worker.postMessage({ ...payload, id }, transfer);
    });
  }

  function framePixels(frame: HTMLCanvasElement): {
    buffer: ArrayBuffer;
    width: number;
    height: number;
  } {
    const width = frame.width;
    const height = frame.height;
    const context = frame.getContext('2d');
    if (context) {
      const imageData = context.getImageData(0, 0, width, height);
      return { buffer: imageData.data.buffer, width, height };
    }
    return { buffer: new ArrayBuffer(width * height * 4), width, height };
  }

  const engine: ScanEngine = {
    async detect(frame) {
      const { buffer, width, height } = framePixels(frame);
      const response = (await postRequest(
        { type: 'detect', buffer, width, height },
        [buffer],
      )) as { classified: RawCorners | null; points: number[] | null };
      return { classified: response.classified, points: response.points };
    },
    async extractPaper(frame, corners, width, height) {
      const {
        buffer,
        width: sourceWidth,
        height: sourceHeight,
      } = framePixels(frame);
      const response = (await postRequest(
        {
          type: 'warp',
          buffer,
          width: sourceWidth,
          height: sourceHeight,
          corners,
          targetWidth: width,
          targetHeight: height,
        },
        [buffer],
      )) as { buffer: ArrayBuffer; width: number; height: number };
      const canvas = document.createElement('canvas');
      canvas.width = response.width;
      canvas.height = response.height;
      const context = canvas.getContext('2d');
      if (context) {
        const imageData = new ImageData(
          new Uint8ClampedArray(response.buffer),
          response.width,
          response.height,
        );
        context.putImageData(imageData, 0, 0);
      }
      return canvas;
    },
    terminate() {
      worker.removeEventListener('message', onMessage);
      worker.removeEventListener('error', onError);
      worker.terminate();
    },
  };

  return engine;
}
