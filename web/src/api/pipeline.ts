import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type PipelineParams } from './query-keys';
import type { components } from './schema.gen';

export type PipelinePage = components['schemas']['PipelinePage'];
export type PipelineItem = components['schemas']['PipelineItem'];
export type PipelineStage = components['schemas']['PipelineStage'];

export interface UsePipelineOptions {
  enabled?: boolean;
  refetchInterval?:
    | number
    | false
    | ((data: PipelinePage | undefined, isError: boolean) => number | false);
}

/**
 * Fetches the pipeline stage of the given receipts, or of every unsettled
 * receipt of the signed-in user when no ids are given.
 */
export function usePipeline(
  params: PipelineParams = {},
  options: UsePipelineOptions = {},
) {
  const { enabled, refetchInterval } = options;
  return useQuery({
    queryKey: queryKeys.pipeline.list(params),
    enabled,
    refetchInterval:
      typeof refetchInterval === 'function'
        ? (query) =>
            refetchInterval(query.state.data, query.state.status === 'error')
        : refetchInterval,
    queryFn: () => unwrap(api.GET('/pipeline', { params: { query: params } })),
  });
}
