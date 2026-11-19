import { useState } from 'react';
import { Link } from 'react-router';
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { getErrorMessage } from '../../api/errors';
import { useProductPriceHistory } from '../../api/products';
import type { components } from '../../api/schema.gen';
import { useStores } from '../../api/stores';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { formatMoney, formatQuantity } from '../../components/ui/format';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { Select } from '../../components/ui/Select';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';
import { groupPriceHistoryByCurrency } from './price-history-chart';

export interface ProductPriceHistoryCardProps {
  productId: string;
}

type StoreSummaryRow = components['schemas']['StorePriceSummary'];
type PricePointRow = components['schemas']['PricePoint'];

const storeSummaryColumns: readonly TableColumn<StoreSummaryRow>[] = [
  {
    key: 'store',
    header: 'Store',
    cell: (row) => (
      <span className="font-medium text-ink">
        {row.store?.name ?? 'Unknown store'}
      </span>
    ),
  },
  {
    key: 'min',
    header: 'Min',
    align: 'right',
    cell: (row) => <Money value={row.min} currency={row.currency} />,
  },
  {
    key: 'max',
    header: 'Max',
    align: 'right',
    cell: (row) => <Money value={row.max} currency={row.currency} />,
  },
  {
    key: 'latest',
    header: 'Latest',
    align: 'right',
    cell: (row) => (
      <Money
        value={row.latest}
        currency={row.currency}
        className="font-medium text-ink"
      />
    ),
  },
  {
    key: 'latestOn',
    header: 'Latest purchase',
    cell: (row) => <DateText value={row.latestOn} />,
  },
  {
    key: 'purchases',
    header: 'Purchases',
    align: 'right',
    cell: (row) => (
      <span className="font-mono tabular-nums text-ink">
        {row.purchaseCount}
      </span>
    ),
  },
];

const basePointColumns: readonly TableColumn<PricePointRow>[] = [
  {
    key: 'date',
    header: 'Purchase date',
    cell: (row) => (
      <Link
        to={`/receipts/${row.receiptId}`}
        className="font-medium text-accent hover:underline"
      >
        <DateText value={row.purchasedOn} />
      </Link>
    ),
  },
  {
    key: 'store',
    header: 'Store',
    cell: (row) => (
      <span className="text-ink">{row.store?.name ?? 'Unknown store'}</span>
    ),
  },
  {
    key: 'quantity',
    header: 'Quantity',
    align: 'right',
    cell: (row) => (
      <span className="font-mono tabular-nums text-ink">
        {formatQuantity(row.quantity, '')}
        {row.quantityUnit ? ` ${row.quantityUnit}` : ''}
      </span>
    ),
  },
  {
    key: 'unitPrice',
    header: 'Unit price',
    align: 'right',
    cell: (row) => (
      <Money
        value={row.unitPrice}
        currency={row.currency}
        className="font-medium text-ink"
      />
    ),
  },
];

/** Point columns; a kg or l product also shows its comparable per-unit price. */
function pointColumns(
  basis: components['schemas']['PriceBasis'],
): readonly TableColumn<PricePointRow>[] {
  if (basis === 'item') {
    return basePointColumns;
  }
  return [
    ...basePointColumns,
    {
      key: 'basisPrice',
      header: basis === 'kg' ? 'Per kg' : 'Per litre',
      align: 'right',
      cell: (row) => (
        <Money
          value={row.basisPrice}
          currency={row.currency}
          className="font-medium text-ink"
        />
      ),
    },
  ];
}

/** Cross-store price history card with filters, per-currency line charts, and receipt links. */
export function ProductPriceHistoryCard({
  productId,
}: ProductPriceHistoryCardProps) {
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [storeId, setStoreId] = useState('');

  const hasFilters = from !== '' || to !== '' || storeId !== '';
  const hasInvalidRange = from !== '' && to !== '' && from > to;

  const storesQuery = useStores({ limit: 100 });
  const historyQuery = useProductPriceHistory(
    hasInvalidRange ? undefined : productId,
    {
      from: from || undefined,
      to: to || undefined,
      storeId: storeId || undefined,
    },
  );

  const storeOptions = (storesQuery.data?.items ?? []).map((store) => ({
    value: store.id,
    label: store.name,
  }));

  function handleClearFilters() {
    setFrom('');
    setTo('');
    setStoreId('');
  }

  const currencyGroups = historyQuery.data
    ? groupPriceHistoryByCurrency(historyQuery.data)
    : [];

  return (
    <Card
      title="Price history"
      description="Unit and basis price trends by store, grouped per currency. Select any purchase date to inspect the receipt."
    >
      <div className="flex flex-col gap-6">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Input
            label="From"
            type="date"
            value={from}
            onChange={(event) => {
              setFrom(event.target.value);
            }}
          />
          <Input
            label="To"
            type="date"
            value={to}
            onChange={(event) => {
              setTo(event.target.value);
            }}
          />
          <Select
            label="Store"
            value={storeId}
            placeholder="All stores"
            options={storeOptions}
            onChange={(event) => {
              setStoreId(event.target.value);
            }}
          />
          {hasFilters ? (
            <div className="flex items-end">
              <Button variant="ghost" size="sm" onClick={handleClearFilters}>
                Clear filters
              </Button>
            </div>
          ) : null}
        </div>

        {hasInvalidRange ? (
          <ErrorState
            title="Invalid date range"
            message="The start date must be on or before the end date."
          />
        ) : historyQuery.isPending ? (
          <div className="flex justify-center py-8">
            <Spinner showLabel label="Loading price history…" />
          </div>
        ) : historyQuery.isError ? (
          <ErrorState
            title="Could not load price history"
            message={getErrorMessage(historyQuery.error)}
            onRetry={() => {
              void historyQuery.refetch();
            }}
          />
        ) : currencyGroups.length === 0 ? (
          <EmptyState
            title="No price history"
            description={
              hasFilters
                ? 'No purchases matched the selected date range or store.'
                : 'No verified purchases have been recorded for this product yet.'
            }
            action={
              hasFilters ? (
                <Button variant="secondary" onClick={handleClearFilters}>
                  Clear filters
                </Button>
              ) : undefined
            }
          />
        ) : (
          <div className="flex flex-col gap-8">
            {currencyGroups.map((group) => (
              <section
                key={group.currency}
                aria-label={`${group.currency} price history`}
                className="flex flex-col gap-4 border-t border-border pt-6 first:border-t-0 first:pt-0"
              >
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <h3 className="text-heading font-semibold text-ink">
                    {group.currency} unit prices
                  </h3>
                  <span className="text-caption text-muted">
                    {group.points.length}{' '}
                    {group.points.length === 1 ? 'purchase' : 'purchases'}{' '}
                    across {group.stores.length}{' '}
                    {group.stores.length === 1 ? 'store' : 'stores'}
                  </span>
                </div>

                {group.stores.length > 0 ? (
                  <Table
                    caption={`${group.currency} store summary`}
                    columns={storeSummaryColumns}
                    rows={group.stores}
                    rowKey={(row) =>
                      `${row.currency}-${row.store?.id ?? 'unknown'}`
                    }
                  />
                ) : null}

                {group.basis !== 'item' ? (
                  <p className="text-caption text-muted">
                    The chart compares basis prices per{' '}
                    {group.basis === 'kg' ? 'kilogram' : 'litre'}; purchases
                    without a usable size are not plotted.
                  </p>
                ) : null}

                {group.chartRows.length > 0 ? (
                  <div className="h-72 w-full rounded-card border border-border bg-surface-subtle p-4">
                    <ResponsiveContainer
                      width="100%"
                      height={250}
                      initialDimension={{ width: 640, height: 250 }}
                    >
                      <LineChart
                        data={group.chartRows}
                        margin={{ top: 8, right: 16, left: 8, bottom: 8 }}
                      >
                        <CartesianGrid
                          stroke="var(--color-border)"
                          strokeDasharray="3 3"
                        />
                        <XAxis
                          dataKey="dateLabel"
                          stroke="var(--color-muted)"
                          tick={{ fill: 'var(--color-muted)', fontSize: 12 }}
                        />
                        <YAxis
                          stroke="var(--color-muted)"
                          tick={{ fill: 'var(--color-muted)', fontSize: 12 }}
                        />
                        <Tooltip
                          isAnimationActive={false}
                          formatter={(value) =>
                            formatMoney(
                              typeof value === 'number' ||
                                typeof value === 'string'
                                ? String(value)
                                : undefined,
                              group.currency,
                            )
                          }
                          contentStyle={{
                            backgroundColor: 'var(--color-surface)',
                            borderColor: 'var(--color-border)',
                            color: 'var(--color-ink)',
                          }}
                        />
                        <Legend />
                        {group.series.map((series) => (
                          <Line
                            key={series.dataKey}
                            type="monotone"
                            dataKey={series.dataKey}
                            name={series.label}
                            stroke={series.color}
                            strokeWidth={2}
                            dot={{ r: 4 }}
                            connectNulls
                            isAnimationActive={false}
                          />
                        ))}
                      </LineChart>
                    </ResponsiveContainer>
                  </div>
                ) : null}

                <Table
                  caption={`${group.currency} purchase points`}
                  columns={pointColumns(group.basis)}
                  rows={group.points}
                  rowKey={(row) => row.lineItemId}
                />
              </section>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
}
