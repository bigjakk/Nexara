import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";

import { FavoritesSection } from "./FavoritesSection";
import { useAuthStore } from "@/stores/auth-store";
import { useSidebarStore } from "@/stores/sidebar-store";
import type { Favorite } from "@/types/api";

const USER_A = "aaaaaaaa-0000-0000-0000-000000000001";
const USER_B = "bbbbbbbb-0000-0000-0000-000000000002";
const CLUSTER = "cccccccc-0000-0000-0000-000000000003";

const navigateMock = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual =
    await vi.importActual<typeof import("react-router-dom")>(
      "react-router-dom",
    );
  return { ...actual, useNavigate: () => navigateMock };
});

const listMock = vi.fn();
const deleteMock = vi.fn();
const postMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    list: (path: string) => listMock(path) as unknown,
    delete: (path: string) => deleteMock(path) as unknown,
    post: (path: string, body: unknown) => postMock(path, body) as unknown,
  },
}));

function signIn(id: string) {
  useAuthStore.setState({
    user: { id, email: "u@example.com", display_name: "U", role: "admin" },
    isAuthenticated: true,
  });
}

function favorite(overrides: Partial<Favorite> = {}): Favorite {
  return {
    resource_type: "vm",
    cluster_id: CLUSTER,
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

/**
 * Answers /favorites from the queue and every other collection with [].
 *
 * FavoritesSection also reads the cluster list (a cluster has no status of its
 * own), so a mock that returned favorites for every path would feed nonsense to
 * that query.
 */
function serveFavorites(rows: Favorite[]) {
  listMock.mockImplementation((path: string) =>
    path === "/api/v1/favorites" ? Promise.resolve(rows) : Promise.resolve([]),
  );
}

function renderSection(client?: QueryClient) {
  const qc =
    client ??
    new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  );
  return { client: qc, ...render(<FavoritesSection />, { wrapper }) };
}

beforeEach(() => {
  listMock.mockReset();
  deleteMock.mockReset();
  postMock.mockReset();
  deleteMock.mockResolvedValue({ message: "Favorite removed" });
  navigateMock.mockReset();
  useSidebarStore.setState({ favoritesCollapsed: false });
  signIn(USER_A);
});

describe("FavoritesSection", () => {
  // An empty heading would cost two permanent lines at the top of every
  // sidebar to advertise a feature whose entry point is elsewhere.
  it("renders nothing when nothing is starred", async () => {
    serveFavorites([]);
    const { container } = renderSection();

    await waitFor(() => {
      expect(listMock).toHaveBeenCalledWith("/api/v1/favorites");
    });
    expect(container).toBeEmptyDOMElement();
  });

  it("lists starred clusters, nodes and guests", async () => {
    serveFavorites([
      favorite({
        resource_type: "cluster",
        ref: "",
        name: "cluster01",
        target_id: CLUSTER,
        vmid: 0,
        vm_kind: "",
      }),
      favorite({
        resource_type: "node",
        ref: "pve-01",
        name: "pve-01",
        status: "online",
        vmid: 0,
        vm_kind: "",
      }),
      favorite(),
    ]);
    renderSection();

    expect(await screen.findByText("Favorites")).toBeInTheDocument();
    expect(screen.getByText("cluster01")).toBeInTheDocument();
    expect(screen.getByText("pve-01")).toBeInTheDocument();
    expect(screen.getByText("101 linux01")).toBeInTheDocument();
  });

  // The route segment is the difference between a working link and a 404, and
  // it is derived from vm_kind rather than stored.
  it.each([
    ["qemu", `/inventory/qemu/${CLUSTER}/vm-row-id`],
    ["lxc", `/inventory/lxc/${CLUSTER}/vm-row-id`],
  ])("navigates a %s guest to its own route", async (kind, expected) => {
    serveFavorites([favorite({ vm_kind: kind, target_id: "vm-row-id" })]);
    renderSection();

    await userEvent.click(await screen.findByText("101 linux01"));
    expect(navigateMock).toHaveBeenCalledWith(expected);
  });

  it("navigates a starred node to its cluster's node page", async () => {
    serveFavorites([
      favorite({
        resource_type: "node",
        ref: "pve-01",
        name: "pve-01",
        target_id: "node-row-id",
        vmid: 0,
        vm_kind: "",
      }),
    ]);
    renderSection();

    await userEvent.click(await screen.findByText("pve-01"));
    expect(navigateMock).toHaveBeenCalledWith(
      `/clusters/${CLUSTER}/nodes/node-row-id`,
    );
  });

  // The tree swaps the status dot for a wrench on a node in HA maintenance. The
  // Favorites row sits two inches above it and has to say the same thing, or the
  // same node reads healthy in one panel and drained in the other.
  it("marks a node in maintenance the way the tree does", async () => {
    serveFavorites([
      favorite({
        resource_type: "node",
        ref: "pve-01",
        name: "pve-01",
        status: "online",
        ha_state: "maintenance",
        vmid: 0,
        vm_kind: "",
      }),
    ]);
    renderSection();

    expect(await screen.findByLabelText("Maintenance")).toBeInTheDocument();
  });

  it("folds away and back", async () => {
    serveFavorites([favorite()]);
    renderSection();

    const header = await screen.findByRole("button", { name: /Favorites/ });
    expect(screen.getByText("101 linux01")).toBeInTheDocument();

    await userEvent.click(header);
    expect(screen.queryByText("101 linux01")).not.toBeInTheDocument();
    expect(useSidebarStore.getState().favoritesCollapsed).toBe(true);

    await userEvent.click(header);
    expect(screen.getByText("101 linux01")).toBeInTheDocument();
  });

  /**
   * Nothing clears the QueryClient on logout and it caches for 5 minutes, so a
   * key that did not carry the user id would show the previous account's
   * starred cluster, node and guest names to whoever signs in next on this
   * browser. Same class of leak the sessions list had to be keyed for.
   */
  it("does not serve one user's favorites to the next", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    serveFavorites([favorite({ name: "secret-guest" })]);
    const first = renderSection(client);
    expect(await screen.findByText("101 secret-guest")).toBeInTheDocument();
    first.unmount();

    signIn(USER_B);
    serveFavorites([]);
    const { container } = renderSection(client);

    // Counted per path: the section also reads the cluster list, so a bare
    // call count would not say whether /favorites was re-fetched.
    const favoriteFetches = () =>
      listMock.mock.calls.filter(([p]) => p === "/api/v1/favorites").length;

    await waitFor(() => {
      expect(favoriteFetches()).toBe(2);
    });
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText("101 secret-guest")).not.toBeInTheDocument();
  });
});
