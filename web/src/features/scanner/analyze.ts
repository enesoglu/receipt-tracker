/**
 * The scan's quality analysis: one downscaled grayscale read of the
 * warped canvas, judged by the calibrated constants — advice shown
 * before the queue, never a gate on the upload.
 *
 * The pixel read is canvas work jsdom cannot perform, so the one
 * browser implementation sits behind a seam the tests fake; the pure
 * metrics it feeds live in `quality.ts`, tested on synthetic buffers.
 */
import {
  analysisSize,
  rasterFromRGBA,
  scanWarnings,
  type GrayRaster,
  type ScanWarnings,
} from './quality';

/** The analysis seam: one warped scan's pixels in, its warnings out. */
export interface ScanAnalyzer {
  analyze(canvas: HTMLCanvasElement): ScanWarnings;
}

/**
 * Reads the analysis raster: the frame drawn once at the analysis
 * size — at most 512 px on the longer side, the aspect kept — and
 * its pixels taken as one block. Null where the environment has no
 * 2D context, so the advice stays silent instead of wrongly warning.
 */
function readRaster(canvas: HTMLCanvasElement): GrayRaster | null {
  const size = analysisSize(canvas.width, canvas.height);
  const small = document.createElement('canvas');
  small.width = size.width;
  small.height = size.height;
  // jsdom answers null here; the guard is what keeps the analyzer
  // silent under tests and in a browser without canvas support.
  const context = small.getContext('2d');
  if (!context) {
    return null;
  }
  context.drawImage(canvas, 0, 0, size.width, size.height);
  const image = context.getImageData(0, 0, size.width, size.height);
  return rasterFromRGBA(image.data, size.width, size.height);
}

/**
 * The browser analysis: judges the scan, and where no pixels can be
 * read says nothing at all — a warning that cannot be computed is
 * advice the scanner does not give.
 */
export const browserScanAnalyzer: ScanAnalyzer = {
  analyze(canvas) {
    const raster = readRaster(canvas);
    return raster === null
      ? { blurry: false, dark: false }
      : scanWarnings(raster);
  },
};
