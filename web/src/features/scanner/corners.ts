/**
 * Pure corner geometry for the document scanner: reordering the corners
 * jscanify classifies, judging whether four corners form a usable quad,
 * and the extreme-point fallback for contours whose corners the
 * quadrant classification cannot find. Everything here runs on plain
 * numbers — no canvas, no OpenCV — so the tests feed synthetic data.
 */

/** One point in image coordinates. */
export interface Point {
  x: number;
  y: number;
}

/** Four paper corners in canonical order: top-left, top-right,
 * bottom-right, bottom-left. */
export type Quad = [Point, Point, Point, Point];

/**
 * The corner names jscanify's `getCornerPoints` returns. Any of them
 * may be missing: the classification keeps only points that lie in the
 * expected quadrant around the contour's centre, which is exactly what
 * defeats a sheet rotated toward 45°.
 */
export interface RawCorners {
  topLeftCorner?: Point;
  topRightCorner?: Point;
  bottomLeftCorner?: Point;
  bottomRightCorner?: Point;
}

/**
 * Reorders jscanify's corner names into the canonical TL/TR/BR/BL
 * order, or null when any corner is missing.
 */
export function orderCorners(raw: RawCorners): Quad | null {
  const { topLeftCorner, topRightCorner, bottomRightCorner, bottomLeftCorner } =
    raw;
  if (
    !topLeftCorner ||
    !topRightCorner ||
    !bottomRightCorner ||
    !bottomLeftCorner
  ) {
    return null;
  }
  return [topLeftCorner, topRightCorner, bottomRightCorner, bottomLeftCorner];
}

/**
 * True when segment a–b crosses segment c–d, collinear overlaps
 * included: each pair of endpoints on opposite sides of the other
 * segment, or one endpoint on it with all four points collinear.
 */
export function segmentsIntersect(
  a: Point,
  b: Point,
  c: Point,
  d: Point,
): boolean {
  const turn1 = orientation(c, d, a);
  const turn2 = orientation(c, d, b);
  const turn3 = orientation(a, b, c);
  const turn4 = orientation(a, b, d);
  if (
    ((turn1 > 0 && turn2 < 0) || (turn1 < 0 && turn2 > 0)) &&
    ((turn3 > 0 && turn4 < 0) || (turn3 < 0 && turn4 > 0))
  ) {
    return true;
  }
  return (
    (turn1 === 0 && between(c, d, a)) ||
    (turn2 === 0 && between(c, d, b)) ||
    (turn3 === 0 && between(a, b, c)) ||
    (turn4 === 0 && between(a, b, d))
  );
}

/**
 * True when the quad's boundary does not cross itself: a shape fit for
 * the overlay's handles and for the warp. Only the two pairs of
 * opposite edges are tested — adjacent edges always share a corner, and
 * the diagonals of almost any four points cross, so the diagonal form
 * of this test answers the opposite of what is wanted. A concave quad
 * stays simple: its handles can still be dragged into place, so only
 * the crossed shape is refused.
 */
export function isSimpleQuad(quad: Quad): boolean {
  const [topLeft, topRight, bottomRight, bottomLeft] = quad;
  return (
    !segmentsIntersect(topLeft, topRight, bottomRight, bottomLeft) &&
    !segmentsIntersect(topRight, bottomRight, bottomLeft, topLeft)
  );
}

/**
 * Extreme-point corners of a contour given as the flat
 * x0, y0, x1, y1, … list OpenCV's contour Mats carry: the points where
 * x+y is smallest and largest (top-left, bottom-right) and where x−y
 * is largest and smallest (top-right, bottom-left). Rotation toward
 * 45° cannot hide a sheet's corners there, which is where jscanify's
 * quadrant classification loses them. Null when the contour has fewer
 * than four points. Ties keep the earliest point, so the result is
 * deterministic.
 */
export function cornersFromContour(flat: ArrayLike<number>): Quad | null {
  const count = Math.floor(flat.length / 2);
  if (count < 4) {
    return null;
  }
  let topLeft = pointAt(flat, 0);
  let bottomRight = topLeft;
  let topRight = topLeft;
  let bottomLeft = topLeft;
  for (let i = 1; i < count; i++) {
    const point = pointAt(flat, i * 2);
    if (point.x + point.y < topLeft.x + topLeft.y) {
      topLeft = point;
    }
    if (point.x + point.y > bottomRight.x + bottomRight.y) {
      bottomRight = point;
    }
    if (point.x - point.y > topRight.x - topRight.y) {
      topRight = point;
    }
    if (point.x - point.y < bottomLeft.x - bottomLeft.y) {
      bottomLeft = point;
    }
  }
  return [topLeft, topRight, bottomRight, bottomLeft];
}

/** The floor for a scan's longer side: below this the text a receipt
 * prints is unreadable, so the warp scales up to it. */
export const MIN_SCAN_LONGER_SIDE = 480;

/**
 * The warp's output size for a chosen quad: the rounded means of the
 * two horizontal and the two vertical edge lengths — the paper's own
 * size as the user framed it. The longer side never falls below
 * 480 px, so a far-away sheet still scans readably, and neither side
 * ever exceeds the source frame, whose pixels are all there are to
 * sample.
 */
export function scanDimensions(
  quad: Quad,
  source: { width: number; height: number },
): { width: number; height: number } {
  const [topLeft, topRight, bottomRight, bottomLeft] = quad;
  let width = Math.round(
    (distance(topLeft, topRight) + distance(bottomLeft, bottomRight)) / 2,
  );
  let height = Math.round(
    (distance(topLeft, bottomLeft) + distance(topRight, bottomRight)) / 2,
  );
  const longer = Math.max(width, height);
  if (longer > 0 && longer < MIN_SCAN_LONGER_SIDE) {
    const scale = MIN_SCAN_LONGER_SIDE / longer;
    width = Math.round(width * scale);
    height = Math.round(height * scale);
  }
  return {
    width: Math.min(Math.max(width, 1), Math.max(source.width, 1)),
    height: Math.min(Math.max(height, 1), Math.max(source.height, 1)),
  };
}

/** How far the default handles sit inside the frame, as a share of
 * each side: a tenth in from every edge. */
export const DEFAULT_INSET_FRACTION = 0.1;

/**
 * The handle starting quad when detection finds no paper: an
 * axis-aligned rectangle a tenth in from every edge of the frame, so
 * the handles still begin where a receipt most likely is and the
 * first drag starts close to the paper.
 */
export function defaultQuad(frame: { width: number; height: number }): Quad {
  const x = DEFAULT_INSET_FRACTION * frame.width;
  const y = DEFAULT_INSET_FRACTION * frame.height;
  return [
    { x, y },
    { x: frame.width - x, y },
    { x: frame.width - x, y: frame.height - y },
    { x, y: frame.height - y },
  ];
}

/** Where c lies around the directed line a→b: positive to its left,
 * negative to its right, zero on it. */
function orientation(a: Point, b: Point, c: Point): number {
  return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}

/** True when p lies on segment a–b, given all three collinear. */
function between(a: Point, b: Point, p: Point): boolean {
  return (
    p.x >= Math.min(a.x, b.x) &&
    p.x <= Math.max(a.x, b.x) &&
    p.y >= Math.min(a.y, b.y) &&
    p.y <= Math.max(a.y, b.y)
  );
}

/** The length of segment a–b. */
function distance(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

/** Point i of a flat x, y list. */
function pointAt(flat: ArrayLike<number>, offset: number): Point {
  return { x: flat[offset] ?? 0, y: flat[offset + 1] ?? 0 };
}
