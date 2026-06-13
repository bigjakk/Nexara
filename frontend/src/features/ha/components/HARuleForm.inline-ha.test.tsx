import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { HARuleForm } from "./HARuleForm";
import type { VMResponse, NodeResponse } from "@/types/api";

const mockCreateHARule = vi.fn();
const mockCreateHAResource = vi.fn();
const mockUpdateHARule = vi.fn();
let mockHAResources: { data: { sid: string }[] | undefined; isSuccess: boolean } = {
  data: [],
  isSuccess: true,
};

vi.mock("@/features/ha/api/ha-queries", () => ({
  useCreateHARule: () => ({ mutateAsync: mockCreateHARule, isPending: false, error: null }),
  useUpdateHARule: () => ({ mutateAsync: mockUpdateHARule, isPending: false, error: null }),
  useCreateHAResource: () => ({ mutateAsync: mockCreateHAResource, isPending: false, error: null }),
  useHAGroups: () => ({ data: [] }),
  useHAResources: () => mockHAResources,
}));

function makeVM(vmid: number, name: string, type: "qemu" | "lxc" = "qemu"): VMResponse {
  return {
    id: `vm-${String(vmid)}`,
    cluster_id: "c1",
    node_id: "n1",
    vmid,
    name,
    type,
    status: "running",
    template: false,
  } as unknown as VMResponse;
}

function makeNode(name: string): NodeResponse {
  return {
    id: `node-${name}`,
    cluster_id: "c1",
    name,
    status: "online",
  } as unknown as NodeResponse;
}

/** Click the single checkbox inside the row/label that holds `el`. */
function checkboxIn(el: Element | null | undefined): HTMLElement {
  if (!(el instanceof HTMLElement)) throw new Error("container element not found");
  return within(el).getByRole("checkbox");
}

const vms = [makeVM(100, "web-1"), makeVM(101, "db-1")];
const nodes = [makeNode("pve1"), makeNode("pve2")];

describe("HARuleForm — inline HA management", () => {
  beforeEach(() => {
    mockHAResources = { data: [], isSuccess: true };
    mockCreateHARule.mockReset();
    mockCreateHARule.mockResolvedValue(undefined);
    mockCreateHAResource.mockReset();
    mockCreateHAResource.mockResolvedValue(undefined);
    mockUpdateHARule.mockReset();
    mockUpdateHARule.mockResolvedValue(undefined);
  });

  it("reveals inline HA-management controls when an unmanaged resource is selected", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <HARuleForm mode="create" clusterId="c1" allVMs={vms} allNodes={nodes} onSuccess={() => undefined} />,
    );

    // Nothing is offered until an unmanaged resource is picked.
    expect(screen.queryByText(/Add it to HA management/i)).not.toBeInTheDocument();

    await user.click(checkboxIn(screen.getByText("vm:100").closest("label")));

    expect(screen.getByText(/Add it to HA management/i)).toBeInTheDocument();
    expect(screen.getByText(/Requested State/i)).toBeInTheDocument();
    expect(screen.getByText(/Max Restart/i)).toBeInTheDocument();
  });

  it("creates the unmanaged resource before the rule on submit", async () => {
    const user = userEvent.setup();
    const onSuccess = vi.fn();
    renderWithProviders(
      <HARuleForm mode="create" clusterId="c1" allVMs={vms} allNodes={nodes} onSuccess={onSuccess} />,
    );

    await user.type(screen.getByPlaceholderText("my-rule"), "keep-here");
    await user.click(checkboxIn(screen.getByText("vm:100").closest("label")));
    await user.click(checkboxIn(screen.getByText("pve1").closest("tr")));

    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => { expect(mockCreateHAResource).toHaveBeenCalledTimes(1); });
    expect(mockCreateHAResource).toHaveBeenCalledWith(
      expect.objectContaining({ sid: "vm:100", state: "started", failback: 1 }),
    );
    expect(mockCreateHARule).toHaveBeenCalledWith(
      expect.objectContaining({ rule: "keep-here", type: "node-affinity", resources: "vm:100" }),
    );
    await waitFor(() => { expect(onSuccess).toHaveBeenCalled(); });
  });

  it("does not auto-create resources when 'Add to HA management' is unchecked", async () => {
    const user = userEvent.setup();
    const onSuccess = vi.fn();
    renderWithProviders(
      <HARuleForm mode="create" clusterId="c1" allVMs={vms} allNodes={nodes} onSuccess={onSuccess} />,
    );

    await user.type(screen.getByPlaceholderText("my-rule"), "no-manage");
    await user.click(checkboxIn(screen.getByText("vm:100").closest("label")));
    // Opt out of inline management.
    await user.click(checkboxIn(screen.getByText(/Add it to HA management/i).closest("label")));
    await user.click(checkboxIn(screen.getByText("pve1").closest("tr")));

    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => { expect(mockCreateHARule).toHaveBeenCalled(); });
    expect(mockCreateHAResource).not.toHaveBeenCalled();
  });
});
