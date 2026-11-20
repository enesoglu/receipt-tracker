import { describe, expect, it } from 'vitest';
import type { Quad } from './corners';
import { LIVE_SAMPLE_INTERVAL_MS, sampleGuideQuad } from './guide';
import type { FrameDetection, ScanEngine } from './engine';

/** A video element carrying a frame size jsdom never populates. */
function makeVideo(width: number, height: number): HTMLVideoElement {
  const video = document.createElement('video');
  Object.defineProperty(video, 'videoWidth', {
    value: width,
    configurable: true,
  });
  Object.defineProperty(video, 'videoHeight', {
    value: height,
    configurable: true,
  });
  return video;
}

/** An engine whose detection the test controls, recording the frame
 * it was handed. */
function makeEngine(detection: FrameDetection): {
  engine: ScanEngine;
  seenFrames: HTMLCanvasElement[];
} {
  const seenFrames: HTMLCanvasElement[] = [];
  return {
    seenFrames,
    engine: {
      async detect(frame) {
        seenFrames.push(frame);
        return detection;
      },
      extractPaper() {
        throw new Error('The live guide never warps.');
      },
      terminate() {},
    },
  };
}

/** The classified corners of an axis-aligned sheet. */
function sheetQuad(): Quad {
  return [
    { x: 100, y: 80 },
    { x: 900, y: 80 },
    { x: 900, y: 720 },
    { x: 100, y: 720 },
  ];
}

/** The jscanify classification of `quad`, as a detection would give it. */
function classified(quad: Quad): FrameDetection {
  return {
    classified: {
      topLeftCorner: quad[0],
      topRightCorner: quad[1],
      bottomRightCorner: quad[2],
      bottomLeftCorner: quad[3],
    },
    points: null,
  };
}

describe('LIVE_SAMPLE_INTERVAL_MS', () => {
  it('samples the live view at most twice per second', () => {
    expect(LIVE_SAMPLE_INTERVAL_MS).toBe(500);
  });
});

describe('sampleGuideQuad', () => {
  it('detects the quad of the current frame, at native size', async () => {
    const video = makeVideo(1280, 720);
    const quad = sheetQuad();
    const { engine, seenFrames } = makeEngine(classified(quad));

    expect(await sampleGuideQuad(engine, video)).toEqual(quad);
    expect(seenFrames).toHaveLength(1);
    expect(seenFrames[0]?.width).toBe(1280);
    expect(seenFrames[0]?.height).toBe(720);
  });

  it('keeps sampling where no 2D context exists, silently', async () => {
    const video = makeVideo(1280, 720);
    const quad = sheetQuad();
    const { engine, seenFrames } = makeEngine(classified(quad));

    // jsdom draws nothing, and the guide still answers from the
    // detection — the draw guard must never become a failure.
    expect(await sampleGuideQuad(engine, video)).toEqual(quad);
    expect(seenFrames).toHaveLength(1);
  });

  it('answers null for a video without a frame', async () => {
    const video = makeVideo(0, 0);
    const { engine, seenFrames } = makeEngine(classified(sheetQuad()));

    expect(await sampleGuideQuad(engine, video)).toBeNull();
    expect(seenFrames).toHaveLength(0);
  });

  it('answers null when the frame holds no paper', async () => {
    const video = makeVideo(1280, 720);
    const { engine } = makeEngine({ classified: null, points: null });

    expect(await sampleGuideQuad(engine, video)).toBeNull();
  });

  it('answers null when the corner policy refuses the detection', async () => {
    const video = makeVideo(1280, 720);
    // A contour degenerated to a line: neither corner path yields a
    // usable quad.
    const { engine } = makeEngine({
      classified: null,
      points: [0, 0, 1, 1, 2, 2, 3, 3],
    });

    expect(await sampleGuideQuad(engine, video)).toBeNull();
  });
});
