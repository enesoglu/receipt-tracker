import type { KeyboardEvent, ReactNode } from 'react';

export interface TableColumn<T> {
  key: string;
  header: string;
  align?: 'left' | 'right';
  cell: (row: T) => ReactNode;
}

export interface TableProps<T> {
  columns: readonly TableColumn<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  caption?: string;
  emptyMessage?: string;
  onRowClick?: (row: T) => void;
  getRowLabel?: (row: T) => string;
  className?: string;
}

/** Accessible data table with horizontal overflow support and optional row activation. */
export function Table<T>({
  columns,
  rows,
  rowKey,
  caption,
  emptyMessage = 'No items to display.',
  onRowClick,
  getRowLabel,
  className = '',
}: TableProps<T>) {
  function handleRowKeyDown(event: KeyboardEvent<HTMLTableRowElement>, row: T) {
    if (!onRowClick) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onRowClick(row);
    }
  }

  return (
    <div
      className={`w-full overflow-x-auto rounded-card border border-border bg-surface ${className}`.trim()}
    >
      <table className="w-full border-collapse text-left text-small">
        {caption ? <caption className="sr-only">{caption}</caption> : null}
        <thead className="border-b border-border bg-surface-subtle">
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                scope="col"
                className={`px-4 py-3 font-semibold text-muted ${column.align === 'right' ? 'text-right' : 'text-left'}`}
              >
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {rows.length === 0 ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-4 py-8 text-center text-muted"
              >
                {emptyMessage}
              </td>
            </tr>
          ) : (
            rows.map((row) => {
              const interactive = Boolean(onRowClick);
              return (
                <tr
                  key={rowKey(row)}
                  tabIndex={interactive ? 0 : undefined}
                  aria-label={
                    interactive && getRowLabel ? getRowLabel(row) : undefined
                  }
                  onClick={interactive ? () => onRowClick?.(row) : undefined}
                  onKeyDown={
                    interactive
                      ? (event) => handleRowKeyDown(event, row)
                      : undefined
                  }
                  className={
                    interactive
                      ? 'cursor-pointer hover:bg-surface-subtle focus-visible:bg-accent-soft'
                      : undefined
                  }
                >
                  {columns.map((column) => (
                    <td
                      key={column.key}
                      className={`px-4 py-3 text-ink ${column.align === 'right' ? 'text-right' : 'text-left'}`}
                    >
                      {column.cell(row)}
                    </td>
                  ))}
                </tr>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
