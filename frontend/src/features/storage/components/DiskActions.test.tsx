import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { MoveDiskDialog } from "./DiskActions";

const moveState = vi.hoisted(() => ({
  vars: undefined as Record<string, unknown> | undefined,
}));

vi.mock("@/features/vms/api/vm-queries", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/features/vms/api/vm-queries")>();
  return {
    ...actual,
    useMoveDisk: () => ({
      mutate: (
        vars: Record<string, unknown>,
        opts?: { onSuccess?: (data: { upid: string }) => void },
      ) => {
        moveState.vars = vars;
        opts?.onSuccess?.({
          upid: "UPID:pve1:0001:0002:0003:qmmove:100:root@pam:",
        });
      },
      isPending: false,
      isError: false,
      error: null,
    }),
  };
});

const defaultProps = {
  clusterId: "c1",
  vmId: "vm-1",
  diskName: "scsi0",
  currentStorage: "local",
  currentFormat: "raw",
  storageOptions: [
    { storage: "local", type: "dir" },
    { storage: "nfs-store", type: "nfs" },
    { storage: "local-lvm", type: "lvmthin" },
  ],
};

async function openDialog() {
  const user = userEvent.setup();
  renderWithProviders(<MoveDiskDialog {...defaultProps} />);
  await user.click(screen.getByRole("button", { name: "Move" }));
  return user;
}

describe("MoveDiskDialog", () => {
  beforeEach(() => {
    moveState.vars = undefined;
  });

  it("excludes the storage the disk already lives on", async () => {
    await openDialog();
    const values = Array.from(
      screen.getByLabelText<HTMLSelectElement>("Target Storage").options,
    ).map((o) => o.value);
    // "local" is the current storage — Proxmox rejects moving a disk onto it.
    expect(values).toEqual(["", "nfs-store", "local-lvm"]);
  });

  it("defaults delete-source off and explains what is kept", async () => {
    await openDialog();
    expect(screen.getByLabelText(/delete source/i)).not.toBeChecked();
    expect(screen.getByText(/kept as an unused disk/i)).toBeInTheDocument();
  });

  it("enables the format choice for file-based targets", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    expect(screen.getByLabelText("Format")).toBeEnabled();
  });

  it("preselects the source format so an untouched move preserves it", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    // Sending no format would let PVE fall back to the storage default.
    expect(screen.getByLabelText<HTMLSelectElement>("Format").value).toBe(
      "raw",
    );
    await user.click(screen.getByRole("button", { name: "Move Disk" }));
    expect(moveState.vars).toMatchObject({ format: "raw" });
  });

  it("allows explicitly deferring to the storage default", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    await user.selectOptions(screen.getByLabelText("Format"), "");
    await user.click(screen.getByRole("button", { name: "Move Disk" }));
    expect(moveState.vars).toMatchObject({ format: "" });
  });

  it("disables the format choice for block-backed targets", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "local-lvm",
    );
    expect(screen.getByLabelText("Format")).toBeDisabled();
    expect(screen.getByText(/raw only/i)).toBeInTheDocument();
  });

  it("submits the chosen format, delete flag and bandwidth limit", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    await user.selectOptions(screen.getByLabelText("Format"), "qcow2");
    await user.type(screen.getByLabelText(/bandwidth limit/i), "51200");
    await user.click(screen.getByLabelText(/delete source/i));
    await user.click(screen.getByRole("button", { name: "Move Disk" }));

    expect(moveState.vars).toMatchObject({
      clusterId: "c1",
      vmId: "vm-1",
      disk: "scsi0",
      storage: "nfs-store",
      format: "qcow2",
      deleteOriginal: true,
      bwlimitKib: 51200,
    });
  });

  it("drops a stale format when the target cannot honor it", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    await user.selectOptions(screen.getByLabelText("Format"), "qcow2");
    // Switching to a block-backed target must not send format=qcow2, which
    // Proxmox would reject.
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "local-lvm",
    );
    await user.click(screen.getByRole("button", { name: "Move Disk" }));

    expect(moveState.vars).toMatchObject({ storage: "local-lvm", format: "" });
  });

  it("blocks a non-numeric bandwidth limit", async () => {
    const user = await openDialog();
    await user.selectOptions(
      screen.getByLabelText("Target Storage"),
      "nfs-store",
    );
    await user.type(screen.getByLabelText(/bandwidth limit/i), "-5");
    expect(screen.getByText(/whole number of KiB\/s/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Move Disk" })).toBeDisabled();
  });
});
