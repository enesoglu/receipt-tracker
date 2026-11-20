import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { scanFileName } from '../../api/upload-queue';
import { Button } from '../../components/ui/Button';
import { ErrorState } from '../../components/ui/ErrorState';
import { Spinner } from '../../components/ui/Spinner';
import { browserScanAnalyzer, type ScanAnalyzer } from './analyze';
import { defaultQuad, scanDimensions, type Quad } from './corners';
import { deviceCamera, type CameraSource, type PhotoCapture } from './camera';
import { encodeJpeg, SCAN_JPEG_QUALITY } from './encode';
import { detectQuad, loadScanEngine, type ScanEngine } from './engine';
import { LIVE_SAMPLE_INTERVAL_MS, sampleGuideQuad } from './guide';
import type { ScanWarnings } from './quality';

/** One screen's phases: opening, aiming, adjusting, the quality
 * warning, and the two ways opening can fail. */
type Phase =
  | 'loading'
  | 'live'
  | 'adjust'
  | 'warn'
  | 'camera-denied'
  | 'engine-unavailable';

/** The handle labels, in the quad's canonical corner order. */
const CORNER_LABELS = [
  'Top left corner',
  'Top right corner',
  'Bottom right corner',
  'Bottom left corner',
] as const;

export interface ScannerOverlayProps {
  /** Abandons the scan and returns to the upload screen. */
  onClose: () => void;
  /** Uploads the plain photo, without a scan. */
  onEnqueuePhoto: (photo: File) => void;
  /** Uploads the warped scan together with the photo it was made
   * from. */
  onEnqueueScan: (photo: File, scan: File) => void;
  /** Where the camera comes from: the default opens the device
   * camera; tests pass a fake. */
  openCamera?: (video: HTMLVideoElement) => CameraSource;
  /** How the engine loads: the default loads both scripts lazily;
   * tests pass a fake. */
  loadEngine?: () => Promise<ScanEngine>;
  /** How the scan's quality is judged: the default reads the
   * browser's pixels; tests pass a fake. */
  analyzeScan?: ScanAnalyzer;
}

/** Draws one value onto a bounded range. */
function clamp(value: number, low: number, high: number): number {
  return Math.min(Math.max(value, low), high);
}

/**
 * The scanner's full-screen overlay: the camera's live view with a
 * silent paper guide, then the captured photo with four draggable
 * corner handles, then the warped scan on its way to the queue —
 * always alongside the untouched photo it was made from. A blurred
 * or dark scan is shown first, with Retake and Upload anyway: the
 * warning advises, it never blocks.
 */
export function ScannerOverlay({
  onClose,
  onEnqueuePhoto,
  onEnqueueScan,
  openCamera = deviceCamera,
  loadEngine = loadScanEngine,
  analyzeScan = browserScanAnalyzer,
}: ScannerOverlayProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const mountRef = useRef<HTMLDivElement>(null);
  const warnMountRef = useRef<HTMLDivElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const cameraRef = useRef<CameraSource | null>(null);
  const engineRef = useRef<ScanEngine | null>(null);
  const dragRef = useRef<{ corner: number; rect: DOMRect } | null>(null);

  const [phase, setPhase] = useState<Phase>('loading');
  const [frame, setFrame] = useState<{ width: number; height: number } | null>(
    null,
  );
  const [guide, setGuide] = useState<Quad | null>(null);
  const [photo, setPhoto] = useState<PhotoCapture | null>(null);
  const [corners, setCorners] = useState<Quad | null>(null);
  const [warned, setWarned] = useState<{
    canvas: HTMLCanvasElement;
    warnings: ScanWarnings;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [working, setWorking] = useState(false);

  // The camera opens once per overlay: the stream keeps running under
  // the adjust view, so Retake is instant and close-on-unmount is the
  // only teardown.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const camera = openCamera(video);
    cameraRef.current = camera;
    let cancelled = false;
    const cameraOpen = camera.open();
    const engineLoad = loadEngine();
    void (async () => {
      let cameraFailed = false;
      try {
        await cameraOpen;
        if (!cancelled) {
          setFrame({
            width: video.videoWidth,
            height: video.videoHeight,
          });
        }
      } catch {
        cameraFailed = true;
        if (!cancelled) {
          camera.close();
          setPhase('camera-denied');
        }
      }
      try {
        const engine = await engineLoad;
        if (!cancelled && !cameraFailed) {
          engineRef.current = engine;
          setPhase('live');
        }
      } catch {
        // Both may fail: the camera's message stays, since without a
        // camera no scan can happen either way.
        if (!cancelled && !cameraFailed) {
          camera.close();
          setPhase('engine-unavailable');
        }
      }
    })();
    return () => {
      cancelled = true;
      camera.close();
      if (engineRef.current) {
        engineRef.current.terminate();
        engineRef.current = null;
      }
    };
  }, [openCamera, loadEngine]);

  // The live guide samples at most twice per second; globalThis
  // carries the browser's timers and is what tests fake.
  useEffect(() => {
    if (phase !== 'live') return;
    const video = videoRef.current;
    const engine = engineRef.current;
    if (!video || !engine) return;
    const id = globalThis.setInterval(() => {
      void sampleGuideQuad(engine, video).then((quad) => {
        setGuide(quad);
      });
    }, LIVE_SAMPLE_INTERVAL_MS);
    return () => {
      globalThis.clearInterval(id);
      setGuide(null);
    };
  }, [phase]);

  // The captured canvas is appended imperatively: it is the scanner's
  // own DOM node, never React-managed, and the handles position
  // themselves over it.
  useEffect(() => {
    if (phase !== 'adjust' || !photo) return;
    const mount = mountRef.current;
    if (!mount) return;
    mount.replaceChildren(photo.canvas);
  }, [phase, photo]);

  // The warped scan is mounted the same way in the warning view: the
  // user judges the exact pixels the queue would receive.
  useEffect(() => {
    if (phase !== 'warn' || !warned) return;
    const mount = warnMountRef.current;
    if (!mount) return;
    mount.replaceChildren(warned.canvas);
  }, [phase, warned]);

  const handleCapture = useCallback(async () => {
    const camera = cameraRef.current;
    const engine = engineRef.current;
    if (!camera || !engine || working) return;
    setWorking(true);
    setError(null);
    try {
      const shot = await camera.snapshot();
      // The canvas arrives at its captured size and is styled here,
      // once, in the handler that receives it — the element is the
      // scanner's own object, never React-managed.
      shot.canvas.className = 'block h-auto w-full';
      setPhoto(shot);
      const detection = await engine.detect(shot.canvas);
      setCorners(detectQuad(detection) ?? defaultQuad(shot.canvas));
      setGuide(null);
      setPhase('adjust');
    } catch {
      // The frame could not be captured or encoded: stay in the live
      // view, where trying again is the only sensible move.
      setError('The photo could not be captured. Try again.');
    } finally {
      setWorking(false);
    }
  }, [working]);

  const handleAutoCorners = useCallback(async () => {
    const engine = engineRef.current;
    const shot = photo;
    if (!engine || !shot) return;
    const found = detectQuad(await engine.detect(shot.canvas));
    // A detection miss keeps the handles where they are: silently,
    // the way the live guide treats one.
    if (found) {
      setCorners(found);
    }
  }, [photo]);

  const startDrag = useCallback(
    (corner: number) => (event: ReactPointerEvent<HTMLButtonElement>) => {
      const box = boxRef.current;
      const shot = photo;
      if (!box || !shot) return;
      event.preventDefault();
      dragRef.current = { corner, rect: box.getBoundingClientRect() };
    },
    [photo],
  );

  const dragTo = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      const drag = dragRef.current;
      const shot = photo;
      if (!drag || !shot) return;
      if (drag.rect.width === 0 || drag.rect.height === 0) return;
      const x = clamp(
        ((event.clientX - drag.rect.left) / drag.rect.width) *
          shot.canvas.width,
        0,
        shot.canvas.width,
      );
      const y = clamp(
        ((event.clientY - drag.rect.top) / drag.rect.height) *
          shot.canvas.height,
        0,
        shot.canvas.height,
      );
      setCorners((current) => {
        if (!current) return current;
        const next: Quad = [...current];
        next[drag.corner] = { x, y };
        return next;
      });
    },
    [photo],
  );

  const endDrag = useCallback(() => {
    dragRef.current = null;
  }, []);

  const handleRetake = useCallback(() => {
    setPhoto(null);
    setCorners(null);
    setWarned(null);
    setError(null);
    setPhase('live');
  }, []);

  const handleWholePhoto = useCallback(() => {
    const shot = photo;
    if (!shot) return;
    onEnqueuePhoto(shot.photo);
    onClose();
  }, [photo, onEnqueuePhoto, onClose]);

  const handleConfirm = useCallback(async () => {
    const engine = engineRef.current;
    const shot = photo;
    const chosen = corners;
    if (!engine || !shot || !chosen || working) return;
    setWorking(true);
    setError(null);
    try {
      const size = scanDimensions(chosen, shot.canvas);
      const scanCanvas = await engine.extractPaper(
        shot.canvas,
        chosen,
        size.width,
        size.height,
      );
      // Styled once where it is made, the same way the photo canvas
      // is: the element is the scanner's own object.
      scanCanvas.className = 'block h-auto w-full';
      const warnings = analyzeScan.analyze(scanCanvas);
      if (warnings.blurry || warnings.dark) {
        // Advice before the queue: the scan is held, shown, and
        // judged by the user, never discarded on its own.
        setWarned({ canvas: scanCanvas, warnings });
        setPhase('warn');
        return;
      }
      const scan = await encodeJpeg(
        scanCanvas,
        SCAN_JPEG_QUALITY,
        scanFileName(new Date()),
      );
      onEnqueueScan(shot.photo, scan);
      onClose();
    } catch {
      setError('The scan could not be created. Adjust the corners and retry.');
    } finally {
      setWorking(false);
    }
  }, [photo, corners, working, analyzeScan, onEnqueueScan, onClose]);

  const handleUploadAnyway = useCallback(async () => {
    const shot = photo;
    const result = warned;
    if (!shot || !result || working) return;
    setWorking(true);
    setError(null);
    try {
      const scan = await encodeJpeg(
        result.canvas,
        SCAN_JPEG_QUALITY,
        scanFileName(new Date()),
      );
      onEnqueueScan(shot.photo, scan);
      onClose();
    } catch {
      // Encoding failed after all: back to the handles, where the
      // corners can be fixed or the shot retaken.
      setWarned(null);
      setPhase('adjust');
      setError('The scan could not be created. Adjust the corners and retry.');
    } finally {
      setWorking(false);
    }
  }, [photo, warned, working, onEnqueueScan, onClose]);

  const handleKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLDivElement>) => {
      if (event.key === 'Escape') {
        onClose();
      }
    },
    [onClose],
  );

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Scan a receipt with the camera"
      onKeyDown={handleKeyDown}
      className="fixed inset-0 z-50 flex flex-col gap-4 overflow-y-auto bg-canvas p-4 sm:p-6"
    >
      <header className="mx-auto flex w-full max-w-content items-center justify-between gap-4">
        <h2 className="text-heading font-semibold text-ink">Scan a receipt</h2>
        <Button variant="secondary" size="sm" onClick={onClose}>
          Cancel
        </Button>
      </header>

      <div className="mx-auto flex w-full max-w-content flex-col gap-4">
        {phase === 'loading' ? (
          <div className="flex flex-col items-center justify-center gap-3 py-16">
            <Spinner showLabel label="Opening the scanner" />
            <p className="text-small text-muted">
              Loading the scanning engine…
            </p>
          </div>
        ) : null}

        {phase === 'camera-denied' ? (
          <ErrorState
            title="Camera unavailable"
            message="The camera could not be opened. Check the browser's camera permission, or upload the plain photo instead."
          />
        ) : null}

        {phase === 'engine-unavailable' ? (
          <ErrorState
            title="Scanner engine unavailable"
            message="The scanning engine could not be loaded. Upload the plain photo instead."
          />
        ) : null}

        <div className="relative w-full">
          <video
            ref={videoRef}
            muted
            playsInline
            autoPlay
            aria-hidden="true"
            className={`block h-auto w-full bg-ink ${
              phase === 'live' ? '' : 'hidden'
            }`}
          />
          {phase === 'live' && guide && frame
            ? renderOutline(guide, frame.width, frame.height)
            : null}
        </div>

        {phase === 'live' ? (
          <div className="flex flex-col gap-3">
            <p className="text-small text-muted">
              Hold the receipt flat and fill the frame. The outline marks the
              detected paper.
            </p>
            {error ? (
              <ErrorState title="Capture failed" message={error} />
            ) : null}
            <div className="flex items-center justify-center">
              <Button
                variant="primary"
                loading={working}
                onClick={() => void handleCapture()}
              >
                Capture
              </Button>
            </div>
          </div>
        ) : null}

        {phase === 'adjust' && photo && corners ? (
          <div className="flex flex-col gap-3">
            <div
              ref={boxRef}
              role="img"
              aria-label="Captured photo with corner handles"
              className="relative w-full touch-none"
              onPointerMove={dragTo}
              onPointerUp={endDrag}
              onPointerLeave={endDrag}
            >
              <div ref={mountRef} className="w-full" />
              {renderOutline(corners, photo.canvas.width, photo.canvas.height)}
              {corners.map((point, index) => (
                <button
                  key={CORNER_LABELS[index]}
                  type="button"
                  aria-label={CORNER_LABELS[index]}
                  onPointerDown={startDrag(index)}
                  className="absolute h-4 w-4 -translate-x-1/2 -translate-y-1/2 cursor-grab rounded-full border-2 border-accent bg-surface focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                  style={{
                    left: `${(point.x / photo.canvas.width) * 100}%`,
                    top: `${(point.y / photo.canvas.height) * 100}%`,
                  }}
                />
              ))}
            </div>
            <p className="text-small text-muted">
              Drag the handles onto the receipt's corners.
            </p>
            {error ? <ErrorState title="Scan failed" message={error} /> : null}
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button variant="secondary" onClick={handleAutoCorners}>
                Auto corners
              </Button>
              <Button variant="secondary" onClick={handleWholePhoto}>
                Use whole photo
              </Button>
              <Button variant="secondary" onClick={handleRetake}>
                Retake
              </Button>
              <Button
                variant="primary"
                loading={working}
                onClick={() => void handleConfirm()}
              >
                Confirm
              </Button>
            </div>
          </div>
        ) : null}

        {phase === 'warn' && warned && photo ? (
          <div className="flex flex-col gap-3">
            <div ref={warnMountRef} className="relative w-full" />
            <div
              role="alert"
              className="flex flex-col gap-1 rounded-card border border-warning/30 bg-warning-soft p-4"
            >
              <h3 className="text-body font-semibold text-warning">
                Check the scan before uploading
              </h3>
              <p className="text-small text-ink">
                {[
                  warned.warnings.blurry ? 'The scan may be blurry.' : '',
                  warned.warnings.dark ? 'The scan may be too dark.' : '',
                ]
                  .filter((sentence) => sentence !== '')
                  .join(' ')}
              </p>
            </div>
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button variant="secondary" onClick={handleRetake}>
                Retake
              </Button>
              <Button
                variant="primary"
                loading={working}
                onClick={() => void handleUploadAnyway()}
              >
                Upload anyway
              </Button>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** The static outline of one quad, in its own pixel space. */
function renderOutline(quad: Quad, width: number, height: number) {
  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      className="pointer-events-none absolute inset-0 h-full w-full"
    >
      <polygon
        points={quad.map((point) => `${point.x},${point.y}`).join(' ')}
        className="fill-none stroke-accent stroke-2"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
