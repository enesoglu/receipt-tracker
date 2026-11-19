import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type StoreListParams } from './query-keys';
import type { components } from './schema.gen';

export type StorePage = components['schemas']['StorePage'];
export type Store = components['schemas']['Store'];
export type StoreDetail = components['schemas']['StoreDetail'];
export type StoreCreate = components['schemas']['StoreCreate'];
export type StoreUpdate = components['schemas']['StoreUpdate'];
export type StoreMerge = components['schemas']['StoreMerge'];

/** Lists the user's stores ordered by name. */
export function useStores(params: StoreListParams = {}) {
  return useQuery({
    queryKey: queryKeys.stores.list(params),
    queryFn: () => unwrap(api.GET('/stores', { params: { query: params } })),
  });
}

/** Fetches a store with the raw names and branches its receipts showed. */
export function useStore(storeId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.stores.detail(storeId ?? ''),
    enabled: Boolean(storeId),
    queryFn: () =>
      unwrap(
        api.GET('/stores/{storeId}', {
          params: { path: { storeId: storeId ?? '' } },
        }),
      ),
  });
}

/** Creates a store and refreshes store, receipt, report, and product queries. */
export function useCreateStore() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: StoreCreate) => unwrap(api.POST('/stores', { body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.stores.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
    },
  });
}

/** Renames a store and refreshes store, receipt, report, and product queries. */
export function useUpdateStore() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ storeId, body }: { storeId: string; body: StoreUpdate }) =>
      unwrap(
        api.PUT('/stores/{storeId}', {
          params: { path: { storeId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.stores.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
    },
  });
}

/** Merges a source store into the target and refreshes dependent queries. */
export function useMergeStore() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ storeId, body }: { storeId: string; body: StoreMerge }) =>
      unwrap(
        api.POST('/stores/{storeId}/merge', {
          params: { path: { storeId } },
          body,
        }),
      ),
    onSuccess: (merged) => {
      queryClient.setQueryData(queryKeys.stores.detail(merged.id), merged);
      void queryClient.invalidateQueries({ queryKey: queryKeys.stores.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
    },
  });
}

/** Deletes an unused store and refreshes store, receipt, report, and product queries. */
export function useDeleteStore() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (storeId: string) =>
      unwrap(
        api.DELETE('/stores/{storeId}', {
          params: { path: { storeId } },
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.stores.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
    },
  });
}
