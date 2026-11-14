import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type StoreListParams } from './query-keys';
import type { components } from './schema.gen';

export type StorePage = components['schemas']['StorePage'];
export type Store = components['schemas']['Store'];
export type StoreUpdate = components['schemas']['StoreUpdate'];

/** Lists normalized stores ordered by name. */
export function useStores(params: StoreListParams = {}) {
  return useQuery({
    queryKey: queryKeys.stores.list(params),
    queryFn: () => unwrap(api.GET('/stores', { params: { query: params } })),
  });
}

/** Renames a store and invalidates store, receipt, and report queries. */
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
    },
  });
}
