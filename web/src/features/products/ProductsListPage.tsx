import { useState } from 'react';
import { Link } from 'react-router';
import { useCategories } from '../../api/categories';
import { getErrorMessage } from '../../api/errors';
import { useProducts, type ProductSummary } from '../../api/products';
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

interface PaginationState {
  filterKey: string;
  cursor: string | undefined;
  priorItems: ProductSummary[];
}

function formatPackageSize(
  sizeValue: string | null,
  sizeUnit: string | null,
): string | null {
  if (!sizeValue && !sizeUnit) return null;
  if (sizeValue && sizeUnit) return `${sizeValue} ${sizeUnit}`;
  return sizeValue ?? sizeUnit;
}

const columns: readonly TableColumn<ProductSummary>[] = [
  {
    key: 'product',
    header: 'Product',
    cell: (row) => {
      const sizeText = formatPackageSize(row.sizeValue, row.sizeUnit);
      return (
        <div className="flex flex-col gap-0.5">
          <Link
            to={`/products/${row.id}`}
            className="font-medium text-ink hover:text-accent hover:underline"
          >
            {row.canonicalName}
          </Link>
          {sizeText ? (
            <span className="text-caption text-muted">{sizeText}</span>
          ) : null}
        </div>
      );
    },
  },
  {
    key: 'category',
    header: 'Category',
    cell: (row) =>
      row.category ? (
        <Badge variant="neutral">{row.category.name}</Badge>
      ) : (
        <span className="text-muted">Uncategorised</span>
      ),
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
  {
    key: 'lastPurchase',
    header: 'Last purchase',
    cell: (row) => {
      if (!row.lastPurchase) {
        return <span className="text-muted">—</span>;
      }
      return (
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <Money
            value={row.lastPurchase.unitPrice}
            currency={row.lastPurchase.currency}
            className="font-medium text-ink"
          />
          <span className="text-caption text-muted">
            {row.lastPurchase.store ? `${row.lastPurchase.store.name} · ` : ''}
            <DateText value={row.lastPurchase.purchasedOn} />
          </span>
        </div>
      );
    },
  },
];

/** Filterable, cursor-paginated catalog of canonical products. */
export function ProductsListPage() {
  const [searchText, setSearchText] = useState('');
  const [categoryId, setCategoryId] = useState('');
  const [pagination, setPagination] = useState<PaginationState>({
    filterKey: '\0',
    cursor: undefined,
    priorItems: [],
  });

  const trimmedQuery = searchText.trim();
  const filterKey = `${trimmedQuery}\0${categoryId}`;
  const hasActiveFilters = trimmedQuery !== '' || categoryId !== '';
  const activeCursor =
    pagination.filterKey === filterKey ? pagination.cursor : undefined;
  const priorItems =
    pagination.filterKey === filterKey ? pagination.priorItems : [];

  const categoriesQuery = useCategories({ limit: 100 });
  const productsQuery = useProducts({
    q: trimmedQuery || undefined,
    categoryId: categoryId || undefined,
    cursor: activeCursor,
  });

  const items = activeCursor
    ? [...priorItems, ...(productsQuery.data?.items ?? [])]
    : (productsQuery.data?.items ?? []);
  const nextCursor = productsQuery.data?.nextCursor ?? null;

  const categoryOptions = (categoriesQuery.data?.items ?? []).map((cat) => ({
    value: cat.id,
    label: cat.name,
  }));

  function handleClearFilters() {
    setSearchText('');
    setCategoryId('');
    setPagination({ filterKey: '\0', cursor: undefined, priorItems: [] });
  }

  function handleLoadMore() {
    if (!nextCursor) return;
    setPagination({
      filterKey,
      cursor: nextCursor,
      priorItems: items,
    });
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Catalog"
        title="Products"
        description="Browse canonical products, filter by category or alias, and inspect price history across stores."
      />

      <Card>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Input
            label="Search"
            placeholder="Product name or alias"
            value={searchText}
            onChange={(event) => {
              setSearchText(event.target.value);
            }}
          />
          <Select
            label="Category"
            value={categoryId}
            placeholder="All categories"
            options={categoryOptions}
            onChange={(event) => {
              setCategoryId(event.target.value);
            }}
          />
          {hasActiveFilters ? (
            <div className="flex items-end">
              <Button variant="ghost" size="sm" onClick={handleClearFilters}>
                Clear filters
              </Button>
            </div>
          ) : null}
        </div>
      </Card>

      {productsQuery.isPending && items.length === 0 ? (
        <Card>
          <div className="flex justify-center py-8">
            <Spinner showLabel label="Loading products…" />
          </div>
        </Card>
      ) : productsQuery.isError && items.length === 0 ? (
        <ErrorState
          title="Could not load products"
          message={getErrorMessage(productsQuery.error)}
          onRetry={() => {
            void productsQuery.refetch();
          }}
        />
      ) : items.length === 0 ? (
        hasActiveFilters ? (
          <EmptyState
            title="No matching products"
            description="No products matched the current search or category filter."
            action={
              <Button variant="secondary" onClick={handleClearFilters}>
                Clear filters
              </Button>
            }
          />
        ) : (
          <EmptyState
            title="No products yet"
            description="Products appear automatically once receipts are extracted and matched."
            action={
              <Link
                to="/upload"
                className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover"
              >
                Upload receipts
              </Link>
            }
          />
        )
      ) : (
        <div className="flex flex-col gap-4">
          <Table
            caption="Canonical products"
            columns={columns}
            rows={items}
            rowKey={(row) => row.id}
          />

          {productsQuery.isError ? (
            <ErrorState
              title="Could not load more products"
              message={getErrorMessage(productsQuery.error)}
              onRetry={() => {
                void productsQuery.refetch();
              }}
            />
          ) : null}

          {nextCursor ? (
            <div className="flex justify-center">
              <Button
                variant="secondary"
                loading={productsQuery.isFetching}
                onClick={handleLoadMore}
              >
                Load more
              </Button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
