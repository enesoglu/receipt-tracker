import { Link } from 'react-router';
import { usePipeline, type PipelinePage } from '../../api/pipeline';

const POLL_INTERVAL_MS = 2000;

/**
 * Counts the receipts still working through the pipeline: without ids the
 * pipeline endpoint returns exactly the signed-in user's unsettled ones.
 */
function unsettledCount(page: PipelinePage | undefined): number {
  return page?.items.length ?? 0;
}

/**
 * Shell-wide processing activity: shows how many receipts are queued,
 * extracting, matching or waiting for quota, and links to the upload
 * screen. It polls every 2 seconds while any are unsettled and stops at
 * zero. Nothing renders at zero or on error — a passive indicator never
 * interrupts a screen — and nothing animates: the only thing that changes
 * between renders is the count.
 */
export function ProcessingIndicator() {
  const pipelineQuery = usePipeline(undefined, {
    refetchInterval: (data, isError) =>
      isError || unsettledCount(data) === 0 ? false : POLL_INTERVAL_MS,
  });
  const count = unsettledCount(pipelineQuery.data);
  if (count === 0) return null;

  return (
    <Link
      to="/upload"
      className="flex items-center gap-2 rounded-control bg-accent-soft px-3 py-2 text-small font-medium text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <span className="h-2 w-2 rounded-full bg-accent" aria-hidden="true" />
      <span>
        Processing {count} receipt{count === 1 ? '' : 's'}
      </span>
    </Link>
  );
}
