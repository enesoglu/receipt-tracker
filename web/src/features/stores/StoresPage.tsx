import { useState, type FormEvent } from 'react';
import {
  useCreateStore,
  useDeleteStore,
  useMergeStore,
  useStore,
  useStores,
  useUpdateStore,
  type Store,
} from '../../api/stores';
import { formatApiError, getErrorMessage } from '../../api/errors';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { Dialog } from '../../components/ui/Dialog';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { PageHeader } from '../../components/ui/PageHeader';
import { Select, type SelectOption } from '../../components/ui/Select';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';

const STORE_PAGE_LIMIT = 100;
const MAX_STORE_NAME_LENGTH = 200;

function formatReceiptCount(count: number): string {
  return `${count} ${count === 1 ? 'receipt' : 'receipts'}`;
}

function validateStoreName(raw: string): string | null {
  const trimmed = raw.trim();
  if (trimmed.length === 0) {
    return 'Store name is required.';
  }
  if (trimmed.length > MAX_STORE_NAME_LENGTH) {
    return 'Store name must be 200 characters or fewer.';
  }
  return null;
}

/** Store management screen supporting creation, rename, merge, and delete of unused stores. */
export function StoresPage() {
  const [cursorHistory, setCursorHistory] = useState<(string | undefined)[]>([
    undefined,
  ]);
  const currentCursor = cursorHistory[cursorHistory.length - 1];

  const storesQuery = useStores({
    limit: STORE_PAGE_LIMIT,
    cursor: currentCursor,
  });
  const createStore = useCreateStore();
  const updateStore = useUpdateStore();
  const mergeStore = useMergeStore();
  const deleteStore = useDeleteStore();

  const [newName, setNewName] = useState('');
  const [createFieldError, setCreateFieldError] = useState<string | null>(null);
  const [createSubmitError, setCreateSubmitError] = useState<string | null>(
    null,
  );
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  const [renameTarget, setRenameTarget] = useState<Store | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameFieldError, setRenameFieldError] = useState<string | null>(null);
  const [renameSubmitError, setRenameSubmitError] = useState<string | null>(
    null,
  );

  const [mergeTarget, setMergeTarget] = useState<Store | null>(null);
  const [mergeSourceId, setMergeSourceId] = useState('');
  const [mergeError, setMergeError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<Store | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const [expandedStoreId, setExpandedStoreId] = useState<string | null>(null);

  const stores = storesQuery.data?.items ?? [];
  const nextCursor = storesQuery.data?.nextCursor ?? null;
  const expandedStore =
    stores.find((store) => store.id === expandedStoreId) ?? null;
  const detailQuery = useStore(expandedStore?.id);

  const mergeOptions: readonly SelectOption[] =
    mergeTarget === null
      ? []
      : stores
          .filter((store) => store.id !== mergeTarget.id)
          .map((store) => ({
            value: store.id,
            label: `${store.name} (${formatReceiptCount(store.receiptCount)})`,
          }));

  async function handleCreateSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setStatusMessage(null);
    setCreateSubmitError(null);

    const validationError = validateStoreName(newName);
    if (validationError) {
      setCreateFieldError(validationError);
      return;
    }

    const trimmed = newName.trim();
    setCreateFieldError(null);

    try {
      const created = await createStore.mutateAsync({ name: trimmed });
      setNewName('');
      setCursorHistory([undefined]);
      setStatusMessage(`Store "${created.name}" created.`);
    } catch (error) {
      const formatted = formatApiError(error);
      if (formatted.fieldErrors.name) {
        setCreateFieldError(formatted.fieldErrors.name);
      }
      setCreateSubmitError(formatted.message);
    }
  }

  function openRenameDialog(store: Store) {
    setRenameTarget(store);
    setRenameValue(store.name);
    setRenameFieldError(null);
    setRenameSubmitError(null);
  }

  async function submitRename() {
    if (!renameTarget) return;

    setRenameSubmitError(null);
    const validationError = validateStoreName(renameValue);
    if (validationError) {
      setRenameFieldError(validationError);
      return;
    }

    const trimmed = renameValue.trim();
    setRenameFieldError(null);

    try {
      const updated = await updateStore.mutateAsync({
        storeId: renameTarget.id,
        body: { name: trimmed },
      });
      setRenameTarget(null);
      setStatusMessage(`Store renamed to "${updated.name}".`);
    } catch (error) {
      const formatted = formatApiError(error);
      if (formatted.fieldErrors.name) {
        setRenameFieldError(formatted.fieldErrors.name);
      }
      setRenameSubmitError(formatted.message);
    }
  }

  function handleRenameSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void submitRename();
  }

  function openMergeDialog(store: Store) {
    setMergeTarget(store);
    setMergeSourceId('');
    setMergeError(null);
  }

  async function handleConfirmMerge() {
    if (!mergeTarget || !mergeSourceId) return;
    setMergeError(null);
    const sourceName =
      stores.find((store) => store.id === mergeSourceId)?.name ??
      'the source store';

    try {
      const merged = await mergeStore.mutateAsync({
        storeId: mergeTarget.id,
        body: { sourceId: mergeSourceId },
      });
      setMergeTarget(null);
      setStatusMessage(`Merged ${sourceName} into ${merged.name}.`);
    } catch (error) {
      setMergeError(getErrorMessage(error));
    }
  }

  function openDeleteDialog(store: Store) {
    setDeleteTarget(store);
    setDeleteError(null);
  }

  async function handleConfirmDelete() {
    if (!deleteTarget) return;
    setDeleteError(null);
    const removedName = deleteTarget.name;

    try {
      await deleteStore.mutateAsync(deleteTarget.id);
      setDeleteTarget(null);
      if (expandedStoreId === deleteTarget.id) {
        setExpandedStoreId(null);
      }
      setStatusMessage(`Store "${removedName}" deleted.`);
    } catch (error) {
      setDeleteError(getErrorMessage(error));
    }
  }

  const columns: readonly TableColumn<Store>[] = [
    {
      key: 'name',
      header: 'Store',
      cell: (row) => <span className="font-medium text-ink">{row.name}</span>,
    },
    {
      key: 'receiptCount',
      header: 'Receipts',
      cell: (row) => (
        <Badge
          variant={row.receiptCount > 0 ? 'accent' : 'neutral'}
          className="whitespace-nowrap"
        >
          {formatReceiptCount(row.receiptCount)}
        </Badge>
      ),
    },
    {
      key: 'vkn',
      header: 'Tax number',
      cell: (row) => (
        <span
          className={`font-mono tabular-nums ${row.vkn === null ? 'text-muted' : 'text-ink'}`}
        >
          {row.vkn ?? 'Unknown'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: 'Actions',
      align: 'right',
      cell: (row) => (
        <div className="flex flex-wrap justify-end gap-2">
          <Button
            variant="secondary"
            size="sm"
            aria-expanded={expandedStoreId === row.id}
            aria-label={`Details for ${row.name}`}
            onClick={() => {
              setExpandedStoreId((prev) => (prev === row.id ? null : row.id));
            }}
          >
            {expandedStoreId === row.id ? 'Hide details' : 'Details'}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            aria-label={`Rename ${row.name}`}
            onClick={() => openRenameDialog(row)}
          >
            Rename
          </Button>
          <Button
            variant="secondary"
            size="sm"
            aria-label={`Merge into ${row.name}`}
            onClick={() => openMergeDialog(row)}
          >
            Merge…
          </Button>
          <Button
            variant="danger"
            size="sm"
            aria-label={`Delete ${row.name}`}
            disabled={row.receiptCount > 0}
            onClick={() => openDeleteDialog(row)}
          >
            Delete
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Organization"
        title="Stores"
        description="Create, rename, merge, and remove the shops your receipts name — one store per chain, branches stay on the receipt."
      />

      {statusMessage ? (
        <div
          role="status"
          className="rounded-card border border-success/30 bg-success-soft px-4 py-3 text-small font-medium text-success"
        >
          {statusMessage}
        </div>
      ) : null}

      <Card
        title="Add store"
        description="Store names are unique (case-insensitive) and up to 200 characters. The seller tax number is machine-assigned."
      >
        <form
          onSubmit={(event) => {
            void handleCreateSubmit(event);
          }}
          noValidate
          className="flex flex-col gap-4"
        >
          {createSubmitError ? (
            <ErrorState
              title="Could not create store"
              message={createSubmitError}
            />
          ) : null}

          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div className="min-w-0 flex-1">
              <Input
                label="Store name"
                placeholder="e.g. Migros"
                maxLength={MAX_STORE_NAME_LENGTH}
                value={newName}
                error={createFieldError ?? undefined}
                onChange={(event) => {
                  setNewName(event.target.value);
                  setCreateFieldError(null);
                  setCreateSubmitError(null);
                }}
              />
            </div>
            <Button
              type="submit"
              loading={createStore.isPending}
              className="sm:mb-0.5"
            >
              Create store
            </Button>
          </div>
        </form>
      </Card>

      {storesQuery.isPending ? (
        <div className="flex items-center justify-center rounded-card border border-border bg-surface p-10">
          <Spinner showLabel label="Loading stores…" />
        </div>
      ) : storesQuery.isError ? (
        <ErrorState
          title="Could not load stores"
          message={getErrorMessage(storesQuery.error)}
          onRetry={() => {
            void storesQuery.refetch();
          }}
        />
      ) : stores.length === 0 ? (
        <EmptyState
          title="No stores yet"
          description="Stores appear here as receipts name them. Create one ahead of your next upload, or let a receipt introduce its store."
        />
      ) : (
        <section aria-label="Store list" className="flex flex-col gap-4">
          <Table
            caption="Stores ordered by name"
            columns={columns}
            rows={stores}
            rowKey={(row) => row.id}
          />

          {expandedStore ? (
            <Card
              title={`Store details — ${expandedStore.name}`}
              description="The raw names and branches this store's receipts showed."
            >
              {detailQuery.isPending ? (
                <div className="flex justify-center py-6">
                  <Spinner showLabel label="Loading store details…" />
                </div>
              ) : detailQuery.isError ? (
                <ErrorState
                  title="Could not load store details"
                  message={getErrorMessage(detailQuery.error)}
                  onRetry={() => {
                    void detailQuery.refetch();
                  }}
                />
              ) : (
                <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <section
                    aria-label="Names seen"
                    className="flex flex-col gap-3"
                  >
                    <h3 className="text-small font-semibold text-ink">
                      Names seen
                    </h3>
                    {detailQuery.data.aliases.length === 0 ? (
                      <p className="text-small text-muted">
                        No raw names recorded yet.
                      </p>
                    ) : (
                      <ul className="flex flex-col gap-2">
                        {detailQuery.data.aliases.map((alias) => (
                          <li
                            key={alias.rawName}
                            className="flex flex-wrap items-baseline justify-between gap-2 text-small"
                          >
                            <span className="font-medium text-ink">
                              {alias.rawName}
                            </span>
                            <span className="text-muted">
                              {alias.seenCount}{' '}
                              {alias.seenCount === 1 ? 'time' : 'times'} · last
                              seen <DateText value={alias.lastSeenAt} />
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                  <section
                    aria-label="Branches seen"
                    className="flex flex-col gap-3"
                  >
                    <h3 className="text-small font-semibold text-ink">
                      Branches seen
                    </h3>
                    {detailQuery.data.branches.length === 0 ? (
                      <p className="text-small text-muted">
                        No branches recorded yet.
                      </p>
                    ) : (
                      <ul className="flex flex-col gap-2">
                        {detailQuery.data.branches.map((branch) => (
                          <li
                            key={branch.name}
                            className="flex flex-wrap items-baseline justify-between gap-2 text-small"
                          >
                            <span className="font-medium text-ink">
                              {branch.name}
                            </span>
                            <span className="text-muted">
                              {formatReceiptCount(branch.receiptCount)}
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                </div>
              )}
            </Card>
          ) : null}

          {cursorHistory.length > 1 || nextCursor !== null ? (
            <div className="flex items-center justify-end gap-2">
              {cursorHistory.length > 1 ? (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    setCursorHistory((prev) =>
                      prev.length > 1 ? prev.slice(0, -1) : prev,
                    );
                  }}
                >
                  Previous page
                </Button>
              ) : null}
              {nextCursor !== null ? (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    setCursorHistory((prev) => [...prev, nextCursor]);
                  }}
                >
                  Next page
                </Button>
              ) : null}
            </div>
          ) : null}
        </section>
      )}

      <Dialog
        open={renameTarget !== null}
        onClose={() => {
          if (!updateStore.isPending) {
            setRenameTarget(null);
          }
        }}
        title={renameTarget ? `Rename "${renameTarget.name}"` : 'Rename store'}
        description="Receipts keep pointing at this store under the new name; remembered raw names stay untouched."
        footer={
          <>
            <Button
              variant="secondary"
              disabled={updateStore.isPending}
              onClick={() => {
                setRenameTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="primary"
              loading={updateStore.isPending}
              onClick={() => {
                void submitRename();
              }}
            >
              Save name
            </Button>
          </>
        }
      >
        <form
          onSubmit={handleRenameSubmit}
          noValidate
          className="flex flex-col gap-4"
        >
          {renameSubmitError ? (
            <ErrorState
              title="Could not rename store"
              message={renameSubmitError}
            />
          ) : null}

          <Input
            label="New name"
            maxLength={MAX_STORE_NAME_LENGTH}
            value={renameValue}
            error={renameFieldError ?? undefined}
            onChange={(event) => {
              setRenameValue(event.target.value);
              setRenameFieldError(null);
              setRenameSubmitError(null);
            }}
          />
        </form>
      </Dialog>

      <Dialog
        open={mergeTarget !== null}
        onClose={() => {
          if (!mergeStore.isPending) {
            setMergeTarget(null);
          }
        }}
        title="Merge another store"
        description={
          mergeTarget
            ? `Select the duplicate store to merge into ${mergeTarget.name}. Its receipts, remembered names, and branch records will move here.`
            : undefined
        }
        footer={
          <>
            <Button
              variant="secondary"
              disabled={mergeStore.isPending}
              onClick={() => {
                setMergeTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={!mergeSourceId}
              loading={mergeStore.isPending}
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
            <ErrorState title="Could not merge stores" message={mergeError} />
          ) : null}

          <Select
            label="Store to merge"
            value={mergeSourceId}
            placeholder="Select a store…"
            options={mergeOptions}
            onChange={(event) => {
              setMergeSourceId(event.target.value);
              setMergeError(null);
            }}
          />

          <p className="text-caption text-danger">
            The store you pick is deleted after merging into {mergeTarget?.name}
            . This cannot be undone.
          </p>
        </div>
      </Dialog>

      <Dialog
        open={deleteTarget !== null}
        onClose={() => {
          if (!deleteStore.isPending) {
            setDeleteTarget(null);
          }
        }}
        title={deleteTarget ? `Delete "${deleteTarget.name}"?` : 'Delete store'}
        footer={
          <>
            <Button
              variant="secondary"
              disabled={deleteStore.isPending}
              onClick={() => {
                setDeleteTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={deleteStore.isPending}
              onClick={() => {
                void handleConfirmDelete();
              }}
            >
              Delete store
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4 text-small">
          {deleteError ? (
            <ErrorState title="Could not delete store" message={deleteError} />
          ) : null}

          <p className="text-muted">
            &ldquo;{deleteTarget?.name}&rdquo; has no receipts. Deleting it also
            forgets the raw names seen under it. This cannot be undone.
          </p>
        </div>
      </Dialog>
    </div>
  );
}
