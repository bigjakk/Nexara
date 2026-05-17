import { describe, expect, it } from "vitest";
import { filterStorageByContent } from "./storage-filter";
import type { NodeResponse, StorageResponse } from "@/types/api";

function makeStorage(p: Partial<StorageResponse>): StorageResponse {
  return {
    id: p.id ?? "s1",
    cluster_id: "c1",
    node_id: p.node_id ?? "n1",
    storage: p.storage ?? "test-pool",
    type: p.type ?? "dir",
    content: p.content ?? "",
    active: p.active ?? true,
    enabled: p.enabled ?? true,
    shared: p.shared ?? false,
    total: 0,
    used: 0,
    avail: 0,
    last_seen_at: "",
    created_at: "",
    updated_at: "",
  };
}

const node1: NodeResponse = {
  id: "n1",
  cluster_id: "c1",
  name: "pve1",
  address: "",
  status: "online",
  cpu_count: 0,
  cpu_model: "",
  cpu_cores: 0,
  cpu_sockets: 0,
  cpu_threads: 0,
  cpu_mhz: "",
  mem_total: 0,
  disk_total: 0,
  swap_total: 0,
  swap_used: 0,
  swap_free: 0,
  pve_version: "",
  kernel_version: "",
  dns_servers: "",
  dns_search: "",
  timezone: "",
  subscription_status: "",
  subscription_level: "",
  load_avg: "",
  io_wait: 0,
  uptime: 0,
  last_seen_at: "",
  created_at: "",
  updated_at: "",
};

const node2: NodeResponse = { ...node1, id: "n2", name: "pve2" };

describe("filterStorageByContent", () => {
  it("returns empty for undefined storage", () => {
    expect(filterStorageByContent(undefined, "rootdir", [node1], "pve1")).toEqual([]);
  });

  it("filters out storage without the requested content type", () => {
    const list = [
      makeStorage({ storage: "iso-only", content: "iso,backup" }),
      makeStorage({ storage: "ct-ok", content: "rootdir,images", shared: true }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["ct-ok"]);
  });

  it("filters out inactive or disabled storage", () => {
    const list = [
      makeStorage({ storage: "off", content: "rootdir", active: false, shared: true }),
      makeStorage({ storage: "disabled", content: "rootdir", enabled: false, shared: true }),
      makeStorage({ storage: "ok", content: "rootdir", shared: true }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["ok"]);
  });

  it("matches content type as a comma-separated list (no substring false positives)", () => {
    // "rootdir-fake" should not match "rootdir"; "rootdir" should.
    const list = [
      makeStorage({ storage: "fake", content: "rootdir-fake", shared: true }),
      makeStorage({ storage: "real", content: "images,rootdir", shared: true }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["real"]);
  });

  it("hides non-shared storage owned by a different node", () => {
    const list = [
      makeStorage({
        storage: "local-lvm",
        content: "rootdir",
        node_id: "n1",
        shared: false,
      }),
      makeStorage({
        storage: "local-other",
        content: "rootdir",
        node_id: "n2",
        shared: false,
      }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1, node2], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["local-lvm"]);
  });

  it("includes shared storage regardless of node_id", () => {
    const list = [
      makeStorage({
        storage: "shared-nfs",
        content: "rootdir",
        node_id: "n2",
        shared: true,
      }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1, node2], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["shared-nfs"]);
  });

  it("dedupes by storage name (one entry per name)", () => {
    // Shared storage typically appears once per node — we should collapse those.
    const list = [
      makeStorage({ id: "s1", storage: "proxmox-ssd", content: "rootdir", shared: true, node_id: "n1" }),
      makeStorage({ id: "s2", storage: "proxmox-ssd", content: "rootdir", shared: true, node_id: "n2" }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1, node2], "pve1");
    expect(result).toHaveLength(1);
    expect(result[0]?.storage).toBe("proxmox-ssd");
  });

  it("skips the node filter when target node cannot be resolved", () => {
    // When the user hasn't picked a node yet, prefer showing too much over too
    // little — an empty dropdown is more confusing than a few wrong entries.
    const list = [
      makeStorage({ storage: "a", content: "rootdir", node_id: "n2", shared: false }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1, node2], "");
    expect(result.map((s) => s.storage)).toEqual(["a"]);
  });

  it("sorts results by name", () => {
    const list = [
      makeStorage({ storage: "zeta", content: "rootdir", shared: true }),
      makeStorage({ storage: "alpha", content: "rootdir", shared: true }),
      makeStorage({ storage: "mid", content: "rootdir", shared: true }),
    ];
    const result = filterStorageByContent(list, "rootdir", [node1], "pve1");
    expect(result.map((s) => s.storage)).toEqual(["alpha", "mid", "zeta"]);
  });
});
