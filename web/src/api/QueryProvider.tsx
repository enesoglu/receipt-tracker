import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useState, type ReactNode } from 'react';
import { createAppQueryClient } from './query-client';

export interface QueryProviderProps {
  children: ReactNode;
  onUnauthorized?: () => void;
  client?: QueryClient;
}

interface ManagedClient {
  client: QueryClient;
  setOnUnauthorized: (handler: (() => void) | undefined) => void;
}

function createManagedClient(): ManagedClient {
  let handler: (() => void) | undefined;
  const client = createAppQueryClient({
    onUnauthorized: () => handler?.(),
  });
  return {
    client,
    setOnUnauthorized: (next) => {
      handler = next;
    },
  };
}

/** Provides TanStack Query state and centralizes 401 redirection handling. */
export function QueryProvider({
  children,
  onUnauthorized,
  client,
}: QueryProviderProps) {
  const [managed] = useState(createManagedClient);

  useEffect(() => {
    managed.setOnUnauthorized(onUnauthorized);
  }, [managed, onUnauthorized]);

  return (
    <QueryClientProvider client={client ?? managed.client}>
      {children}
    </QueryClientProvider>
  );
}
