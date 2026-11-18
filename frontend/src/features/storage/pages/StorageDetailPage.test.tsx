import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeStorage } from "@/test/storage.fixtures";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse, StorageResponse } from "@/types/api";
import { StorageDetailPage } from "./StorageDetailPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

afterEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

const PVE_01 = { id: "n1", name: "pve-01" } as NodeResponse;

const HISTORY_GRANTS = ["view:audit", "view:task", "view:storage"];

// Signs in with the History grants (or `permissions`), mounts the page for a
// pool whose host node is "n1", and returns the user. `nodes` answers the
// cluster's node list, `pools` its storage.
function mountPage(
  shared: boolean,
  nodes: () => Promise<NodeResponse[]>,
  {
    permissions = HISTORY_GRANTS,
    pools = [makeStorage({ id: "s1", storage: "store01", shared })],
  }: { permissions?: string[]; pools?: StorageResponse[] } = {},
) {
  const user = setupUser();
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "U",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
  vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
    const p = String(path);
    if (p.endsWith("/nodes")) return nodes();
    if (p.endsWith("/storage")) return Promise.resolve(pools);
    return Promise.resolve([]);
  });
  vi.mocked(apiClient.get).mockResolvedValue({ pve_version: "9.0.0" });
  vi.mocked(apiClient.page).mockReset();
  vi.mocked(apiClient.page).mockResolvedValue({ items: [], total: 0 });
  renderWithProviders(
    <Routes>
      <Route
        path="/clusters/:clusterId/storage/:storageId"
        element={<StorageDetailPage />}
      />
    </Routes>,
    { router: { initialEntries: ["/clusters/c1/storage/s1"] } },
  );
  return user;
}

// The window the History opens on (a week back) is not what is asserted.
const historyReads = () =>
  vi
    .mocked(apiClient.page)
    .mock.calls.map((call) => String(call[0]).replace(/&start_time=[^&]*/, ""))
    .filter((path) => path.includes("/history"));

// A storage's History is read by its name; one that is not shared is a
// different storage on every node, so the page asks for its host node's.
describe("a storage's History tab", () => {
  it.each([
    [false, "&node=pve-01"],
    [true, ""],
  ])(
    "for a pool with shared=%s reads the storage's rows%s",
    async (shared, node) => {
      const user = mountPage(shared, () => Promise.resolve([PVE_01]));

      await user.click(await screen.findByRole("tab", { name: "History" }));

      // The window the History opens on (a week back) is not what is asserted.
      await waitFor(() => {
        expect(historyReads()).toEqual([
          `/api/v1/clusters/c1/storages/store01/history?limit=50&offset=0${node}`,
        ]);
      });
    },
  );

  // The first read of a pool that is not shared must already be its node's: the
  // node list still loading reads nothing, and a node that cannot be told
  // reads nothing either, rather than every node's storage of that name.
  it("reads nothing while the host node is not known, and only its rows once it is", async () => {
    let release: (nodes: NodeResponse[]) => void = () => undefined;
    const user = mountPage(
      false,
      () =>
        new Promise<NodeResponse[]>((resolve) => {
          release = resolve;
        }),
    );

    await user.click(await screen.findByRole("tab", { name: "History" }));
    expect(historyReads()).toEqual([]);

    release([PVE_01]);
    await waitFor(() => {
      expect(historyReads()).toEqual([
        "/api/v1/clusters/c1/storages/store01/history?limit=50&offset=0&node=pve-01",
      ]);
    });
  });

  it.each([
    ["the node list fails", () => Promise.reject(new Error("down"))],
    // An empty name would drop `node` from the read, and with it every node's rows.
    ["the node has no name", () => Promise.resolve([{ ...PVE_01, name: "" }])],
  ])(
    "says so, and reads nothing, when the host node cannot be told: %s",
    async (_, nodes) => {
      const user = mountPage(false, nodes);

      await user.click(await screen.findByRole("tab", { name: "History" }));

      expect(
        await screen.findByText(/Which node this storage is on is not known/),
      ).toBeInTheDocument();
      expect(historyReads()).toEqual([]);
    },
  );
});

// What the store's routes take: manage:storage to edit it or add content to it,
// delete:storage to remove it, and manage:vm to move VM disks off it
// (Evacuate). Each control is offered only with its grant, and the More menu
// only when one of its items is.
describe("a store's actions", () => {
  const POOLS = [
    makeStorage({ id: "s1", storage: "store01", content: "images,iso,vztmpl" }),
    // Somewhere for Evacuate to move the disks to.
    makeStorage({ id: "s2", storage: "store02", content: "images" }),
  ];
  const CONTROLS = [
    "Edit",
    "Configuration",
    "Upload",
    "Download from URL",
    "More ways to add content",
    "More actions",
  ];

  it.each<[string, string[], string[], string[]]>([
    ["view:storage", [], ["Configuration"], []],
    [
      "manage:storage",
      ["manage:storage"],
      ["Edit", "Upload", "Download from URL", "More ways to add content"],
      [],
    ],
    [
      "delete:storage",
      ["delete:storage"],
      ["Configuration", "More actions"],
      ["Delete"],
    ],
    [
      "manage:vm",
      ["manage:vm"],
      ["Configuration", "More actions"],
      ["Evacuate"],
    ],
  ])("for a role holding %s", async (_, grants, controls, items) => {
    const user = mountPage(false, () => Promise.resolve([PVE_01]), {
      permissions: ["view:storage", ...grants],
      pools: POOLS,
    });

    expect(
      await screen.findByRole("heading", { name: "store01" }),
    ).toBeInTheDocument();
    for (const name of CONTROLS) {
      expect(screen.queryByRole("button", { name }) !== null, name).toBe(
        controls.includes(name),
      );
    }
    if (items.length === 0) return;

    await user.click(screen.getByRole("button", { name: "More actions" }));
    for (const name of ["Evacuate", "Delete"]) {
      expect(screen.queryByRole("menuitem", { name }) !== null, name).toBe(
        items.includes(name),
      );
    }
  });
});
