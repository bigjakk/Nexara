import { describe, it, expect } from "vitest";
import { auditClusterLabel } from "./audit-cluster";

describe("auditClusterLabel", () => {
  it.each([
    ["a cluster", "cluster01", false, "cluster01"],
    ["no cluster", "", false, "System"],
    ["a removed cluster", "cluster01", true, "cluster01 (removed)"],
    ["a removed cluster whose name was not kept", "", true, "Removed cluster"],
  ])("labels %s", (_what, cluster_name, cluster_removed, want) => {
    expect(auditClusterLabel({ cluster_name, cluster_removed }, "System")).toBe(
      want,
    );
  });
});
