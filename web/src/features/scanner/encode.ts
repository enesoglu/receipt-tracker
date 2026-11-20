/**
 * The scanner's JPEG encoding: every captured image becomes bytes
 * exactly once, through one narrow canvas seam.
 *
 * The photo keeps the quality it was captured with, because its bytes
 * are stored as the upload's original — the one deliberate exception
 * to "the browser never recompresses" (docs §5.1). The scan, already
 * a reprojection of those bytes, trades a little quality for size.
 */

/** JPEG quality of the untouched camera photo. */
export const PHOTO_JPEG_QUALITY = 0.95;

/** JPEG quality of the warped scan. */
export const SCAN_JPEG_QUALITY = 0.92;

/**
 * The narrow slice of a canvas the encoder needs. jsdom can neither
 * draw nor encode a canvas, so tests fake exactly this shape.
 */
export interface EncodableCanvas {
  /** The browser's one-step encode; the wrapper makes it a promise. */
  toBlob(
    callback: (blob: Blob | null) => void,
    type: string,
    quality: number,
  ): void;
}

/**
 * Encodes a canvas once as JPEG and wraps the bytes as the named
 * file. Rejects when the browser answers with no blob — the capture
 * is over, never silently empty.
 */
export function encodeJpeg(
  canvas: EncodableCanvas,
  quality: number,
  fileName: string,
): Promise<File> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => {
        if (blob === null) {
          reject(new Error(`The image could not be encoded as ${fileName}.`));
          return;
        }
        resolve(new File([blob], fileName, { type: 'image/jpeg' }));
      },
      'image/jpeg',
      quality,
    );
  });
}
