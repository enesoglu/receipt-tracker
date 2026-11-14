import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import {
  queryKeys,
  type PriceHistoryParams,
  type ProductListParams,
} from './query-keys';
import type { components } from './schema.gen';

export type ProductPage = components['schemas']['ProductPage'];
export type ProductSummary = components['schemas']['ProductSummary'];
export type Product = components['schemas']['Product'];
export type ProductUpdate = components['schemas']['ProductUpdate'];
export type MergeProductsRequest =
  components['schemas']['MergeProductsRequest'];
export type SplitProductRequest = components['schemas']['SplitProductRequest'];
export type PriceHistory = components['schemas']['PriceHistory'];

/** Lists canonical products with optional search and category filters. */
export function useProducts(params: ProductListParams = {}) {
  return useQuery({
    queryKey: queryKeys.products.list(params),
    queryFn: () => unwrap(api.GET('/products', { params: { query: params } })),
  });
}

/** Fetches a product with its aliases, codes, and recent purchases. */
export function useProduct(productId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.products.detail(productId ?? ''),
    enabled: Boolean(productId),
    queryFn: () =>
      unwrap(
        api.GET('/products/{productId}', {
          params: { path: { productId: productId ?? '' } },
        }),
      ),
  });
}

/** Updates a product's name, category, or unit size. */
export function useUpdateProduct(productId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ProductUpdate) =>
      unwrap(
        api.PUT('/products/{productId}', {
          params: { path: { productId } },
          body,
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.products.detail(productId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.categories.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Merges source products into the target product. */
export function useMergeProducts(productId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: MergeProductsRequest) =>
      unwrap(
        api.POST('/products/{productId}/merge', {
          params: { path: { productId } },
          body,
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.products.detail(productId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Splits selected aliases or codes from a product into a new product. */
export function useSplitProduct(productId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: SplitProductRequest) =>
      unwrap(
        api.POST('/products/{productId}/split', {
          params: { path: { productId } },
          body,
        }),
      ),
    onSuccess: (created) => {
      queryClient.setQueryData(queryKeys.products.detail(created.id), created);
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Fetches the unit-price series for a product across stores. */
export function useProductPriceHistory(
  productId: string | undefined,
  params: PriceHistoryParams = {},
) {
  return useQuery({
    queryKey: queryKeys.products.priceHistory(productId ?? '', params),
    enabled: Boolean(productId),
    queryFn: () =>
      unwrap(
        api.GET('/products/{productId}/price-history', {
          params: { path: { productId: productId ?? '' }, query: params },
        }),
      ),
  });
}
