import { describe, expect, it } from "vitest";
import { collectSubtreeFolderIds } from "./folder-tree";
import type { VMFolder } from "@/types/api";

function folder(id: string, parent_id: string | null): VMFolder {
  return { id, cluster_id: "c1", parent_id, name: id };
}

describe("collectSubtreeFolderIds", () => {
  const folders: VMFolder[] = [
    folder("root", null),
    folder("a", "root"),
    folder("b", "root"),
    folder("a1", "a"),
    folder("a1x", "a1"),
    folder("other", null),
  ];

  it("collects the folder itself and all descendants", () => {
    expect(collectSubtreeFolderIds(folders, "root")).toEqual(
      new Set(["root", "a", "b", "a1", "a1x"]),
    );
    expect(collectSubtreeFolderIds(folders, "a")).toEqual(
      new Set(["a", "a1", "a1x"]),
    );
  });

  it("returns just the id for a leaf or unknown root", () => {
    expect(collectSubtreeFolderIds(folders, "b")).toEqual(new Set(["b"]));
    expect(collectSubtreeFolderIds(folders, "missing")).toEqual(
      new Set(["missing"]),
    );
  });

  it("does not loop on a corrupt parent cycle", () => {
    const cyclic: VMFolder[] = [folder("x", "y"), folder("y", "x")];
    expect(collectSubtreeFolderIds(cyclic, "x")).toEqual(new Set(["x", "y"]));
  });
});
