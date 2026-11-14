import { useQueryClient } from '@tanstack/react-query';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type DragEvent,
} from 'react';
import { Link } from 'react-router';
import { getErrorMessage } from '../../api/errors';
import { queryKeys } from '../../api/query-keys';
import {
  useReceipts,
  type ReceiptPage,
  type ReceiptSummary,
} from '../../api/receipts';
import { useUsage } from '../../api/settings';
import { useUploadReceipts } from '../../api/uploads';
import { Badge, type BadgeVariant } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';
import {
  formatByteSize,
  MAX_BATCH_FILES,
  UPLOAD_FILE_ACCEPT,
  validateUploadFile,
} from './upload-validation';

const POLL_INTERVAL_MS = 2000;

type DisplayState =
  | 'uploading'
  | 'queued'
  | 'processing'
  | 'needs_review'
  | 'ready'
  | 'failed'
  | 'waiting_quota'
  | 'duplicate';

interface UploadBatchItem {
  localId: string;
  fileName: string;
  sizeBytes: number;
  phase: 'uploading' | 'created' | 'duplicate' | 'rejected';
  receiptId: string | null;
  errorMessage: string | null;
}

interface FailedBatch {
  files: File[];
  localIds: string[];
}

function getStatePresentation(state: DisplayState): {
  label: string;
  variant: BadgeVariant;
} {
  switch (state) {
    case 'uploading':
      return { label: 'Uploading', variant: 'accent' };
    case 'queued':
      return { label: 'Queued', variant: 'neutral' };
    case 'processing':
      return { label: 'Processing', variant: 'accent' };
    case 'needs_review':
      return { label: 'Needs review', variant: 'warning' };
    case 'ready':
      return { label: 'Ready', variant: 'success' };
    case 'failed':
      return { label: 'Failed', variant: 'danger' };
    case 'waiting_quota':
      return { label: 'Waiting for quota', variant: 'warning' };
    case 'duplicate':
      return { label: 'Duplicate', variant: 'neutral' };
  }
}

function resolveItemState(
  item: UploadBatchItem,
  summaries: ReadonlyMap<string, ReceiptSummary>,
): {
  state: DisplayState;
  detail: string | null;
} {
  if (item.phase === 'uploading') {
    return { state: 'uploading', detail: 'Sending photo to the server...' };
  }
  if (item.phase === 'duplicate') {
    return {
      state: 'duplicate',
      detail: 'Already uploaded earlier.',
    };
  }
  if (item.phase === 'rejected') {
    return {
      state: 'failed',
      detail: item.errorMessage ?? 'Upload failed.',
    };
  }

  const summary = item.receiptId ? summaries.get(item.receiptId) : undefined;
  const status = summary?.status ?? 'queued';
  switch (status) {
    case 'queued':
      return { state: 'queued', detail: 'Waiting for extraction...' };
    case 'processing':
      return { state: 'processing', detail: 'Extracting receipt fields...' };
    case 'needs_review':
      return {
        state: 'needs_review',
        detail:
          summary && summary.openFlagCount > 0
            ? `${summary.openFlagCount} item${summary.openFlagCount === 1 ? '' : 's'} flagged for review.`
            : 'Needs review before finalizing.',
      };
    case 'ready':
      return { state: 'ready', detail: 'Extraction complete.' };
    case 'waiting_quota':
      return {
        state: 'waiting_quota',
        detail: 'Monthly extraction allowance reached; queued for next month.',
      };
    case 'failed':
      return {
        state: 'failed',
        detail: summary?.failureReason ?? 'Extraction failed.',
      };
  }
}

/** Batch receipt photo upload screen with client validation, usage meter, and status polling. */
export function UploadPage() {
  const queryClient = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const nextLocalSeqRef = useRef(1);
  const prevHasActiveRef = useRef(false);

  const [isDragActive, setIsDragActive] = useState(false);
  const [batchLimitError, setBatchLimitError] = useState<string | null>(null);
  const [items, setItems] = useState<UploadBatchItem[]>([]);
  const [failedBatch, setFailedBatch] = useState<FailedBatch | null>(null);

  const usageQuery = useUsage();
  const uploadMutation = useUploadReceipts();

  const createdIds = useMemo(() => {
    const seen = new Set<string>();
    const ids: string[] = [];
    for (const item of items) {
      if (
        item.phase === 'created' &&
        item.receiptId &&
        !seen.has(item.receiptId)
      ) {
        seen.add(item.receiptId);
        ids.push(item.receiptId);
      }
    }
    return ids.slice(-100);
  }, [items]);

  const hasPendingReceipt = useCallback(
    (page: ReceiptPage | undefined) => {
      if (createdIds.length === 0) return false;
      const byId = new Map(
        (page?.items ?? []).map((entry) => [entry.id, entry]),
      );
      return createdIds.some((id) => {
        const status = byId.get(id)?.status ?? 'queued';
        return status === 'queued' || status === 'processing';
      });
    },
    [createdIds],
  );

  const pollingQuery = useReceipts(
    { ids: createdIds },
    {
      enabled: createdIds.length > 0,
      refetchInterval: (data, isError) => {
        if (isError) return false;
        return hasPendingReceipt(data) ? POLL_INTERVAL_MS : false;
      },
    },
  );

  const summaryMap = useMemo(() => {
    const map = new Map<string, ReceiptSummary>();
    for (const summary of pollingQuery.data?.items ?? []) {
      map.set(summary.id, summary);
    }
    return map;
  }, [pollingQuery.data]);

  const hasActiveReceipts = useMemo(
    () => !pollingQuery.isError && hasPendingReceipt(pollingQuery.data),
    [hasPendingReceipt, pollingQuery.data, pollingQuery.isError],
  );

  useEffect(() => {
    if (prevHasActiveRef.current && !hasActiveReceipts) {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.settings.usage,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
    }
    prevHasActiveRef.current = hasActiveReceipts;
  }, [hasActiveReceipts, queryClient]);

  function sendBatch(validFiles: File[], validLocalIds: string[]) {
    setFailedBatch(null);
    uploadMutation.mutate(validFiles, {
      onSuccess: (response) => {
        const resultByIndex = new Map(
          response.results.map((entry) => [entry.index, entry]),
        );
        setItems((current) =>
          current.map((item) => {
            const validIdx = validLocalIds.indexOf(item.localId);
            if (validIdx === -1) return item;
            const result = resultByIndex.get(validIdx);
            if (!result) {
              return {
                ...item,
                phase: 'rejected',
                errorMessage: 'Missing server upload result for this file.',
              };
            }
            if (result.outcome === 'created') {
              return {
                ...item,
                phase: 'created',
                receiptId: result.receiptId,
                errorMessage: null,
              };
            }
            if (result.outcome === 'duplicate') {
              return {
                ...item,
                phase: 'duplicate',
                receiptId: result.receiptId,
                errorMessage: null,
              };
            }
            return {
              ...item,
              phase: 'rejected',
              receiptId: null,
              errorMessage:
                result.error?.message ?? 'File was rejected by the server.',
            };
          }),
        );
      },
      onError: (error) => {
        const message = getErrorMessage(error);
        setFailedBatch({ files: validFiles, localIds: validLocalIds });
        setItems((current) =>
          current.map((item) =>
            validLocalIds.includes(item.localId)
              ? { ...item, phase: 'rejected', errorMessage: message }
              : item,
          ),
        );
      },
    });
  }

  function handleSelectedFiles(fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return;
    const files = Array.from(fileList);

    if (files.length > MAX_BATCH_FILES) {
      setBatchLimitError(
        `You can upload at most ${MAX_BATCH_FILES} files in one batch (${files.length} selected).`,
      );
      return;
    }

    setBatchLimitError(null);

    const newItems: UploadBatchItem[] = [];
    const validFiles: File[] = [];
    const validLocalIds: string[] = [];

    for (const file of files) {
      const localId = `upload-${nextLocalSeqRef.current++}`;
      const validationError = validateUploadFile(file);
      if (validationError) {
        newItems.push({
          localId,
          fileName: file.name,
          sizeBytes: file.size,
          phase: 'rejected',
          receiptId: null,
          errorMessage: validationError,
        });
      } else {
        newItems.push({
          localId,
          fileName: file.name,
          sizeBytes: file.size,
          phase: 'uploading',
          receiptId: null,
          errorMessage: null,
        });
        validFiles.push(file);
        validLocalIds.push(localId);
      }
    }

    setItems((current) => [...newItems, ...current]);

    if (validFiles.length > 0) {
      sendBatch(validFiles, validLocalIds);
    }
  }

  function handleInputChange(event: ChangeEvent<HTMLInputElement>) {
    handleSelectedFiles(event.target.files);
    event.target.value = '';
  }

  function handleDragOver(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    if (!isDragActive) setIsDragActive(true);
  }

  function handleDragLeave(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setIsDragActive(false);
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setIsDragActive(false);
    handleSelectedFiles(event.dataTransfer.files);
  }

  function handleRetryFailedBatch() {
    if (!failedBatch || uploadMutation.isPending) return;
    const { files, localIds } = failedBatch;
    setItems((current) =>
      current.map((item) =>
        localIds.includes(item.localId)
          ? { ...item, phase: 'uploading', errorMessage: null }
          : item,
      ),
    );
    sendBatch(files, localIds);
  }

  function renderUsageCard() {
    if (usageQuery.isPending) {
      return (
        <Card title="Monthly extraction allowance">
          <Spinner size="sm" showLabel label="Loading extraction usage..." />
        </Card>
      );
    }

    if (usageQuery.isError) {
      return (
        <ErrorState
          title="Could not load extraction usage"
          message={getErrorMessage(usageQuery.error)}
          onRetry={() => {
            void usageQuery.refetch();
          }}
        />
      );
    }

    const usage = usageQuery.data;
    const remaining = Math.max(0, usage.pagesLimit - usage.pagesUsed);
    const isExhausted = usage.pagesUsed >= usage.pagesLimit;

    return (
      <Card
        title="Monthly extraction allowance"
        description="Pages sent to the receipt extractor this calendar month (UTC)."
        actions={
          <Badge variant={isExhausted ? 'warning' : 'neutral'}>
            {usage.provider === 'fake' ? 'Fake extractor' : 'Azure OCR'}
          </Badge>
        }
      >
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-col gap-1">
            <p className="text-body font-semibold text-ink">
              <span className="tabular-nums">{usage.pagesUsed}</span> of{' '}
              <span className="tabular-nums">{usage.pagesLimit}</span> pages
              used
            </p>
            <p className="text-small text-muted">
              Period: <DateText value={usage.period} format="month" />
            </p>
          </div>
          <div>
            {isExhausted ? (
              <Badge variant="warning">
                Quota reached — new uploads wait until next month
              </Badge>
            ) : (
              <Badge variant="success">
                {remaining} page{remaining === 1 ? '' : 's'} remaining
              </Badge>
            )}
          </div>
        </div>
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Upload"
        title="Upload receipts"
        description="Send up to 20 JPEG, PNG, or HEIC/HEIF photos (15 MB max each) in a single batch."
        actions={
          <Link
            to="/receipts"
            className="inline-flex items-center justify-center rounded-control border border-border bg-surface px-4 py-2 text-small font-medium text-ink hover:border-border-strong hover:bg-surface-subtle focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            View all receipts
          </Link>
        }
      />

      {renderUsageCard()}

      <Card
        title="Select receipt photos"
        description="Photos are uploaded together as one batch. Previews are omitted because browsers do not render HEIC files natively."
      >
        <div className="flex flex-col gap-4">
          <div
            role="region"
            aria-label="Receipt photo drop area"
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            className={`flex flex-col items-center justify-center gap-3 rounded-card border-2 border-dashed px-6 py-10 text-center ${
              isDragActive
                ? 'border-accent bg-accent-soft'
                : 'border-border bg-surface-subtle'
            }`}
          >
            <p className="text-body font-medium text-ink">
              Drop receipt photos here, or pick files from your device
            </p>
            <p className="text-small text-muted">
              Accepted formats: JPEG, PNG, HEIC, HEIF · Maximum 15 MB per file ·
              Up to 20 files per batch
            </p>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              accept={UPLOAD_FILE_ACCEPT}
              aria-label="Choose receipt photos"
              className="sr-only"
              onChange={handleInputChange}
            />
            <Button
              variant="primary"
              loading={uploadMutation.isPending}
              onClick={() => fileInputRef.current?.click()}
            >
              {uploadMutation.isPending
                ? 'Uploading batch...'
                : 'Select photos'}
            </Button>
          </div>

          {batchLimitError ? (
            <ErrorState title="Batch size limit" message={batchLimitError} />
          ) : null}

          {uploadMutation.isError && failedBatch ? (
            <ErrorState
              title="Upload failed"
              message={getErrorMessage(uploadMutation.error)}
              onRetry={handleRetryFailedBatch}
              retryLabel="Retry batch"
            />
          ) : null}
        </div>
      </Card>

      {pollingQuery.isError ? (
        <ErrorState
          title="Could not refresh receipt statuses"
          message={getErrorMessage(pollingQuery.error)}
          onRetry={() => {
            void pollingQuery.refetch();
          }}
        />
      ) : null}

      {items.length === 0 ? (
        <EmptyState
          title="No files uploaded yet"
          description="Choose or drop receipt photos above to start extraction."
        />
      ) : (
        <Card
          title="Upload batch status"
          description={
            hasActiveReceipts
              ? 'Checking extraction progress every 2 seconds...'
              : 'All files in this session have reached a settled state.'
          }
          actions={
            <div className="flex items-center gap-2">
              {hasActiveReceipts ? (
                <Spinner size="sm" showLabel label="Polling" />
              ) : null}
              <Button
                variant="secondary"
                size="sm"
                disabled={uploadMutation.isPending}
                onClick={() => {
                  setItems([]);
                  setFailedBatch(null);
                }}
              >
                Clear list
              </Button>
            </div>
          }
        >
          <ul className="divide-y divide-border">
            {items.map((item) => {
              const { state, detail } = resolveItemState(item, summaryMap);
              const badge = getStatePresentation(state);
              const showReceiptLink =
                item.receiptId !== null &&
                (state === 'ready' ||
                  state === 'needs_review' ||
                  state === 'duplicate');

              return (
                <li
                  key={item.localId}
                  className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="flex min-w-0 flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate text-small font-semibold text-ink">
                        {item.fileName}
                      </span>
                      <span className="text-caption text-muted tabular-nums">
                        {formatByteSize(item.sizeBytes)}
                      </span>
                    </div>
                    {detail ? (
                      <p
                        className={`text-caption ${state === 'failed' ? 'text-danger' : 'text-muted'}`}
                      >
                        {detail}
                      </p>
                    ) : null}
                  </div>

                  <div className="flex shrink-0 flex-wrap items-center gap-3">
                    <Badge variant={badge.variant}>{badge.label}</Badge>
                    {showReceiptLink && item.receiptId ? (
                      <Link
                        to={`/receipts/${item.receiptId}`}
                        className="text-small font-medium text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                      >
                        {state === 'duplicate'
                          ? 'View existing receipt'
                          : 'View receipt'}
                      </Link>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ul>
        </Card>
      )}
    </div>
  );
}
