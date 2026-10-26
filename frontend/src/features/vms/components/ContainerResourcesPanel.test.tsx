import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQuery } from "@tanstack/react-query";
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
    // A real query, seeded so the data is there on the first render: the
    // panel also reads it straight from the cache when it checks a Save.
    useContainerConfig: (clusterId: string, ctId: string) =>
      useQuery({
        queryKey: actual.containerConfigKey(clusterId, ctId),
        queryFn: () => state.config,
        initialData: state.config,
      }),
    useSetResourceConfig: () => ({
      mutateAsync: (vars: Record<string, unknown>) => {
        state.saved = vars;
        return Promise.resolve({ status: "ok" });
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
      net0: "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth",
      // What a move-volume without "delete source" leaves behind.
      unused0: "local-lvm:vm-200-disk-0",
    };
  });

  it("lists the volume a non-deleting move left behind", () => {
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    expect(screen.getByText("unused0")).toBeInTheDocument();
    expect(screen.getByText("local-lvm:vm-200-disk-0")).toBeInTheDocument();
  });

  // Saving a removal, and the confirmation that comes first, are tested in
  // ContainerResourcesPanel.volume-delete.test.tsx against the request itself.

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
      net0: "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth",
    };
  });

  it("reports no pending changes on an untouched container", () => {
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    expect(screen.queryByText(/\d+ changes?/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled();
  });

  // Proxmox stores features as sent, in any order and with keys the form has
  // no field for. Rebuilt from its five fields, such a value opened the panel
  // dirty and lost force_rw_sys, so every Save sent a feature change that
  // Proxmox lets only root@pam make. Nesting is the one a token may change.
  it("opens clean on features with a key it has no field for, and keeps it when Nesting is ticked", async () => {
    state.config = {
      ...state.config,
      features: "keyctl=1,force_rw_sys=1",
    };
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    const save = screen.getByRole("button", { name: /save/i });
    expect(save).toBeDisabled();

    const nesting = screen
      .getByText("Nesting")
      .parentElement?.querySelector("button");
    if (!nesting) throw new Error("Nesting checkbox not found");
    await user.click(nesting);
    await user.click(save);

    expect(state.saved?.["fields"]).toEqual({
      features: "keyctl=1,force_rw_sys=1,nesting=1",
    });
  });

  // pve-startup-order takes the order bare and refuses an empty value: a bare
  // order read as a key opened the panel dirty and went from any Save, and a
  // startup cleared was sent as "" and refused with the whole write.
  it.each([
    [
      "keeps a bare startup order when a delay is edited",
      "1,up=30",
      "Up Delay (s)",
      "45",
      { startup: "1,up=45" },
    ],
    [
      "deletes a startup cleared",
      "1",
      "Startup Order",
      "",
      { delete: "startup" },
    ],
  ])("%s", async (_name, stored, label, value, want) => {
    state.config = { ...state.config, startup: stored };
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    const save = screen.getByRole("button", { name: /save/i });
    expect(save).toBeDisabled();

    const input = screen.getByText(label).parentElement?.querySelector("input");
    if (!input) throw new Error(`${label} input not found`);
    await user.clear(input);
    if (value) await user.type(input, value);
    await user.click(save);

    expect(state.saved?.["fields"]).toEqual(want);
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
