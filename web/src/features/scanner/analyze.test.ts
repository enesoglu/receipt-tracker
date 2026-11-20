import { describe, expect, it, vi } from 'vitest';
import { browserScanAnalyzer } from './analyze';

/** RGBA bytes whose gray pixels follow one pattern per column. */
function rgbaFromGray(
  width: number,
  height: number,
  grayAt: (x: number) => number,
): Uint8ClampedArray {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    const gray = grayAt(i % width);
    data[i * 4] = gray;
    data[i * 4 + 1] = gray;
    data[i * 4 + 2] = gray;
    data[i * 4 + 3] = 255;
  }
  return data;
}

/** The canvas the analyzer is handed, at a camera-like size. */
function scanCanvas(): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = 1600;
  canvas.height = 1200;
  return canvas;
}

/**
 * Installs a 2D context fake that answers the given RGBA bytes and
 * records every read, then hands back the uninstaller.
 */
function fakeContext(data: Uint8ClampedArray): {
  drawCalls: Array<{
    source: HTMLCanvasElement;
    dx: number;
    dy: number;
    width: number;
    height: number;
  }>;
  readCalls: Array<{ x: number; y: number; width: number; height: number }>;
  restore: () => void;
} {
  const drawCalls: Array<{
    source: HTMLCanvasElement;
    dx: number;
    dy: number;
    width: number;
    height: number;
  }> = [];
  const readCalls: Array<{
    x: number;
    y: number;
    width: number;
    height: number;
  }> = [];
  // One narrow cast: the fake answers exactly the two calls the
  // analyzer makes; everything else stays the jsdom default.
  const context = {
    drawImage(
      source: HTMLCanvasElement,
      dx: number,
      dy: number,
      width: number,
      height: number,
    ) {
      drawCalls.push({ source, dx, dy, width, height });
    },
    getImageData(x: number, y: number, width: number, height: number) {
      readCalls.push({ x, y, width, height });
      return { data, width, height } as unknown as ImageData;
    },
  } as unknown as CanvasRenderingContext2D;
  const spy = vi
    .spyOn(HTMLCanvasElement.prototype, 'getContext')
    .mockReturnValue(context);
  return { drawCalls, readCalls, restore: () => spy.mockRestore() };
}

describe('browserScanAnalyzer', () => {
  it('says nothing where no 2D context exists', () => {
    // jsdom ships no canvas context: the analyzer must answer with
    // no warnings instead of throwing at the user.
    expect(browserScanAnalyzer.analyze(scanCanvas())).toEqual({
      blurry: false,
      dark: false,
    });
  });

  it('judges the downscaled copy the browser would read', () => {
    const { drawCalls, readCalls, restore } = fakeContext(
      rgbaFromGray(512, 384, () => 30),
    );
    try {
      // Uniform gray 30: no edges, and far too dark.
      expect(browserScanAnalyzer.analyze(scanCanvas())).toEqual({
        blurry: true,
        dark: true,
      });
    } finally {
      restore();
    }

    // The frame was drawn once at the analysis size and read back
    // as one block of exactly that size.
    expect(drawCalls).toHaveLength(1);
    expect(drawCalls[0]?.dx).toBe(0);
    expect(drawCalls[0]?.dy).toBe(0);
    expect(drawCalls[0]?.width).toBe(512);
    expect(drawCalls[0]?.height).toBe(384);
    expect(readCalls).toEqual([{ x: 0, y: 0, width: 512, height: 384 }]);
  });

  it('stays silent for a sharp, bright frame', () => {
    // The exact stripe pattern quality.test.ts pins at a variance
    // of 100: at the threshold, so no warning is raised.
    const { restore } = fakeContext(
      rgbaFromGray(512, 384, (x) => 128 + (x % 4 < 2 ? -5 : 5)),
    );
    try {
      expect(browserScanAnalyzer.analyze(scanCanvas())).toEqual({
        blurry: false,
        dark: false,
      });
    } finally {
      restore();
    }
  });
});
