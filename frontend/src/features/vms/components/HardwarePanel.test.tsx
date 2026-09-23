import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { HardwarePanel } from "./HardwarePanel";

// The transport is mocked rather than the hooks, so the real useDetachDisk and
// useSetVMConfig run and each test can assert the exact request that leaves
// the browser — which is the claim a destructive action's test has to make.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: { get: vi.fn(), list: vi.fn(), post: vi.fn(), put: vi.fn() },
  };
});

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const VM = "d0000000-0000-4000-8000-000000000001";
const CONFIG_URL = `/api/v1/clusters/${CLUSTER}/vms/${VM}/config`;
const DETACH_URL = `/api/v1/clusters/${CLUSTER}/vms/${VM}/disks/detach`;
const STORAGE_URL = `/api/v1/clusters/${CLUSTER}/storage`;

// The live disk and the unused one sit on DIFFERENT storages, so an assertion
// naming the unused volume cannot be satisfied by the live disk's.
const UNUSED_VOLUME = "store02:vm-101-disk-1";

// A VM as a disk move without "delete source" leaves it: the moved disk, and
// the source volume parked as unused0. vga is spelled out because the panel
// reads a config without it as already changed, and one test needs a panel
// that opens clean.
function vmConfig(): Record<string, unknown> {
  return {
    digest: "aabbccddeeff00112233445566778899aabbccdd",
    cores: 2,
    memory: 2048,
    vga: "std",
    scsi0: "store01:vm-101-disk-0,size=32G",
    efidisk0: "store01:vm-101-disk-2,efitype=4m,size=4M",
    unused0: UNUSED_VOLUME,
  };
}

// The one storage the Add Disk form can offer.
const imageStorage = {
  id: "e0000000-0000-4000-8000-000000000001",
  storage: "store01",
  type: "lvmthin",
  content: "images,rootdir",
  enabled: true,
  active: true,
};

// What Proxmox reports once unused0 is deleted: the key, and its volume, gone.
function serverDropsUnused0() {
  serverConfig = Object.fromEntries(
    Object.entries(serverConfig).filter(([key]) => key !== "unused0"),
  );
  return Promise.resolve({ upid: "", status: "completed" });
}

let serverConfig: Record<string, unknown>;

const props = {
  clusterId: CLUSTER,
  vmId: VM,
  vmStatus: "stopped",
  nodeName: "pve-01",
};

/** The disk row whose key label is `key` — the element holding its buttons. */
function diskRow(key: string): HTMLElement {
  const rows = screen
    .getAllByText(key, { exact: true })
    .map((label) => label.parentElement)
    .filter(
      (row): row is HTMLElement =>
        row !== null &&
        within(row).queryByRole("button", { name: /remove/i }) !== null,
    );
  expect(rows).toHaveLength(1);
  const [row] = rows;
  if (!row) throw new Error(`no disk row for ${key}`);
  return row;
}

async function openDeleteDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    within(diskRow("unused0")).getByRole("button", { name: /remove/i }),
  );
  return screen.findByRole("alertdialog");
}

// One change of every staged kind — a disk and a device marked for removal, a
// new disk and a new device — each through the control a user would use. They
// are the four kinds of state Save applies that a config refetch does not
// re-read, so a test of "everything staged is cleared" has to stage all four.
async function stageOneOfEachKind(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
  );
  await user.click(
    within(diskRow("efidisk0")).getByRole("button", { name: /remove/i }),
  );
  await user.click(screen.getByRole("button", { name: "Add Disk" }));
  const addDiskForm = screen.getByText("Add New Disk").parentElement;
  if (!addDiskForm) throw new Error("Add Disk form not found");
  await user.selectOptions(
    within(addDiskForm).getByDisplayValue("Select..."),
    "store01",
  );
  await user.click(within(addDiskForm).getByRole("button", { name: "Add" }));
  await user.click(screen.getByRole("button", { name: /add device/i }));
  await user.click(
    await screen.findByRole("menuitem", { name: /serial port/i }),
  );
  await user.click(
    within(await screen.findByRole("dialog")).getByRole("button", {
      name: "Add",
    }),
  );
  // The removed EFI disk has no marker of its own; it reaches the tests
  // through Save's enabled state and the request Save sends.
  expect(screen.getByText(/marked for\s+removal/i)).toBeInTheDocument();
  expect(screen.getByText("New disks (created on save)")).toBeInTheDocument();
  expect(screen.getByText("Serial Ports (0)")).toBeInTheDocument();
}

function expectNothingStaged() {
  expect(screen.queryByText(/marked for\s+removal/i)).not.toBeInTheDocument();
  expect(
    screen.queryByText("New disks (created on save)"),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("Serial Ports (0)")).not.toBeInTheDocument();
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe("HardwarePanel unused disk removal", () => {
  beforeEach(() => {
    // reset, not clear: one test's post implementation must not answer the
    // next test's request.
    vi.resetAllMocks();
    serverConfig = vmConfig();
    mockedGet.mockImplementation((path: string) =>
      path === CONFIG_URL
        ? Promise.resolve({ ...serverConfig })
        : Promise.reject(new Error(`unexpected GET ${path}`)),
    );
    mockedList.mockImplementation((path: string) =>
      Promise.resolve(path === STORAGE_URL ? [imageStorage] : []),
    );
  });

  it("asks before deleting, naming the volume and saying it is permanent", async () => {
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    const dialog = await openDeleteDialog(user);

    expect(
      within(dialog).getByText("Delete unused disk unused0?"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(UNUSED_VOLUME)).toBeInTheDocument();
    expect(dialog).toHaveTextContent(/permanently delete/i);
    expect(dialog).toHaveTextContent(/from storage/i);
    expect(dialog).toHaveTextContent(/cannot be undone/i);
    // Opening the dialog is not the delete.
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("sends nothing when cancelled, and keeps the disk", async () => {
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    const dialog = await openDeleteDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPut).not.toHaveBeenCalled();
    expect(diskRow("unused0")).toBeInTheDocument();
  });

  it("sends exactly the detach request on confirm, then closes", async () => {
    mockedPost.mockImplementation(serverDropsUnused0);
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    const dialog = await openDeleteDialog(user);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Disk" }),
    );

    expect(mockedPost.mock.calls).toEqual([[DETACH_URL, { disk: "unused0" }]]);
    // Not smuggled into a config write alongside it.
    expect(mockedPut).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    });
  });

  it("holds the dialog while the delete is in flight", async () => {
    const pending = deferred<unknown>();
    mockedPost.mockReturnValue(pending.promise);
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    const dialog = await openDeleteDialog(user);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Disk" }),
    );

    const confirm = await within(dialog).findByRole("button", {
      name: "Deleting...",
    });
    expect(confirm).toBeDisabled();
    // Cancel cannot un-send a request, so it is not offered as if it could.
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(mockedPost).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve({ upid: "", status: "completed" });
      await pending.promise;
    });
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it("keeps a failure on screen, lets the user retry or leave, and does not carry it over", async () => {
    mockedPost.mockRejectedValue(
      new ApiClientError(409, {
        error: "conflict",
        message: "VM is locked (backup)",
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    let dialog = await openDeleteDialog(user);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Disk" }),
    );

    expect(
      await within(dialog).findByText("VM is locked (backup)"),
    ).toBeInTheDocument();
    expect(mockedPost.mock.calls).toEqual([[DETACH_URL, { disk: "unused0" }]]);

    // A retry is the same request again, from the same open dialog.
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Disk" }),
    );
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(2);
    });
    expect(mockedPost.mock.calls[1]).toEqual([DETACH_URL, { disk: "unused0" }]);
    expect(
      await within(dialog).findByText("VM is locked (backup)"),
    ).toBeInTheDocument();
    expect(mockedPut).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(diskRow("unused0")).toBeInTheDocument();

    dialog = await openDeleteDialog(user);
    expect(
      within(dialog).queryByText("VM is locked (backup)"),
    ).not.toBeInTheDocument();
  });

  it("still stages a live disk's removal for Save, without asking", async () => {
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(screen.getByText(/marked for\s+removal/i)).toBeInTheDocument();
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPut).not.toHaveBeenCalled();
  });

  // The delete changes the config, and the refetch re-populates every FIELD
  // from Proxmox while the STAGED changes outlive it. Before they were cleared
  // too, a memory edit vanished while a staged disk removal survived, and the
  // next Save sent the removal alone: half of what the user had prepared.
  it("discards every unsaved change after a delete, so a later Save cannot apply half of them", async () => {
    mockedPost.mockImplementation(serverDropsUnused0);
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");
    const save = screen.getByRole("button", { name: /save changes/i });
    // The fixture opens clean, so Save being disabled at the end is the
    // panel having nothing to send, not the panel never having had anything.
    expect(save).toBeDisabled();

    // An unsaved field edit...
    const memory = screen
      .getByText("Memory (MiB)")
      .parentElement?.querySelector("input");
    if (!memory) throw new Error("Memory input not found");
    await user.clear(memory);
    await user.type(memory, "8192");

    // ...and one change of every staged kind.
    await stageOneOfEachKind(user);
    expect(save).toBeEnabled();

    const dialog = await openDeleteDialog(user);
    expect(dialog).toHaveTextContent(
      /unsaved changes in this panel will be discarded/i,
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Disk" }),
    );
    await waitFor(() => {
      expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    });

    // The edit is gone, re-read from Proxmox as the dialog said it would be...
    await waitFor(() => {
      expect(memory).toHaveValue(2048);
    });
    // ...and so is everything staged, which leaves Save nothing to apply.
    expectNothingStaged();
    await waitFor(() => {
      expect(save).toBeDisabled();
    });
    expect(mockedPut).not.toHaveBeenCalled();
  });

  // Save and the delete clear the staged changes through one shared list, so
  // this pins Save's use of it — for every kind, since a Save that cleared
  // only the disk removals would pass a test that staged only a disk removal.
  it("clears every staged kind once Save succeeds", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");
    const save = screen.getByRole("button", { name: /save changes/i });
    expect(save).toBeDisabled();

    await stageOneOfEachKind(user);
    expect(save).toBeEnabled();
    await user.click(save);

    expect(mockedPut.mock.calls).toEqual([
      [
        CONFIG_URL,
        {
          fields: {
            delete: "efidisk0,scsi0",
            scsi1: "store01:32,format=qcow2",
            serial0: "socket",
          },
        },
      ],
    ]);
    // Settled first: Save is also disabled while its own request is in
    // flight, which would satisfy the check below without anything cleared.
    expect(await screen.findByText("Saved.")).toBeInTheDocument();
    expect(save).toBeDisabled();
    expectNothingStaged();
  });
});
