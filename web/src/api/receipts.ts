import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys, type ReceiptListParams } from './query-keys';
import type { components } from './schema.gen';

export type ReceiptPage = components['schemas']['ReceiptPage'];
export type ReceiptSummary = components['schemas']['ReceiptSummary'];
export type Receipt = components['schemas']['Receipt'];
export type ReceiptUpdate = components['schemas']['ReceiptUpdate'];
export type LineItem = components['schemas']['LineItem'];
export type LineItemInput = components['schemas']['LineItemInput'];

/** Lists receipts matching optional filter and cursor parameters. */
export function useReceipts(params: ReceiptListParams = {}) {
  return useQuery({
    queryKey: queryKeys.receipts.list(params),
    queryFn: () => unwrap(api.GET('/receipts', { params: { query: params } })),
  });
}

/** Fetches a single receipt with its line items, flags, and images. */
export function useReceipt(receiptId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.receipts.detail(receiptId ?? ''),
    enabled: Boolean(receiptId),
    queryFn: () =>
      unwrap(
        api.GET('/receipts/{receiptId}', {
          params: { path: { receiptId: receiptId ?? '' } },
        }),
      ),
  });
}

/** Updates mutable metadata fields on a receipt and refreshes caches. */
export function useUpdateReceipt(receiptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ReceiptUpdate) =>
      unwrap(
        api.PUT('/receipts/{receiptId}', {
          params: { path: { receiptId } },
          body,
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receiptId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Deletes a receipt and invalidates receipt, review, and report queries. */
export function useDeleteReceipt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (receiptId: string) =>
      unwrap(
        api.DELETE('/receipts/{receiptId}', {
          params: { path: { receiptId } },
        }),
      ),
    onSuccess: (_data, receiptId) => {
      queryClient.removeQueries({
        queryKey: queryKeys.receipts.detail(receiptId),
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Queues a receipt for re-extraction and refreshes its detail and usage. */
export function useReprocessReceipt(receiptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/receipts/{receiptId}/reprocess', {
          params: { path: { receiptId } },
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receiptId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.settings.usage,
      });
    },
  });
}

/** Builds a relative API URL for displaying a receipt image or its original. */
export function receiptImageUrl(
  receiptId: string,
  imageId: string,
  variant?: 'original',
): string {
  const base = `/api/v1/receipts/${encodeURIComponent(receiptId)}/images/${encodeURIComponent(imageId)}`;
  return variant ? `${base}?variant=${encodeURIComponent(variant)}` : base;
}

/** Appends a manual line item to a receipt. */
export function useCreateLineItem(receiptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: LineItemInput) =>
      unwrap(
        api.POST('/receipts/{receiptId}/line-items', {
          params: { path: { receiptId } },
          body,
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receiptId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Updates an existing line item on a receipt. */
export function useUpdateLineItem(receiptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      lineItemId,
      body,
    }: {
      lineItemId: string;
      body: LineItemInput;
    }) =>
      unwrap(
        api.PUT('/receipts/{receiptId}/line-items/{lineItemId}', {
          params: { path: { receiptId, lineItemId } },
          body,
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receiptId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Removes a line item from a receipt. */
export function useDeleteLineItem(receiptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (lineItemId: string) =>
      unwrap(
        api.DELETE('/receipts/{receiptId}/line-items/{lineItemId}', {
          params: { path: { receiptId, lineItemId } },
        }),
      ),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receiptId), updated);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}
