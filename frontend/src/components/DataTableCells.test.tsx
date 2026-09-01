import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { DataTableCells } from "./DataTableCells";
import type { ColumnDef, ColumnLayout } from "@/hooks/useColumnLayout";

interface Row {
  id: string;
  name: string;
  note: string;
}

type Key = "name" | "note" | "count";

const COLUMNS: ColumnDef<Row, Key>[] = [
  { key: "name", label: "Name", width: 100, cell: (r) => r.name },
  { key: "note", label: "Note", width: 100, wrap: true, cell: (r) => r.note },
  { key: "count", label: "Count", width: 60, align: "right", cell: () => 7 },
];

function layoutOf(columns: ColumnDef<Row, Key>[]) {
  return {
    columns,
    widths: { name: 100, note: 100, count: 60 },
    totalWidth: 260,
    startResize: () => undefined,
    startReorder: () => undefined,
    dropTarget: null,
    draggingKey: null,
    resizingKey: null,
    isCustomized: false,
    reset: () => undefined,
  } as unknown as ColumnLayout<Row, Key, undefined>;
}

function cellsOf(columns: ColumnDef<Row, Key>[]) {
  const { container } = render(
    <table>
      <tbody>
        <tr>
          <DataTableCells
            row={{ id: "1", name: "web-01", note: "it broke" }}
            layout={layoutOf(columns)}
            ctx={undefined}
          />
        </tr>
      </tbody>
    </table>,
  );
  return [...container.querySelectorAll("td")];
}

describe("DataTableCells", () => {
  it("renders one cell per column, in the layout's order", () => {
    const cells = cellsOf(COLUMNS);
    expect(cells.map((c) => c.textContent)).toEqual(["web-01", "it broke", "7"]);
  });

  it("follows a reordered layout, taking each value with its column", () => {
    // The whole point of the feature: cells are generated from the same
    // ordered list as the headings, so a dragged column cannot leave its
    // values behind under a different heading.
    const reordered = [COLUMNS[2], COLUMNS[0], COLUMNS[1]] as ColumnDef<
      Row,
      Key
    >[];
    expect(cellsOf(reordered).map((c) => c.textContent)).toEqual([
      "7",
      "web-01",
      "it broke",
    ]);
  });

  it("truncates by default, so a long value cannot widen its column", () => {
    expect(cellsOf(COLUMNS)[0]?.className).toContain("truncate");
  });

  it("wraps a column marked wrap, so diagnostic text is not clipped", () => {
    // A cell carrying the only copy of an error must not truncate: `truncate`
    // implies white-space: nowrap, which also defeats break-words, leaving one
    // clipped line with no ellipsis.
    const note = cellsOf(COLUMNS)[1];
    expect(note?.className).not.toContain("truncate");
    expect(note?.className).toContain("whitespace-normal");
    expect(note?.className).toContain("break-words");
  });

  it("right-aligns a column that asked for it", () => {
    expect(cellsOf(COLUMNS)[2]?.className).toContain("text-right");
  });
});
