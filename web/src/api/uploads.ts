import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type UploadResponse = components['schemas']['UploadResponse'];
export type UploadResult = components['schemas']['UploadResult'];

/** Uploads up to 20 receipt photos as multipart/form-data. */
export function useUploadReceipts() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (files: readonly File[]) =>
      unwrap(
        api.POST('/receipts', {
          body: { files: files as unknown as string[] },
          bodySerializer: () => {
            const form = new FormData();
            for (const file of files) {
              form.append('files', file);
            }
            return form;
          },
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.settings.usage,
      });
    },
  });
}
