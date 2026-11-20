/**
 * Pure quality metrics for a captured scan, over a grayscale analysis
 * raster converted from a downscaled copy of the frame — at most
 * 512 px on the longer side. The stored bytes are never touched:
 * analysis only. Everything here runs on plain pixel arrays, so the
 * tests feed synthetic buffers.
 */

/** A grayscale raster: one 0–255 byte per pixel, row-major. */
export interface GrayRaster {
  width: number;
  height: number;
  data: Uint8ClampedArray;
}

/** The longest side the analysis raster may have: quality needs no
 * more resolution than this, so the metrics' cost stays flat. */
export const ANALYSIS_MAX_SIDE = 512;

/**
 * Warn when the variance of the Laplacian falls below this: the frame
 * reads as blurred. Calibrated on the spike's frames — a sharp
 * untextured sheet stays 2.4× above it, 3 px of gaussian blur lands
 * at half of it.
 */
export const BLUR_THRESHOLD = 100;

/**
 * Warn when mean brightness falls below this (0–255): the frame reads
 * as dark. Calibrated on the spike's frames — a ×0.55 exposure lands
 * 15 % below it, a ×0.4 exposure at 62, which a lower threshold would
 * have missed.
 */
export const DARK_THRESHOLD = 100;

/** The warnings a scan's quality raises before it is queued. They never
 * block an upload; they invite a retake. */
export interface ScanWarnings {
  blurry: boolean;
  dark: boolean;
}

/**
 * The size to draw an analysis raster at: the longer side scaled down
 * to at most 512 px, the aspect kept, nothing upscaled.
 */
export function analysisSize(
  width: number,
  height: number,
): { width: number; height: number } {
  const longer = Math.max(width, height);
  if (longer <= ANALYSIS_MAX_SIDE) {
    return { width, height };
  }
  const scale = ANALYSIS_MAX_SIDE / longer;
  return {
    width: Math.max(Math.round(width * scale), 1),
    height: Math.max(Math.round(height * scale), 1),
  };
}

/**
 * Converts the RGBA pixels of a downscaled frame to the grayscale
 * raster the metrics read: the standard luminance weights.
 */
export function rasterFromRGBA(
  rgba: Uint8ClampedArray,
  width: number,
  height: number,
): GrayRaster {
  const data = new Uint8ClampedArray(width * height);
  for (let i = 0; i < data.length; i++) {
    data[i] =
      0.299 * rgba[i * 4] + 0.587 * rgba[i * 4 + 1] + 0.114 * rgba[i * 4 + 2];
  }
  return { width, height, data };
}

/** Mean brightness of the raster, 0–255. */
export function meanBrightness(raster: GrayRaster): number {
  if (raster.data.length === 0) {
    return 0;
  }
  let sum = 0;
  for (const value of raster.data) {
    sum += value;
  }
  return sum / raster.data.length;
}

/**
 * Variance of the discrete Laplacian: the classic blur measure. A
 * sharp edge answers with a large response, smooth blur with near
 * zero; the variance over the frame separates the two. The border is
 * skipped, where responses would lean on missing neighbours.
 */
export function varianceOfLaplacian(raster: GrayRaster): number {
  const { width, height, data } = raster;
  if (width < 3 || height < 3) {
    return 0;
  }
  const responses = new Float64Array((width - 2) * (height - 2));
  let k = 0;
  for (let y = 1; y < height - 1; y++) {
    for (let x = 1; x < width - 1; x++) {
      const center = data[y * width + x];
      responses[k] =
        data[(y - 1) * width + x] +
        data[(y + 1) * width + x] +
        data[y * width + x - 1] +
        data[y * width + x + 1] -
        4 * center;
      k++;
    }
  }
  let mean = 0;
  for (const response of responses) {
    mean += response;
  }
  mean /= responses.length;
  let variance = 0;
  for (const response of responses) {
    variance += (response - mean) ** 2;
  }
  return variance / responses.length;
}

/** Judges the raster by the calibrated thresholds: a warning is raised
 * only when a metric falls strictly below its constant. */
export function scanWarnings(raster: GrayRaster): ScanWarnings {
  return {
    blurry: varianceOfLaplacian(raster) < BLUR_THRESHOLD,
    dark: meanBrightness(raster) < DARK_THRESHOLD,
  };
}
