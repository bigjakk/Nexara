import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";

import {
  sameFavorite,
  useIsFavorite,
  useToggleFavorite,
  type FavoriteTarget,
} from "./favorites-queries";
import { useAuthStore } from "@/stores/auth-store";
import type { Favorite } from "@/types/api";

const CLUSTER_A = "aaaaaaaa-0000-0000-0000-000000000001";
const CLUSTER_B = "bbbbbbbb-0000-0000-0000-000000000002";

const listMock = vi.fn();
const postMock = vi.fn();
const deleteMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    list: (path: string) => listMock(path) as unknown,
    post: (path: string, body: unknown) => postMock(path, body) as unknown,
    delete: (path: string) => deleteMock(path) as unknown,
  },
}));

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function favorite(overrides: Partial<Favorite> = {}): Favorite {
  return {
    resource_type: "vm",
    cluster_id: CLUSTER_A,
    cluster_name: "cluster01",
    ref: "101",
    target_id: "11111111-1111-1111-1111-111111111111",
    name: "linux01",
    status: "running",
    vm_kind: "qemu",
    vmid: 101,
    node_name: "pve-01",
    template: false,
    ha_state: "",
    ostype: "",
    config_ostype: "",
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

beforeEach(() => {
  listMock.mockReset();
  listMock.mockResolvedValue([]);
  postMock.mockReset();
  postMock.mockResolvedValue({ message: "Favorite added" });
  deleteMock.mockReset();
  deleteMock.mockResolvedValue({ message: "Favorite removed" });
  useAuthStore.setState({
    user: {
      id: "dddddddd-0000-0000-0000-000000000004",
      email: "u@example.com",
      display_name: "U",
      role: "admin",
    },
    isAuthenticated: true,
  });
});

describe("sameFavorite", () => {
  const vm101: FavoriteTarget = {
    resource_type: "vm",
    cluster_id: CLUSTER_A,
    ref: "101",
  };

  it("matches the same resource", () => {
    expect(sameFavorite(vm101, { ...vm101 })).toBe(true);
  });

  // A node can legitimately be named "101", and every cluster numbers its
  // guests from 100. Comparing anything less than the whole triple would draw
  // one resource's star on another's row.
  it("separates a node named like a VMID", () => {
    expect(sameFavorite(vm101, { ...vm101, resource_type: "node" })).toBe(
      false,
    );
  });

  it("separates the same VMID in another cluster", () => {
    expect(sameFavorite(vm101, { ...vm101, cluster_id: CLUSTER_B })).toBe(
      false,
    );
  });
});

describe("useIsFavorite", () => {
  it("reports a starred resource", async () => {
    listMock.mockResolvedValue([favorite()]);
    const { result } = renderHook(
      () =>
        useIsFavorite({
          resource_type: "vm",
          cluster_id: CLUSTER_A,
          ref: "101",
        }),
      { wrapper: wrapper(makeClient()) },
    );

    await waitFor(() => {
      expect(result.current).toBe(true);
    });
  });

  it("reports an unstarred resource", async () => {
    listMock.mockResolvedValue([favorite()]);
    const { result } = renderHook(
      () =>
        useIsFavorite({
          resource_type: "vm",
          cluster_id: CLUSTER_A,
          ref: "102",
        }),
      { wrapper: wrapper(makeClient()) },
    );

    await waitFor(() => {
      expect(listMock).toHaveBeenCalled();
    });
    expect(result.current).toBe(false);
  });
});

describe("useToggleFavorite", () => {
  it("posts the identity triple when starring", async () => {
    const { result } = renderHook(() => useToggleFavorite(), {
      wrapper: wrapper(makeClient()),
    });

    act(() => {
      result.current.mutate({
        target: { resource_type: "node", cluster_id: CLUSTER_A, ref: "pve-01" },
        favorited: true,
      });
    });

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith("/api/v1/favorites", {
        resource_type: "node",
        cluster_id: CLUSTER_A,
        ref: "pve-01",
      });
    });
  });

  /**
   * The unstar is keyed by (type, cluster, ref) — the same triple the row was
   * stored under. Sending target_id instead would match no row and delete
   * nothing, while still returning 200: the endpoint is idempotent, so the
   * failure would look exactly like a success and the star would come back on
   * the next refetch.
   */
  it("deletes by ref, not by the resolved row id", async () => {
    const { result } = renderHook(() => useToggleFavorite(), {
      wrapper: wrapper(makeClient()),
    });

    act(() => {
      result.current.mutate({
        target: { resource_type: "vm", cluster_id: CLUSTER_A, ref: "101" },
        favorited: false,
      });
    });

    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledTimes(1);
    });
    const path = deleteMock.mock.calls[0]?.[0] as string;
    const query = new URLSearchParams(path.split("?")[1] ?? "");
    expect(query.get("resource_type")).toBe("vm");
    expect(query.get("cluster_id")).toBe(CLUSTER_A);
    expect(query.get("ref")).toBe("101");
  });

  // A node name is caller data and can hold characters that would otherwise
  // change the query it is spliced into.
  it("encodes a node name that needs escaping", async () => {
    const { result } = renderHook(() => useToggleFavorite(), {
      wrapper: wrapper(makeClient()),
    });

    act(() => {
      result.current.mutate({
        target: {
          resource_type: "node",
          cluster_id: CLUSTER_A,
          ref: "pve-01&ref=pve-02",
        },
        favorited: false,
      });
    });

    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledTimes(1);
    });
    const path = deleteMock.mock.calls[0]?.[0] as string;
    const query = new URLSearchParams(path.split("?")[1] ?? "");
    expect(query.getAll("ref")).toEqual(["pve-01&ref=pve-02"]);
  });
});
