import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { ReactNode } from "react";
import type { SortAccessors } from "@/hooks/useTableSort";
import {
  clampWidth,
  clearColumnLayout,
  loadColumnLayout,
  moveColumn,
  reconcileOrder,
  saveColumnLayout,
} from "@/lib/column-layout";

/**
 * One column, declared once: its heading, its default width, how its cell
 * renders, and (when sortable) what it sorts on.
 *
 * The single declaration is the point. Reordering columns means the header row
 * and the body row must both be generated from the same ordered list — a
 * hand-written sequence of `<td>`s cannot follow a drag, and a header list that
 * moves while the cells do not puts every value under the wrong heading.
 *
 * `Ctx` carries whatever a cell needs that is not on the row itself —
 * permission flags, mutation handlers, a translator. It exists so the column
 * array can stay a module-scope constant (which useTableSort requires of the
 * accessors derived from it) while its cells still close over component state.
 */
export interface ColumnDef<Row, K extends string, Ctx = void> {
  key: K;
  label: string;
  /** Default width in px, used until the user drags this column. */
  width: number;
  /** Right-aligned, for numeric columns. Applied to header and cell alike. */
  align?: "left" | "right";
  /** Classes applied to BOTH the header and the cell. */
  className?: string;
  /**
   * Let this cell wrap instead of truncating to one line.
   *
   * Fixed widths mean a cell clips by default, which is right for a name or a
   * timestamp and wrong for the only copy of an error message: `truncate` sets
   * `white-space: nowrap`, so a wrapped `<p>` becomes one clipped line and
   * `break-words` stops doing anything. Set this on any column whose content
   * is diagnostic rather than a label.
   */
  wrap?: boolean;
  /**
   * Drop this column entirely below the `md` breakpoint.
   *
   * Not a CSS class: a `hidden md:table-cell` column still contributes its
   * width to the table, so the table stays as wide as if it were shown and the
   * narrow viewport scrolls sideways anyway. Removing the column from the
   * layout removes its width with it.
   */
  hideBelowMd?: boolean;
  /**
   * What this column sorts on, for CLIENT-side sorting via useTableSort.
   * Omit to make the column unsortable — an actions column, say.
   */
  sortValue?: (row: Row) => string | number | null;
  /**
   * Force the sort control on or off, independent of `sortValue`.
   *
   * Needed because a column can be sortable without the client knowing how:
   * the Tasks tables order server-side, so they have a sort control and an
   * arrow but no local accessor. Without this they would render as unsortable
   * and the header click would do nothing.
   */
  sortable?: boolean;
  /** Excluded from drag-to-reorder — a trailing actions column that should
   *  stay put. Still resizable. */
  fixed?: boolean;
  cell: (row: Row, ctx: Ctx) => ReactNode;
}

export interface ColumnLayout<Row, K extends string, Ctx> {
  /** The table's columns in the user's order. Render header AND cells from
   *  this, in this order. */
  columns: ColumnDef<Row, K, Ctx>[];
  widths: Record<K, number>;
  /**
   * Sum of the column widths, to be set as the table's own width alongside
   * `table-fixed`. DataTableFrame does both; see it for why the two have to
   * travel together.
   */
  totalWidth: number;
  /** Begin a resize drag from a pointerdown on a column's grip. */
  startResize: (key: K, event: React.PointerEvent) => void;
  /** Begin a reorder drag from a pointerdown on a column's heading. Calls
   *  `onClick` instead if the pointer never passed the drag threshold, which
   *  is how a plain click still sorts. */
  startReorder: (
    key: K,
    event: React.PointerEvent,
    onClick: () => void,
  ) => void;
  /** The column a reorder drag is currently over, and which edge — for the
   *  drop indicator. Null when no drag is in flight. */
  dropTarget: { key: K; side: "before" | "after" } | null;
  /** The column being dragged, for dimming it. */
  draggingKey: K | null;
  resizingKey: K | null;
  /** True once the user has moved or resized anything, so a Reset control can
   *  hide itself until it has something to undo. */
  isCustomized: boolean;
  reset: () => void;
}

/** Tailwind's `md`. Kept in one place so the JS test and the CSS agree. */
const MD_QUERY = "(min-width: 768px)";

/**
 * Whether the viewport is at least `md`, as a subscription.
 *
 * `hideBelowMd` columns are dropped from the layout rather than hidden with a
 * class, so the width they would have taken goes with them — which is the only
 * version of "hide this column on a phone" that actually narrows the table.
 */
function useIsAtLeastMd(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      // matchMedia is absent in some test environments; assume desktop and
      // never subscribe rather than throwing during render.
      if (typeof window.matchMedia !== "function") return () => undefined;
      const mql = window.matchMedia(MD_QUERY);
      mql.addEventListener("change", onChange);
      return () => {
        mql.removeEventListener("change", onChange);
      };
    },
    () =>
      typeof window.matchMedia === "function"
        ? window.matchMedia(MD_QUERY).matches
        : true,
    () => true,
  );
}

/** How far the pointer must travel before a press on a heading becomes a drag
 *  rather than a click. Small enough not to feel sticky, large enough that a
 *  shaky click still sorts. */
const DRAG_THRESHOLD_PX = 5;

/**
 * Per-table column order and widths, persisted to localStorage under
 * `tableId`, with pointer-driven resize and reorder.
 *
 * `tableId` must be unique and stable across releases — it is the storage key,
 * so renaming it silently discards every user's saved layout.
 *
 * `columns` must be a stable reference (a module-scope constant). It is the
 * declaration; this hook only reorders and resizes it.
 */
export function useColumnLayout<Row, K extends string, Ctx = void>(
  tableId: string,
  columns: readonly ColumnDef<Row, K, Ctx>[],
): ColumnLayout<Row, K, Ctx> {
  const atLeastMd = useIsAtLeastMd();
  // The columns this viewport shows at all. Everything below works from this,
  // so a hidden column contributes neither an order slot nor a width.
  const visibleColumns = useMemo(
    () => (atLeastMd ? columns : columns.filter((c) => !c.hideBelowMd)),
    [atLeastMd, columns],
  );
  const declaredKeys = useMemo(
    () => visibleColumns.map((c) => c.key),
    [visibleColumns],
  );

  // Read storage once, on mount. Re-reading on every render would fight the
  // user's in-progress drag.
  const [stored] = useState(() => loadColumnLayout(tableId));

  const [order, setOrder] = useState<K[]>(() =>
    reconcileOrder(declaredKeys, stored.order),
  );

  // Crossing the breakpoint changes which columns exist, so the order has to
  // be reconciled again — otherwise a column hidden on a phone stays missing
  // after a rotate back to desktop.
  const declaredKey = declaredKeys.join(",");
  useEffect(() => {
    setOrder((prev) => {
      const next = reconcileOrder(declaredKeys, prev);
      return next.length === prev.length && next.every((k, i) => k === prev[i])
        ? prev
        : next;
    });
    // declaredKey is the stable identity of declaredKeys; depending on the
    // array itself would re-run on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [declaredKey]);
  const [widths, setWidths] = useState<Record<K, number>>(() => {
    const initial = {} as Record<K, number>;
    for (const col of columns) {
      const saved = stored.widths?.[col.key];
      initial[col.key] =
        typeof saved === "number" ? clampWidth(saved) : col.width;
    }
    return initial;
  });

  const [dropTarget, setDropTarget] = useState<{
    key: K;
    side: "before" | "after";
  } | null>(null);
  const [draggingKey, setDraggingKey] = useState<K | null>(null);
  const [resizingKey, setResizingKey] = useState<K | null>(null);

  // Written by the pointermove handlers and read on pointerup. Refs, not
  // state: a resize drag fires dozens of moves a second and each one would
  // otherwise be a render, and the pointerup handler needs the latest value
  // rather than the one captured when the listener was attached.
  const liveWidth = useRef<number | null>(null);
  const liveDrop = useRef<{ key: K; side: "before" | "after" } | null>(null);

  const persist = useCallback(
    (nextOrder: K[], nextWidths: Record<K, number>) => {
      saveColumnLayout(tableId, {
        order: nextOrder,
        widths: nextWidths,
      });
    },
    [tableId],
  );

  const startResize = useCallback(
    (key: K, event: React.PointerEvent) => {
      // The grip sits inside the heading's press target; without this the same
      // gesture would start a reorder as well.
      event.preventDefault();
      event.stopPropagation();

      const startX = event.clientX;
      const startWidth = widths[key];
      const el = event.currentTarget as HTMLElement;
      // The DOM types say this always exists; jsdom does not implement it, and
      // a component test that clicks a heading would throw before reaching its
      // assertion. The optional call is deliberate.
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
      el.setPointerCapture?.(event.pointerId);
      setResizingKey(key);
      liveWidth.current = startWidth;

      const onMove = (moveEvent: PointerEvent) => {
        const next = clampWidth(startWidth + (moveEvent.clientX - startX));
        liveWidth.current = next;
        setWidths((prev) => ({ ...prev, [key]: next }));
      };
      const onUp = () => {
        el.removeEventListener("pointermove", onMove);
        el.removeEventListener("pointerup", onUp);
        el.removeEventListener("pointercancel", onUp);
        setResizingKey(null);
        const settled = liveWidth.current;
        liveWidth.current = null;
        if (settled !== null) {
          setWidths((prev) => {
            const next = { ...prev, [key]: settled };
            persist(order, next);
            return next;
          });
        }
      };
      el.addEventListener("pointermove", onMove);
      el.addEventListener("pointerup", onUp);
      el.addEventListener("pointercancel", onUp);
    },
    [order, persist, widths],
  );

  const startReorder = useCallback(
    (key: K, event: React.PointerEvent, onClick: () => void) => {
      const column = columns.find((c) => c.key === key);
      const startX = event.clientX;
      const startY = event.clientY;
      const el = event.currentTarget as HTMLElement;
      // The row of headings, resolved once — the drag needs every sibling's
      // box to work out which one the pointer is over.
      const headerRow = el.closest("tr");
      let dragging = false;
      // Travel in ANY direction disqualifies the gesture from being a click,
      // while only horizontal travel starts a reorder. Without the first half,
      // pressing a heading and dragging straight down would sort on release —
      // pointer capture routes that pointerup back here even though the
      // pointer left the header entirely.
      let movedFar = false;
      // See the note in startResize: jsdom lacks setPointerCapture.
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
      el.setPointerCapture?.(event.pointerId);
      liveDrop.current = null;

      const onMove = (moveEvent: PointerEvent) => {
        if (!dragging) {
          if (
            Math.hypot(
              moveEvent.clientX - startX,
              moveEvent.clientY - startY,
            ) >= DRAG_THRESHOLD_PX
          ) {
            movedFar = true;
          }
          if (Math.abs(moveEvent.clientX - startX) < DRAG_THRESHOLD_PX) return;
          // A fixed column can be a drop target but is never itself dragged.
          if (column?.fixed) return;
          dragging = true;
          setDraggingKey(key);
        }
        if (!headerRow) return;
        let found: { key: K; side: "before" | "after" } | null = null;
        for (const cell of headerRow.children) {
          const cellKey = (cell as HTMLElement).dataset["columnKey"] as
            | K
            | undefined;
          if (!cellKey || cellKey === key) continue;
          // A pinned column is not a drop target either. Allowing one would
          // let a data column be dropped "after" the actions column, leaving
          // actions stranded mid-table — the exact thing `fixed` promises
          // cannot happen.
          if (columns.find((c) => c.key === cellKey)?.fixed) continue;
          const box = cell.getBoundingClientRect();
          if (moveEvent.clientX >= box.left && moveEvent.clientX <= box.right) {
            found = {
              key: cellKey,
              side:
                moveEvent.clientX < box.left + box.width / 2
                  ? "before"
                  : "after",
            };
            break;
          }
        }
        liveDrop.current = found;
        setDropTarget(found);
      };

      const detach = () => {
        el.removeEventListener("pointermove", onMove);
        el.removeEventListener("pointerup", onUp);
        el.removeEventListener("pointercancel", onCancel);
      };

      const onUp = () => {
        detach();
        const drop = liveDrop.current;
        liveDrop.current = null;
        setDropTarget(null);
        setDraggingKey(null);
        if (!dragging) {
          // Only a press that stayed put counts as a click.
          if (!movedFar) onClick();
          return;
        }
        if (!drop) return;
        setOrder((prev) => {
          const next = moveColumn(prev, key, drop.key, drop.side);
          persist(next, widths);
          return next;
        });
      };

      // A cancel is NOT a click. The browser fires pointercancel when it takes
      // the pointer over for something else — most often a touch that started
      // on a heading and turned into a vertical scroll. The threshold only
      // measures horizontal travel, so such a gesture never sets `dragging`,
      // and routing cancel through onUp would sort the table out from under
      // someone who was only trying to scroll it.
      const onCancel = () => {
        detach();
        liveDrop.current = null;
        setDropTarget(null);
        setDraggingKey(null);
      };

      el.addEventListener("pointermove", onMove);
      el.addEventListener("pointerup", onUp);
      el.addEventListener("pointercancel", onCancel);
    },
    [columns, persist, widths],
  );

  const reset = useCallback(() => {
    clearColumnLayout(tableId);
    setOrder([...declaredKeys]);
    const defaults = {} as Record<K, number>;
    for (const col of columns) defaults[col.key] = col.width;
    setWidths(defaults);
  }, [columns, declaredKeys, tableId]);

  const orderedColumns = useMemo(() => {
    const byKey = new Map(visibleColumns.map((c) => [c.key, c]));
    return order
      .map((k) => byKey.get(k))
      .filter((c): c is ColumnDef<Row, K, Ctx> => c !== undefined);
  }, [visibleColumns, order]);

  const totalWidth = useMemo(
    () => orderedColumns.reduce((sum, c) => sum + widths[c.key], 0),
    [orderedColumns, widths],
  );

  const isCustomized = useMemo(() => {
    if (order.length !== declaredKeys.length) return true;
    if (order.some((k, i) => k !== declaredKeys[i])) return true;
    return columns.some((c) => widths[c.key] !== c.width);
  }, [columns, declaredKeys, order, widths]);

  return {
    columns: orderedColumns,
    widths,
    totalWidth,
    startResize,
    startReorder,
    dropTarget,
    draggingKey,
    resizingKey,
    isCustomized,
    reset,
  };
}

/**
 * Derive useTableSort's accessors from the column declarations, so a column's
 * heading, width, cell and sort key are written once rather than kept in step
 * across two structures.
 *
 * You almost certainly want useDataTable instead — it calls this for you, and
 * memoises the result, which useTableSort requires. Call it directly only for
 * a table whose accessors are hand-written rather than derived.
 *
 * Columns with no `sortValue` get an accessor that returns null; it is never
 * reached, because DataTableHead only offers a sort control for columns that
 * declare one.
 */
export function sortAccessorsFrom<Row, K extends string, Ctx>(
  columns: readonly ColumnDef<Row, K, Ctx>[],
): SortAccessors<Row, K> {
  const accessors = {} as SortAccessors<Row, K>;
  for (const col of columns) {
    accessors[col.key] = col.sortValue ?? (() => null);
  }
  return accessors;
}
