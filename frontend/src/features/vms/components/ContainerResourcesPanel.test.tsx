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
      unprivileged: 1,
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

  // Proxmox refuses an empty value for most of these, and the whole write with
  // it, and stores an empty features value as sent; absent, each reads as
  // unset, so a field cleared is deleted. And pve-startup-order takes the
  // order bare: read as a key, it opened the panel dirty and went from any
  // Save.
  it.each([
    [
      "keeps a bare startup order when a delay is edited",
      { startup: "1,up=30" },
      "Up Delay (s)",
      "45",
      { startup: "1,up=45" },
    ],
    [
      "deletes a startup cleared",
      { startup: "1" },
      "Startup Order",
      "",
      { delete: "startup" },
    ],
    [
      "deletes a CPU limit, the other numbers and the hostname cleared",
      { cpulimit: 2, cpuunits: 200, swap: 512 },
      ["Cores", "CPU Limit", "CPU Units", "Swap (MiB)", "Hostname"],
      "",
      { delete: "cores,cpulimit,cpuunits,swap,hostname" },
    ],
    [
      "deletes features once the last flag is unticked",
      { features: "nesting=1", unprivileged: 1 },
      "Nesting",
      "",
      { delete: "features" },
    ],
  ])("%s", async (_name, stored, labels, value, want) => {
    state.config = { ...state.config, ...stored };
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    const save = screen.getByRole("button", { name: /save/i });
    expect(save).toBeDisabled();

    for (const label of [labels].flat()) {
      const field = screen
        .getByText(label)
        .parentElement?.querySelector("input, button");
      if (!field) throw new Error(`${label} field not found`);
      if (field instanceof HTMLButtonElement) {
        await user.click(field);
      } else {
        await user.clear(field);
        if (value) await user.type(field, value);
      }
    }
    await user.click(save);

    expect(state.saved?.["fields"]).toEqual(want);
  });

  // Unset, cores and the CPU weight have no number to show and swap is 0, as
  // Proxmox runs them; memory, which it can neither take empty nor delete,
  // holds a Save until it has a value.
  it("shows unset cores, CPU units and swap as Proxmox runs them, and holds Save while memory is cleared", async () => {
    delete state.config["cores"];
    const user = userEvent.setup();
    renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);
    const input = (label: string) => {
      const el = screen.getByText(label).parentElement?.querySelector("input");
      if (!el) throw new Error(`${label} input not found`);
      return el;
    };
    expect(input("Cores")).toHaveValue(null);
    expect(input("CPU Units")).toHaveValue(null);
    expect(input("Swap (MiB)")).toHaveValue(0);
    const save = screen.getByRole("button", { name: /save/i });
    expect(save).toBeDisabled();

    await user.clear(input("Memory (MiB)"));

    expect(
      screen.getByText(/cannot unset a container's memory/),
    ).toBeInTheDocument();
    expect(save).toBeDisabled();
  });

  // check_ct_modify_config_perm lets only root@pam change a privileged
  // container's features, or any but nesting on an unprivileged one, and a
  // token never is root@pam.
  it.each([
    ["an unprivileged", 1, true, /only root@pam change FUSE/],
    ["a privileged", 0, false, /privileged container/],
  ])(
    "locks the features a token cannot change on %s container",
    (_name, unprivileged, nestingOpen, note) => {
      state.config = { ...state.config, features: "nesting=1", unprivileged };
      renderWithProviders(<ContainerResourcesPanel {...defaultProps} />);

      for (const name of ["FUSE", "Keyctl", "Mknod"]) {
        expect(screen.getByRole("checkbox", { name })).toBeDisabled();
      }
      expect(screen.getByLabelText("Allowed Mount Types")).toBeDisabled();
      expect(screen.getByRole("checkbox", { name: "Nesting" })).toHaveProperty(
        "disabled",
        !nestingOpen,
      );
      expect(screen.getByText(note)).toBeInTheDocument();
    },
  );

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
