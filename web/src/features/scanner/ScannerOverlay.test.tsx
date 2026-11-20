import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import type { ScanAnalyzer } from './analyze';
import type { Quad } from './corners';
import type { CameraSource, PhotoCapture } from './camera';
import { SCAN_JPEG_QUALITY } from './encode';
import type { FrameDetection, ScanEngine } from './engine';
import type { ScanWarnings } from './quality';
import { ScannerOverlay } from './ScannerOverlay';

// The OpenCV bytes reach the engine module as a build-time asset URL;
// the mock keeps the asset pipeline out of these tests.
vi.mock('@techstark/opencv-js/dist/opencv.js?url', () => ({
  default: '/assets/opencv-test.js',
}));

// The encode seam is the one piece of browser work the confirm path
// does; the mock records the call while every constant stays real.
const encodeState = vi.hoisted(() => ({
  calls: [] as Array<{ canvas: unknown; quality: number; fileName: string }>,
}));
vi.mock('./encode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./encode')>();
  return {
    ...actual,
    encodeJpeg: async (
      canvas: unknown,
      quality: number,
      fileName: string,
    ): Promise<File> => {
      encodeState.calls.push({ canvas, quality, fileName });
      return new File(['scan-bytes'], fileName, { type: 'image/jpeg' });
    },
  };
});

/** A captured photo: a canvas the tests can size, and its file. */
function makeShot(): PhotoCapture {
  const canvas = document.createElement('canvas');
  canvas.width = 1000;
  canvas.height = 800;
  return {
    canvas,
    photo: new File(['photo-bytes'], 'photo-20261120-142500.jpg', {
      type: 'image/jpeg',
    }),
  };
}

/** A camera the tests control: open, snapshot and close are all
 * observable and all cheap to break. */
function makeFakeCamera(shot: PhotoCapture = makeShot()) {
  let current = shot;
  let open: () => Promise<void> = async () => {};
  let snapshot: () => Promise<PhotoCapture> = () => Promise.resolve(current);
  const calls = { open: 0, snapshot: 0, close: 0 };
  return {
    calls,
    nowAnswers(next: PhotoCapture) {
      current = next;
    },
    failsToOpen() {
      open = () => Promise.reject(new Error('Permission denied'));
    },
    failsToSnapshot() {
      snapshot = () => Promise.reject(new Error('No frame'));
    },
    camera: {
      open: () => {
        calls.open += 1;
        return open();
      },
      snapshot: () => {
        calls.snapshot += 1;
        return snapshot();
      },
      close: () => {
        calls.close += 1;
      },
    } satisfies CameraSource,
  };
}

/** An engine the tests control: the detection can change between
 * captures, and every warp is recorded. */
function makeFakeEngine(detection: FrameDetection) {
  let current = detection;
  let detectCount = 0;
  const warps: Array<{
    frame: HTMLCanvasElement;
    corners: Quad;
    width: number;
    height: number;
  }> = [];
  const warpCanvas = document.createElement('canvas');
  return {
    warps,
    warpCanvas,
    get detectCount() {
      return detectCount;
    },
    nowSees(next: FrameDetection) {
      current = next;
    },
    engine: {
      async detect() {
        detectCount += 1;
        return current;
      },
      async extractPaper(
        frame: HTMLCanvasElement,
        corners: Quad,
        width: number,
        height: number,
      ) {
        warps.push({ frame, corners, width, height });
        return warpCanvas;
      },
      terminate: vi.fn(),
    } satisfies ScanEngine,
  };
}

/** The jscanify classification of an axis-aligned sheet. */
function classifiedSheet(): FrameDetection {
  return {
    classified: {
      topLeftCorner: { x: 100, y: 80 },
      topRightCorner: { x: 900, y: 80 },
      bottomRightCorner: { x: 900, y: 720 },
      bottomLeftCorner: { x: 100, y: 720 },
    },
    points: null,
  };
}

interface OverlayHandlers {
  onClose: Mock<() => void>;
  onEnqueuePhoto: Mock<(photo: File) => void>;
  onEnqueueScan: Mock<(photo: File, scan: File) => void>;
}

/** The analyzer's clean verdict: no warnings, so the confirm path
 * runs straight to the queue. */
const cleanAnalyzer: ScanAnalyzer = {
  analyze: () => ({ blurry: false, dark: false }),
};

/** An analyzer that always raises the given verdict. */
function warningAnalyzer(warnings: ScanWarnings): ScanAnalyzer {
  return { analyze: () => warnings };
}

function renderOverlay(
  camera: CameraSource,
  engine: () => Promise<ScanEngine>,
  analyzeScan: ScanAnalyzer = cleanAnalyzer,
): OverlayHandlers & { unmount: () => void } {
  const handlers: OverlayHandlers = {
    onClose: vi.fn(),
    onEnqueuePhoto: vi.fn(),
    onEnqueueScan: vi.fn(),
  };
  const { unmount } = render(
    <ScannerOverlay
      onClose={handlers.onClose}
      onEnqueuePhoto={handlers.onEnqueuePhoto}
      onEnqueueScan={handlers.onEnqueueScan}
      openCamera={() => camera}
      loadEngine={engine}
      analyzeScan={analyzeScan}
    />,
  );
  return { ...handlers, unmount };
}

/** Renders, opens, and waits for the live view. */
async function arriveAtLive(
  camera: CameraSource,
  engine: () => Promise<ScanEngine>,
  analyzeScan: ScanAnalyzer = cleanAnalyzer,
): Promise<OverlayHandlers> {
  const handlers = renderOverlay(camera, engine, analyzeScan);
  await screen.findByRole('button', { name: 'Capture' });
  return handlers;
}

/** Captures once and waits for the adjust view's handles. */
async function arriveAtAdjust(
  fakeCamera: ReturnType<typeof makeFakeCamera>,
  fakeEngine: ReturnType<typeof makeFakeEngine>,
  analyzeScan: ScanAnalyzer = cleanAnalyzer,
): Promise<OverlayHandlers> {
  const handlers = await arriveAtLive(
    fakeCamera.camera,
    () => Promise.resolve(fakeEngine.engine),
    analyzeScan,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Capture' }));
  await screen.findByRole('button', { name: 'Top left corner' });
  return handlers;
}

afterEach(() => {
  vi.useRealTimers();
  encodeState.calls.length = 0;
});

describe('ScannerOverlay opening', () => {
  it('opens the camera and shows the live view', async () => {
    const fakeCamera = makeFakeCamera();
    const handlers = await arriveAtLive(fakeCamera.camera, () =>
      Promise.resolve(makeFakeEngine(classifiedSheet()).engine),
    );

    expect(
      screen.getByRole('dialog', { name: 'Scan a receipt with the camera' }),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    expect(handlers.onClose).not.toHaveBeenCalled();
    expect(fakeCamera.calls.close).toBe(0);
  });

  it('terminates the engine when the overlay unmounts', async () => {
    const fakeCamera = makeFakeCamera();
    const fakeEngine = makeFakeEngine(classifiedSheet());
    const handlers = renderOverlay(fakeCamera.camera, () =>
      Promise.resolve(fakeEngine.engine),
    );
    await screen.findByRole('button', { name: 'Capture' });
    handlers.unmount();
    expect(fakeEngine.engine.terminate).toHaveBeenCalled();
  });

  it('shows the loading state while the engine and camera open', async () => {
    const fakeCamera = makeFakeCamera();
    renderOverlay(fakeCamera.camera, () => new Promise(() => {}));

    expect(screen.getByText('Loading the scanning engine…')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Capture' })).toBeNull();
  });

  it('reports a denied camera and stays cancellable', async () => {
    const fakeCamera = makeFakeCamera();
    fakeCamera.failsToOpen();
    const handlers = renderOverlay(fakeCamera.camera, () =>
      Promise.resolve(makeFakeEngine(classifiedSheet()).engine),
    );

    await screen.findByText(/The camera could not be opened/);

    expect(screen.queryByRole('button', { name: 'Capture' })).toBeNull();
    expect(fakeCamera.calls.close).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(handlers.onClose).toHaveBeenCalledTimes(1);
  });

  it('reports an unavailable engine and points at the plain photo', async () => {
    const fakeCamera = makeFakeCamera();
    const handlers = renderOverlay(fakeCamera.camera, () =>
      Promise.reject(new Error('offline')),
    );

    await screen.findByText(/The scanning engine could not be loaded/);

    expect(screen.queryByRole('button', { name: 'Capture' })).toBeNull();
    // No scan can happen without the engine: the camera stops too.
    expect(fakeCamera.calls.close).toBe(1);
    expect(handlers.onClose).not.toHaveBeenCalled();
  });

  it('closes from the Escape key', async () => {
    const fakeCamera = makeFakeCamera();
    const handlers = await arriveAtLive(fakeCamera.camera, () =>
      Promise.resolve(makeFakeEngine(classifiedSheet()).engine),
    );

    fireEvent.keyDown(
      screen.getByRole('dialog', { name: 'Scan a receipt with the camera' }),
      { key: 'Escape' },
    );

    expect(handlers.onClose).toHaveBeenCalledTimes(1);
  });
});

describe('ScannerOverlay live guide', () => {
  it('samples the paper at most twice per second', async () => {
    vi.useFakeTimers();
    const fakeCamera = makeFakeCamera();
    const fakeEngine = makeFakeEngine(classifiedSheet());
    renderOverlay(fakeCamera.camera, () => Promise.resolve(fakeEngine.engine));
    // jsdom never gives a video its frame size; the sampling reads it
    // right after the camera opens, before any microtask runs.
    const video = document.querySelector('video');
    Object.defineProperty(video, 'videoWidth', {
      value: 1280,
      configurable: true,
    });
    Object.defineProperty(video, 'videoHeight', {
      value: 720,
      configurable: true,
    });

    // The open effect settles on microtasks; the pump gives them time
    // without touching the sampling interval itself.
    for (let round = 0; round < 10; round++) {
      if (screen.queryByRole('button', { name: 'Capture' })) break;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
    }
    expect(screen.getByRole('button', { name: 'Capture' })).toBeTruthy();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(fakeEngine.detectCount).toBe(2);
    expect(document.querySelector('polygon')?.getAttribute('points')).toBe(
      '100,80 900,80 900,720 100,720',
    );
  });

  it('keeps the live view when a capture fails, and says so', async () => {
    const fakeCamera = makeFakeCamera();
    fakeCamera.failsToSnapshot();
    const handlers = await arriveAtLive(fakeCamera.camera, () =>
      Promise.resolve(makeFakeEngine(classifiedSheet()).engine),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Capture' }));

    await screen.findByText(/The photo could not be captured/);
    expect(screen.getByRole('button', { name: 'Capture' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Confirm' })).toBeNull();
    expect(handlers.onEnqueuePhoto).not.toHaveBeenCalled();
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
  });
});

describe('ScannerOverlay corner adjustment', () => {
  it('initialises the handles from the detection, in photo space', async () => {
    const fakeCamera = makeFakeCamera();
    const shot = makeShot();
    fakeCamera.nowAnswers(shot);
    await arriveAtAdjust(fakeCamera, makeFakeEngine(classifiedSheet()));

    expect(fakeCamera.calls.snapshot).toBe(1);
    const topLeft = screen.getByRole('button', { name: 'Top left corner' });
    expect(topLeft.style.left).toBe('10%');
    expect(topLeft.style.top).toBe('10%');
    const bottomRight = screen.getByRole('button', {
      name: 'Bottom right corner',
    });
    expect(bottomRight.style.left).toBe('90%');
    expect(bottomRight.style.top).toBe('90%');
    // The photo itself is on screen, at its captured size.
    const box = screen.getByRole('img', {
      name: 'Captured photo with corner handles',
    });
    expect(box.querySelector('canvas')?.width).toBe(1000);
    expect(box.querySelector('canvas')?.height).toBe(800);
  });

  it('falls back to the default inset handles when detection misses', async () => {
    const fakeCamera = makeFakeCamera();
    await arriveAtAdjust(
      fakeCamera,
      makeFakeEngine({
        classified: null,
        points: null,
      }),
    );

    const topLeft = screen.getByRole('button', { name: 'Top left corner' });
    expect(topLeft.style.left).toBe('10%');
    expect(topLeft.style.top).toBe('10%');
    const bottomRight = screen.getByRole('button', {
      name: 'Bottom right corner',
    });
    expect(bottomRight.style.left).toBe('90%');
    expect(bottomRight.style.top).toBe('90%');
  });

  it('moves one handle to the pointer and leaves the others alone', async () => {
    const fakeCamera = makeFakeCamera();
    await arriveAtAdjust(fakeCamera, makeFakeEngine(classifiedSheet()));
    const box = screen.getByRole('img', {
      name: 'Captured photo with corner handles',
    });
    // The box shows a 1000×800 photo as 500×400: the pointer's share of
    // the box is its share of the photo.
    vi.spyOn(box, 'getBoundingClientRect').mockReturnValue(
      new DOMRect(0, 0, 500, 400),
    );

    const topLeft = screen.getByRole('button', { name: 'Top left corner' });
    fireEvent.pointerDown(topLeft, { clientX: 100, clientY: 80 });
    fireEvent.pointerMove(box, { clientX: 150, clientY: 120 });
    fireEvent.pointerUp(box);

    expect(topLeft.style.left).toBe('30%');
    expect(topLeft.style.top).toBe('30%');
    const topRight = screen.getByRole('button', { name: 'Top right corner' });
    expect(topRight.style.left).toBe('90%');
    expect(topRight.style.top).toBe('10%');
  });

  it('re-runs detection for Auto corners', async () => {
    const fakeCamera = makeFakeCamera();
    const fakeEngine = makeFakeEngine(classifiedSheet());
    await arriveAtAdjust(fakeCamera, fakeEngine);
    const topLeft = screen.getByRole('button', { name: 'Top left corner' });
    expect(topLeft.style.left).toBe('10%');

    fakeEngine.nowSees({
      classified: {
        topLeftCorner: { x: 400, y: 320 },
        topRightCorner: { x: 600, y: 320 },
        bottomRightCorner: { x: 600, y: 480 },
        bottomLeftCorner: { x: 400, y: 480 },
      },
      points: null,
    });

    fireEvent.click(screen.getByRole('button', { name: 'Auto corners' }));

    await waitFor(() => {
      expect(topLeft.style.left).toBe('40%');
    });
    expect(topLeft.style.top).toBe('40%');
  });

  it('returns to the live view on Retake', async () => {
    const fakeCamera = makeFakeCamera();
    const handlers = await arriveAtAdjust(
      fakeCamera,
      makeFakeEngine(classifiedSheet()),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Retake' }));

    await screen.findByRole('button', { name: 'Capture' });
    expect(
      screen.queryByRole('button', { name: 'Top left corner' }),
    ).toBeNull();
    expect(handlers.onEnqueuePhoto).not.toHaveBeenCalled();
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
  });
});

describe('ScannerOverlay enqueueing', () => {
  it('uploads the plain photo from Use whole photo', async () => {
    const fakeCamera = makeFakeCamera();
    const shot = makeShot();
    fakeCamera.nowAnswers(shot);
    const fakeEngine = makeFakeEngine(classifiedSheet());
    const handlers = await arriveAtAdjust(fakeCamera, fakeEngine);

    fireEvent.click(screen.getByRole('button', { name: 'Use whole photo' }));

    expect(handlers.onEnqueuePhoto).toHaveBeenCalledTimes(1);
    expect(handlers.onEnqueuePhoto).toHaveBeenCalledWith(shot.photo);
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
    expect(handlers.onClose).toHaveBeenCalledTimes(1);
    expect(fakeEngine.warps).toHaveLength(0);
    expect(encodeState.calls).toHaveLength(0);
  });

  it('warps once, encodes once at the scan quality, and enqueues both files', async () => {
    const fakeCamera = makeFakeCamera();
    const shot = makeShot();
    fakeCamera.nowAnswers(shot);
    const fakeEngine = makeFakeEngine(classifiedSheet());
    const handlers = await arriveAtAdjust(fakeCamera, fakeEngine);

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => {
      expect(handlers.onEnqueueScan).toHaveBeenCalledTimes(1);
    });

    // The warp took the detected quad and the size its edges measure.
    expect(fakeEngine.warps).toHaveLength(1);
    expect(fakeEngine.warps[0]?.frame).toBe(shot.canvas);
    expect(fakeEngine.warps[0]?.corners).toEqual([
      { x: 100, y: 80 },
      { x: 900, y: 80 },
      { x: 900, y: 720 },
      { x: 100, y: 720 },
    ]);
    expect(fakeEngine.warps[0]?.width).toBe(800);
    expect(fakeEngine.warps[0]?.height).toBe(640);
    // One encode, at the decided scan quality and UTC name.
    expect(encodeState.calls).toHaveLength(1);
    expect(encodeState.calls[0]?.canvas).toBe(fakeEngine.warpCanvas);
    expect(encodeState.calls[0]?.quality).toBe(SCAN_JPEG_QUALITY);
    expect(encodeState.calls[0]?.fileName).toMatch(/^scan-\d{8}-\d{6}\.jpg$/);
    // The scan carries its photo along, and the overlay finishes.
    expect(handlers.onEnqueueScan).toHaveBeenCalledWith(
      shot.photo,
      expect.objectContaining({ type: 'image/jpeg' }),
    );
    expect(handlers.onEnqueuePhoto).not.toHaveBeenCalled();
    expect(handlers.onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps the adjust view when the warp fails, and says so', async () => {
    const fakeCamera = makeFakeCamera();
    const failingEngine = makeFakeEngine(classifiedSheet());
    failingEngine.engine.extractPaper = async () => {
      throw new Error('warp failed');
    };
    const handlers = await arriveAtAdjust(fakeCamera, failingEngine);

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await screen.findByText(/The scan could not be created/);
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
    expect(handlers.onClose).not.toHaveBeenCalled();
    // Still in the adjust view: the handles remain for another try.
    expect(
      screen.getByRole('button', { name: 'Top left corner' }),
    ).toBeTruthy();
  });
});

describe('ScannerOverlay quality warning', () => {
  it('shows the warp with both warnings and queues it only on Upload anyway', async () => {
    const fakeCamera = makeFakeCamera();
    const shot = makeShot();
    fakeCamera.nowAnswers(shot);
    const fakeEngine = makeFakeEngine(classifiedSheet());
    const handlers = await arriveAtAdjust(
      fakeCamera,
      fakeEngine,
      warningAnalyzer({ blurry: true, dark: true }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await screen.findByText(/The scan may be blurry\./);
    expect(screen.getByText(/The scan may be too dark\./)).toBeTruthy();
    // The exact pixels the queue would receive are on screen.
    expect(document.querySelector('canvas')).toBe(fakeEngine.warpCanvas);
    // Nothing was encoded or queued: the user decides first.
    expect(encodeState.calls).toHaveLength(0);
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
    expect(handlers.onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Upload anyway' }));

    await waitFor(() => {
      expect(handlers.onEnqueueScan).toHaveBeenCalledTimes(1);
    });
    expect(encodeState.calls).toHaveLength(1);
    expect(encodeState.calls[0]?.canvas).toBe(fakeEngine.warpCanvas);
    expect(encodeState.calls[0]?.quality).toBe(SCAN_JPEG_QUALITY);
    expect(encodeState.calls[0]?.fileName).toMatch(/^scan-\d{8}-\d{6}\.jpg$/);
    expect(handlers.onEnqueueScan).toHaveBeenCalledWith(
      shot.photo,
      expect.objectContaining({ type: 'image/jpeg' }),
    );
    expect(handlers.onClose).toHaveBeenCalledTimes(1);
  });

  it('warns about darkness alone, and Retake returns to the camera', async () => {
    const fakeCamera = makeFakeCamera();
    const fakeEngine = makeFakeEngine(classifiedSheet());
    const handlers = await arriveAtAdjust(
      fakeCamera,
      fakeEngine,
      warningAnalyzer({ blurry: false, dark: true }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await screen.findByText(/The scan may be too dark\./);
    expect(screen.queryByText(/blurry/)).toBeNull();
    expect(encodeState.calls).toHaveLength(0);
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Retake' }));

    await screen.findByRole('button', { name: 'Capture' });
    expect(document.querySelector('canvas')).toBeNull();
    expect(handlers.onEnqueuePhoto).not.toHaveBeenCalled();
    expect(handlers.onEnqueueScan).not.toHaveBeenCalled();
    expect(handlers.onClose).not.toHaveBeenCalled();
  });
});
