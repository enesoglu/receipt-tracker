import { useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { formatApiError, getErrorMessage } from '../../api/errors';
import {
  receiptImageUrl,
  useDeleteReceipt,
  useReceipt,
  useReprocessReceipt,
  useUpdateReceipt,
  type Receipt,
  type ReceiptUpdate,
} from '../../api/receipts';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { Dialog } from '../../components/ui/Dialog';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';
import {
  formatReceiptStatus,
  formatReviewFlagSummary,
  getOpenFlagsForField,
  type ReviewFlag,
} from './flag-reasons';
import { ReceiptLineItemsCard } from './ReceiptLineItemsCard';

interface ReceiptFormValues {
  title: string;
  storeName: string;
  purchasedOn: string;
  purchasedTime: string;
  currency: string;
  subtotal: string;
  taxTotal: string;
  total: string;
}

function toReceiptFormValues(receipt: Receipt): ReceiptFormValues {
  return {
    title: receipt.title.trim() || 'Home shopping',
    storeName: receipt.store?.name ?? '',
    purchasedOn: receipt.purchasedOn ?? '',
    purchasedTime: receipt.purchasedTime ?? '',
    currency: receipt.currency,
    subtotal: receipt.subtotal ?? '',
    taxTotal: receipt.taxTotal ?? '',
    total: receipt.total ?? '',
  };
}

function toNullable(value: string): string | null {
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

function toReceiptUpdate(values: ReceiptFormValues): ReceiptUpdate {
  return {
    title: values.title.trim() || 'Home shopping',
    storeName: toNullable(values.storeName),
    purchasedOn: toNullable(values.purchasedOn),
    purchasedTime: toNullable(values.purchasedTime),
    currency: values.currency.trim().toUpperCase() || 'TRY',
    subtotal: toNullable(values.subtotal),
    taxTotal: toNullable(values.taxTotal),
    total: toNullable(values.total),
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

interface FieldFlagNoticeProps {
  flags: readonly ReviewFlag[];
}

function FieldFlagNotice({ flags }: FieldFlagNoticeProps) {
  if (flags.length === 0) return null;
  return (
    <div className="flex flex-col gap-1">
      {flags.map((flag) => {
        const summary = formatReviewFlagSummary(flag);
        return (
          <div
            key={flag.id}
            className="flex flex-wrap items-center gap-2 text-small text-warning"
          >
            <Badge variant="warning">{summary.label}</Badge>
            <span>{summary.reason}</span>
          </div>
        );
      })}
    </div>
  );
}

interface ReceiptDetailContentProps {
  receipt: Receipt;
  isRefreshing: boolean;
  onRefresh: () => void;
}

function ReceiptDetailContent({
  receipt,
  isRefreshing,
  onRefresh,
}: ReceiptDetailContentProps) {
  const navigate = useNavigate();
  const updateReceipt = useUpdateReceipt(receipt.id);
  const reprocessReceipt = useReprocessReceipt(receipt.id);
  const deleteReceipt = useDeleteReceipt();

  const [formValues, setFormValues] = useState<ReceiptFormValues>(() =>
    toReceiptFormValues(receipt),
  );
  const [saveError, setSaveError] = useState<ReturnType<
    typeof formatApiError
  > | null>(null);
  const [savedNotice, setSavedNotice] = useState(false);

  const [confirmAction, setConfirmAction] = useState<
    'reprocess' | 'delete' | null
  >(null);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [reprocessNotice, setReprocessNotice] = useState<string | null>(null);

  const statusMeta = formatReceiptStatus(receipt.status);
  const openFlags = receipt.flags.filter((flag) => flag.resolvedAt === null);
  const duplicateFlags = openFlags.filter(
    (flag): flag is Extract<ReviewFlag, { kind: 'possible_duplicate' }> =>
      flag.kind === 'possible_duplicate',
  );
  const storeFlags = getOpenFlagsForField(receipt.flags, 'store');
  const dateFlags = getOpenFlagsForField(receipt.flags, 'purchased_on');
  const totalFlags = getOpenFlagsForField(receipt.flags, 'total');

  const fieldErrors = saveError?.fieldErrors ?? {};
  const displayTitle = receipt.title.trim() || 'Home shopping';

  async function handleSaveHeader(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaveError(null);
    setSavedNotice(false);
    try {
      const updated = await updateReceipt.mutateAsync(
        toReceiptUpdate(formValues),
      );
      setFormValues(toReceiptFormValues(updated));
      setSavedNotice(true);
    } catch (error) {
      setSaveError(formatApiError(error));
    }
  }

  async function handleConfirmReprocess() {
    setDialogError(null);
    try {
      await reprocessReceipt.mutateAsync();
      setConfirmAction(null);
      setReprocessNotice(
        'Reprocessing queued. Refresh or check back shortly for updated extraction results.',
      );
    } catch (error) {
      setDialogError(getErrorMessage(error));
    }
  }

  async function handleConfirmDelete() {
    setDialogError(null);
    try {
      await deleteReceipt.mutateAsync(receipt.id);
      setConfirmAction(null);
      void navigate('/receipts', { replace: true });
    } catch (error) {
      setDialogError(getErrorMessage(error));
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Link
          to="/receipts"
          className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-3 py-1.5 text-small font-medium text-ink hover:bg-surface-subtle focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Back to receipts
        </Link>
        {openFlags.length > 0 ? (
          <Link
            to="/review"
            className="inline-flex items-center justify-center rounded-control border border-warning bg-warning-soft px-3 py-1.5 text-small font-medium text-warning hover:bg-surface focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Review flags ({openFlags.length})
          </Link>
        ) : null}
      </div>

      <PageHeader
        eyebrow="Receipt"
        title={displayTitle}
        description={
          receipt.store
            ? `${receipt.store.name} · Created ${receipt.createdAt.slice(0, 10)}`
            : `Created ${receipt.createdAt.slice(0, 10)}`
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setDialogError(null);
                setConfirmAction('reprocess');
              }}
            >
              Reprocess
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={() => {
                setDialogError(null);
                setConfirmAction('delete');
              }}
            >
              Delete receipt
            </Button>
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-3 text-small text-muted">
        <Badge variant={statusMeta.variant}>{statusMeta.label}</Badge>
        <span>Source: {receipt.source}</span>
        <span>
          Date: <DateText value={receipt.purchasedOn} />
        </span>
        <span>
          Open flags: <strong className="text-ink">{openFlags.length}</strong>
        </span>
      </div>

      {receipt.failureReason ? (
        <div
          role="alert"
          className="rounded-control border border-danger bg-danger-soft px-4 py-3 text-small text-danger"
        >
          <strong>Extraction failed:</strong> {receipt.failureReason}
        </div>
      ) : null}

      {reprocessNotice ? (
        <div
          role="status"
          className="rounded-control border border-accent bg-accent-soft px-4 py-3 text-small text-ink"
        >
          {reprocessNotice}
        </div>
      ) : null}

      {receipt.status === 'queued' ||
      receipt.status === 'processing' ||
      receipt.status === 'waiting_quota' ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-border bg-surface px-4 py-3 text-small text-ink">
          <span>
            {receipt.status === 'waiting_quota'
              ? 'This receipt is waiting for OCR quota before extraction runs.'
              : 'This receipt is currently queued or processing for OCR extraction.'}
          </span>
          <Button
            variant="secondary"
            size="sm"
            loading={isRefreshing}
            onClick={onRefresh}
          >
            Refresh status
          </Button>
        </div>
      ) : null}

      {duplicateFlags.map((flag) => (
        <div
          key={flag.id}
          className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-warning bg-warning-soft px-4 py-3 text-small text-ink"
        >
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="warning">Possible duplicate</Badge>
            <span>{formatReviewFlagSummary(flag).reason}</span>
          </div>
          <Link
            to={`/receipts/${flag.detail.otherReceiptId}`}
            className="font-medium text-accent underline underline-offset-2"
          >
            View other receipt
          </Link>
        </div>
      ))}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2 lg:items-start">
        <Card
          title="Receipt image"
          description={
            receipt.images.length > 0
              ? `${receipt.images.length} uploaded image${receipt.images.length === 1 ? '' : 's'}`
              : undefined
          }
        >
          {receipt.images.length === 0 ? (
            <EmptyState
              title="No image attached"
              description="No receipt image file is stored for this entry."
            />
          ) : (
            <div className="flex flex-col gap-4">
              {receipt.images.map((image, index) => (
                <div key={image.id} className="flex flex-col gap-2">
                  <img
                    src={receiptImageUrl(receipt.id, image.id)}
                    alt={`${displayTitle} image ${index + 1}`}
                    className="max-h-96 min-h-48 w-full rounded-control border border-border bg-surface-subtle object-contain"
                  />
                  <div className="flex flex-wrap items-center justify-between gap-2 text-small text-muted">
                    <span>
                      Page {image.position + 1} · {image.contentType}
                    </span>
                    {image.original !== null ? (
                      <a
                        href={receiptImageUrl(receipt.id, image.id, 'original')}
                        download
                        className="font-medium text-accent underline underline-offset-2 hover:text-accent-hover"
                      >
                        Download original
                      </a>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>

        <Card
          title="Receipt details"
          description="Edit header fields or totals and save changes."
        >
          <form
            onSubmit={(event) => void handleSaveHeader(event)}
            className="flex flex-col gap-4"
          >
            <div className="grid grid-cols-1 gap-3 rounded-control border border-border bg-surface-subtle p-3 sm:grid-cols-3">
              <div>
                <div className="text-caption font-medium text-muted">
                  Subtotal
                </div>
                <div className="text-body font-medium text-ink">
                  <Money value={receipt.subtotal} currency={receipt.currency} />
                </div>
              </div>
              <div>
                <div className="text-caption font-medium text-muted">Tax</div>
                <div className="text-body font-medium text-ink">
                  <Money value={receipt.taxTotal} currency={receipt.currency} />
                </div>
              </div>
              <div>
                <div className="text-caption font-medium text-muted">Total</div>
                <div className="text-body font-semibold text-ink">
                  <Money value={receipt.total} currency={receipt.currency} />
                </div>
              </div>
            </div>

            <Input
              label="Title"
              required
              placeholder="Home shopping"
              value={formValues.title}
              error={pickFieldError(fieldErrors, 'title')}
              onChange={(event) =>
                setFormValues({ ...formValues, title: event.target.value })
              }
            />

            <div className="flex flex-col gap-1.5">
              <Input
                label="Store"
                placeholder="Store name"
                value={formValues.storeName}
                error={pickFieldError(
                  fieldErrors,
                  'storeName',
                  'store_name',
                  'store',
                )}
                onChange={(event) =>
                  setFormValues({
                    ...formValues,
                    storeName: event.target.value,
                  })
                }
              />
              <FieldFlagNotice flags={storeFlags} />
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-1.5">
                <Input
                  label="Purchase date"
                  placeholder="YYYY-MM-DD"
                  value={formValues.purchasedOn}
                  error={pickFieldError(
                    fieldErrors,
                    'purchasedOn',
                    'purchased_on',
                  )}
                  onChange={(event) =>
                    setFormValues({
                      ...formValues,
                      purchasedOn: event.target.value,
                    })
                  }
                />
                <FieldFlagNotice flags={dateFlags} />
              </div>

              <Input
                label="Purchase time"
                placeholder="HH:MM"
                value={formValues.purchasedTime}
                error={pickFieldError(
                  fieldErrors,
                  'purchasedTime',
                  'purchased_time',
                )}
                onChange={(event) =>
                  setFormValues({
                    ...formValues,
                    purchasedTime: event.target.value,
                  })
                }
              />
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Input
                label="Currency"
                required
                placeholder="TRY"
                value={formValues.currency}
                error={pickFieldError(fieldErrors, 'currency')}
                onChange={(event) =>
                  setFormValues({ ...formValues, currency: event.target.value })
                }
              />

              <Input
                label="Subtotal"
                placeholder="0.00"
                value={formValues.subtotal}
                error={pickFieldError(fieldErrors, 'subtotal')}
                onChange={(event) =>
                  setFormValues({ ...formValues, subtotal: event.target.value })
                }
              />

              <Input
                label="Tax total"
                placeholder="0.00"
                value={formValues.taxTotal}
                error={pickFieldError(fieldErrors, 'taxTotal', 'tax_total')}
                onChange={(event) =>
                  setFormValues({ ...formValues, taxTotal: event.target.value })
                }
              />

              <div className="flex flex-col gap-1.5">
                <Input
                  label="Total"
                  placeholder="0.00"
                  value={formValues.total}
                  error={pickFieldError(fieldErrors, 'total')}
                  onChange={(event) =>
                    setFormValues({ ...formValues, total: event.target.value })
                  }
                />
              </div>
            </div>
            <FieldFlagNotice flags={totalFlags} />

            {saveError ? (
              <p role="alert" className="text-small text-danger">
                {saveError.message}
              </p>
            ) : null}

            {savedNotice ? (
              <p role="status" className="text-small text-success">
                Receipt details saved.
              </p>
            ) : null}

            <div className="flex justify-end">
              <Button type="submit" loading={updateReceipt.isPending}>
                Save details
              </Button>
            </div>
          </form>
        </Card>
      </div>

      <ReceiptLineItemsCard receipt={receipt} />

      <Dialog
        open={confirmAction === 'reprocess'}
        onClose={() => setConfirmAction(null)}
        title="Reprocess receipt?"
        description="This sends the receipt images through OCR and product matching again."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmAction(null)}>
              Cancel
            </Button>
            <Button
              loading={reprocessReceipt.isPending}
              onClick={() => void handleConfirmReprocess()}
            >
              Confirm reprocess
            </Button>
          </>
        }
      >
        <p className="text-small text-muted">
          Manual header or line item edits may be overwritten once the new
          extraction finishes.
        </p>
        {dialogError ? (
          <p role="alert" className="mt-3 text-small text-danger">
            {dialogError}
          </p>
        ) : null}
      </Dialog>

      <Dialog
        open={confirmAction === 'delete'}
        onClose={() => setConfirmAction(null)}
        title="Delete receipt?"
        description="This permanently removes the receipt, its line items, and its review flags."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmAction(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={deleteReceipt.isPending}
              onClick={() => void handleConfirmDelete()}
            >
              Confirm delete
            </Button>
          </>
        }
      >
        <p className="text-small text-muted">
          Receipt <strong>{displayTitle}</strong> cannot be recovered after
          deletion.
        </p>
        {dialogError ? (
          <p role="alert" className="mt-3 text-small text-danger">
            {dialogError}
          </p>
        ) : null}
      </Dialog>
    </div>
  );
}

/** Displays a receipt's images, editable metadata, line items, and review flag context. */
export function ReceiptDetailPage() {
  const { receiptId } = useParams();
  const receiptQuery = useReceipt(receiptId);

  if (!receiptId) {
    return (
      <ErrorState
        title="Receipt not found"
        message="Missing receipt identifier in the address bar."
      />
    );
  }

  if (receiptQuery.isPending) {
    return (
      <div className="flex justify-center py-12">
        <Spinner size="lg" showLabel label="Loading receipt..." />
      </div>
    );
  }

  if (receiptQuery.isError) {
    return (
      <ErrorState
        title="Could not load receipt"
        message={getErrorMessage(receiptQuery.error)}
        onRetry={() => {
          void receiptQuery.refetch();
        }}
      />
    );
  }

  if (!receiptQuery.data) {
    return (
      <EmptyState
        title="Receipt not found"
        description="This receipt no longer exists or is unavailable."
        action={
          <Link
            to="/receipts"
            className="text-small font-medium text-accent underline underline-offset-2"
          >
            Back to receipts
          </Link>
        }
      />
    );
  }

  return (
    <ReceiptDetailContent
      key={receiptQuery.data.id}
      receipt={receiptQuery.data}
      isRefreshing={receiptQuery.isFetching}
      onRefresh={() => {
        void receiptQuery.refetch();
      }}
    />
  );
}
