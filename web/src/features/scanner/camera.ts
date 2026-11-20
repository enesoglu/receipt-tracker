/**
 * The camera seam: one `getUserMedia` implementation behind a narrow
 * interface, so the overlay and its tests both choose where frames
 * come from.
 *
 * The photo is the full video frame drawn once to an offscreen canvas
 * at the stream's native resolution and encoded once as JPEG: no
 * crop, no perspective change, no tonal change. Browsers expose no raw
 * sensor bytes, so this frame is the most original copy that exists —
 * the docs' `original_ref` holds exactly these bytes.
 */
import { encodeJpeg, PHOTO_JPEG_QUALITY, type EncodableCanvas } from './encode';

/** The rear-facing camera the scanner wants, at a comfortable
 * resolution: `ideal` lets a desktop lens or the fake test device
 * fall back to whatever it actually has. */
const CONSTRAINTS: MediaStreamConstraints = {
  audio: false,
  video: {
    facingMode: 'environment',
    width: { ideal: 1920 },
    height: { ideal: 1080 },
  },
};

/** The stream as this source uses it: tracks it can stop. */
export interface CameraStream {
  getTracks(): Array<{ stop(): void }>;
}

/** The narrow mediaDevices slice the one getUserMedia call needs. */
interface CameraDevices {
  getUserMedia(constraints: MediaStreamConstraints): Promise<CameraStream>;
}

/** Where the one implementation looks the camera API up. */
function browserDevices(): CameraDevices | undefined {
  // One narrow cast: older browsers and jsdom leave mediaDevices
  // undefined, which the DOM types model as always present.
  return navigator.mediaDevices as unknown as CameraDevices | undefined;
}

/** One captured frame: the canvas the scanner works on, and the
 * untouched photo it is uploaded as. */
export interface PhotoCapture {
  canvas: HTMLCanvasElement;
  photo: File;
}

/** The camera of one overlay: open it, take the frame, close it. */
export interface CameraSource {
  /** Starts the stream into the video element; rejects when the
   * browser refuses or cannot open a camera. */
  open(): Promise<void>;
  /** The current frame at its native resolution, drawn once and
   * encoded once as the untouched photo. */
  snapshot(): Promise<PhotoCapture>;
  /** Stops the stream. Safe before, during or after an open. */
  close(): void;
}

/** The browser capture work behind one seam: jsdom can neither draw
 * a video frame nor encode a canvas, so tests fake both and assert
 * the composition instead. */
export interface FrameCapture {
  /** Draws the video's current frame once, at native resolution. */
  draw(video: HTMLVideoElement, canvas: HTMLCanvasElement): void;
  /** Encodes the canvas once as the untouched photo. */
  encode(canvas: EncodableCanvas, fileName: string): Promise<File>;
}

/** The guard keeps jsdom silent: without a 2D context there is
 * nothing to draw, and the tests fake this whole seam anyway. */
function drawVideoFrame(
  video: HTMLVideoElement,
  canvas: HTMLCanvasElement,
): void {
  const context = canvas.getContext('2d');
  if (context) {
    context.drawImage(video, 0, 0);
  }
}

/** The capture the browser takes: the decided photo quality. */
export const browserFrameCapture: FrameCapture = {
  draw: drawVideoFrame,
  encode: (canvas, fileName) =>
    encodeJpeg(canvas, PHOTO_JPEG_QUALITY, fileName),
};

/** The photo file name, `photo-<YYYYMMDD>-<HHMMSS>.jpg`: UTC keeps one
 * moment naming one file everywhere, matching the scan's pattern. */
export function photoFileName(when: Date): string {
  const iso = when.toISOString();
  return `photo-${iso.slice(0, 10).replaceAll('-', '')}-${iso
    .slice(11, 19)
    .replaceAll(':', '')}.jpg`;
}

/** Resolves once the element has the stream's dimensions. */
function metadataReady(video: HTMLVideoElement): Promise<void> {
  return new Promise((resolve) => {
    if (video.readyState >= 1) {
      resolve();
      return;
    }
    video.addEventListener(
      'loadedmetadata',
      () => {
        resolve();
      },
      { once: true },
    );
  });
}

/**
 * Opens the device camera into the given video element: the one
 * `getUserMedia` implementation, with its drawing and encoding
 * behind seams whose defaults do the browser's own work.
 */
export function deviceCamera(
  video: HTMLVideoElement,
  capture: FrameCapture = browserFrameCapture,
  devices: () => CameraDevices | undefined = browserDevices,
): CameraSource {
  let stream: CameraStream | null = null;
  let closed = false;

  return {
    async open() {
      const media = devices();
      if (!media) {
        throw new Error('This browser does not expose a camera API.');
      }
      const acquired = await media.getUserMedia(CONSTRAINTS);
      if (closed) {
        // The overlay went away while the permission prompt sat open:
        // the stream still belongs to us, so it is stopped here.
        acquired.getTracks().forEach((track) => {
          track.stop();
        });
        throw new Error('The camera was closed before it opened.');
      }
      stream = acquired;
      // The one narrow cast per browser API: the stream is a plain
      // track holder to this source; the element takes it as its
      // MediaStream.
      video.srcObject = acquired as unknown as MediaStream;
      await metadataReady(video);
    },

    async snapshot() {
      if (!stream) {
        throw new Error('The camera is not open.');
      }
      const width = video.videoWidth;
      const height = video.videoHeight;
      if (width === 0 || height === 0) {
        throw new Error('The camera has no frame to capture yet.');
      }
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      capture.draw(video, canvas);
      const photo = await capture.encode(canvas, photoFileName(new Date()));
      return { canvas, photo };
    },

    close() {
      closed = true;
      stream?.getTracks().forEach((track) => {
        track.stop();
      });
      stream = null;
      if (video.srcObject !== null) {
        video.srcObject = null;
      }
    },
  };
}
