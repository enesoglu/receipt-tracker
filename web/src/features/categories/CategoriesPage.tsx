import { useState, type FormEvent } from 'react';
import {
  useCategories,
  useCreateCategory,
  useDeleteCategory,
  useUpdateCategory,
  type Category,
} from '../../api/categories';
import { formatApiError, getErrorMessage } from '../../api/errors';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Dialog } from '../../components/ui/Dialog';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';

const CATEGORY_PAGE_LIMIT = 100;
const MAX_CATEGORY_NAME_LENGTH = 100;

function formatProductCount(count: number): string {
  return `${count} ${count === 1 ? 'product' : 'products'}`;
}

function validateCategoryName(raw: string): string | null {
  const trimmed = raw.trim();
  if (trimmed.length === 0) {
    return 'Category name is required.';
  }
  if (trimmed.length > MAX_CATEGORY_NAME_LENGTH) {
    return 'Category name must be 100 characters or fewer.';
  }
  return null;
}

/** Category management screen supporting creation, renaming, and in-use delete warnings. */
export function CategoriesPage() {
  const [cursorHistory, setCursorHistory] = useState<(string | undefined)[]>([
    undefined,
  ]);
  const currentCursor = cursorHistory[cursorHistory.length - 1];

  const categoriesQuery = useCategories({
    limit: CATEGORY_PAGE_LIMIT,
    cursor: currentCursor,
  });
  const createCategory = useCreateCategory();
  const updateCategory = useUpdateCategory();
  const deleteCategory = useDeleteCategory();

  const [newName, setNewName] = useState('');
  const [createFieldError, setCreateFieldError] = useState<string | null>(null);
  const [createSubmitError, setCreateSubmitError] = useState<string | null>(
    null,
  );
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  const [renameTarget, setRenameTarget] = useState<Category | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameFieldError, setRenameFieldError] = useState<string | null>(null);
  const [renameSubmitError, setRenameSubmitError] = useState<string | null>(
    null,
  );

  const [deleteTarget, setDeleteTarget] = useState<Category | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const categories = categoriesQuery.data?.items ?? [];
  const nextCursor = categoriesQuery.data?.nextCursor ?? null;

  async function handleCreateSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setStatusMessage(null);
    setCreateSubmitError(null);

    const validationError = validateCategoryName(newName);
    if (validationError) {
      setCreateFieldError(validationError);
      return;
    }

    const trimmed = newName.trim();
    setCreateFieldError(null);

    try {
      const created = await createCategory.mutateAsync({ name: trimmed });
      setNewName('');
      setCursorHistory([undefined]);
      setStatusMessage(`Category "${created.name}" created.`);
    } catch (error) {
      const formatted = formatApiError(error);
      if (formatted.fieldErrors.name) {
        setCreateFieldError(formatted.fieldErrors.name);
      }
      setCreateSubmitError(formatted.message);
    }
  }

  function openRenameDialog(category: Category) {
    setRenameTarget(category);
    setRenameValue(category.name);
    setRenameFieldError(null);
    setRenameSubmitError(null);
  }

  async function submitRename() {
    if (!renameTarget) return;

    setRenameSubmitError(null);
    const validationError = validateCategoryName(renameValue);
    if (validationError) {
      setRenameFieldError(validationError);
      return;
    }

    const trimmed = renameValue.trim();
    setRenameFieldError(null);

    try {
      const updated = await updateCategory.mutateAsync({
        categoryId: renameTarget.id,
        body: { name: trimmed },
      });
      setRenameTarget(null);
      setStatusMessage(`Category renamed to "${updated.name}".`);
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

  function openDeleteDialog(category: Category) {
    setDeleteTarget(category);
    setDeleteError(null);
  }

  async function handleConfirmDelete() {
    if (!deleteTarget) return;
    setDeleteError(null);
    const removedName = deleteTarget.name;

    try {
      await deleteCategory.mutateAsync(deleteTarget.id);
      setDeleteTarget(null);
      setStatusMessage(`Category "${removedName}" deleted.`);
    } catch (error) {
      setDeleteError(getErrorMessage(error));
    }
  }

  const columns: readonly TableColumn<Category>[] = [
    {
      key: 'name',
      header: 'Category',
      cell: (row) => <span className="font-medium text-ink">{row.name}</span>,
    },
    {
      key: 'productCount',
      header: 'Products',
      cell: (row) => (
        <Badge
          variant={row.productCount > 0 ? 'accent' : 'neutral'}
          className="whitespace-nowrap"
        >
          {formatProductCount(row.productCount)}
        </Badge>
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
            aria-label={`Rename ${row.name}`}
            onClick={() => openRenameDialog(row)}
          >
            Rename
          </Button>
          <Button
            variant="danger"
            size="sm"
            aria-label={`Delete ${row.name}`}
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
        title="Categories"
        description="Create, rename, and remove spending categories applied across your products."
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
        title="Add category"
        description="Category names are unique (case-insensitive) and up to 100 characters."
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
              title="Could not create category"
              message={createSubmitError}
            />
          ) : null}

          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div className="min-w-0 flex-1">
              <Input
                label="Category name"
                placeholder="e.g. Beverages"
                maxLength={MAX_CATEGORY_NAME_LENGTH}
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
              loading={createCategory.isPending}
              className="sm:mb-0.5"
            >
              Create category
            </Button>
          </div>
        </form>
      </Card>

      {categoriesQuery.isPending ? (
        <div className="flex items-center justify-center rounded-card border border-border bg-surface p-10">
          <Spinner showLabel label="Loading categories…" />
        </div>
      ) : categoriesQuery.isError ? (
        <ErrorState
          title="Could not load categories"
          message={getErrorMessage(categoriesQuery.error)}
          onRetry={() => {
            void categoriesQuery.refetch();
          }}
        />
      ) : categories.length === 0 ? (
        <EmptyState
          title="No categories yet"
          description="Create your first category above to organize products across receipts."
        />
      ) : (
        <section aria-label="Category list" className="flex flex-col gap-4">
          <Table
            caption="Spending categories ordered by name"
            columns={columns}
            rows={categories}
            rowKey={(row) => row.id}
          />

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
          if (!updateCategory.isPending) {
            setRenameTarget(null);
          }
        }}
        title={
          renameTarget ? `Rename "${renameTarget.name}"` : 'Rename category'
        }
        description="Products using this category will keep their assignment under the new name."
        footer={
          <>
            <Button
              variant="secondary"
              disabled={updateCategory.isPending}
              onClick={() => {
                setRenameTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="primary"
              loading={updateCategory.isPending}
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
              title="Could not rename category"
              message={renameSubmitError}
            />
          ) : null}

          <Input
            label="New name"
            maxLength={MAX_CATEGORY_NAME_LENGTH}
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
        open={deleteTarget !== null}
        onClose={() => {
          if (!deleteCategory.isPending) {
            setDeleteTarget(null);
          }
        }}
        title={
          deleteTarget ? `Delete "${deleteTarget.name}"?` : 'Delete category'
        }
        footer={
          <>
            <Button
              variant="secondary"
              disabled={deleteCategory.isPending}
              onClick={() => {
                setDeleteTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={deleteCategory.isPending}
              onClick={() => {
                void handleConfirmDelete();
              }}
            >
              Delete category
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4 text-small">
          {deleteError ? (
            <ErrorState
              title="Could not delete category"
              message={deleteError}
            />
          ) : null}

          {deleteTarget && deleteTarget.productCount > 0 ? (
            <div
              role="alert"
              className="flex flex-col gap-1.5 rounded-card border border-warning/40 bg-warning-soft p-4"
            >
              <p className="font-semibold text-warning">
                Category currently in use by{' '}
                {formatProductCount(deleteTarget.productCount)}
              </p>
              <p className="text-ink">
                Deleting &ldquo;{deleteTarget.name}&rdquo; will not delete any
                products. Instead,{' '}
                {formatProductCount(deleteTarget.productCount)} will stay in
                your catalog as uncategorised.
              </p>
            </div>
          ) : (
            <p className="text-muted">
              No products currently use &ldquo;{deleteTarget?.name}&rdquo;.
              Deleting this category cannot be undone.
            </p>
          )}
        </div>
      </Dialog>
    </div>
  );
}
