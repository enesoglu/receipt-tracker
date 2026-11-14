import { useMutation, useQuery } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { queryKeys } from './query-keys';
import { createAppQueryClient } from './query-client';
import { QueryProvider } from './QueryProvider';

function makeProblemError(status: number, title = 'Error') {
  return new ApiError({
    type: `https://example.com/problems/${status}`,
    title,
    status,
  });
}

describe('QueryProvider and createAppQueryClient', () => {
  it('clears cached user and triggers onUnauthorized on 401 query error', async () => {
    const onUnauthorized = vi.fn();
    const client = createAppQueryClient({ onUnauthorized, staleTime: 0 });
    client.setQueryData(queryKeys.me, {
      id: 'u1',
      email: 'ada@example.com',
      name: 'Ada',
      isAdmin: false,
    });

    function Probe() {
      const query = useQuery({
        queryKey: ['probe-401'],
        queryFn: () => Promise.reject(makeProblemError(401, 'Unauthorized')),
      });
      return <span>{query.status}</span>;
    }

    render(
      <QueryProvider client={client}>
        <Probe />
      </QueryProvider>,
    );

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(client.getQueryData(queryKeys.me)).toBeNull();
  });

  it('triggers onUnauthorized on 401 mutation error', async () => {
    const onUnauthorized = vi.fn();
    const client = createAppQueryClient({ onUnauthorized });

    function Probe() {
      const mutation = useMutation({
        mutationFn: () => Promise.reject(makeProblemError(401, 'Unauthorized')),
      });
      return (
        <button
          type="button"
          onClick={() => {
            mutation.mutate();
          }}
        >
          Trigger
        </button>
      );
    }

    render(
      <QueryProvider client={client}>
        <Probe />
      </QueryProvider>,
    );

    await act(async () => {
      screen.getByRole('button', { name: 'Trigger' }).click();
    });

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it('does not retry 4xx errors', async () => {
    const queryFn = vi.fn(() =>
      Promise.reject(makeProblemError(404, 'Not found')),
    );
    const client = createAppQueryClient({ staleTime: 0 });

    function Probe() {
      const query = useQuery({
        queryKey: ['probe-404'],
        queryFn,
      });
      return <span>{query.isError ? 'failed' : 'loading'}</span>;
    }

    render(
      <QueryProvider client={client}>
        <Probe />
      </QueryProvider>,
    );

    await waitFor(() => expect(screen.getByText('failed')).toBeInTheDocument());
    expect(queryFn).toHaveBeenCalledTimes(1);
  });
});
