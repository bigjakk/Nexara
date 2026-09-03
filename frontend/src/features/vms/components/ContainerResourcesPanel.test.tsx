import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { ContainerResourcesPanel } from "./ContainerResourcesPanel";

interface PanelState {
  config: Record<string, unknown>;
  saved: Record<string, unknown> | undefined;
}

const state = vi.hoisted<PanelState>(() => ({ config: {}, saved: undefined }));

vi.mock("../api/vm-queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/vm-queries")>();
  return {
    ...actual,
    useContainerConfig: () => ({
      data: state.config,
      isLoading: false,
      error: null,
    }),
    useSetResourceConfig: () => ({
      mutate: (vars: Record<string, unknown>) => {
        state.saved = vars;
      },
      isPending: false,
      isError: false,
      error: null,
    }),
  };
});

vi.mock("@/features/clusters/api/cluster-queries", async (importOriginal) => {
  const actual =
    await importOriginal<
      typeof import("@/features/clusters/api/cluster-queries")
    >();
  return { ...actual, useNodeBridges: () => ({ data: [] }) };
});

vi.mock("@/features/storage/api/storage-queries", async (importOriginal) => {
  const actual =
    await importOriginal<
      typeof import("@/features/storage/api/storage-queries")
    >();
  return { ...actual, useClusterStorage: () => ({ data: [] }) };
});

const defaultProps = {
  clusterId: "c1",
  ctId: "ct-1",
  ctStatus: "stopped",
  nodeName: "pve1",
};

describe("ContainerResourcesPanel unused volumes", () => {
  beforeEach(() => {
    state.saved = undefined;
    state.config = {
      hostname: "ct200",
      cores: 2,
      memory: 1024,
      rootfs: "ceph:vm-200-disk-0,size=8G",
      net0: "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:80:F0:95,ip=dhcp,type=veth",
      // What a move-volume without "delete source" leaves behind.
      unused0: "local-lvm:vm-200-disk-0",
    };
  });

  it("lists the volume a non-deleting move left behind", () => {
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    expect(screen.getByText("unused0")).toBeInTheDocument();
    expect(screen.getByText("local-lvm:vm-200-disk-0")).toBeInTheDocument();
  });

  it("saves the removal as a config delete", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    await user.click(screen.getByTitle("Remove volume"));
    expect(screen.getByText("removing")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /save/i }));
    expect(state.saved).toMatchObject({
      clusterId: "c1",
      resourceId: "ct-1",
      kind: "ct",
      fields: { delete: "unused0" },
    });
  });

  it("undoes a pending removal", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    await user.click(screen.getByTitle("Remove volume"));
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(screen.queryByText("removing")).not.toBeInTheDocument();
    expect(screen.getByTitle("Remove volume")).toBeInTheDocument();
  });

  it("omits the section when there is nothing unused", () => {
    state.config = {
      hostname: "ct200",
      rootfs: "ceph:vm-200-disk-0,size=8G",
    };
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    expect(screen.queryByText("Unused Volumes")).not.toBeInTheDocument();
  });
});

describe("ContainerResourcesPanel change tracking", () => {
  beforeEach(() => {
    state.saved = undefined;
    state.config = {
      hostname: "ct200",
      cores: 2,
      memory: 1024,
      rootfs: "ceph:vm-200-disk-0,size=8G",
      // type=veth is on every Proxmox container NIC and the form has no field
      // for it; if the rebuild drops it the panel is dirty before you touch it.
      net0: "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:80:F0:95,ip=dhcp,type=veth",
    };
  });

  it("reports no pending changes on an untouched container", () => {
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    expect(screen.queryByText(/\d+ changes?/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled();
  });

  it("keeps unmodelled NIC keys when saving an unrelated edit", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    // The panel's labels aren't tied to their inputs, so reach the field
    // through its label's container.
    const cores = screen
      .getByText("Cores")
      .parentElement?.querySelector("input");
    if (!cores) throw new Error("Cores input not found");
    await user.clear(cores);
    await user.type(cores, "4");
    await user.click(screen.getByRole("button", { name: /save/i }));

    const fields = state.saved?.["fields"] as
      | Record<string, string>
      | undefined;
    expect(fields?.["cores"]).toBe("4");
    // Only the edited field travels — the NIC is untouched, so it is not resent.
    expect(fields).not.toHaveProperty("net0");
  });
});
