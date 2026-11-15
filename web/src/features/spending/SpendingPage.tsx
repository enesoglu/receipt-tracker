import { useState } from 'react';
import { Link } from 'react-router';
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { getErrorMessage } from '../../api/errors';
import { useMonthBreakdown, useMonthlySpending } from '../../api/reports';
import type { components } from '../../api/schema.gen';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { formatDateValue, formatMoney } from '../../components/ui/format';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';
import { Tabs, type TabItem } from '../../components/ui/Tabs';
import {
  buildBreakdownReceiptsHref,
  formatSharePercentage,
  getDefaultMonthRange,
  groupMonthlySpendingByCurrency,
  toBreakdownChartRows,
  validateMonthRange,
  type SpendingChartRow,
} from './spending-helpers';

type BreakdownGroupBy = 'category' | 'store';
type BreakdownGroup = components['schemas']['BreakdownGroup'];

function getBreakdownGroupKindBadge(kind: BreakdownGroup['kind']) {
  if (kind === 'uncategorised') {
    return <Badge variant="warning">Uncategorised</Badge>;
  }
  if (kind === 'unallocated') {
    return <Badge variant="neutral">Unallocated</Badge>;
  }
  return null;
}

/** Monthly spending trends and clickable category/store drill-down screen. */
export function SpendingPage() {
  const defaultRange = getDefaultMonthRange();
  const [fromMonth, setFromMonth] = useState(defaultRange.from);
  const [toMonth, setToMonth] = useState(defaultRange.to);
  const [userSelectedMonth, setUserSelectedMonth] = useState<string | null>(
    null,
  );
  const [groupBy, setGroupBy] = useState<BreakdownGroupBy>('category');

  const rangeError = validateMonthRange(fromMonth, toMonth);
  const isCustomRange =
    fromMonth !== defaultRange.from || toMonth !== defaultRange.to;

  const spendingQuery = useMonthlySpending(
    { from: fromMonth, to: toMonth },
    rangeError === null,
  );

  const months = spendingQuery.data?.months ?? [];
  const monthsWithTotals = months.filter((entry) => entry.totals.length > 0);
  const fallbackSelectedMonth =
    monthsWithTotals.length > 0
      ? monthsWithTotals[monthsWithTotals.length - 1].month
      : (months[months.length - 1]?.month ?? null);
  const validUserMonth =
    userSelectedMonth &&
    months.some((entry) => entry.month === userSelectedMonth)
      ? userSelectedMonth
      : null;
  const activeMonth = validUserMonth ?? fallbackSelectedMonth;

  const breakdownQuery = useMonthBreakdown(
    { month: activeMonth ?? toMonth, groupBy },
    rangeError === null && activeMonth !== null,
  );

  const currencyGroups = spendingQuery.data
    ? groupMonthlySpendingByCurrency(spendingQuery.data)
    : [];

  function handleResetRange() {
    const freshDefault = getDefaultMonthRange();
    setFromMonth(freshDefault.from);
    setToMonth(freshDefault.to);
    setUserSelectedMonth(null);
  }

  function renderMonthlyTableColumns(
    currency: string,
  ): readonly TableColumn<SpendingChartRow>[] {
    return [
      {
        key: 'month',
        header: 'Month',
        cell: (row) => {
          const isSelected = row.month === activeMonth;
          return (
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  setUserSelectedMonth(row.month);
                }}
                aria-pressed={isSelected}
                className={`cursor-pointer font-medium hover:underline ${
                  isSelected ? 'text-accent' : 'text-ink'
                }`}
              >
                <DateText value={row.month} format="month" />
              </button>
              {isSelected ? <Badge variant="accent">Selected</Badge> : null}
            </div>
          );
        },
      },
      {
        key: 'receiptCount',
        header: 'Receipts',
        align: 'right',
        cell: (row) => (
          <span className="font-mono tabular-nums text-ink">
            {row.receiptCount}
          </span>
        ),
      },
      {
        key: 'amount',
        header: 'Total',
        align: 'right',
        cell: (row) => (
          <Money
            value={row.amount}
            currency={currency}
            className="font-medium text-ink"
          />
        ),
      },
    ];
  }

  function renderBreakdownContent(mode: BreakdownGroupBy) {
    if (!activeMonth) {
      return (
        <p className="text-small text-muted">
          Select a month from the spending summary to inspect its breakdown.
        </p>
      );
    }

    if (breakdownQuery.isPending) {
      return (
        <div className="flex justify-center py-8">
          <Spinner showLabel label="Loading month breakdown…" />
        </div>
      );
    }

    if (breakdownQuery.isError) {
      return (
        <ErrorState
          title="Could not load month breakdown"
          message={getErrorMessage(breakdownQuery.error)}
          onRetry={() => {
            void breakdownQuery.refetch();
          }}
        />
      );
    }

    const breakdownCurrencies = breakdownQuery.data.currencies;
    if (breakdownCurrencies.length === 0) {
      return (
        <EmptyState
          title={`No recorded spending in ${formatDateValue(activeMonth, 'month')}`}
          description="No verified receipts with totals matched this month."
          action={
            <Link
              to={`/receipts?month=${encodeURIComponent(activeMonth)}`}
              className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-4 py-2 text-small font-medium text-ink hover:border-border-strong hover:bg-surface-subtle"
            >
              Inspect receipts for {formatDateValue(activeMonth, 'month')}
            </Link>
          }
        />
      );
    }

    const groupHeaderLabel = mode === 'store' ? 'Store' : 'Category';

    return (
      <div className="flex flex-col gap-8">
        {breakdownCurrencies.map((currencyGroup) => {
          const chartRows = toBreakdownChartRows(currencyGroup.groups);
          const breakdownColumns: readonly TableColumn<BreakdownGroup>[] = [
            {
              key: 'group',
              header: groupHeaderLabel,
              cell: (row) => (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-ink">{row.name}</span>
                  {getBreakdownGroupKindBadge(row.kind)}
                </div>
              ),
            },
            {
              key: 'items',
              header: 'Line items',
              align: 'right',
              cell: (row) => (
                <span className="font-mono tabular-nums text-ink">
                  {row.itemCount}
                </span>
              ),
            },
            {
              key: 'share',
              header: 'Share',
              align: 'right',
              cell: (row) => (
                <span className="font-mono tabular-nums text-muted">
                  {formatSharePercentage(row.share)}
                </span>
              ),
            },
            {
              key: 'amount',
              header: 'Amount',
              align: 'right',
              cell: (row) => (
                <Money
                  value={row.amount}
                  currency={currencyGroup.currency}
                  className="font-medium text-ink"
                />
              ),
            },
            {
              key: 'receipts',
              header: 'Receipts',
              align: 'right',
              cell: (row) => (
                <Link
                  to={buildBreakdownReceiptsHref(activeMonth, row)}
                  className="font-medium text-accent hover:underline"
                >
                  View receipts
                </Link>
              ),
            },
          ];

          return (
            <section
              key={currencyGroup.currency}
              aria-label={`${currencyGroup.currency} ${mode} breakdown`}
              className="flex flex-col gap-4 border-t border-border pt-6 first:border-t-0 first:pt-0"
            >
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="text-heading font-semibold text-ink">
                  {currencyGroup.currency} total:{' '}
                  <Money
                    value={currencyGroup.total}
                    currency={currencyGroup.currency}
                  />
                </h3>
              </div>

              {chartRows.length > 0 ? (
                <div className="h-60 w-full rounded-card border border-border bg-surface-subtle p-4">
                  <ResponsiveContainer
                    width="100%"
                    height={210}
                    initialDimension={{ width: 640, height: 210 }}
                  >
                    <BarChart
                      layout="vertical"
                      data={chartRows}
                      margin={{ top: 8, right: 20, left: 8, bottom: 8 }}
                    >
                      <CartesianGrid
                        stroke="var(--color-border)"
                        strokeDasharray="3 3"
                      />
                      <XAxis
                        type="number"
                        stroke="var(--color-muted)"
                        tick={{ fill: 'var(--color-muted)', fontSize: 12 }}
                      />
                      <YAxis
                        type="category"
                        dataKey="name"
                        width={120}
                        stroke="var(--color-muted)"
                        tick={{ fill: 'var(--color-ink)', fontSize: 12 }}
                      />
                      <Tooltip
                        isAnimationActive={false}
                        formatter={(value) =>
                          formatMoney(
                            typeof value === 'number' ||
                              typeof value === 'string'
                              ? String(value)
                              : undefined,
                            currencyGroup.currency,
                          )
                        }
                        contentStyle={{
                          backgroundColor: 'var(--color-surface)',
                          borderColor: 'var(--color-border)',
                          color: 'var(--color-ink)',
                        }}
                      />
                      <Bar
                        dataKey="amountNumeric"
                        name={groupHeaderLabel}
                        fill="var(--color-accent)"
                        radius={[0, 4, 4, 0]}
                        isAnimationActive={false}
                      />
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              ) : null}

              <Table
                caption={`${currencyGroup.currency} ${mode} breakdown for ${activeMonth}`}
                columns={breakdownColumns}
                rows={currencyGroup.groups}
                rowKey={(row) => `${row.kind}-${row.id ?? row.name}`}
              />
            </section>
          );
        })}
      </div>
    );
  }

  const breakdownTabs: readonly TabItem[] = [
    {
      id: 'category',
      label: 'By category',
      content: renderBreakdownContent('category'),
    },
    {
      id: 'store',
      label: 'By store',
      content: renderBreakdownContent('store'),
    },
  ];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Reports"
        title="Spending"
        description="Track monthly spending per currency and click any month to drill down by category or store."
      />

      <Card>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Input
            label="From month"
            type="month"
            value={fromMonth}
            onChange={(event) => {
              setFromMonth(event.target.value);
            }}
          />
          <Input
            label="To month"
            type="month"
            value={toMonth}
            onChange={(event) => {
              setToMonth(event.target.value);
            }}
          />
          {isCustomRange ? (
            <div className="flex items-end">
              <Button variant="ghost" size="sm" onClick={handleResetRange}>
                Reset to last 12 months
              </Button>
            </div>
          ) : null}
        </div>
      </Card>

      {rangeError ? (
        <ErrorState title="Invalid month range" message={rangeError} />
      ) : spendingQuery.isPending ? (
        <Card>
          <div className="flex justify-center py-8">
            <Spinner showLabel label="Loading monthly spending…" />
          </div>
        </Card>
      ) : spendingQuery.isError ? (
        <ErrorState
          title="Could not load spending report"
          message={getErrorMessage(spendingQuery.error)}
          onRetry={() => {
            void spendingQuery.refetch();
          }}
        />
      ) : (
        <>
          {spendingQuery.data.excludedReceipts > 0 ? (
            <div
              role="status"
              className="rounded-card border border-warning/30 bg-warning-soft px-4 py-3 text-small text-ink"
            >
              {spendingQuery.data.excludedReceipts}{' '}
              {spendingQuery.data.excludedReceipts === 1
                ? 'receipt is'
                : 'receipts are'}{' '}
              excluded from these totals because verification is still pending
              or total/date details are incomplete.
            </div>
          ) : null}

          {currencyGroups.length === 0 ? (
            <EmptyState
              title="No spending data in this range"
              description="Verified receipts with totals and purchase dates will appear here grouped by currency."
              action={
                <Link
                  to="/upload"
                  className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover"
                >
                  Upload receipts
                </Link>
              }
            />
          ) : (
            <div className="flex flex-col gap-6">
              {currencyGroups.map((group) => (
                <Card
                  key={group.currency}
                  title={`${group.currency} monthly spending`}
                  description={`${group.totalReceipts} ${group.totalReceipts === 1 ? 'receipt' : 'receipts'} across ${group.rows.length} ${group.rows.length === 1 ? 'month' : 'months'}. Click a bar or month below to inspect its breakdown.`}
                >
                  <div className="flex flex-col gap-6">
                    <div className="h-72 w-full rounded-card border border-border bg-surface-subtle p-4">
                      <ResponsiveContainer
                        width="100%"
                        height={250}
                        initialDimension={{ width: 640, height: 250 }}
                      >
                        <BarChart
                          data={group.rows}
                          margin={{ top: 8, right: 16, left: 8, bottom: 8 }}
                        >
                          <CartesianGrid
                            stroke="var(--color-border)"
                            strokeDasharray="3 3"
                          />
                          <XAxis
                            dataKey="monthLabel"
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
                          <Bar
                            dataKey="amountNumeric"
                            name={group.currency}
                            fill="var(--color-accent)"
                            radius={[4, 4, 0, 0]}
                            isAnimationActive={false}
                            onClick={(_, index) => {
                              const row = group.rows[index];
                              if (row) {
                                setUserSelectedMonth(row.month);
                              }
                            }}
                          />
                        </BarChart>
                      </ResponsiveContainer>
                    </div>

                    <Table
                      caption={`${group.currency} monthly totals`}
                      columns={renderMonthlyTableColumns(group.currency)}
                      rows={[...group.rows].reverse()}
                      rowKey={(row) => `${group.currency}-${row.month}`}
                    />
                  </div>
                </Card>
              ))}

              {activeMonth ? (
                <Card
                  title={`Breakdown for ${formatDateValue(activeMonth, 'month')}`}
                  description="Compare spending across categories or stores and follow any group to matching receipts."
                  actions={
                    <Link
                      to={`/receipts?month=${encodeURIComponent(activeMonth)}`}
                      className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-3 py-1.5 text-small font-medium text-ink hover:border-border-strong hover:bg-surface-subtle"
                    >
                      All {formatDateValue(activeMonth, 'month')} receipts
                    </Link>
                  }
                >
                  <Tabs
                    label="Breakdown grouping"
                    items={breakdownTabs}
                    value={groupBy}
                    onChange={(nextId) => {
                      if (nextId === 'category' || nextId === 'store') {
                        setGroupBy(nextId);
                      }
                    }}
                  />
                </Card>
              ) : null}
            </div>
          )}
        </>
      )}
    </div>
  );
}
