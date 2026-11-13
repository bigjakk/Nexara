import { describe, expect, it } from "vitest";
import { makeStorage } from "@/test/storage.fixtures";
import {
  contentBreakdown,
  contentList,
  storageSummary,
  storageTotals,
  storeGroups,
  storeRows,
} from "./store-rows";

const GIB = 1024 ** 3;
const NODES = new Map([
  ["n1", "pve-01"],
  ["n2", "pve-02"],
]);

// A shared store is reported once per node; a local one is a store per node.
const SHARED = [
  makeStorage({ id: "a1", node_id: "n1", storage: "store01", shared: true }),
  makeStorage({ id: "a2", node_id: "n2", storage: "store01", shared: true }),
].map((pool) => ({ ...pool, total: 100 * GIB, used: 80 * GIB }));
const LOCAL = [
  makeStorage({ id: "b2", node_id: "n2", storage: "local" }),
  makeStorage({ id: "b1", node_id: "n1", storage: "local" }),
].map((pool) => ({ ...pool, total: 50 * GIB, used: 10 * GIB }));

describe("storeRows", () => {
  it("keeps a shared store once and a local store per node, shared first then by node", () => {
    const rows = storeRows([...LOCAL, ...SHARED], NODES);
    expect(rows.map((r) => [r.pool.id, r.nodeName, r.level])).toEqual([
      ["a1", "", "warn"],
      ["b1", "pve-01", "ok"],
      ["b2", "pve-02", "ok"],
    ]);
  });
});

// The list shows each storage definition once, as Proxmox's Datacenter >
// Storage does: a local store stands for its fullest node and its worst state.
describe("storeGroups", () => {
  it("lists a local store once with its nodes, by cluster, shared first", () => {
    const pools = [
      ...SHARED,
      // local: the node with more used stands for the store.
      makeStorage({
        id: "b1",
        node_id: "n1",
        storage: "local",
        total: 50 * GIB,
        used: 10 * GIB,
      }),
      makeStorage({
        id: "b2",
        node_id: "n2",
        storage: "local",
        total: 50 * GIB,
        used: 40 * GIB,
      }),
      // local-lvm: unreachable on one node is unreachable.
      makeStorage({
        id: "d1",
        node_id: "n1",
        storage: "local-lvm",
        total: 50 * GIB,
        used: 25 * GIB,
      }),
      makeStorage({
        id: "d2",
        node_id: "n2",
        storage: "local-lvm",
        active: false,
      }),
      // Restricted to n2: Proxmox reports it on n1 too, disabled there.
      makeStorage({
        id: "f1",
        node_id: "n1",
        storage: "store02",
        enabled: false,
      }),
      makeStorage({ id: "f2", node_id: "n2", storage: "store02" }),
      // The same id in another cluster is another store.
      makeStorage({ id: "e1", cluster_id: "c2", storage: "local" }),
    ];
    const groups = storeGroups(storeRows(pools, NODES));
    expect(
      groups.map((g) => [
        `${g.lead.pool.cluster_id}/${g.lead.pool.storage}`,
        g.rows.map((r) => r.pool.id),
        g.lead.pool.id,
        g.fullest?.pool.id ?? null,
        g.state,
      ]),
    ).toEqual([
      ["c1/store01", ["a1"], "a1", null, "active"],
      ["c1/local", ["b1", "b2"], "b2", "b2", "active"],
      ["c2/local", ["e1"], "e1", null, "active"],
      // Only one node reports a size, so none is the fullest.
      ["c1/local-lvm", ["d1", "d2"], "d1", null, "inactive"],
      ["c1/store02", ["f2"], "f2", null, "active"],
    ]);
  });
});

describe("storageSummary", () => {
  it.each([
    ["no stores", [], 1, 0, undefined],
    [
      "counts a shared store's space once, and a local store once with every node's space",
      [...SHARED, ...LOCAL],
      1,
      0,
      "2 stores on 1 cluster, 100.0 GiB of 200.0 GiB used, 1 near full",
    ],
    [
      "leaves out what is zero",
      LOCAL.slice(0, 1),
      2,
      0,
      "1 store on 2 clusters, 10.0 GiB of 50.0 GiB used",
    ],
    [
      "says an enabled store Proxmox cannot reach, and no size when none reports one",
      [
        makeStorage({ storage: "store01", active: false }),
        makeStorage({ id: "s2", storage: "store02", enabled: false }),
      ],
      1,
      0,
      "2 stores on 1 cluster, 1 inactive",
    ],
    [
      "says a cluster that could not be read apart from the ones the stores are on",
      LOCAL.slice(0, 1),
      1,
      1,
      "1 store on 1 cluster, 10.0 GiB of 50.0 GiB used, 1 cluster could not be read",
    ],
  ])("%s", (_, pools, clusters, unread, summary) => {
    const totals = storageTotals(storeRows(pools, NODES));
    expect(storageSummary(totals, clusters, unread)).toBe(summary);
  });
});

// A set of stores that reports no capacity (all inactive) has no share to
// draw: null leaves the bar's track empty, where 0 or NaN would draw a claim.
describe("storageTotals", () => {
  it("has no percentage when the stores report no capacity", () => {
    const empty = storageTotals(
      storeRows(
        LOCAL.slice(0, 1).map((pool) => ({
          ...pool,
          total: 0,
          used: 0,
          avail: 0,
        })),
        NODES,
      ),
    );
    expect(empty.percent).toBeNull();
    expect(storageTotals(storeRows(LOCAL.slice(0, 1), NODES)).percent).toBe(20);
  });
});

describe("content kinds in words", () => {
  it("lists a store's kinds in one order, and counts a list of items", () => {
    expect(contentList("backup,iso,images,none")).toBe(
      "Disk images, ISO images, backups",
    );
    expect(contentList("")).toBe("");
    expect(contentBreakdown(["iso", "backup", "iso", "custom"])).toBe(
      "2 ISO images, 1 backup, 1 custom",
    );
  });
});
