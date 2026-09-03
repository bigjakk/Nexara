import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  useColumnLayout,
  sortAccessorsFrom,
  type ColumnDef,
} from "./useColumnLayout";
import { saveColumnLayout } from "@/lib/column-layout";

interface Row {
  id: string;
  a: string;
}
type Key = "a" | "b" | "wide" | "narrow";

const COLUMNS: ColumnDef<Row, Key>[] = [
  { key: "a", label: "A", width: 100, sortValue: (r) => r.a, cell: (r) => r.a },
  { key: "b", label: "B", width: 200, cell: () => null },
  {
    key: "wide",
    label: "Wide",
    width: 160,
    hideBelowMd: true,
    cell: () => null,
  },
  { key: "narrow", label: "Narrow", width: 40, cell: () => null },
];

/**
 * Drive the md breakpoint the hook subscribes to.
 *
 * The hook asks useIsMobile, whose query is `(max-width: 767px)` — so
 * `matches` is the NEGATION of the argument. Get this backwards and every
 * assertion below still runs, against the opposite viewport.
 */
function mockViewport(atLeastMd: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      matches: !atLeastMd,
      media: query,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })),
  );
}

beforeEach(() => {
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useColumnLayout", () => {
  it("starts in the declared order at the declared widths", () => {
    mockViewport(true);
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns.map((c) => c.key)).toEqual([
      "a",
      "b",
      "wide",
      "narrow",
    ]);
    expect(result.current.totalWidth).toBe(500);
    expect(result.current.isCustomized).toBe(false);
  });

  it("restores a stored order and widths", () => {
    mockViewport(true);
    saveColumnLayout("t", {
      order: ["b", "a", "narrow", "wide"],
      widths: { a: 250 },
    });
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns.map((c) => c.key)).toEqual([
      "b",
      "a",
      "narrow",
      "wide",
    ]);
    expect(result.current.widths.a).toBe(250);
    // 250 + 200 + 160 + 40
    expect(result.current.totalWidth).toBe(650);
    expect(result.current.isCustomized).toBe(true);
  });

  it("drops a hideBelowMd column below the breakpoint, and its width with it", () => {
    // The whole point of hideBelowMd over `hidden md:table-cell`: a CSS-hidden
    // column still contributes its width, so the table stays as wide as if it
    // were shown and a phone scrolls sideways anyway.
    mockViewport(false);
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns.map((c) => c.key)).toEqual([
      "a",
      "b",
      "narrow",
    ]);
    expect(result.current.totalWidth).toBe(340);
  });

  it("keeps the hidden column when the viewport is wide", () => {
    mockViewport(true);
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns.map((c) => c.key)).toContain("wide");
  });

  it("clamps a stored width that would wedge a column shut", () => {
    mockViewport(true);
    saveColumnLayout("t", { widths: { a: 2 } });
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.widths.a).toBe(56);
  });

  it("ignores a stored order naming columns this table no longer has", () => {
    mockViewport(true);
    saveColumnLayout("t", { order: ["gone", "b"] });
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns.map((c) => c.key)).toEqual([
      "b",
      "a",
      "wide",
      "narrow",
    ]);
  });

  it("reset restores the defaults and clears storage", () => {
    mockViewport(true);
    saveColumnLayout("t", {
      order: ["narrow", "a", "b", "wide"],
      widths: { a: 300 },
    });
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.isCustomized).toBe(true);
    act(() => {
      result.current.reset();
    });
    expect(result.current.columns.map((c) => c.key)).toEqual([
      "a",
      "b",
      "wide",
      "narrow",
    ]);
    expect(result.current.widths.a).toBe(100);
    expect(result.current.isCustomized).toBe(false);
    expect(localStorage.getItem("nexara-columns:t")).toBeNull();
  });

  it("keeps tables independent", () => {
    mockViewport(true);
    saveColumnLayout("other", { order: ["narrow", "a", "b", "wide"] });
    const { result } = renderHook(() => useColumnLayout("t", COLUMNS));
    expect(result.current.columns[0]?.key).toBe("a");
  });
});

describe("sortAccessorsFrom", () => {
  it("gives every column an accessor, so useTableSort can index any key", () => {
    const accessors = sortAccessorsFrom(COLUMNS);
    for (const col of COLUMNS) {
      expect(accessors[col.key]).toBeTypeOf("function");
    }
  });

  it("uses the column's own sortValue where it has one", () => {
    const accessors = sortAccessorsFrom(COLUMNS);
    expect(accessors.a({ id: "1", a: "x" })).toBe("x");
  });

  it("returns null for a column with no sortValue", () => {
    // Unreachable in practice — DataTableHead offers no sort control for such
    // a column — but it must not throw if something does index it.
    const accessors = sortAccessorsFrom(COLUMNS);
    expect(accessors.b({ id: "1", a: "x" })).toBeNull();
  });
});
