import { describe, expect, it } from "vitest";
import {
  clustersSummary,
  nodesNote,
  type ClusterTally,
} from "./cluster-summary";

const node = (pve_version: string, status = "online") => ({
  pve_version,
  status,
});

describe("nodesNote", () => {
  it.each([
    { name: "no nodes", nodes: [], want: undefined },
    {
      name: "one version",
      nodes: [node("pve-manager/9.2.5/abc"), node("9.2.5"), node("9.2.5")],
      want: "3 nodes, all on 9.2.5",
    },
    { name: "a single node", nodes: [node("9.2.5")], want: "1 node on 9.2.5" },
    {
      name: "mixed versions and a node down",
      nodes: [node("9.2.5"), node("9.1.2", "offline")],
      want: "2 nodes on 2 versions, 1 offline",
    },
    {
      name: "no version reported",
      nodes: [node(""), node("")],
      want: "2 nodes",
    },
  ])("$name", ({ nodes, want }) => {
    expect(nodesNote(nodes)).toBe(want);
  });
});

describe("clustersSummary", () => {
  const tally = (over: Partial<ClusterTally> = {}): ClusterTally => ({
    status: "online",
    nodes: 3,
    guests: 40,
    ...over,
  });

  it.each([
    { name: "no clusters", tallies: [], want: undefined },
    {
      name: "all online",
      tallies: [tally(), tally(), tally()],
      want: "3 clusters, 9 nodes, 120 guests, all online",
    },
    {
      name: "one cluster",
      tallies: [tally({ nodes: 1, guests: 1 })],
      want: "1 cluster, 1 node, 1 guest, online",
    },
    {
      name: "the ones that are not online are counted by state",
      tallies: [
        tally(),
        tally({ status: "degraded" }),
        tally({ status: "offline" }),
      ],
      want: "3 clusters, 9 nodes, 120 guests, 1 offline, 1 degraded",
    },
    {
      name: "a count one cluster has not reported is left out",
      tallies: [tally(), tally(), tally({ guests: undefined })],
      want: "3 clusters, 9 nodes, all online",
    },
  ])("$name", ({ tallies, want }) => {
    expect(clustersSummary(tallies)).toBe(want);
  });
});
