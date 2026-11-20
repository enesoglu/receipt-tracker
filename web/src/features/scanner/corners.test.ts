import { describe, expect, it } from 'vitest';
import {
  cornersFromContour,
  DEFAULT_INSET_FRACTION,
  defaultQuad,
  isSimpleQuad,
  orderCorners,
  scanDimensions,
  segmentsIntersect,
  type Point,
  type Quad,
  type RawCorners,
} from './corners';

const point = (x: number, y: number): Point => ({ x, y });

/** A convex quad in canonical order, 100 wide and 60 tall. */
const rectangle = (): Quad => [
  point(0, 0),
  point(100, 0),
  point(100, 60),
  point(0, 60),
];

/** The flat x, y list of a contour drawn through the given corners,
 * with `perEdge` interpolated points between each pair. */
function contourThrough(corners: readonly Point[], perEdge: number): number[] {
  const flat: number[] = [];
  corners.forEach((from, i) => {
    // The wrap closes the contour; the fallback covers only the
    // degenerate empty-input case, which never reaches a step.
    const to = corners[(i + 1) % corners.length] ?? from;
    for (let step = 0; step < perEdge; step++) {
      const share = step / perEdge;
      flat.push(
        from.x + (to.x - from.x) * share,
        from.y + (to.y - from.y) * share,
      );
    }
  });
  return flat;
}

/**
 * A rectangle's contour, rotated by `degrees` around a centre: the
 * classic 44°-style frame whose corners quadrant classification loses.
 */
function rotatedRectangleContour(
  center: Point,
  halfWidth: number,
  halfHeight: number,
  degrees: number,
): { corners: Quad; flat: number[] } {
  const radians = (degrees * Math.PI) / 180;
  const alongX = { x: Math.cos(radians), y: Math.sin(radians) };
  const alongY = { x: -Math.sin(radians), y: Math.cos(radians) };
  const corner = (sx: number, sy: number): Point => ({
    x: center.x + sx * halfWidth * alongX.x + sy * halfHeight * alongY.x,
    y: center.y + sx * halfWidth * alongX.y + sy * halfHeight * alongY.y,
  });
  const corners: Quad = [
    corner(-1, -1),
    corner(1, -1),
    corner(1, 1),
    corner(-1, 1),
  ];
  return { corners, flat: contourThrough(corners, 12) };
}

/** The extreme of `pick` over the corners, the fallback's expected
 * corner for that role. */
function extreme(
  corners: readonly Point[],
  pick: (a: Point, b: Point) => Point,
): Point {
  return corners.reduce(pick);
}

describe('orderCorners', () => {
  it('reorders jscanify corner names into the canonical order', () => {
    const raw: RawCorners = {
      topLeftCorner: point(0, 0),
      topRightCorner: point(100, 0),
      bottomLeftCorner: point(0, 60),
      bottomRightCorner: point(100, 60),
    };
    expect(orderCorners(raw)).toEqual([
      point(0, 0),
      point(100, 0),
      point(100, 60),
      point(0, 60),
    ]);
  });

  it.each([
    'topLeftCorner',
    'topRightCorner',
    'bottomLeftCorner',
    'bottomRightCorner',
  ] as const)('returns null when %s is missing', (name) => {
    const raw: RawCorners = {
      topLeftCorner: point(0, 0),
      topRightCorner: point(100, 0),
      bottomLeftCorner: point(0, 60),
      bottomRightCorner: point(100, 60),
    };
    expect(orderCorners({ ...raw, [name]: undefined })).toBeNull();
  });
});

describe('segmentsIntersect', () => {
  it('detects a crossing', () => {
    expect(
      segmentsIntersect(point(0, 0), point(10, 10), point(0, 10), point(10, 0)),
    ).toBe(true);
  });

  it('passes two disjoint parallel segments', () => {
    expect(
      segmentsIntersect(point(0, 0), point(10, 0), point(0, 5), point(10, 5)),
    ).toBe(false);
  });

  it('detects collinear overlap', () => {
    expect(
      segmentsIntersect(point(0, 0), point(10, 0), point(5, 0), point(15, 0)),
    ).toBe(true);
  });

  it('passes collinear segments with a gap', () => {
    expect(
      segmentsIntersect(point(0, 0), point(4, 0), point(5, 0), point(9, 0)),
    ).toBe(false);
  });

  it('detects an endpoint lying on the other segment', () => {
    expect(
      segmentsIntersect(point(0, 0), point(10, 0), point(5, 0), point(5, 8)),
    ).toBe(true);
  });
});

describe('isSimpleQuad', () => {
  it('accepts a convex quad', () => {
    expect(isSimpleQuad(rectangle())).toBe(true);
  });

  it('accepts a concave quad: its handles can still be dragged', () => {
    const concave: Quad = [
      point(0, 0),
      point(100, 0),
      point(20, 30),
      point(0, 60),
    ];
    expect(isSimpleQuad(concave)).toBe(true);
  });

  it('rejects a self-crossing quad', () => {
    const crossed: Quad = [
      point(0, 0),
      point(100, 60),
      point(100, 0),
      point(0, 60),
    ];
    expect(isSimpleQuad(crossed)).toBe(false);
  });

  it('rejects a quad collapsed onto one line', () => {
    const collinear: Quad = [
      point(0, 0),
      point(40, 0),
      point(60, 0),
      point(100, 0),
    ];
    expect(isSimpleQuad(collinear)).toBe(false);
  });
});

describe('cornersFromContour', () => {
  it('finds the corners of an axis-aligned rectangle', () => {
    const flat = contourThrough(
      [point(10, 20), point(110, 20), point(110, 90), point(10, 90)],
      7,
    );
    expect(cornersFromContour(flat)).toEqual([
      point(10, 20),
      point(110, 20),
      point(110, 90),
      point(10, 90),
    ]);
  });

  it('recovers the corners of a rectangle rotated 44 degrees', () => {
    const { corners, flat } = rotatedRectangleContour(
      point(500, 400),
      300,
      200,
      44,
    );
    const found = cornersFromContour(flat);
    expect(found).not.toBeNull();
    // The fallback's roles: smallest and largest x+y, largest and
    // smallest x−y over the contour.
    expect(found?.[0]).toEqual(
      extreme(corners, (a, b) => (a.x + a.y <= b.x + b.y ? a : b)),
    );
    expect(found?.[1]).toEqual(
      extreme(corners, (a, b) => (a.x - a.y >= b.x - b.y ? a : b)),
    );
    expect(found?.[2]).toEqual(
      extreme(corners, (a, b) => (a.x + a.y >= b.x + b.y ? a : b)),
    );
    expect(found?.[3]).toEqual(
      extreme(corners, (a, b) => (a.x - a.y <= b.x - b.y ? a : b)),
    );
  });

  it('returns null for fewer than four points', () => {
    expect(cornersFromContour([0, 0, 10, 0, 10, 10])).toBeNull();
    expect(cornersFromContour([])).toBeNull();
  });

  it('breaks ties by keeping the earliest point', () => {
    // Two corners share the smallest x+y; the first wins.
    const flat = [0, 0, 5, 5, 40, 0, 60, 0, 60, 40, 40, 40, 0, 40];
    const found = cornersFromContour(flat);
    expect(found?.[0]).toEqual(point(0, 0));
  });
});

describe('scanDimensions', () => {
  const source = { width: 1600, height: 1200 };

  it('takes the paper size the user framed', () => {
    const quad: Quad = [
      point(400, 300),
      point(1200, 300),
      point(1200, 900),
      point(400, 900),
    ];
    expect(scanDimensions(quad, source)).toEqual({ width: 800, height: 600 });
  });

  it('scales a far-away sheet up to the 480 px floor', () => {
    const quad: Quad = [
      point(700, 500),
      point(940, 500),
      point(940, 680),
      point(700, 680),
    ];
    expect(scanDimensions(quad, source)).toEqual({ width: 480, height: 360 });
  });

  it('never exceeds the source pixel grid', () => {
    const quad: Quad = [
      point(-400, -300),
      point(2000, -300),
      point(2000, 1500),
      point(-400, 1500),
    ];
    expect(scanDimensions(quad, source)).toEqual({
      width: 1600,
      height: 1200,
    });
  });

  it('stays at one pixel for a fully degenerate quad', () => {
    const quad: Quad = [
      point(500, 400),
      point(500, 400),
      point(500, 400),
      point(500, 400),
    ];
    expect(scanDimensions(quad, source)).toEqual({ width: 1, height: 1 });
  });
});

describe('defaultQuad', () => {
  it('keeps the decided inset share', () => {
    expect(DEFAULT_INSET_FRACTION).toBe(0.1);
  });

  it('frames the shot a tenth in from every edge, corners ordered', () => {
    expect(defaultQuad({ width: 1000, height: 800 })).toEqual([
      { x: 100, y: 80 },
      { x: 900, y: 80 },
      { x: 900, y: 720 },
      { x: 100, y: 720 },
    ]);
  });

  it('answers a usable quad for any frame, however small', () => {
    const quad = defaultQuad({ width: 1, height: 1 });
    expect(isSimpleQuad(quad)).toBe(true);
    expect(
      orderCorners({
        topLeftCorner: quad[0],
        topRightCorner: quad[1],
        bottomRightCorner: quad[2],
        bottomLeftCorner: quad[3],
      }),
    ).toEqual(quad);
  });
});
