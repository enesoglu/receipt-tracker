import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { getErrorMessage } from '../../api/errors';
import { receiptImageUrl, useReceipt } from '../../api/receipts';
import {
  useResolveReviewFlag,
  type ReviewFlagEntry,
  type ReviewResolution,
} from '../../api/review';
import type { components } from '../../api/schema.gen';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import {
  formatFlaggedFieldLabel,
  formatReceiptStatus,
  formatReviewFlagSummary,
} from '../receipts/flag-reasons';
import { LineEditForm } from './LineEditForm';
import { ProductCandidateChooser } from './ProductCandidateChooser';
import { type LineEditValues } from './review-form-values';

type LineItemInput = components['schemas']['LineItemInput'];

function toNullable(value: string): string | null {
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return (
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    tag === 'SELECT' ||
    target.isContentEditable
  );
}

function initialLineValues(entry: ReviewFlagEntry): LineEditValues {
  const item = entry.lineItem;
  if (entry.flag.kind === 'line_math') {
    return {
      name: item?.name ?? '',
      quantity: item?.quantity ?? entry.flag.detail.quantity,
      quantityUnit: item?.quantityUnit ?? '',
      unitPrice: item?.unitPrice ?? entry.flag.detail.unitPrice,
      lineTotal: item?.lineTotal ?? entry.flag.detail.lineTotal,
      productCode: item?.productCode ?? '',
    };
  }
  return {
    name: item?.name ?? '',
    quantity: item?.quantity ?? '',
    quantityUnit: item?.quantityUnit ?? '',
    unitPrice: item?.unitPrice ?? '',
    lineTotal: item?.lineTotal ?? '',
    productCode: item?.productCode ?? '',
  };
}

function toLineItemInput(values: LineEditValues): LineItemInput {
  return {
    name: values.name.trim(),
    quantity: toNullable(values.quantity),
    quantityUnit: toNullable(values.quantityUnit),
    unitPrice: toNullable(values.unitPrice),
    lineTotal: values.lineTotal.trim(),
    productCode: toNullable(values.productCode),
  };
}

interface FlagResolutionCardProps {
  entry: ReviewFlagEntry;
  index: number;
  totalInPage: number;
  totalOpen: number;
  hasNext: boolean;
  onNext: () => void;
}

export function FlagResolutionCard({
  entry,
  index,
  totalInPage,
  totalOpen,
  hasNext,
  onNext,
}: FlagResolutionCardProps) {
  const { flag, receipt, lineItem } = entry;
  const resolveMutation = useResolveReviewFlag();
  const receiptDetailQuery = useReceipt(receipt.id);

  const [errorText, setErrorText] = useState<string | null>(null);
  const [headerFieldValue, setHeaderFieldValue] = useState<string>(() => {
    if (flag.kind === 'low_confidence') {
      if (flag.detail.field === 'store') return receipt.store?.name ?? '';
      if (flag.detail.field === 'purchased_on')
        return receipt.purchasedOn ?? '';
      if (flag.detail.field === 'total') return receipt.total ?? '';
    }
    return '';
  });
  const [lineValues, setLineValues] = useState<LineEditValues>(() =>
    initialLineValues(entry),
  );
  const shortcutConfirmRef = useRef<(() => void) | null>(null);

  const summary = formatReviewFlagSummary(flag);
  const statusMeta = formatReceiptStatus(receipt.status);
  const images = receiptDetailQuery.data?.images ?? [];

  async function submitResolution(resolution: ReviewResolution) {
    setErrorText(null);
    try {
      await resolveMutation.mutateAsync({
        flagId: flag.id,
        body: resolution,
      });
    } catch (error) {
      setErrorText(getErrorMessage(error));
    }
  }

  function getPrimaryConfirmAction(): (() => void) | null {
    if (flag.kind === 'low_confidence') {
      return () => void submitResolution({ action: 'confirm' });
    }
    if (
      flag.kind === 'line_math' ||
      flag.kind === 'sum_mismatch' ||
      flag.kind === 'possible_duplicate'
    ) {
      return () => void submitResolution({ action: 'accept' });
    }
    if (flag.kind === 'uncertain_match' || flag.kind === 'code_conflict') {
      return shortcutConfirmRef.current;
    }
    return null;
  }

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (event.ctrlKey || event.metaKey || event.altKey) return;
      if (isEditableTarget(event.target)) return;
      if (resolveMutation.isPending) return;

      const key = event.key.toLowerCase();
      if (key === 'c') {
        const confirmAction = getPrimaryConfirmAction();
        if (confirmAction) {
          event.preventDefault();
          confirmAction();
        }
      } else if (key === 'n' && hasNext) {
        event.preventDefault();
        onNext();
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  });

  function renderFlagControls() {
    switch (flag.kind) {
      case 'low_confidence': {
        const field = flag.detail.field;
        const fieldLabel = formatFlaggedFieldLabel(field);
        return (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-border bg-surface-subtle p-3">
              <div>
                <div className="text-caption font-medium text-muted">
                  Extracted {fieldLabel.toLowerCase()}
                </div>
                <div className="text-body font-medium text-ink">
                  {field === 'store'
                    ? (receipt.store?.name ?? 'Unknown store')
                    : field === 'purchased_on'
                      ? (receipt.purchasedOn ?? 'Missing')
                      : field === 'total'
                        ? (receipt.total ?? 'Missing')
                        : (lineItem?.name ?? 'Line item')}
                </div>
              </div>
              <Button
                loading={resolveMutation.isPending}
                onClick={() => void submitResolution({ action: 'confirm' })}
              >
                Confirm as extracted
              </Button>
            </div>

            {field === 'line_item' ? (
              <LineEditForm
                values={lineValues}
                isSubmitting={resolveMutation.isPending}
                submitLabel="Save corrected line"
                onChange={setLineValues}
                onSubmit={() =>
                  void submitResolution({
                    action: 'correct',
                    lineItem: toLineItemInput(lineValues),
                  })
                }
              />
            ) : (
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  const trimmed = headerFieldValue.trim();
                  if (!trimmed) return;
                  if (field === 'store') {
                    void submitResolution({
                      action: 'correct',
                      storeName: trimmed,
                    });
                  } else if (field === 'purchased_on') {
                    void submitResolution({
                      action: 'correct',
                      purchasedOn: trimmed,
                    });
                  } else {
                    void submitResolution({
                      action: 'correct',
                      total: trimmed,
                    });
                  }
                }}
                className="flex flex-col gap-3 rounded-control border border-border bg-surface-subtle p-4"
              >
                <Input
                  label={`Correct ${fieldLabel.toLowerCase()}`}
                  required
                  placeholder={field === 'purchased_on' ? 'YYYY-MM-DD' : ''}
                  value={headerFieldValue}
                  onChange={(event) => setHeaderFieldValue(event.target.value)}
                />
                <div className="flex justify-end">
                  <Button
                    type="submit"
                    variant="secondary"
                    loading={resolveMutation.isPending}
                  >
                    Save correction
                  </Button>
                </div>
              </form>
            )}

            <p className="text-caption text-muted">
              Shortcut: press <kbd className="font-mono font-semibold">C</kbd>{' '}
              to confirm as extracted.
            </p>
          </div>
        );
      }

      case 'missing_field': {
        const field = flag.detail.field;
        const fieldLabel = formatFlaggedFieldLabel(field);
        return (
          <div className="flex flex-col gap-4">
            <form
              onSubmit={(event) => {
                event.preventDefault();
                const trimmed = headerFieldValue.trim();
                if (!trimmed) return;
                void submitResolution(
                  field === 'purchased_on'
                    ? { action: 'correct', purchasedOn: trimmed }
                    : { action: 'correct', total: trimmed },
                );
              }}
              className="flex flex-col gap-3 rounded-control border border-border bg-surface-subtle p-4"
            >
              <Input
                label={`Enter ${fieldLabel.toLowerCase()}`}
                required
                placeholder={field === 'purchased_on' ? 'YYYY-MM-DD' : '0.00'}
                value={headerFieldValue}
                onChange={(event) => setHeaderFieldValue(event.target.value)}
              />
              <div className="flex flex-wrap justify-end gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  loading={resolveMutation.isPending}
                  onClick={() => void submitResolution({ action: 'accept' })}
                >
                  Leave empty
                </Button>
                <Button type="submit" loading={resolveMutation.isPending}>
                  Save field
                </Button>
              </div>
            </form>
          </div>
        );
      }

      case 'line_math': {
        return (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-3 rounded-control border border-border bg-surface-subtle p-3 sm:grid-cols-4">
              <div>
                <div className="text-caption text-muted">Quantity</div>
                <div className="text-body font-medium text-ink">
                  {flag.detail.quantity}
                </div>
              </div>
              <div>
                <div className="text-caption text-muted">Unit price</div>
                <div className="text-body font-medium text-ink">
                  <Money
                    value={flag.detail.unitPrice}
                    currency={receipt.currency}
                  />
                </div>
              </div>
              <div>
                <div className="text-caption text-muted">Calculated</div>
                <div className="text-body font-medium text-ink">
                  <Money
                    value={flag.detail.expectedTotal}
                    currency={receipt.currency}
                  />
                </div>
              </div>
              <div>
                <div className="text-caption text-muted">Printed total</div>
                <div className="text-body font-semibold text-ink">
                  <Money
                    value={flag.detail.lineTotal}
                    currency={receipt.currency}
                  />
                </div>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-caption text-muted">
                Shortcut: press <kbd className="font-mono font-semibold">C</kbd>{' '}
                to accept printed total
              </span>
              <Button
                loading={resolveMutation.isPending}
                onClick={() => void submitResolution({ action: 'accept' })}
              >
                Accept printed total
              </Button>
            </div>

            <LineEditForm
              values={lineValues}
              isSubmitting={resolveMutation.isPending}
              submitLabel="Save corrected line"
              onChange={setLineValues}
              onSubmit={() =>
                void submitResolution({
                  action: 'correct',
                  lineItem: toLineItemInput(lineValues),
                })
              }
            />
          </div>
        );
      }

      case 'sum_mismatch': {
        return (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 gap-3 rounded-control border border-border bg-surface-subtle p-3 sm:grid-cols-3">
              <div>
                <div className="text-caption text-muted">Line sum</div>
                <div className="text-body font-medium text-ink">
                  <Money
                    value={flag.detail.lineSum}
                    currency={receipt.currency}
                  />
                </div>
              </div>
              <div>
                <div className="text-caption text-muted">Receipt total</div>
                <div className="text-body font-medium text-ink">
                  <Money
                    value={flag.detail.total}
                    currency={receipt.currency}
                  />
                </div>
              </div>
              <div>
                <div className="text-caption text-muted">Difference</div>
                <div className="text-body font-semibold text-warning">
                  <Money
                    value={flag.detail.difference}
                    currency={receipt.currency}
                  />
                </div>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-between gap-3">
              <Link
                to={`/receipts/${receipt.id}`}
                className="text-small font-medium text-accent underline underline-offset-2"
              >
                Open receipt to edit lines or total
              </Link>
              <Button
                loading={resolveMutation.isPending}
                onClick={() => void submitResolution({ action: 'accept' })}
              >
                Accept difference
              </Button>
            </div>

            <p className="text-caption text-muted">
              Shortcut: press <kbd className="font-mono font-semibold">C</kbd>{' '}
              to accept difference.
            </p>
          </div>
        );
      }

      case 'uncertain_match':
      case 'code_conflict': {
        return (
          <ProductCandidateChooser
            flag={flag}
            entry={entry}
            isSubmitting={resolveMutation.isPending}
            onSubmit={submitResolution}
            onRegisterShortcutAction={(fn) => {
              shortcutConfirmRef.current = fn;
            }}
          />
        );
      }

      case 'possible_duplicate': {
        return (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap gap-4 rounded-control border border-warning bg-warning-soft p-4 text-small text-ink">
              <Link
                to={`/receipts/${flag.detail.otherReceiptId}`}
                className="font-medium text-accent underline underline-offset-2"
              >
                Inspect matching receipt
              </Link>
              <Link
                to={`/receipts/${receipt.id}`}
                className="font-medium text-accent underline underline-offset-2"
              >
                Open this receipt
              </Link>
            </div>

            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-caption text-muted">
                Shortcut: press <kbd className="font-mono font-semibold">C</kbd>{' '}
                to keep both
              </span>
              <Button
                loading={resolveMutation.isPending}
                onClick={() => void submitResolution({ action: 'accept' })}
              >
                Keep both receipts
              </Button>
            </div>
          </div>
        );
      }
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-border bg-surface px-4 py-3">
        <div className="flex flex-wrap items-center gap-3">
          <Badge variant="warning">{summary.label}</Badge>
          <span className="text-small font-medium text-ink">
            Flag {index + 1} of {totalInPage}
            {totalOpen > totalInPage ? ` (${totalOpen} open total)` : ''}
          </span>
        </div>

        <Button
          variant="secondary"
          size="sm"
          disabled={!hasNext}
          onClick={onNext}
        >
          Next flag (N)
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2 lg:items-start">
        <Card
          title="Receipt context"
          description={
            receipt.store?.name
              ? `${receipt.store.name} · Uploaded receipt details`
              : 'Uploaded receipt details'
          }
          actions={
            <Link
              to={`/receipts/${receipt.id}`}
              className="text-small font-medium text-accent underline underline-offset-2"
            >
              Open receipt
            </Link>
          }
        >
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-2 text-small">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-ink">{receipt.title}</span>
                <Badge variant={statusMeta.variant}>{statusMeta.label}</Badge>
              </div>
              <div className="flex items-center gap-3 text-muted">
                <DateText value={receipt.purchasedOn} />
                <span className="font-medium text-ink">
                  <Money value={receipt.total} currency={receipt.currency} />
                </span>
              </div>
            </div>

            {lineItem ? (
              <div className="rounded-control border border-border bg-surface-subtle p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-body font-medium text-ink">
                    {lineItem.name}
                  </span>
                  <span className="text-body font-semibold text-ink">
                    <Money
                      value={lineItem.lineTotal}
                      currency={receipt.currency}
                    />
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-small text-muted">
                  <span>
                    Qty: {lineItem.quantity ?? '—'}
                    {lineItem.quantityUnit ? ` ${lineItem.quantityUnit}` : ''}
                  </span>
                  <span>
                    Unit price:{' '}
                    {lineItem.unitPrice ? (
                      <Money
                        value={lineItem.unitPrice}
                        currency={receipt.currency}
                      />
                    ) : (
                      '—'
                    )}
                  </span>
                  {lineItem.productCode ? (
                    <span>Code: {lineItem.productCode}</span>
                  ) : null}
                  {lineItem.rawName && lineItem.rawName !== lineItem.name ? (
                    <span>OCR: {lineItem.rawName}</span>
                  ) : null}
                </div>
              </div>
            ) : null}

            {images.length > 0 ? (
              <img
                src={receiptImageUrl(receipt.id, images[0].id)}
                alt={`${receipt.title || 'Receipt'} image preview`}
                className="max-h-80 min-h-40 w-full rounded-control border border-border bg-surface-subtle object-contain"
              />
            ) : null}
          </div>
        </Card>

        <Card title="Resolution" description={summary.reason}>
          <div className="flex flex-col gap-4">
            {errorText ? (
              <p
                role="alert"
                className="rounded-control border border-danger bg-danger-soft px-3 py-2 text-small text-danger"
              >
                {errorText}
              </p>
            ) : null}

            {renderFlagControls()}
          </div>
        </Card>
      </div>
    </div>
  );
}
