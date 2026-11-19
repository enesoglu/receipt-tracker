import type { components } from '../../api/schema.gen';
import { formatDateValue } from '../../components/ui/format';

type MonthlySpending = components['schemas']['MonthlySpending'];
type BreakdownGroup = components['schemas']['BreakdownGroup'];

export interface SpendingChartRow {
  month: string;
  monthLabel: string;
  amount: string;
  amountNumeric: number;
  receiptCount: number;
}

export interface CurrencyMonthlySpendingGroup {
  currency: string;
  totalReceipts: number;
  rows: SpendingChartRow[];
}

export interface BreakdownChartRow {
  name: string;
  amount: string;
  amountNumeric: number;
  share: string;
  itemCount: number;
}

/** Returns the default inclusive last-12-month range ending at the user's current local month. */
export function getDefaultMonthRange(now = new Date()): {
  from: string;
  to: string;
} {
  const endYear = now.getFullYear();
  const endMonth = now.getMonth() + 1;
  const endTotalMonths = endYear * 12 + (endMonth - 1);
  const startTotalMonths = endTotalMonths - 11;
  const startYear = Math.floor(startTotalMonths / 12);
  const startMonth = (startTotalMonths % 12) + 1;

  return {
    from: `${startYear}-${String(startMonth).padStart(2, '0')}`,
    to: `${endYear}-${String(endMonth).padStart(2, '0')}`,
  };
}

/** Validates YYYY-MM boundaries against the API constraint (from <= to, max 60 months). */
export function validateMonthRange(from: string, to: string): string | null {
  const fromMatch = /^(\d{4})-(0[1-9]|1[0-2])$/.exec(from.trim());
  const toMatch = /^(\d{4})-(0[1-9]|1[0-2])$/.exec(to.trim());
  if (!fromMatch || !toMatch) {
    return 'Select valid From and To months in YYYY-MM format.';
  }
  if (from > to) {
    return 'From month must be on or before To month.';
  }

  const fromYear = Number(fromMatch[1]);
  const fromMonth = Number(fromMatch[2]);
  const toYear = Number(toMatch[1]);
  const toMonth = Number(toMatch[2]);
  const monthSpan = (toYear - fromYear) * 12 + (toMonth - fromMonth) + 1;
  if (monthSpan > 60) {
    return 'Date range cannot exceed 60 months.';
  }

  return null;
}

/** Splits monthly spending entries into separate series per currency preserving timeline continuity. */
export function groupMonthlySpendingByCurrency(
  spending: MonthlySpending,
): CurrencyMonthlySpendingGroup[] {
  const currencies: string[] = [];
  for (const monthEntry of spending.months) {
    for (const total of monthEntry.totals) {
      if (!currencies.includes(total.currency)) {
        currencies.push(total.currency);
      }
    }
  }

  return currencies.map((currency) => {
    let totalReceipts = 0;
    const rows: SpendingChartRow[] = spending.months.map((monthEntry) => {
      const match = monthEntry.totals.find(
        (total) => total.currency === currency,
      );
      const amount = match?.amount ?? '0.00';
      const numeric = Number(amount);
      const receiptCount = match?.receiptCount ?? 0;
      totalReceipts += receiptCount;

      return {
        month: monthEntry.month,
        monthLabel: formatDateValue(monthEntry.month, 'month'),
        amount,
        amountNumeric: Number.isFinite(numeric) ? numeric : 0,
        receiptCount,
      };
    });

    return {
      currency,
      totalReceipts,
      rows,
    };
  });
}

/** Formats a decimal Ratio string (e.g. "0.6530") as a readable percentage ("65.3%"). */
export function formatSharePercentage(share: string): string {
  const numeric = Number(share);
  if (!Number.isFinite(numeric)) return '—';
  return new Intl.NumberFormat('en-US', {
    style: 'percent',
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(numeric);
}

/** Builds breakdown chart rows from groups without floating-point money accumulation. */
export function toBreakdownChartRows(
  groups: readonly BreakdownGroup[],
): BreakdownChartRow[] {
  return groups.map((group) => {
    const numeric = Number(group.amount);
    return {
      name: group.name,
      amount: group.amount,
      amountNumeric: Number.isFinite(numeric) ? numeric : 0,
      share: group.share,
      itemCount: group.itemCount,
    };
  });
}

/** Builds a filtered /receipts link for a breakdown row. */
export function buildBreakdownReceiptsHref(
  month: string,
  group: BreakdownGroup,
): string {
  const params = new URLSearchParams({ month });
  if (group.kind === 'store' && group.id) {
    params.set('storeId', group.id);
  }
  return `/receipts?${params.toString()}`;
}
