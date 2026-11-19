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
  usePipeline,
  type PipelineItem,
  type PipelinePage,
} from '../../api/pipeline';
import { useUsage } from '../../api/settings';
import type { UploadItem } from '../../api/upload-queue';
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
import { useUploadQueue, type UploadProgress } from './use-upload-queue';

const POLL_INTERVAL_MS = 2000;

/** One row of the upload list: either a queued file or a rejected selection. */
interface UploadRow {
  key: string;
  fileName: string;
  sizeBytes: number;
  label: string;
  variant: BadgeVariant;
  detail: string | null;
  /** Upload percent while the file is being sent; null otherwise. */
  percent: number | null;
  receiptId: string | null;
  linkLabel: string | null;
}

/** A file rejected client-side; it never enters the durable queue. */
interface RejectedFile {
  localId: string;
  fileName: string;
  sizeBytes: number;
  errorMessage: string;
}

function percentOf(progress: UploadProgress | undefined): number | null {
  if (!progress || progress.total <= 0) return null;
  return Math.min(100, Math.round((progress.uploaded / progress.total) * 100));
}

/** Merges one queued item with its server-side pipeline stage. */
function describeQueueItem(
  item: UploadItem,
  stages: ReadonlyMap<string, PipelineItem>,
  progress: ReadonlyMap<string, UploadProgress>,
): UploadRow {
  const base = {
    key: item.id,
    fileName: item.fileName,
    sizeBytes: item.sizeBytes,
  };
  if (item.state === 'uploading') {
    const sent = progress.get(item.id);
    const percent = percentOf(sent);
    return {
      ...base,
      label: 'Uploading',
      variant: 'accent',
      detail: sent
        ? `Sending photo to the server (${percent}% of ${formatByteSize(sent.total)})...`
        : 'Sending photo to the server...',
      percent,
      receiptId: null,
      linkLabel: null,
    };
  }
  if (item.state === 'pending') {
    return {
      ...base,
      label: 'Waiting',
      variant: 'neutral',
      detail: item.error ?? 'Waiting for its turn to upload.',
      percent: null,
      receiptId: null,
      linkLabel: null,
    };
  }
  if (item.state === 'needs_signin') {
    return {
      ...base,
      label: 'Sign in required',
      variant: 'warning',
      detail:
        item.error ??
        'Your session expired; sign in again to finish this upload.',
      percent: null,
      receiptId: null,
      linkLabel: null,
    };
  }
  if (item.state === 'failed') {
    return {
      ...base,
      label: 'Failed',
      variant: 'danger',
      detail: item.error ?? 'The photo could not be uploaded.',
      percent: null,
      receiptId: null,
      linkLabel: null,
    };
  }
  // Done: the file reached the server; show where its receipt stands.
  if (item.outcome === 'duplicate' && item.receiptId) {
    return {
      ...base,
      label: 'Duplicate',
      variant: 'neutral',
      detail: 'These photo bytes were already uploaded earlier.',
      percent: null,
      receiptId: item.receiptId,
      linkLabel: 'View existing receipt',
    };
  }
  const stage = item.receiptId ? stages.get(item.receiptId) : undefined;
  switch (stage?.stage ?? 'queued') {
    case 'queued':
      return {
        ...base,
        label: 'Queued',
        variant: 'neutral',
        detail: stage?.queuePosition
          ? `Position ${stage.queuePosition} in the extraction queue.`
          : 'Waiting for extraction.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: null,
      };
    case 'extracting':
      return {
        ...base,
        label: 'Extracting',
        variant: 'accent',
        detail: 'Reading the receipt image.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: null,
      };
    case 'matching':
      return {
        ...base,
        label: 'Matching',
        variant: 'accent',
        detail: 'Applying the extracted fields.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: null,
      };
    case 'waiting_quota':
      return {
        ...base,
        label: 'Waiting for quota',
        variant: 'warning',
        detail: 'Monthly extraction allowance reached; queued for next month.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: null,
      };
    case 'needs_review':
      return {
        ...base,
        label: 'Needs review',
        variant: 'warning',
        detail: 'Some items are flagged for review.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: 'View receipt',
      };
    case 'ready':
      return {
        ...base,
        label: 'Ready',
        variant: 'success',
        detail: 'Extraction complete.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: 'View receipt',
      };
    case 'failed':
      return {
        ...base,
        label: 'Failed',
        variant: 'danger',
        detail: stage?.failureReason ?? 'Extraction failed.',
        percent: null,
        receiptId: item.receiptId,
        linkLabel: null,
      };
  }
}

/** Receipt photo upload screen: a durable queue with per-file progress and
 * server-derived pipeline stages, polled every 2 s while anything runs. */
export function UploadPage() {
  const queryClient = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const nextLocalSeqRef = useRef(1);
  const prevStagesActiveRef = useRef(false);

  const [isDragActive, setIsDragActive] = useState(false);
  const [batchLimitError, setBatchLimitError] = useState<string | null>(null);
  const [rejectedFiles, setRejectedFiles] = useState<RejectedFile[]>([]);

  const usageQuery = useUsage();
  const { items, progress, enqueue, clearSettled } = useUploadQueue();

  // Receipts whose stage the screen follows: files the queue landed.
  const trackedIds = useMemo(() => {
    const seen = new Set<string>();
    const ids: string[] = [];
    for (const item of items) {
      if (
        item.state === 'done' &&
        item.outcome !== 'duplicate' &&
        item.receiptId &&
        !seen.has(item.receiptId)
      ) {
        seen.add(item.receiptId);
        ids.push(item.receiptId);
      }
    }
    return ids.slice(-100);
  }, [items]);

  const hasUnsettledStages = useCallback(
    (page: PipelinePage | undefined) => {
      if (trackedIds.length === 0) return false;
      const byId = new Map(
        (page?.items ?? []).map((entry) => [entry.receiptId, entry]),
      );
      return trackedIds.some((id) => {
        const stage = byId.get(id)?.stage ?? 'queued';
        return (
          stage === 'queued' || stage === 'extracting' || stage === 'matching'
        );
      });
    },
    [trackedIds],
  );

  const pipelineQuery = usePipeline(
    { ids: trackedIds },
    {
      enabled: trackedIds.length > 0,
      refetchInterval: (data, isError) => {
        if (isError) return false;
        return hasUnsettledStages(data) ? POLL_INTERVAL_MS : false;
      },
    },
  );

  const stageById = useMemo(() => {
    const map = new Map<string, PipelineItem>();
    for (const entry of pipelineQuery.data?.items ?? []) {
      map.set(entry.receiptId, entry);
    }
    return map;
  }, [pipelineQuery.data]);

  const stagesActive = useMemo(
    () => !pipelineQuery.isError && hasUnsettledStages(pipelineQuery.data),
    [hasUnsettledStages, pipelineQuery.data, pipelineQuery.isError],
  );

  // When the last tracked receipt settles, the usage counter and the
  // review list may have changed behind the caches.
  useEffect(() => {
    if (prevStagesActiveRef.current && !stagesActive) {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.settings.usage,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
    }
    prevStagesActiveRef.current = stagesActive;
  }, [stagesActive, queryClient]);

  function handleSelectedFiles(fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return;
    const files = Array.from(fileList);

    if (files.length > MAX_BATCH_FILES) {
      setBatchLimitError(
        `You can upload at most ${MAX_BATCH_FILES} files in one selection (${files.length} chosen).`,
      );
      return;
    }

    setBatchLimitError(null);

    const rejected: RejectedFile[] = [];
    const valid: File[] = [];
    for (const file of files) {
      const errorMessage = validateUploadFile(file);
      if (errorMessage) {
        rejected.push({
          localId: `rejected-${nextLocalSeqRef.current++}`,
          fileName: file.name,
          sizeBytes: file.size,
          errorMessage,
        });
      } else {
        valid.push(file);
      }
    }
    if (rejected.length > 0) {
      setRejectedFiles((current) => [...rejected, ...current]);
    }
    if (valid.length > 0) {
      void enqueue(valid);
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

  function handleClearSettled() {
    setRejectedFiles([]);
    void clearSettled();
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

  const rejectedRows: UploadRow[] = rejectedFiles.map((entry) => ({
    key: entry.localId,
    fileName: entry.fileName,
    sizeBytes: entry.sizeBytes,
    label: 'Failed',
    variant: 'danger',
    detail: entry.errorMessage,
    percent: null,
    receiptId: null,
    linkLabel: null,
  }));

  const queueRows = items.map((item) =>
    describeQueueItem(item, stageById, progress),
  );
  const rows = [...rejectedRows, ...queueRows];
  const needsSignin = items.some((item) => item.state === 'needs_signin');

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Upload"
        title="Upload receipts"
        description="Each photo uploads on its own and resumes automatically if the connection drops or the app closes."
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
        description="Photos are stored on this device before sending, so an interrupted upload continues where it left off. Previews are omitted because browsers do not render HEIC files natively."
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
              Up to 20 files per selection
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
              onClick={() => fileInputRef.current?.click()}
            >
              Select photos
            </Button>
          </div>

          {batchLimitError ? (
            <ErrorState
              title="Selection size limit"
              message={batchLimitError}
            />
          ) : null}
        </div>
      </Card>

      {needsSignin ? (
        <ErrorState
          title="Sign in required"
          message="Your session expired. Sign in again; queued photos resume when you return to this page."
        />
      ) : null}

      {pipelineQuery.isError ? (
        <ErrorState
          title="Could not refresh extraction progress"
          message={getErrorMessage(pipelineQuery.error)}
          onRetry={() => {
            void pipelineQuery.refetch();
          }}
        />
      ) : null}

      {rows.length === 0 ? (
        <EmptyState
          title="No files uploaded yet"
          description="Choose or drop receipt photos above to start extraction."
        />
      ) : (
        <Card
          title="Upload status"
          description={
            stagesActive
              ? 'Checking extraction progress every 2 seconds...'
              : 'Everything in this list has settled.'
          }
          actions={
            <div className="flex items-center gap-2">
              {stagesActive ? (
                <Spinner size="sm" showLabel label="Polling" />
              ) : null}
              <Button
                variant="secondary"
                size="sm"
                onClick={handleClearSettled}
              >
                Clear list
              </Button>
            </div>
          }
        >
          <ul className="divide-y divide-border">
            {rows.map((row) => (
              <li
                key={row.key}
                className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="flex min-w-0 flex-col gap-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-small font-semibold text-ink">
                      {row.fileName}
                    </span>
                    <span className="text-caption text-muted tabular-nums">
                      {formatByteSize(row.sizeBytes)}
                    </span>
                  </div>
                  {row.detail ? (
                    <p
                      className={`text-caption ${row.variant === 'danger' ? 'text-danger' : 'text-muted'}`}
                    >
                      {row.detail}
                    </p>
                  ) : null}
                  {row.percent !== null ? (
                    <div
                      role="progressbar"
                      aria-label={`Upload progress for ${row.fileName}`}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={row.percent}
                      className="h-2 w-full max-w-48 overflow-hidden rounded-control bg-surface-subtle"
                    >
                      {/* Static and determinate by design: the width is the
                          only thing that changes between renders. */}
                      <div
                        className="h-full rounded-control bg-accent"
                        style={{ width: `${row.percent}%` }}
                      />
                    </div>
                  ) : null}
                </div>

                <div className="flex shrink-0 flex-wrap items-center gap-3">
                  <Badge variant={row.variant}>{row.label}</Badge>
                  {row.receiptId && row.linkLabel ? (
                    <Link
                      to={`/receipts/${row.receiptId}`}
                      className="text-small font-medium text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                    >
                      {row.linkLabel}
                    </Link>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}
