import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type ReviewFlagListParams } from './query-keys';
import type { components } from './schema.gen';

export type ReviewFlagPage = components['schemas']['ReviewFlagPage'];
export type ReviewFlagEntry = components['schemas']['ReviewFlagEntry'];
export type ReviewFlag = components['schemas']['ReviewFlag'];
export type ReviewResolution = components['schemas']['ReviewResolution'];

/** Fetches the open review-flag queue and total open count. */
export function useReviewFlags(params: ReviewFlagListParams = {}) {
  return useQuery({
    queryKey: queryKeys.reviewFlags.list(params),
    queryFn: () =>
      unwrap(api.GET('/review-flags', { params: { query: params } })),
  });
}

/** Applies a resolution to a review flag and refreshes affected caches. */
export function useResolveReviewFlag() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      flagId,
      body,
    }: {
      flagId: string;
      body: ReviewResolution;
    }) =>
      unwrap(
        api.POST('/review-flags/{flagId}/resolve', {
          params: { path: { flagId } },
          body,
        }),
      ),
    onSuccess: (receipt) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receipt.id), receipt);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}
