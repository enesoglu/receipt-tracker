import type { components } from '../../api/schema.gen';
import type { BadgeVariant } from '../../components/ui/Badge';

export type ReceiptStatus = components['schemas']['ReceiptStatus'];
export type ReviewFlag = components['schemas']['ReviewFlag'];
export type ReviewFlagKind = components['schemas']['ReviewFlagKind'];
export type FlaggedField = components['schemas']['FlaggedField'];

const STATUS_PRESENTATION: Record<
  ReceiptStatus,
  { label: string; variant: BadgeVariant }
> = {
  queued: { label: 'Queued', variant: 'neutral' },
  processing: { label: 'Processing', variant: 'accent' },
  waiting_quota: { label: 'Waiting for quota', variant: 'warning' },
  needs_review: { label: 'Needs review', variant: 'warning' },
  ready: { label: 'Ready', variant: 'success' },
  failed: { label: 'Failed', variant: 'danger' },
};

const UNCERTAIN_MATCH_REASONS: Record<string, string> = {
  size_conflict:
    'The name matches, but the unit or package size differs from the existing product.',
  code_name_mismatch:
    'The printed barcode or product code matches an existing product, but the item name differs.',
  similar_name:
    'No exact match was found; this name looks similar to an existing product.',
  ambiguous_code:
    'This product code is shared by more than one existing product.',
};

const FLAGGED_FIELD_LABELS: Record<FlaggedField, string> = {
  store: 'Store',
  purchased_on: 'Purchase date',
  total: 'Total',
  line_item: 'Line item',
};

/** Returns badge label and visual tone for any receipt status. */
export function formatReceiptStatus(status: ReceiptStatus): {
  label: string;
  variant: BadgeVariant;
} {
  return STATUS_PRESENTATION[status];
}

/** Formats uncertain-match reason codes by string with a safe fallback for future codes. */
export function formatUncertainMatchReason(reason: string): string {
  return (
    UNCERTAIN_MATCH_REASONS[reason] ??
    'Automatic product matching could not decide cleanly; please confirm.'
  );
}

/** Returns human-readable name for a flagged receipt field. */
export function formatFlaggedFieldLabel(field: FlaggedField): string {
  return FLAGGED_FIELD_LABELS[field];
}

/** Returns short badge label and plain-English explanation for any review flag. */
export function formatReviewFlagSummary(flag: ReviewFlag): {
  label: string;
  reason: string;
} {
  switch (flag.kind) {
    case 'low_confidence': {
      const percent = Math.round(flag.detail.confidence * 100);
      const fieldLabel = formatFlaggedFieldLabel(flag.detail.field);
      return {
        label: 'Low confidence',
        reason: `Extractor confidence was ${percent}% for ${fieldLabel.toLowerCase()}.`,
      };
    }
    case 'missing_field': {
      const fieldLabel = formatFlaggedFieldLabel(flag.detail.field);
      return {
        label: 'Missing field',
        reason: `${fieldLabel} could not be extracted from the receipt.`,
      };
    }
    case 'line_math':
      return {
        label: 'Line math mismatch',
        reason: `Quantity (${flag.detail.quantity}) × unit price (${flag.detail.unitPrice}) = ${flag.detail.expectedTotal}, which differs from ${flag.detail.lineTotal}.`,
      };
    case 'sum_mismatch':
      return {
        label: 'Total mismatch',
        reason: `Line items sum to ${flag.detail.lineSum} vs receipt total ${flag.detail.total} (difference ${flag.detail.difference}).`,
      };
    case 'uncertain_match':
      return {
        label: 'Uncertain match',
        reason: formatUncertainMatchReason(flag.detail.reason),
      };
    case 'code_conflict':
      return {
        label: 'Code conflict',
        reason: `Code ${flag.detail.code} belongs to "${flag.detail.codeProduct.canonicalName}", while the name matches "${flag.detail.nameProduct.canonicalName}".`,
      };
    case 'possible_duplicate':
      return {
        label: 'Possible duplicate',
        reason:
          'Another receipt already has the same store, purchase date, and total.',
      };
  }
}

/** Returns open receipt-level flags associated with a header field. */
export function getOpenFlagsForField(
  flags: readonly ReviewFlag[],
  field: Exclude<FlaggedField, 'line_item'>,
): ReviewFlag[] {
  return flags.filter((flag) => {
    if (flag.resolvedAt !== null) return false;
    if (flag.kind === 'low_confidence' || flag.kind === 'missing_field') {
      return flag.detail.field === field;
    }
    if (flag.kind === 'sum_mismatch' && field === 'total') {
      return true;
    }
    return false;
  });
}

/** Returns open flags attached directly to a specific line item. */
export function getOpenFlagsForLineItem(
  flags: readonly ReviewFlag[],
  lineItemId: string,
): ReviewFlag[] {
  return flags.filter(
    (flag) => flag.resolvedAt === null && flag.lineItemId === lineItemId,
  );
}
