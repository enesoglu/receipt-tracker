import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import {
  queryKeys,
  type MonthBreakdownParams,
  type MonthlySpendingParams,
} from './query-keys';
import type { components } from './schema.gen';

export type MonthlySpending = components['schemas']['MonthlySpending'];
export type MonthBreakdown = components['schemas']['MonthBreakdown'];

/** Fetches monthly spending totals across a closed YYYY-MM month range. */
export function useMonthlySpending(
  params: MonthlySpendingParams,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.reports.monthlySpending(params),
    enabled,
    queryFn: () =>
      unwrap(
        api.GET('/reports/monthly-spending', { params: { query: params } }),
      ),
  });
}

/** Fetches a single month's spending broken down by category or store. */
export function useMonthBreakdown(
  params: MonthBreakdownParams,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.reports.monthBreakdown(params),
    enabled,
    queryFn: () =>
      unwrap(
        api.GET('/reports/month-breakdown', { params: { query: params } }),
      ),
  });
}
