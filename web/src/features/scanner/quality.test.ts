import { describe, expect, it } from 'vitest';
import {
  ANALYSIS_MAX_SIDE,
  BLUR_THRESHOLD,
  DARK_THRESHOLD,
  analysisSize,
  meanBrightness,
  rasterFromRGBA,
  scanWarnings,
  varianceOfLaplacian,
  type GrayRaster,
} from './quality';

/** A grayscale raster drawn by the given per-pixel function. */
function raster(
  width: number,
  height: number,
  value: (x: number, y: number) => number,
): GrayRaster {
  const data = new Uint8ClampedArray(width * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      data[y * width + x] = value(x, y);
    }
  }
  return { width, height, data };
}

/** A raster of one brightness everywhere. */
function uniformRaster(
  width: number,
  height: number,
  value: number,
): GrayRaster {
  return {
    width,
    height,
    data: new Uint8ClampedArray(width * height).fill(value),
  };
}

describe('quality thresholds', () => {
  it('keeps the calibrated constants', () => {
    expect(BLUR_THRESHOLD).toBe(100);
    expect(DARK_THRESHOLD).toBe(100);
    expect(ANALYSIS_MAX_SIDE).toBe(512);
  });
});

describe('analysisSize', () => {
  it('keeps a frame already within the bound, whatever its shape', () => {
    expect(analysisSize(400, 300)).toEqual({ width: 400, height: 300 });
    expect(analysisSize(512, 384)).toEqual({ width: 512, height: 384 });
  });

  it('scales a landscape frame down, aspect kept', () => {
    expect(analysisSize(1600, 1200)).toEqual({ width: 512, height: 384 });
  });

  it('scales a portrait frame down, aspect kept', () => {
    expect(analysisSize(900, 1600)).toEqual({ width: 288, height: 512 });
  });
});

describe('rasterFromRGBA', () => {
  it('applies the luminance weights to each pixel', () => {
    const raster = rasterFromRGBA(
      new Uint8ClampedArray([
        255,
        0,
        0,
        255, //
        0,
        255,
        0,
        255, //
        0,
        0,
        255,
        255, //
        255,
        255,
        255,
        255, //
        0,
        0,
        0,
        255, //
        128,
        128,
        128,
        255, //
      ]),
      1,
      6,
    );
    expect(Array.from(raster.data)).toEqual([
      76, // 0.299 × 255
      150, // 0.587 × 255
      29, // 0.114 × 255
      255,
      0,
      128,
    ]);
  });

  it('ignores the alpha channel', () => {
    const raster = rasterFromRGBA(
      new Uint8ClampedArray([255, 0, 0, 0, 0, 255, 0, 0]),
      2,
      1,
    );
    expect(Array.from(raster.data)).toEqual([76, 150]);
  });
});

describe('varianceOfLaplacian', () => {
  it('answers 256 for a checkerboard of amplitude 2', () => {
    // Every interior pixel's four neighbours are the opposite colour, so
    // each response is ±8 × amplitude with both signs equally common.
    const checkerboard = raster(
      8,
      8,
      (x, y) => 128 + ((x + y) % 2 === 0 ? 2 : -2),
    );
    expect(varianceOfLaplacian(checkerboard)).toBe(256);
  });

  it('answers exactly 100 for period-4 stripes of amplitude 5', () => {
    // The stripes' second difference is ±2 × amplitude at every interior
    // pixel, both signs equally common — the threshold's exact value.
    const stripes = raster(16, 12, (x) => 128 + (x % 4 < 2 ? -5 : 5));
    expect(varianceOfLaplacian(stripes)).toBe(100);
  });

  it('answers 0 for a linear ramp', () => {
    const ramp = raster(8, 6, (x) => x * 10);
    expect(varianceOfLaplacian(ramp)).toBe(0);
  });

  it('answers 0 for a uniform raster', () => {
    expect(varianceOfLaplacian(uniformRaster(8, 6, 128))).toBe(0);
  });

  it('answers 0 when the raster has no interior', () => {
    expect(varianceOfLaplacian(uniformRaster(2, 2, 128))).toBe(0);
    expect(varianceOfLaplacian(uniformRaster(1, 9, 128))).toBe(0);
  });
});

describe('scanWarnings', () => {
  it('raises nothing for a sharp, bright frame', () => {
    const sharp = raster(8, 8, (x, y) => 128 + ((x + y) % 2 === 0 ? 2 : -2));
    expect(scanWarnings(sharp)).toEqual({ blurry: false, dark: false });
  });

  it('warns on blur only when the variance falls strictly below the bound', () => {
    // Exactly at the threshold: no warning.
    const atBound = raster(16, 12, (x) => 128 + (x % 4 < 2 ? -5 : 5));
    expect(scanWarnings(atBound).blurry).toBe(false);
    // Below it: warned.
    const belowBound = raster(16, 12, (x) => 128 + (x % 4 < 2 ? -4 : 4));
    expect(scanWarnings(belowBound).blurry).toBe(true);
  });

  it.each([99, 100, 101])(
    'warns on brightness %d against the dark bound',
    (brightness) => {
      const flat = uniformRaster(8, 6, brightness);
      expect(meanBrightness(flat)).toBe(brightness);
      expect(scanWarnings(flat).dark).toBe(brightness < DARK_THRESHOLD);
    },
  );

  it('raises both warnings for a dark, empty frame', () => {
    expect(scanWarnings(uniformRaster(8, 6, 50))).toEqual({
      blurry: true,
      dark: true,
    });
  });
});
