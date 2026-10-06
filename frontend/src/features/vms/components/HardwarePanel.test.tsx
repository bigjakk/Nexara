import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { deferred } from "@/test/fake-server";
import { fill, setupUser } from "@/test/user";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { HardwarePanel } from "./HardwarePanel";

// The transport is mocked rather than the hooks, so the real useDetachDisk and
// useSetVMConfig run and each test can assert the exact request that leaves
// the browser — which is the claim a destructive action's test has to make.
vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// Not under test, and its injected stylesheet makes every getComputedStyle
// call (role queries, Radix Presence, user-event) match against its rules.
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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

let user: UserEvent;

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

/** The card of disk `key` — the element holding its Cache and Resize To. */
function diskCard(key: string): HTMLElement {
  const card = diskRow(key).parentElement;
  if (!card) throw new Error(`no disk card for ${key}`);
  return card;
}

async function openDeleteDialog() {
  await user.click(
    within(diskRow("unused0")).getByRole("button", { name: /remove/i }),
  );
  return screen.findByRole("alertdialog");
}

// One change of every staged kind — a disk and a device marked for removal, a
// new disk and a new device — each through the control a user would use. They
// are the four kinds of state Save applies that a config refetch does not
// re-read, so a test of "everything staged is cleared" has to stage all four.
async function stageOneOfEachKind() {
  await user.click(
    within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
  );
  await user.click(
    within(diskRow("efidisk0")).getByRole("button", { name: /remove/i }),
  );
  await user.click(screen.getByRole("button", { name: "Add Disk" }));
  const addDiskForm = screen.getByText("Add New Disk").parentElement;
  if (!addDiskForm) throw new Error("Add Disk form not found");
  fill(within(addDiskForm).getByDisplayValue("Select..."), "store01");
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

beforeEach(() => {
  user = setupUser();
  // reset, not clear: one test's post implementation must not answer the
  // next test's request.
  vi.resetAllMocks();
  serverConfig = vmConfig();
  serveConfig();
});

describe("HardwarePanel unused disk removal", () => {
  it("asks before deleting, naming the volume and saying it is permanent, sends nothing when cancelled, and still stages a live disk's removal for Save without asking", async () => {
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    const dialog = await openDeleteDialog();

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

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPut).not.toHaveBeenCalled();
    expect(diskRow("unused0")).toBeInTheDocument();

    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(screen.getByText(/marked for\s+removal/i)).toBeInTheDocument();
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("sends exactly the detach request, keeps a failure on screen to retry or leave and does not carry it over, holds the dialog while the delete is in flight, and closes on success", async () => {
    const failure = new ApiClientError(409, {
      error: "conflict",
      message: "VM is locked (backup)",
    });
    const pending = deferred<unknown>();
    mockedPost
      .mockRejectedValueOnce(failure)
      .mockRejectedValueOnce(failure)
      .mockReturnValueOnce(pending.promise);
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");

    let dialog = await openDeleteDialog();
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

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(diskRow("unused0")).toBeInTheDocument();

    dialog = await openDeleteDialog();
    expect(
      within(dialog).queryByText("VM is locked (backup)"),
    ).not.toBeInTheDocument();

    // Once more, held in flight.
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
    expect(mockedPost).toHaveBeenCalledTimes(3);

    await act(async () => {
      void serverDropsUnused0();
      pending.resolve({ upid: "", status: "completed" });
      await pending.promise;
    });
    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    });
    expect(mockedPost.mock.calls).toEqual([
      [DETACH_URL, { disk: "unused0" }],
      [DETACH_URL, { disk: "unused0" }],
      [DETACH_URL, { disk: "unused0" }],
    ]);
    // Not smuggled into a config write alongside it.
    expect(mockedPut).not.toHaveBeenCalled();
  });

  // The delete changes the config, and the refetch re-populates every FIELD
  // from Proxmox while the STAGED changes outlive it. Before they were cleared
  // too, a memory edit vanished while a staged disk removal survived, and the
  // next Save sent the removal alone: half of what the user had prepared.
  it("discards every unsaved change after a delete, so a later Save cannot apply half of them", async () => {
    mockedPost.mockImplementation(serverDropsUnused0);
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");
    const save = screen.getByText("Save Changes", { selector: "button" });
    // The fixture opens clean, so Save being disabled at the end is the
    // panel having nothing to send, not the panel never having had anything.
    expect(save).toBeDisabled();

    // An unsaved field edit...
    const memory = screen
      .getByText("Memory (MiB)")
      .parentElement?.querySelector("input");
    if (!memory) throw new Error("Memory input not found");
    fill(memory, "8192");

    // ...and one change of every staged kind.
    await stageOneOfEachKind();
    expect(save).toBeEnabled();

    const dialog = await openDeleteDialog();
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
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("unused0");
    const save = screen.getByText("Save Changes", { selector: "button" });
    expect(save).toBeDisabled();

    await stageOneOfEachKind();
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
function moveNet0ToVmbr1() {
  fill(control(nicCard("net0"), "Bridge"), "vmbr1");
}

async function renderPanel(config: Record<string, unknown>) {
  serverConfig = config;
  renderWithProviders(<HardwarePanel {...props} />);
  // The NIC cards render from the edit state the config populates, in the
  // same pass as the snapshot Save compares against. Once net0's MAC is on
  // screen, Save's state is about the config, not a panel still loading.
  await screen.findByText(MAC0);
  return screen.getByText("Save Changes", { selector: "button" });
}

async function saveAndGetFields(save: HTMLElement) {
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
    mockedPut.mockResolvedValue({ status: "ok" });
  });

  // The panel opens clean with NICs carrying options it has no control for and
  // no vga line: either would make it dirty if it were rebuilt from the fields.
  it("sends only what was edited: a NIC as stored but for the edited option, and vga as picked, and not the other NIC", async () => {
    const save = await renderPanel(nicVM());
    expect(save).toBeDisabled();
    const vga = control(document.body, "VGA");
    // No vga line reads as Proxmox's default, not as a type it may not be.
    expect(vga).toHaveValue("");

    moveNet0ToVmbr1();
    fill(vga, "qxl");

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { net0: NET0_ON_VMBR1, vga: "qxl" } }],
    ]);
  });

  it("keeps the vga options it has no control for when the type changes", async () => {
    const save = await renderPanel(nicVM({ vga: "std,clipboard=vnc" }));
    expect(save).toBeDisabled();

    fill(control(document.body, "VGA"), "qxl");

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { vga: "qxl,clipboard=vnc" } }],
    ]);
  });

  it("deletes vga when it is set back to the default", async () => {
    const save = await renderPanel(nicVM({ vga: "qxl" }));
    const vga = control(document.body, "VGA");
    expect(vga).toHaveValue("qxl");

    fill(vga, "");

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { delete: "vga" } }],
    ]);
  });

  // The "(current)" option comes from the STORED value, which stands here for
  // one newer than the lists (Proxmox's own enums today). It shows as the
  // value, so the panel opens clean, and picking another does not take it out
  // of the list: it can be picked back, and is then no change at all. React
  // would otherwise show the FIRST option of a select whose value matches none.
  it("keeps a stored value it has no option for as the current one: shown on open, offered after another is picked, and never sent with another edit", async () => {
    const save = await renderPanel(
      nicVM({
        net2: "pcnet=02:00:00:00:00:03,bridge=vmbr0",
        vga: "qxl2",
        keyboard: "ko",
        audio0: "device=ich9-intel-hda,driver=none",
        vmstatestorage: "store09",
        watchdog: "model=diag288,action=inject-nmi",
      }),
    );
    expect(save).toBeDisabled();
    // The Advanced Options section starts closed.
    await user.click(screen.getByText("Advanced Options"));

    const stored: [
      name: string,
      select: () => HTMLElement,
      stored: string,
      other: string,
      otherLabel: string,
    ][] = [
      [
        "NIC model",
        () => control(nicCard("net2"), "Model"),
        "pcnet",
        "e1000",
        "Intel E1000",
      ],
      [
        "vga type",
        () => control(document.body, "VGA"),
        "qxl2",
        "std",
        "Standard VGA",
      ],
      [
        "keyboard layout",
        () => control(document.body, "Keyboard Layout"),
        "ko",
        "de",
        "de",
      ],
      [
        "VM state storage",
        () => control(document.body, "VM State Storage"),
        "store09",
        "store01",
        "store01 (lvmthin)",
      ],
      [
        "watchdog device",
        () => screen.getByRole("combobox", { name: "Watchdog device" }),
        "diag288",
        "ib700",
        "iBASE 700",
      ],
      [
        "watchdog action",
        () => screen.getByRole("combobox", { name: "Watchdog action" }),
        "inject-nmi",
        "reset",
        "Reset",
      ],
    ];
    for (const [name, select, was, other, otherLabel] of stored) {
      const el = select();
      expect(el, `${name} opens on the stored value`).toHaveValue(was);
      // The storage list arrives on a query of its own.
      await within(el).findByText(otherLabel);

      fill(el, other);
      expect(el).toHaveValue(other);
      expect(save, `${name} change is a change`).toBeEnabled();
      expect(
        within(el).getByRole("option", { name: `${was} (current)` }),
      ).toBeInTheDocument();

      fill(el, was);
      expect(el).toHaveValue(was);
      expect(save, `${name} picked back is no change`).toBeDisabled();
    }

    // Another edit carries none of them, and keeps the audio driver option it
    // has no control for beside the device it does.
    moveNet0ToVmbr1();
    fill(control(document.body, "Audio"), "AC97");

    expect(await saveAndGetFields(save)).toEqual([
      [
        CONFIG_URL,
        {
          fields: {
            net0: NET0_ON_VMBR1,
            audio0: "device=AC97,driver=none",
          },
        },
      ],
    ]);
  });

  // Proxmox refuses a key that is both set and deleted ("you can't use
  // '-net1' and -delete net1' at the same time", $update_vm_api in
  // src/PVE/API2/Qemu.pm), which fails the whole Save. A NIC and a disk, with a
  // resize on it, edited and then removed are sent as the removal alone, in one
  // Save beside the same edits on ones that are kept, so that the edits really
  // are sent when nothing removes them. A resize is a request of its own, sent
  // before the config write, and for a disk the same Save removes it would race
  // the delete.
  it("sends only the removal for a NIC or disk edited and then removed, and the edit for one that is kept", async () => {
    const save = await renderPanel(
      nicVM({
        scsi0: "store01:vm-101-disk-0,size=32G",
        scsi1: "store01:vm-101-disk-1,size=16G",
      }),
    );

    fill(control(nicCard("net1"), "Bridge"), "vmbr3");
    fill(control(nicCard("net0"), "Bridge"), "vmbr9");
    await user.click(
      within(nicCard("net0")).getByRole("button", { name: /remove/i }),
    );
    fill(control(diskCard("scsi0"), "Cache"), "writeback");
    fill(control(diskCard("scsi1"), "Cache"), "writeback");
    fill(control(diskCard("scsi1"), "Resize To"), "40G");
    await user.click(
      within(diskRow("scsi1")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(save)).toEqual([
      [
        CONFIG_URL,
        {
          fields: {
            net1: "e1000=02:00:00:00:00:02,bridge=vmbr3,firewall=on,queues=2",
            scsi0: "store01:vm-101-disk-0,size=32G,cache=writeback",
            delete: "net0,scsi1",
          },
        },
      ],
    ]);
    // Save issues a resize before the config write, so by the time that write
    // has been answered, one would have been sent.
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("sends a resize for a disk that is kept, and no config write for it", async () => {
    mockedPost.mockResolvedValue({ upid: "", status: "completed" });
    const save = await renderPanel(
      nicVM({ scsi0: "store01:vm-101-disk-0,size=32G" }),
    );

    fill(control(diskCard("scsi0"), "Resize To"), "40G");
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

  async function moveEarlierInBoot(label: string) {
    await user.click(
      screen.getByRole("button", {
        name: `Move ${label} earlier in the boot order`,
      }),
    );
  }

  async function removeScsi0() {
    await user.click(
      within(diskRow("scsi0")).getByRole("button", { name: /remove/i }),
    );
  }

  it("sends a reordered boot order when every device in it is kept", async () => {
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(SCSI1_BOOT);

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=scsi1;scsi0;net0" } }],
    ]);
  });

  it("sends no boot order when a reorder is undone by removing the disk it moved past", async () => {
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(SCSI1_BOOT);
    await removeScsi0();

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
  });

  it("sends a reordered boot order without the disk the same Save removes", async () => {
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(NET0_BOOT);
    await moveEarlierInBoot(NET0_BOOT);
    await removeScsi0();

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=net0;scsi1", delete: "scsi0" } }],
    ]);
  });

  it("sends a reordered boot order without the NIC the same Save removes", async () => {
    const save = await renderPanel(bootVM());

    await moveEarlierInBoot(SCSI1_BOOT);
    await user.click(
      within(nicCard("net0")).getByRole("button", { name: /remove/i }),
    );

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { boot: "order=scsi1;scsi0", delete: "net0" } }],
    ]);
  });

  it("sends no boot order for a ticked disk removed without a reorder", async () => {
    const save = await renderPanel(bootVM());

    await removeScsi0();
    // The removal leaves scsi0 ticked in the boot list: leaving it out of
    // the order is Save's doing, not the list's.
    expect(document.getElementById("boot-scsi0")).toBeChecked();

    expect(await saveAndGetFields(save)).toEqual([
      [CONFIG_URL, { fields: { delete: "scsi0" } }],
    ]);
  });
});

describe("HardwarePanel USB devices", () => {
  const USB_URL = `/api/v1/clusters/${CLUSTER}/nodes/pve-01/hardware/usb`;
  const MAPPINGS_URL = `/api/v1/clusters/${CLUSTER}/nodes/pve-01/usb-mappings`;

  beforeEach(() => {
    // A raw port passthrough, set in Proxmox by root; a mapped device; and a
    // SPICE port.
    serverConfig = {
      digest: "aabbccddeeff00112233445566778899aabbccdd",
      cores: 2,
      memory: 2048,
      scsi0: "store01:vm-101-disk-0,size=32G",
      usb0: "host=1-2",
      usb1: "mapping=usbdev01,usb3=1",
      usb2: "spice",
    };
    mockedList.mockImplementation((path: string) => {
      if (path === STORAGE_URL) return Promise.resolve([imageStorage]);
      if (path === USB_URL)
        return Promise.resolve([
          {
            busnum: 1,
            devnum: 3,
            port: "2",
            prodid: "5678",
            vendid: "1234",
            // The device names itself; the row shows it cleaned.
            product: "Example Serial‮ Adapter",
            manufacturer: "Example Corp",
            speed: "12",
            class: 0,
            usbpath: "2",
            level: 1,
          },
        ]);
      if (path === MAPPINGS_URL)
        return Promise.resolve([
          {
            id: "usbdev01",
            description: "Example Radio",
            map: ["id=abcd:ef01,node=pve-01"],
            errors: [],
          },
        ]);
      return Promise.resolve([]);
    });
    mockedPut.mockResolvedValue({ status: "ok" });
  });

  it("will not offer to remove a raw passthrough, and says why, but removes a mapped device on Save", async () => {
    renderWithProviders(<HardwarePanel {...props} />);
    await user.click(await screen.findByText("USB Devices (3)"));

    // The raw row has no button to find it by, so by its key's own row.
    const label = screen.getByText("usb0", { exact: true });
    const row = label.parentElement;
    if (!row) throw new Error("no row for usb0");
    // Named from the node's own USB list.
    expect(
      await within(row).findByText("Example Serial Adapter (1-2)"),
    ).toBeInTheDocument();
    // The reason in place of the action, as text — a disabled button's title
    // never shows — and nothing to press.
    expect(
      within(row).getByText(/only root@pam can remove it/),
    ).toBeInTheDocument();
    expect(within(row).queryAllByRole("button")).toEqual([]);

    // A mapping and a SPICE port can go: Proxmox checks Mapping.Use and
    // VM.Config.HWType for those, not root.
    expect(
      within(diskRow("usb1")).getByRole("button", { name: /remove/i }),
    ).toBeEnabled();
    expect(within(diskRow("usb1")).getByText("Mapped")).toBeInTheDocument();
    expect(within(diskRow("usb1")).getByText("usbdev01")).toBeInTheDocument();
    expect(
      within(diskRow("usb2")).getByRole("button", { name: /remove/i }),
    ).toBeEnabled();

    await user.click(
      within(diskRow("usb1")).getByRole("button", { name: /remove/i }),
    );
    await user.click(screen.getByText("Save Changes", { selector: "button" }));

    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "usb1" } }],
    ]);
  });

  // Each add is staged until Save, so the next dialog has to count the staged
  // ones as taken — otherwise the second lands on the first's slot and
  // silently replaces it. CD/DVD drives are staged apart from other devices,
  // so the slot count has to see them too, or the second drive replaces the
  // first.
  it("puts two devices of a kind added before a Save into two slots: USB devices and CD/DVD drives", async () => {
    renderWithProviders(<HardwarePanel {...props} />);
    await screen.findByText("USB Devices (3)");

    async function add(
      menuItem: RegExp,
      pick?: (dialog: HTMLElement) => Promise<void>,
    ) {
      await user.click(screen.getByRole("button", { name: /add device/i }));
      await user.click(await screen.findByRole("menuitem", { name: menuItem }));
      const dialog = await screen.findByRole("dialog");
      await pick?.(dialog);
      await user.click(within(dialog).getByRole("button", { name: "Add" }));
    }
    await add(/usb device/i, async (dialog) => {
      await user.click(
        within(dialog).getByRole("radio", { name: /mapped device/i }),
      );
      fill(await within(dialog).findByLabelText("Mapping"), "usbdev01");
    });
    await add(/usb device/i, async (dialog) => {
      await user.click(
        within(dialog).getByRole("radio", { name: /spice port/i }),
      );
    });
    await add(/cd\/dvd drive/i);
    await add(/cd\/dvd drive/i);
    await user.click(screen.getByText("Save Changes", { selector: "button" }));

    expect(mockedPut.mock.calls).toEqual([
      [
        CONFIG_URL,
        {
          fields: {
            usb3: "mapping=usbdev01,usb3=1",
            usb4: "spice,usb3=1",
            ide0: "none,media=cdrom",
            ide1: "none,media=cdrom",
          },
        },
      ],
    ]);
  });
});

describe("HardwarePanel PCI devices", () => {
  const PCI_URL = `/api/v1/clusters/${CLUSTER}/nodes/pve-01/hardware/pci`;

  beforeEach(() => {
    // A raw passthrough, set in Proxmox by root; a mapped device; and a
    // mapped device with a ROM file, which only root may set or remove.
    serverConfig = {
      digest: "aabbccddeeff00112233445566778899aabbccdd",
      cores: 2,
      memory: 2048,
      scsi0: "store01:vm-101-disk-0,size=32G",
      hostpci0: "01:00,x-vga=1",
      hostpci1: "mapping=pcidev01,pcie=1",
      hostpci2: "mapping=pcidev02,romfile=vbios.bin",
    };
    mockedList.mockImplementation((path: string) => {
      if (path === STORAGE_URL) return Promise.resolve([imageStorage]);
      if (path === PCI_URL)
        return Promise.resolve([
          {
            id: "0000:01:00.0",
            class: "0x030000",
            // The device names itself; the row shows it cleaned.
            device_name: "Example‮ GPU",
            vendor_name: "Example Corp",
            device: "0x5678",
            vendor: "0x1234",
            iommugroup: 14,
          },
        ]);
      return Promise.resolve([]);
    });
    mockedPut.mockResolvedValue({ status: "ok" });
  });

  /** A row with no button to find it by: its key's own row. */
  function rowOf(key: string): HTMLElement {
    const row = screen.getByText(key, { exact: true }).parentElement;
    if (!row) throw new Error(`no row for ${key}`);
    return row;
  }

  it("will not offer to remove a raw passthrough or a ROM file, and says why, removes a mapped device on Save, and offers PCIe for a q35 machine type staged but not yet saved", async () => {
    renderWithProviders(<HardwarePanel {...props} />);
    await user.click(await screen.findByText("PCI Devices (3)"));

    // Named from the node's own PCI list — the whole device by function 0,
    // the domain qemu-server takes as optional filled in.
    expect(
      await within(rowOf("hostpci0")).findByText("Example GPU (01:00)"),
    ).toBeInTheDocument();
    expect(
      within(rowOf("hostpci0")).getByText(
        /Passed through directly; only root@pam can remove it/,
      ),
    ).toBeInTheDocument();
    expect(within(rowOf("hostpci0")).queryAllByRole("button")).toEqual([]);

    expect(
      within(rowOf("hostpci2")).getByText(
        /Uses a ROM file; only root@pam can remove it/,
      ),
    ).toBeInTheDocument();
    expect(within(rowOf("hostpci2")).queryAllByRole("button")).toEqual([]);

    // A mapping without a ROM file can go: Proxmox checks Mapping.Use and
    // VM.Config.HWType for it, not root.
    expect(
      within(diskRow("hostpci1")).getByRole("button", { name: /remove/i }),
    ).toBeEnabled();
    expect(within(diskRow("hostpci1")).getByText("Mapped")).toBeInTheDocument();
    expect(
      within(diskRow("hostpci1")).getByText("pcidev01"),
    ).toBeInTheDocument();

    await user.click(
      within(diskRow("hostpci1")).getByRole("button", { name: /remove/i }),
    );
    await user.click(screen.getByText("Save Changes", { selector: "button" }));

    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "hostpci1" } }],
    ]);

    // The machine type is saved in the same write as a device added now, so
    // PCIe follows the staged one, not the one Proxmox has.
    fill(screen.getByDisplayValue("i440fx (Default)"), "q35");
    await user.click(screen.getByRole("button", { name: /add device/i }));
    await user.click(
      await screen.findByRole("menuitem", { name: /pci device/i }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/^PCIe/)).toBeEnabled();
    expect(within(dialog).getByLabelText(/^PCIe/)).toBeChecked();
  });
});
