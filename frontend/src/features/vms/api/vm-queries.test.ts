import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";

import { useDetachDisk } from "./vm-queries";

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const OTHER_CLUSTER = "c0000000-0000-4000-8000-000000000002";
const VM = "d0000000-0000-4000-8000-000000000001";

const postMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    post: (path: string, body: unknown) => postMock(path, body) as unknown,
  },
}));

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

// A detach can free a storage volume — an unusedN or vmstate key, or a
// cloud-init drive — so it leaves the same views stale that a disk move does.
//
// Asserted on the query CACHE, not on the invalidateQueries calls: a key is
// invalidated by any call whose key is a prefix of it (the resource-list call
// on ["clusters", id, "vms"] already reaches this VM's config), so what the
// user gets is which queries end up stale, not which calls were made.
describe("useDetachDisk", () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  it("marks the VM's config, the storage views and the resource lists stale, and nothing else", async () => {
    postMock.mockResolvedValue({ upid: "", status: "completed" });
    const client = makeClient();
    const stale = {
      "the VM's config": ["clusters", CLUSTER, "vms", VM, "config"],
      "the storage list": ["clusters", CLUSTER, "storage"],
      // Where the freed volume is listed.
      "a storage's content": [
        "clusters",
        CLUSTER,
        "storage",
        "store01",
        "content",
      ],
      "the VM list": ["clusters", CLUSTER, "vms"],
      "the container list": ["clusters", CLUSTER, "containers"],
      "the VMID set": ["clusters", CLUSTER, "vmids"],
    };
    const fresh = {
      "the cluster's nodes": ["clusters", CLUSTER, "nodes"],
      "another cluster's storage": ["clusters", OTHER_CLUSTER, "storage"],
    };
    for (const key of [...Object.values(stale), ...Object.values(fresh)]) {
      client.setQueryData(key, {});
    }
    const { result } = renderHook(() => useDetachDisk(), {
      wrapper: wrapper(client),
    });

    act(() => {
      result.current.mutate({ clusterId: CLUSTER, vmId: VM, disk: "unused0" });
    });
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    expect(postMock).toHaveBeenCalledWith(
      `/api/v1/clusters/${CLUSTER}/vms/${VM}/disks/detach`,
      { disk: "unused0" },
    );
    for (const [name, key] of Object.entries(stale)) {
      expect(client.getQueryState(key)?.isInvalidated, name).toBe(true);
    }
    // A hook that invalidated everything would pass every line above.
    for (const [name, key] of Object.entries(fresh)) {
      expect(client.getQueryState(key)?.isInvalidated, name).toBe(false);
    }
  });
});
