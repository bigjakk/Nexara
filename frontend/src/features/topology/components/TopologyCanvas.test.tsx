import { describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import type { Node, NodeChange } from "@xyflow/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
import type { TopologyInput } from "../lib/topology-transform";
import { TopologyCanvas } from "./TopologyCanvas";

// The canvas, not React Flow: jsdom cannot drag a node (its canvas has no
// size, so React Flow auto-pans every frame), so the flow is a stand-in that
// keeps what the canvas hands it and the change handler it is given.
const flow = vi.hoisted(() => ({
  nodes: [] as Node[],
  onNodesChange: null as ((changes: NodeChange[]) => void) | null,
}));
vi.mock("@xyflow/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@xyflow/react")>()),
  ReactFlow: (p: {
    nodes: Node[];
    onNodesChange: (c: NodeChange[]) => void;
    children: ReactNode;
  }) => {
    flow.nodes = p.nodes;
    flow.onNodesChange = p.onNodesChange;
    return <div>{p.children}</div>;
  },
  Background: () => null,
  MiniMap: () => null,
  Controls: (p: { children: ReactNode }) => <div>{p.children}</div>,
  ControlButton: (p: { onClick: () => void; "aria-label": string }) => (
    <button onClick={p.onClick}>{p["aria-label"]}</button>
  ),
  useReactFlow: () => ({ fitView: () => Promise.resolve(true) }),
  useStore: () => false,
}));

function input(): TopologyInput {
  const cluster = makeCluster({ id: "c1", name: "cluster01" });
  return {
    clusters: [cluster],
    nodesByCluster: new Map([["c1", [makeNode({ id: "n1", name: "pve-01" })]]]),
    vmsByCluster: new Map(),
    storageByCluster: new Map(),
  };
}

const where = () => flow.nodes.find((n) => n.id === "host-n1")?.position;

describe("TopologyCanvas arrangement", () => {
  // Every refetch builds a new input and lays the graph out again, which put
  // a dragged node back within 30 s. It stays until Reset layout.
  it("keeps a dragged node in place over a re-layout until the layout is reset", () => {
    const props = {
      filters: { showVMs: false, showStorage: false, selectedClusterId: null },
      direction: "TB" as const,
      liveMetrics: new Map(),
      migrations: [],
      power: null,
    };
    const { rerender } = renderWithProviders(
      <TopologyCanvas input={input()} {...props} />,
    );
    const laidOut = where();
    const to = { x: 999, y: 777 };

    act(() => {
      flow.onNodesChange?.([
        { id: "host-n1", type: "position", position: to, dragging: true },
      ]);
      flow.onNodesChange?.([
        { id: "host-n1", type: "position", position: to, dragging: false },
      ]);
    });
    rerender(<TopologyCanvas input={input()} {...props} />);
    expect(where()).toEqual(to);

    fireEvent.click(screen.getByRole("button", { name: "Reset layout" }));
    expect(where()).toEqual(laidOut);
  });
});
