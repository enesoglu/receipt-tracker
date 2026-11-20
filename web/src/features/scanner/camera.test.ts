import { describe, expect, it, vi } from 'vitest';
import {
  browserFrameCapture,
  deviceCamera,
  photoFileName,
  type CameraStream,
  type FrameCapture,
} from './camera';
import { PHOTO_JPEG_QUALITY, type EncodableCanvas } from './encode';

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

/** A stream that exposes its two tracks' stop spies. */
function makeStream() {
  const firstStop = vi.fn();
  const secondStop = vi.fn();
  return {
    stream: { getTracks: () => [{ stop: firstStop }, { stop: secondStop }] },
    firstStop,
    secondStop,
  };
}

/** A devices lookup answering the given stream, recording the call. */
function makeDevices(stream: CameraStream) {
  const getUserMedia = vi.fn(async () => stream);
  return {
    devices: vi.fn(() => ({ getUserMedia })),
    getUserMedia,
  };
}

/** A capture seam recording what the browser work was asked to do. */
function recordingCapture(): FrameCapture & {
  draws: Array<{ video: HTMLVideoElement; canvas: HTMLCanvasElement }>;
  encodes: Array<{ canvas: EncodableCanvas; fileName: string }>;
} {
  const draws: Array<{ video: HTMLVideoElement; canvas: HTMLCanvasElement }> =
    [];
  const encodes: Array<{ canvas: EncodableCanvas; fileName: string }> = [];
  return {
    draws,
    encodes,
    draw(video, canvas) {
      draws.push({ video, canvas });
    },
    async encode(canvas, fileName) {
      encodes.push({ canvas, fileName });
      return new File(['encoded'], fileName, { type: 'image/jpeg' });
    },
  };
}

/** Opens a source whose devices answer `stream`, resolving once the
 * fake metadata event has been dispatched. */
async function openSource(
  source: ReturnType<typeof deviceCamera>,
  video: HTMLVideoElement,
  stream: CameraStream,
): Promise<void> {
  const pending = source.open();
  await vi.waitFor(() => {
    expect(video.srcObject).toBe(stream);
  });
  video.dispatchEvent(new Event('loadedmetadata'));
  await pending;
}

describe('photoFileName', () => {
  it('names the photo after one UTC moment', () => {
    expect(photoFileName(new Date(Date.UTC(2026, 10, 20, 14, 25, 30)))).toBe(
      'photo-20261120-142530.jpg',
    );
  });
});

describe('deviceCamera.open', () => {
  it('reports the missing camera API of an old or test browser', async () => {
    const video = makeVideo(1280, 720);
    // jsdom ships no mediaDevices, so the default lookup finds none.
    const source = deviceCamera(video, recordingCapture());

    await expect(source.open()).rejects.toThrow(
      'This browser does not expose a camera API.',
    );
  });

  it('requests the rear-facing camera once and waits for its stream', async () => {
    const video = makeVideo(1280, 720);
    const { stream, firstStop, secondStop } = makeStream();
    const { devices, getUserMedia } = makeDevices(stream);
    const source = deviceCamera(video, recordingCapture(), devices);

    await openSource(source, video, stream);

    expect(getUserMedia).toHaveBeenCalledTimes(1);
    expect(getUserMedia).toHaveBeenCalledWith({
      audio: false,
      video: {
        facingMode: 'environment',
        width: { ideal: 1920 },
        height: { ideal: 1080 },
      },
    });
    expect(firstStop).not.toHaveBeenCalled();
    expect(secondStop).not.toHaveBeenCalled();
  });

  it('propagates a refused permission', async () => {
    const video = makeVideo(1280, 720);
    const getUserMedia = vi.fn(async () => {
      throw new Error('Permission denied');
    });
    const source = deviceCamera(video, recordingCapture(), () => ({
      getUserMedia,
    }));

    await expect(source.open()).rejects.toThrow('Permission denied');
  });

  it('stops a stream that arrives after the overlay closed', async () => {
    const video = makeVideo(1280, 720);
    const { stream, firstStop, secondStop } = makeStream();
    let grantPermission!: (value: CameraStream) => void;
    const source = deviceCamera(video, recordingCapture(), () => ({
      getUserMedia: () =>
        new Promise<CameraStream>((resolve) => {
          grantPermission = resolve;
        }),
    }));
    const pending = source.open();

    source.close();
    grantPermission(stream);

    await expect(pending).rejects.toThrow(
      'The camera was closed before it opened.',
    );
    expect(firstStop).toHaveBeenCalledTimes(1);
    expect(secondStop).toHaveBeenCalledTimes(1);
  });
});

describe('deviceCamera.snapshot', () => {
  it('refuses to capture before the camera opens', async () => {
    const source = deviceCamera(
      makeVideo(1280, 720),
      recordingCapture(),
      () => undefined,
    );

    await expect(source.snapshot()).rejects.toThrow('The camera is not open.');
  });

  it('captures the full frame once and encodes it once', async () => {
    const video = makeVideo(1280, 720);
    const { stream } = makeStream();
    const { devices } = makeDevices(stream);
    const capture = recordingCapture();
    const source = deviceCamera(video, capture, devices);
    await openSource(source, video, stream);

    const shot = await source.snapshot();

    expect(shot.canvas.width).toBe(1280);
    expect(shot.canvas.height).toBe(720);
    expect(capture.draws).toEqual([{ video, canvas: shot.canvas }]);
    expect(capture.encodes).toHaveLength(1);
    expect(capture.encodes[0]?.canvas).toBe(shot.canvas);
    expect(capture.encodes[0]?.fileName).toMatch(/^photo-\d{8}-\d{6}\.jpg$/);
    expect(shot.photo.name).toBe(capture.encodes[0]?.fileName);
    expect(shot.photo.type).toBe('image/jpeg');
  });
});

describe('deviceCamera.close', () => {
  it('stops every track and detaches the element', async () => {
    const video = makeVideo(1280, 720);
    const { stream, firstStop, secondStop } = makeStream();
    const { devices } = makeDevices(stream);
    const source = deviceCamera(video, recordingCapture(), devices);
    await openSource(source, video, stream);

    source.close();

    expect(firstStop).toHaveBeenCalledTimes(1);
    expect(secondStop).toHaveBeenCalledTimes(1);
    expect(video.srcObject).toBeNull();
  });

  it('is safe before the camera ever opens', async () => {
    const video = makeVideo(1280, 720);
    const { stream, firstStop, secondStop } = makeStream();
    const { devices } = makeDevices(stream);
    const source = deviceCamera(video, recordingCapture(), devices);

    source.close();
    await expect(source.open()).rejects.toThrow(
      'The camera was closed before it opened.',
    );

    expect(firstStop).toHaveBeenCalledTimes(1);
    expect(secondStop).toHaveBeenCalledTimes(1);
  });
});

describe('browserFrameCapture', () => {
  it('encodes the photo at the decided quality', async () => {
    const calls: Array<{ type: string; quality: number }> = [];
    const canvas: EncodableCanvas = {
      toBlob(callback, type, quality) {
        calls.push({ type, quality });
        callback(new Blob(['frame']));
      },
    };

    const photo = await browserFrameCapture.encode(canvas, 'photo-1.jpg');

    expect(calls).toEqual([{ type: 'image/jpeg', quality: 0.95 }]);
    expect(photo).toBeInstanceOf(File);
    expect(photo.name).toBe('photo-1.jpg');
    expect(photo.type).toBe('image/jpeg');
    expect(PHOTO_JPEG_QUALITY).toBe(0.95);
  });

  it('stays silent where no 2D context exists', () => {
    expect(() =>
      browserFrameCapture.draw(
        makeVideo(1280, 720),
        document.createElement('canvas'),
      ),
    ).not.toThrow();
  });
});
