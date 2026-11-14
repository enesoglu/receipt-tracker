import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { queryKeys } from './query-keys';
import type { components } from './schema.gen';

export type User = components['schemas']['User'];

/** Fetches the currently signed-in user, or returns null after a 401/logout. */
export function useMe() {
  return useQuery<User | null>({
    queryKey: queryKeys.me,
    queryFn: () => unwrap(api.GET('/me')),
  });
}

/** Destroys the session and clears cached user data. */
export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST('/auth/logout')),
    onSuccess: () => {
      queryClient.setQueryData(queryKeys.me, null);
      queryClient.removeQueries({
        predicate: (query) => query.queryKey[0] !== 'auth',
      });
    },
  });
}
