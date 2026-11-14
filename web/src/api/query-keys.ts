import type { operations } from './schema.gen';

export type ReceiptListParams = NonNullable<
  operations['listReceipts']['parameters']['query']
>;
export type ReviewFlagListParams = NonNullable<
  operations['listReviewFlags']['parameters']['query']
>;
export type ProductListParams = NonNullable<
  operations['listProducts']['parameters']['query']
>;
export type PriceHistoryParams = NonNullable<
  operations['getProductPriceHistory']['parameters']['query']
>;
export type StoreListParams = NonNullable<
  operations['listStores']['parameters']['query']
>;
export type CategoryListParams = NonNullable<
  operations['listCategories']['parameters']['query']
>;
export type MonthlySpendingParams =
  operations['getMonthlySpending']['parameters']['query'];
export type MonthBreakdownParams =
  operations['getMonthBreakdown']['parameters']['query'];

/** Centralized TanStack Query keys for every API resource and filter combination. */
export const queryKeys = {
  me: ['auth', 'me'] as const,
  receipts: {
    all: ['receipts'] as const,
    list: (params: ReceiptListParams = {}) =>
      ['receipts', 'list', params] as const,
    detail: (receiptId: string) => ['receipts', 'detail', receiptId] as const,
  },
  reviewFlags: {
    all: ['review-flags'] as const,
    list: (params: ReviewFlagListParams = {}) =>
      ['review-flags', 'list', params] as const,
  },
  products: {
    all: ['products'] as const,
    list: (params: ProductListParams = {}) =>
      ['products', 'list', params] as const,
    detail: (productId: string) => ['products', 'detail', productId] as const,
    priceHistory: (productId: string, params: PriceHistoryParams = {}) =>
      ['products', 'price-history', productId, params] as const,
  },
  stores: {
    all: ['stores'] as const,
    list: (params: StoreListParams = {}) => ['stores', 'list', params] as const,
  },
  categories: {
    all: ['categories'] as const,
    list: (params: CategoryListParams = {}) =>
      ['categories', 'list', params] as const,
  },
  reports: {
    all: ['reports'] as const,
    monthlySpending: (params: MonthlySpendingParams) =>
      ['reports', 'monthly-spending', params] as const,
    monthBreakdown: (params: MonthBreakdownParams) =>
      ['reports', 'month-breakdown', params] as const,
  },
  settings: {
    all: ['settings'] as const,
    user: ['settings', 'user'] as const,
    usage: ['settings', 'usage'] as const,
  },
  storage: {
    all: ['storage'] as const,
    status: ['storage', 'status'] as const,
  },
} as const;
