import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type LineSuggestion = components['schemas']['LineSuggestion'];
export type ReceiptSuggestions = components['schemas']['ReceiptSuggestions'];
export type SuggestionsStatus = components['schemas']['SuggestionsStatus'];

export interface UseReceiptSuggestionsOptions {
  enabled?: boolean;
}

/** Fetches the model's suggestions for one receipt, in line order. */
export function useReceiptSuggestions(
  receiptId: string,
  options: UseReceiptSuggestionsOptions = {},
) {
  const { enabled } = options;
  return useQuery({
    queryKey: queryKeys.suggestions.forReceipt(receiptId),
    enabled,
    queryFn: () =>
      unwrap(
        api.GET('/receipts/{receiptId}/suggestions', {
          params: { path: { receiptId } },
        }),
      ),
  });
}

/** Fetches whether a suggestion endpoint is configured and reachable. */
export function useSuggestionsStatus(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.suggestions.status,
    enabled,
    queryFn: () => unwrap(api.GET('/suggestions/status')),
  });
}

/** Accepts a pending suggestion and refreshes the affected caches. */
export function useAcceptReceiptSuggestion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      receiptId,
      suggestionId,
    }: {
      receiptId: string;
      suggestionId: string;
    }) =>
      unwrap(
        api.POST('/receipts/{receiptId}/suggestions/{suggestionId}/accept', {
          params: { path: { receiptId, suggestionId } },
        }),
      ),
    onSuccess: (receipt) => {
      queryClient.setQueryData(queryKeys.receipts.detail(receipt.id), receipt);
      void queryClient.invalidateQueries({ queryKey: queryKeys.receipts.all });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.suggestions.all,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.reviewFlags.all,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.products.all });
      void queryClient.invalidateQueries({ queryKey: queryKeys.reports.all });
    },
  });
}

/** Ignores a pending suggestion and refreshes the affected caches. */
export function useIgnoreReceiptSuggestion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      receiptId,
      suggestionId,
    }: {
      receiptId: string;
      suggestionId: string;
    }) =>
      unwrap(
        api.POST('/receipts/{receiptId}/suggestions/{suggestionId}/ignore', {
          params: { path: { receiptId, suggestionId } },
        }),
      ),
    onSuccess: (suggestion) => {
      queryClient.setQueryData<ReceiptSuggestions>(
        queryKeys.suggestions.forReceipt(suggestion.receiptId),
        (previous) =>
          previous
            ? {
                items: previous.items.map((item) =>
                  item.id === suggestion.id ? suggestion : item,
                ),
              }
            : previous,
      );
      void queryClient.invalidateQueries({
        queryKey: queryKeys.suggestions.all,
      });
    },
  });
}
