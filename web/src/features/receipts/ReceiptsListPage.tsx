import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { getErrorMessage } from '../../api/errors';
import type { ReceiptListParams } from '../../api/query-keys';
import { useReceipts, type ReceiptSummary } from '../../api/receipts';
import { useStores } from '../../api/stores';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { PageHeader } from '../../components/ui/PageHeader';
import { Select } from '../../components/ui/Select';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';
import { formatReceiptStatus, type ReceiptStatus } from './flag-reasons';

const STATUS_OPTIONS: readonly { value: ReceiptStatus; label: string }[] = [
  { value: 'queued', label: 'Queued' },
  { value: 'processing', label: 'Processing' },
  { value: 'waiting_quota', label: 'Waiting for quota' },
  { value: 'needs_review', label: 'Needs review' },
  { value: 'ready', label: 'Ready' },
  { value: 'failed', label: 'Failed' },
];

const MONTH_PATTERN = /^\d{4}-(0[1-9]|1[0-2])$/;

interface PagingState {
  filterKey: string;
  cursor: string | undefined;
  accumulated: ReceiptSummary[];
}

/** Filterable, cursor-paginated list of the user's receipts. */
export function ReceiptsListPage() {
  const [month, setMonth] = useState('');
  const [status, setStatus] = useState<ReceiptStatus | ''>('');
  const [storeId, setStoreId] = useState('');
  const [search, setSearch] = useState('');

  const trimmedMonth = month.trim();
  const validMonth = MONTH_PATTERN.test(trimmedMonth)
    ? trimmedMonth
    : undefined;
  const monthHint =
    trimmedMonth.length > 0 && !validMonth
      ? 'Enter month as YYYY-MM (for example, 2026-11).'
      : undefined;

  const baseParams = useMemo<ReceiptListParams>(() => {
    const query: ReceiptListParams = {};
    if (validMonth) query.month = validMonth;
    if (status) query.status = status;
    if (storeId) query.storeId = storeId;
    const trimmedSearch = search.trim();
    if (trimmedSearch) query.q = trimmedSearch;
    return query;
  }, [validMonth, status, storeId, search]);

  const filterKey = JSON.stringify(baseParams);
  const [paging, setPaging] = useState<PagingState>({
    filterKey,
    cursor: undefined,
    accumulated: [],
  });

  const activeCursor =
    paging.filterKey === filterKey ? paging.cursor : undefined;
  const accumulatedRows = useMemo(
    () => (paging.filterKey === filterKey ? paging.accumulated : []),
    [paging.filterKey, paging.accumulated, filterKey],
  );

  const storesQuery = useStores({ limit: 100 });
  const receiptsQuery = useReceipts({
    ...baseParams,
    cursor: activeCursor,
  });

  const rows = useMemo(() => {
    const current = receiptsQuery.data?.items ?? [];
    if (accumulatedRows.length === 0) return current;
    const seen = new Set(accumulatedRows.map((entry) => entry.id));
    const combined = [...accumulatedRows];
    for (const entry of current) {
      if (!seen.has(entry.id)) {
        combined.push(entry);
      }
    }
    return combined;
  }, [accumulatedRows, receiptsQuery.data?.items]);

  const storeOptions = useMemo(
    () =>
      (storesQuery.data?.items ?? []).map((store) => ({
        value: store.id,
        label: store.name,
      })),
    [storesQuery.data?.items],
  );

  const hasActiveFilters = Boolean(
    trimmedMonth || status || storeId || search.trim(),
  );

  function clearFilters() {
    setMonth('');
    setStatus('');
    setStoreId('');
    setSearch('');
  }

  function handleLoadMore() {
    const nextCursor = receiptsQuery.data?.nextCursor;
    if (!nextCursor) return;
    setPaging({
      filterKey,
      cursor: nextCursor,
      accumulated: rows,
    });
  }

  const columns: readonly TableColumn<ReceiptSummary>[] = [
    {
      key: 'purchasedOn',
      header: 'Date',
      cell: (row) => <DateText value={row.purchasedOn} />,
    },
    {
      key: 'store',
      header: 'Store',
      cell: (row) =>
        row.store ? (
          <span className="text-ink">{row.store.name}</span>
        ) : (
          <span className="text-muted">Unknown store</span>
        ),
    },
    {
      key: 'title',
      header: 'Receipt',
      cell: (row) => (
        <Link
          to={`/receipts/${row.id}`}
          className="font-medium text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {row.title}
        </Link>
      ),
    },
    {
      key: 'total',
      header: 'Total',
      align: 'right',
      cell: (row) => <Money value={row.total} currency={row.currency} />,
    },
    {
      key: 'status',
      header: 'Status',
      cell: (row) => {
        const presentation = formatReceiptStatus(row.status);
        return (
          <Badge variant={presentation.variant}>{presentation.label}</Badge>
        );
      },
    },
    {
      key: 'openFlagCount',
      header: 'Review',
      cell: (row) =>
        row.openFlagCount > 0 ? (
          <Badge variant="warning">
            {row.openFlagCount} {row.openFlagCount === 1 ? 'flag' : 'flags'}
          </Badge>
        ) : (
          <span className="text-muted">None</span>
        ),
    },
  ];

  function renderContent() {
    if (receiptsQuery.isPending && accumulatedRows.length === 0) {
      return (
        <Card>
          <Spinner size="md" showLabel label="Loading receipts..." />
        </Card>
      );
    }

    if (receiptsQuery.isError && rows.length === 0) {
      return (
        <ErrorState
          title="Could not load receipts"
          message={getErrorMessage(receiptsQuery.error)}
          onRetry={() => {
            void receiptsQuery.refetch();
          }}
        />
      );
    }

    if (rows.length === 0) {
      if (hasActiveFilters) {
        return (
          <EmptyState
            title="No receipts match these filters"
            description="Adjust or clear your filters, or upload a new receipt photo."
            action={
              <div className="flex flex-wrap items-center justify-center gap-3">
                <Button variant="secondary" onClick={clearFilters}>
                  Clear filters
                </Button>
                <Link
                  to="/upload"
                  className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                >
                  Upload receipts
                </Link>
              </div>
            }
          />
        );
      }

      return (
        <EmptyState
          title="No receipts yet"
          description="Upload receipt photos to extract store details, totals, and line items."
          action={
            <Link
              to="/upload"
              className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              Upload receipts
            </Link>
          }
        />
      );
    }

    return (
      <div className="flex flex-col gap-4">
        <Table
          columns={columns}
          rows={rows}
          rowKey={(row) => row.id}
          caption="Uploaded receipts"
        />

        {receiptsQuery.isError ? (
          <ErrorState
            title="Could not load more receipts"
            message={getErrorMessage(receiptsQuery.error)}
            onRetry={() => {
              void receiptsQuery.refetch();
            }}
          />
        ) : null}

        {receiptsQuery.data?.nextCursor ? (
          <div className="flex justify-center">
            <Button
              variant="secondary"
              loading={receiptsQuery.isFetching}
              onClick={handleLoadMore}
            >
              Load more
            </Button>
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Receipts"
        title="Receipts"
        description="Browse uploaded receipts, filter by month, store, or status, and open any receipt to edit lines."
        actions={
          <Link
            to="/upload"
            className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Upload receipts
          </Link>
        }
      />

      <Card
        title="Filters"
        actions={
          hasActiveFilters ? (
            <Button variant="ghost" size="sm" onClick={clearFilters}>
              Clear filters
            </Button>
          ) : undefined
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Input
            label="Search"
            placeholder="Title, store, or line item"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <Input
            label="Month"
            placeholder="YYYY-MM"
            value={month}
            hint={monthHint}
            onChange={(event) => setMonth(event.target.value)}
          />
          <Select
            label="Status"
            placeholder="All statuses"
            options={STATUS_OPTIONS}
            value={status}
            onChange={(event) =>
              setStatus(event.target.value as ReceiptStatus | '')
            }
          />
          <Select
            label="Store"
            placeholder="All stores"
            options={storeOptions}
            value={storeId}
            onChange={(event) => setStoreId(event.target.value)}
          />
        </div>
      </Card>

      {renderContent()}
    </div>
  );
}
