import createClient, { type Client, type ClientOptions } from 'openapi-fetch';
import type { components, paths } from './schema.gen';

/**
 * The RFC 9457 problem document that every failed API call answers with.
 * Branch on `status` and `code`; `title` and `detail` are text for the user.
 */
export type Problem = components['schemas']['Problem'];

/**
 * ApiError is thrown by `unwrap` when an API call fails. It always carries a
 * problem document, also when the failure did not come from the application
 * (a proxy error page, for example), so callers handle one shape.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem;

  constructor(problem: Problem) {
    super(problem.detail ?? problem.title);
    this.name = 'ApiError';
    this.status = problem.status;
    this.problem = problem;
  }

  /** The server's message for one input field, if it reported one. */
  fieldMessage(field: string): string | undefined {
    return this.problem.errors?.find((entry) => entry.field === field)?.message;
  }
}

function isProblem(value: unknown): value is Problem {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.status === 'number' &&
    typeof candidate.title === 'string' &&
    typeof candidate.type === 'string'
  );
}

/**
 * Creates a typed client for the contract in `api/openapi.yaml`. The
 * application uses the shared `api` instance; tests pass their own `fetch`.
 */
export function createApiClient(options: ClientOptions = {}): Client<paths> {
  return createClient<paths>({
    baseUrl: '/api/v1',
    // The contract sends lists as one comma-separated value (`ids=a,b`).
    querySerializer: { array: { style: 'form', explode: false } },
    ...options,
  });
}

/** The client the application uses. Paths are relative to `/api/v1`. */
export const api = createApiClient();

/** What a call on the client resolves to. */
interface CallResult<Data> {
  data?: Data;
  error?: unknown;
  response: Response;
}

/**
 * Resolves to the data of an API call, or throws an `ApiError`.
 *
 *     const receipt = await unwrap(api.GET('/receipts/{receiptId}', { params }));
 *
 * A call without a response body, such as a delete, resolves to `undefined`.
 */
export async function unwrap<Data>(
  call: Promise<CallResult<Data>>,
): Promise<Data> {
  const { data, error, response } = await call;
  if (response.ok) return data as Data;
  if (isProblem(error)) throw new ApiError(error);
  throw new ApiError({
    type: 'about:blank',
    title: response.statusText || 'Request failed',
    status: response.status,
  });
}
