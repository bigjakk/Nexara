import { describe, expect, it } from "vitest";
import { upsDevice, upsFeed } from "../power.fixtures";
import { feedName, protectionSummary, protectionView } from "./ups-feeds";

describe("protectionView", () => {
  it.each([
    ["protected", "Protected", "ok"],
    ["not_protected", "Not protected", "warn"],
    ["checked_elsewhere", "Checked elsewhere", "muted"],
    ["node_offline", "Node offline", "muted"],
    ["unchecked", "Not checked yet", "muted"],
  ] as const)("%s reads %s", (protection, label, tone) => {
    expect(protectionView(upsFeed({ protection }))).toMatchObject({ label, tone });
  });
});

describe("protectionSummary", () => {
  it("counts the judged node feeds and names the unprotected once", () => {
    const devices = [
      upsDevice({
        feeds: [
          upsFeed({ protection: "protected" }),
          upsFeed({ id: "f2", node_name: "pve-03", protection: "not_protected" }),
          upsFeed({ id: "f3", node_name: "pve-02", protection: "node_offline" }),
          upsFeed({ id: "f4", kind: "label", label: "Core switch", node_name: "", protection: "" }),
        ],
      }),
      upsDevice({ id: "u2", feeds: [upsFeed({ id: "f5", node_name: "pve-03", protection: "not_protected" })] }),
    ];
    expect(protectionSummary(devices)).toEqual({ judged: 3, protected: 1, unprotected: ["pve-03 (cluster01)"] });
  });

  it("names a label by its label", () => {
    expect(feedName(upsFeed({ kind: "label", label: "Core switch" }))).toBe("Core switch");
  });
});
