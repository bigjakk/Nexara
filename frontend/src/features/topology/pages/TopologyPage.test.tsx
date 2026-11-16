import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
import { upsDevice, upsFeed } from "@/features/power/power.fixtures";
import { TopologyPage } from "./TopologyPage";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
// One map for every render: the canvas re-injects live metrics whenever it changes.
vi.mock("@/hooks/useMetrics", () => {
  const none = new Map();
  return { useDashboardMetrics: () => none };
});

// jsdom has no DOMMatrixReadOnly. React Flow reads the canvas zoom from one
// when a change of direction moves a node's handles to another side.
vi.stubGlobal(
  "DOMMatrixReadOnly",
  class {
    m22 = 1;
  },
);

/** A mouse event as d3-drag reads it: with the window as its view. */
function mouse(target: EventTarget, type: string, x: number, y: number) {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 });
  Object.defineProperty(event, "view", { value: window });
  act(() => {
    target.dispatchEvent(event);
  });
}

const placeOf = (name: string) => (screen.getByText(name).closest(".react-flow__node") as HTMLElement).style.transform;

const upsListed = () => vi.mocked(apiClient.list).mock.calls.some(([p]) => String(p).includes("ups-devices"));

describe("TopologyPage power overlay", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) return Promise.resolve([makeCluster({ name: "cluster01" })]);
      if (p.endsWith("/nodes")) return Promise.resolve([makeNode({ id: "n1", name: "pve-01" }), makeNode({ id: "n2", name: "pve-02" })]);
      if (p.includes("ups-devices")) return Promise.resolve([upsDevice({ feeds: [upsFeed({ protection: "not_protected" })] })]);
      return Promise.resolve([]);
    });
  });

  it("draws each UPS and marks the hosts no upsmon would shut down once Power is on", async () => {
    useAuthStore.setState({ globalPermissions: ["view:ups"], permissions: ["view:ups"] });
    const user = setupUser();
    renderWithProviders(<TopologyPage />);

    await user.click(await screen.findByRole("switch", { name: "Power" }));

    expect(await screen.findByText("ups01")).toBeInTheDocument();
    expect(screen.getByText("No upsmon")).toBeInTheDocument();

    // A re-layout (here a new direction; the clusters refetch does it every
    // 30 s) drops the overlay, which must be applied again after it.
    await user.click(screen.getByRole("button", { name: "Left-right" }));
    expect(await screen.findByText("ups01")).toBeInTheDocument();
    expect(screen.getByText("No upsmon")).toBeInTheDocument();
  });

  it("offers no Power overlay, and lists no UPS, without view:ups", async () => {
    useAuthStore.setState({ globalPermissions: [], permissions: [] });
    renderWithProviders(<TopologyPage />);

    expect(await screen.findByText("pve-01")).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Power" })).not.toBeInTheDocument();
    expect(upsListed()).toBe(false);
  });
});

describe("TopologyPage arrangement", () => {
  // Every refetch lays the graph out again, and that put a dragged node back
  // within 30 s. It stays where it was left until Reset layout.
  it("keeps a dragged node in place over a refetch until the layout is reset", async () => {
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) return Promise.resolve([makeCluster({ name: "cluster01" })]);
      if (p.endsWith("/nodes")) return Promise.resolve([makeNode({ id: "n1", name: "pve-01" })]);
      return Promise.resolve([]);
    });
    const user = setupUser();
    const { queryClient } = renderWithProviders(<TopologyPage />);
    const node = (await screen.findByText("pve-01")).closest(".react-flow__node") as HTMLElement;
    const laidOut = placeOf("pve-01");

    mouse(node, "mousedown", 10, 10);
    // The first move starts the drag, the second moves the node.
    mouse(document, "mousemove", 100, 100);
    mouse(document, "mousemove", 200, 300);
    mouse(document, "mouseup", 200, 300);
    // Reset layout is offered once a drag has ended, and the node has moved.
    const reset = await screen.findByRole("button", { name: "Reset layout" });
    await waitFor(() => {
      expect(placeOf("pve-01")).not.toBe(laidOut);
    });
    const dragged = placeOf("pve-01");

    await act(() => queryClient.invalidateQueries());
    expect(vi.mocked(apiClient.list).mock.calls.filter(([p]) => String(p).endsWith("/nodes")).length).toBeGreaterThan(1);
    expect(placeOf("pve-01")).toBe(dragged);

    await user.click(reset);
    await waitFor(() => {
      expect(placeOf("pve-01")).toBe(laidOut);
    });
  });
});
