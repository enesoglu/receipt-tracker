import { Link, useParams } from 'react-router';
import { getErrorMessage } from '../../api/errors';
import { useReceipt } from '../../api/receipts';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Spinner } from '../../components/ui/Spinner';
import { ReceiptDetailContent } from './ReceiptDetailContent';

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
      // Remount when extraction or an edit changes the receipt, so the form
      // cannot save values from before that change over the newer ones.
      key={`${receiptQuery.data.id}:${receiptQuery.data.updatedAt}`}
      receipt={receiptQuery.data}
      isRefreshing={receiptQuery.isFetching}
      onRefresh={() => {
        void receiptQuery.refetch();
      }}
    />
  );
}
