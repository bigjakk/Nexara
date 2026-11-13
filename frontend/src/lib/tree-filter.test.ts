import { describe, expect, it } from "vitest";
import {
  filterTree,
  normalizeTreeFilter,
  type TreeFilterNode,
} from "./tree-filter";

const tree: TreeFilterNode[] = [
  {
    id: "c1",
    label: "cluster01",
    children: [
      {
        id: "n1",
        label: "pve-01",
        children: [
          { id: "g1", label: "101 linux01" },
          { id: "g2", label: "102 win01" },
        ],
      },
      {
        id: "n2",
        label: "pve-02",
        children: [{ id: "g3", label: "103 Linux02" }],
      },
    ],
  },
  { id: "c2", label: "cluster02" },
];

const sorted = (set: ReadonlySet<string>) => [...set].sort();

describe("filterTree", () => {
  it("filters nothing for a blank box", () => {
    expect(filterTree(tree, normalizeTreeFilter("   "))).toBeNull();
  });

  it.each([
    {
      name: "a leaf keeps its ancestors and holds them open",
      query: "win01",
      visible: ["c1", "g2", "n1"],
      open: ["c1", "n1"],
    },
    {
      name: "matching ignores case and reaches every branch",
      query: " LINUX ",
      visible: ["c1", "g1", "g3", "n1", "n2"],
      open: ["c1", "n1", "n2"],
    },
    {
      name: "a matching branch keeps everything under it, without opening it",
      query: "pve-02",
      visible: ["c1", "g3", "n2"],
      open: ["c1"],
    },
    {
      name: "the number in a label matches too",
      query: "103",
      visible: ["c1", "g3", "n2"],
      open: ["c1", "n2"],
    },
    {
      name: "a root with no children matches by its own name",
      query: "cluster02",
      visible: ["c2"],
      open: [],
    },
    { name: "nothing matches", query: "nas01", visible: [], open: [] },
  ])("$name", ({ query, visible, open }) => {
    const result = filterTree(tree, normalizeTreeFilter(query));
    expect(sorted(result?.visible ?? new Set())).toEqual(visible);
    expect(sorted(result?.open ?? new Set())).toEqual(open);
  });
});
