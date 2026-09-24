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
const RESIZE_URL = `/api/v1/clusters/${CLUSTER}/vms/${VM}/disks/resize`;
const STORAGE_URL = `/api/v1/clusters/${CLUSTER}/storage`;

// The live disk and the unused one sit on DIFFERENT storages, so an assertion
// naming the unused volume cannot be satisfied by the live disk's.
const UNUSED_VOLUME = "store02:vm-101-disk-1";

// A VM as a disk move without "delete source" leaves it: the moved disk, and
// the source volume parked as unused0. It has no vga line, and the panel opens
// clean without one (the "HardwarePanel dirty check" tests below pin that).
function vmConfig(): Record<string, unknown> {
  return {
    digest: "aabbccddeeff00112233445566778899aabbccdd",
    cores: 2,
    memory: 2048,
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

// Proxmox's side of every test: the config GET answers with serverConfig,
// any other GET fails, and every list but the storage list is empty.
function serveConfig() {
  mockedGet.mockImplementation((path: string) =>
    path === CONFIG_URL
      ? Promise.resolve({ ...serverConfig })
      : Promise.reject(new Error(`unexpected GET ${path}`)),
  );
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(path === STORAGE_URL ? [imageStorage] : []),
  );
}

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
    serveConfig();
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

// A NIC with net options the editor has no control for — queues, trunks, and
// a key newer than this code — beside ones it has, written out of print_net's
// sorted order, so a panel that rebuilt it from its fields, or re-sorted it,
// could not reproduce it.
const MAC0 = "02:00:00:00:00:01";
const NET0 = `virtio=${MAC0},bridge=vmbr0,queues=4,trunks=10;20;30,mtu=1500,rate=12.5,link_down=1,tag=5,future-opt=abc`;
// net0 as Save writes it once moveNet0ToVmbr1 has run: that segment alone
// changed.
const NET0_ON_VMBR1 = `virtio=${MAC0},bridge=vmbr1,queues=4,trunks=10;20;30,mtu=1500,rate=12.5,link_down=1,tag=5,future-opt=abc`;
// A second NIC the old builder rewrote too (firewall=on read as off, queues
// dropped), so "only the edited NIC is sent" has something to catch.
const NET1 = "e1000=02:00:00:00:00:02,bridge=vmbr2,firewall=on,queues=2";

function nicVM(extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    digest: "aabbccddeeff00112233445566778899aabbccdd",
    cores: 2,
    memory: 2048,
    net0: NET0,
    net1: NET1,
    ...extra,
  };
}

/** The card of NIC `key` — the element holding its controls. */
function nicCard(key: string): HTMLElement {
  const cards = screen
    .getAllByText(key, { exact: true })
    .map((label) => label.parentElement?.parentElement)
    .filter(
      (card): card is HTMLElement =>
        card != null && within(card).queryByText("Bridge") !== null,
    );
  expect(cards).toHaveLength(1);
  const [card] = cards;
  if (!card) throw new Error(`no NIC card for ${key}`);
  return card;
}

/** The input or select under the label `text`, inside `scope`. */
function control(scope: HTMLElement, text: string) {
  const el = within(scope)
    .getByText(text, { exact: true })
    .parentElement?.querySelector<HTMLInputElement | HTMLSelectElement>(
      "input, select",
    );
  if (!el) throw new Error(`no control labelled ${text}`);
  return el;
}

/** Edit net0's bridge to vmbr1, the edit NET0_ON_VMBR1 describes. */
async function moveNet0ToVmbr1(user: ReturnType<typeof userEvent.setup>) {
  const bridge = control(nicCard("net0"), "Bridge");
  await user.clear(bridge);
  await user.type(bridge, "vmbr1");
}

async function renderPanel(config: Record<string, unknown>) {
  serverConfig = config;
  renderWithProviders(<HardwarePanel {...props} />);
  // The NIC cards render from the edit state the config populates, in the
  // same pass as the snapshot Save compares against. Once net0's MAC is on
  // screen, Save's state is about the config, not a panel still loading.
  await screen.findByText(MAC0);
  return screen.getByRole("button", { name: /save changes/i });
}

async function saveAndGetFields(
  user: ReturnType<typeof userEvent.setup>,
  save: HTMLElement,
) {
  expect(save).toBeEnabled();
  await user.click(save);
  await waitFor(() => {
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });
  expect(await screen.findByText("Saved.")).toBeInTheDocument();
  return mockedPut.mock.calls;
}

describe("HardwarePanel dirty check", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    serveConfig();
    mockedPut.mockResolvedValue({ status: "ok" });
  });

  it.each([
    // Either the NICs or the missing vga line would make this one dirty...
    ["and no vga line", nicVM()],
    // ...and with vga spelled out, only the NICs could.
    ["and vga: std", nicVM({ vga: "std" })],
  ])(
    "opens clean with NICs carrying options it has no control for %s",
    async (_name, config) => {
      const user = userEvent.setup();
      const save = await renderPanel(config);
      expect(save).toBeDisabled();

      // Not disabled for some other reason: an edit enables it.
      await moveNet0ToVmbr1(user);
      expect(save).toBeEnabled();
    },
  );

  it("sends only the edited NIC, as stored but for the edited option", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM());

    await moveNet0ToVmbr1(user);

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { net0: NET0_ON_VMBR1 } }],
    ]);
  });

  it("sends only vga when only vga is edited", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM());
    const vga = control(document.body, "VGA");
    // No vga line reads as Proxmox's default, not as a type it may not be.
    expect(vga).toHaveValue("");

    await user.selectOptions(vga, "qxl");

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { vga: "qxl" } }],
    ]);
  });

  it("keeps the vga options it has no control for when the type changes", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM({ vga: "std,clipboard=vnc" }));
    expect(save).toBeDisabled();

    await user.selectOptions(control(document.body, "VGA"), "qxl");

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { vga: "qxl,clipboard=vnc" } }],
    ]);
  });

  it("deletes vga when it is set back to the default", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM({ vga: "qxl" }));
    const vga = control(document.body, "VGA");
    expect(vga).toHaveValue("qxl");

    await user.selectOptions(vga, "Default");

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "vga" } }],
    ]);
  });

  // React shows the FIRST option of a select whose value matches none, which
  // here is "Default" — for a VM that has a different, real type.
  it("shows a vga type it has no option for as the current value", async () => {
    const save = await renderPanel(nicVM({ vga: "qxl2" }));
    expect(control(document.body, "VGA")).toHaveValue("qxl2");
    expect(save).toBeDisabled();
  });

  it("shows a NIC model it has no option for as the current value", async () => {
    const save = await renderPanel(
      nicVM({ net2: "pcnet=02:00:00:00:00:03,bridge=vmbr0" }),
    );
    expect(control(nicCard("net2"), "Model")).toHaveValue("pcnet");
    expect(save).toBeDisabled();
  });

  it("opens clean with an audio driver it has no control for, and keeps it on a device edit", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ audio0: "device=ich9-intel-hda,driver=none" }),
    );
    expect(save).toBeDisabled();

    await user.selectOptions(control(document.body, "Audio"), "AC97");

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { audio0: "device=AC97,driver=none" } }],
    ]);
  });

  // The "(current)" option comes from the STORED value, so picking another
  // does not take it out of the list: it can be picked back, and is then no
  // change at all.
  it("lets a stored NIC model it has no option for be picked back after another", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ net2: "pcnet=02:00:00:00:00:03,bridge=vmbr0" }),
    );
    const model = control(nicCard("net2"), "Model");

    await user.selectOptions(model, "e1000");
    expect(save).toBeEnabled();
    await user.selectOptions(model, "pcnet");
    expect(model).toHaveValue("pcnet");
    expect(save).toBeDisabled();

    // Nor does it ride along with another edit.
    await moveNet0ToVmbr1(user);
    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { net0: NET0_ON_VMBR1 } }],
    ]);
  });

  it("lets a stored vga type it has no option for be picked back after another", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM({ vga: "qxl2" }));
    const vga = control(document.body, "VGA");

    await user.selectOptions(vga, "std");
    expect(save).toBeEnabled();
    await user.selectOptions(vga, "qxl2");
    expect(vga).toHaveValue("qxl2");
    expect(save).toBeDisabled();

    await moveNet0ToVmbr1(user);
    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { net0: NET0_ON_VMBR1 } }],
    ]);
  });

  // The Advanced Options section starts closed.
  async function openAdvancedOptions(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByText("Advanced Options"));
  }

  // "ko" stands for a layout newer than the list, which is what the
  // "(current)" option is for.
  it("lets a stored keyboard layout it has no option for be picked back after another", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM({ keyboard: "ko" }));
    await openAdvancedOptions(user);
    const keyboard = control(document.body, "Keyboard Layout");
    expect(keyboard).toHaveValue("ko");

    await user.selectOptions(keyboard, "de");
    expect(save).toBeEnabled();
    await user.selectOptions(keyboard, "ko");
    expect(keyboard).toHaveValue("ko");
    expect(save).toBeDisabled();

    await moveNet0ToVmbr1(user);
    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { net0: NET0_ON_VMBR1 } }],
    ]);
  });

  // The other selects' "(current)" options read the stored value too, so
  // each is still offered after another value is picked. The stored values
  // stand for ones newer than the lists (Proxmox's own enums today).
  it.each([
    [
      "VM state storage",
      { vmstatestorage: "store09" },
      () => control(document.body, "VM State Storage"),
      "store01",
      "store01 (lvmthin)",
      "store09",
    ],
    [
      "watchdog device",
      { watchdog: "model=diag288" },
      () => screen.getByRole("combobox", { name: "Watchdog device" }),
      "ib700",
      "iBASE 700",
      "diag288",
    ],
    [
      "watchdog action",
      { watchdog: "model=i6300esb,action=inject-nmi" },
      () => screen.getByRole("combobox", { name: "Watchdog action" }),
      "reset",
      "Reset",
      "inject-nmi",
    ],
  ])(
    "still offers a stored %s it has no option for after another is picked",
    async (_name, extra, select, other, otherLabel, stored) => {
      const user = userEvent.setup();
      await renderPanel(nicVM(extra));
      await openAdvancedOptions(user);
      const el = select();
      expect(el).toHaveValue(stored);
      // The storage list arrives on a query of its own.
      await within(el).findByRole("option", { name: otherLabel });

      await user.selectOptions(el, other);

      expect(el).toHaveValue(other);
      expect(
        within(el).getByRole("option", { name: `${stored} (current)` }),
      ).toBeInTheDocument();
    },
  );

  // Proxmox refuses a key that is both set and deleted ("you can't use
  // '-net1' and -delete net1' at the same time", $update_vm_api in
  // src/PVE/API2/Qemu.pm), which fails the whole Save. Each removal test
  // has a twin showing the same edit, not removed, really is sent.
  async function moveNet1ToVmbr3(user: ReturnType<typeof userEvent.setup>) {
    const bridge = control(nicCard("net1"), "Bridge");
    await user.clear(bridge);
    await user.type(bridge, "vmbr3");
  }

  it("sends a NIC edit when the NIC is kept", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM());

    await moveNet1ToVmbr3(user);

    expect(await saveAndGetFields(user, save)).toEqual([
      [
        CONFIG_URL,
        {
          fields: {
            net1: "e1000=02:00:00:00:00:02,bridge=vmbr3,firewall=on,queues=2",
          },
        },
      ],
    ]);
  });

  it("sends only the removal for a NIC edited and then removed", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(nicVM());

    await moveNet1ToVmbr3(user);
    await user.click(
      within(nicCard("net1")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "net1" } }],
    ]);
  });

  async function setScsi0CacheWriteback(
    user: ReturnType<typeof userEvent.setup>,
  ) {
    const card = diskRow("scsi0").parentElement;
    if (!card) throw new Error("no disk card for scsi0");
    await user.selectOptions(control(card, "Cache"), "writeback");
  }

  it("sends a disk edit when the disk is kept", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ scsi0: "store01:vm-101-disk-0,size=32G" }),
    );

    await setScsi0CacheWriteback(user);

    expect(await saveAndGetFields(user, save)).toEqual([
      [
        CONFIG_URL,
        { fields: { scsi0: "store01:vm-101-disk-0,size=32G,cache=writeback" } },
      ],
    ]);
  });

  it("sends only the removal for a disk edited and then removed", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ scsi0: "store01:vm-101-disk-0,size=32G" }),
    );

    await setScsi0CacheWriteback(user);
    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
  });

  // A resize is not part of the config write: Save sends it as a request of
  // its own, before that write. For a disk the same Save removes it would
  // race the delete, so it is not sent. The twin shows it is, for a disk
  // that is kept.
  async function resizeScsi0To40G(user: ReturnType<typeof userEvent.setup>) {
    const card = diskRow("scsi0").parentElement;
    if (!card) throw new Error("no disk card for scsi0");
    await user.type(control(card, "Resize To"), "40G");
  }

  it("sends a resize for a disk that is kept", async () => {
    mockedPost.mockResolvedValue({ upid: "", status: "completed" });
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ scsi0: "store01:vm-101-disk-0,size=32G" }),
    );

    await resizeScsi0To40G(user);
    expect(save).toBeEnabled();
    await user.click(save);

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost.mock.calls).toEqual([
      [RESIZE_URL, { disk: "scsi0", size: "40G" }],
    ]);
    expect(
      await screen.findByText("Disk resized successfully."),
    ).toBeInTheDocument();
    // A resize alone changes nothing the config write carries.
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("sends only the delete, and no resize, for a disk given a size and then removed", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(
      nicVM({ scsi0: "store01:vm-101-disk-0,size=32G" }),
    );

    await resizeScsi0To40G(user);
    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
    // Save issues a resize before the config write, so by the time that write
    // has been answered, one would have been sent.
    expect(mockedPost).not.toHaveBeenCalled();
  });

  // Proxmox refuses a boot order naming a device the same request deletes,
  // but only after it has written the deletes (and possibly other options)
  // to the VM's pending changes, which then apply later anyway. So a device
  // this Save removes is left out of the boot order it sends; and since
  // Proxmox drops a deleted device from the stored order by itself, the
  // order left over is compared with the stored one without it.
  function bootVM(): Record<string, unknown> {
    return nicVM({
      scsi0: "store01:vm-101-disk-0,size=32G",
      scsi1: "store01:vm-101-disk-1,size=16G",
      boot: "order=scsi0;scsi1;net0",
    });
  }
  const SCSI1_BOOT = "Disk (scsi1) — store01, 16G";
  const NET0_BOOT = "Network (net0) — vmbr0";

  async function moveEarlierInBoot(
    user: ReturnType<typeof userEvent.setup>,
    label: string,
  ) {
    await user.click(
      screen.getByRole("button", {
        name: `Move ${label} earlier in the boot order`,
      }),
    );
  }

  async function removeScsi0(user: ReturnType<typeof userEvent.setup>) {
    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );
  }

  it("sends a reordered boot order when every device in it is kept", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(user, SCSI1_BOOT);

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=scsi1;scsi0;net0" } }],
    ]);
  });

  it("sends no boot order when a reorder is undone by removing the disk it moved past", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(user, SCSI1_BOOT);
    await removeScsi0(user);

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
  });

  it("sends a reordered boot order without the disk the same Save removes", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(user, NET0_BOOT);
    await moveEarlierInBoot(user, NET0_BOOT);
    await removeScsi0(user);

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=net0;scsi1", delete: "scsi0" } }],
    ]);
  });

  it("sends a reordered boot order without the NIC the same Save removes", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(user, SCSI1_BOOT);
    await user.click(
      within(nicCard("net0")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=scsi1;scsi0", delete: "net0" } }],
    ]);
  });

  it("sends no boot order for a ticked disk removed without a reorder", async () => {
    const user = userEvent.setup();
    const save = await renderPanel(bootVM());

    await removeScsi0(user);
    // The removal leaves scsi0 ticked in the boot list: leaving it out of
    // the order is Save's doing, not the list's.
    expect(document.getElementById("boot-scsi0")).toBeChecked();

    expect(await saveAndGetFields(user, save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
  });
});
