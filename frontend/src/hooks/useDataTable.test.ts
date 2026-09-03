import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useDataTable } from "./useDataTable";
import { byId } from "./useTableSort";
import type { ColumnDef } from "./useColumnLayout";

interface Row {
  id: string;
  name: string;
}
type Key = "name" | "actions";

const COLUMNS: ColumnDef<Row, Key>[] = [
  {
    key: "name",
    label: "Name",
    width: 200,
    sortValue: (r) => r.name,
    cell: (r) => r.name,
  },
  { key: "actions", label: "", width: 80, fixed: true, cell: () => null },
];

const ROWS: Row[] = [
  { id: "2", name: "beta" },
  { id: "1", name: "alpha" },
];

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useDataTable", () => {
  it("sorts on the accessors derived from the column declarations", () => {
    const { result } = renderHook(() =>
      useDataTable("t", COLUMNS, ROWS, byId),
    );
    expect(result.current.rows.map((r) => r.id)).toEqual(["2", "1"]);
    act(() => {
      result.current.toggle("name");
    });
    expect(result.current.rows.map((r) => r.id)).toEqual(["1", "2"]);
    expect(result.current.directionFor("name")).toBe("asc");
    expect(result.current.layout.totalWidth).toBe(280);
  });

  // The whole reason the per-table `const X_SORT = sortAccessorsFrom(COLUMNS)`
  // constants could be deleted. useTableSort memoises on the accessors object,
  // so if the memo here went (or grew a dep that changes each render) every
  // table would re-sort on every render — invisible to the type checker, to
  // the tests above and to any DOM comparison, and only felt as jank on a
  // table that repolls under a few hundred rows.
  it("keeps the sorted array stable across renders", () => {
    const { result, rerender } = renderHook(() =>
      useDataTable("t", COLUMNS, ROWS, byId),
    );
    act(() => {
      result.current.toggle("name");
    });
    const sorted = result.current.rows;
    rerender();
    rerender();
    expect(result.current.rows).toBe(sorted);
  });

  // The caller's half of that contract: an inline rowKey defeats the memo just
  // as surely as a rebuilt accessor object would.
  it("cannot hold the array stable when rowKey is rebuilt per render", () => {
    const { result, rerender } = renderHook(() =>
      useDataTable("t", COLUMNS, ROWS, (r: Row) => r.id),
    );
    act(() => {
      result.current.toggle("name");
    });
    const sorted = result.current.rows;
    rerender();
    expect(result.current.rows).not.toBe(sorted);
  });
});
