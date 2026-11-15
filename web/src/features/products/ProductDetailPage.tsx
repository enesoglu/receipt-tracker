import { useState, type FormEvent } from 'react';
import { Link, useParams } from 'react-router';
import { useCategories } from '../../api/categories';
import { ApiError } from '../../api/client';
import { getErrorMessage } from '../../api/errors';
import {
  useMergeProducts,
  useProduct,
  useProducts,
  useSplitProduct,
  useUpdateProduct,
  type Product,
} from '../../api/products';
import type { components } from '../../api/schema.gen';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Checkbox } from '../../components/ui/Checkbox';
import { DateText } from '../../components/ui/DateText';
import { Dialog } from '../../components/ui/Dialog';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { PageHeader } from '../../components/ui/PageHeader';
import { Select } from '../../components/ui/Select';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';
import { ProductPriceHistoryCard } from './ProductPriceHistoryCard';

type ProductCodeRow = components['schemas']['ProductCode'];

const codeColumns: readonly TableColumn<ProductCodeRow>[] = [
  {
    key: 'code',
    header: 'Code',
    cell: (row) => <span className="font-mono text-ink">{row.code}</span>,
  },
  {
    key: 'kind',
    header: 'Kind',
    cell: (row) => (
      <Badge variant={row.kind === 'gtin' ? 'accent' : 'neutral'}>
        {row.kind === 'gtin' ? 'GTIN' : 'Store code'}
      </Badge>
    ),
  },
  {
    key: 'store',
    header: 'Store',
    cell: (row) => (
      <span className="text-ink">{row.store?.name ?? 'All stores'}</span>
    ),
  },
  {
    key: 'seen',
    header: 'Seen',
    align: 'right',
    cell: (row) => (
      <span className="font-mono tabular-nums text-ink">{row.seenCount}</span>
    ),
  },
];

interface ProductEditFormProps {
  product: Product;
  categoryOptions: { value: string; label: string }[];
  onSaveNotice: (message: string) => void;
}

function ProductEditForm({
  product,
  categoryOptions,
  onSaveNotice,
}: ProductEditFormProps) {
  const [canonicalName, setCanonicalName] = useState(product.canonicalName);
  const [categoryId, setCategoryId] = useState(product.category?.id ?? '');
  const [sizeValue, setSizeValue] = useState(product.sizeValue ?? '');
  const [sizeUnit, setSizeUnit] = useState(product.sizeUnit ?? '');
  const [validationError, setValidationError] = useState<string | null>(null);
  const [serverError, setServerError] = useState<string | null>(null);

  const updateMutation = useUpdateProduct(product.id);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setValidationError(null);
    setServerError(null);

    const trimmedName = canonicalName.trim();
    if (!trimmedName) {
      setValidationError('Canonical name is required.');
      return;
    }

    const trimmedSizeValue = sizeValue.trim();
    const trimmedSizeUnit = sizeUnit.trim();
    if (
      (trimmedSizeValue !== '' && trimmedSizeUnit === '') ||
      (trimmedSizeValue === '' && trimmedSizeUnit !== '')
    ) {
      setValidationError(
        'Provide both a package size and unit, or leave both blank.',
      );
      return;
    }
    if (trimmedSizeValue !== '' && !/^\d+(?:\.\d+)?$/.test(trimmedSizeValue)) {
      setValidationError('Package size must be a positive decimal number.');
      return;
    }

    try {
      await updateMutation.mutateAsync({
        canonicalName: trimmedName,
        categoryId: categoryId || null,
        sizeValue: trimmedSizeValue || null,
        sizeUnit: trimmedSizeUnit || null,
      });
      onSaveNotice('Product details saved.');
    } catch (error) {
      setServerError(getErrorMessage(error));
    }
  }

  return (
    <form
      onSubmit={(event) => {
        void handleSubmit(event);
      }}
      className="flex flex-col gap-4"
    >
      {validationError ? (
        <ErrorState title="Check product details" message={validationError} />
      ) : null}
      {serverError ? (
        <ErrorState title="Could not save product" message={serverError} />
      ) : null}

      <Input
        label="Canonical name"
        value={canonicalName}
        required
        maxLength={200}
        onChange={(event) => {
          setCanonicalName(event.target.value);
          setValidationError(null);
          setServerError(null);
        }}
      />

      <Select
        label="Category"
        value={categoryId}
        placeholder="Uncategorised"
        options={categoryOptions}
        onChange={(event) => {
          setCategoryId(event.target.value);
          setServerError(null);
        }}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Input
          label="Package size"
          placeholder="e.g. 1 or 350"
          value={sizeValue}
          onChange={(event) => {
            setSizeValue(event.target.value);
            setValidationError(null);
            setServerError(null);
          }}
        />
        <Input
          label="Size unit"
          placeholder="e.g. l, kg, g, ml, ad"
          maxLength={20}
          value={sizeUnit}
          onChange={(event) => {
            setSizeUnit(event.target.value);
            setValidationError(null);
            setServerError(null);
          }}
        />
      </div>

      <div className="flex justify-end pt-1">
        <Button type="submit" loading={updateMutation.isPending}>
          Save details
        </Button>
      </div>
    </form>
  );
}

/** Product catalog detail view with editing, merge/split workflows, and cross-store price history. */
export function ProductDetailPage() {
  const { productId } = useParams<{ productId: string }>();

  const productQuery = useProduct(productId);
  const categoriesQuery = useCategories({ limit: 100 });
  const mergeMutation = useMergeProducts(productId ?? '');
  const splitMutation = useSplitProduct(productId ?? '');

  const [statusMessage, setStatusMessage] = useState<{
    productId: string;
    text: string;
    createdProduct?: { id: string; canonicalName: string };
  } | null>(null);

  // Split selection & dialog state.
  const [selectedAliasIds, setSelectedAliasIds] = useState<string[]>([]);
  const [splitName, setSplitName] = useState('');
  const [splitDialogOpen, setSplitDialogOpen] = useState(false);
  const [splitError, setSplitError] = useState<string | null>(null);

  // Merge picker & dialog state.
  const [mergeDialogOpen, setMergeDialogOpen] = useState(false);
  const [mergeSearch, setMergeSearch] = useState('');
  const [selectedSourceId, setSelectedSourceId] = useState('');
  const [mergeError, setMergeError] = useState<string | null>(null);

  const mergeCandidatesQuery = useProducts({
    q: mergeSearch.trim() || undefined,
    limit: 100,
  });
  const sourceProductQuery = useProduct(
    mergeDialogOpen && selectedSourceId ? selectedSourceId : undefined,
  );

  if (productQuery.isPending) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader eyebrow="Product" title="Loading product" />
        <Card>
          <div className="flex justify-center py-8">
            <Spinner showLabel label="Loading product…" />
          </div>
        </Card>
      </div>
    );
  }

  if (productQuery.isError) {
    if (
      productQuery.error instanceof ApiError &&
      productQuery.error.status === 404
    ) {
      return (
        <div className="flex flex-col gap-6">
          <PageHeader eyebrow="Product" title="Product not found" />
          <EmptyState
            title="Product not found"
            description="This product does not exist or may have been merged into another catalog item."
            action={
              <Link
                to="/products"
                className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover"
              >
                Back to products
              </Link>
            }
          />
        </div>
      );
    }

    return (
      <div className="flex flex-col gap-6">
        <PageHeader eyebrow="Product" title="Product details" />
        <ErrorState
          title="Could not load product"
          message={getErrorMessage(productQuery.error)}
          onRetry={() => {
            void productQuery.refetch();
          }}
        />
      </div>
    );
  }

  const product = productQuery.data;
  const activeStatus =
    statusMessage && statusMessage.productId === product.id
      ? statusMessage
      : null;

  const categoryOptions = (categoriesQuery.data?.items ?? []).map((cat) => ({
    value: cat.id,
    label: cat.name,
  }));

  const aliasIdsSet = new Set(selectedAliasIds);
  const selectedAliases = product.aliases.filter((alias) =>
    aliasIdsSet.has(alias.id),
  );
  const selectedAliasSeenTotal = selectedAliases.reduce(
    (sum, alias) => sum + alias.seenCount,
    0,
  );

  const mergeCandidateOptions = (mergeCandidatesQuery.data?.items ?? [])
    .filter((candidate) => candidate.id !== product.id)
    .map((candidate) => ({
      value: candidate.id,
      label: candidate.category
        ? `${candidate.canonicalName} (${candidate.category.name})`
        : candidate.canonicalName,
    }));

  const descriptionSummary = product.lastPurchase ? (
    <>
      {product.purchaseCount}{' '}
      {product.purchaseCount === 1 ? 'purchase' : 'purchases'} · Last{' '}
      <Money
        value={product.lastPurchase.unitPrice}
        currency={product.lastPurchase.currency}
      />{' '}
      {product.lastPurchase.store
        ? `at ${product.lastPurchase.store.name} on `
        : 'on '}
      <DateText value={product.lastPurchase.purchasedOn} />
    </>
  ) : (
    `${product.purchaseCount} ${product.purchaseCount === 1 ? 'purchase' : 'purchases'}`
  );

  function toggleAliasSelection(aliasId: string, checked: boolean) {
    setSelectedAliasIds((prev) =>
      checked
        ? prev.includes(aliasId)
          ? prev
          : [...prev, aliasId]
        : prev.filter((id) => id !== aliasId),
    );
  }

  function handleOpenMergeDialog() {
    setMergeSearch('');
    setSelectedSourceId('');
    setMergeError(null);
    setMergeDialogOpen(true);
  }

  async function handleConfirmMerge() {
    if (!selectedSourceId) return;
    setMergeError(null);
    const sourceName =
      sourceProductQuery.data?.canonicalName ?? 'selected product';
    try {
      await mergeMutation.mutateAsync({
        sourceProductId: selectedSourceId,
      });
      setMergeDialogOpen(false);
      setSelectedSourceId('');
      setMergeSearch('');
      setStatusMessage({
        productId: product.id,
        text: `Merged ${sourceName} into ${product.canonicalName}.`,
      });
    } catch (error) {
      setMergeError(getErrorMessage(error));
    }
  }

  function handleOpenSplitDialog() {
    setSplitError(null);
    setSplitDialogOpen(true);
  }

  async function handleConfirmSplit() {
    const trimmedSplitName = splitName.trim();
    if (selectedAliases.length === 0 || !trimmedSplitName) return;
    setSplitError(null);
    try {
      const created = await splitMutation.mutateAsync({
        aliasIds: selectedAliases.map((alias) => alias.id),
        canonicalName: trimmedSplitName,
      });
      setSplitDialogOpen(false);
      setSelectedAliasIds([]);
      setSplitName('');
      setStatusMessage({
        productId: product.id,
        text: `Moved ${selectedAliases.length} ${selectedAliases.length === 1 ? 'alias' : 'aliases'} to new product`,
        createdProduct: {
          id: created.id,
          canonicalName: created.canonicalName,
        },
      });
    } catch (error) {
      setSplitError(getErrorMessage(error));
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Product"
        title={product.canonicalName}
        description={`${product.purchaseCount} ${product.purchaseCount === 1 ? 'purchase' : 'purchases'} recorded in catalog`}
        actions={
          <>
            <Link
              to="/products"
              className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-3 py-1.5 text-small font-medium text-ink hover:border-border-strong hover:bg-surface-subtle"
            >
              All products
            </Link>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleOpenMergeDialog}
            >
              Merge another product…
            </Button>
          </>
        }
      />

      <p className="text-small text-muted">{descriptionSummary}</p>

      {activeStatus ? (
        <div
          role="status"
          className="rounded-card border border-success/30 bg-success-soft px-4 py-3 text-small text-ink"
        >
          <span>{activeStatus.text}</span>
          {activeStatus.createdProduct ? (
            <>
              {' '}
              <Link
                to={`/products/${activeStatus.createdProduct.id}`}
                className="font-semibold text-accent hover:underline"
              >
                {activeStatus.createdProduct.canonicalName}
              </Link>
              <span>.</span>
            </>
          ) : null}
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card
          title="Product details"
          description="Edit the canonical display name, category, and normalised package size."
        >
          <ProductEditForm
            key={`${product.id}:${product.updatedAt}`}
            product={product}
            categoryOptions={categoryOptions}
            onSaveNotice={(text) => {
              setStatusMessage({ productId: product.id, text });
            }}
          />
        </Card>

        <Card
          title="Product codes"
          description="Barcodes and store-specific SKUs linked to this product."
        >
          {product.codes.length === 0 ? (
            <p className="text-small text-muted">
              No product codes recorded for this product yet.
            </p>
          ) : (
            <Table
              caption="Linked product codes"
              columns={codeColumns}
              rows={product.codes}
              rowKey={(row) => row.id}
            />
          )}
        </Card>
      </div>

      <Card
        title="Aliases"
        description="Receipt spellings matched to this product. Tick aliases to split them off into a new product."
      >
        <div className="flex flex-col gap-5">
          {product.aliases.length === 0 ? (
            <p className="text-small text-muted">
              No receipt aliases remain on this product.
            </p>
          ) : (
            <>
              <ul className="divide-y divide-border rounded-card border border-border">
                {product.aliases.map((alias) => {
                  const checked = aliasIdsSet.has(alias.id);
                  return (
                    <li
                      key={alias.id}
                      className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
                    >
                      <Checkbox
                        label={alias.rawName}
                        description={`Normalised: ${alias.normalizedName}`}
                        checked={checked}
                        onChange={(event) => {
                          toggleAliasSelection(alias.id, event.target.checked);
                        }}
                      />
                      <div className="flex flex-wrap items-center gap-2 pl-6 sm:pl-0">
                        <Badge variant="neutral">
                          {alias.store?.name ?? 'Any store'}
                        </Badge>
                        <span className="text-caption font-mono tabular-nums text-muted">
                          Seen {alias.seenCount}
                        </span>
                        <span className="text-caption text-muted">
                          <DateText value={alias.lastSeenAt} />
                        </span>
                      </div>
                    </li>
                  );
                })}
              </ul>

              <div className="flex flex-col gap-3 rounded-card border border-border bg-surface-subtle p-4 sm:flex-row sm:items-end">
                <div className="flex-1">
                  <Input
                    label="New product name"
                    placeholder="Name for the split product"
                    maxLength={200}
                    value={splitName}
                    onChange={(event) => {
                      setSplitName(event.target.value);
                    }}
                  />
                </div>
                <Button
                  variant="secondary"
                  disabled={
                    selectedAliases.length === 0 || splitName.trim() === ''
                  }
                  onClick={handleOpenSplitDialog}
                >
                  Split selected aliases…
                </Button>
              </div>
            </>
          )}
        </div>
      </Card>

      <ProductPriceHistoryCard productId={product.id} />

      <Dialog
        open={mergeDialogOpen}
        onClose={() => {
          if (!mergeMutation.isPending) {
            setMergeDialogOpen(false);
          }
        }}
        title="Merge another product"
        description={`Select a duplicate product to merge into ${product.canonicalName}. Its aliases, codes, and purchase links will move here.`}
        footer={
          <>
            <Button
              variant="secondary"
              disabled={mergeMutation.isPending}
              onClick={() => {
                setMergeDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={
                !selectedSourceId ||
                sourceProductQuery.isPending ||
                sourceProductQuery.isError
              }
              loading={mergeMutation.isPending}
              onClick={() => {
                void handleConfirmMerge();
              }}
            >
              Confirm merge
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {mergeError ? (
            <ErrorState title="Could not merge products" message={mergeError} />
          ) : null}

          <Input
            label="Filter products"
            placeholder="Search by product name or alias"
            value={mergeSearch}
            onChange={(event) => {
              setMergeSearch(event.target.value);
            }}
          />

          <Select
            label="Product to merge"
            value={selectedSourceId}
            placeholder="Select a product…"
            options={mergeCandidateOptions}
            onChange={(event) => {
              setSelectedSourceId(event.target.value);
              setMergeError(null);
            }}
          />

          {selectedSourceId ? (
            sourceProductQuery.isPending ? (
              <div className="rounded-card border border-border bg-surface-subtle p-4">
                <Spinner showLabel label="Loading selected product summary…" />
              </div>
            ) : sourceProductQuery.isError ? (
              <ErrorState
                title="Could not load selected product"
                message={getErrorMessage(sourceProductQuery.error)}
              />
            ) : (
              <div className="flex flex-col gap-2 rounded-card border border-border bg-surface-subtle p-4 text-small">
                <p className="font-semibold text-ink">
                  Summary of items moving from{' '}
                  {sourceProductQuery.data.canonicalName}
                </p>
                <ul className="list-disc pl-5 text-muted">
                  <li>
                    {sourceProductQuery.data.aliases.length}{' '}
                    {sourceProductQuery.data.aliases.length === 1
                      ? 'alias'
                      : 'aliases'}{' '}
                    {sourceProductQuery.data.aliases.length > 0
                      ? `(${sourceProductQuery.data.aliases
                          .map((alias) => alias.rawName)
                          .join(', ')})`
                      : ''}
                  </li>
                  <li>
                    {sourceProductQuery.data.codes.length}{' '}
                    {sourceProductQuery.data.codes.length === 1
                      ? 'product code'
                      : 'product codes'}
                  </li>
                  <li>
                    {sourceProductQuery.data.purchaseCount}{' '}
                    {sourceProductQuery.data.purchaseCount === 1
                      ? 'purchase'
                      : 'purchases'}
                  </li>
                </ul>
                <p className="text-caption text-danger">
                  {sourceProductQuery.data.canonicalName} will be deleted after
                  merging into {product.canonicalName}.
                </p>
              </div>
            )
          ) : null}
        </div>
      </Dialog>

      <Dialog
        open={splitDialogOpen}
        onClose={() => {
          if (!splitMutation.isPending) {
            setSplitDialogOpen(false);
          }
        }}
        title="Confirm split"
        description={`Create a new product named ${splitName.trim()} using the selected aliases from ${product.canonicalName}.`}
        footer={
          <>
            <Button
              variant="secondary"
              disabled={splitMutation.isPending}
              onClick={() => {
                setSplitDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="primary"
              loading={splitMutation.isPending}
              onClick={() => {
                void handleConfirmSplit();
              }}
            >
              Confirm split
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4 text-small">
          {splitError ? (
            <ErrorState title="Could not split product" message={splitError} />
          ) : null}

          <div className="flex flex-col gap-2 rounded-card border border-border bg-surface-subtle p-4">
            <p className="font-semibold text-ink">
              New product: {splitName.trim()}
            </p>
            <p className="text-muted">
              {selectedAliases.length}{' '}
              {selectedAliases.length === 1 ? 'alias' : 'aliases'} ({' '}
              {selectedAliasSeenTotal}{' '}
              {selectedAliasSeenTotal === 1 ? 'observation' : 'observations'})
              will move off {product.canonicalName}:
            </p>
            <ul className="list-disc pl-5 text-ink">
              {selectedAliases.map((alias) => (
                <li key={alias.id}>
                  {alias.rawName} ({alias.store?.name ?? 'Any store'} · Seen{' '}
                  {alias.seenCount})
                </li>
              ))}
            </ul>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
