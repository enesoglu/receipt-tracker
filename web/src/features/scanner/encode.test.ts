import { describe, expect, it } from 'vitest';
import {
  encodeJpeg,
  PHOTO_JPEG_QUALITY,
  SCAN_JPEG_QUALITY,
  type EncodableCanvas,
} from './encode';

/** A canvas fake that records the encode and answers the blob. */
function canvasAnswering(blob: Blob | null): {
  canvas: EncodableCanvas;
  calls: Array<{ type: string; quality: number }>;
} {
  const calls: Array<{ type: string; quality: number }> = [];
  return {
    calls,
    canvas: {
      toBlob(callback, type, quality) {
        calls.push({ type, quality });
        callback(blob);
      },
    },
  };
}

describe('capture quality constants', () => {
  it('keeps the decided photo and scan qualities', () => {
    expect(PHOTO_JPEG_QUALITY).toBe(0.95);
    expect(SCAN_JPEG_QUALITY).toBe(0.92);
  });
});

describe('encodeJpeg', () => {
  it('encodes once as JPEG at the given quality and names the file', async () => {
    const { canvas, calls } = canvasAnswering(new Blob(['frame']));

    const file = await encodeJpeg(canvas, SCAN_JPEG_QUALITY, 'scan-1.jpg');

    expect(calls).toEqual([{ type: 'image/jpeg', quality: 0.92 }]);
    expect(file.name).toBe('scan-1.jpg');
    expect(file.type).toBe('image/jpeg');
    expect(await file.text()).toBe('frame');
  });

  it('rejects when the browser answers with no blob', async () => {
    const { canvas } = canvasAnswering(null);

    await expect(
      encodeJpeg(canvas, PHOTO_JPEG_QUALITY, 'photo-1.jpg'),
    ).rejects.toThrow('photo-1.jpg');
  });
});
