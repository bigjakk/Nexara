import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { HARuleForm } from "./HARuleForm";
import type { VMResponse, NodeResponse } from "@/types/api";

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
    cpus: 1,
    cpu_usage: 0,
    mem_used: 0,
    mem_total: 0,
    disk_used: 0,
    disk_total: 0,
    uptime: 0,
    tags: "",
    created_at: "",
    updated_at: "",
  } as unknown as VMResponse;
}

function makeNode(name: string): NodeResponse {
  return {
    id: `node-${name}`,
    cluster_id: "c1",
    name,
    status: "online",
    cpu_count: 4,
    cpu_usage: 0,
    mem_used: 0,
    mem_total: 0,
    disk_total: 0,
    uptime: 0,
    pve_version: "",
    ssl_fingerprint: "",
    created_at: "",
    updated_at: "",
  } as unknown as NodeResponse;
}

describe("HARuleForm", () => {
  const vms = [makeVM(100, "web-1"), makeVM(101, "db-1")];
  const nodes = [makeNode("pve1"), makeNode("pve2")];

  it("renders node-affinity fields by default in create mode", () => {
    renderWithProviders(
      <HARuleForm
        mode="create"
        clusterId="c1"
        allVMs={vms}
        allNodes={nodes}
        onSuccess={() => undefined}
      />,
    );

    // Node-affinity-specific
    expect(screen.getByText(/Nodes & Priorities/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Strict/i)).toBeInTheDocument();
    // Resource-affinity-specific should NOT be present
    expect(screen.queryByText(/Positive \(keep together\)/i)).not.toBeInTheDocument();
  });

  it("switches to resource-affinity fields when type changes", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <HARuleForm
        mode="create"
        clusterId="c1"
        allVMs={vms}
        allNodes={nodes}
        onSuccess={() => undefined}
      />,
    );

    // The Type Select is the only combobox in initial create-mode render.
    const [typeTrigger] = screen.getAllByRole("combobox");
    if (!typeTrigger) throw new Error("Type select not rendered");
    await user.click(typeTrigger);
    await user.click(screen.getByRole("option", { name: /Resource Affinity/i }));

    // Now resource-affinity-specific should appear and node-affinity should disappear
    expect(screen.getAllByText(/Positive \(keep together\)/i).length).toBeGreaterThan(0);
    expect(screen.queryByText(/Nodes & Priorities/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Strict/i)).not.toBeInTheDocument();
  });

  it("flags an invalid rule name (with spaces) before submit and disables Create", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <HARuleForm
        mode="create"
        clusterId="c1"
        allVMs={vms}
        allNodes={nodes}
        onSuccess={() => undefined}
      />,
    );

    const nameInput = screen.getByPlaceholderText("my-rule");
    await user.type(nameInput, "test rule");

    expect(screen.getByText(/no spaces/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Create$/i })).toBeDisabled();

    // A valid name clears the error.
    await user.clear(nameInput);
    await user.type(nameInput, "test-rule");
    expect(screen.queryByText(/no spaces/i)).not.toBeInTheDocument();
  });

  it("filters the resource list via the search box", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <HARuleForm
        mode="create"
        clusterId="c1"
        allVMs={vms}
        allNodes={nodes}
        onSuccess={() => undefined}
      />,
    );

    // Both VMs are visible initially.
    expect(screen.getByText("web-1")).toBeInTheDocument();
    expect(screen.getByText("db-1")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText(/Search by name or ID/i), "web");

    expect(screen.getByText("web-1")).toBeInTheDocument();
    expect(screen.queryByText("db-1")).not.toBeInTheDocument();
  });

  it("locks rule name and type in edit mode", () => {
    renderWithProviders(
      <HARuleForm
        mode="edit"
        clusterId="c1"
        allVMs={vms}
        allNodes={nodes}
        rule={{
          rule: "my-rule",
          type: "node-affinity",
          resources: "vm:100",
          nodes: "pve1:50",
          strict: 1,
          disable: 0,
        }}
        onSuccess={() => undefined}
      />,
    );

    const nameInput = screen.getByDisplayValue("my-rule");
    expect(nameInput).toBeDisabled();
    // Disable toggle should be visible in edit mode
    expect(screen.getByLabelText(/Disabled/i)).toBeInTheDocument();
  });
});
