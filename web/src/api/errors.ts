import { ApiError } from './client';

export interface FormattedApiError {
  title: string;
  message: string;
  status?: number;
  code?: string;
  requestId?: string;
  fieldErrors: Record<string, string>;
}

/** Returns true when an error is an API 401 Unauthorized problem. */
export function isUnauthorizedError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}

/** Returns true when an error is an API 403 Forbidden problem. */
export function isForbiddenError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403;
}

/**
 * Extracts a consistent, user-ready summary and per-field errors from any
 * `ApiError` (RFC 9457 Problem) or unexpected runtime failure.
 */
export function formatApiError(
  error: unknown,
  fallback = 'Something went wrong. Please try again.',
): FormattedApiError {
  if (error instanceof ApiError) {
    const fieldErrors: Record<string, string> = {};
    for (const entry of error.problem.errors ?? []) {
      if (!(entry.field in fieldErrors)) {
        fieldErrors[entry.field] = entry.message;
      }
    }
    return {
      title: error.problem.title,
      message: error.problem.detail ?? error.problem.title,
      status: error.problem.status,
      code: error.problem.code,
      requestId: error.problem.requestId,
      fieldErrors,
    };
  }

  if (error instanceof Error) {
    return {
      title: 'Connection error',
      message: error.message || fallback,
      fieldErrors: {},
    };
  }

  return {
    title: 'Error',
    message: fallback,
    fieldErrors: {},
  };
}

/** Convenience helper returning the primary user-facing error message. */
export function getErrorMessage(
  error: unknown,
  fallback = 'Something went wrong. Please try again.',
): string {
  return formatApiError(error, fallback).message;
}
