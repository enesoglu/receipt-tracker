import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { ApiError } from './client';
import { isUnauthorizedError } from './errors';
import { queryKeys } from './query-keys';

export interface AppQueryClientOptions {
  onUnauthorized?: () => void;
  staleTime?: number;
}

/** Builds a QueryClient configured with 401 session handling and no 4xx retries. */
export function createAppQueryClient({
  onUnauthorized,
  staleTime = 30_000,
}: AppQueryClientOptions = {}): QueryClient {
  const client = new QueryClient({
    queryCache: new QueryCache({
      onError: (error) => handleError(error),
    }),
    mutationCache: new MutationCache({
      onError: (error) => handleError(error),
    }),
    defaultOptions: {
      queries: {
        staleTime,
        retry: (failureCount, error) => {
          if (
            error instanceof ApiError &&
            error.status >= 400 &&
            error.status < 500
          ) {
            return false;
          }
          return failureCount < 1;
        },
      },
      mutations: {
        retry: false,
      },
    },
  });

  function handleError(error: unknown) {
    if (!isUnauthorizedError(error)) return;
    client.setQueryData(queryKeys.me, null);
    onUnauthorized?.();
  }

  return client;
}
