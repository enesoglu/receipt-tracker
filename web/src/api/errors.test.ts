import { describe, expect, it } from 'vitest';
import { ApiError } from './client';
import {
  formatApiError,
  getErrorMessage,
  isForbiddenError,
  isUnauthorizedError,
} from './errors';

function makeApiError(
  status: number,
  overrides: Partial<ApiError['problem']> = {},
) {
  return new ApiError({
    type: `https://example.com/problems/${status}`,
    title: 'Request failed',
    status,
    code: 'err_code',
    detail: 'Detailed failure reason',
    requestId: 'req-123',
    ...overrides,
  });
}

describe('api error helpers', () => {
  it('detects 401 and 403 ApiError instances', () => {
    const err401 = makeApiError(401);
    const err403 = makeApiError(403);
    const err404 = makeApiError(404);
    const native = new Error('boom');

    expect(isUnauthorizedError(err401)).toBe(true);
    expect(isUnauthorizedError(err403)).toBe(false);
    expect(isUnauthorizedError(native)).toBe(false);

    expect(isForbiddenError(err403)).toBe(true);
    expect(isForbiddenError(err401)).toBe(false);
    expect(isForbiddenError(err404)).toBe(false);
  });

  it('formats RFC 9457 Problem details including first field error per field', () => {
    const err = makeApiError(400, {
      title: 'Validation failed',
      detail: 'One or more fields are invalid',
      code: 'invalid_input',
      errors: [
        { field: 'name', message: 'is required' },
        { field: 'name', message: 'should not overwrite' },
        { field: 'amount', message: 'must be positive' },
      ],
    });

    expect(formatApiError(err)).toEqual({
      title: 'Validation failed',
      message: 'One or more fields are invalid',
      status: 400,
      code: 'invalid_input',
      requestId: 'req-123',
      fieldErrors: {
        name: 'is required',
        amount: 'must be positive',
      },
    });
  });

  it('falls back gracefully for native errors and unknown throwables', () => {
    expect(formatApiError(new Error('Network down'))).toEqual({
      title: 'Connection error',
      message: 'Network down',
      fieldErrors: {},
    });

    expect(getErrorMessage('weird failure', 'Fallback msg')).toBe(
      'Fallback msg',
    );
  });
});
