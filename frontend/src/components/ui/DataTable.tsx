import type { ReactNode } from 'react';

import { ArrowDownUp } from 'lucide-react';
import { useMemo, useState } from 'react';

import { cx } from './utils';

export type DataTableColumn<T> = {
  id: string;
  header: string;
  render: (row: T) => ReactNode;
  sortValue?: (row: T) => number | string;
  sortable?: boolean;
};

type DataTableProps<T> = {
  columns: Array<DataTableColumn<T>>;
  rows: T[];
  getRowID: (row: T) => string;
  selectedIDs?: Set<string>;
  onToggleRow?: (id: string) => void;
  bulkActions?: ReactNode;
  empty?: ReactNode;
};

type SortState = {
  columnID: string;
  direction: 'asc' | 'desc';
};

export function DataTable<T>({
  bulkActions,
  columns,
  empty,
  getRowID,
  onToggleRow,
  rows,
  selectedIDs = new Set<string>(),
}: DataTableProps<T>) {
  const hasSelection = selectedIDs.size > 0;
  const [sort, setSort] = useState<SortState | null>(null);
  const visibleRows = useMemo(() => {
    if (!sort) {
      return rows;
    }

    const sortColumn = columns.find((column) => column.id === sort.columnID);
    if (!sortColumn?.sortValue) {
      return rows;
    }

    return [...rows].sort((left, right) => {
      const leftValue = sortColumn.sortValue?.(left);
      const rightValue = sortColumn.sortValue?.(right);
      const direction = sort.direction === 'asc' ? 1 : -1;

      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * direction;
      }

      return String(leftValue).localeCompare(String(rightValue), undefined, {
        numeric: true,
        sensitivity: 'base',
      }) * direction;
    });
  }, [columns, rows, sort]);

  const toggleSort = (column: DataTableColumn<T>) => {
    if (!column.sortable || !column.sortValue) {
      return;
    }

    setSort((current) =>
      current?.columnID === column.id
        ? { columnID: column.id, direction: current.direction === 'asc' ? 'desc' : 'asc' }
        : { columnID: column.id, direction: 'asc' },
    );
  };

  if (rows.length === 0 && empty) {
    return <>{empty}</>;
  }

  return (
    <div className="overflow-hidden rounded-card border border-border bg-bg-card">
      {hasSelection ? (
        <div className="flex h-11 items-center justify-between border-b border-border bg-accent/10 px-3 text-sm">
          <span>{selectedIDs.size} selected</span>
          <div>{bulkActions}</div>
        </div>
      ) : null}
      <div className="max-h-[420px] overflow-auto">
        <table className="min-w-full table-fixed text-left text-sm">
          <thead className="sticky top-0 z-10 bg-bg-inset text-xs text-text-muted">
            <tr>
              {onToggleRow ? <th className="w-10 px-3 py-2" scope="col" /> : null}
              {columns.map((column) => (
                <th
                  aria-sort={
                    column.sortable
                      ? sort?.columnID === column.id
                        ? sort.direction === 'asc'
                          ? 'ascending'
                          : 'descending'
                        : 'none'
                      : undefined
                  }
                  className="px-3 py-2 font-medium"
                  key={column.id}
                  scope="col"
                >
                  {column.sortable ? (
                    <button
                      className="inline-flex items-center gap-1 text-left hover:text-text-primary"
                      onClick={() => toggleSort(column)}
                      type="button"
                    >
                      {column.header}
                      <ArrowDownUp aria-hidden="true" size={12} />
                    </button>
                  ) : (
                    <span>{column.header}</span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {visibleRows.map((row) => {
              const id = getRowID(row);
              const selected = selectedIDs.has(id);
              return (
                <tr className={cx('hover:bg-bg-inset', selected && 'bg-accent/10')} key={id}>
                  {onToggleRow ? (
                    <td className="px-3 py-2">
                      <input
                        aria-label={`Select row ${id}`}
                        checked={selected}
                        onChange={() => onToggleRow(id)}
                        type="checkbox"
                      />
                    </td>
                  ) : null}
                  {columns.map((column) => (
                    <td className="truncate px-3 py-2 text-text-secondary" key={column.id}>
                      {column.render(row)}
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
