import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useTableSort, byId, type SortAccessors } from "./useTableSort";

interface Row {
  id: string;
  name: string;
  count: number;
  when: number | null;
}

type Key = "name" | "count" | "when";

const ACCESSORS: SortAccessors<Row, Key> = {
  name: (r) => r.name,
  count: (r) => r.count,
  when: (r) => r.when,
};

function row(
  id: string,
  name: string,
  count: number,
  when: number | null,
): Row {
  return { id, name, count, when };
}

function names(rows: Row[]): string[] {
  return rows.map((r) => r.name);
}

describe("useTableSort", () => {
  it("leaves the rows alone until a column is chosen", () => {
    const rows = [row("a", "b", 2, 1), row("b", "a", 1, 2)];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));
    expect(result.current.rows).toBe(rows);
    expect(result.current.sort).toBeNull();
  });

  it("goes ascending on first click, then toggles", () => {
    const rows = [row("a", "b", 2, 1), row("b", "a", 1, 2)];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));

    act(() => {
      result.current.toggle("name");
    });
    expect(names(result.current.rows)).toEqual(["a", "b"]);
    expect(result.current.directionFor("name")).toBe("asc");

    act(() => {
      result.current.toggle("name");
    });
    expect(names(result.current.rows)).toEqual(["b", "a"]);
    expect(result.current.directionFor("name")).toBe("desc");
    expect(result.current.directionFor("count")).toBeNull();
  });

  it("keeps rows with no value last in BOTH directions", () => {
    // A job that has never run is not the earliest job. Floating a screenful
    // of "Never" to the top on one click buries what was asked for.
    const rows = [
      row("a", "has", 1, 100),
      row("b", "none", 2, null),
      row("c", "also", 3, 50),
    ];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));

    act(() => {
      result.current.toggle("when");
    });
    expect(names(result.current.rows)).toEqual(["also", "has", "none"]);

    act(() => {
      result.current.toggle("when");
    });
    expect(names(result.current.rows)).toEqual(["has", "also", "none"]);
  });

  it("orders numbers numerically, not as text", () => {
    const rows = [row("a", "x", 10, 0), row("b", "y", 9, 0)];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));
    act(() => {
      result.current.toggle("count");
    });
    expect(names(result.current.rows)).toEqual(["y", "x"]);
  });

  it("collates embedded numbers so Job_2 precedes Job_10", () => {
    const rows = [
      row("a", "Job_10", 0, 0),
      row("b", "Job_2", 0, 0),
      row("c", "Job_1", 0, 0),
    ];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));
    act(() => {
      result.current.toggle("name");
    });
    expect(names(result.current.rows)).toEqual(["Job_1", "Job_2", "Job_10"]);
  });

  it("breaks ties on the row key, so a refetch cannot reshuffle them", () => {
    // Duplicate names are the premise of the orphaned-objects view, and the
    // backend's ORDER BY does not disambiguate them. Without a tiebreak the
    // tied rows reorder under the operator on every collector poll.
    const c = row("id-c", "same", 1, 0);
    const a1 = row("id-a", "same", 1, 0);
    const b1 = row("id-b", "same", 1, 0);
    const first = [c, a1, b1];
    // The same rows as the backend might hand them back on the next poll.
    const refetched = [a1, b1, c];

    const a = renderHook(() => useTableSort(first, ACCESSORS, byId));
    act(() => {
      a.result.current.toggle("count");
    });
    const b = renderHook(() => useTableSort(refetched, ACCESSORS, byId));
    act(() => {
      b.result.current.toggle("count");
    });

    const ids = (rows: Row[]) => rows.map((r) => r.id);
    expect(ids(a.result.current.rows)).toEqual(["id-a", "id-b", "id-c"]);
    expect(ids(b.result.current.rows)).toEqual(ids(a.result.current.rows));
  });

  it("does not sort the caller's array in place", () => {
    // `rows` is TanStack Query cache data; mutating it reorders every other
    // consumer's view of the same query.
    const rows = [row("a", "b", 2, 1), row("b", "a", 1, 2)];
    const snapshot = [...rows];
    const { result } = renderHook(() => useTableSort(rows, ACCESSORS, byId));
    act(() => {
      result.current.toggle("name");
    });
    expect(rows).toEqual(snapshot);
  });
});
