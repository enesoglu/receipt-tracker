import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type CategoryListParams } from './query-keys';
import type { components } from './schema.gen';

export type CategoryPage = components['schemas']['CategoryPage'];
export type Category = components['schemas']['Category'];
export type CategoryInput = components['schemas']['CategoryInput'];

/** Lists the user's spending categories. */
export function useCategories(params: CategoryListParams = {}) {
  return useQuery({
    queryKey: queryKeys.categories.list(params),
    queryFn: () =>
      unwrap(api.GET('/categories', { params: { query: params } })),
  });
}

/** Creates a new category and invalidates category queries. */
export function useCreateCategory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CategoryInput) =>
      unwrap(api.POST('/categories', { body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.categories.all,
      });
    },
  });
}

/** Renames a category and refreshes category, product, and report caches. */
export function useUpdateCategory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      categoryId,
      body,
    }: {
      categoryId: string;
      body: CategoryInput;
    }) =>
      unwrap(
        api.PUT('/categories/{categoryId}', {
          params: { path: { categoryId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.categories.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Deletes a category and clears references across products and reports. */
export function useDeleteCategory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (categoryId: string) =>
      unwrap(
        api.DELETE('/categories/{categoryId}', {
          params: { path: { categoryId } },
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.categories.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}
