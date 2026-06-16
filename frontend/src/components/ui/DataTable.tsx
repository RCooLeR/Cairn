import type { ReactNode } from "react";

import { ArrowDownUp } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { cx } from "./utils";

const virtualRowHeight = 44;
const virtualViewportHeight = 420;
const virtualOverscanRows = 6;
const virtualizeRowThreshold = 120;

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
  ariaLabel?: string;
  selectedIDs?: Set<string>;
  onToggleRow?: (id: string) => void;
  onToggleAllRows?: (ids: string[], selected: boolean) => void;
  rowLabel?: (row: T) => string;
  bulkActions?: ReactNode;
  empty?: ReactNode;
};

type SortState = {
  columnID: string;
  direction: "asc" | "desc";
};

type ScrollState = {
  key: string;
  top: number;
};

export function DataTable<T>({
  ariaLabel = "Data table",
  bulkActions,
  columns,
  empty,
  getRowID,
  onToggleRow,
  onToggleAllRows,
  rowLabel,
  rows,
  selectedIDs = new Set<string>(),
}: DataTableProps<T>) {
  const hasSelection = selectedIDs.size > 0;
  const [sort, setSort] = useState<SortState | null>(null);
  const virtualWindowKey = `${rows.length}:${sort?.columnID ?? ""}:${sort?.direction ?? ""}`;
  const [scrollState, setScrollState] = useState<ScrollState>({
    key: virtualWindowKey,
    top: 0,
  });
  const scrollTop = scrollState.key === virtualWindowKey ? scrollState.top : 0;
  const selectAllRef = useRef<HTMLInputElement>(null);
  const scrollViewportRef = useRef<HTMLDivElement>(null);
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
      const direction = sort.direction === "asc" ? 1 : -1;

      if (typeof leftValue === "number" && typeof rightValue === "number") {
        return (leftValue - rightValue) * direction;
      }

      return (
        String(leftValue).localeCompare(String(rightValue), undefined, {
          numeric: true,
          sensitivity: "base",
        }) * direction
      );
    });
  }, [columns, rows, sort]);

  const toggleSort = (column: DataTableColumn<T>) => {
    if (!column.sortable || !column.sortValue) {
      return;
    }

    setSort((current) =>
      current?.columnID === column.id
        ? {
            columnID: column.id,
            direction: current.direction === "asc" ? "desc" : "asc",
          }
        : { columnID: column.id, direction: "asc" },
    );
  };
  const virtualized = visibleRows.length > virtualizeRowThreshold;
  const visibleCount = Math.ceil(virtualViewportHeight / virtualRowHeight);
  const virtualWindowSize = visibleCount + virtualOverscanRows * 2;
  const maxVirtualStart = Math.max(0, visibleRows.length - virtualWindowSize);
  const virtualStart = virtualized
    ? Math.min(
        Math.max(
          0,
          Math.floor(scrollTop / virtualRowHeight) - virtualOverscanRows,
        ),
        maxVirtualStart,
      )
    : 0;
  const virtualEnd = virtualized
    ? Math.min(visibleRows.length, virtualStart + virtualWindowSize)
    : visibleRows.length;
  const virtualRows = virtualized
    ? visibleRows.slice(virtualStart, virtualEnd)
    : visibleRows;
  const topPadding = virtualized ? virtualStart * virtualRowHeight : 0;
  const bottomPadding = virtualized
    ? Math.max(0, (visibleRows.length - virtualEnd) * virtualRowHeight)
    : 0;
  const columnCount = columns.length + (onToggleRow ? 1 : 0);
  const visibleIDs = useMemo(
    () => visibleRows.map(getRowID),
    [getRowID, visibleRows],
  );
  const selectedVisibleCount = visibleIDs.filter((id) =>
    selectedIDs.has(id),
  ).length;
  const canToggleAll = Boolean(
    onToggleAllRows && onToggleRow && visibleIDs.length > 0,
  );
  const allVisibleSelected =
    canToggleAll && selectedVisibleCount === visibleIDs.length;

  useLayoutEffect(() => {
    if (scrollViewportRef.current) {
      scrollViewportRef.current.scrollTop = 0;
    }
  }, [rows.length, sort?.columnID, sort?.direction]);

  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate =
        selectedVisibleCount > 0 && selectedVisibleCount < visibleIDs.length;
    }
  }, [selectedVisibleCount, visibleIDs.length]);

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
      <div
        className="overflow-auto"
        onScroll={(event) => {
          if (virtualized) {
            setScrollState({
              key: virtualWindowKey,
              top: event.currentTarget.scrollTop,
            });
          }
        }}
        ref={scrollViewportRef}
        style={{ maxHeight: virtualViewportHeight }}
      >
        <table
          aria-label={ariaLabel}
          aria-colcount={columnCount}
          aria-rowcount={visibleRows.length}
          className="min-w-full table-fixed text-left text-sm"
        >
          <thead className="sticky top-0 z-10 bg-bg-inset text-xs text-text-muted">
            <tr>
              {onToggleRow ? (
                <th
                  aria-label="Selection"
                  className="w-10 px-3 py-2"
                  scope="col"
                >
                  {onToggleAllRows ? (
                    <input
                      aria-label={
                        allVisibleSelected
                          ? "Clear row selection"
                          : "Select all rows"
                      }
                      checked={allVisibleSelected}
                      disabled={visibleIDs.length === 0}
                      onChange={() =>
                        onToggleAllRows(visibleIDs, !allVisibleSelected)
                      }
                      ref={selectAllRef}
                      type="checkbox"
                    />
                  ) : null}
                </th>
              ) : null}
              {columns.map((column) => (
                <th
                  aria-sort={
                    column.sortable
                      ? sort?.columnID === column.id
                        ? sort.direction === "asc"
                          ? "ascending"
                          : "descending"
                        : "none"
                      : undefined
                  }
                  className="px-3 py-2 font-medium"
                  key={column.id}
                  scope="col"
                >
                  {column.sortable ? (
                    <button
                      aria-label={sortButtonLabel(
                        column.header,
                        sort,
                        column.id,
                      )}
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
            {topPadding > 0 ? (
              <tr aria-hidden="true">
                <td
                  colSpan={columnCount}
                  style={{ height: topPadding, padding: 0 }}
                />
              </tr>
            ) : null}
            {virtualRows.map((row) => {
              const id = getRowID(row);
              const selected = selectedIDs.has(id);
              const label = rowLabel?.(row) || id;
              return (
                <tr
                  className={cx(
                    "hover:bg-bg-inset",
                    selected && "bg-accent/10",
                  )}
                  key={id}
                >
                  {onToggleRow ? (
                    <td className="px-3 py-2">
                      <input
                        aria-label={`Select ${label}`}
                        checked={selected}
                        onChange={() => onToggleRow(id)}
                        type="checkbox"
                      />
                    </td>
                  ) : null}
                  {columns.map((column) => (
                    <td
                      className="truncate px-3 py-2 text-text-secondary"
                      key={column.id}
                    >
                      {column.render(row)}
                    </td>
                  ))}
                </tr>
              );
            })}
            {bottomPadding > 0 ? (
              <tr aria-hidden="true">
                <td
                  colSpan={columnCount}
                  style={{ height: bottomPadding, padding: 0 }}
                />
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function sortButtonLabel(
  header: string,
  sort: SortState | null,
  columnID: string,
) {
  if (sort?.columnID !== columnID) {
    return `Sort by ${header}, not sorted`;
  }
  return `Sort by ${header}, sorted ${sort.direction === "asc" ? "ascending" : "descending"}`;
}
