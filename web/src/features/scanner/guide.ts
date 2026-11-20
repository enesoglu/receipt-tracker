/**
 * The live view's silent paper guide: the camera is sampled at most
 * twice per second — a hint of where the paper is, never a tracker.
 * A detection miss is simply no hint, and so is a frame the
 * environment cannot draw.
 */
import type { Quad } from './corners';
import { detectQuad, type ScanEngine } from './engine';

/** The sampling interval: at most twice per second, the rate the
 * spike's per-frame detection cost measured out to. */
export const LIVE_SAMPLE_INTERVAL_MS = 500;

/**
 * One live sample: the current frame drawn once at its native
 * resolution, one detection pass, then the corner policy. Null when
 * the video has no frame, holds no paper, or the policy refuses the
 * detection — all silent.
 */
export async function sampleGuideQuad(
  engine: ScanEngine,
  video: HTMLVideoElement,
): Promise<Quad | null> {
  const width = video.videoWidth;
  const height = video.videoHeight;
  if (width === 0 || height === 0) {
    return null;
  }
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  // jsdom has no 2D context: the guard keeps the sample silent
  // instead of throwing where tests run. The frame is only the
  // detection's input, never kept.
  const context = canvas.getContext('2d');
  if (context) {
    context.drawImage(video, 0, 0);
  }
  return detectQuad(await engine.detect(canvas));
}
