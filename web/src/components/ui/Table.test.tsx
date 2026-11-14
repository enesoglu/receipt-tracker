import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Table, type TableColumn } from './Table';

interface SampleRow {
  id: string;
  name: string;
  total: string;
}

const columns: readonly TableColumn<SampleRow>[] = [
  { key: 'name', header: 'Store', cell: (row) => row.name },
  {
    key: 'total',
    header: 'Total',
    align: 'right',
    cell: (row) => row.total,
  },
];

describe('Table', () => {
  it('renders headers, rows, caption, and empty message', () => {
    const { rerender } = render(
      <Table
        caption="Recent receipts"
        columns={columns}
        rows={[{ id: '1', name: 'Example Market', total: '129.40' }]}
        rowKey={(row) => row.id}
      />,
    );

    expect(
      screen.getByRole('table', { name: 'Recent receipts' }),
    ).toBeInTheDocument();
    expect(screen.getByText('Example Market')).toBeInTheDocument();
    expect(screen.getByText('129.40')).toBeInTheDocument();

    rerender(
      <Table
        caption="Recent receipts"
        columns={columns}
        rows={[]}
        rowKey={(row) => row.id}
        emptyMessage="No matching receipts."
      />,
    );
    expect(screen.getByText('No matching receipts.')).toBeInTheDocument();
  });

  it('supports click and keyboard Enter/Space activation on interactive rows', () => {
    const onRowClick = vi.fn();
    render(
      <Table
        columns={columns}
        rows={[{ id: '1', name: 'Corner Shop', total: '44.00' }]}
        rowKey={(row) => row.id}
        onRowClick={onRowClick}
        getRowLabel={(row) => `Open ${row.name}`}
      />,
    );

    const row = screen.getByRole('row', { name: 'Open Corner Shop' });
    row.focus();
    expect(row).toHaveFocus();
    fireEvent.keyDown(row, { key: 'Enter' });
    fireEvent.keyDown(row, { key: ' ' });
    fireEvent.click(row);
    expect(onRowClick).toHaveBeenCalledTimes(3);
  });
});
