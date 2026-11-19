import type { PriceHistory } from '../../api/products';
import type { components } from '../../api/schema.gen';
import { formatDateValue } from '../../components/ui/format';

export const CHART_SERIES_COLORS = [
  'var(--color-accent)',
  'var(--color-success)',
  'var(--color-warning)',
  'var(--color-danger)',
  'var(--color-ink)',
  'var(--color-muted)',
] as const;

export interface PriceSeriesDefinition {
  dataKey: string;
  label: string;
  color: string;
  storeId: string | null;
}

export type PriceChartRow = {
  purchasedOn: string;
  dateLabel: string;
} & Record<string, string | number | undefined>;

export interface CurrencyPriceHistoryGroup {
  currency: string;
  series: PriceSeriesDefinition[];
  chartRows: PriceChartRow[];
  stores: components['schemas']['StorePriceSummary'][];
  points: components['schemas']['PricePoint'][];
}

function getStoreSeriesId(store: components['schemas']['StoreRef'] | null): {
  bucketKey: string;
  storeId: string | null;
  label: string;
} {
  if (!store) {
    return {
      bucketKey: 'unknown-store',
      storeId: null,
      label: 'Unknown store',
    };
  }
  return {
    bucketKey: store.id,
    storeId: store.id,
    label: store.name,
  };
}

/** Groups price-history points and store summaries by currency for Recharts rendering. */
export function groupPriceHistoryByCurrency(
  history: PriceHistory,
): CurrencyPriceHistoryGroup[] {
  const currencies: string[] = [];
  const pushCurrency = (currency: string) => {
    if (!currencies.includes(currency)) {
      currencies.push(currency);
    }
  };

  for (const point of history.points) {
    pushCurrency(point.currency);
  }
  for (const storeSummary of history.stores) {
    pushCurrency(storeSummary.currency);
  }

  return currencies.map((currency) => {
    const currencyPoints = history.points.filter(
      (point) => point.currency === currency,
    );
    const currencyStores = history.stores.filter(
      (store) => store.currency === currency,
    );

    const chronologicalPoints = [...currencyPoints].sort((a, b) =>
      a.purchasedOn.localeCompare(b.purchasedOn),
    );
    const newestFirstPoints = [...currencyPoints].sort((a, b) =>
      b.purchasedOn.localeCompare(a.purchasedOn),
    );

    const seriesByBucket = new Map<string, PriceSeriesDefinition>();
    for (const point of chronologicalPoints) {
      const info = getStoreSeriesId(point.store);
      if (!seriesByBucket.has(info.bucketKey)) {
        const index = seriesByBucket.size;
        seriesByBucket.set(info.bucketKey, {
          dataKey: `store_${index}`,
          label: info.label,
          color: CHART_SERIES_COLORS[index % CHART_SERIES_COLORS.length],
          storeId: info.storeId,
        });
      }
    }

    const rowByPurchase = new Map<string, PriceChartRow>();
    for (const point of chronologicalPoints) {
      const info = getStoreSeriesId(point.store);
      const series = seriesByBucket.get(info.bucketKey);
      if (!series) continue;

      // One row per purchase, so two purchases on one day are both
      // plotted instead of the later overwriting the earlier.
      const rowKey = `${point.purchasedOn}:${point.lineItemId}`;
      let row = rowByPurchase.get(rowKey);
      if (!row) {
        row = {
          purchasedOn: point.purchasedOn,
          dateLabel: formatDateValue(point.purchasedOn, 'date'),
        };
        rowByPurchase.set(rowKey, row);
      }

      const numericPrice = Number(point.unitPrice);
      if (Number.isFinite(numericPrice)) {
        row[series.dataKey] = numericPrice;
      }
    }

    return {
      currency,
      series: Array.from(seriesByBucket.values()),
      chartRows: Array.from(rowByPurchase.values()),
      stores: currencyStores,
      points: newestFirstPoints,
    };
  });
}
