import { useState } from 'react';
import { Link } from 'react-router';
import { getErrorMessage } from '../../api/errors';
import { useReviewFlags } from '../../api/review';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';
import { FlagResolutionCard } from './FlagResolutionCard';

/** Interactive queue to review and resolve open extraction and matching flags. */
export function ReviewQueuePage() {
  const flagsQuery = useReviewFlags();
  const [cursorIndex, setCursorIndex] = useState(0);

  const entries = flagsQuery.data?.items ?? [];
  const totalOpen = flagsQuery.data?.totalOpen ?? entries.length;
  const safeIndex = entries.length > 0 ? cursorIndex % entries.length : 0;
  const currentEntry = entries[safeIndex];

  function handleNext() {
    if (entries.length <= 1) return;
    setCursorIndex((prev) => (prev + 1) % entries.length);
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Review"
        title="Review queue"
        description="Resolve flagged values, product matches, and potential duplicates one flag at a time."
        actions={
          <Link
            to="/receipts"
            className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-3 py-1.5 text-small font-medium text-ink hover:bg-surface-subtle focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            View receipts
          </Link>
        }
      />

      {flagsQuery.isPending ? (
        <div className="flex justify-center py-12">
          <Spinner size="lg" showLabel label="Loading review queue..." />
        </div>
      ) : flagsQuery.isError ? (
        <ErrorState
          title="Could not load review queue"
          message={getErrorMessage(flagsQuery.error)}
          onRetry={() => {
            void flagsQuery.refetch();
          }}
        />
      ) : !currentEntry ? (
        <EmptyState
          title="Review queue is clear"
          description="Every receipt and line item has been checked."
          action={
            <Link
              to="/receipts"
              className="inline-flex items-center justify-center rounded-control bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              Browse receipts
            </Link>
          }
        />
      ) : (
        <FlagResolutionCard
          key={currentEntry.flag.id}
          entry={currentEntry}
          index={safeIndex}
          totalInPage={entries.length}
          totalOpen={totalOpen}
          hasNext={entries.length > 1}
          onNext={handleNext}
        />
      )}
    </div>
  );
}
