import { useState, type FormEvent } from 'react';
import { formatApiError } from '../../api/errors';
import {
  useCreateLineItem,
  useDeleteLineItem,
  useUpdateLineItem,
  type LineItem,
  type LineItemInput,
  type Receipt,
} from '../../api/receipts';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { EmptyState } from '../../components/ui/EmptyState';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import {
  formatReviewFlagSummary,
  getOpenFlagsForLineItem,
} from './flag-reasons';

interface LineItemFormValues {
  name: string;
  quantity: string;
  quantityUnit: string;
  unitPrice: string;
  lineTotal: string;
  productCode: string;
}

const EMPTY_FORM: LineItemFormValues = {
  name: '',
  quantity: '',
  quantityUnit: '',
  unitPrice: '',
  lineTotal: '',
  productCode: '',
};

function toFormValues(item: LineItem): LineItemFormValues {
  return {
    name: item.name,
    quantity: item.quantity ?? '',
    quantityUnit: item.quantityUnit ?? '',
    unitPrice: item.unitPrice ?? '',
    lineTotal: item.lineTotal ?? '',
    productCode: item.productCode ?? '',
  };
}

function toNullable(value: string): string | null {
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

function toLineItemInput(values: LineItemFormValues): LineItemInput {
  return {
    name: values.name.trim(),
    quantity: toNullable(values.quantity),
    quantityUnit: toNullable(values.quantityUnit),
    unitPrice: toNullable(values.unitPrice),
    lineTotal: values.lineTotal.trim(),
    productCode: toNullable(values.productCode),
  };
}

function pickFieldError(
  fieldErrors: Record<string, string>,
  ...keys: string[]
): string | undefined {
  for (const key of keys) {
    if (fieldErrors[key]) {
      return fieldErrors[key];
    }
  }
  return undefined;
}

interface LineItemEditorFieldsProps {
  values: LineItemFormValues;
  fieldErrors: Record<string, string>;
  onChange: (next: LineItemFormValues) => void;
}

function LineItemEditorFields({
  values,
  fieldErrors,
  onChange,
}: LineItemEditorFieldsProps) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-6">
      <div className="sm:col-span-2 lg:col-span-2">
        <Input
          label="Item name"
          required
          value={values.name}
          error={pickFieldError(fieldErrors, 'name')}
          onChange={(event) =>
            onChange({ ...values, name: event.target.value })
          }
        />
      </div>
      <Input
        label="Quantity"
        placeholder="e.g. 2"
        value={values.quantity}
        error={pickFieldError(fieldErrors, 'quantity')}
        onChange={(event) =>
          onChange({ ...values, quantity: event.target.value })
        }
      />
      <Input
        label="Unit"
        placeholder="e.g. kg"
        value={values.quantityUnit}
        error={pickFieldError(fieldErrors, 'quantityUnit', 'quantity_unit')}
        onChange={(event) =>
          onChange({ ...values, quantityUnit: event.target.value })
        }
      />
      <Input
        label="Unit price"
        placeholder="e.g. 12.50"
        value={values.unitPrice}
        error={pickFieldError(fieldErrors, 'unitPrice', 'unit_price')}
        onChange={(event) =>
          onChange({ ...values, unitPrice: event.target.value })
        }
      />
      <Input
        label="Line total"
        required
        placeholder="e.g. 25.00"
        value={values.lineTotal}
        error={pickFieldError(fieldErrors, 'lineTotal', 'line_total')}
        onChange={(event) =>
          onChange({ ...values, lineTotal: event.target.value })
        }
      />
      <div className="sm:col-span-2 lg:col-span-2">
        <Input
          label="Product code"
          placeholder="Optional barcode or PLU"
          value={values.productCode}
          error={pickFieldError(fieldErrors, 'productCode', 'product_code')}
          onChange={(event) =>
            onChange({ ...values, productCode: event.target.value })
          }
        />
      </div>
    </div>
  );
}

export interface ReceiptLineItemsCardProps {
  receipt: Receipt;
}

/** Editable list of receipt line items with inline updates, creation, and deletion. */
export function ReceiptLineItemsCard({ receipt }: ReceiptLineItemsCardProps) {
  const createLineItem = useCreateLineItem(receipt.id);
  const updateLineItem = useUpdateLineItem(receipt.id);
  const deleteLineItem = useDeleteLineItem(receipt.id);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValues, setEditValues] = useState<LineItemFormValues>(EMPTY_FORM);
  const [editError, setEditError] = useState<ReturnType<
    typeof formatApiError
  > | null>(null);

  const [adding, setAdding] = useState(false);
  const [addValues, setAddValues] = useState<LineItemFormValues>(EMPTY_FORM);
  const [addError, setAddError] = useState<ReturnType<
    typeof formatApiError
  > | null>(null);

  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  function startEdit(item: LineItem) {
    setAdding(false);
    setAddError(null);
    setDeleteError(null);
    setEditingId(item.id);
    setEditValues(toFormValues(item));
    setEditError(null);
  }

  function startAdd() {
    setEditingId(null);
    setEditError(null);
    setDeleteError(null);
    setAdding(true);
    setAddValues(EMPTY_FORM);
    setAddError(null);
  }

  async function handleSaveEdit(event: FormEvent<HTMLFormElement>, id: string) {
    event.preventDefault();
    setEditError(null);
    try {
      await updateLineItem.mutateAsync({
        lineItemId: id,
        body: toLineItemInput(editValues),
      });
      setEditingId(null);
    } catch (error) {
      setEditError(formatApiError(error));
    }
  }

  async function handleAddSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAddError(null);
    try {
      await createLineItem.mutateAsync(toLineItemInput(addValues));
      setAddValues(EMPTY_FORM);
      setAdding(false);
    } catch (error) {
      setAddError(formatApiError(error));
    }
  }

  async function handleDelete(item: LineItem) {
    setDeleteError(null);
    setDeletingId(item.id);
    try {
      await deleteLineItem.mutateAsync(item.id);
      if (editingId === item.id) {
        setEditingId(null);
        setEditError(null);
      }
    } catch (error) {
      setDeleteError(formatApiError(error).message);
    } finally {
      setDeletingId(null);
    }
  }

  return (
    <Card
      title="Line items"
      description={`${receipt.lineItems.length} item${receipt.lineItems.length === 1 ? '' : 's'} on this receipt`}
      actions={
        !adding ? (
          <Button variant="secondary" size="sm" onClick={startAdd}>
            Add line item
          </Button>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-4">
        {deleteError ? (
          <p
            role="alert"
            className="rounded-control border border-danger bg-danger-soft px-3 py-2 text-small text-danger"
          >
            {deleteError}
          </p>
        ) : null}

        {receipt.lineItems.length === 0 && !adding ? (
          <EmptyState
            title="No line items yet"
            description="Extracted line items will appear here, or you can add items manually."
            action={
              <Button variant="secondary" size="sm" onClick={startAdd}>
                Add line item
              </Button>
            }
          />
        ) : (
          <ul className="flex flex-col gap-3">
            {receipt.lineItems.map((item) => {
              const itemFlags = getOpenFlagsForLineItem(receipt.flags, item.id);
              const isFlagged = itemFlags.length > 0;
              const isEditing = editingId === item.id;
              const isDeleting = deletingId === item.id;

              return (
                <li
                  key={item.id}
                  className={
                    isFlagged
                      ? 'rounded-control border border-warning bg-warning-soft p-4'
                      : 'rounded-control border border-border bg-surface p-4'
                  }
                >
                  {isEditing ? (
                    <form
                      onSubmit={(event) => void handleSaveEdit(event, item.id)}
                      className="flex flex-col gap-4"
                    >
                      <LineItemEditorFields
                        values={editValues}
                        fieldErrors={editError?.fieldErrors ?? {}}
                        onChange={setEditValues}
                      />
                      {editError ? (
                        <p role="alert" className="text-small text-danger">
                          {editError.message}
                        </p>
                      ) : null}
                      <div className="flex flex-wrap gap-2">
                        <Button
                          type="submit"
                          size="sm"
                          loading={updateLineItem.isPending}
                        >
                          Save line
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setEditingId(null);
                            setEditError(null);
                          }}
                        >
                          Cancel
                        </Button>
                      </div>
                    </form>
                  ) : (
                    <div className="flex flex-col gap-3">
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="flex min-w-0 flex-1 flex-col gap-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="text-body font-medium text-ink">
                              {item.name}
                            </span>
                            {item.product ? (
                              <Badge variant="success">
                                {item.product.canonicalName === item.name
                                  ? 'Matched'
                                  : `Product: ${item.product.canonicalName}`}
                              </Badge>
                            ) : (
                              <Badge variant="neutral">Unmatched</Badge>
                            )}
                            {itemFlags.map((flag) => {
                              const summary = formatReviewFlagSummary(flag);
                              return (
                                <Badge key={flag.id} variant="warning">
                                  {summary.label}
                                </Badge>
                              );
                            })}
                          </div>
                          <div className="flex flex-wrap gap-x-4 gap-y-1 text-small text-muted">
                            <span>
                              Qty: {item.quantity ?? '—'}
                              {item.quantityUnit ? ` ${item.quantityUnit}` : ''}
                            </span>
                            <span>
                              Unit price:{' '}
                              {item.unitPrice ? (
                                <Money
                                  value={item.unitPrice}
                                  currency={receipt.currency}
                                />
                              ) : (
                                '—'
                              )}
                            </span>
                            {item.productCode ? (
                              <span>Code: {item.productCode}</span>
                            ) : null}
                            {item.rawName && item.rawName !== item.name ? (
                              <span>OCR: {item.rawName}</span>
                            ) : null}
                          </div>
                        </div>

                        <div className="flex items-center gap-3">
                          <div className="text-right">
                            <div className="text-body font-medium text-ink">
                              <Money
                                value={item.lineTotal}
                                currency={receipt.currency}
                              />
                            </div>
                          </div>
                          <div className="flex items-center gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-label={`Edit ${item.name}`}
                              onClick={() => startEdit(item)}
                            >
                              Edit
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-label={`Delete ${item.name}`}
                              loading={isDeleting}
                              onClick={() => void handleDelete(item)}
                            >
                              Delete
                            </Button>
                          </div>
                        </div>
                      </div>

                      {itemFlags.length > 0 ? (
                        <ul className="flex flex-col gap-1 text-small text-warning">
                          {itemFlags.map((flag) => {
                            const summary = formatReviewFlagSummary(flag);
                            return <li key={flag.id}>{summary.reason}</li>;
                          })}
                        </ul>
                      ) : null}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}

        {adding ? (
          <form
            onSubmit={(event) => void handleAddSubmit(event)}
            className="flex flex-col gap-4 rounded-control border border-border bg-surface-subtle p-4"
          >
            <h3 className="text-small font-semibold text-ink">New line item</h3>
            <LineItemEditorFields
              values={addValues}
              fieldErrors={addError?.fieldErrors ?? {}}
              onChange={setAddValues}
            />
            {addError ? (
              <p role="alert" className="text-small text-danger">
                {addError.message}
              </p>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <Button
                type="submit"
                size="sm"
                loading={createLineItem.isPending}
              >
                Add line
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setAdding(false);
                  setAddError(null);
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        ) : null}
      </div>
    </Card>
  );
}
