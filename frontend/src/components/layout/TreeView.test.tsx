import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import { listOf, stubApi } from "@/test/fetch-stub";
import { renderWithProviders } from "@/test/test-utils";
import { fill } from "@/test/user";
import { useSidebarStore } from "@/stores/sidebar-store";
import { TreeView } from "./TreeView";

afterEach(() => {
  vi.unstubAllGlobals();
});

// The box narrows the rows the tree has loaded, holds a collapsed branch open
// over a match, and leaves the stored expansion as it was for when it clears.
describe("TreeView filter", () => {
  it("narrows the tree to matching rows and restores it when cleared", async () => {
    stubApi({
      "/api/v1/clusters": listOf([makeCluster({ name: "cluster01" })]),
      "/api/v1/clusters/c1/nodes": listOf([
        makeNode({ id: "n1", name: "pve-01" }),
        makeNode({ id: "n2", name: "pve-02" }),
      ]),
      "/api/v1/clusters/c1/vms": listOf([
        makeVM({ id: "v1", node_id: "n1", vmid: 101, name: "linux01" }),
        makeVM({ id: "v2", node_id: "n2", vmid: 102, name: "win01" }),
      ]),
    });
    // The cluster is open, its nodes are not.
    useSidebarStore.setState({
      perspective: "hosts",
      expandedNodes: new Set(["cluster:c1"]),
    });
    renderWithProviders(<TreeView />);
    expect(await screen.findByText("pve-02")).toBeInTheDocument();
    expect(screen.queryByText("win01")).not.toBeInTheDocument();

    const box = screen.getByRole("textbox", {
      name: "Filter the tree by name",
    });
    fill(box, "WIN");
    expect(screen.getByText("win01")).toBeInTheDocument();
    expect(screen.queryByText("pve-01")).not.toBeInTheDocument();
    // A branch the filter holds open ignores its chevron: the click would
    // change nothing on screen and flip what clearing the box restores.
    fireEvent.click(
      screen.getByRole("button", { name: "Collapse node pve-02" }),
    );
    expect(screen.getByText("win01")).toBeInTheDocument();

    // Nothing matches: the cluster's own row goes too.
    fill(box, "nas01");
    expect(screen.queryByText("cluster01")).not.toBeInTheDocument();

    fill(box, "");
    expect(screen.getByText("pve-01")).toBeInTheDocument();
    expect(screen.queryByText("win01")).not.toBeInTheDocument();
    expect(useSidebarStore.getState().expandedNodes).toEqual(
      new Set(["cluster:c1"]),
    );
  });
});
