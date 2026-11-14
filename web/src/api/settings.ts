import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type Settings = components['schemas']['Settings'];
export type Usage = components['schemas']['Usage'];

/** Fetches the current user's default settings. */
export function useSettings() {
  return useQuery({
    queryKey: queryKeys.settings.user,
    queryFn: () => unwrap(api.GET('/settings')),
  });
}

/** Updates user settings and refreshes the settings cache. */
export function useUpdateSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Settings) => unwrap(api.PUT('/settings', { body })),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.settings.user, updated);
    },
  });
}

/** Fetches OCR extraction page usage for the current billing period. */
export function useUsage() {
  return useQuery({
    queryKey: queryKeys.settings.usage,
    queryFn: () => unwrap(api.GET('/usage')),
  });
}
