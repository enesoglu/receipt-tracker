import { describe, expect, it } from 'vitest';
import type { PriceHistory } from '../../api/products';
import type { components } from '../../api/schema.gen';
import { groupPriceHistoryByCurrency } from './price-history-chart';

type PricePoint = components['schemas']['PricePoint'];
type StorePriceSummary = components['schemas']['StorePriceSummary'];

function point(overrides: Partial<PricePoint>): PricePoint {
  return {
    receiptId: '0193a1b2-0000-7000-8000-000000000100',
    lineItemId: '0193a1b2-0000-7000-8000-000000000201',
    purchasedOn: '2026-11-03',
    store: {
      id: '0193a1b2-0000-7000-8000-000000000010',
      name: 'Synthetic Market',
    },
    unitPrice: '32.50',
    quantity: '1',
    quantityUnit: 'AD',
    currency: 'TRY',
    basis: 'item',
    basisPrice: null,
    ...overrides,
  };
}

function historyWith(
  points: PricePoint[],
  stores: StorePriceSummary[] = [],
): PriceHistory {
  return { productId: '0193a1b2-0000-7000-8000-000000000001', points, stores };
}

describe('groupPriceHistoryByCurrency', () => {
  it('plots one row per purchase when two purchases share a date and store', () => {
    const history = historyWith(
      [
        point({ unitPrice: '99.00', lineItemId: '...0201' }),
        point({ unitPrice: '32.50', lineItemId: '...0202' }),
      ],
      [
        {
          store: {
            id: '0193a1b2-0000-7000-8000-000000000010',
            name: 'Synthetic Market',
          },
          currency: 'TRY',
          min: '32.50',
          max: '99.00',
          latest: '32.50',
          latestOn: '2026-11-03',
          purchaseCount: 2,
        },
      ],
    );

    const [group] = groupPriceHistoryByCurrency(history);

    expect(group.points).toHaveLength(2);
    expect(group.chartRows).toHaveLength(2);
    expect(group.chartRows.map((row) => row.store_0)).toEqual([99, 32.5]);
    expect(group.chartRows.map((row) => row.dateLabel)).toEqual([
      'Nov 3, 2026',
      'Nov 3, 2026',
    ]);
  });

  it('plots the basis price of a kg product and skips purchases without one', () => {
    const history = historyWith([
      point({
        basis: 'kg',
        unitPrice: '100.00',
        basisPrice: '100.00',
        lineItemId: '...0201',
      }),
      point({
        basis: 'kg',
        unitPrice: '50.00',
        basisPrice: '100.00',
        lineItemId: '...0202',
      }),
      point({
        basis: 'kg',
        unitPrice: '70.00',
        basisPrice: null,
        lineItemId: '...0203',
      }),
    ]);

    const [group] = groupPriceHistoryByCurrency(history);

    expect(group.basis).toBe('kg');
    expect(group.chartRows.map((row) => row.store_0)).toEqual([
      100,
      100,
      undefined,
    ]);
  });
});
