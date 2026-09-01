import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  MAX_COLUMN_WIDTH,
  MIN_COLUMN_WIDTH,
  clampWidth,
  clearColumnLayout,
  loadColumnLayout,
  moveColumn,
  reconcileOrder,
  saveColumnLayout,
} from "./column-layout";

beforeEach(() => {
  localStorage.clear();
});

describe("reconcileOrder", () => {
  const declared = ["a", "b", "c"] as const;

  it("uses the declared order when nothing is stored", () => {
    expect(reconcileOrder(declared, undefined)).toEqual(["a", "b", "c"]);
    expect(reconcileOrder(declared, [])).toEqual(["a", "b", "c"]);
  });

  it("honours a stored order", () => {
    expect(reconcileOrder(declared, ["c", "a", "b"])).toEqual(["c", "a", "b"]);
  });

  it("appends a column the stored order has never seen", () => {
    // A release adds a column. It must appear, not vanish because an old
    // layout in localStorage does not mention it.
    expect(reconcileOrder(declared, ["c", "a"])).toEqual(["c", "a", "b"]);
  });

  it("drops a stored key the table no longer has", () => {
    // A release renames or removes a column; the stale key must not survive
    // into the render, where it would resolve to no column at all.
    expect(reconcileOrder(declared, ["c", "gone", "a", "b"])).toEqual([
      "c",
      "a",
      "b",
    ]);
  });

  it("ignores a duplicated stored key", () => {
    // Hand-edited or corrupted storage must not render one column twice and
    // leave another off the table.
    expect(reconcileOrder(declared, ["a", "a", "b", "c"])).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  it("always returns every declared column exactly once", () => {
    for (const stored of [["c"], ["x", "y"], ["b", "b", "b"], []]) {
      const result = reconcileOrder(declared, stored);
      expect([...result].sort()).toEqual(["a", "b", "c"]);
    }
  });
});

describe("moveColumn", () => {
  const order = ["a", "b", "c", "d"] as const;

  it("moves a column before a target", () => {
    expect(moveColumn(order, "d", "b", "before")).toEqual([
      "a",
      "d",
      "b",
      "c",
    ]);
  });

  it("moves a column after a target", () => {
    expect(moveColumn(order, "a", "c", "after")).toEqual(["b", "c", "a", "d"]);
  });

  it("handles a leftward move without an off-by-one", () => {
    // Removing the dragged key first shifts every later index; the insert
    // point has to be computed after that removal, not before.
    expect(moveColumn(order, "c", "a", "before")).toEqual([
      "c",
      "a",
      "b",
      "d",
    ]);
  });

  it("is a no-op when dropped on itself", () => {
    expect(moveColumn(order, "b", "b", "after")).toEqual(["a", "b", "c", "d"]);
  });

  it("is a no-op against an unknown target", () => {
    expect(moveColumn(order, "b", "zzz" as "a", "after")).toEqual([
      "a",
      "b",
      "c",
      "d",
    ]);
  });

  it("never loses or duplicates a column", () => {
    for (const key of order) {
      for (const target of order) {
        for (const side of ["before", "after"] as const) {
          const result = moveColumn(order, key, target, side);
          expect([...result].sort()).toEqual(["a", "b", "c", "d"]);
        }
      }
    }
  });
});

describe("clampWidth", () => {
  it("keeps a column wide enough to read", () => {
    // Dragging left past the edge must not wedge a column shut, leaving no
    // grip to drag back out.
    expect(clampWidth(-500)).toBe(MIN_COLUMN_WIDTH);
    expect(clampWidth(0)).toBe(MIN_COLUMN_WIDTH);
  });

  it("caps a runaway drag", () => {
    expect(clampWidth(99999)).toBe(MAX_COLUMN_WIDTH);
  });

  it("rounds to whole pixels", () => {
    expect(clampWidth(120.7)).toBe(121);
  });
});

describe("storage", () => {
  it("round-trips a layout", () => {
    saveColumnLayout("t1", { order: ["b", "a"], widths: { a: 120 } });
    expect(loadColumnLayout("t1")).toEqual({
      order: ["b", "a"],
      widths: { a: 120 },
    });
  });

  it("keeps tables independent", () => {
    saveColumnLayout("t1", { order: ["b", "a"] });
    saveColumnLayout("t2", { order: ["z"] });
    expect(loadColumnLayout("t1").order).toEqual(["b", "a"]);
    expect(loadColumnLayout("t2").order).toEqual(["z"]);
  });

  it("clears a layout", () => {
    saveColumnLayout("t1", { order: ["b", "a"] });
    clearColumnLayout("t1");
    expect(loadColumnLayout("t1")).toEqual({});
  });

  it("returns empty for an absent key", () => {
    expect(loadColumnLayout("never-written")).toEqual({});
  });

  it("survives malformed stored JSON", () => {
    localStorage.setItem("nexara-columns:t1", "{not json");
    expect(loadColumnLayout("t1")).toEqual({});
  });

  it("survives a stored non-object", () => {
    localStorage.setItem("nexara-columns:t1", '"a string"');
    expect(loadColumnLayout("t1")).toEqual({});
  });

  it("discards non-string keys and non-numeric widths", () => {
    // Storage is user-editable; a hand-edited file must not put a NaN width
    // into a style attribute or a number into the order.
    localStorage.setItem(
      "nexara-columns:t1",
      JSON.stringify({ order: ["a", 7, null], widths: { a: "wide", b: 90 } }),
    );
    expect(loadColumnLayout("t1")).toEqual({
      order: ["a"],
      widths: { b: 90 },
    });
  });

  it("does not throw when localStorage is unavailable", () => {
    // Private windows and blocked site data make the accessor itself throw.
    const getItem = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("blocked");
      });
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("blocked");
      });
    expect(loadColumnLayout("t1")).toEqual({});
    expect(() => {
      saveColumnLayout("t1", { order: ["a"] });
    }).not.toThrow();
    getItem.mockRestore();
    setItem.mockRestore();
  });
});

describe("column declarations across the adopted tables", () => {
  it("never hides a column with a CSS class", async () => {
    // `hidden md:table-cell` removes a column's content but keeps its width,
    // so the table stays as wide as if it were shown. hideBelowMd drops the
    // column from the layout instead, taking its width with it. A class-based
    // hide would silently reintroduce the mobile overflow.
    const modules = await Promise.all([
      import("@/components/layout/activity-column-defs"),
      import("@/features/tasks/lib/task-column-defs"),
    ]);
    const columns = [
      ...modules[0].ACTIVITY_COLUMN_DEFS,
      ...modules[1].TASK_COLUMNS_WITH_VM,
    ];
    for (const col of columns) {
      expect(col.className ?? "").not.toContain("hidden");
    }
  });
});
