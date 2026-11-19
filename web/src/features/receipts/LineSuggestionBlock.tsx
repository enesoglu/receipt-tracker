import { useState } from 'react';
import { getErrorMessage } from '../../api/errors';
import {
  useAcceptReceiptSuggestion,
  useIgnoreReceiptSuggestion,
  type LineSuggestion,
} from '../../api/suggestions';
import { Button } from '../../components/ui/Button';
import { DateText } from '../../components/ui/DateText';

interface LineSuggestionBlockProps {
  suggestion: LineSuggestion;
}

/** Shows one pending model suggestion for a line with accept and ignore controls. */
export function LineSuggestionBlock({ suggestion }: LineSuggestionBlockProps) {
  const acceptSuggestion = useAcceptReceiptSuggestion();
  const ignoreSuggestion = useIgnoreReceiptSuggestion();
  const [error, setError] = useState<string | null>(null);

  const isDeciding = acceptSuggestion.isPending || ignoreSuggestion.isPending;

  async function handleAccept() {
    setError(null);
    try {
      await acceptSuggestion.mutateAsync({
        receiptId: suggestion.receiptId,
        suggestionId: suggestion.id,
      });
    } catch (cause) {
      setError(getErrorMessage(cause));
    }
  }

  async function handleIgnore() {
    setError(null);
    try {
      await ignoreSuggestion.mutateAsync({
        receiptId: suggestion.receiptId,
        suggestionId: suggestion.id,
      });
    } catch (cause) {
      setError(getErrorMessage(cause));
    }
  }

  return (
    <div className="rounded-control border border-accent/40 bg-accent-soft p-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="text-caption font-medium text-muted">
            Model suggestion for &quot;{suggestion.rawName}&quot;
          </span>
          <span className="text-body font-medium text-ink">
            {suggestion.suggestedName}
          </span>
          <span className="text-small text-muted">
            {suggestion.category
              ? `Suggested category: ${suggestion.category.name}`
              : 'No category suggested'}
          </span>
          <span className="text-caption text-muted">
            From {suggestion.model} on{' '}
            <DateText value={suggestion.createdAt} format="datetime" />
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            loading={isDeciding}
            aria-label={`Accept model suggestion for ${suggestion.rawName}`}
            onClick={() => {
              void handleAccept();
            }}
          >
            Accept
          </Button>
          <Button
            size="sm"
            variant="secondary"
            loading={isDeciding}
            aria-label={`Ignore model suggestion for ${suggestion.rawName}`}
            onClick={() => {
              void handleIgnore();
            }}
          >
            Ignore
          </Button>
        </div>
      </div>
      {error ? (
        <p role="alert" className="mt-2 text-small text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
