import type { MouseEvent as ReactMouseEvent, ReactNode } from "react";

import { ArrowDownUp } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { cx } from "./utils";

const virtualRowHeight = 44;
const virtualViewportHeight = 420;
const virtualOverscanRows = 6;
const virtualizeRowThreshold = 120;
const defaultColumnWidth = 180;
const maxColumnWidth = 1200;
const minColumnWidth = 80;
const selectionColumnWidth = 40;
const columnMenuWidth = 220;
const columnMenuMaxHeight = 360;

export type DataTableColumn<T> = {
  id: string;
  header: string;
  render: (row: T) => ReactNode;
  cellClassName?: string;
  defaultWidth?: number;
  headerClassName?: string;
  hideable?: boolean;
  minWidth?: number;
  sortValue?: (row: T) => number | string;
  sortable?: boolean;
  wrap?: boolean;
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
  const [columnWidths, setColumnWidths] = useState<Record<string, number>>({});
  const [hiddenColumnIDs, setHiddenColumnIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const [columnMenu, setColumnMenu] = useState<{
    x: number;
    y: number;
  } | null>(null);
  const activeSort = sort && !hiddenColumnIDs.has(sort.columnID) ? sort : null;
  const virtualWindowKey = `${rows.length}:${activeSort?.columnID ?? ""}:${activeSort?.direction ?? ""}`;
  const [scrollState, setScrollState] = useState<ScrollState>({
    key: virtualWindowKey,
    top: 0,
  });
  const scrollTop = scrollState.key === virtualWindowKey ? scrollState.top : 0;
  const resizeCleanupRef = useRef<(() => void) | null>(null);
  const selectAllRef = useRef<HTMLInputElement>(null);
  const scrollViewportRef = useRef<HTMLDivElement>(null);
  const visibleColumns = useMemo(
    () => columns.filter((column) => !hiddenColumnIDs.has(column.id)),
    [columns, hiddenColumnIDs],
  );
  const visibleRows = useMemo(() => {
    if (!activeSort) {
      return rows;
    }

    const sortColumn = columns.find(
      (column) => column.id === activeSort.columnID,
    );
    if (!sortColumn?.sortValue) {
      return rows;
    }

    return [...rows].sort((left, right) => {
      const leftValue = sortColumn.sortValue?.(left);
      const rightValue = sortColumn.sortValue?.(right);
      const direction = activeSort.direction === "asc" ? 1 : -1;

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
  }, [activeSort, columns, rows]);

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
  const columnCount = visibleColumns.length + (onToggleRow ? 1 : 0);
  const getColumnWidth = (column: DataTableColumn<T>) =>
    columnWidths[column.id] ?? column.defaultWidth ?? defaultColumnWidth;
  const tableWidth = visibleColumns.reduce(
    (total, column) => total + getColumnWidth(column),
    onToggleRow ? selectionColumnWidth : 0,
  );
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

  const openColumnMenu = (event: ReactMouseEvent) => {
    event.preventDefault();
    setColumnMenu({
      x: Math.min(
        event.clientX,
        Math.max(8, window.innerWidth - columnMenuWidth - 8),
      ),
      y: Math.min(
        event.clientY,
        Math.max(8, window.innerHeight - columnMenuMaxHeight - 8),
      ),
    });
  };

  const resetColumnWidth = (columnID: string) => {
    setColumnWidths((current) => {
      if (!(columnID in current)) {
        return current;
      }

      const next = { ...current };
      delete next[columnID];
      return next;
    });
  };

  const resizeColumn = (column: DataTableColumn<T>, width: number) => {
    const nextWidth = Math.min(
      maxColumnWidth,
      Math.max(column.minWidth ?? minColumnWidth, width),
    );
    setColumnWidths((current) => ({ ...current, [column.id]: nextWidth }));
  };

  const startColumnResize = (
    event: ReactMouseEvent,
    column: DataTableColumn<T>,
  ) => {
    if (event.button !== 0) {
      return;
    }

    event.preventDefault();
    event.stopPropagation();

    resizeCleanupRef.current?.();

    const startX = event.clientX;
    const startWidth = getColumnWidth(column);
    const handlePointerMove = (moveEvent: MouseEvent) => {
      resizeColumn(column, startWidth + moveEvent.clientX - startX);
    };
    const handlePointerUp = () => {
      resizeCleanupRef.current?.();
    };
    const cleanup = () => {
      window.removeEventListener("mousemove", handlePointerMove);
      window.removeEventListener("mouseup", handlePointerUp);
      resizeCleanupRef.current = null;
    };

    resizeCleanupRef.current = cleanup;
    window.addEventListener("mousemove", handlePointerMove);
    window.addEventListener("mouseup", handlePointerUp);
  };

  const toggleColumnVisibility = (column: DataTableColumn<T>) => {
    if (column.hideable === false) {
      return;
    }

    setHiddenColumnIDs((current) => {
      const currentlyVisible = columns.filter(
        (candidate) => !current.has(candidate.id),
      ).length;
      const next = new Set(current);

      if (next.has(column.id)) {
        next.delete(column.id);
      } else if (currentlyVisible > 1) {
        next.add(column.id);
      }

      return next;
    });
  };

  const showAllColumns = () => {
    setHiddenColumnIDs(new Set());
  };

  useLayoutEffect(() => {
    if (scrollViewportRef.current) {
      scrollViewportRef.current.scrollTop = 0;
    }
  }, [rows.length, activeSort?.columnID, activeSort?.direction]);

  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate =
        selectedVisibleCount > 0 && selectedVisibleCount < visibleIDs.length;
    }
  }, [selectedVisibleCount, visibleIDs.length]);

  useEffect(() => {
    if (!columnMenu) {
      return undefined;
    }

    const closeColumnMenu = () => {
      setColumnMenu(null);
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeColumnMenu();
      }
    };

    window.addEventListener("click", closeColumnMenu);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("click", closeColumnMenu);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [columnMenu]);

  useEffect(
    () => () => {
      resizeCleanupRef.current?.();
    },
    [],
  );

  if (rows.length === 0 && empty) {
    return <>{empty}</>;
  }

  return (
    <div className="relative overflow-hidden rounded-card border border-border bg-bg-card">
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
          style={{ minWidth: "100%", width: tableWidth }}
        >
          <colgroup>
            {onToggleRow ? (
              <col style={{ width: selectionColumnWidth }} />
            ) : null}
            {visibleColumns.map((column) => (
              <col
                data-column-id={column.id}
                key={column.id}
                style={{ width: getColumnWidth(column) }}
              />
            ))}
          </colgroup>
          <thead
            className="sticky top-0 z-10 bg-bg-inset text-xs text-text-muted"
            onContextMenu={openColumnMenu}
          >
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
              {visibleColumns.map((column) => (
                <th
                  aria-sort={
                    column.sortable
                      ? activeSort?.columnID === column.id
                        ? activeSort.direction === "asc"
                          ? "ascending"
                          : "descending"
                        : "none"
                      : undefined
                  }
                  className={cx(
                    "relative select-none px-3 py-2 font-medium",
                    column.headerClassName,
                  )}
                  key={column.id}
                  scope="col"
                >
                  <div className="pr-2">
                    {column.sortable ? (
                      <button
                        aria-label={sortButtonLabel(
                          column.header,
                          activeSort,
                          column.id,
                        )}
                        className="inline-flex max-w-full items-center gap-1 text-left hover:text-text-primary"
                        onClick={() => toggleSort(column)}
                        type="button"
                      >
                        <span className="truncate">{column.header}</span>
                        <ArrowDownUp
                          aria-hidden="true"
                          className="shrink-0"
                          size={12}
                        />
                      </button>
                    ) : (
                      <span className="block truncate">{column.header}</span>
                    )}
                  </div>
                  <span
                    aria-label={`Resize ${columnLabel(column)} column`}
                    aria-orientation="vertical"
                    aria-valuemax={maxColumnWidth}
                    aria-valuemin={column.minWidth ?? minColumnWidth}
                    aria-valuenow={getColumnWidth(column)}
                    className="group absolute bottom-0 right-0 top-0 z-20 w-4 cursor-col-resize touch-none focus:outline-none"
                    onDoubleClick={(event) => {
                      event.preventDefault();
                      event.stopPropagation();
                      resetColumnWidth(column.id);
                    }}
                    onKeyDown={(event) => {
                      if (event.key === "ArrowLeft") {
                        event.preventDefault();
                        resizeColumn(column, getColumnWidth(column) - 16);
                      }
                      if (event.key === "ArrowRight") {
                        event.preventDefault();
                        resizeColumn(column, getColumnWidth(column) + 16);
                      }
                    }}
                    onMouseDown={(event) => startColumnResize(event, column)}
                    role="separator"
                    tabIndex={0}
                    title="Drag to resize. Double-click to reset."
                  >
                    <span className="pointer-events-none absolute right-1 top-1/2 h-5 -translate-y-1/2 rounded border-r border-border transition-colors group-hover:border-accent group-focus:border-accent" />
                    <span className="pointer-events-none absolute right-2 top-1/2 hidden h-4 -translate-y-1/2 border-r border-border/70 group-hover:block group-focus:block" />
                  </span>
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
                  {visibleColumns.map((column) => (
                    <td
                      className={cx(
                        column.wrap
                          ? "px-3 py-2 align-top text-text-secondary"
                          : "truncate px-3 py-2 text-text-secondary",
                        column.cellClassName,
                      )}
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
      {columnMenu ? (
        <div
          aria-label="Table columns"
          className="fixed z-50 w-56 rounded-card border border-border bg-bg-panel p-2 text-sm shadow-xl"
          onClick={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
          role="menu"
          style={{ left: columnMenu.x, top: columnMenu.y }}
        >
          <div className="px-2 pb-1 text-xs font-semibold uppercase text-text-muted">
            Columns
          </div>
          <div className="max-h-80 space-y-1 overflow-auto">
            {columns.map((column) => {
              const visible = !hiddenColumnIDs.has(column.id);
              const disabled =
                column.hideable === false ||
                (visible && visibleColumns.length <= 1);

              return (
                <label
                  className={cx(
                    "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 hover:bg-bg-inset",
                    disabled && "cursor-not-allowed opacity-50",
                  )}
                  key={column.id}
                >
                  <input
                    checked={visible}
                    disabled={disabled}
                    onChange={() => toggleColumnVisibility(column)}
                    type="checkbox"
                  />
                  <span className="truncate">{columnLabel(column)}</span>
                </label>
              );
            })}
          </div>
          <button
            className="mt-2 w-full rounded border border-border px-2 py-1.5 text-left text-xs text-text-secondary hover:bg-bg-inset hover:text-text-primary"
            onClick={showAllColumns}
            type="button"
          >
            Show all columns
          </button>
        </div>
      ) : null}
    </div>
  );
}

function columnLabel<T>(column: DataTableColumn<T>) {
  return column.header || column.id;
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
