// Dedicated worker for the scanner engine. Served from one fixed path
// (/scanner-worker.js) so its response carries its own CSP that admits
// 'unsafe-eval' for the pinned OpenCV UMD, which calls new Function
// during initialisation. Nothing here is bundled into the app's chunks.
self.onmessage = function (event) {
  const message = event.data;
  if (!message || typeof message !== 'object') return;

  if (message.type === 'init') {
    try {
      importScripts(message.jscanifyUrl);
      importScripts(message.opencvUrl);
      const started = Date.now();
      const interval = setInterval(function () {
        const cv = self.cv;
        if (cv && cv.Mat !== undefined) {
          clearInterval(interval);
          self.__cv = cv;
          self.__scanner = new self.jscanify();
          self.postMessage({ id: message.id, type: 'ready' });
        } else if (Date.now() - started >= 20000) {
          clearInterval(interval);
          self.postMessage({
            id: message.id,
            type: 'error',
            message: 'The scanning engine did not become ready in time.',
          });
        }
      }, 100);
    } catch (error) {
      self.postMessage({
        id: message.id,
        type: 'error',
        message: String(error && error.message ? error.message : error),
      });
    }
    return;
  }

  const cv = self.__cv;
  const scanner = self.__scanner;
  if (!cv || !scanner) {
    self.postMessage({
      id: message.id,
      type: 'error',
      message: 'The scanning engine is not ready.',
    });
    return;
  }

  if (message.type === 'detect') {
    let image = null;
    let contour = null;
    try {
      const data = new Uint8ClampedArray(message.buffer);
      const imageData = new ImageData(data, message.width, message.height);
      image = cv.matFromImageData(imageData);
      contour = scanner.findPaperContour(image);
      let classified = null;
      let points = null;
      if (contour) {
        classified = scanner.getCornerPoints(contour);
        points = Array.from(contour.data32S);
        contour.delete();
        contour = null;
      }
      image.delete();
      image = null;
      self.postMessage({
        id: message.id,
        type: 'detect-result',
        classified: classified,
        points: points,
      });
    } catch (error) {
      if (contour) {
        try {
          contour.delete();
        } catch {
          // ignore
        }
      }
      if (image) {
        try {
          image.delete();
        } catch {
          // ignore
        }
      }
      self.postMessage({
        id: message.id,
        type: 'error',
        message: String(error && error.message ? error.message : error),
      });
    }
    return;
  }

  if (message.type === 'warp') {
    let src = null;
    let srcPts = null;
    let dstPts = null;
    let matrix = null;
    let dst = null;
    try {
      const data = new Uint8ClampedArray(message.buffer);
      const imageData = new ImageData(data, message.width, message.height);
      src = cv.matFromImageData(imageData);
      const quad = message.corners;
      srcPts = cv.matFromArray(4, 1, cv.CV_32FC2, [
        quad[0].x,
        quad[0].y,
        quad[1].x,
        quad[1].y,
        quad[2].x,
        quad[2].y,
        quad[3].x,
        quad[3].y,
      ]);
      dstPts = cv.matFromArray(4, 1, cv.CV_32FC2, [
        0,
        0,
        message.targetWidth,
        0,
        message.targetWidth,
        message.targetHeight,
        0,
        message.targetHeight,
      ]);
      matrix = cv.getPerspectiveTransform(srcPts, dstPts);
      dst = new cv.Mat();
      const dsize = new cv.Size(message.targetWidth, message.targetHeight);
      cv.warpPerspective(src, dst, matrix, dsize);
      const out = new Uint8ClampedArray(dst.data.slice());
      const buffer = out.buffer;
      src.delete();
      src = null;
      srcPts.delete();
      srcPts = null;
      dstPts.delete();
      dstPts = null;
      matrix.delete();
      matrix = null;
      dst.delete();
      dst = null;
      self.postMessage(
        {
          id: message.id,
          type: 'warp-result',
          buffer: buffer,
          width: message.targetWidth,
          height: message.targetHeight,
        },
        [buffer],
      );
    } catch (error) {
      if (src)
        try {
          src.delete();
        } catch {
          /* ignore */
        }
      if (srcPts)
        try {
          srcPts.delete();
        } catch {
          /* ignore */
        }
      if (dstPts)
        try {
          dstPts.delete();
        } catch {
          /* ignore */
        }
      if (matrix)
        try {
          matrix.delete();
        } catch {
          /* ignore */
        }
      if (dst)
        try {
          dst.delete();
        } catch {
          /* ignore */
        }
      self.postMessage({
        id: message.id,
        type: 'error',
        message: String(error && error.message ? error.message : error),
      });
    }
    return;
  }
};
