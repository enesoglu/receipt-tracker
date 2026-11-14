import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type StorageStatus = components['schemas']['StorageStatus'];

/** Fetches the connected Google Drive storage and backup health status. */
export function useStorageStatus() {
  return useQuery({
    queryKey: queryKeys.storage.status,
    queryFn: () => unwrap(api.GET('/storage')),
  });
}

/** Disconnects Google Drive storage and refreshes the status cache. */
export function useDisconnectStorage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.DELETE('/storage/connection')),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
    },
  });
}
